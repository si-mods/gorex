package rexapi

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// CreatorName and FlavorShell are what every GoRex block reports: its
// terminals are Rex's, and they are all shells.
const (
	CreatorName = "com.superlogical.terminal"
	FlavorShell = "com.superlogical.terminal.shell"
)

// unplacedSession is the Rex session that holds sessions no window shows,
// as gorex new makes them: a block ID a Rex client can hold.
var unplacedSession = "session:" + uuidLike("session", "unplaced")

// model is what the Rex API shows of GoRex: every workspace as a session,
// its tabs as windows, and its panes as blocks.
type model struct {
	rev      uint64
	sig      string
	sessions []*mSession
	session  map[string]*mSession
	windows  map[string]*mWindow
	blocks   map[string]*mBlock
}

// mSession is a workspace as a Rex session.
type mSession struct {
	sig          string
	ID           string
	Label        string
	Workspace    string
	ActiveWindow string
	Windows      []*mWindow
	Blocks       []*mBlock // layout order, then detached, sorted by ID
}

// mWindow is a tab as a Rex window.
type mWindow struct {
	ID      string
	Label   string
	Focused string
	Zoomed  string
	Tab     *layout.Tab
	Blocks  []*mBlock // layout order
}

// mBlock is a pane, or a session no tab shows, as a Rex block.
type mBlock struct {
	ID        string
	SID       string
	Label     string
	Session   string
	WindowID  string
	Placement string // "placed" or "detached"
}

// blockID names the Rex block of a GoRex session.
func blockID(sid string) string { return "block:" + uuidLike("block", sid) }

// sessionID names the Rex session of a workspace.
func sessionID(workspace string) string { return "session:" + uuidLike("session", workspace) }

// windowID names a tab's window by its first block, which stays while the
// tab keeps that pane.
func windowID(root *layout.Node) string {
	return "window:" + uuidLike("window", firstSID(root))
}

// layerID names a window's one layer.
func layerID(w *mWindow) string {
	return "layer:" + uuidLike("layer", w.ID+":tiled")
}

// splitID names a split by the first block on its before side.
func splitID(n *layout.Node) string { return "split:" + uuidLike("split", firstSID(n)) }

// firstSID is the SID of the first pane of a node.
func firstSID(n *layout.Node) string {
	if n == nil {
		return ""
	}
	if n.SID != "" {
		return n.SID
	}
	if s := firstSID(n.A); s != "" {
		return s
	}
	return firstSID(n.B)
}

// buildModel derives the Rex view of GoRex from the layout the app saved
// and the sessions the server runs, at layout revision rev.
func buildModel(rev uint64, raw json.RawMessage, infos []rex.SessionInfo) (*model, error) {
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
	m := &model{
		rev:     rev,
		session: map[string]*mSession{},
		windows: map[string]*mWindow{},
		blocks:  map[string]*mBlock{},
	}
	placed := map[string]bool{}
	for i := range spaces {
		ws := &spaces[i]
		s := &mSession{
			ID:        sessionID(ws.ID),
			Label:     ws.Name,
			Workspace: ws.ID,
		}
		if s.Label == "" {
			s.Label = "session " + short(ws.ID)
		}
		for ti := range ws.Tabs {
			tab := &ws.Tabs[ti]
			if tab.Root == nil {
				continue
			}
			w := &mWindow{ID: windowID(tab.Root), Label: tab.Name, Tab: tab}
			if w.Label == "" {
				w.Label = "Window " + short(w.ID)
			}
			for _, n := range paneNodes(tab.Root) {
				b := &mBlock{
					ID:        blockID(n.SID),
					SID:       n.SID,
					Label:     blockLabel(n),
					Session:   s.ID,
					WindowID:  w.ID,
					Placement: "placed",
				}
				placed[n.SID] = true
				w.Blocks = append(w.Blocks, b)
				s.Blocks = append(s.Blocks, b)
				m.blocks[b.ID] = b
			}
			if len(w.Blocks) == 0 {
				continue
			}
			if i := tab.Focus; i >= 0 && i < len(w.Blocks) {
				w.Focused = w.Blocks[i].ID
			} else {
				w.Focused = w.Blocks[0].ID
			}
			if tab.Zoom {
				w.Zoomed = w.Focused
			}
			if ti == ws.Active {
				s.ActiveWindow = w.ID
			}
			s.Windows = append(s.Windows, w)
			m.windows[w.ID] = w
		}
		if s.ActiveWindow == "" && len(s.Windows) > 0 {
			s.ActiveWindow = s.Windows[0].ID
		}
		m.sessions = append(m.sessions, s)
		m.session[s.ID] = s
	}
	// A session no tab shows is a detached block, which only the layout
	// knows nothing of: an empty workspace is still a session.
	var loose []*mBlock
	for _, in := range infos {
		if placed[in.ID] || in.ID == "" {
			continue
		}
		b := &mBlock{
			ID:        blockID(in.ID),
			SID:       in.ID,
			Label:     "shell",
			Session:   unplacedSession,
			Placement: "detached",
		}
		loose = append(loose, b)
		m.blocks[b.ID] = b
	}
	if len(loose) > 0 {
		sort.Slice(loose, func(i, j int) bool { return loose[i].ID < loose[j].ID })
		s := &mSession{ID: unplacedSession, Label: "Detached", Blocks: loose}
		m.sessions = append(m.sessions, s)
		m.session[s.ID] = s
	}
	var all []string
	for _, s := range m.sessions {
		s.sig = sessionSig(s)
		all = append(all, s.sig)
	}
	m.sig = fmt.Sprintf("%d|%s", rev, strings.Join(all, ";"))
	return m, nil
}

// paneNodes returns the panes of a layout tree in layout order.
func paneNodes(n *layout.Node) []*layout.Node {
	if n == nil {
		return nil
	}
	if n.SID != "" && n.A == nil && n.B == nil {
		return []*layout.Node{n}
	}
	if n.A == nil || n.B == nil {
		return nil
	}
	return append(paneNodes(n.A), paneNodes(n.B)...)
}

// blockLabel is what a pane reports as its block's label.
func blockLabel(n *layout.Node) string {
	if n.Name != "" {
		return n.Name
	}
	return "shell"
}

// short returns the readable end of an ID.
func short(id string) string {
	if i := strings.LastIndexAny(id, ":.-"); i >= 0 {
		id = id[i+1:]
	}
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return id
}

// window returns a session's window by ID.
func (s *mSession) window(id string) *mWindow {
	for _, w := range s.Windows {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// activeWindow returns the session's active window, or its first.
func (s *mSession) activeWindow() *mWindow {
	if w := s.window(s.ActiveWindow); w != nil {
		return w
	}
	if len(s.Windows) > 0 {
		return s.Windows[0]
	}
	return nil
}

// findAll returns every block the session holds, placed or detached.
func (s *mSession) findBlock(id string) *mBlock {
	for _, b := range s.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// ordinal is a window's position among its session's windows, from 1.
func (s *mSession) ordinal(w *mWindow) int {
	for i, o := range s.Windows {
		if o == w {
			return i + 1
		}
	}
	return 0
}

// sessionOfBlock returns the session that holds the block.
func (m *model) sessionOfBlock(id string) *mSession {
	if b := m.blocks[id]; b != nil {
		return m.session[b.Session]
	}
	return nil
}

// view is session.view's result.
type view struct {
	Revision       uint64       `json:"revision"`
	SessionID      string       `json:"session_id"`
	Label          string       `json:"label,omitempty"`
	ActiveWindowID string       `json:"active_window_id,omitempty"`
	Windows        []windowView `json:"windows"`
	windowIndex    map[string]int
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
	Label       string `json:"label"`
	CreatorName string `json:"creator_name,omitempty"`
	Flavor      string `json:"flavor,omitempty"`
	HostID      string `json:"host_id,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// viewOf builds a session's view. hostID names the machine the blocks
// run on.
func (m *model) viewOf(s *mSession, hostID string) view {
	v := view{
		Revision:       m.rev,
		SessionID:      s.ID,
		Label:          s.Label,
		ActiveWindowID: s.ActiveWindow,
		Windows:        make([]windowView, 0, len(s.Windows)),
		windowIndex:    map[string]int{},
	}
	if v.Windows == nil {
		v.Windows = []windowView{}
	}
	for i, w := range s.Windows {
		wv := windowView{
			WindowID:       w.ID,
			Label:          w.Label,
			FocusedBlockID: w.Focused,
			ZoomedBlockID:  w.Zoomed,
			Layers:         []layerView{},
		}
		layer := layerView{
			LayerID: layerID(w),
			Kind:    "tiled",
			Bounds:  [4]float64{0, 0, 1, 1},
			Layout:  layoutOf(w.Tab.Root, [4]float64{0, 0, 1, 1}),
			Blocks:  []blockView{},
		}
		for _, b := range w.Blocks {
			layer.Blocks = append(layer.Blocks, blockView{
				BlockID:     b.ID,
				Label:       b.Label,
				CreatorName: CreatorName,
				Flavor:      FlavorShell,
				HostID:      hostID,
			})
		}
		wv.Layers = append(wv.Layers, layer)
		v.Windows = append(v.Windows, wv)
		v.windowIndex[w.ID] = i
	}
	return v
}

// layoutOf turns a layout tree into Rex's rects and splits.
func layoutOf(n *layout.Node, rect [4]float64) *layoutView {
	if n == nil {
		return nil
	}
	lv := &layoutView{Rect: rect}
	if n.A == nil || n.B == nil {
		lv.BlockID = blockID(n.SID)
		return lv
	}
	ratio := float64(n.Ratio)
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	direction := "horizontal"
	a, b := rect, rect
	if n.Vertical {
		direction = "vertical"
		a[3] = rect[3] * ratio
		b[1] = rect[1] + a[3]
		b[3] = rect[3] - a[3]
	} else {
		a[2] = rect[2] * ratio
		b[0] = rect[0] + a[2]
		b[2] = rect[2] - a[2]
	}
	lv.Split = &splitView{
		SplitID:   splitID(n),
		Direction: direction,
		Ratio:     ratio,
		Before:    layoutOf(n.A, a),
		After:     layoutOf(n.B, b),
	}
	return lv
}

// listBlocks is session.list_blocks' result for a session, blocks sorted
// by ID as Rex sorts them.
func (m *model) listBlocks(s *mSession, hostID string) []blockInfo {
	all := append([]*mBlock(nil), s.Blocks...)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	out := make([]blockInfo, 0, len(all))
	for _, b := range all {
		out = append(out, blockInfo{
			BlockID:     b.ID,
			Label:       b.Label,
			CreatorName: CreatorName,
			Flavor:      FlavorShell,
			HostID:      hostID,
			Placement:   b.Placement,
			WindowID:    b.WindowID,
		})
	}
	return out
}

type blockInfo struct {
	BlockID     string `json:"block_id"`
	Label       string `json:"label"`
	CreatorName string `json:"creator_name,omitempty"`
	Flavor      string `json:"flavor,omitempty"`
	HostID      string `json:"host_id,omitempty"`
	Placement   string `json:"placement"`
	WindowID    string `json:"window_id,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// sessionSig names everything a change of which the session's clients
// hear of.
func sessionSig(s *mSession) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%d", s.ID, s.Label, s.ActiveWindow, len(s.Blocks))
	for _, w := range s.Windows {
		fmt.Fprintf(&b, "|%s|%s|%s|%s", w.ID, w.Label, w.Focused, w.Zoomed)
		for _, x := range w.Blocks {
			fmt.Fprintf(&b, "|%s|%s", x.ID, x.Label)
		}
	}
	for _, x := range s.Blocks {
		fmt.Fprintf(&b, "|%s|%s|%s", x.ID, x.Label, x.Placement)
	}
	return b.String()
}

// diff describes what changed between two models as the events a
// subscribed connection gets.
func (m *model) diff(old *model) []sessionEvent {
	if old == nil || old.sig == m.sig {
		return nil
	}
	var events []sessionEvent
	for _, s := range m.sessions {
		o := old.session[s.ID]
		if o != nil && o.sig == s.sig {
			continue
		}
		if o == nil {
			events = append(events, sessionEvent{s.ID, event{"session_created", sessionCreated{Label: s.Label}}})
			for _, w := range s.Windows {
				events = append(events, sessionEvent{s.ID, event{"window_created", windowCreated{WindowID: w.ID, Label: w.Label}}})
			}
			for _, b := range s.Blocks {
				events = append(events, sessionEvent{s.ID, event{"block_created", blockCreated{BlockID: b.ID}}})
			}
			events = append(events, sessionEvent{s.ID, event{"session_view_changed", viewChanged{m.rev}}})
			continue
		}
		if o.Label != s.Label {
			events = append(events, sessionEvent{s.ID, event{"session_label_changed", labelChanged{Label: s.Label}}})
		}
		for _, b := range s.Blocks {
			if o.findBlock(b.ID) == nil {
				events = append(events, sessionEvent{s.ID, event{"block_created", blockCreated{BlockID: b.ID}}})
			}
		}
		for _, b := range o.Blocks {
			if s.findBlock(b.ID) == nil {
				events = append(events, sessionEvent{s.ID, event{"block_closed", blockClosed{BlockID: b.ID}}})
			}
		}
		for _, w := range s.Windows {
			ow := o.window(w.ID)
			if ow == nil {
				events = append(events, sessionEvent{s.ID, event{"window_created", windowCreated{WindowID: w.ID, Label: w.Label}}})
				continue
			}
			if ow.Label != w.Label {
				events = append(events, sessionEvent{s.ID, event{"window_label_changed", windowLabel{WindowID: w.ID, Label: w.Label}}})
			}
			for _, b := range w.Blocks {
				ob := ow.block(b.ID)
				if ob != nil && ob.Label != b.Label {
					events = append(events, sessionEvent{s.ID, event{"block_label_changed", blockLabelChanged{BlockID: b.ID, Label: b.Label}}})
				}
			}
		}
		for _, w := range o.Windows {
			if s.window(w.ID) == nil {
				events = append(events, sessionEvent{s.ID, event{"window_closed", windowClosed{WindowID: w.ID}}})
			}
		}
		if o.ActiveWindow != s.ActiveWindow {
			events = append(events, sessionEvent{s.ID, event{"active_window_changed", activeWindow{WindowID: s.ActiveWindow}}})
		}
		events = append(events, sessionEvent{s.ID, event{"session_view_changed", viewChanged{m.rev}}})
	}
	for _, o := range old.sessions {
		if m.session[o.ID] == nil {
			events = append(events, sessionEvent{o.ID, event{"session_destroyed", struct{}{}}})
		}
	}
	return events
}

// block returns the window's block by ID.
func (w *mWindow) block(id string) *mBlock {
	for _, b := range w.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// sessionEvent is a change of one session, as a control connection sends
// it.
type sessionEvent struct {
	Session string
	Event   event
}

type event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

type sessionCreated struct {
	Label string `json:"label"`
}

type labelChanged struct {
	Label string `json:"label"`
}

type windowCreated struct {
	WindowID string `json:"window_id"`
	Label    string `json:"label"`
}

type windowClosed struct {
	WindowID string `json:"window_id"`
}

type windowLabel struct {
	WindowID string `json:"window_id"`
	Label    string `json:"label"`
}

type activeWindow struct {
	WindowID string `json:"window_id,omitempty"`
}

type blockCreated struct {
	BlockID string `json:"block_id"`
}

type blockClosed struct {
	BlockID string `json:"block_id"`
}

type blockLabelChanged struct {
	BlockID string `json:"block_id"`
	Label   string `json:"label"`
}

type viewChanged struct {
	Revision uint64 `json:"revision"`
}
