//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"sync"

	"gorex/internal/layout"
)

// applied are the layout commands the server makes itself while no GoRex
// window takes them (apply), as the window would; the others, actions and
// teleports, need the window.
var applied = map[string]bool{
	"space.create": true, "space.rename": true, "space.close": true,
	"tab.create": true, "tab.close": true, "tab.rename": true, "tab.move": true,
	"pane.split": true, "pane.close": true, "pane.rename": true, "pane.focus": true,
	"pane.zoom": true, "pane.resize": true, "pane.swap": true, "pane.move": true,
	"tab.set": true, "windows.set": true,
}

// apply makes a change of the layout a client asks for with no window to
// make it: on the layout the server keeps, starting and ending the
// sessions of its panes here, as the window would. A command that needs
// the window is answered so.
func (s *Server) apply(c LayoutCommand) LayoutCommand {
	if !applied[c.Op] && (c.Op != "action" || c.Action != "float.set_state") {
		return LayoutCommand{ID: c.ID, Code: "desktop_unavailable", Error: "no GoRex window is open"}
	}
	a := &applying{s: s}
	res := s.applyHeld(c, a)
	// The sessions of the panes it closed end once the layout no longer
	// has them, side by side and outside the lock, as each may take a
	// while; those it started for a change it did not make end too.
	ends := a.ends
	if res.Code != "" {
		ends = nil
		for _, n := range a.made {
			ends = append(ends, n.SID)
		}
	}
	s.endAll(ends)
	return res
}

// applyHook, when set, runs in each apply between its change and its
// commit: for tests.
var applyHook func()

// applyHeld makes c under the locks: an upgrade waits for the change, its
// sessions and its layout alike, and the applies take their turns. A
// layout another client set meanwhile, as the app's whole layout, makes
// it again on that one.
func (s *Server) applyHeld(c LayoutCommand, a *applying) LayoutCommand {
	s.ops.RLock()
	defer s.ops.RUnlock()
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	for range 10 {
		s.mu.Lock()
		raw, rev := s.layout, s.layoutRev
		s.mu.Unlock()
		st := layout.Parse(raw)
		before := encodeLayout(&st)
		a.n, a.ends = 0, nil
		res := s.applyTo(&st, c, a)
		res.ID = c.ID
		if res.Code != "" {
			return res
		}
		// A command that changes nothing, as a resize at its bound,
		// leaves the layout as it was, an older one unmigrated.
		after := encodeLayout(&st)
		if bytes.Equal(before, after) {
			return res
		}
		if applyHook != nil {
			applyHook()
		}
		if _, err := s.commitLayout(after, rev); err == nil {
			return res
		}
	}
	return LayoutCommand{ID: c.ID, Code: "layout_conflict", Error: "the layout kept changing; try again"}
}

// encodeLayout writes st as the app saves a layout: with the first
// window's tabs for a client of before workspaces, and no nulls.
func encodeLayout(st *layout.Store) []byte {
	st.Layout = layout.Layout{Tabs: []layout.Tab{}}
	if w := defaultSpace(st); w != nil {
		st.Layout = w.Layout
	}
	if st.Sessions == nil {
		st.Sessions = []layout.Workspace{}
	}
	if st.Windows == nil {
		st.Windows = []string{}
	}
	for i := range st.Sessions {
		if st.Sessions[i].Tabs == nil {
			st.Sessions[i].Tabs = []layout.Tab{}
		}
	}
	b, _ := json.Marshal(st)
	return b
}

// applying is what an apply did to sessions: made are the panes it
// started, the first n of them used by the pass at the layout, and ends
// the sessions of the panes the pass closed. A pass made again on a newer
// layout uses the panes started before.
type applying struct {
	s    *Server
	made []*layout.Node
	n    int
	ends []string
}

// start returns a new pane's leaf, starting its session in the first pass.
func (a *applying) start(dir string, cols, rows int, command []string) (*layout.Node, error) {
	if a.n < len(a.made) {
		n := *a.made[a.n]
		a.n++
		return &n, nil
	}
	n, err := a.s.startPane(dir, cols, rows, command)
	if err != nil {
		return nil, err
	}
	a.made = append(a.made, n)
	a.n++
	c := *n
	return &c, nil
}

// end ends the sessions of this machine's panes under n, once the layout
// is saved without them.
func (a *applying) end(n *layout.Node) {
	for _, l := range layout.Leaves(n) {
		if l.Host == "" && l.SID != "" {
			a.ends = append(a.ends, l.SID)
		}
	}
}

// endAll ends the sessions, side by side, each as a kill does.
func (s *Server) endAll(sids []string) {
	var wg sync.WaitGroup
	for _, sid := range sids {
		s.mu.Lock()
		ss := s.sessions[sid]
		s.mu.Unlock()
		if ss == nil {
			continue
		}
		wg.Go(func() {
			s.ops.RLock()
			defer s.ops.RUnlock()
			s.end(ss)
		})
	}
	wg.Wait()
}

// defaultSpace is the workspace of a command of no space: the first
// window's, as the app has it, else the first; nil without workspaces.
func defaultSpace(st *layout.Store) *layout.Workspace {
	for _, id := range st.Windows {
		if w := st.Space(id); w != nil {
			return w
		}
	}
	if len(st.Sessions) > 0 {
		return &st.Sessions[0]
	}
	return nil
}

// spot is where a pane of this machine's is in a layout: tab ti of w, the
// leaf n, and fi its place among the tab's floats, -1 for a tiled pane.
type spot struct {
	w  *layout.Workspace
	ti int
	n  *layout.Node
	fi int
}

func (p spot) tab() *layout.Tab { return &p.w.Tabs[p.ti] }

// find returns the pane of this machine's session sid in space, every
// space for "", the default first; ok is false for none.
func find(st *layout.Store, space, sid string) (spot, bool) {
	var spaces []*layout.Workspace
	if space != "" {
		if w := st.Space(space); w != nil {
			spaces = append(spaces, w)
		}
	} else {
		if w := defaultSpace(st); w != nil {
			spaces = append(spaces, w)
		}
		for i := range st.Sessions {
			if w := &st.Sessions[i]; !slices.Contains(spaces, w) {
				spaces = append(spaces, w)
			}
		}
	}
	for _, w := range spaces {
		if p, ok := findIn(w, sid); ok {
			return p, true
		}
	}
	return spot{}, false
}

// findIn is find in one workspace.
func findIn(w *layout.Workspace, sid string) (spot, bool) {
	if sid == "" {
		return spot{}, false
	}
	local := func(n *layout.Node) *layout.Node {
		for _, l := range layout.Leaves(n) {
			if l.Host == "" && l.SID == sid {
				return l
			}
		}
		return nil
	}
	for ti := range w.Tabs {
		t := &w.Tabs[ti]
		if n := local(t.Root); n != nil {
			return spot{w, ti, n, -1}, true
		}
		for fi, f := range t.Floats {
			if n := local(f.Pane); n != nil {
				return spot{w, ti, n, fi}, true
			}
		}
	}
	return spot{}, false
}

// tabHost is the host of a tab, its first pane's: "" for this machine.
func tabHost(t *layout.Tab) string {
	for _, r := range t.Roots() {
		if l := layout.FirstLeaf(r); l != nil {
			return l.Host
		}
	}
	return ""
}

// hasRemote tells a tab with a pane of another host's session: the server
// cannot end that session, and does not close the tab with no window.
func hasRemote(t *layout.Tab) bool {
	for _, r := range t.Roots() {
		for _, l := range layout.Leaves(r) {
			if l.Host != "" && l.SID != "" {
				return true
			}
		}
	}
	return false
}

// hasLocal tells a tab with a session of this machine's, which the
// server's other clients see.
func hasLocal(t *layout.Tab) bool {
	for _, r := range t.Roots() {
		for _, l := range layout.Leaves(r) {
			if l.Host == "" && l.SID != "" {
				return true
			}
		}
	}
	return false
}

// indexOf returns the place of leaf n among a tree's leaves, -1 for none.
func indexOf(root, n *layout.Node) int { return slices.Index(layout.Leaves(root), n) }

// startPane starts the session of a new pane, as the window does.
func (s *Server) startPane(dir string, cols, rows int, command []string) (*layout.Node, error) {
	<-s.hostDone
	var env []string
	if s.host.Name != "" {
		env = []string{"GOREX_CLIENT=" + s.host.Name}
	}
	ss, err := s.createHeld(CreateOptions{Command: command, Dir: dir, Cols: cols, Rows: rows, Env: env}, "")
	if err != nil {
		return nil, err
	}
	in := ss.info()
	return &layout.Node{SID: ss.id, Dir: ss.dir, Cols: in.Cols, Rows: in.Rows}, nil
}

// sessionOf returns what the server knows of the session of a leaf of
// this machine's; ok is false for none.
func (s *Server) sessionOf(n *layout.Node) (SessionInfo, bool) {
	if n == nil || n.Host != "" || n.SID == "" {
		return SessionInfo{}, false
	}
	s.mu.Lock()
	ss := s.sessions[n.SID]
	s.mu.Unlock()
	if ss == nil {
		return SessionInfo{}, false
	}
	return ss.info(), true
}

// removeTab takes tab i out of w, as the window does.
func removeTab(w *layout.Workspace, i int) {
	w.Tabs = slices.Delete(w.Tabs, i, i+1)
	if w.Active > i || w.Active >= len(w.Tabs) {
		w.Active = max(w.Active-1, 0)
	}
}

// insertTab puts t in w at at, the active tab staying.
func insertTab(w *layout.Workspace, at int, t layout.Tab, focus bool) {
	w.Tabs = slices.Insert(w.Tabs, at, t)
	switch {
	case focus || len(w.Tabs) == 1:
		w.Active = at
	case at <= w.Active:
		w.Active++
	}
}

// focusOn gives the focus of tab t to its pane of session sid, tiled or
// floating; false when it has none.
func focusOn(t *layout.Tab, sid string) bool {
	if sid == "" {
		return false
	}
	if i := layout.LeafIndex(t.Root, sid); i >= 0 {
		t.Focus, t.FocusFloat = i, 0
		return true
	}
	for fi, f := range t.Floats {
		if layout.Find(f.Pane, sid) != nil {
			t.FocusFloat = fi + 1
			return true
		}
	}
	return false
}

// unlink takes pane p out of its tab, as closing it does, ending nothing:
// the other side of its split takes the split's place, and a focus it had
// goes to the pane of session target in the tab, else to the pane that
// took its place. The topmost float takes the place of a tab's last tiled
// pane, but on a canvas. It reports whether the tab is left empty.
func unlink(p spot, target string) bool {
	t := p.tab()
	if p.fi >= 0 {
		focused := t.FocusFloat == p.fi+1
		t.Floats = slices.Delete(t.Floats, p.fi, p.fi+1)
		switch {
		case t.FocusFloat > p.fi+1:
			t.FocusFloat--
		case focused:
			t.FocusFloat = 0
			if !focusOn(t, target) && t.Root == nil {
				t.FocusFloat = topFloat(t)
			}
		}
		return t.Root == nil && len(t.Floats) == 0
	}
	leaves := layout.Leaves(t.Root)
	var tiled *layout.Node
	if t.Focus >= 0 && t.Focus < len(leaves) {
		tiled = leaves[t.Focus]
	}
	focused := t.FocusFloat == 0 && tiled == p.n
	zoomed := t.Zoom && tiled == p.n
	keep := t.Remove(p.n)
	if t.Root == nil {
		t.Focus = 0
		switch {
		case len(t.Floats) == 0:
			return true
		case t.Canvas != nil:
			if focused && !focusOn(t, target) {
				t.FocusFloat = topFloat(t)
			}
		default:
			top := t.Floats[len(t.Floats)-1]
			t.Floats = t.Floats[:len(t.Floats)-1]
			t.Root, t.Zoom, t.FocusFloat = top.Pane, false, 0
		}
		return false
	}
	if zoomed {
		t.Zoom = false
	}
	if tiled == p.n {
		tiled = layout.FirstLeaf(keep)
	}
	t.Focus = max(indexOf(t.Root, tiled), 0)
	if focused {
		focusOn(t, target)
	}
	return false
}

// topFloat is the place, from 1, of the topmost float of t in sight, else
// of its first, as the window's focus goes when it has no tiled pane; 0
// with no float.
func topFloat(t *layout.Tab) int {
	for i := len(t.Floats) - 1; i >= 0; i-- {
		if !t.Floats[i].Hidden {
			return i + 1
		}
	}
	return min(len(t.Floats), 1)
}

// reveal shows float fi of t, above its others, with the focus.
func reveal(t *layout.Tab, fi int) {
	f := t.Floats[fi]
	f.Hidden = false
	t.Floats = append(slices.Delete(t.Floats, fi, fi+1), f)
	t.FocusFloat = len(t.Floats)
}

// canvasGap is the room between the boxes of a canvas, the window's gap
// between panes, in DIPs.
const canvasGap = 8

// placeOnCanvas puts leaf n on canvas tab t, in a box of of's size right
// of it, or below it, past the boxes there, as the window does.
func placeOnCanvas(t *layout.Tab, of layout.Float, n *layout.Node, below bool) {
	cv := t.Canvas
	gx, gy := float32(canvasGap)/max(cv.W, 1), float32(canvasGap)/max(cv.H, 1)
	f := layout.Float{Pane: n, X: of.X + of.W + gx, Y: of.Y, W: of.W, H: of.H}
	if below {
		f.X, f.Y = of.X, of.Y+of.H+gy
	}
	overlaps := func(g layout.Float) bool {
		return !g.Hidden && f.X < g.X+g.W && g.X < f.X+f.W && f.Y < g.Y+g.H && g.Y < f.Y+f.H
	}
	for range 64 {
		if !slices.ContainsFunc(t.Floats, overlaps) {
			break
		}
		if below {
			f.Y += f.H + gy
		} else {
			f.X += f.W + gx
		}
	}
	t.Floats = append(t.Floats, f)
	t.FocusFloat = len(t.Floats)
}

// resizeFloat changes a float's size by dw, dh, kept in the room, as the
// window does.
func resizeFloat(f *layout.Float, free bool, dw, dh float32) {
	f.W, f.H = max(f.W+dw, layout.FloatMin), max(f.H+dh, layout.FloatMin)
	if free {
		return
	}
	f.W, f.H = min(f.W, 1, 1-f.X), min(f.H, 1, 1-f.Y)
	if f.W < layout.FloatMin {
		f.W, f.X = layout.FloatMin, 1-layout.FloatMin
	}
	if f.H < layout.FloatMin {
		f.H, f.Y = layout.FloatMin, 1-layout.FloatMin
	}
}

// applyTo makes c on st, and returns the answer.
func (s *Server) applyTo(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	fail := func(code, msg string) LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	switch c.Op {
	case "space.create":
		w := newSpace(st, c.Name)
		if c.Space != "" && st.Space(c.Space) == nil {
			w.ID = c.Space
		}
		setColor(w, c.Color)
		res.Space = w.ID
		return res
	case "space.rename":
		w := st.Space(c.Space)
		if w == nil {
			return fail("space_not_found", "no such space in the GoRex layout")
		}
		if name := layout.Label(c.Name); name != "" {
			w.Name = name
		}
		setColor(w, c.Color)
		return res
	case "windows.set":
		var ids []string
		for _, id := range c.Spaces {
			if st.Space(id) == nil {
				return fail("space_not_found", "no such space in the GoRex layout")
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		st.Windows = ids
		return res
	case "tab.set":
		return s.setTab(st, c)
	case "space.close":
		w := st.Space(c.Space)
		if w == nil {
			return fail("space_not_found", "no such space in the GoRex layout")
		}
		for i := range w.Tabs {
			if hasRemote(&w.Tabs[i]) {
				return fail("desktop_unavailable", "the space has panes of another host, which the GoRex window ends")
			}
		}
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				a.end(r)
			}
		}
		st.Windows = slices.DeleteFunc(st.Windows, func(id string) bool { return id == c.Space })
		st.Sessions = slices.DeleteFunc(st.Sessions, func(o layout.Workspace) bool { return o.ID == c.Space })
		return res
	case "tab.create":
		return s.createTab(st, c, a)
	case "action":
		return floatState(st, c)
	}
	isTab := strings.HasPrefix(c.Op, "tab.")
	p, ok := find(st, c.Space, c.SID)
	if c.Tab != "" && isTab {
		ok = false
		for i := range st.Sessions {
			w := &st.Sessions[i]
			if c.Space != "" && w.ID != c.Space {
				continue
			}
			for ti := range w.Tabs {
				if w.Tabs[ti].ID == c.Tab {
					p, ok = spot{w: w, ti: ti, fi: -1}, true
				}
			}
		}
	}
	if !ok {
		if isTab {
			return fail("tab_not_found", "no such tab in the GoRex layout")
		}
		return fail("pane_not_found", "no such pane in the GoRex layout")
	}
	t := p.tab()
	switch c.Op {
	case "tab.close":
		if hasRemote(t) {
			return fail("desktop_unavailable", "the tab has panes of another host, which the GoRex window ends")
		}
		for _, r := range t.Roots() {
			a.end(r)
		}
		removeTab(p.w, p.ti)
	case "tab.rename":
		t.Name = layout.Label(c.Name)
	case "tab.move":
		res.Index = moveTab(p.w, p.ti, c.Index)
	case "pane.rename":
		p.n.Name = layout.PaneName(c.Name)
		res.Name = p.n.Name
	case "pane.split":
		return s.split(p, c, a)
	case "pane.close":
		a.end(p.n)
		if unlink(p, c.Target) {
			removeTab(p.w, p.ti)
		}
	case "pane.focus":
		p.w.Active = p.ti
		if p.fi >= 0 {
			reveal(t, p.fi)
			break
		}
		focusOn(t, p.n.SID)
	case "pane.zoom":
		if p.fi >= 0 {
			f := &t.Floats[p.fi]
			f.Zoom = c.Mode == "on" || c.Mode != "off" && !f.Zoom
			t.FocusFloat = p.fi + 1
			res.Zoomed = f.Zoom
			break
		}
		on := c.Mode == "on" || c.Mode != "off" && !t.Zoom
		t.Zoom = false
		if on && len(layout.Leaves(t.Root)) > 1 {
			focusOn(t, p.n.SID)
			t.Zoom = true
		}
		res.Zoomed = t.Zoom
	case "pane.resize":
		res.Changed, res.Reason = resize(p, c.Direction, c.Amount)
	case "pane.swap":
		q, ok := findIn(p.w, c.Target)
		switch {
		case !ok:
			res.Reason = "not_found"
		case q.n == p.n:
			res.Reason = "same_pane"
		case q.ti != p.ti:
			res.Reason = "cross_tab"
		case q.fi >= 0 || p.fi >= 0:
			res.Reason = "not_found"
		default:
			*p.n, *q.n = *q.n, *p.n
			focusOn(t, c.SID)
			res.Changed = true
		}
	case "pane.move":
		return movePane(p, c)
	default:
		return fail("unknown_op", "GoRex does not know "+c.Op)
	}
	return res
}

// newSpace adds a workspace of name, else a new one, and a new color.
func newSpace(st *layout.Store, name string) *layout.Workspace {
	taken := func(n string) bool {
		return slices.ContainsFunc(st.Sessions, func(w layout.Workspace) bool { return strings.EqualFold(w.Name, n) })
	}
	colors := make([]string, len(st.Sessions))
	for i, w := range st.Sessions {
		colors[i] = w.Color
	}
	w := layout.Workspace{ID: layout.NewSpaceID(), Name: layout.Label(name), Color: layout.NextColor(colors), Layout: layout.Layout{Tabs: []layout.Tab{}}}
	if w.Name == "" {
		w.Name = layout.SpaceName(taken)
	}
	st.Sessions = append(st.Sessions, w)
	return &st.Sessions[len(st.Sessions)-1]
}

// createTab makes tab.create: a tab of a new session, after the tab of
// After, else after this machine's tabs, which stay first.
func (s *Server) createTab(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	w := defaultSpace(st)
	if c.Space != "" {
		w = st.Space(c.Space)
		if w == nil {
			res.Code, res.Error = "space_not_found", "no such space in the GoRex layout"
			return res
		}
	}
	if w == nil {
		// A layout of no space, as a server's first: the window would
		// show one.
		w = newSpace(st, "")
	}
	dir, at := c.Dir, -1
	if p, ok := findIn(w, c.After); ok {
		at = p.ti + 1
		if dir == "" {
			if in, ok := s.sessionOf(p.n); ok {
				dir = in.Dir
			}
			dir = cmp.Or(dir, p.n.Dir)
		}
	}
	if at < 0 {
		at = 0
		for i := range w.Tabs {
			if tabHost(&w.Tabs[i]) == "" {
				at = i + 1
			}
		}
	}
	// The size of the pane the window shows, the active tab's focused one.
	var cols, rows int
	if w.Active >= 0 && w.Active < len(w.Tabs) {
		t := &w.Tabs[w.Active]
		focused := layout.FirstLeaf(t.Root)
		if leaves := layout.Leaves(t.Root); t.Focus >= 0 && t.Focus < len(leaves) {
			focused = leaves[t.Focus]
		}
		if f := t.FocusFloat; f > 0 && f <= len(t.Floats) {
			focused = t.Floats[f-1].Pane
		}
		if in, ok := s.sessionOf(focused); ok {
			cols, rows = in.Cols, in.Rows
		}
	}
	n, err := a.start(dir, cols, rows, c.Command)
	if err != nil {
		res.Code, res.Error = "create_failed", err.Error()
		return res
	}
	t := layout.Tab{ID: layout.NewTabID(), Name: layout.Label(c.Name), Root: n}
	insertTab(w, min(at, len(w.Tabs)), t, c.Focus)
	res.New, res.Tab = n.SID, t.ID
	return res
}

// split makes pane.split: a new session beside the pane, in its split of
// the half of its size, or beside its box on a canvas; a float of a tab
// of tiled panes splits the tab's focused tiled pane.
func (s *Server) split(p spot, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	t := p.tab()
	anchor, canvas := p.n, p.fi >= 0 && t.Canvas != nil
	if p.fi >= 0 && !canvas && !c.Float {
		leaves := layout.Leaves(t.Root)
		if len(leaves) == 0 {
			res.Code, res.Error = "create_failed", "GoRex cannot split the pane now"
			return res
		}
		anchor = leaves[min(max(t.Focus, 0), len(leaves)-1)]
		if anchor.Host != "" {
			res.Code, res.Error = "create_failed", "the tab's pane is another host's"
			return res
		}
	}
	dir := c.Dir
	cols, rows := 0, 0
	if in, ok := s.sessionOf(anchor); ok {
		dir = cmp.Or(dir, in.Dir)
		cols, rows = in.Cols, in.Rows
	}
	dir = cmp.Or(dir, anchor.Dir)
	switch {
	case cols <= 0 || rows <= 0:
		cols, rows = 80, 24
	case canvas || c.Float:
	case c.Vertical:
		rows = max(rows/2, 2)
	default:
		cols = max(cols/2, 10)
	}
	n, err := a.start(dir, cols, rows, c.Command)
	if err != nil {
		res.Code, res.Error = "create_failed", err.Error()
		return res
	}
	n.Name = layout.PaneName(c.Name)
	switch {
	case canvas:
		placeOnCanvas(t, t.Floats[p.fi], n, c.Vertical)
	case c.Float:
		// Over the tab's tiled panes, each new float offset from the last,
		// as the window opens one.
		d := float32(len(t.Floats)) * 0.03
		t.Floats = append(t.Floats, layout.Float{Pane: n, X: min(0.15+d, 0.3), Y: min(0.12+d, 0.28), W: 0.7, H: 0.72})
		t.FocusFloat = len(t.Floats)
	default:
		t.Replace(anchor, &layout.Node{Vertical: c.Vertical, Ratio: 0.5, A: anchor, B: n})
		t.Zoom, t.FocusFloat = false, 0
		t.Focus = indexOf(t.Root, n)
	}
	res.New = n.SID
	return res
}

// resize makes pane.resize: a float's box grows right and down, and
// shrinks left and up; a tiled pane moves the divider of its edge toward
// direction.
func resize(p spot, direction string, amount float32) (bool, string) {
	vertical := direction == "up" || direction == "down"
	if direction != "left" && direction != "right" && !vertical {
		return false, "unchanged"
	}
	amount = float32(math.Abs(float64(amount)))
	if amount == 0 || math.IsNaN(float64(amount)) || math.IsInf(float64(amount), 0) {
		amount = 0.05
	}
	amount = min(amount, 0.5)
	if direction == "left" || direction == "up" {
		amount = -amount
	}
	t := p.tab()
	if p.fi >= 0 {
		dw, dh := amount, float32(0)
		if vertical {
			dw, dh = 0, amount
		}
		f := &t.Floats[p.fi]
		w, h := f.W, f.H
		resizeFloat(f, t.Canvas != nil, dw, dh)
		if f.W == w && f.H == h {
			return false, "unchanged"
		}
		return true, ""
	}
	split := layout.SplitAt(t.Root, p.n, direction)
	if split == nil {
		return false, "unchanged"
	}
	old := split.Ratio
	split.Ratio = min(max(old+amount, 0.1), 0.9)
	if split.Ratio == old {
		return false, "unchanged"
	}
	return true, ""
}

// moveTab makes tab.move: the tab goes to place index among this machine's
// tabs, counted with it in its place, and the active tab stays. It returns
// the tab's place among them then.
func moveTab(w *layout.Workspace, ti, index int) int {
	var local []int
	for i := range w.Tabs {
		if hasLocal(&w.Tabs[i]) {
			local = append(local, i)
		}
	}
	at := min(max(index, 0), len(local))
	if slices.Index(local, ti) < at {
		at--
	}
	// Places in the tabs without the one moved.
	shift := func(i int) int {
		if i > ti {
			return i - 1
		}
		return i
	}
	local = slices.DeleteFunc(local, func(i int) bool { return i == ti })
	rest := slices.Delete(slices.Clone(w.Tabs), ti, ti+1)
	to := slices.IndexFunc(rest, func(t layout.Tab) bool { return tabHost(&t) == "" })
	if at > 0 {
		to = shift(local[at-1]) + 1
	}
	to = min(max(to, 0), len(rest))
	active := w.Active
	if active != ti {
		active = shift(active)
		if active >= to {
			active++
		}
	} else {
		active = to
	}
	w.Tabs = slices.Insert(rest, to, w.Tabs[ti])
	w.Active = active
	n := 0
	for i := range to {
		if hasLocal(&w.Tabs[i]) {
			n++
		}
	}
	return n
}

// movePane makes pane.move: the pane goes into a tab of its own after its
// tab, or beside the pane of Target, its session going on.
func movePane(p spot, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	w, src := p.w, p.tab()
	if src.Zoom {
		res.Reason = "zoomed_tab"
		return res
	}
	wasActive := w.Active == p.ti
	leaf := *p.n
	leaf.A, leaf.B = nil, nil
	if c.NewTab {
		empty := unlink(p, "")
		t := layout.Tab{ID: layout.NewTabID(), Name: layout.PaneName(c.Name), Root: &leaf}
		at := p.ti + 1
		insertTab(w, at, t, false)
		if empty {
			removeTab(w, p.ti)
			at--
			if wasActive {
				w.Active = at
			}
		}
		res.Changed = true
		return res
	}
	q, ok := findIn(w, c.Target)
	switch {
	case !ok || q.fi >= 0:
		res.Reason = "not_found"
		return res
	case q.ti == p.ti:
		res.Reason = "same_tab"
		return res
	case q.tab().Zoom:
		res.Reason = "zoomed_tab"
		return res
	}
	ratio := c.Ratio
	if ratio <= 0 || ratio > 1 || math.IsNaN(float64(ratio)) {
		ratio = 0.5
	}
	ratio = min(max(ratio, 0.1), 0.9)
	dst := q.tab()
	empty := unlink(p, "")
	dst.Replace(q.n, &layout.Node{Vertical: c.Direction == "down", Ratio: ratio, A: q.n, B: &leaf})
	dst.Zoom, dst.FocusFloat = false, 0
	dst.Focus = indexOf(dst.Root, &leaf)
	if empty {
		at := q.ti
		if at > p.ti {
			at--
		}
		removeTab(w, p.ti)
		if wasActive {
			w.Active = at
		}
	}
	res.Changed = true
	return res
}

// floatState makes the action float.set_state: a float shows, "normal",
// with the focus when asked, or hides, "minimized".
func floatState(st *layout.Store, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	var a struct {
		SID   string `json:"sid"`
		State string `json:"state"`
		Focus bool   `json:"focus"`
	}
	if len(c.Args) > 0 {
		if err := json.Unmarshal(c.Args, &a); err != nil {
			res.Code, res.Error = "bad_args", err.Error()
			return res
		}
	}
	if a.State != "normal" && a.State != "minimized" {
		res.Code, res.Error = "bad_args", "state is normal or minimized"
		return res
	}
	p, ok := find(st, c.Space, cmp.Or(a.SID, c.SID))
	if !ok || p.fi < 0 {
		res.Code, res.Error = "pane_not_found", "no floating pane"
		return res
	}
	t := p.tab()
	f := &t.Floats[p.fi]
	if a.State == "minimized" {
		if f.Hidden {
			return res
		}
		// At rest out of sight, it keeps no zoom; a focus it had goes to
		// the tiled panes, else to the topmost float in sight.
		f.Hidden, f.Zoom = true, false
		if t.FocusFloat == p.fi+1 {
			t.FocusFloat = 0
			if t.Root == nil {
				t.FocusFloat = topFloat(t)
			}
		}
		return res
	}
	if !a.Focus {
		f.Hidden = false
		return res
	}
	// It shows above the others, with the focus, in the active tab.
	reveal(t, p.fi)
	p.w.Active = p.ti
	return res
}

// setColor gives w the color of a command: "default" for the default
// look, "" for the one it has.
func setColor(w *layout.Workspace, color string) {
	switch color = strings.TrimSpace(color); color {
	case "":
	case "default":
		w.Color = ""
	default:
		w.Color = color
	}
}

// localSIDs are the sessions of this machine the layout's leaves have,
// each with its leaf.
func localSIDs(st *layout.Store) map[string]*layout.Node {
	out := map[string]*layout.Node{}
	for _, w := range st.Sessions {
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, l := range layout.Leaves(r) {
					if l.Host == "" && l.SID != "" {
						out[l.SID] = l
					}
				}
			}
		}
	}
	return out
}

// live tells a session of this machine the server runs, not exited.
func (s *Server) live(sid string) bool {
	in, ok := s.sessionOf(&layout.Node{SID: sid})
	return ok && !in.Exited
}

// wellFormed tells a tree whose every node is a leaf or a split of two.
func wellFormed(n *layout.Node) bool {
	if n == nil || n.IsLeaf() {
		return true
	}
	return n.A != nil && n.B != nil && wellFormed(n.A) && wellFormed(n.B)
}

// setTab makes tab.set: the tab of TabLayout's ID goes in place of the one
// of that ID, wherever it is, else in as a new one, in Space at Index, the
// active one with Focus or when it was. It arranges what the layout has:
// a session of this machine another tab has moves from there, and one
// that ended goes; a session no pane has, as one a remote client made,
// may come in, until the server places those itself. It starts and ends
// none: a running session the tab leaves out, a session twice, or one the
// server does not run is a conflict. Another host's panes, and those of no
// session, are the app's. The facts of a pane of this machine, its
// directory, size and program, are the server's.
func (s *Server) setTab(st *layout.Store, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	fail := func(code, msg string) LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	if c.TabLayout == nil || c.TabLayout.ID == "" {
		return fail("bad_args", "tab.set needs a tab with an ID")
	}
	t := *c.TabLayout
	if t.Root == nil && len(t.Floats) == 0 {
		return fail("bad_args", "tab.set needs a tab with a pane; tab.close closes one")
	}
	for _, r := range t.Roots() {
		if !wellFormed(r) {
			return fail("bad_args", "a split of the tab lacks a side")
		}
	}
	w := defaultSpace(st)
	if c.Space != "" {
		if w = st.Space(c.Space); w == nil {
			return fail("space_not_found", "no such space in the GoRex layout")
		}
	}
	if w == nil {
		w = newSpace(st, "")
	}
	before := localSIDs(st)
	focus := c.Focus
	for i := range st.Sessions {
		o := &st.Sessions[i]
		if ti := slices.IndexFunc(o.Tabs, func(x layout.Tab) bool { return x.ID == t.ID }); ti >= 0 {
			focus = focus || o == w && o.Active == ti
			removeTab(o, ti)
		}
	}
	mine := map[string]bool{}
	for _, r := range t.Roots() {
		for _, l := range layout.Leaves(r) {
			if l.Host != "" || l.SID == "" {
				continue
			}
			sid := l.SID
			if mine[sid] {
				return fail("conflict", "the tab has a session twice")
			}
			mine[sid] = true
			was, had := before[sid]
			if !had && !s.live(sid) {
				return fail("conflict", "no such session")
			}
			if p, ok := find(st, "", sid); ok && unlink(p, "") {
				removeTab(p.w, p.ti)
			}
			s.stamp(l, was)
		}
	}
	for sid := range before {
		if !mine[sid] && s.live(sid) {
			if _, ok := find(st, "", sid); !ok {
				return fail("conflict", "the tab leaves out a session that runs; pane.close ends one")
			}
		}
	}
	t.Focus = max(t.Focus, 0)
	t.FocusFloat = min(max(t.FocusFloat, 0), len(t.Floats))
	insertTab(w, min(max(c.Index, 0), len(w.Tabs)), t, focus)
	res.Tab = t.ID
	return res
}

// stamp gives leaf n of this machine the facts the server has of its
// session, as the app saves them, else those its leaf had, was.
func (s *Server) stamp(n, was *layout.Node) {
	in, ok := s.sessionOf(n)
	if !ok {
		if was != nil {
			n.Dir, n.Cols, n.Rows = was.Dir, was.Cols, was.Rows
			n.Prog, n.Args, n.AgentSession, n.AgentConfigDir = was.Prog, was.Args, was.AgentSession, was.AgentConfigDir
		}
		return
	}
	n.Dir = cmp.Or(in.Dir, n.Dir)
	if in.Cols > 0 && in.Rows > 0 {
		n.Cols, n.Rows = in.Cols, in.Rows
	}
	n.Prog, n.Args, n.AgentSession, n.AgentConfigDir = "", nil, "", ""
	if !in.Idle && (len(in.Args) > 0 || in.Program != "") {
		n.Prog, n.Args = in.Program, in.Args
		n.AgentSession, n.AgentConfigDir = in.AgentSession, in.AgentConfigDir
	}
}
