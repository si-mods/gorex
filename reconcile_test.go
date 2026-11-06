package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// followApp has the test app follow its server's layout, as adopt does,
// and saves its layout; other is another client of the server.
func followApp(t *testing.T, a *App) (other *rex.Client) {
	t.Helper()
	a.saveNow()
	a.st.writer.use(a.client)
	if _, err := a.st.read(); err != nil {
		t.Fatal(err)
	}
	go a.st.follow(a.client, a.post)
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
	if rev := a.st.writer.revision(); rev != ls.Revision {
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

// TestFollowStaleWrite saves a change the window made of the layout
// before another client's: the server refuses it, and the window takes the
// other's layout, which stays, as a window that opens while a client's
// change lands does.
func TestFollowStaleWrite(t *testing.T) {
	a, tt := newTestApp(t)
	other := followApp(t, a)
	s := savedWorkspaces(t, other)
	s.Sessions[0].Tabs[0].Name = "theirs"
	setOther(t, other, s)
	// Before the window hears of it, it saves a change of its own.
	a.tab().Name = "mine"
	a.st.save()
	waitFor(t, tt, "the window to take the other's", func() bool { return a.tab().Name == "theirs" })
	a.st.writer.wait(time.Second)
	if got := savedWorkspaces(t, other).Sessions[0].Tabs[0].Name; got != "theirs" {
		t.Errorf("the server's tab is %q", got)
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
	b := &App{st: st, client: a.client, stop: make(chan struct{})}
	st.apps = []*App{b}
	b.show(w, nil)
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
	if rev := st.writer.revision(); rev == 0 {
		t.Error("the app shows no revision of the new connection")
	}
	quit(st)
}
