package main

import (
	"slices"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// The saved layout: the tabs, their splits and the sessions of their
// panes, which the server keeps, and the app restores when it starts.
type savedLayout = layout.Layout
type savedTab = layout.Tab
type savedNode = layout.Node
type savedFloat = layout.Float

func (a *App) snapshot() savedLayout { return a.layoutOf(a.tabs, a.active) }

// layoutOf is the layout of the tabs, of which active is shown.
func (a *App) layoutOf(tabs []*Tab, active int) savedLayout {
	l := savedLayout{Active: active, Tabs: []savedTab{}}
	for _, t := range tabs {
		l.Tabs = append(l.Tabs, a.savedTab(t))
	}
	return l
}

// savedTab is a tab as the layout keeps it.
func (a *App) savedTab(t *Tab) savedTab {
	var save func(n *Node) *savedNode
	save = func(n *Node) *savedNode {
		if n == nil {
			return nil
		}
		if p := n.Pane; p != nil {
			if p.ext != nil {
				s := &savedNode{Name: p.Name}
				p.ext.save(p, s)
				return s
			}
			s := &savedNode{SID: p.SID, Name: p.Name, Dir: p.info.Dir}
			if p.host != nil {
				s.Host = p.host.ID
			}
			if s.Dir == "" {
				s.Dir = p.startDir
			}
			if cols, rows := p.paneSize(); cols > 0 && rows > 0 {
				s.Cols, s.Rows = cols, rows
			}
			// What ran there, not the shell at its prompt: a pane whose
			// session is lost offers to run it again. A host's sessions
			// stay on their machine, and the pane attaches to one again.
			if p.host == nil && !p.info.Idle && (len(p.info.Args) > 0 || p.info.Program != "") {
				s.Prog, s.Args = p.info.Program, p.info.Args
				s.AgentSession, s.AgentConfigDir = p.info.AgentSession, p.info.AgentConfigDir
			}
			return s
		}
		return &savedNode{Vertical: n.Vertical, Ratio: n.Ratio, A: save(n.A), B: save(n.B)}
	}
	st := savedTab{ID: t.UID, Name: t.Name, Root: save(t.Root), Zoom: t.Zoom != nil}
	if t.canvas != nil {
		cv := *t.canvas
		st.Canvas = &cv
	}
	for _, f := range t.Floats {
		if f.Pane == nil || f.Pane.Node == nil {
			continue
		}
		st.Floats = append(st.Floats, layout.Float{
			Pane: save(f.Pane.Node), X: f.X, Y: f.Y, W: f.W, H: f.H, Hidden: f.Hidden, Zoom: f.Zoom,
		})
	}
	if t.Focus != nil && t.Focus.float != nil {
		st.FocusFloat = slices.Index(t.Floats, t.Focus.float) + 1
		// The tiled focus is the zoomed pane, when one is, which the
		// layout keeps as the focus.
		q := t.Zoom
		if q == nil {
			q = t.latestTiled()
		}
		if q != nil {
			st.Focus = max(slices.Index(t.panes(), q), 0)
		}
	} else {
		st.Focus = max(slices.Index(t.panes(), t.Focus), 0)
	}
	return st
}

// changed notes that the layout changed, to save it soon.
func (a *App) changed() { a.saveDue = true }

// save sends the layout of every workspace to the server, which keeps it
// on disk.
func (a *App) save() {
	a.saveDue = false
	a.lastSave = time.Now()
	if a.st != nil {
		a.st.save()
	}
}

// reconcile shows the tabs of layout l in the window, attached to the
// sessions the server has, keeping what the window has of them already: a
// tab by its ID, a pane by its key (paneKey), terminal, page and note as
// they are. A pane new to the window attaches, to a session the server
// started again in place of one a run lost too; one l no longer has
// closes, its session going on. The active tab and the tabs' focus are
// l's, and the keyboard moves only with a focus that moved. It reports
// whether the window has a tab. A window with no tabs, as one that opens,
// restores l. A canvas the window shows keeps its pan and zoom, which are
// its own.
func (a *App) reconcile(l savedLayout) bool {
	if a.client == nil || len(a.tabs) == 0 && len(l.Tabs) == 0 {
		return false
	}
	wasTab := a.tab()
	var was *Pane
	if wasTab != nil {
		was = wasTab.Focus
	}
	tabs := map[string]*Tab{}
	index := map[string]*Pane{}
	var before []*Pane
	for _, t := range a.tabs {
		tabs[t.UID] = t
		for _, p := range t.allPanes() {
			before = append(before, p)
			if k := p.key(); k != "" && index[k] == nil {
				index[k] = p
			}
		}
	}
	kept := map[*Pane]bool{}
	used := map[string]bool{}
	// The splits the window has, by their sides: one of the same two sides
	// stays, as its divider does.
	parents := map[*Node]*Node{}
	for _, t := range a.tabs {
		var walk func(n *Node)
		walk = func(n *Node) {
			if n != nil && n.A != nil && n.B != nil {
				parents[n.A], parents[n.B] = n, n
				walk(n.A)
				walk(n.B)
			}
		}
		walk(t.Root)
	}
	// What the server runs, as the windows last polled it: a session the
	// list does not have yet, as one a client just made, asks it again.
	live := map[string]rex.SessionInfo{}
	listed := false
	list := func() {
		if listed {
			return
		}
		listed = true
		if infos, err := a.client.List(); err == nil {
			for _, in := range infos {
				if !in.Exited {
					live[in.ID] = in
				}
			}
		}
	}
	if a.st != nil && a.st.infos != nil {
		for id, in := range a.st.infos {
			if !in.Exited {
				live[id] = in
			}
		}
	} else {
		list()
	}
	// A pane of a host attaches once the host connects, and closes then
	// if its session is gone; one of a host no longer saved goes now.
	var load func(s *savedNode, t *Tab, parent *Node) *Node
	load = func(s *savedNode, t *Tab, parent *Node) *Node {
		if s.A == nil || s.B == nil {
			if p := index[savedNodeKey(s)]; p != nil && !kept[p] {
				kept[p] = true
				if s.Host == "" && s.SID != "" {
					used[s.SID] = true
				}
				p.Name, p.Tab = s.Name, t
				if p.ext != nil {
					p.ext.apply(a, p, s)
				}
				n := p.Node
				if n == nil {
					n = &Node{ID: a.id()}
				}
				n.Pane, n.Parent, n.A, n.B = p, parent, nil, nil
				p.Node = n
				return n
			}
			n := &Node{ID: a.id(), Parent: parent}
			var p *Pane
			in, isLive := live[s.SID]
			if !isLive && s.Host == "" && s.SID != "" {
				list()
				in, isLive = live[s.SID]
			}
			if p = a.extPaneLoad(s, func(s *savedNode) bool { return index[savedNodeKey(s)] != nil }); p != nil {
				// An extension pane: a browser, a note, a pack's, or the
				// placeholder of a kind the build does not have.
			} else {
				switch {
				case s.Host != "":
					h := a.hostByID(s.Host)
					if h == nil || s.SID == "" {
						return nil
					}
					// What the host's last list says of it, as this machine's
					// panes start from theirs, lest the next poll read it as news.
					in, ok := h.listed(s.SID)
					if !ok {
						in = rex.SessionInfo{ID: s.SID, Dir: s.Dir}
					}
					p = &Pane{ID: a.id(), SID: s.SID, Name: s.Name, host: h, info: in, startDir: s.Dir}
					a.attach(p, s.Cols, s.Rows)
				case isLive && !used[s.SID]:
					used[s.SID] = true
					p = &Pane{ID: a.id(), SID: s.SID, Name: s.Name, info: in, startDir: s.Dir}
					a.attach(p, in.Cols, in.Rows)
					// A session the server started again after a crash or a
					// reboot is the shell the pane's program ran in: back at
					// its prompt, the pane offers the program again.
					if in.Idle {
						p.resume = resumeHintFor(in.Shell, s)
					}
				default:
					// A session the server does not run, as one that just
					// ended, whose pane the server takes out: the pane is it
					// till then.
					p = &Pane{ID: a.id(), SID: s.SID, Name: s.Name, info: rex.SessionInfo{ID: s.SID, Dir: s.Dir}, startDir: s.Dir}
					a.attach(p, s.Cols, s.Rows)
				}
			}
			p.Tab, p.Node = t, n
			n.Pane = p
			return n
		}
		n := &Node{ID: a.id(), Parent: parent}
		n.Vertical, n.Ratio = s.Vertical, min(max(s.Ratio, 0.05), 0.95)
		n.A, n.B = load(s.A, t, n), load(s.B, t, n)
		// The side left takes the place of a split whose other side went.
		switch {
		case n.A == nil && n.B == nil:
			return nil
		case n.A == nil:
			n.B.Parent = parent
			return n.B
		case n.B == nil:
			n.A.Parent = parent
			return n.A
		}
		if old := parents[n.A]; old != nil && old == parents[n.B] && old.A == n.A && old.B == n.B {
			delete(parents, n.A)
			delete(parents, n.B)
			old.Parent, old.Vertical, old.Ratio, old.Pane = parent, n.Vertical, n.Ratio, nil
			n.A.Parent, n.B.Parent = old, old
			return old
		}
		return n
	}
	a.tabs = nil
	for _, st := range l.Tabs {
		t := tabs[st.ID]
		reused := t != nil && st.ID != ""
		if reused {
			delete(tabs, st.ID)
			t.Root, t.Floats, t.Zoom = nil, nil, nil
		} else {
			t = a.makeTab(st.ID)
		}
		t.Name = st.Name
		// Before its floats, which a canvas keeps where they were.
		view := t.canvas
		t.canvas = loadCanvas(st.Canvas)
		if view != nil && t.canvas != nil {
			t.canvas.X, t.canvas.Y, t.canvas.Scale = view.X, view.Y, view.Scale
		}
		if st.Root != nil {
			t.Root = load(st.Root, t, nil)
		}
		for _, p := range t.panes() {
			p.float = nil // a float that docked
		}
		// The saved float with the focus, by its place among the saved
		// floats: a float that does not come shifts no index as it does.
		wanted := -1
		if st.FocusFloat > 0 {
			wanted = st.FocusFloat - 1
		}
		var focusedFloat *Float
		for i, sf := range st.Floats {
			if sf.Pane == nil {
				continue
			}
			n := load(sf.Pane, t, nil)
			if n == nil || n.Pane == nil {
				continue
			}
			p := n.Pane
			f := p.float // the float it was, which stays as it moves
			if f == nil {
				f = &Float{Pane: p}
			}
			f.X, f.Y, f.W, f.H, f.Hidden, f.Zoom = sf.X, sf.Y, sf.W, sf.H, sf.Hidden, sf.Zoom
			f.clamp()
			p.float = f
			t.Floats = append(t.Floats, f)
			if i == wanted {
				focusedFloat = f
			}
		}
		if !slices.Contains(t.Floats, t.hiddenLast) {
			t.hiddenLast = nil
		}
		if t.Root == nil {
			if len(t.Floats) == 0 {
				continue
			}
			// The tab's tiled panes are gone: its topmost float takes
			// their place, as it does when the last tiled pane closes.
			a.promoteFloat(t)
		}
		ps, all := t.panes(), t.allPanes()
		if reused {
			// The panes it had keep their order of use.
			t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return !slices.Contains(all, q) })
		} else {
			for _, p := range all {
				t.setFocus(p)
			}
		}
		// A canvas has no tiled pane: its focus is a float's.
		want := t.Focus
		if !slices.Contains(all, want) {
			want = all[len(all)-1]
		}
		if len(ps) > 0 {
			want = ps[min(max(st.Focus, 0), len(ps)-1)]
		}
		if st.Zoom && len(ps) > 1 {
			t.Zoom = want
		}
		if focusedFloat != nil && slices.Contains(t.Floats, focusedFloat) && !focusedFloat.Hidden {
			want = focusedFloat.Pane
		}
		if t.Focus != want {
			t.setFocus(want)
		}
		// The panes new to it go in its order of use behind the focus,
		// which stays.
		for _, p := range all {
			if !slices.Contains(t.recent, p) {
				t.recentBehind(p)
			}
		}
		a.tabs = append(a.tabs, t)
		// A canvas saved with the setting on, which is off now, is tiled
		// again; a tiled tab becomes a canvas as it shows, in its room.
		if !prefs.Canvas && t.canvas != nil {
			a.leaveCanvas(t)
		}
	}
	// What l no longer has closes; the sessions go on.
	for _, p := range before {
		if !kept[p] {
			a.disposePane(p, false)
		}
	}
	if a.renaming != nil && !slices.Contains(a.tabs, a.renaming) {
		a.renaming = nil
	}
	if a.focusReq != nil && a.focusReq.closed {
		a.focusReq = nil
	}
	if len(a.tabs) == 0 {
		a.active, a.focusReq = 0, nil
		return false
	}
	a.active = min(max(l.Active, 0), len(a.tabs)-1)
	a.groupTabs()
	if now := a.tab(); now != wasTab || now.Focus != was {
		a.focusReq = now.Focus
	}
	return true
}
