package main

import (
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// renameHost is a host whose server has some sessions and a layout, and a
// window watching it, as TestWatchHostSyncsRenamedPaneAndTab sets up, for
// the renames made here.
type renameHost struct {
	t       *testing.T
	remote  *rex.Client
	c       *rex.Client
	h       *hostConn
	a       *App
	runMain func(func())
}

func newRenameHost(t *testing.T, n int, layoutOf func(sids []string) string) (*renameHost, []string) {
	t.Helper()
	sock, remote := startHostServer(t)
	var sids []string
	for range n {
		s, err := remote.Create(rex.CreateOptions{Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, s.ID)
	}
	if err := remote.SetLayout([]byte(layoutOf(sids))); err != nil {
		t.Fatal(err)
	}
	c, err := rex.ConnectSocket(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	h := newHostConn(savedHost{ID: "h1", Name: "lab-box", Address: sock})
	t.Cleanup(h.close)
	h.mu.Lock()
	h.setClient(c)
	h.mu.Unlock()
	a := &App{hosts: []*hostConn{h}}
	mainQ := make(chan func(), 32)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case fn := <-mainQ:
				fn()
			case <-done:
				return
			}
		}
	}()
	r := &renameHost{t: t, remote: remote, c: c, h: h, a: a}
	r.runMain = func(fn func()) {
		reply := make(chan struct{})
		mainQ <- func() {
			fn()
			close(reply)
		}
		<-reply
	}
	go a.watchHost(h, c, func(fn func()) { mainQ <- fn })
	return r, sids
}

func (r *renameHost) waitFor(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		r.runMain(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

// names returns the names the host's layout gives its tabs, by ID, and
// its panes, by session.
func (r *renameHost) names() (tabs, panes map[string]string) {
	r.t.Helper()
	raw, err := r.remote.Layout()
	if err != nil {
		r.t.Fatal(err)
	}
	tabs, panes = map[string]string{}, map[string]string{}
	for _, w := range layout.Parse(raw).Sessions {
		for _, st := range w.Tabs {
			tabs[st.ID] = st.Name
			for _, root := range st.Roots() {
				for _, n := range layout.Leaves(root) {
					panes[n.SID] = n.Name
				}
			}
		}
	}
	return tabs, panes
}

func (r *renameHost) waitRemote(what string, cond func(tabs, panes map[string]string) bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond(r.names()) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	tabs, panes := r.names()
	r.t.Fatalf("timed out waiting for %s: tabs %v, panes %v", what, tabs, panes)
}

func splitLayout(s []string) string {
	return `{"version":2,"windows":["sp1"],"sessions":[{"id":"sp1","name":"Main","tabs":[{"id":"t1","root":{"vertical":true,"ratio":0.5,"a":{"sid":"` + s[0] + `"},"b":{"sid":"` + s[1] + `"}}}]}]}`
}

// A name given here to a pane or tab of a host goes to the host's layout,
// for its other clients; a name cleared here is cleared there; the names
// here stay as given, with no echo of the host's taking them back.
func TestRenamePushesPaneAndTabNameToHost(t *testing.T) {
	r, _ := newRenameHost(t, 2, splitLayout)
	a := r.a
	r.waitFor("adopt", func() bool { return len(a.tabs) == 1 && len(a.tabs[0].allPanes()) == 2 })

	var p *Pane
	r.runMain(func() {
		p = a.tabs[0].allPanes()[0]
		a.setPaneName(p, "build")
	})
	r.waitRemote("pane name on host", func(_, panes map[string]string) bool { return panes[p.SID] == "build" })

	r.runMain(func() { a.setTabName(a.tabs[0], "work") })
	r.waitRemote("tab name on host", func(tabs, _ map[string]string) bool { return tabs["t1"] == "work" })

	r.runMain(func() { a.setPaneName(p, "") })
	r.waitRemote("pane name cleared on host", func(_, panes map[string]string) bool { return panes[p.SID] == "" })

	// The host's layout events come back here: the names stay as given.
	time.Sleep(1500 * time.Millisecond)
	r.runMain(func() {
		if p.Name != "" || a.tabs[0].Name != "work" {
			t.Errorf("names here: pane %q, tab %q", p.Name, a.tabs[0].Name)
		}
		if r.h.renaming(renameKey{id: p.SID}) || r.h.renaming(renameKey{tab: true, id: "t1"}) {
			t.Error("a rename the host took is still kept")
		}
	})
}

// A tab here that is not plainly one tab of the host's, as one holding
// the panes of two of its tabs, sends no name to the host; its panes'
// names go all the same.
func TestRenameAmbiguousTabStaysLocal(t *testing.T) {
	r, sids := newRenameHost(t, 2, splitLayout)
	a := r.a
	r.waitFor("adopt", func() bool { return len(a.tabs) == 1 && len(a.tabs[0].allPanes()) == 2 })

	// The host splits its tab in two; the tab here keeps both panes.
	two := `{"version":2,"windows":["sp1"],"sessions":[{"id":"sp1","name":"Main","tabs":[{"id":"t1","root":{"sid":"` + sids[0] + `"}},{"id":"t2","root":{"sid":"` + sids[1] + `"}}]}]}`
	if err := r.remote.SetLayout([]byte(two)); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the host's two tabs", func() bool { return len(r.h.layoutTabs) == 2 })
	var second *Pane
	r.runMain(func() {
		if len(a.tabs) != 1 || len(a.tabs[0].allPanes()) != 2 {
			t.Errorf("tabs here: %d", len(a.tabs))
			return
		}
		if h, id := hostTabOf(a.tabs[0]); h != nil {
			t.Errorf("hostTabOf = %q, want none", id)
		}
		second = a.tabs[0].allPanes()[1]
		a.setTabName(a.tabs[0], "both")
		if r.h.renamesWaiting() {
			t.Error("a name of the tab waits for the host")
		}
		a.setPaneName(second, "logs")
	})
	if t.Failed() {
		t.FailNow()
	}
	r.waitRemote("pane name on host", func(_, panes map[string]string) bool { return panes[second.SID] == "logs" })
	if tabs, _ := r.names(); tabs["t1"] != "" || tabs["t2"] != "" {
		t.Fatalf("host tabs = %v, want no names", tabs)
	}
}

// A tab here split since it opened, so that the host's tab has fewer
// panes, still takes the name the host gives that tab (#621).
func TestRenameDesktopSplitStillSyncsHostTabName(t *testing.T) {
	r, _ := newRenameHost(t, 1, func(s []string) string {
		return `{"version":2,"windows":["sp1"],"sessions":[{"id":"sp1","name":"Main","tabs":[{"id":"t1","root":{"sid":"` + s[0] + `"}}]}]}`
	})
	a := r.a
	r.waitFor("adopt", func() bool { return len(a.tabs) == 1 && a.tabs[0].Focus != nil })
	// The session starts and its pane takes it in one go on the main
	// thread, as no list of the host's comes between them: as newPaneRun's
	// expecting does.
	r.runMain(func() {
		s2, err := r.remote.Create(rex.CreateOptions{Cols: 80, Rows: 24})
		if err != nil {
			t.Error(err)
			return
		}
		a.splitWith(a.tabs[0].Focus, &Pane{ID: a.id(), SID: s2.ID, host: r.h}, false)
	})
	if t.Failed() {
		t.FailNow()
	}
	r.waitFor("the split", func() bool { return len(a.tabs) == 1 && len(a.tabs[0].allPanes()) == 2 })
	if res, err := r.remote.Command(rex.LayoutCommand{Op: "tab.rename", Tab: "t1", Name: "from-host"}); err != nil || res.Code != "" {
		t.Fatalf("tab.rename: err=%v res=%+v", err, res)
	}
	r.waitFor("the host's tab name", func() bool { return a.tabs[0].Name == "from-host" })
}

// A name given here that waits to go to the host is not overwritten by
// the name of before in the host's layout.
func TestSyncHostNamesSkipsWaitingRenames(t *testing.T) {
	r, sids := newRenameHost(t, 1, func(s []string) string {
		return `{"version":2,"windows":["sp1"],"sessions":[{"id":"sp1","name":"Main","tabs":[{"id":"t1","name":"host-tab","root":{"sid":"` + s[0] + `","name":"host-pane"}}]}]}`
	})
	a := r.a
	r.waitFor("adopt", func() bool {
		return len(a.tabs) == 1 && a.tabs[0].Name == "host-tab" && a.tabs[0].Focus != nil && a.tabs[0].Focus.Name == "host-pane"
	})
	r.runMain(func() {
		tab := a.tabs[0]
		tab.Name, tab.Focus.Name = "mine", "my-pane"
		r.h.mu.Lock()
		r.h.renames = map[renameKey]string{{tab: true, id: tab.UID}: "mine", {id: sids[0]}: "my-pane"}
		r.h.mu.Unlock()
		a.syncHostNames(r.h, hostLayout{tabs: r.h.layoutTabs})
		if tab.Name != "mine" || tab.Focus.Name != "my-pane" {
			t.Errorf("names here: tab %q, pane %q", tab.Name, tab.Focus.Name)
		}
		// Taken by the host, the names follow it again.
		r.h.mu.Lock()
		r.h.renames = nil
		r.h.mu.Unlock()
		a.syncHostNames(r.h, hostLayout{tabs: r.h.layoutTabs})
		if tab.Name != "host-tab" || tab.Focus.Name != "host-pane" {
			t.Errorf("names here: tab %q, pane %q", tab.Name, tab.Focus.Name)
		}
	})
}

// A name given while the host is away stays here, and goes to the host as
// it connects again.
func TestRenameWaitsForHostConnection(t *testing.T) {
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, remote := startHostServer(t)
	s, err := remote.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	// The host's space has the name of this window's, whose tabs its tab
	// joins (adopt).
	if err := remote.SetLayout([]byte(`{"version":2,"windows":["sp1"],"sessions":[{"id":"sp1","name":"` + a.ws.Name + `","tabs":[{"id":"t1","root":{"sid":"` + s.ID + `","name":"old"}}]}]}`)); err != nil {
		t.Fatal(err)
	}
	proxy := newHostProxy(t, sock)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "lab-box", Address: proxy.path})
	var tab *Tab
	waitFor(t, tt, "adopt", func() bool {
		if ts := a.tabsOn(h); len(ts) == 1 && ts[0].Focus != nil && ts[0].Focus.Name == "old" {
			tab = ts[0]
		}
		return tab != nil
	})
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.state != hostConnected && h.current() == nil })
	a.setPaneName(tab.Focus, "new")
	a.setTabName(tab, "tab-new")
	if !h.renaming(renameKey{id: s.ID}) || !h.renaming(renameKey{tab: true, id: "t1"}) {
		t.Fatal("the renames are not kept for the host")
	}
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the host to connect again", func() bool { return h.state == hostConnected })
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := remote.Layout()
		if err != nil {
			t.Fatal(err)
		}
		st := layout.Parse(raw)
		if len(st.Sessions) == 1 && len(st.Sessions[0].Tabs) == 1 {
			ht := st.Sessions[0].Tabs[0]
			if ht.Name == "tab-new" && ht.Root != nil && ht.Root.Name == "new" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("host layout = %s", raw)
		}
		tt.Frame()
		time.Sleep(50 * time.Millisecond)
	}
	waitFor(t, tt, "the renames to be forgotten", func() bool { return !h.renamesWaiting() })
	if tab.Name != "tab-new" || tab.Focus.Name != "new" {
		t.Errorf("names here: tab %q, pane %q", tab.Name, tab.Focus.Name)
	}
}
