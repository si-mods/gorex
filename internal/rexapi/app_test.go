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
		for i := range a.store.Sessions {
			if a.store.Sessions[i].ID == c.Space {
				a.store.Sessions[i].Name = c.Name
				return res
			}
		}
		return fail("space_not_found", "no workspace")
	}
	ws := a.workspace(c.Space)
	if ws == nil {
		// The app shows the window that has the commands.
		if len(a.store.Sessions) == 0 {
			return fail("space_not_found", "no workspace")
		}
		ws = &a.store.Sessions[0]
	}
	if c.Op == "tab.create" {
		sid := a.newSession(c.Dir)
		if sid == "" {
			return fail("create_failed", "the session did not start")
		}
		tab := layout.Tab{Name: c.Name, Root: &layout.Node{SID: sid}, Focus: 0}
		at := len(ws.Tabs)
		if i := findTab(ws, c.After); i >= 0 {
			at = i + 1
		}
		ws.Tabs = append(ws.Tabs[:at:at], append([]layout.Tab{tab}, ws.Tabs[at:]...)...)
		if c.Focus || len(ws.Tabs) == 1 {
			ws.Active = at
		}
		res.New = sid
		return res
	}
	tab, node := findNode(ws, c.SID)
	if node == nil {
		if strings.HasPrefix(c.Op, "tab.") {
			return fail("tab_not_found", "no tab")
		}
		return fail("pane_not_found", "no pane")
	}
	switch c.Op {
	case "tab.close":
		at := tabIndex(ws, tab)
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
		cur := tabIndex(ws, tab)
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
		replaceNode(tab, node, &layout.Node{Vertical: c.Vertical, Ratio: 0.5, A: node, B: newNode})
		tab.Zoom = false
		tab.Focus = leafIndex(tab.Root, newNode.SID)
		res.New = sid
		return res
	case "pane.close":
		a.kill(node.SID)
		removeNode(tab, node)
		if tab.Root == nil {
			at := tabIndex(ws, tab)
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
		tab.Focus = leafIndex(tab.Root, node.SID)
		ws.Active = tabIndex(ws, tab)
		return res
	case "pane.zoom":
		on := c.Mode == "on" || c.Mode != "off" && !tab.Zoom
		tab.Zoom = on
		if on {
			tab.Focus = leafIndex(tab.Root, node.SID)
		}
		res.Zoomed = tab.Zoom
		return res
	case "pane.resize":
		split := splitOfNode(tab.Root, node, c.Direction)
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
		other := nodeOf(tab.Root, c.Target)
		switch {
		case other == nil:
			res.Reason = "not_found"
		case other == node:
			res.Reason = "same_pane"
		case findTabFor(&a.store, c.Target) != tab:
			res.Reason = "cross_tab"
		default:
			node.SID, other.SID = other.SID, node.SID
			node.Name, other.Name = other.Name, node.Name
			res.Changed = true
			tab.Focus = leafIndex(tab.Root, node.SID)
		}
		return res
	case "pane.move":
		if tab.Zoom {
			res.Reason = "zoomed_tab"
			return res
		}
		target := findTabFor(&a.store, c.Target)
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
		removeNode(tab, node)
		if tab.Root == nil {
			at := tabIndex(ws, tab)
			ws.Tabs = append(ws.Tabs[:at:at], ws.Tabs[at+1:]...)
		}
		targetNode := nodeOf(target.Root, c.Target)
		if targetNode == nil {
			res.Reason = "not_found"
			return res
		}
		newNode := &layout.Node{SID: node.SID, Name: node.Name}
		replaceNode(target, targetNode, &layout.Node{
			Vertical: vertical, Ratio: ratio,
			A: targetNode, B: newNode,
		})
		target.Focus = leafIndex(target.Root, newNode.SID)
		ws.Active = tabIndex(ws, target)
		res.Changed = true
		return res
	}
	return fail("unknown_op", "GoRex does not know "+c.Op)
}

// workspace returns the workspace with an ID, nil for none.
func (a *fakeApp) workspace(id string) *layout.Workspace {
	for i := range a.store.Sessions {
		if a.store.Sessions[i].ID == id {
			return &a.store.Sessions[i]
		}
	}
	return nil
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

// findTab returns the index of the tab that holds a pane's session.
func findTab(ws *layout.Workspace, sid string) int {
	for i, tab := range ws.Tabs {
		if leafIndex(tab.Root, sid) >= 0 {
			return i
		}
	}
	return -1
}

// tabIndex returns a tab's place in a workspace.
func tabIndex(ws *layout.Workspace, tab *layout.Tab) int {
	for i := range ws.Tabs {
		if &ws.Tabs[i] == tab {
			return i
		}
	}
	return -1
}

// findNode returns the tab and the node of a pane's session.
func findNode(ws *layout.Workspace, sid string) (*layout.Tab, *layout.Node) {
	for i := range ws.Tabs {
		if node := nodeOf(ws.Tabs[i].Root, sid); node != nil {
			return &ws.Tabs[i], node
		}
	}
	return nil, nil
}

// findTabFor returns the tab of a pane's session in the whole store.
func findTabFor(store *layout.Store, sid string) *layout.Tab {
	for i := range store.Sessions {
		for j := range store.Sessions[i].Tabs {
			if nodeOf(store.Sessions[i].Tabs[j].Root, sid) != nil {
				return &store.Sessions[i].Tabs[j]
			}
		}
	}
	return nil
}

// nodeOf returns the leaf of a session under n.
func nodeOf(n *layout.Node, sid string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.A == nil && n.B == nil {
		if n.SID == sid {
			return n
		}
		return nil
	}
	if x := nodeOf(n.A, sid); x != nil {
		return x
	}
	return nodeOf(n.B, sid)
}

// leafIndex returns the place of a session among a tree's leaves.
func leafIndex(n *layout.Node, sid string) int {
	i := 0
	var walk func(*layout.Node) int
	walk = func(n *layout.Node) int {
		if n == nil {
			return -1
		}
		if n.A == nil && n.B == nil {
			if n.SID == sid {
				return i
			}
			i++
			return -1
		}
		if at := walk(n.A); at >= 0 {
			return at
		}
		return walk(n.B)
	}
	return walk(n)
}

// splitOfNode returns the split a pane's direction edge belongs to.
func splitOfNode(root, node *layout.Node, direction string) *layout.Node {
	vertical := direction == "up" || direction == "down"
	grows := direction == "right" || direction == "down"
	var nearest *layout.Node
	for n := node; ; {
		split := parentOf(root, n)
		if split == nil {
			break
		}
		if split.Vertical == vertical {
			if nearest == nil {
				nearest = split
			}
			if (split.A == n) == grows {
				return split
			}
		}
		n = split
	}
	return nearest
}

// parentOf returns the split above a node.
func parentOf(root, node *layout.Node) *layout.Node {
	if root == nil || root == node {
		return nil
	}
	if root.A == node || root.B == node {
		return root
	}
	if p := parentOf(root.A, node); p != nil {
		return p
	}
	return parentOf(root.B, node)
}

// replaceNode puts next where old was under n.
func replaceNode(tab *layout.Tab, old, next *layout.Node) {
	if tab.Root == old {
		tab.Root = next
		return
	}
	p := parentOf(tab.Root, old)
	if p == nil {
		return
	}
	if p.A == old {
		p.A = next
	} else {
		p.B = next
	}
}

// removeNode takes a leaf out of a tab's tree, closing the splits it
// leaves empty.
func removeNode(tab *layout.Tab, node *layout.Node) {
	if tab.Root == node {
		tab.Root, tab.Zoom = nil, false
		return
	}
	p := parentOf(tab.Root, node)
	if p == nil {
		return
	}
	var keep *layout.Node
	if p.A == node {
		keep = p.B
	} else {
		keep = p.A
	}
	replaceNode(tab, p, keep)
	tab.Focus = min(max(tab.Focus, 0), leafCount(tab.Root)-1)
}

// leafCount counts a tree's leaves.
func leafCount(n *layout.Node) int {
	if n == nil {
		return 0
	}
	if n.A == nil && n.B == nil {
		return 1
	}
	return leafCount(n.A) + leafCount(n.B)
}
