package rexapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorex/internal/layout"
)

// The one creator and flavor GoRex has: every block is a terminal, and
// every terminal is a shell.
const (
	creatorName = "com.superlogical.terminal"
	flavorShell = "com.superlogical.terminal.shell"
)

// model is what the Rex API shows of GoRex at one moment: every
// workspace as a session, its tabs as windows, and its local panes as
// blocks.
type model struct {
	sessions []*mSession
	byID     map[string]*mSession
	blocks   map[string]*mBlock
	// host names the machine the blocks run on.
	host string
}

// mSession is a GoRex workspace as a Rex session.
type mSession struct {
	id       string
	label    string
	active   string
	windows  []*mWindow
	blocks   []*mBlock
	revision uint64
	// space is the GoRex workspace the session is.
	space string
	// sig names everything the session's view shows, so that a later
	// build can tell whether the view changed.
	sig string
}

// mWindow is a GoRex tab as a Rex window.
type mWindow struct {
	id, label string
	focused   string
	zoomed    string
	layer     string
	layout    *layoutView
	tree      *fnode
	blocks    []*mBlock
}

// mBlock is a GoRex pane with a local session as a Rex block.
type mBlock struct {
	id      string
	sid     string
	label   string
	session string
	window  string
	// owner is the client that owns the block, "" for none.
	owner string
}

// view is session.view's result, and the view inside session.attach and
// session.bootstrap's results.
type view struct {
	Revision       uint64       `json:"revision"`
	SessionID      string       `json:"session_id"`
	Label          string       `json:"label,omitempty"`
	ActiveWindowID string       `json:"active_window_id,omitempty"`
	Windows        []windowView `json:"windows,omitempty"`
}

type windowView struct {
	WindowID       string      `json:"window_id"`
	Label          string      `json:"label,omitempty"`
	FocusedBlockID string      `json:"focused_block_id,omitempty"`
	ZoomedBlockID  string      `json:"zoomed_block_id,omitempty"`
	Layers         []layerView `json:"layers"`
}

type layerView struct {
	LayerID string      `json:"layer_id"`
	Kind    string      `json:"kind"`
	Bounds  [4]float64  `json:"bounds"`
	Layout  *layoutView `json:"layout"`
	Blocks  []blockView `json:"blocks"`
}

type layoutView struct {
	Rect    [4]float64 `json:"rect"`
	BlockID string     `json:"block_id,omitempty"`
	Split   *splitView `json:"split,omitempty"`
}

type splitView struct {
	SplitID   string      `json:"split_id"`
	Direction string      `json:"direction"`
	Ratio     float64     `json:"ratio"`
	Before    *layoutView `json:"before"`
	After     *layoutView `json:"after"`
}

type blockView struct {
	BlockID     string `json:"block_id"`
	Label       string `json:"label,omitempty"`
	CreatorName string `json:"creator_name,omitempty"`
	Flavor      string `json:"flavor,omitempty"`
	HostID      string `json:"host_id,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// mapper builds a model from the layout and remembers what it built
// before: the window ID a tab keeps, and the revision of each session's
// view. One goroutine uses it at a time, under the gateway's refresh
// lock.
type mapper struct {
	// windows are the windows of the last build, with the sessions that
	// name each tab.
	windows []windowRef
	// revs and sigs are the last revision and view signature of each
	// session by ID.
	revs map[string]uint64
	sigs map[string]string
}

// windowRef remembers which tab a window ID belongs to, by the sessions
// in it: a tab keeps its window while it keeps any of them.
type windowRef struct {
	id   string
	sids map[string]bool
}

func newMapper() *mapper {
	return &mapper{revs: map[string]uint64{}, sigs: map[string]string{}}
}

// build maps the layout to the Rex model, host naming the machine the
// blocks run on.
func (mp *mapper) build(raw json.RawMessage, host string, owners map[string]string) (*model, error) {
	var store layout.Store
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &store); err != nil {
			return nil, fmt.Errorf("layout: %w", err)
		}
	}
	spaces := store.Sessions
	if len(spaces) == 0 && len(store.Tabs) > 0 {
		// A layout of before workspaces: its tabs are one workspace.
		spaces = []layout.Workspace{{ID: "main", Name: "gorex", Layout: store.Layout}}
	}
	m := &model{byID: map[string]*mSession{}, blocks: map[string]*mBlock{}, host: host}
	used := make([]bool, len(mp.windows))
	refs := make([]windowRef, 0, len(spaces))
	for i := range spaces {
		ws := &spaces[i]
		workspace := ws.ID
		if workspace == "" {
			workspace = "main"
		}
		s := &mSession{id: sessionID(workspace), label: ws.Name, space: workspace}
		if s.label == "" {
			s.label = "Session " + shortID(s.id)
		}
		perTab := make([]string, len(ws.Tabs))
		for ti := range ws.Tabs {
			tab := &ws.Tabs[ti]
			if tab.Root == nil {
				continue
			}
			sids := sidsOf(tab.Root)
			// A tab keeps its window ID while it keeps any of its
			// sessions, even while it shows no block this server serves.
			wid := mp.windowFor(sids, used)
			refs = append(refs, windowRef{id: wid, sids: sids})
			root := filterNode(tab.Root)
			if root == nil {
				// A tab with no local session is not a window this
				// server can show.
				continue
			}
			w := &mWindow{id: wid}
			w.label = tab.Name
			if w.label == "" {
				w.label = "Window " + shortID(w.id)
			}
			w.layer = layerID(w.id)
			kept := map[*layout.Node]string{}
			walkBlocks(root, func(n *layout.Node, b string) {
				kept[n] = b
				block := &mBlock{
					id: b, sid: n.SID, label: blockLabel(n),
					session: s.id, window: w.id, owner: owners[b],
				}
				w.blocks = append(w.blocks, block)
				s.blocks = append(s.blocks, block)
				m.blocks[b] = block
			})
			assignRect(root, [4]float64{0, 0, 1, 1})
			w.tree = root
			w.layout = root.view()
			// The focus names a place among every leaf the tab keeps,
			// browser panes and the unavailable among them.
			leaves := leavesOf(tab.Root)
			if tab.Focus >= 0 && tab.Focus < len(leaves) {
				w.focused = kept[leaves[tab.Focus]]
			}
			if tab.Zoom {
				w.zoomed = w.focused
			}
			s.windows = append(s.windows, w)
			perTab[ti] = w.id
		}
		if ws.Active >= 0 && ws.Active < len(perTab) {
			s.active = perTab[ws.Active]
		}
		if s.active == "" && len(s.windows) > 0 {
			s.active = s.windows[0].id
		}
		v := s.view(host)
		s.sig = viewSignature(v)
		if old, ok := mp.sigs[s.id]; ok && old == s.sig {
			s.revision = mp.revs[s.id]
		} else if ok {
			s.revision = mp.revs[s.id] + 1
		} else {
			s.revision = 1
		}
		mp.revs[s.id], mp.sigs[s.id] = s.revision, s.sig
		m.sessions = append(m.sessions, s)
		m.byID[s.id] = s
	}
	mp.windows = refs
	for id := range mp.revs {
		if m.byID[id] == nil {
			delete(mp.revs, id)
			delete(mp.sigs, id)
		}
	}
	return m, nil
}

// windowFor returns the ID of the window of a tab with sids, keeping the
// ID of the last window that shares any of them, or making a new one.
// used marks the last build's windows that a tab took.
func (mp *mapper) windowFor(sids map[string]bool, used []bool) string {
	best, most := -1, 0
	for i, w := range mp.windows {
		if used[i] {
			continue
		}
		n := 0
		for sid := range sids {
			if w.sids[sid] {
				n++
			}
		}
		if n > most {
			best, most = i, n
		}
	}
	if best >= 0 {
		used[best] = true
		return mp.windows[best].id
	}
	return newWindowID()
}

// sidsOf returns every session a tab holds, by ID.
func sidsOf(n *layout.Node) map[string]bool {
	sids := map[string]bool{}
	var walk func(*layout.Node)
	walk = func(n *layout.Node) {
		if n == nil {
			return
		}
		if n.SID != "" {
			sids[n.SID] = true
		}
		walk(n.A)
		walk(n.B)
	}
	walk(n)
	return sids
}

// fnode is a layout tree after the panes GoRex can show: a kept pane, or
// a split of two such trees, with its place in the window.
type fnode struct {
	leaf     *layout.Node
	block    string
	split    string
	vertical bool
	ratio    float64
	a, b     *fnode
	rect     [4]float64
}

// filterNode removes the panes this server does not show: ones on a
// remote host, browser panes, panes the server would not share, and
// panes with no session. A split with one side left takes the split's
// whole place.
func filterNode(n *layout.Node) *fnode {
	if n == nil {
		return nil
	}
	if n.A == nil || n.B == nil {
		if n.SID == "" || n.Host != "" || n.URL != "" || n.Unavailable {
			return nil
		}
		return &fnode{leaf: n, block: blockID(n.SID)}
	}
	a, b := filterNode(n.A), filterNode(n.B)
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	ratio := float64(n.Ratio)
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	f := &fnode{vertical: n.Vertical, ratio: ratio, a: a, b: b}
	f.split = splitID(f.shape())
	return f
}

// shape names the panes and splits under f, without their ratios: a
// split keeps its ID while it is resized.
func (f *fnode) shape() string {
	if f.leaf != nil {
		return "b:" + f.leaf.SID
	}
	direction := "h"
	if f.vertical {
		direction = "v"
	}
	return "(" + direction + ":" + f.a.shape() + "," + f.b.shape() + ")"
}

// assignRect gives f and the trees under it their places in rect.
func assignRect(f *fnode, rect [4]float64) {
	f.rect = rect
	if f.leaf != nil {
		return
	}
	a, b := rect, rect
	if f.vertical {
		a[3] = rect[3] * f.ratio
		b[1] = rect[1] + a[3]
		b[3] = rect[3] - a[3]
	} else {
		a[2] = rect[2] * f.ratio
		b[0] = rect[0] + a[2]
		b[2] = rect[2] - a[2]
	}
	assignRect(f.a, a)
	assignRect(f.b, b)
}

// view is f's place in Rex's layout tree.
func (f *fnode) view() *layoutView {
	lv := &layoutView{Rect: f.rect}
	if f.leaf != nil {
		lv.BlockID = f.block
		return lv
	}
	direction := "horizontal"
	if f.vertical {
		direction = "vertical"
	}
	lv.Split = &splitView{
		SplitID: f.split, Direction: direction, Ratio: f.ratio,
		Before: f.a.view(), After: f.b.view(),
	}
	return lv
}

// walkBlocks calls fn for each kept pane under f, in layout order.
func walkBlocks(f *fnode, fn func(*layout.Node, string)) {
	if f.leaf != nil {
		fn(f.leaf, f.block)
		return
	}
	walkBlocks(f.a, fn)
	walkBlocks(f.b, fn)
}

// leavesOf returns every leaf of a layout tree, as the layout's focus
// counts them, including the ones filterNode leaves out.
func leavesOf(n *layout.Node) []*layout.Node {
	if n == nil {
		return nil
	}
	if n.A == nil || n.B == nil {
		return []*layout.Node{n}
	}
	return append(leavesOf(n.A), leavesOf(n.B)...)
}

// blockLabel is what a pane reports as its block's label.
func blockLabel(n *layout.Node) string {
	if n.Name != "" {
		return n.Name
	}
	return "shell"
}

// view builds the session's view, with its revision.
func (s *mSession) view(host string) view {
	v := view{
		Revision:       s.revision,
		SessionID:      s.id,
		Label:          s.label,
		ActiveWindowID: s.active,
	}
	for _, w := range s.windows {
		wv := windowView{
			WindowID:       w.id,
			Label:          w.label,
			FocusedBlockID: w.focused,
			ZoomedBlockID:  w.zoomed,
			Layers:         []layerView{},
		}
		layer := layerView{
			LayerID: w.layer,
			Kind:    "tiled",
			Bounds:  [4]float64{0, 0, 1, 1},
			Layout:  w.layout,
			Blocks:  make([]blockView, 0, len(w.blocks)),
		}
		for _, b := range w.blocks {
			layer.Blocks = append(layer.Blocks, blockView{
				BlockID:     b.id,
				Label:       b.label,
				CreatorName: creatorName,
				Flavor:      flavorShell,
				HostID:      host,
				Owner:       b.owner,
			})
		}
		wv.Layers = append(wv.Layers, layer)
		v.Windows = append(v.Windows, wv)
	}
	return v
}

// viewSignature names everything a view shows but its revision, so that
// a change of any of it gives the session a new revision.
func viewSignature(v view) string {
	v.Revision = 0
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// list returns the sessions as session.list reports them.
func (m *model) list() []sessionListEntry {
	out := make([]sessionListEntry, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, sessionListEntry{SessionID: s.id, Label: s.label})
	}
	return out
}

// window returns s's window by ID.
func (s *mSession) window(id string) *mWindow {
	for _, w := range s.windows {
		if w.id == id {
			return w
		}
	}
	return nil
}

// ordinal is w's place among s's windows, counting from 1.
func (s *mSession) ordinal(w *mWindow) int {
	for i, o := range s.windows {
		if o == w {
			return i + 1
		}
	}
	return 0
}

// findBlock returns s's block by ID.
func (s *mSession) findBlock(id string) *mBlock {
	for _, b := range s.blocks {
		if b.id == id {
			return b
		}
	}
	return nil
}

// sortedStrings returns ss sorted, as client.list reports session IDs.
func sortedStrings(ss []string) []string {
	sort.Strings(ss)
	return ss
}

// fallbackLabel is a person's name for a value with no label, for target
// results: the field is required and may be empty, but an empty one is
// less useful than the ID's end.
func fallbackLabel(label, id string) string {
	if label != "" {
		return label
	}
	return shortID(id)
}

// joinNonEmpty joins the parts that are not empty with a space, for the
// "where" of a target.
func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

// blockByID returns the model's block with ID id, nil for none.
func (m *model) blockByID(id string) *mBlock {
	if m == nil {
		return nil
	}
	return m.blocks[id]
}

// sessionOfSpace returns the session of a GoRex workspace.
func (m *model) sessionOfSpace(space string) *mSession {
	for _, s := range m.sessions {
		if s.space == space {
			return s
		}
	}
	return nil
}

// focused returns the block with the focus in the session's active
// window, nil when it has none.
func (s *mSession) focused() *mBlock {
	w := s.window(s.active)
	if w == nil || w.focused == "" {
		return nil
	}
	return s.findBlock(w.focused)
}

// windowOf returns the window that holds a block.
func (s *mSession) windowOf(b *mBlock) *mWindow {
	if b == nil {
		return nil
	}
	return s.window(b.window)
}

// placed is a block and its place in its window, in fractions of it.
type placed struct {
	b    *mBlock
	rect [4]float64
}

// places returns every block of the window with its place, in layout
// order.
func (w *mWindow) places() []placed {
	var out []placed
	var walk func(*fnode, *mBlock)
	seen := 0
	walk = func(n *fnode, _ *mBlock) {
		if n == nil {
			return
		}
		if n.leaf != nil {
			if seen < len(w.blocks) {
				out = append(out, placed{b: w.blocks[seen], rect: n.rect})
				seen++
			}
			return
		}
		walk(n.a, nil)
		walk(n.b, nil)
	}
	walk(w.tree, nil)
	return out
}

// neighbor returns the block next to from in direction ("left", "right",
// "up", "down") within w, and whether there was one. With wrap, a block
// at an edge goes on from the opposite one.
func (w *mWindow) neighbor(from *mBlock, direction string, wrap bool) (*mBlock, bool) {
	places := w.places()
	var at *placed
	for i := range places {
		if places[i].b == from {
			at = &places[i]
			break
		}
	}
	if at == nil {
		return nil, false
	}
	const eps = 1e-6
	horizontal := direction == "left" || direction == "right"
	forward := direction == "right" || direction == "down"
	best, bestGap, bestOverlap := -1, 0.0, 0.0
	for i := range places {
		p := places[i]
		if p.b == from {
			continue
		}
		var gap, overlap float64
		if horizontal {
			if forward {
				gap = p.rect[0] - (at.rect[0] + at.rect[2])
				if gap < -eps {
					continue
				}
			} else {
				gap = at.rect[0] - (p.rect[0] + p.rect[2])
				if gap < -eps {
					continue
				}
			}
			overlap = spanOverlap(at.rect[1], at.rect[3], p.rect[1], p.rect[3])
		} else {
			if forward {
				gap = p.rect[1] - (at.rect[1] + at.rect[3])
				if gap < -eps {
					continue
				}
			} else {
				gap = at.rect[1] - (p.rect[1] + p.rect[3])
				if gap < -eps {
					continue
				}
			}
			overlap = spanOverlap(at.rect[0], at.rect[2], p.rect[0], p.rect[2])
		}
		if gap <= eps && overlap <= eps {
			// Side by side on the other axis only: no neighbor this way.
			continue
		}
		gap = max(gap, 0)
		if best < 0 || gap < bestGap-eps || (gap <= bestGap+eps && overlap > bestOverlap+eps) {
			best, bestGap, bestOverlap = i, gap, overlap
		}
	}
	if best >= 0 {
		return places[best].b, true
	}
	if !wrap {
		return nil, false
	}
	// Wrapping: the block farthest the other way.
	for i := range places {
		p := places[i]
		if p.b == from {
			continue
		}
		gap := at.rect[0] - p.rect[0]
		if !horizontal {
			gap = at.rect[1] - p.rect[1]
		}
		if forward {
			gap = -gap
		}
		if gap < -eps {
			continue
		}
		overlap := spanOverlap(at.rect[1], at.rect[3], p.rect[1], p.rect[3])
		if !horizontal {
			overlap = spanOverlap(at.rect[0], at.rect[2], p.rect[0], p.rect[2])
		}
		if best < 0 || gap < bestGap-eps || (gap <= bestGap+eps && overlap > bestOverlap+eps) {
			best, bestGap, bestOverlap = i, gap, overlap
		}
	}
	if best < 0 {
		return nil, false
	}
	return places[best].b, true
}

// spanOverlap returns how much two spans share.
func spanOverlap(a0, aLen, b0, bLen float64) float64 {
	return max(0, min(a0+aLen, b0+bLen)-max(a0, b0))
}

// splitByID returns the split view of an ID under l.
func splitByID(l *layoutView, id string) *splitView {
	if l == nil || l.Split == nil {
		return nil
	}
	if l.Split.SplitID == id {
		return l.Split
	}
	if s := splitByID(l.Split.Before, id); s != nil {
		return s
	}
	return splitByID(l.Split.After, id)
}

// splitIDs returns every split ID under l.
func splitIDs(l *layoutView) []string {
	if l == nil || l.Split == nil {
		return nil
	}
	return append(append([]string{l.Split.SplitID}, splitIDs(l.Split.Before)...), splitIDs(l.Split.After)...)
}

// lowestSplit returns the innermost split that holds both blocks, nil
// when none does.
func lowestSplit(l *layoutView, a, b string) *splitView {
	if l == nil || l.Split == nil {
		return nil
	}
	if containsBlock(l.Split.Before, a) && containsBlock(l.Split.After, b) ||
		containsBlock(l.Split.Before, b) && containsBlock(l.Split.After, a) {
		return l.Split
	}
	if containsBlock(l.Split.Before, a) && containsBlock(l.Split.Before, b) {
		return lowestSplit(l.Split.Before, a, b)
	}
	if containsBlock(l.Split.After, a) && containsBlock(l.Split.After, b) {
		return lowestSplit(l.Split.After, a, b)
	}
	return nil
}

// containsBlock tells whether a block is a leaf of l.
func containsBlock(l *layoutView, block string) bool {
	if l == nil {
		return false
	}
	if l.Split == nil {
		return l.BlockID == block
	}
	return containsBlock(l.Split.Before, block) || containsBlock(l.Split.After, block)
}

// findFnode returns the leaf of a GoRex session under f.
func findFnode(f *fnode, sid string) *fnode {
	if f == nil {
		return nil
	}
	if f.leaf != nil {
		if f.leaf.SID == sid {
			return f
		}
		return nil
	}
	if x := findFnode(f.a, sid); x != nil {
		return x
	}
	return findFnode(f.b, sid)
}

// fnodePath returns the tree from f down to leaf, nil when leaf is not
// under f.
func fnodePath(f, leaf *fnode) []*fnode {
	if f == nil {
		return nil
	}
	if f == leaf {
		return []*fnode{f}
	}
	if p := fnodePath(f.a, leaf); p != nil {
		return append([]*fnode{f}, p...)
	}
	if p := fnodePath(f.b, leaf); p != nil {
		return append([]*fnode{f}, p...)
	}
	return nil
}

// appResizeSplit returns the split the app moves for leaf, whose
// direction edge points grows to the right or down: the nearest
// same-axis split that has leaf on that side, or the nearest one.
func appResizeSplit(root, leaf *fnode, vertical, grows bool) *fnode {
	path := fnodePath(root, leaf)
	var nearest *fnode
	for i := len(path) - 2; i >= 0; i-- {
		sp := path[i]
		if sp.leaf != nil || sp.vertical != vertical {
			continue
		}
		if nearest == nil {
			nearest = sp
		}
		child := path[i+1]
		if (sp.a == child) == grows {
			return sp
		}
	}
	return nearest
}
