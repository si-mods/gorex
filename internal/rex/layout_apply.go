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
	"time"

	"gorex/internal/layout"
)

// applied are the layout commands the server makes itself (apply); the
// others, actions but float.set_state, and config.reload, need the
// window.
var applied = map[string]bool{
	"space.create": true, "space.rename": true, "space.close": true,
	"tab.create": true, "tab.close": true, "tab.rename": true, "tab.move": true,
	"pane.split": true, "pane.close": true, "pane.rename": true, "pane.focus": true,
	"pane.zoom": true, "pane.resize": true, "pane.swap": true, "pane.move": true,
	"pane.restart": true, "pane.teleporting": true, "pane.teleported": true,
	"tab.set": true, "windows.set": true, "layout.apply": true, "kills.taken": true, "show.taken": true,
	"tab.to_space": true,
}

// batched are the commands a layout.apply carries: the app's changes of
// the layout that start no session.
var batched = map[string]bool{
	"space.create": true, "space.rename": true, "space.close": true, "windows.set": true,
	"tab.set": true, "tab.close": true, "pane.close": true,
}

// applies tells a command the server makes itself.
func applies(c LayoutCommand) bool {
	return applied[c.Op] || c.Op == "action" && c.Action == "float.set_state"
}

// apply makes a change of the layout a client asks for: on the layout the
// server keeps, starting and ending the sessions of its panes here.
func (s *Server) apply(c LayoutCommand) LayoutCommand {
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

// applyHeld makes c under the locks (change).
func (s *Server) applyHeld(c LayoutCommand, a *applying) LayoutCommand {
	res := s.change(a, func(st *layout.Store) LayoutCommand { return s.applyTo(st, c, a) })
	res.ID = c.ID
	return res
}

// change makes f's change of the layout under the locks: an upgrade waits
// for the change, its sessions and its layout alike, and the changes take
// their turns. A layout another client set meanwhile, as the app's whole
// layout, makes it again on that one. What it commits has the facts of
// the sessions now (stampAll).
func (s *Server) change(a *applying, f func(st *layout.Store) LayoutCommand) LayoutCommand {
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
		a.n, a.ends, a.kills, a.touched = 0, nil, nil, nil
		res := f(&st)
		if res.Code != "" {
			return res
		}
		st.Kills = append(st.Kills, a.kills...)
		res.Revs = a.countRevs(&st)
		// A command that changes nothing, as a resize at its bound,
		// leaves the layout as it was, an older one unmigrated.
		after := encodeLayout(&st)
		if bytes.Equal(before, after) {
			return res
		}
		if s.stampAll(&st) {
			after = encodeLayout(&st)
		}
		if applyHook != nil {
			applyHook()
		}
		if _, err := s.commitLayout(after, rev); err == nil {
			return res
		}
	}
	return LayoutCommand{Code: "layout_conflict", Error: "the layout kept changing; try again"}
}

// touch notes the tab t as it is, before the change changes it: its
// revision counts up once the change leaves it otherwise (countRevs). A
// new tab, of no tab before, is touched by its ID with t nil.
func (a *applying) touch(id string, t *layout.Tab) {
	if a.touched == nil {
		a.touched = map[string]touchedTab{}
	}
	if _, ok := a.touched[id]; ok {
		return
	}
	var was touchedTab
	if t != nil {
		was = touchedTab{rev: t.Rev, arrangement: t.Arrangement()}
	}
	a.touched[id] = was
}

// touchedTab is a tab as it was before a change: its revision and its
// arrangement, nil for a tab new to the layout.
type touchedTab struct {
	rev         uint64
	arrangement []byte
}

// countRevs counts up the revision of each tab the change touched whose
// arrangement it changed, and returns them.
func (a *applying) countRevs(st *layout.Store) map[string]uint64 {
	var revs map[string]uint64
	for i := range st.Sessions {
		for j := range st.Sessions[i].Tabs {
			t := &st.Sessions[i].Tabs[j]
			was, ok := a.touched[t.ID]
			if !ok {
				continue
			}
			t.Rev = was.rev
			if was.arrangement != nil && bytes.Equal(was.arrangement, t.Arrangement()) {
				continue
			}
			t.Rev++
			if revs == nil {
				revs = map[string]uint64{}
			}
			revs[t.ID] = t.Rev
		}
	}
	return revs
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
	// touched are the tabs the change touched, by ID, as they were; batch
	// tells the changes of a layout.apply, whose sessions are checked
	// once, on the layout they make together, and before are the sessions
	// of the layout before it, with their leaves.
	touched map[string]touchedTab
	batch   bool
	before  map[string]*layout.Node
	ends    []string

	// kills are the sessions of other hosts' panes the pass closed.
	kills []layout.Kill
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

// end ends the sessions of the panes under n, once the layout is saved
// without them: this machine's here, another host's through the app
// (layout.Store.Kills), but in a batch, the app's, which ends those itself
// as its user closes them.
func (a *applying) end(n *layout.Node) {
	for _, l := range layout.Leaves(n) {
		switch {
		case l.SID == "":
		case l.Host == "":
			a.ends = append(a.ends, l.SID)
		case !a.batch:
			a.kills = append(a.kills, layout.Kill{Host: l.Host, SID: l.SID})
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
		if p, ok := findIn(w, localLeaf(sid)); ok {
			return p, true
		}
	}
	return spot{}, false
}

// findIn returns the first leaf of w that match takes.
func findIn(w *layout.Workspace, match func(*layout.Node) bool) (spot, bool) {
	for ti := range w.Tabs {
		t := &w.Tabs[ti]
		for fi := -1; fi < len(t.Floats); fi++ {
			r := t.Root
			if fi >= 0 {
				r = t.Floats[fi].Pane
			}
			for _, l := range layout.Leaves(r) {
				if match(l) {
					return spot{w, ti, l, fi}, true
				}
			}
		}
	}
	return spot{}, false
}

// localLeaf matches the leaf of this machine's session sid; none for "".
func localLeaf(sid string) func(*layout.Node) bool {
	return func(n *layout.Node) bool { return sid != "" && n.Host == "" && n.SID == sid }
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

// toSpace makes tab.to_space: the tab of p goes, as it is, to the space
// c.ToSpace, before its tab c.Target, else at its end, as the app's Move
// to Space does. That space's active tab stays; the space it leaves gives
// its active tab to the next, and goes once it has no tab. It starts and
// ends no session.
func toSpace(st *layout.Store, p spot, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	to := st.Space(c.ToSpace)
	switch {
	case to == nil:
		res.Code, res.Error = "space_not_found", "no such space in the GoRex layout"
		return res
	case to == p.w:
		res.Code, res.Error = "same_space", "the tab is in that space already"
		return res
	}
	at := len(to.Tabs)
	if c.Target != "" {
		at = slices.IndexFunc(to.Tabs, func(x layout.Tab) bool { return x.ID == c.Target })
		if at < 0 {
			if q, ok := findIn(to, localLeaf(c.Target)); ok {
				at = q.ti
			}
		}
		if at < 0 {
			res.Code, res.Error = "tab_not_found", "no such tab in the space"
			return res
		}
	}
	t := *p.tab()
	removeTab(p.w, p.ti)
	insertTab(to, at, t, false)
	res.Tab = t.ID
	if len(p.w.Tabs) == 0 {
		gone := p.w.ID
		st.Windows = slices.DeleteFunc(st.Windows, func(id string) bool { return id == gone })
		st.Sessions = slices.DeleteFunc(st.Sessions, func(o layout.Workspace) bool { return o.ID == gone })
		if st.Show == gone {
			st.Show, st.ShowAt = "", time.Time{}
		}
	}
	return res
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

// focused is the focused pane of t, and its place among the floats, -1
// for a tiled one; nil for none.
func focused(t *layout.Tab) (*layout.Node, int) {
	if f := t.FocusFloat; f > 0 && f <= len(t.Floats) {
		return t.Floats[f-1].Pane, f - 1
	}
	if leaves := layout.Leaves(t.Root); len(leaves) > 0 {
		return leaves[min(max(t.Focus, 0), len(leaves)-1)], -1
	}
	if n := len(t.Floats); n > 0 {
		return t.Floats[n-1].Pane, n - 1
	}
	return nil, -1
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
	case "show.taken":
		if st.Show == c.Space {
			st.Show, st.ShowAt = "", time.Time{}
		}
		return res
	case "tab.set":
		return s.setTab(st, c, a)
	case "space.close":
		w := st.Space(c.Space)
		if w == nil {
			return fail("space_not_found", "no such space in the GoRex layout")
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
		return floatState(st, c, a)
	case "layout.apply":
		return s.applyBatch(st, c, a)
	case "pane.teleporting":
		return s.teleporting(st, c)
	case "pane.teleported":
		return s.teleported(st, c, a)
	case "kills.taken":
		st.Kills = slices.DeleteFunc(st.Kills, func(k layout.Kill) bool { return slices.Contains(c.Kills, k) })
		return res
	}
	isTab := strings.HasPrefix(c.Op, "tab.")
	p, ok := find(st, c.Space, c.SID)
	// A tab of its ID; a split by the tab, as of a pane of no session,
	// splits its focused pane.
	if c.Tab != "" && (isTab || c.Op == "pane.split" && c.SID == "") {
		ok = false
		for i := range st.Sessions {
			w := &st.Sessions[i]
			if c.Space != "" && w.ID != c.Space {
				continue
			}
			for ti := range w.Tabs {
				if w.Tabs[ti].ID == c.Tab {
					n, fi := focused(&w.Tabs[ti])
					p, ok = spot{w, ti, n, fi}, n != nil || isTab
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
	// A pane.close of the tab the app names, built on it as it was: one
	// whose pane went to another tab meanwhile changes nothing.
	if c.Op == "pane.close" && c.Tab != "" && t.ID != c.Tab {
		return fail("conflict", "the pane is in another tab now")
	}
	if c.Base != 0 && t.Rev != c.Base && (isTab || c.Op == "pane.close") {
		return fail("conflict", "the tab changed meanwhile")
	}
	a.touch(t.ID, t)
	switch c.Op {
	case "tab.close":
		for _, r := range t.Roots() {
			a.end(r)
		}
		removeTab(p.w, p.ti)
	case "tab.rename":
		t.Name = layout.Label(c.Name)
	case "tab.move":
		res.Index = moveTab(p.w, p.ti, c.Index)
	case "tab.to_space":
		return toSpace(st, p, c)
	case "pane.rename":
		p.n.Name = layout.PaneName(c.Name)
		res.Name = p.n.Name
	case "pane.split":
		return s.split(p, c, a)
	case "pane.restart":
		return s.restart(p, c, a)
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
		q, ok := findIn(p.w, localLeaf(c.Target))
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
		return movePane(p, c, a)
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
	if p, ok := findIn(w, localLeaf(c.After)); ok {
		at = p.ti + 1
		if dir == "" {
			if in, ok := s.sessionOf(p.n); ok {
				dir = in.Dir
			}
			dir = cmp.Or(dir, p.n.Dir)
		}
	}
	if at < 0 {
		at = afterLocal(w)
	}
	// The room of the tab the window shows, all its tiled panes', so that
	// a tab made in the background does not start at a narrow split's size
	// and wrap what it prints; else the largest pane of the space.
	var cols, rows int
	if w.Active >= 0 && w.Active < len(w.Tabs) {
		cols, rows = s.area(w.Tabs[w.Active].Root)
	}
	if cols <= 0 || rows <= 0 {
		cols, rows = s.largest(w)
	}
	n, err := a.start(dir, cols, rows, c.Command)
	if err != nil {
		res.Code, res.Error = "create_failed", err.Error()
		return res
	}
	t := layout.Tab{ID: layout.NewTabID(), Name: layout.Label(c.Name), Root: n}
	a.touch(t.ID, nil)
	insertTab(w, min(at, len(w.Tabs)), t, c.Focus)
	if c.Focus && !slices.Contains(st.Windows, w.ID) {
		st.Show, st.ShowAt = w.ID, time.Now()
	}
	res.New, res.Tab = n.SID, t.ID
	return res
}

// splitCost is what a split takes of its room, in cells, as the window
// lays it out at its font: the gap between the sides and their padding,
// and below the lower side's header too; 135 columns split into 65 and 65,
// 33 rows into 14 and 14.
const splitCost = 5

// area is the size, in cells, of the room of the tiled panes under n, as
// the window lays them out: a split's sides, side by side or one above
// the other, with what the split takes between them (splitCost); 0, 0
// when no pane under n has a size.
func (s *Server) area(n *layout.Node) (cols, rows int) {
	if n == nil {
		return 0, 0
	}
	if n.IsLeaf() {
		return s.sizeOf(n)
	}
	ac, ar := s.area(n.A)
	bc, br := s.area(n.B)
	switch {
	case ac <= 0 || ar <= 0:
		return bc, br
	case bc <= 0 || br <= 0:
		return ac, ar
	case n.Vertical:
		return max(ac, bc), ar + br + splitCost
	}
	return ac + bc + splitCost, max(ar, br)
}

// sizeOf is the size of leaf n: its session's, else the one the layout
// keeps, as of another host's pane; 0, 0 for none, as a page's.
func (s *Server) sizeOf(n *layout.Node) (cols, rows int) {
	if n.Host == "" && n.SID != "" {
		s.mu.Lock()
		ss := s.sessions[n.SID]
		s.mu.Unlock()
		if ss != nil {
			ss.mu.Lock()
			defer ss.mu.Unlock()
			return ss.cols, ss.rows
		}
	}
	return n.Cols, n.Rows
}

// largest is the size of the largest pane of w, tiled or floating; 0, 0
// for none.
func (s *Server) largest(w *layout.Workspace) (cols, rows int) {
	for _, t := range w.Tabs {
		for _, r := range t.Roots() {
			for _, l := range layout.Leaves(r) {
				if c, r := s.sizeOf(l); c*r > cols*rows {
					cols, rows = c, r
				}
			}
		}
	}
	return cols, rows
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

// restart makes pane.restart: a new session, of the user's shell unless
// Command says, takes the place of the pane's, in its directory and at its
// size, with the focus; the pane's own ends.
func (s *Server) restart(p spot, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	dir, cols, rows := c.Dir, p.n.Cols, p.n.Rows
	if in, ok := s.sessionOf(p.n); ok {
		dir, cols, rows = cmp.Or(dir, in.Dir), in.Cols, in.Rows
	}
	n, err := a.start(cmp.Or(dir, p.n.Dir), cols, rows, c.Command)
	if err != nil {
		res.Code, res.Error = "create_failed", err.Error()
		return res
	}
	a.end(p.n)
	n.Name = p.n.Name
	*p.n = *n
	focusOn(p.tab(), n.SID)
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
// tab, or beside the tiled pane of Target, in its tab or another, its
// session going on. Direction is the side of Target it goes on: "left"
// or "up" puts it first, "down" below, else right; Ratio is the first
// side's share, as the window's movePaneBeside places a dropped pane.
func movePane(p spot, c LayoutCommand, a *applying) LayoutCommand {
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
		a.touch(t.ID, nil)
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
	q, ok := findIn(w, localLeaf(c.Target))
	switch {
	case !ok || q.fi >= 0:
		res.Reason = "not_found"
		return res
	case q.n == p.n:
		res.Reason = "same_pane"
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
	a.touch(dst.ID, dst)
	// In its own tab, taking it out leaves q.n where it was, and the tab
	// keeps a pane: Target's.
	empty := unlink(p, "")
	sp := &layout.Node{Vertical: c.Direction == "down" || c.Direction == "up", Ratio: ratio, A: q.n, B: &leaf}
	if c.Direction == "left" || c.Direction == "up" {
		sp.A, sp.B = &leaf, q.n
	}
	dst.Replace(q.n, sp)
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
func floatState(st *layout.Store, c LayoutCommand, ap *applying) LayoutCommand {
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
	ap.touch(t.ID, t)
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
// a session another tab has, this machine's or another host's, moves from
// there, and one that ended goes. It starts, ends and places none: a
// running session the tab leaves out, a session twice, or one no pane has
// is a conflict. Another host's panes, and those of no session, are the
// app's. The facts of a pane of this machine, its directory, size and
// program, are the server's.
func (s *Server) setTab(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	fail := func(code, msg string) LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	if c.TabLayout == nil || c.TabLayout.ID == "" {
		return fail("bad_args", "tab.set needs a tab with an ID")
	}
	t := *c.TabLayout
	t.Rev = 0
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
	// A batch's tab may take a session another of its tab.sets left out.
	before := a.before
	if !a.batch {
		before = localSIDs(st)
	}
	focus, at := c.Focus, -1
	// ahead are the tabs before the one replaced, by ID: the unlinks of
	// the sessions it takes may close one.
	var ahead []string
	for i := range st.Sessions {
		o := &st.Sessions[i]
		if ti := slices.IndexFunc(o.Tabs, func(x layout.Tab) bool { return x.ID == t.ID }); ti >= 0 {
			if c.Base != 0 && o.Tabs[ti].Rev != c.Base {
				return fail("conflict", "the tab changed meanwhile")
			}
			a.touch(t.ID, &o.Tabs[ti])
			t.Rev = o.Tabs[ti].Rev
			if o == w {
				for _, x := range o.Tabs[:ti] {
					ahead = append(ahead, x.ID)
				}
				at, focus = ti, focus || o.Active == ti
			}
			removeTab(o, ti)
		}
	}
	a.touch(t.ID, nil)
	mine := map[string]bool{}
	for _, r := range t.Roots() {
		for _, l := range layout.Leaves(r) {
			if l.SID == "" {
				continue
			}
			// Another host's session moves here too, as one the app placed
			// as the server did.
			if l.Host != "" {
				if p, ok := findLeaf(st, hostLeaf(l.Host, l.SID)); ok {
					a.touch(p.tab().ID, p.tab())
					if unlink(p, "") {
						removeTab(p.w, p.ti)
					}
				}
				continue
			}
			sid := l.SID
			if mine[sid] {
				return fail("conflict", "the tab has a session twice")
			}
			mine[sid] = true
			was, had := before[sid]
			if !had {
				return fail("conflict", "no pane has the session; the server places its sessions")
			}
			if p, ok := find(st, "", sid); ok {
				a.touch(p.tab().ID, p.tab())
				if unlink(p, "") {
					removeTab(p.w, p.ti)
				}
			}
			s.stamp(l, was)
		}
	}
	// A batch's sessions are checked on the layout it makes (applyBatch).
	for sid := range before {
		if !a.batch && !mine[sid] && s.live(sid) {
			if _, ok := find(st, "", sid); !ok {
				return fail("conflict", "the tab leaves out a session that runs; pane.close ends one")
			}
		}
	}
	t.Focus = max(t.Focus, 0)
	t.FocusFloat = min(max(t.FocusFloat, 0), len(t.Floats))
	if at >= 0 {
		at = 0
		for _, id := range ahead {
			if i := slices.IndexFunc(w.Tabs, func(x layout.Tab) bool { return x.ID == id }); i >= 0 {
				at = i + 1
			}
		}
	}
	// After the tab of After; with none there, where it was, a new one
	// after this machine's tabs; with no After, at Index.
	switch i := slices.IndexFunc(w.Tabs, func(x layout.Tab) bool { return x.ID == c.After }); {
	case c.After == "":
		at = c.Index
	case i >= 0:
		at = i + 1
	case at < 0:
		at = afterLocal(w)
	}
	insertTab(w, min(max(at, 0), len(w.Tabs)), t, focus)
	res.Tab = t.ID
	return res
}

// stamp gives leaf n of this machine the facts the server has of its
// session, else those its leaf had, was, when not nil: its directory, its
// size, and the program in its foreground, not the shell at its prompt,
// as the session last sampled them (session.facts): a stamp inspects no
// process. A shell that ran nothing yet, as one started again after a
// crash, keeps the program the pane ran, which the window offers to run
// again.
func (s *Server) stamp(n, was *layout.Node) {
	if was != nil {
		n.Dir, n.Cols, n.Rows = was.Dir, was.Cols, was.Rows
		n.Prog, n.Args, n.AgentSession, n.AgentConfigDir = was.Prog, was.Args, was.AgentSession, was.AgentConfigDir
	}
	if n.Host != "" || n.SID == "" {
		return
	}
	s.mu.Lock()
	ss := s.sessions[n.SID]
	s.mu.Unlock()
	if ss == nil {
		return
	}
	in, sampled := ss.facts()
	if in.Exited {
		return
	}
	if in.Cols > 0 && in.Rows > 0 {
		n.Cols, n.Rows = in.Cols, in.Rows
	}
	if !sampled {
		return
	}
	n.Dir = cmp.Or(in.Dir, n.Dir)
	if in.Idle && !in.ran {
		return
	}
	n.Prog, n.Args, n.AgentSession, n.AgentConfigDir = "", nil, "", ""
	if !in.Idle && (len(in.Args) > 0 || in.Program != "") {
		n.Prog, n.Args = in.Program, in.Args
		n.AgentSession, n.AgentConfigDir = in.AgentSession, in.AgentConfigDir
	}
}

// stampAll stamps every leaf of this machine in st with its session's
// facts (stamp), and reports whether any changed.
func (s *Server) stampAll(st *layout.Store) bool {
	changed := false
	for _, n := range localSIDs(st) {
		was := *n
		s.stamp(n, nil)
		if n.Dir != was.Dir || n.Cols != was.Cols || n.Rows != was.Rows || n.Prog != was.Prog ||
			!slices.Equal(n.Args, was.Args) || n.AgentSession != was.AgentSession || n.AgentConfigDir != was.AgentConfigDir {
			changed = true
		}
	}
	return changed
}

// applyBatch makes layout.apply: the changes of Batch, of the app's, all
// of them or none. Each is built on its tab as the layout had it before
// the batch (Base); the sessions they leave are checked once, on the
// layout they make together: none twice, and none that runs gone but
// those the batch closes.
func (s *Server) applyBatch(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	fail := func(code, msg string) LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	revs := map[string]uint64{}
	for _, w := range st.Sessions {
		for _, t := range w.Tabs {
			revs[t.ID] = t.Rev
		}
	}
	before := localSIDs(st)
	a.batch, a.before = true, before
	defer func() { a.batch, a.before = false, nil }()
	for _, x := range c.Batch {
		if !batched[x.Op] {
			return fail("bad_args", x.Op+" is not of a layout.apply")
		}
		// The close of what another client closed first is made already.
		if x.Op == "tab.close" && !hasTab(st, x.Tab) {
			continue
		}
		if _, ok := find(st, "", x.SID); x.Op == "pane.close" && !ok {
			continue
		}
		if id := batchTab(x); x.Base != 0 && revs[id] != x.Base {
			return fail("conflict", "the tab changed meanwhile")
		}
		x.Base = 0 // checked
		if r := s.applyTo(st, x, a); r.Code != "" {
			return r
		}
	}
	ended := map[string]bool{}
	for _, sid := range a.ends {
		ended[sid] = true
	}
	seen := map[string]bool{}
	for _, w := range st.Sessions {
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, l := range layout.Leaves(r) {
					if l.Host != "" || l.SID == "" {
						continue
					}
					if seen[l.SID] {
						return fail("conflict", "a session is in two panes")
					}
					seen[l.SID] = true
				}
			}
		}
	}
	for sid := range before {
		if !seen[sid] && !ended[sid] && s.live(sid) {
			return fail("conflict", "the change leaves out a session that runs")
		}
	}
	return res
}

// hasTab tells a tab of the ID in st.
func hasTab(st *layout.Store, id string) bool {
	for _, w := range st.Sessions {
		if slices.ContainsFunc(w.Tabs, func(t layout.Tab) bool { return t.ID == id }) {
			return true
		}
	}
	return false
}

// batchTab is the tab a change of a batch is built on.
func batchTab(x LayoutCommand) string {
	if x.Op == "tab.set" && x.TabLayout != nil {
		return x.TabLayout.ID
	}
	return x.Tab
}

// countSetRevs is the whole layout l a client sets, with the revisions of
// its tabs the server's: that of the tab of its ID in was, the layout
// before, counted up for a tab whose arrangement l changes; a client's
// own are ignored. A layout whose revisions need no change, as one of
// tabs of no IDs, is kept as it is; another goes as parsed.
func countSetRevs(was, l json.RawMessage) json.RawMessage {
	old := map[string]layout.Tab{}
	for _, w := range layout.Parse(was).Sessions {
		for _, t := range w.Tabs {
			old[t.ID] = t
		}
	}
	now := layout.Parse(l)
	changed := false
	for i := range now.Sessions {
		for j := range now.Sessions[i].Tabs {
			t := &now.Sessions[i].Tabs[j]
			if t.ID == "" {
				continue // of no ID a change names it by
			}
			o, had := old[t.ID]
			rev := o.Rev
			if !had || !bytes.Equal(o.Arrangement(), t.Arrangement()) {
				rev++
			}
			if rev != t.Rev {
				t.Rev, changed = rev, true
			}
		}
	}
	if !changed {
		return bytes.Clone(l)
	}
	return encodeLayout(&now)
}
