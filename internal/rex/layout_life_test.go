//go:build darwin || linux

package rex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
)

// quickRefresh refreshes the layout soon after a change of the sessions,
// for the test.
func quickRefresh(t *testing.T) {
	old := refreshWait.set(20 * time.Millisecond)
	t.Cleanup(func() { refreshWait.set(old) })
}

// waitStore waits for the layout to be as ok says.
func waitStore(t *testing.T, c *Client, what string, ok func(layout.Store) bool) layout.Store {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		st := storeOf(t, c)
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %s", what, must(c.Layout()))
		}
	}
}

// journalLines counts the layouts the journal kept.
func journalLines() int {
	n := 0
	for _, e := range readJournal() {
		if e.Event == eventLayout {
			n++
		}
	}
	return n
}

// TestExitPrunes checks that the pane of a session that exits goes, with
// no window: the other side of its split takes the split's place, in one
// revision, and the session is forgotten; a session a client kills takes
// its pane, and its tab with its last, too.
func TestExitPrunes(t *testing.T) {
	quickRefresh(t)
	_, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	two := command(t, c, LayoutCommand{Op: "pane.split", SID: one.New, Command: cmd})
	three := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	rev := must(c.LayoutState()).Revision
	if err := c.Input(two.New, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	st := waitStore(t, c, "the exited pane to go", func(st layout.Store) bool {
		return layout.Find(st.Sessions[0].Tabs[0].Root, two.New) == nil
	})
	w := st.Sessions[0]
	if len(w.Tabs) != 2 || w.Tabs[0].Root.SID != one.New || !w.Tabs[0].Root.IsLeaf() || w.Tabs[0].Focus != 0 {
		t.Errorf("after the exit: %s", must(c.Layout()))
	}
	if got := must(c.LayoutState()).Revision; got != rev+1 {
		t.Errorf("revision %d, want %d", got, rev+1)
	}
	if _, ok := liveSession(t, c, two.New); ok {
		t.Error("the exited session is still listed")
	}
	if err := c.Kill(three.New); err != nil {
		t.Fatal(err)
	}
	st = waitStore(t, c, "the killed pane's tab to go", func(st layout.Store) bool { return len(st.Sessions[0].Tabs) == 1 })
	if st.Sessions[0].Tabs[0].Root.SID != one.New {
		t.Errorf("after the kill: %s", must(c.Layout()))
	}
}

// TestNoPruneWhileStopping checks that a session that exits as the run
// stops, or while an upgrade hands the sessions on, keeps its pane: the
// run after it starts the session again.
func TestNoPruneWhileStopping(t *testing.T) {
	quickRefresh(t)
	for _, upgrade := range []bool{false, true} {
		name := "stopping"
		if upgrade {
			name = "upgrade"
		}
		t.Run(name, func(t *testing.T) {
			s, c := applyFixture(t)
			cmd, dir := testShell()
			one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
			before := must(c.Layout())
			if upgrade {
				// An upgrade holds the ops, and the run stops once the
				// handoff is written.
				s.ops.Lock()
			} else {
				s.mu.Lock()
				s.stopping = true
				s.mu.Unlock()
			}
			if err := c.Input(one.New, []byte("exit\n")); err != nil {
				t.Fatal(err)
			}
			waitInfo(t, c, one.New, "the session to exit", func(in SessionInfo) bool { return in.Exited })
			time.Sleep(100 * time.Millisecond)
			if upgrade {
				s.mu.Lock()
				s.stopping = true
				s.mu.Unlock()
				s.ops.Unlock()
				time.Sleep(100 * time.Millisecond)
			}
			s.mu.Lock()
			l := s.layout
			s.mu.Unlock()
			if string(l) != string(before) {
				t.Errorf("the layout changed: %s", l)
			}
		})
	}
}

// TestRestoreLost checks the sessions the run before lost: those past the
// first the server started before it listened start once it listens, at
// their panes' places, and the pane of one that does not start goes,
// another host's and a page staying.
func TestRestoreLost(t *testing.T) {
	s, c := applyFixture(t)
	dir := os.Getenv("GOREX_DIR")
	t.Cleanup(func() {
		if ss := s.sessions["back"]; ss != nil {
			ss.kill()
		}
	})
	seed(t, c,
		layout.Tab{ID: "a", Root: &layout.Node{SID: "back", Dir: dir, Cols: 90, Rows: 20}},
		layout.Tab{ID: "b", Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: "far", Host: "h"}, B: &layout.Node{URL: "https://example.com/", ID: "web"}}})
	s.restored = make(chan struct{})
	s.restoreLost(nil)
	in, ok := liveSession(t, c, "back")
	if !ok || in.Cols != 90 || in.Rows != 20 {
		t.Fatalf("the lost session is %+v, %v", in, ok)
	}
	if st := storeOf(t, c); len(st.Sessions[0].Tabs) != 2 {
		t.Fatalf("the layout lost a tab: %s", must(c.Layout()))
	}

	// One that does not start: its pane goes, the tab with it.
	seed(t, c,
		layout.Tab{ID: "a", Root: &layout.Node{SID: "back"}},
		layout.Tab{ID: "c", Root: &layout.Node{SID: "fails", Dir: dir}},
		layout.Tab{ID: "b", Root: &layout.Node{SID: "far", Host: "h"}})
	t.Setenv("SHELL", filepath.Join(dir, "no-such-shell"))
	s.restored = make(chan struct{})
	s.restoreLost(nil)
	var ids []string
	for _, x := range storeOf(t, c).Sessions[0].Tabs {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "a,b" {
		t.Errorf("the tabs are %v", ids)
	}
	if _, ok := liveSession(t, c, "fails"); ok {
		t.Error("the session that failed is listed")
	}
}

// TestPeerSessionGetsTab checks that a session a remote client starts here
// gets a tab of its own, in the default space after this machine's tabs,
// without the focus; one of this machine's own client gets none.
func TestPeerSessionGetsTab(t *testing.T) {
	s, c := applyFixture(t)
	cmd, dir := testShell()
	seed(t, c,
		layout.Tab{ID: "mine", Root: &layout.Node{SID: "1"}},
		layout.Tab{ID: "far", Root: &layout.Node{SID: "x", Host: "h"}})
	local := must(c.Create(CreateOptions{Command: cmd, Dir: dir}))
	data, err := s.do(Request{Op: "create", Create: &CreateOptions{Command: cmd, Dir: dir, Cols: 97, Rows: 31}}, testPeer, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer := data.(SessionInfo)
	w := storeOf(t, c).Sessions[0]
	if len(w.Tabs) != 3 || w.Active != 0 {
		t.Fatalf("the tabs: %s", must(c.Layout()))
	}
	got := w.Tabs[1].Root
	if got.SID != peer.ID || got.Cols != 97 || got.Rows != 31 || w.Tabs[2].ID != "far" {
		t.Errorf("the peer's tab is %+v", got)
	}
	if _, ok := find(&layout.Store{Sessions: []layout.Workspace{w}}, "", local.ID); ok {
		t.Error("this machine's own session got a tab")
	}
}

// TestFactsStamped checks that the facts of the sessions reach the layout
// as they change, a split's anchor's size included, with no revision and
// no journal entry, and that a layout left alone changes nothing.
func TestFactsStamped(t *testing.T) {
	quickRefresh(t)
	s, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	command(t, c, LayoutCommand{Op: "pane.split", SID: one.New, Command: cmd})
	ls := must(c.LayoutState())
	lines := journalLines()
	if err := c.Resize(one.New, 40, 24); err != nil {
		t.Fatal(err)
	}
	waitStore(t, c, "the anchor's size", func(st layout.Store) bool {
		n := layout.Find(st.Sessions[0].Tabs[0].Root, one.New)
		return n != nil && n.Cols == 40
	})
	if got := must(c.LayoutState()).Revision; got != ls.Revision {
		t.Errorf("the facts counted a revision: %d, was %d", got, ls.Revision)
	}
	if n := journalLines(); n != lines {
		t.Errorf("the journal has %d layouts, had %d", n, lines)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "layout.json"))
	if err != nil || !strings.Contains(string(b), `"cols":40`) {
		t.Errorf("layout.json has %s, %v", b, err)
	}
	// Left alone, nothing changes.
	now := must(c.Layout())
	time.Sleep(200 * time.Millisecond)
	if string(must(c.Layout())) != string(now) || journalLines() != lines {
		t.Error("the layout changed while left alone")
	}
}

// TestPaneRestart checks that pane.restart starts a shell in the pane's
// place, in its directory and at its size, with the focus, and ends the
// pane's own session.
func TestPaneRestart(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd, Name: "t"})
	two := command(t, c, LayoutCommand{Op: "pane.split", SID: one.New, Command: cmd, Name: "kept"})
	command(t, c, LayoutCommand{Op: "pane.focus", SID: one.New})
	was, _ := liveSession(t, c, two.New)
	res := command(t, c, LayoutCommand{Op: "pane.restart", SID: two.New})
	tab := storeOf(t, c).Sessions[0].Tabs[0]
	n := tab.Root.B
	if res.New == "" || n.SID != res.New || n.Name != "kept" || tab.Focus != 1 {
		t.Fatalf("after the restart: %s", must(c.Layout()))
	}
	if _, ok := liveSession(t, c, two.New); ok {
		t.Error("the pane's old session runs")
	}
	in, _ := liveSession(t, c, res.New)
	if in.Cols != was.Cols || in.Rows != was.Rows || !sameDir(n.Dir, dir) {
		t.Errorf("the new session %dx%d in %q, the old %dx%d", in.Cols, in.Rows, n.Dir, was.Cols, was.Rows)
	}
}

// TestTeleportWithoutWindow teleports a pane with no window: the server
// maps the host's address to its ID by hosts.json, shows the host's
// session in a tab after the pane's, active and named as the pane's was,
// ends the pane's session, and moves a pane of the host's session the app
// placed meanwhile rather than show it twice.
func TestTeleportWithoutWindow(t *testing.T) {
	_, work := teleportRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	src, _ := testServer(t)
	dst, _ := testServer(t)
	src.journal = openJournal()
	t.Cleanup(func() { src.journal.close() })
	src.home, dst.home = resolved(t), resolved(t)
	remote := pipe(dst, testPeer)
	t.Cleanup(func() {
		for _, in := range must(remote.List()) {
			remote.Kill(in.ID)
		}
		remote.Close()
	})
	writeFile(t, filepath.Join(src.dir, "hosts.json"), `{"hosts":[{"id":"h1","name":"dst","address":" tcDST "},{"id":"h2","name":"x","address":"tcX"}]}`)
	local, sid := shellIn(t, src, filepath.Join(work, "sub"))
	seed(t, local,
		layout.Tab{ID: "first", Name: "work", Root: &layout.Node{SID: sid, Name: "shell"}},
		layout.Tab{ID: "other", Root: &layout.Node{URL: "https://example.com/", ID: "web"}})
	in, err := Teleport(context.Background(), local, sid, remote, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	// The target placed the session, a remote client's, in a tab there.
	there := pipe(dst, "")
	defer there.Close()
	if st := storeOf(t, there); len(st.Sessions) != 1 || st.Sessions[0].Tabs[0].Root.SID != in.ID {
		t.Errorf("the target's layout: %s", must(there.Layout()))
	}
	// The app here placed the host's new session meanwhile.
	adopted := layout.Tab{ID: "adopted", Root: &layout.Node{SID: in.ID, Host: "h1"}}
	command(t, local, LayoutCommand{Op: "tab.set", Space: "w", TabLayout: &adopted, After: "other"})

	if res, _ := local.Command(LayoutCommand{Op: "pane.teleported", SID: sid, Target: in.ID, Host: "tcNONE"}); res.Code != "host_not_found" {
		t.Errorf("a host hosts.json lacks: %+v", res)
	}
	command(t, local, LayoutCommand{Op: "pane.teleporting", SID: sid, Target: in.ID, Host: "tcDST"})
	res := command(t, local, LayoutCommand{Op: "pane.teleported", SID: sid, Target: in.ID, Host: "tcDST"})
	w := storeOf(t, local).Sessions[0]
	var ids []string
	for _, x := range w.Tabs {
		ids = append(ids, x.ID)
	}
	if len(w.Tabs) != 2 || w.Tabs[0].ID != res.Tab || w.Tabs[1].ID != "other" || w.Active != 0 {
		t.Fatalf("the tabs %v, active %d", ids, w.Active)
	}
	n := w.Tabs[0].Root
	if n.Host != "h1" || n.SID != in.ID || n.Name != "shell" || w.Tabs[0].Name != "work" {
		t.Errorf("the host's tab %+v, named %q", n, w.Tabs[0].Name)
	}
	if _, ok := liveSession(t, local, sid); ok {
		t.Error("the pane's session runs here")
	}
	// An app that places the session after the server did moves it: the
	// host's session is in one pane still.
	late := layout.Tab{ID: "late", Root: &layout.Node{SID: in.ID, Host: "h1"}}
	command(t, local, LayoutCommand{Op: "tab.set", Space: "w", TabLayout: &late, After: "other"})
	ids = nil
	for _, x := range storeOf(t, local).Sessions[0].Tabs {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "other,late" {
		t.Errorf("after a late placement the tabs are %v", ids)
	}
}
