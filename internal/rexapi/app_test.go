package rexapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// fakeApp is a GoRex window for tests: it takes the layout commands the
// gateway asks for, makes them in a layout of its own as the app does,
// saves it, and answers.
type fakeApp struct {
	t    *testing.T
	desk *rex.Client
	mu   sync.Mutex
	seq  int
	// tabs counts the tabs made, for their lasting IDs.
	tabs int
	// store is the layout the app keeps.
	store layout.Store
	done  chan struct{}
}

// startApp starts a fake GoRex window over the fixture's server, with the
// layout the server has.
func startApp(f *fixture) *fakeApp {
	f.t.Helper()
	a := &fakeApp{t: f.t, desk: f.desk, done: make(chan struct{})}
	if raw, err := f.desk.Layout(); err == nil && len(raw) > 0 {
		json.Unmarshal(raw, &a.store)
	}
	go a.run()
	f.t.Cleanup(func() { close(a.done) })
	return a
}

// run makes the commands the app takes, until the test ends.
func (a *fakeApp) run() {
	for {
		select {
		case <-a.done:
			return
		default:
		}
		cmd, err := a.desk.TakeCommand()
		if err != nil {
			return
		}
		if cmd == nil {
			continue
		}
		a.mu.Lock()
		res := a.apply(*cmd)
		raw, _ := json.Marshal(a.store)
		a.mu.Unlock()
		if err := a.desk.SetLayout(raw); err != nil {
			a.t.Errorf("fake app: saving the layout: %v", err)
		}
		if err := a.desk.CommandDone(res); err != nil {
			a.t.Errorf("fake app: answering: %v", err)
		}
	}
}

// newSession starts a GoRex session for a new pane, which ends at once:
// only its ID matters to the layout.
func (a *fakeApp) newSession(dir string) string {
	info, err := a.desk.Create(rex.CreateOptions{Dir: dir, Command: exitCommand("pane")})
	if err != nil {
		a.t.Errorf("fake app: starting a pane: %v", err)
		return ""
	}
	return info.ID
}

// kill ends a pane's session.
func (a *fakeApp) kill(sid string) {
	if sid != "" {
		a.desk.Kill(sid)
	}
}

// apply makes one layout command, and returns the app's answer.
func (a *fakeApp) apply(c rex.LayoutCommand) rex.LayoutCommand {
	res := rex.LayoutCommand{ID: c.ID}
	fail := func(code, why string) rex.LayoutCommand {
		res.Code, res.Error = code, why
		return res
	}
	switch c.Op {
	case "space.create":
		a.seq++
		id := fmt.Sprintf("w%d", a.seq)
		ws := layout.Workspace{ID: id, Name: c.Name}
		ws.Active = 0
		a.store.Sessions = append(a.store.Sessions, ws)
		res.Space = id
		return res
	case "space.close":
		for i := range a.store.Sessions {
			if a.store.Sessions[i].ID != c.Space {
				continue
			}
			for _, tab := range a.store.Sessions[i].Tabs {
				a.killTree(tab.Root)
			}
			a.store.Sessions = append(a.store.Sessions[:i:i], a.store.Sessions[i+1:]...)
			return res
		}
		return fail("space_not_found", "no workspace")
	case "space.rename":
		if ws := a.store.Space(c.Space); ws != nil {
			ws.Name = c.Name
			return res
		}
		return fail("space_not_found", "no workspace")
	}
	ws := a.store.Space(c.Space)
	if ws == nil {
		// The app shows the window that has the commands.
		if len(a.store.Sessions) == 0 {
			return fail("space_not_found", "no workspace")
		}
		ws = &a.store.Sessions[0]
	}
	if c.Op == "action" {
		if c.Action != "float.set_state" {
			return fail("unknown_action", "GoRex does not know "+c.Action)
		}
		var p struct {
			SID   string `json:"sid"`
			State string `json:"state"`
			Focus bool   `json:"focus"`
		}
		if len(c.Args) > 0 {
			if err := json.Unmarshal(c.Args, &p); err != nil {
				return fail("bad_args", err.Error())
			}
		}
		for wi := range a.store.Sessions {
			w := &a.store.Sessions[wi]
			if c.Space != "" && w.ID != c.Space {
				continue
			}
			for ti := range w.Tabs {
				tab := &w.Tabs[ti]
				for fi := range tab.Floats {
					fl := &tab.Floats[fi]
					if fl.Pane == nil || fl.Pane.SID != p.SID {
						continue
					}
					switch p.State {
					case "normal":
						fl.Hidden = false
						if p.Focus {
							tab.FocusFloat = fi + 1
							w.Active = ti
						}
					case "minimized":
						fl.Hidden = true
						if tab.FocusFloat == fi+1 {
							tab.FocusFloat = 0
						}
					default:
						return fail("bad_args", "state is normal or minimized")
					}
					return res
				}
			}
		}
		return fail("pane_not_found", "no floating pane")
	}
	if c.Op == "tab.create" {
		sid := a.newSession(c.Dir)
		if sid == "" {
			return fail("create_failed", "the session did not start")
		}
		a.tabs++
		tab := layout.Tab{ID: fmt.Sprintf("tab-%d", a.tabs), Name: c.Name, Root: &layout.Node{SID: sid}, Focus: 0}
		at := len(ws.Tabs)
		if t, _ := ws.Find(c.After); t != nil {
			at = ws.TabIndex(t) + 1
		}
		ws.Tabs = append(ws.Tabs[:at:at], append([]layout.Tab{tab}, ws.Tabs[at:]...)...)
		if c.Focus || len(ws.Tabs) == 1 {
			ws.Active = at
		}
		res.New, res.Tab = sid, tab.ID
		return res
	}
	tab, node := ws.Find(c.SID)
	if c.Tab != "" && strings.HasPrefix(c.Op, "tab.") {
		tab, node = nil, nil
		for i := range ws.Tabs {
			if ws.Tabs[i].ID == c.Tab {
				tab = &ws.Tabs[i]
				node = layout.FirstLeaf(tab.Root)
			}
		}
	}
	if node == nil {
		if strings.HasPrefix(c.Op, "tab.") {
			return fail("tab_not_found", "no tab")
		}
		return fail("pane_not_found", "no pane")
	}
	switch c.Op {
	case "tab.close":
		at := ws.TabIndex(tab)
		a.killTree(tab.Root)
		ws.Tabs = append(ws.Tabs[:at:at], ws.Tabs[at+1:]...)
		if ws.Active >= len(ws.Tabs) {
			ws.Active = max(len(ws.Tabs)-1, 0)
		}
		return res
	case "tab.rename":
		tab.Name = c.Name
		return res
	case "tab.move":
		cur := ws.TabIndex(tab)
		n := len(ws.Tabs)
		at := min(max(c.Index, 0), n)
		if cur < at {
			at--
		}
		active := ""
		if ws.Active >= 0 && ws.Active < len(ws.Tabs) {
			active = ws.Tabs[ws.Active].Name
		}
		moved := *tab
		ws.Tabs = append(ws.Tabs[:cur:cur], ws.Tabs[cur+1:]...)
		at = min(max(at, 0), len(ws.Tabs))
		ws.Tabs = append(ws.Tabs[:at:at], append([]layout.Tab{moved}, ws.Tabs[at:]...)...)
		// The tab that was active stays the active one.
		for i := range ws.Tabs {
			if ws.Tabs[i].Name == active {
				ws.Active = i
				break
			}
		}
		res.Index = at
		return res
	case "pane.split":
		sid := a.newSession(c.Dir)
		if sid == "" {
			return fail("create_failed", "the session did not start")
		}
		newNode := &layout.Node{SID: sid}
		tab.Replace(node, &layout.Node{Vertical: c.Vertical, Ratio: 0.5, A: node, B: newNode})
		tab.Zoom = false
		tab.Focus = layout.LeafIndex(tab.Root, newNode.SID)
		res.New = sid
		return res
	case "pane.close":
		a.kill(node.SID)
		tab.Remove(node)
		if tab.Root == nil {
			at := ws.TabIndex(tab)
			ws.Tabs = append(ws.Tabs[:at:at], ws.Tabs[at+1:]...)
			if ws.Active >= len(ws.Tabs) {
				ws.Active = max(len(ws.Tabs)-1, 0)
			}
		}
		return res
	case "pane.rename":
		node.Name = c.Name
		res.Name = c.Name
		return res
	case "pane.focus":
		tab.Focus = layout.LeafIndex(tab.Root, node.SID)
		tab.FocusFloat = 0
		ws.Active = ws.TabIndex(tab)
		return res
	case "pane.zoom":
		on := c.Mode == "on" || c.Mode != "off" && !tab.Zoom
		tab.Zoom = on
		if on {
			tab.Focus = layout.LeafIndex(tab.Root, node.SID)
			tab.FocusFloat = 0
		}
		res.Zoomed = tab.Zoom
		return res
	case "pane.resize":
		split := layout.SplitAt(tab.Root, node, c.Direction)
		if split == nil {
			res.Reason = "unchanged"
			return res
		}
		amount := c.Amount
		if c.Direction == "left" || c.Direction == "up" {
			amount = -amount
		}
		old := split.Ratio
		split.Ratio = min(max(old+amount, 0.1), 0.9)
		res.Changed = split.Ratio != old
		return res
	case "pane.swap":
		other := layout.Find(tab.Root, c.Target)
		switch {
		case other == nil:
			res.Reason = "not_found"
		case other == node:
			res.Reason = "same_pane"
		case a.store.TabOf(c.Target) != tab:
			res.Reason = "cross_tab"
		default:
			node.SID, other.SID = other.SID, node.SID
			node.Name, other.Name = other.Name, node.Name
			res.Changed = true
			tab.Focus = layout.LeafIndex(tab.Root, node.SID)
		}
		return res
	case "pane.move":
		if tab.Zoom {
			res.Reason = "zoomed_tab"
			return res
		}
		target := a.store.TabOf(c.Target)
		if target == nil {
			res.Reason = "not_found"
			return res
		}
		if target == tab {
			res.Reason = "same_tab"
			return res
		}
		vertical := c.Direction == "down"
		ratio := c.Ratio
		if ratio <= 0 || ratio >= 1 {
			ratio = 0.5
		}
		tab.Remove(node)
		if tab.Root == nil {
			at := ws.TabIndex(tab)
			ws.Tabs = append(ws.Tabs[:at:at], ws.Tabs[at+1:]...)
		}
		targetNode := layout.Find(target.Root, c.Target)
		if targetNode == nil {
			res.Reason = "not_found"
			return res
		}
		newNode := &layout.Node{SID: node.SID, Name: node.Name}
		target.Replace(targetNode, &layout.Node{
			Vertical: vertical, Ratio: ratio,
			A: targetNode, B: newNode,
		})
		target.Focus = layout.LeafIndex(target.Root, newNode.SID)
		ws.Active = ws.TabIndex(target)
		res.Changed = true
		return res
	}
	return fail("unknown_op", "GoRex does not know "+c.Op)
}

// killTree ends the sessions of a layout tree.
func (a *fakeApp) killTree(n *layout.Node) {
	if n == nil {
		return
	}
	if n.A == nil && n.B == nil {
		a.kill(n.SID)
		return
	}
	a.killTree(n.A)
	a.killTree(n.B)
}
