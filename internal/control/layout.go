package control

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"gorex/internal/rex"
)

// workspace is the id of the one workspace, this machine.
const workspace = "w"

// bridge is the state of a bridge: one goroutine runs it (Run), but for
// out, which the attaches send to too.
type bridge struct {
	c     *rex.Client
	hello rex.Hello
	// owner is the bridge's size lock owner, and id its connection id.
	owner string
	id    uint64

	out           chan []byte
	quit, flushed chan struct{}

	// infos are the server's sessions, as of etag, and layout the app's
	// layout, as the server last gave it.
	infos  map[string]rex.SessionInfo
	etag   string
	layout []byte
	// tabs are the layout's tabs with a session of this machine, active
	// the id of the one the app shows, nextTab the last id's number.
	tabs    []*tab
	active  string
	nextTab int
	// sizes are the sizes rootshell gave the tabs, by id; owned the tabs
	// rootshell holds, whose sessions the bridge locked, those in locked.
	sizes  map[string][2]int
	owned  map[string]bool
	locked map[string]bool
	// shape is the topology rootshell was last told, and told and ctl the
	// layouts and controllers of its tabs.
	shape string
	told  map[string]string
	ctl   map[string]controller

	attaches   map[string]*attach
	nextAttach int

	// picked is the pane rootshell focused last in each tab, by tab id,
	// which it shows focused over the desktop's; anchor is the tab it
	// focused or claimed last, which a new tab goes after.
	picked map[string]string
	anchor string
	// jobs are the layout commands for the app, one at a time (work), and
	// done those it answered, to finish (finish); waiting is the one the
	// app made that the bridge waits to see (check).
	jobs, done chan *job
	waiting    *job
	// subscribed tells that the server tells of its changes.
	subscribed bool
}

// tab is a tab of the layout, without the panes of remote hosts: zoom
// tells that its focused pane fills it.
type tab struct {
	id, name string
	root     *node
	focus    string // the focused pane's session
	zoom     bool
}

// node is a pane, of session sid, or a split: vertical stacks a above b,
// else a is left of b; ratio is a's share.
type node struct {
	sid      string
	vertical bool
	ratio    float64
	a, b     *node
}

func (n *node) sids() []string {
	if n.sid != "" {
		return []string{n.sid}
	}
	return append(n.a.sids(), n.b.sids()...)
}

func (n *node) shape() string {
	if n.sid != "" {
		return n.sid
	}
	return fmt.Sprintf("(%v %s %s)", n.vertical, n.a.shape(), n.b.shape())
}

// The layout the app keeps on the server (main's savedLayout).
type savedLayout struct {
	Active int `json:"active"`
	Tabs   []struct {
		Name  string     `json:"name"`
		Root  *savedNode `json:"root"`
		Focus int        `json:"focus"`
		Zoom  bool       `json:"zoom"`
	} `json:"tabs"`
}

type savedNode struct {
	SID      string     `json:"sid"`
	Host     string     `json:"host"`
	Vertical bool       `json:"vertical"`
	Ratio    float64    `json:"ratio"`
	A        *savedNode `json:"a"`
	B        *savedNode `json:"b"`
}

// poll reads the server's sessions and layout (fetch).
func (b *bridge) poll() bool { return b.fetch(true, true) }

// fetch reads the server's sessions and layout, those asked for, makes the
// tabs of them, and keeps the locks of the tabs rootshell holds. It
// reports whether it read them: it changes nothing when it could not.
func (b *bridge) fetch(sessions, layout bool) bool {
	var list []rex.SessionInfo
	var etag string
	var changed bool
	var err error
	if sessions {
		if list, etag, changed, err = b.c.ListSince(b.etag); err != nil {
			return false
		}
	}
	l := json.RawMessage(b.layout)
	if layout {
		if l, err = b.c.Layout(); err != nil {
			return false
		}
	}
	if changed {
		b.etag = etag
		clear(b.infos)
		for _, in := range list {
			b.infos[in.ID] = in
		}
	}
	b.layout = l
	b.build()
	// A tab whose session another took is no longer rootshell's; one
	// whose new pane is not locked yet locks it.
	inOwned := map[string]bool{}
	for _, t := range b.tabs {
		if !b.owned[t.id] {
			continue
		}
		for _, sid := range t.root.sids() {
			inOwned[sid] = true
			if b.locked[sid] && b.infos[sid].SizeLock != b.owner {
				b.lose(t)
				break
			}
		}
		if b.owned[t.id] {
			b.lockTab(t)
		}
	}
	for sid := range b.locked {
		if !inOwned[sid] {
			b.c.UnlockSize(sid, b.owner, 0, 0)
			delete(b.locked, sid)
		}
	}
	for id := range b.owned {
		if b.tab(id) == nil {
			delete(b.owned, id)
		}
	}
	return true
}

// build makes the tabs of the layout, keeping each tab's id by the
// sessions it shares with a tab of before.
func (b *bridge) build() {
	var l savedLayout
	json.Unmarshal(b.layout, &l)
	seen := map[string]bool{}
	var tabs []*tab
	active := ""
	used := map[string]bool{}
	for i, st := range l.Tabs {
		var leaves []string // every pane's, "" for those left out
		var conv func(n *savedNode) *node
		conv = func(n *savedNode) *node {
			if n == nil {
				return nil
			}
			if n.A == nil || n.B == nil {
				_, known := b.infos[n.SID]
				if n.Host != "" || !known || seen[n.SID] {
					leaves = append(leaves, "")
					return nil
				}
				seen[n.SID] = true
				leaves = append(leaves, n.SID)
				return &node{sid: n.SID}
			}
			x, y := conv(n.A), conv(n.B)
			if x == nil || y == nil {
				return cmp.Or(x, y)
			}
			r := n.Ratio
			if r == 0 {
				r = 0.5
			}
			return &node{vertical: n.Vertical, ratio: min(max(r, 0.05), 0.95), a: x, b: y}
		}
		root := conv(st.Root)
		if root == nil {
			continue
		}
		t := &tab{name: st.Name, root: root, focus: root.sids()[0]}
		if st.Focus >= 0 && st.Focus < len(leaves) && leaves[st.Focus] != "" {
			t.focus = leaves[st.Focus]
			t.zoom = st.Zoom && len(root.sids()) > 1
		}
		best, most := "", 0
		for _, o := range b.tabs {
			if n := overlap(o, t); !used[o.id] && n > most {
				best, most = o.id, n
			}
		}
		if best == "" {
			b.nextTab++
			best = fmt.Sprintf("t%d", b.nextTab)
		}
		used[best] = true
		t.id = best
		// The pane zoomed is the desktop's focus; else rootshell's stays.
		if sid := b.picked[t.id]; !t.zoom && slices.Contains(root.sids(), sid) {
			t.focus = sid
		}
		tabs = append(tabs, t)
		if i == l.Active {
			active = t.id
		}
	}
	if active == "" && len(tabs) > 0 {
		active = tabs[0].id
	}
	b.tabs, b.active = tabs, active
}

func overlap(a, b *tab) int {
	n := 0
	for _, sid := range b.root.sids() {
		if slices.Contains(a.root.sids(), sid) {
			n++
		}
	}
	return n
}

func (b *bridge) tab(id string) *tab {
	for _, t := range b.tabs {
		if t.id == id {
			return t
		}
	}
	return nil
}

// tabOf returns the tab of session sid.
func (b *bridge) tabOf(sid string) *tab {
	for _, t := range b.tabs {
		if slices.Contains(t.root.sids(), sid) {
			return t
		}
	}
	return nil
}

// claim makes t rootshell's, which sizes it, and gives the desktop back
// the tab rootshell held before: rootshell shows one tab at a time.
func (b *bridge) claim(t *tab) {
	for _, o := range b.tabs {
		if o != t && b.owned[o.id] {
			b.lose(o)
		}
	}
	b.owned[t.id] = true
}

// lockTab locks the sessions of t, which rootshell holds, to the tiles of
// its size, and gives t back when it cannot.
func (b *bridge) lockTab(t *tab) {
	size := b.sizes[t.id]
	l := b.tiled(t, size[0], size[1])
	for _, p := range l.Panes {
		sid := sidOf(p.PaneID)
		w, h := max(p.Rect.Width, 1), max(p.Rect.Height, 1)
		in := b.infos[sid]
		var err error
		switch {
		case b.locked[sid] && in.Cols == w && in.Rows == h && in.SizeLock == b.owner:
		case b.locked[sid]:
			err = b.c.LockedResize(sid, b.owner, w, h)
		default:
			err = b.c.LockSize(sid, b.owner, w, h)
			b.locked[sid] = err == nil
		}
		if err != nil {
			b.lose(t)
			return
		}
	}
}

// lose gives t back to the desktop: rootshell views it now.
func (b *bridge) lose(t *tab) {
	b.owned[t.id] = false
	for _, sid := range t.root.sids() {
		if b.locked[sid] {
			b.c.UnlockSize(sid, b.owner, 0, 0)
		}
		delete(b.locked, sid)
	}
}

// renew renews the leases of the locks the bridge holds.
func (b *bridge) renew() {
	for sid := range b.locked {
		if err := b.c.RenewSize(sid, b.owner); err != nil {
			delete(b.locked, sid)
			if t := b.tabOf(sid); t != nil {
				b.lose(t)
			}
		}
	}
}

// topology is what rootshell rebuilds its tabs from when it changes.
func (b *bridge) topology() string {
	var s strings.Builder
	s.WriteString(b.active)
	for _, t := range b.tabs {
		fmt.Fprintf(&s, "|%s %q %s", t.id, t.name, t.root.shape())
	}
	return s.String()
}

// tell tells rootshell what changed: the topology, with events.gap, upon
// which it asks for the session again; else the tabs' controllers and
// layouts.
func (b *bridge) tell() {
	if shape := b.topology(); shape != b.shape {
		b.shape = shape
		b.send(map[string]any{"type": "events.gap", "dropped": 0, "resume_sequence": 0})
		// The snapshot rootshell asks for has them too; their layouts now
		// tell it as soon, the new pane's size with them.
		clear(b.told)
		clear(b.ctl)
	}
	for _, t := range b.tabs {
		l := b.layoutOf(t)
		if was, ok := b.ctl[t.id]; ok && was != l.GeometryController {
			b.send(map[string]any{"event": "tab.geometry_changed", "data": map[string]any{
				"tab_id": t.id, "workspace_id": workspace, "geometry_controller": l.GeometryController, "previous": was,
			}})
		}
		b.ctl[t.id] = l.GeometryController
		if j := mustJSON(l); j != b.told[t.id] {
			b.told[t.id] = j
			b.send(map[string]any{"type": "tab.layout", "layout": l})
		}
	}
}

// tellQuietly notes the tabs' layouts as told: rootshell takes them from
// a session snapshot.
func (b *bridge) tellQuietly() {
	clear(b.told)
	clear(b.ctl)
	for _, t := range b.tabs {
		l := b.layoutOf(t)
		b.told[t.id], b.ctl[t.id] = mustJSON(l), l.GeometryController
	}
}

func mustJSON(v any) string {
	j, _ := json.Marshal(v)
	return string(j)
}

// The layout of a tab, as herdr's LayoutSnapshot.
type layoutSnapshot struct {
	WorkspaceID        string      `json:"workspace_id"`
	TabID              string      `json:"tab_id"`
	Zoomed             bool        `json:"zoomed"`
	Area               rect        `json:"area"`
	FocusedPaneID      string      `json:"focused_pane_id"`
	Panes              []paneRect  `json:"panes"`
	Splits             []splitRect `json:"splits"`
	GeometryController controller  `json:"geometry_controller"`
}

type rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type paneRect struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
	Rect    rect   `json:"rect"`
}

type splitRect struct {
	ID        string  `json:"id"`
	Direction string  `json:"direction"`
	Ratio     float64 `json:"ratio"`
	Rect      rect    `json:"rect"`
}

// controller tells who sizes a tab: "client", the desktop, or "control",
// the bridge of ConnectionID.
type controller struct {
	Kind         string `json:"kind"`
	ConnectionID uint64 `json:"connection_id,omitempty"`
	Chrome       string `json:"chrome"`
}

func paneID(sid string) string { return "p-" + sid }

// sidOf is the session of a pane id, or of a terminal id, the session's.
func sidOf(id string) string { return strings.TrimPrefix(id, "p-") }

// cell clamps n to what rootshell takes for a size: 1 to 65535.
func cell(n int) int { return min(max(n, 1), math.MaxUint16) }

// layoutOf is t's layout: tiled at the size rootshell gave it while
// rootshell holds it, else its sessions' sizes packed.
func (b *bridge) layoutOf(t *tab) layoutSnapshot {
	if b.owned[t.id] {
		size := b.sizes[t.id]
		return b.tiled(t, size[0], size[1])
	}
	l := layoutSnapshot{WorkspaceID: workspace, TabID: t.id, FocusedPaneID: paneID(t.focus), Panes: []paneRect{}, Splits: []splitRect{}}
	if t.zoom {
		in := b.infos[t.focus]
		l.Zoomed, l.Area = true, rect{0, 0, cell(in.Cols), cell(in.Rows)}
		l.Panes = append(l.Panes, paneRect{paneID(t.focus), true, l.Area})
	} else {
		w, h := b.pack(t.root, 0, 0, "s", t.focus, &l)
		l.Area = rect{0, 0, cell(w), cell(h)}
	}
	// The lock of another, which holds every session of the tab, makes it
	// that one's; the bridge's own, a tab it lost, is on its way out.
	l.GeometryController = controller{Kind: "client", Chrome: "none"}
	owner := b.infos[t.root.sids()[0]].SizeLock
	for _, sid := range t.root.sids() {
		if b.infos[sid].SizeLock != owner {
			owner = ""
		}
	}
	if owner != "" && owner != b.owner {
		l.GeometryController = controller{Kind: "control", ConnectionID: connectionID(owner), Chrome: "none"}
	}
	return l
}

// tiled lays t out at cols×rows, its panes tiling it, as rootshell draws
// the dividers: each pane 4×2 at least, where the tab has the room.
func (b *bridge) tiled(t *tab, cols, rows int) layoutSnapshot {
	cols, rows = cell(cols), cell(rows)
	l := layoutSnapshot{
		WorkspaceID: workspace, TabID: t.id, Area: rect{0, 0, cols, rows}, FocusedPaneID: paneID(t.focus),
		Panes: []paneRect{}, Splits: []splitRect{},
		GeometryController: controller{Kind: "control", ConnectionID: b.id, Chrome: "none"},
	}
	// A zoomed pane fills the tab; the others keep their sizes.
	if t.zoom {
		l.Zoomed = true
		l.Panes = append(l.Panes, paneRect{paneID(t.focus), true, l.Area})
		return l
	}
	var tile func(n *node, r rect, id string)
	tile = func(n *node, r rect, id string) {
		if n.sid != "" {
			l.Panes = append(l.Panes, paneRect{paneID(n.sid), n.sid == t.focus, r})
			return
		}
		ext := r.Width
		if n.vertical {
			ext = r.Height
		}
		a, least, most := ext, 1, ext-1
		if lo, hi := least4x2(n.a, n.vertical), least4x2(n.b, n.vertical); lo+hi <= ext {
			least, most = lo, ext-hi
		}
		if ext >= 2 {
			a = min(max(int(math.Round(float64(ext)*n.ratio)), least), most)
		}
		first, second := r, r
		if n.vertical {
			first.Height, second.Y, second.Height = a, r.Y+a, ext-a
		} else {
			first.Width, second.X, second.Width = a, r.X+a, ext-a
		}
		l.Splits = append(l.Splits, splitRect{id, direction(n), float64(a) / float64(ext), r})
		tile(n.a, first, id+"0")
		tile(n.b, second, id+"1")
	}
	tile(t.root, l.Area, "s")
	return l
}

// least4x2 is the least extent of n's panes, 4×2 each, down if vertical,
// else across.
func least4x2(n *node, vertical bool) int {
	if n.sid != "" {
		if vertical {
			return 2
		}
		return 4
	}
	a, b := least4x2(n.a, vertical), least4x2(n.b, vertical)
	if n.vertical == vertical {
		return a + b
	}
	return max(a, b)
}

func direction(n *node) string {
	if n.vertical {
		return "down"
	}
	return "right"
}

// pack lays the subtree n out at its sessions' sizes from x, y, and
// returns its extent.
func (b *bridge) pack(n *node, x, y int, id, focus string, l *layoutSnapshot) (w, h int) {
	if n.sid != "" {
		in := b.infos[n.sid]
		w, h = cell(in.Cols), cell(in.Rows)
		l.Panes = append(l.Panes, paneRect{paneID(n.sid), n.sid == focus, rect{x, y, w, h}})
		return w, h
	}
	wa, ha := b.pack(n.a, x, y, id+"0", focus, l)
	var share int
	if n.vertical {
		wb, hb := b.pack(n.b, x, y+ha, id+"1", focus, l)
		w, h, share = max(wa, wb), ha+hb, ha
	} else {
		wb, hb := b.pack(n.b, x+wa, y, id+"1", focus, l)
		w, h, share = wa+wb, max(ha, hb), wa
	}
	ext := w
	if n.vertical {
		ext = h
	}
	l.Splits = append(l.Splits, splitRect{id, direction(n), float64(share) / float64(ext), rect{x, y, w, h}})
	return w, h
}

// The node of layout.export.
func exportNode(n *node) map[string]any {
	if n.sid != "" {
		return map[string]any{"type": "pane", "pane_id": paneID(n.sid)}
	}
	return map[string]any{"type": "split", "direction": direction(n), "ratio": n.ratio, "first": exportNode(n.a), "second": exportNode(n.b)}
}
