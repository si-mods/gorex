package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// followApp has the test app follow its server's layout, as adopt does,
// and saves its layout; other is another client of the server.
func followApp(t *testing.T, a *App) (other *rex.Client) {
	t.Helper()
	a.saveNow()
	a.st.use(a.client, a.post)
	if _, err := a.st.read(); err != nil {
		t.Fatal(err)
	}
	go a.st.follow(a.client)
	c, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// setOther writes s from another client.
func setOther(t *testing.T, c *rex.Client, s savedStore) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetLayout(b); err != nil {
		t.Fatal(err)
	}
}

// TestFollowFollowsServer changes the layout from another client while
// the window shows it: the window keeps its tabs, panes and terminals,
// attaches a new leaf, closes a dropped one without ending its session,
// and takes the active tab and the focus.
func TestFollowFollowsServer(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.tab()
	p1 := first.Focus
	p2 := a.splitPane(p1, false, os.TempDir())
	second := a.newTab(os.TempDir())
	p3 := second.Focus
	fp := a.newFloat(nil)
	f := fp.float
	second.setFocus(p3)
	a.selectTab(0)
	first.setFocus(p1)
	other := followApp(t, a)
	term1, stream1 := p1.term, p1.stream

	// Another client adds a session as a split of p1's, drops p2, focuses
	// the new pane and makes the second tab active.
	in, err := other.Create(rex.CreateOptions{Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := savedWorkspaces(t, other)
	// The server placed it in a tab of its own, with no app taking
	// commands: the other client moves it into the split.
	s.Sessions[0].Tabs = s.Sessions[0].Tabs[:2]
	tab := &s.Sessions[0].Tabs[0]
	tab.Root = &layout.Node{Ratio: 0.3, A: &layout.Node{SID: p1.SID}, B: &layout.Node{SID: in.ID, Name: "added"}}
	tab.Focus = 1
	s.Sessions[0].Active = 1
	s.Sessions[0].Tabs[1].Floats[0].X = 0.25
	setOther(t, other, s)
	// As the follower does, which the frames run.
	ls, err := other.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	a.st.takeLayout(ls)
	// The server moved the active tab: the keyboard goes to its focus.
	if a.focusReq != p3 {
		t.Errorf("focusReq %v, want p3", a.focusReq)
	}
	waitFor(t, tt, "the window to follow", func() bool { return a.active == 1 })
	if len(a.tabs) != 2 || a.tabs[0] != first || a.tabs[1] != second {
		t.Fatalf("tabs %v, want the same two", a.tabs)
	}
	ps := first.panes()
	if len(ps) != 2 || ps[0] != p1 || p1.term != term1 || p1.stream != stream1 || p1.closed {
		t.Fatalf("p1 was not kept: panes %v", ps)
	}
	added := ps[1]
	if added.SID != in.ID || added.Name != "added" || added.stream == nil || added.term == nil {
		t.Errorf("new leaf %+v not attached", added)
	}
	if r := first.Root.Ratio; r != 0.3 {
		t.Errorf("ratio %v, want 0.3", r)
	}
	if first.Focus != added {
		t.Errorf("focus %v, want the new pane", first.Focus)
	}
	if live := slices.Contains(liveSIDs(t, other), p2.SID); !p2.closed || !live {
		t.Errorf("p2 closed %v, its session live %v", p2.closed, live)
	}
	if second.Focus != p3 || p3.closed {
		t.Error("the second tab's pane changed")
	}
	if len(second.Floats) != 1 || second.Floats[0] != f || f.Pane != fp || fp.float != f || f.X != 0.25 || fp.closed {
		t.Errorf("the float was not kept and moved: %+v", second.Floats)
	}

	// A change that leaves the focus where it was asks for no focus.
	tt.Frame()
	a.focusReq = nil
	s = savedWorkspaces(t, other)
	s.Sessions[0].Tabs[1].Name = "renamed"
	setOther(t, other, s)
	if ls, err = other.LayoutState(); err != nil {
		t.Fatal(err)
	}
	a.st.takeLayout(ls)
	if second.Name != "renamed" || a.focusReq != nil {
		t.Errorf("name %q, focusReq %v after a rename", second.Name, a.focusReq)
	}
}

// TestFollowOwnWrites saves the window's own changes: the layout events
// they make do not reconcile the window, and nothing writes again.
func TestFollowOwnWrites(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	other := followApp(t, a)

	// A name given without saving it: a reconcile would take the
	// server's, which has none.
	p.Name = "unsaved"
	a.tab().Name = "saved"
	a.changed()
	a.save()
	start := time.Now()
	for time.Since(start) < 1500*time.Millisecond {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	ls, err := other.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	if got := layout.Parse(ls.Layout).Sessions[0].Tabs[0].Name; got != "saved" {
		t.Fatalf("server's tab name %q", got)
	}
	if p.Name != "unsaved" {
		t.Errorf("the window reconciled its own write: pane name %q", p.Name)
	}
	if rev := a.st.out.revision(); rev != ls.Revision {
		t.Errorf("app at revision %d, server at %d", rev, ls.Revision)
	}
	for range 25 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if again, _ := other.LayoutState(); again.Revision != ls.Revision {
		t.Errorf("the layout kept changing: %d then %d", ls.Revision, again.Revision)
	}
}

// TestFollowServerApplied makes the server apply a client's commands
// while the window shows the space but takes no commands: the window shows
// the new tab and split attached, and closing a pane closes it here with
// its session.
func TestFollowServerApplied(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.tab()
	other := followApp(t, a)

	res, err := other.Command(rex.LayoutCommand{Op: "tab.create", Dir: os.TempDir(), Name: "from-api", Focus: true})
	if err != nil || res.Code != "" {
		t.Fatalf("tab.create: %v %+v", err, res)
	}
	waitFor(t, tt, "the new tab", func() bool { return len(a.tabs) == 2 && a.active == 1 })
	tab := a.tabs[1]
	if a.tabs[0] != first || tab.Name != "from-api" || tab.Focus == nil || tab.Focus.SID != res.New || tab.Focus.stream == nil {
		t.Fatalf("tabs %v, new %+v", a.tabs, tab)
	}
	p := tab.Focus
	res, err = other.Command(rex.LayoutCommand{Op: "pane.split", SID: p.SID, Vertical: true})
	if err != nil || res.Code != "" {
		t.Fatalf("pane.split: %v %+v", err, res)
	}
	waitFor(t, tt, "the split", func() bool { return len(tab.panes()) == 2 })
	if tab.panes()[0] != p || tab.Focus.SID != res.New {
		t.Errorf("panes %v, focus %v", tab.panes(), tab.Focus)
	}
	if _, err = other.Command(rex.LayoutCommand{Op: "pane.close", SID: res.New}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the close", func() bool { return len(tab.panes()) == 1 })
	if tab.Focus != p || tab.panes()[0] != p {
		t.Errorf("after the close: focus %v", tab.Focus)
	}
}

// TestFollowNoWindow follows the layout while no window shows a
// space, and the space's window shows it as it opens.
func TestFollowNoWindow(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	other := followApp(t, a)
	w := a.ws
	st.closed(a)
	res, err := other.Command(rex.LayoutCommand{Op: "tab.create", Dir: os.TempDir(), Name: "away"})
	if err != nil || res.Code != "" {
		t.Fatalf("tab.create: %v %+v", err, res)
	}
	// No window runs the posted changes: the test runs them.
	waitFor(t, tt, "the space to follow", func() bool {
		a.runPosted()
		return len(w.layout.Tabs) == 2
	})
	b := &App{st: st, client: a.client}
	st.apps = []*App{b}
	b.show(w)
	if len(b.tabs) != 2 || b.tabs[1].Name != "away" || b.tabs[1].Focus.SID != res.New || b.tabs[1].Focus.stream == nil {
		t.Errorf("reopened tabs %v", b.tabs)
	}
	if slices.ContainsFunc(b.tabs, func(t *Tab) bool { return t.Focus == nil }) {
		t.Error("a tab without focus")
	}
}

// TestFollowReconnect changes the layout while the app's connection is
// lost: the window connects again and shows the server's layout, its
// sessions attached on the new connection.
func TestFollowReconnect(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	a.saveNow()
	old := st.client
	old.Close()
	<-old.Closed()
	other, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	res, err := other.Command(rex.LayoutCommand{Op: "tab.create", Dir: os.TempDir(), Name: "meanwhile"})
	if err != nil || res.Code != "" {
		t.Fatalf("tab.create: %v %+v", err, res)
	}
	if !st.connect() || st.client == old {
		t.Fatal("no new connection")
	}
	t.Cleanup(func() { st.client.Shutdown() })
	if len(a.tabs) != 2 || a.tabs[1].Name != "meanwhile" || a.tabs[1].Focus.SID != res.New {
		t.Fatalf("tabs %v after reconnecting", a.tabs)
	}
	for _, tab := range a.tabs {
		if p := tab.Focus; p.stream == nil || p.closed {
			t.Errorf("pane %s not attached", p.SID)
		}
	}
	if rev := st.out.revision(); rev == 0 {
		t.Error("the app shows no revision of the new connection")
	}
	quit(st)
}

// TestReconnectShowsSpace reconnects after another client made a tab with
// the focus in a space no window shows: the window shows that space then.
func TestReconnectShowsSpace(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	space := clientCommand(a, rex.LayoutCommand{Op: "space.create", Name: "away"}).Space
	a.saveNow()
	old := st.client
	old.Close()
	<-old.Closed()
	other, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if res, err := other.Command(rex.LayoutCommand{Op: "tab.create", Space: space, Dir: os.TempDir(), Focus: true}); err != nil || res.Code != "" {
		t.Fatalf("tab.create: %v %+v", err, res)
	}
	if !st.connect() || st.client == old {
		t.Fatal("no new connection")
	}
	t.Cleanup(func() { st.client.Shutdown() })
	if a.ws == nil || a.ws.ID != space {
		t.Fatalf("the window shows %v, not %s", a.ws, space)
	}
	quit(st)
}

// TestReconnectReadsShow reconnects with a request to show a space the
// app knew of the last connection, which the server forgot meanwhile:
// the window acts on the layout it reads then, and stays.
func TestReconnectReadsShow(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	home := a.ws
	space := clientCommand(a, rex.LayoutCommand{Op: "space.create", Name: "away"}).Space
	clientCommand(a, rex.LayoutCommand{Op: "tab.create", Space: space, Dir: os.TempDir()})
	a.saveNow()
	st.base.Show, st.base.ShowAt = space, time.Now()
	old := st.client
	old.Close()
	<-old.Closed()
	if !st.connect() || st.client == old {
		t.Fatal("no new connection")
	}
	t.Cleanup(func() { st.client.Shutdown() })
	if a.ws != home {
		t.Fatalf("the window shows %s", a.ws.ID)
	}
	quit(st)
}

// takeOther has the app take the layout another client wrote, as the
// follower does, and returns the layout's revision.
func takeOther(t *testing.T, a *App, other *rex.Client) uint64 {
	t.Helper()
	ls, err := other.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	a.st.takeLayout(ls)
	return ls.Revision
}

// TestFollowReconnectTwoWindows reconnects two windows, the space of the
// first closed meanwhile: it takes another, never the second's.
func TestFollowReconnectTwoWindows(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	x := a.ws
	b := st.openWindow(st.addWorkspace())
	y := b.ws
	st.saveNow()
	other, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s := savedWorkspaces(t, other)
	s.Sessions = slices.DeleteFunc(s.Sessions, func(w savedWorkspace) bool { return w.ID == x.ID })
	s.Windows = []string{y.ID}
	old := st.client
	old.Close()
	<-old.Closed()
	setOther(t, other, s)
	if !st.connect() || st.client == old {
		t.Fatal("no new connection")
	}
	t.Cleanup(func() { st.client.Shutdown() })
	if b.ws != y || a.ws == nil || a.ws == y || a.ws == x || y.app != b || a.ws.app != a {
		t.Errorf("windows show %v and %v; want y for the second", a.ws, b.ws)
	}
	quit(st)
}

// TestFollowEmptiedAndGone takes a layout that empties the window's space
// and closes a hidden one: the window shows the space left with tabs, and
// the emptied space stays.
func TestFollowEmptiedAndGone(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	x := a.ws
	gone, left := st.addWorkspace(), st.addWorkspace()
	for i, w := range []*Workspace{gone, left} {
		w.layout = savedLayout{Tabs: []savedTab{{ID: layout.NewTabID(), Root: &savedNode{URL: "https://example.com/", ID: fmt.Sprint("web", i)}}}}
	}
	other := followApp(t, a)
	s := savedWorkspaces(t, other)
	for i := range s.Sessions {
		if s.Sessions[i].ID == x.ID {
			s.Sessions[i].Tabs = []savedTab{}
		}
	}
	s.Sessions = slices.DeleteFunc(s.Sessions, func(w savedWorkspace) bool { return w.ID == gone.ID })
	setOther(t, other, s)
	takeOther(t, a, other)
	if a.ws != left || len(a.tabs) != 1 {
		t.Errorf("window shows %v with %d tabs, want the space left", a.ws, len(a.tabs))
	}
	if !slices.Contains(st.workspaces, x) || slices.Contains(st.workspaces, gone) {
		t.Errorf("workspaces %v", st.workspaces)
	}
}

// TestFollowTabToSpace takes a server's tab.to_space, as a Rex client's
// gorex.move_window asks it, between the spaces of two windows: the tab
// keeps its ID in the window it moves to, though that window takes the
// layout before the one it leaves, and its session goes on. The move of
// a space's last tab takes the space away, and its window shows another.
func TestFollowTabToSpace(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	x := a.ws
	b := st.openWindow(st.addWorkspace())
	y := b.ws
	b.newTab(testShell.dir)
	if len(b.tabs) != 2 {
		t.Fatalf("the second window has %d tabs", len(b.tabs))
	}
	moved := b.tabs[1]
	uid, sid := moved.UID, moved.Focus.SID
	other := followApp(t, a)
	if res, err := other.Command(rex.LayoutCommand{Op: "tab.to_space", Tab: uid, ToSpace: x.ID}); err != nil || res.Code != "" {
		t.Fatalf("tab.to_space: %+v %v", res, err)
	}
	takeOther(t, a, other)
	if len(a.tabs) != 2 || a.tabs[1].UID != uid || a.tabs[1].Focus == nil || a.tabs[1].Focus.SID != sid {
		t.Fatalf("the first window has %q, want the moved tab %s last", sidsOf(a), uid)
	}
	if len(b.tabs) != 1 || slices.Contains(sidsOf(b), sid) {
		t.Errorf("the second window has %q", sidsOf(b))
	}
	if !slices.Contains(liveSIDs(t, other), sid) {
		t.Error("the moved tab's session ended")
	}
	last := b.tabs[0]
	luid, lsid := last.UID, last.Focus.SID
	if res, err := other.Command(rex.LayoutCommand{Op: "tab.to_space", Tab: luid, ToSpace: x.ID, Target: a.tabs[0].UID}); err != nil || res.Code != "" {
		t.Fatalf("tab.to_space of the last tab: %+v %v", res, err)
	}
	takeOther(t, a, other)
	if len(a.tabs) != 3 || a.tabs[0].UID != luid || a.tabs[0].Focus.SID != lsid {
		t.Fatalf("the first window has %q, want the last tab %s first", sidsOf(a), luid)
	}
	if slices.Contains(st.workspaces, y) || b.ws == y {
		t.Errorf("the emptied space stays: %v", st.workspaces)
	}
	if !slices.Contains(liveSIDs(t, other), lsid) {
		t.Error("the last tab's session ended")
	}
	quit(st)
}

// TestFollowBrowser follows another client's page in a browser pane, keeps
// the pane's own navigation, and keeps the pane of a layout whose writer
// drops browser IDs.
func TestFollowBrowser(t *testing.T) {
	a, _ := newTestApp(t)
	a.splitBrowser(false)
	web := a.tab().Focus
	other := followApp(t, a)
	browser := func(s savedStore) *savedNode { return s.Sessions[0].Tabs[0].Root.B }

	s := savedWorkspaces(t, other)
	browser(s).URL = "https://theirs.test/"
	setOther(t, other, s)
	takeOther(t, a, other)
	if p := a.tab().panes()[1]; p != web || web.browser.url != "https://theirs.test/" {
		t.Errorf("pane %v at %q", p, web.browser.url)
	}
	// The page went on by itself, unsaved; another change leaves it.
	web.browser.url = "https://mine.test/"
	s = savedWorkspaces(t, other)
	s.Sessions[0].Tabs[0].Name = "renamed"
	setOther(t, other, s)
	takeOther(t, a, other)
	if web.browser.url != "https://mine.test/" {
		t.Errorf("own navigation lost: %q", web.browser.url)
	}
	// A writer that drops the ID: the pane is named by its place, once,
	// and stays at the next such write.
	a.st.out.wait(time.Second)
	var kept *Pane
	for i := range 2 {
		s = savedWorkspaces(t, other)
		browser(s).ID = ""
		s.Sessions[0].Tabs[0].Name = fmt.Sprint("drop", i)
		b, _ := json.Marshal(s)
		var raw map[string]any
		json.Unmarshal(b, &raw)
		strip(raw)
		b, _ = json.Marshal(raw)
		if err := other.SetLayout(b); err != nil {
			t.Fatal(err)
		}
		takeOther(t, a, other)
		a.st.out.wait(time.Second)
		p := a.tab().panes()[1]
		if i == 1 && p != kept {
			t.Error("the browser pane was made again")
		}
		kept = p
	}
	if id := browser(savedWorkspaces(t, other)).ID; id == "" {
		t.Error("the ID given was not written back")
	}
}

// strip drops every "id" of a browser pane of a decoded layout.
func strip(v any) {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["url"]; ok {
			delete(x, "id")
		}
		for _, y := range x {
			strip(y)
		}
	case []any:
		for _, y := range x {
			strip(y)
		}
	}
}

// TestFollowRecentAndInfos adds a pane to a tab from another client while
// the polled list does not have its session yet: it attaches, joins the
// tab's order of use behind the focus, which stays, and the window writes
// nothing back.
func TestFollowRecentAndInfos(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	p1 := tab.Focus
	other := followApp(t, a)
	refresh(a) // st.infos, without the session to come
	a.st.infos = map[string]rex.SessionInfo{p1.SID: a.st.infos[p1.SID]}
	in, err := other.Create(rex.CreateOptions{Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := savedWorkspaces(t, other)
	s.Sessions[0].Tabs[0].Root = &layout.Node{Ratio: 0.5, A: &layout.Node{SID: p1.SID}, B: &layout.Node{SID: in.ID}}
	s.Sessions[0].Tabs[0].Focus = 0
	setOther(t, other, s)
	rev := takeOther(t, a, other)
	ps := tab.panes()
	if len(ps) != 2 || ps[1].SID != in.ID || ps[1].stream == nil || ps[1].info.ID != in.ID {
		t.Fatalf("panes %v", ps)
	}
	if tab.Focus != p1 || !slices.Contains(tab.recent, ps[1]) || tab.recent[len(tab.recent)-1] != p1 {
		t.Errorf("focus %v, recent %v", tab.Focus, tab.recent)
	}
	for range 30 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if ls, _ := other.LayoutState(); ls.Revision != rev {
		t.Errorf("the window wrote back what it took: revision %d, then %d", rev, ls.Revision)
	}
}

// TestSplitReachesServer splits a pane from the window, which follows the
// server's layout as the app does: the server makes the pane, and the
// window shows it attached, with the keyboard.
func TestSplitReachesServer(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	p1 := tab.Focus
	other := followApp(t, a)
	if p := a.splitPane(p1, false, os.TempDir()); p != nil {
		t.Fatal("the split showed before the server made it")
	}
	waitFor(t, tt, "the split", func() bool { return len(tab.panes()) == 2 })
	p2 := tab.panes()[1]
	if tab.panes()[0] != p1 || p2.stream == nil || tab.Focus != p2 {
		t.Errorf("panes %v, focus %v", tab.panes(), tab.Focus)
	}
	s := savedWorkspaces(t, other)
	if root := s.Sessions[0].Tabs[0].Root; root.A == nil || root.A.SID != p1.SID || root.B.SID != p2.SID {
		t.Errorf("the server's tab %+v", s.Sessions[0].Tabs[0])
	}
}

// TestRestartFocusedOnServer restarts the focused pane's shell: the server
// starts the new session in the pane's place (pane.restart), ends the old
// one, and the window shows the new one, focused, beside the other pane.
func TestRestartFocusedOnServer(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	p1 := tab.Focus
	followApp(t, a)
	a.splitPane(p1, false, os.TempDir())
	waitFor(t, tt, "the split", func() bool { return len(tab.panes()) == 2 })
	old := tab.Focus
	a.restartFocused()
	waitFor(t, tt, "the restarted pane", func() bool {
		ps := tab.panes()
		return len(ps) == 2 && ps[1] != old && ps[1].SID != old.SID && tab.Focus == ps[1]
	})
	if tab.panes()[0] != p1 || !old.closed {
		t.Errorf("panes %v, the old one closed %v", tab.panes(), old.closed)
	}
	if slices.Contains(liveSIDs(t, a.client), old.SID) {
		t.Error("the old session runs")
	}
}

// TestRestartFocusedOnHost restarts the shell of a host's pane, then closes
// the new pane: the focus goes to the other pane, not to the one the
// restart ended.
func TestRestartFocusedOnHost(t *testing.T) {
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, _ := startHostServer(t)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "studio", Address: sock})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	tab := hostTab(t, a, tt, h, "")
	a.split(true)
	waitFor(t, tt, "the split", func() bool { return len(tab.panes()) == 2 })
	p1, old := tab.panes()[0], tab.Focus
	a.restartFocused()
	waitFor(t, tt, "the restarted pane", func() bool { return tab.Focus != old })
	p := tab.Focus
	if p == old || tab.panes()[1] != p || !old.closed {
		t.Fatal("the restarted pane did not take the old one's place, focused")
	}
	if slices.Contains(tab.recent, old) {
		t.Error("the ended pane is in the tab's order of use")
	}
	a.closePane(p)
	if tab.Focus != p1 {
		t.Errorf("the focus went to the ended pane %v, not the other pane", tab.Focus == old)
	}
}

// TestRefusedTabSetRestored changes a tab the server changed meanwhile: the
// window's tab.set, which leaves out the pane another client split off, is
// refused, and the window shows the server's tab, the pane in it.
func TestRefusedTabSetRestored(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	other, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	res, err := other.Command(rex.LayoutCommand{Op: "pane.split", SID: tab.Focus.SID, Command: exitLater()})
	if err != nil || res.Code != "" {
		t.Fatalf("pane.split: %v %+v", err, res)
	}
	tab.Name = "mine"
	a.changed()
	a.save()
	if len(tab.panes()) != 2 || tab.panes()[1].SID != res.New || tab.Name != "" {
		t.Errorf("after the refusal: panes %v, name %q", tab.panes(), tab.Name)
	}
	s := savedWorkspaces(t, other)
	if leaves := layout.Leaves(s.Sessions[0].Tabs[0].Root); len(leaves) != 2 || s.Sessions[0].Tabs[0].Name != "" {
		t.Errorf("the server's tab %+v", s.Sessions[0].Tabs[0])
	}
}

// exitLater is a program that runs a while, as a pane's.
func exitLater() []string { return []string{testShell.path, "-c", "sleep 30"} }

// TestGestureSendsOneTabSet drags a divider for more than a second: the
// window keeps the ratio its own while the pointer holds it, and sends it
// once, as one tab.set, when it lets go.
func TestGestureSendsOneTabSet(t *testing.T) {
	a, tt := newTestApp(t)
	a.split(false)
	tab := a.tab()
	a.saveNow()
	tt.Frame()
	state := func() (uint64, float32) {
		ls, err := a.client.LayoutState()
		if err != nil {
			t.Fatal(err)
		}
		return ls.Revision, layout.Parse(ls.Layout).Sessions[0].Tabs[0].Root.Ratio
	}
	rev, ratio := state()
	d, ok := tt.Find(locales.PaneDivider())
	if !ok {
		t.Fatalf("no divider; texts %q", tt.Texts())
	}
	x, y := d.X+d.W/2, d.Y+d.H/2
	tt.Press(x, y)
	tt.Frame()
	for i := 1; i <= 6; i++ {
		tt.Move(x-float32(i*20), y)
		tt.Frame()
		time.Sleep(250 * time.Millisecond)
		tt.Frame()
	}
	if r, got := state(); r != rev || got != ratio {
		t.Errorf("during the drag the server's layout changed: revision %d to %d, ratio %v to %v", rev, r, ratio, got)
	}
	tt.Release(x-120, y)
	for range 3 {
		tt.Frame()
	}
	waitFor(t, tt, "the drag's save", func() bool { r, _ := state(); return r != rev })
	time.Sleep(1200 * time.Millisecond)
	tt.Frame()
	r, got := state()
	if r != rev+1 || got == ratio || got != tab.Root.Ratio {
		t.Errorf("after the drag: revision %d (from %d), ratio %v, the window's %v", r, rev, got, tab.Root.Ratio)
	}
}

// TestStaleTabSetKeepsOthersChange renames a tab from another client while
// the window, which follows the server asynchronously, has a change of its
// own to the same tab not sent yet: the window's change, built on the tab
// as it was, is refused, and the other client's rename stays, in the
// server and in the window.
func TestStaleTabSetKeepsOthersChange(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	other := followApp(t, a)
	caughtUp(t, a, tt)
	if res, err := other.Command(rex.LayoutCommand{Op: "tab.rename", Tab: tab.UID, Name: "theirs"}); err != nil || res.Code != "" {
		t.Fatalf("rename: %+v %v", res, err)
	}
	// The window has not taken it: its own rename goes first.
	tab.Name = "mine"
	a.changed()
	a.save()
	caughtUp(t, a, tt)
	if got := savedWorkspaces(t, other).Sessions[0].Tabs[0].Name; got != "theirs" {
		t.Errorf("the server's tab is %q", got)
	}
	if tab.Name != "theirs" {
		t.Errorf("the window's tab is %q", tab.Name)
	}
}

// TestRefusedChangeRestored has the window send a change the server
// refuses, with no other client's change: the window shows the server's
// layout again, at the same revision.
func TestRefusedChangeRestored(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	followApp(t, a)
	caughtUp(t, a, tt)
	p := tab.Focus
	rev := a.st.out.revision()
	// A second pane of the same session: the tab.set that names it twice
	// is refused, and changes nothing on the server.
	twin := &Pane{ID: a.id(), SID: p.SID, info: p.info}
	a.splitWith(p, twin, false)
	if len(tab.panes()) != 2 {
		t.Fatal("no twin")
	}
	a.save()
	waitFor(t, tt, "the window to show the server's tab", func() bool {
		return len(tab.panes()) == 1 && tab.panes()[0] == p
	})
	if ls, _ := a.client.LayoutState(); ls.Revision != rev {
		t.Errorf("the server's layout changed: revision %d, was %d", ls.Revision, rev)
	}
}

// TestSaveNowWhileMainBusy closes a window while the outbox would take the
// server's layout on the main thread, which is the one saving: the save
// returns once the server answered, not a second later.
func TestSaveNowWhileMainBusy(t *testing.T) {
	a, tt := newTestApp(t)
	followApp(t, a)
	caughtUp(t, a, tt)
	// The main thread runs nothing meanwhile: a.post's work waits.
	a.tab().Name = "closing"
	a.changed()
	start := time.Now()
	a.st.saveNow()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("the save took %v", d)
	}
	if got := savedWorkspaces(t, a.client).Sessions[0].Tabs[0].Name; got != "closing" {
		t.Errorf("the server's tab is %q", got)
	}
}

// TestRemoveHostKeepsWindow removes a host whose panes are all the
// window's, which follows the server asynchronously: the window keeps its
// space, and shows the new tab of this machine in place of the host's.
func TestRemoveHostKeepsWindow(t *testing.T) {
	a, tt := newTestApp(t)
	h := newHostConn(savedHost{ID: "h1", Name: "studio"})
	a.hosts = []*hostConn{h}
	ws := a.ws
	// Only a host's pane: the local tab goes.
	local := a.tab()
	rp := &Pane{ID: a.id(), SID: "far", host: h, info: rex.SessionInfo{ID: "far"}}
	rt := a.makeTab("")
	rt.Root = &Node{ID: a.id(), Pane: rp}
	rp.Tab, rp.Node = rt, rt.Root
	rt.setFocus(rp)
	a.tabs = []*Tab{rt}
	a.active = 0
	a.closePanes([]*Tab{local})
	followApp(t, a)
	caughtUp(t, a, tt)
	a.removeHost(h)
	waitFor(t, tt, "a tab of this machine", func() bool {
		return len(a.tabs) == 1 && a.tabs[0].Focus != nil && a.tabs[0].Focus.host == nil && a.tabs[0].Focus.SID != ""
	})
	if a.ws != ws || a.st.byID(ws.ID) == nil {
		t.Errorf("the window left its space: %v", a.ws)
	}
	caughtUp(t, a, tt)
	if a.err != "" {
		t.Errorf("error %q", a.err)
	}
}

// TestFloatNewBox runs float.new with a box, the server making the float
// asynchronously: the float takes the box once it shows.
func TestFloatNewBox(t *testing.T) {
	a, tt := newTestApp(t)
	followApp(t, a)
	caughtUp(t, a, tt)
	if err := cmdNewFloat.RunArgs(a, map[string]any{"x": 0.1, "y": 0.2, "w": 0.5, "h": 0.4}); err != nil {
		t.Fatal(err)
	}
	tab := a.tab()
	waitFor(t, tt, "the float", func() bool { return len(tab.Floats) == 1 })
	f := tab.Floats[0]
	if !near(f.X, 0.1) || !near(f.Y, 0.2) || !near(f.W, 0.5) || !near(f.H, 0.4) {
		t.Errorf("the float's box %v %v %v %v", f.X, f.Y, f.W, f.H)
	}
	a.save()
	caughtUp(t, a, tt)
	if fs := savedWorkspaces(t, a.client).Sessions[0].Tabs[0].Floats; len(fs) != 1 || !near(fs[0].W, 0.5) {
		t.Errorf("the server's floats %+v", fs)
	}
}

// TestEndPaneWhenTabFails ends the last pane, a host's, while the server
// makes no tab: the dead pane goes anyway.
func TestEndPaneWhenTabFails(t *testing.T) {
	a, _ := newTestApp(t)
	h := newHostConn(savedHost{ID: "h1", Name: "studio"})
	a.hosts = []*hostConn{h}
	rp := &Pane{ID: a.id(), SID: "far", host: h, info: rex.SessionInfo{ID: "far"}}
	t1 := a.tab()
	a.closePanes([]*Tab{t1})
	rt := a.makeTab("")
	rt.Root = &Node{ID: a.id(), Pane: rp}
	rp.Tab, rp.Node = rt, rt.Root
	rt.setFocus(rp)
	a.tabs, a.active = []*Tab{rt}, 0
	a.st.readOnly = true // no tab comes
	a.endPane(rp)
	if !rp.closed {
		t.Error("the dead pane stays")
	}
}

// TestActionOnHiddenSpace runs an action on a pane of a space no window
// shows: the window shows that space, and the action acts on the pane.
func TestActionOnHiddenSpace(t *testing.T) {
	a, _ := newTestApp(t)
	home := a.ws
	res := clientCommand(a, rex.LayoutCommand{Op: "space.create", Name: "away"})
	made := clientCommand(a, rex.LayoutCommand{Op: "tab.create", Space: res.Space, Dir: os.TempDir()})
	if made.Code != "" || a.ws != home {
		t.Fatalf("tab.create: %+v, window on %v", made, a.ws)
	}
	app := a.st.commandAppFor("", made.New)
	got := app.command(rex.LayoutCommand{Op: "action", SID: made.New, Action: "tab.rename", Args: json.RawMessage(`{"name":"renamed"}`)})
	if got.Code != "" || app.ws == nil || app.ws.ID != res.Space || app.tab().Name != "renamed" {
		t.Errorf("the action: %+v, window on %v", got, app.ws)
	}
}

// TestReadOnlyReverts closes a pane while the server is one of before it
// made the layout's changes: the window shows the pane again, and says why.
func TestReadOnlyReverts(t *testing.T) {
	a, tt := newTestApp(t)
	a.split(false)
	tab := a.tab()
	followApp(t, a)
	caughtUp(t, a, tt)
	a.st.readOnly = true
	a.closePane(tab.panes()[1])
	a.save()
	waitFor(t, tt, "the pane back", func() bool { return len(tab.panes()) == 2 })
	if a.errMsg == nil {
		t.Error("no banner")
	}
	if infos, _ := a.client.List(); len(infos) != 2 {
		t.Errorf("sessions %d", len(infos))
	}
}

// TestSwapPanesBetweenTabs moves a pane of each of two tabs into the other
// in one save, the window following the server asynchronously: the server
// makes both at once, and the window keeps them.
func TestSwapPanesBetweenTabs(t *testing.T) {
	a, tt := newTestApp(t)
	a.split(false)
	one := a.tab()
	a.newTab(os.TempDir())
	a.split(false)
	two := a.tab()
	followApp(t, a)
	caughtUp(t, a, tt)
	x, y := one.panes()[1], two.panes()[1]
	rev := a.st.out.revision()
	a.movePaneBeside(x, two.panes()[0], false, false, 0.5)
	a.movePaneBeside(y, one.panes()[0], false, false, 0.5)
	a.save()
	caughtUp(t, a, tt)
	s := savedWorkspaces(t, a.client).Sessions[0]
	has := func(tab layout.Tab, sid string) bool { return layout.Find(tab.Root, sid) != nil }
	if len(s.Tabs) != 2 || !has(s.Tabs[0], y.SID) || !has(s.Tabs[1], x.SID) || has(s.Tabs[0], x.SID) {
		t.Fatalf("the server's tabs: %s %s", s.Tabs[0].Arrangement(), s.Tabs[1].Arrangement())
	}
	if ls, _ := a.client.LayoutState(); ls.Revision != rev+1 {
		t.Errorf("revision %d, want %d: the swap at once", ls.Revision, rev+1)
	}
	if x.Tab != two || y.Tab != one || x.closed || y.closed {
		t.Errorf("the window's panes moved back")
	}
}

// TestBatchRefusedWhole changes two tabs in one save, one of which another
// client renamed meanwhile: the server makes neither change, and the
// window shows the server's tabs, the other client's rename kept.
func TestBatchRefusedWhole(t *testing.T) {
	a, tt := newTestApp(t)
	one := a.tab()
	a.newTab(os.TempDir())
	two := a.tab()
	other := followApp(t, a)
	caughtUp(t, a, tt)
	if res, err := other.Command(rex.LayoutCommand{Op: "tab.rename", Tab: two.UID, Name: "theirs"}); err != nil || res.Code != "" {
		t.Fatalf("rename: %+v %v", res, err)
	}
	one.Name, two.Name = "mine-1", "mine-2"
	a.changed()
	a.save()
	caughtUp(t, a, tt)
	s := savedWorkspaces(t, other).Sessions[0]
	if s.Tabs[0].Name != "" || s.Tabs[1].Name != "theirs" {
		t.Errorf("the server's tabs %q %q: half a batch made", s.Tabs[0].Name, s.Tabs[1].Name)
	}
	if one.Name != "" || two.Name != "theirs" {
		t.Errorf("the window's tabs %q %q", one.Name, two.Name)
	}
}

// TestSaveNowWhileTakeWaits saves at once, as a window that closes, while
// the outbox's take of another client's change waits for the main thread,
// the one saving: the take gives way, the save reaches the server at
// once, and the take comes after.
func TestSaveNowWhileTakeWaits(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	other := followApp(t, a)
	caughtUp(t, a, tt)
	a.newTab(os.TempDir()) // posted, as the other client's change will be
	if _, err := other.Command(rex.LayoutCommand{Op: "space.create", Name: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	// The take of it waits on the main thread, which runs no frame.
	waitForThat(t, func() bool {
		a.postMu.Lock()
		defer a.postMu.Unlock()
		return len(a.posted) > 0
	}, "a take waiting for the main thread")
	tab.Name = "closing"
	a.changed()
	start := time.Now()
	a.st.saveNow()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("the save took %v", d)
	}
	if got := savedWorkspaces(t, other).Sessions[0].Tabs[0].Name; got != "closing" {
		t.Errorf("the server's tab is %q", got)
	}
	caughtUp(t, a, tt)
	if tab.Name != "closing" || len(a.st.workspaces) != 2 {
		t.Errorf("after the take: tab %q, %d spaces", tab.Name, len(a.st.workspaces))
	}
}

// TestUseRunsDroppedDones switches the outbox's connection with a command
// queued: its done runs, with an error, as ask promises.
func TestUseRunsDroppedDones(t *testing.T) {
	a, _ := newTestApp(t)
	var o outbox
	o.use(a.client, func(fn func()) { go fn() }, nil)
	var got []rex.LayoutCommand
	o.add(rex.LayoutCommand{Op: "tab.create"}, func(res rex.LayoutCommand) { got = append(got, res) })
	o.use(a.client, func(fn func()) { go fn() }, nil)
	if len(got) != 1 || got[0].Code == "" {
		t.Errorf("the dropped command's answers: %+v", got)
	}
}

// TestSaveNowWithTakePosted saves at once, as a window that closes, while
// the take of another client's change is posted to the main thread, the
// one saving, and not run yet: the save reaches the server at once; the
// take, run after, shows the other client's change; and the revisions the
// next change builds on are the server's, so the server takes it.
func TestSaveNowWithTakePosted(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	other := followApp(t, a)
	caughtUp(t, a, tt)
	if _, err := other.Command(rex.LayoutCommand{Op: "space.create", Name: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	waitForThat(t, func() bool {
		a.postMu.Lock()
		defer a.postMu.Unlock()
		return len(a.posted) > 0
	}, "a take posted to the main thread")
	tab.Name = "closing"
	a.changed()
	start := time.Now()
	a.st.saveNow()
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("the save took %v", d)
	}
	if got := savedWorkspaces(t, other).Sessions[0].Tabs[0].Name; got != "closing" {
		t.Errorf("the server's tab is %q", got)
	}
	caughtUp(t, a, tt)
	if len(a.st.workspaces) != 2 || tab.Name != "closing" {
		t.Fatalf("after the take: %d spaces, tab %q", len(a.st.workspaces), tab.Name)
	}
	// The next change is built on the server's revisions: it is made.
	tab.Name = "after"
	a.changed()
	a.save()
	caughtUp(t, a, tt)
	if got := savedWorkspaces(t, other).Sessions[0].Tabs[0].Name; got != "after" || tab.Name != "after" {
		t.Errorf("the next change: server %q, window %q", got, tab.Name)
	}
}

// TestSaveNowLeavesNoCheck saves at once with nothing to send while
// another client's change is still to be taken: the window takes it
// still, the outbox not left stopped with a check to make.
func TestSaveNowLeavesNoCheck(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	other := followApp(t, a)
	caughtUp(t, a, tt)
	if _, err := other.Command(rex.LayoutCommand{Op: "tab.rename", Tab: tab.UID, Name: "theirs"}); err != nil {
		t.Fatal(err)
	}
	a.st.saveNow()
	waitFor(t, tt, "the other client's rename", func() bool { return tab.Name == "theirs" })
}

// TestUseRunsInFlightDone switches the outbox's connection while a command
// is on its way: its done runs once, with an error.
func TestUseRunsInFlightDone(t *testing.T) {
	a, _ := newTestApp(t)
	var o outbox
	o.use(a.client, func(fn func()) { go fn() }, func(rex.LayoutState) bool { return true })
	calls := make(chan rex.LayoutCommand, 4)
	// An action waits for a window that takes it and never answers.
	desk, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer desk.Close()
	go desk.TakeCommand()
	time.Sleep(100 * time.Millisecond)
	o.add(rex.LayoutCommand{Op: "action", Action: "tab.rename"}, func(res rex.LayoutCommand) { calls <- res })
	o.kick()
	waitForThat(t, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.sending
	}, "the command on its way")
	other, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	o.use(other, func(fn func()) { go fn() }, func(rex.LayoutState) bool { return true })
	select {
	case res := <-calls:
		if res.Code == "" {
			t.Errorf("the done of a switched connection: %+v", res)
		}
	case <-time.After(rex.CommandTimeout + 3*time.Second): // the action times out
		t.Fatal("the done did not run")
	}
	select {
	case res := <-calls:
		t.Errorf("the done ran twice: %+v", res)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestRemoveHostNoSpace removes the host of every pane of a window that
// shows no space: the window takes a space of its own for the new tab of
// this machine, and the other window's space is left as it was.
func TestRemoveHostNoSpace(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	b := st.openWindow(st.addWorkspace())
	followApp(t, a)
	caughtUp(t, a, tt)
	theirs := len(b.tabs)
	h := newHostConn(savedHost{ID: "h1", Name: "studio"})
	a.hosts = []*hostConn{h}
	rp := &Pane{ID: a.id(), SID: "far", host: h, info: rex.SessionInfo{ID: "far"}}
	rt := a.makeTab("")
	rt.Root = &Node{ID: a.id(), Pane: rp}
	rp.Tab, rp.Node = rt, rt.Root
	rt.setFocus(rp)
	a.closePanes(a.tabs)
	a.ws.app, a.ws = nil, nil
	a.tabs, a.active = []*Tab{rt}, 0
	a.removeHost(h)
	waitFor(t, tt, "a tab of this machine", func() bool {
		return len(a.tabs) == 1 && a.tabs[0].Focus != nil && a.tabs[0].Focus.host == nil && a.tabs[0].Focus.SID != ""
	})
	if a.ws == nil || a.ws == b.ws || len(b.tabs) != theirs {
		t.Errorf("window on %v, the other's space has %d tabs, had %d", a.ws, len(b.tabs), theirs)
	}
	quit(st)
}
