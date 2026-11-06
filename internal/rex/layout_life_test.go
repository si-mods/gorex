//go:build darwin || linux

package rex

import (
	"context"
	"encoding/json"
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

// waitForSession waits for session sid to be listed, or not.
func waitForSession(t *testing.T, c *Client, sid string, listed bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := liveSession(t, c, sid); ok == listed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s listed is not %v", sid, listed)
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
	// Forgotten once a client polling the sessions could see it exit.
	waitForSession(t, c, two.New, false)
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
	t.Cleanup(func() {
		if ss := s.sessions["moved"]; ss != nil {
			ss.kill()
		}
	})
	seed(t, c,
		layout.Tab{ID: "a", Root: &layout.Node{SID: "back", Dir: dir, Cols: 90, Rows: 20}},
		layout.Tab{ID: "b", Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: "far", Host: "h"}, B: &layout.Node{URL: "https://example.com/", ID: "web"}}},
		layout.Tab{ID: "gone", Root: &layout.Node{SID: "moved", Dir: filepath.Join(dir, "removed-worktree")}})
	s.restoreLost(nil, s.lostPanes())
	in, ok := liveSession(t, c, "back")
	if !ok || in.Cols != 90 || in.Rows != 20 {
		t.Fatalf("the lost session is %+v, %v", in, ok)
	}
	// A directory gone, as a removed work tree, starts the shell at home.
	home, _ := os.UserHomeDir()
	if in, ok := liveSession(t, c, "moved"); !ok || !sameDir(s.sessions["moved"].dir, home) {
		t.Errorf("the session of a directory gone is %+v, %v", in, ok)
	}
	if st := storeOf(t, c); len(st.Sessions[0].Tabs) != 3 {
		t.Fatalf("the layout lost a tab: %s", must(c.Layout()))
	}
	c.Kill("moved")

	// One that does not start: its pane goes, the tab with it.
	seed(t, c,
		layout.Tab{ID: "a", Root: &layout.Node{SID: "back"}},
		layout.Tab{ID: "c", Root: &layout.Node{SID: "fails", Dir: dir}},
		layout.Tab{ID: "b", Root: &layout.Node{SID: "far", Host: "h"}})
	t.Setenv("SHELL", filepath.Join(dir, "no-such-shell"))
	s.restoreLost(nil, s.lostPanes())
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
// without the focus, as one this machine's client starts with no app does.
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
	if len(w.Tabs) != 4 || w.Active != 0 {
		t.Fatalf("the tabs: %s", must(c.Layout()))
	}
	got := w.Tabs[2].Root
	if w.Tabs[1].Root.SID != local.ID || got.SID != peer.ID || got.Cols != 97 || got.Rows != 31 || w.Tabs[3].ID != "far" {
		t.Errorf("the tabs: %s", must(c.Layout()))
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

	// A session no pane has, with an address hosts.json lacks too: no pane,
	// as the CLI ends the source then.
	// An app takes commands, so the session gets no tab.
	app := pipe(src, "")
	defer app.Close()
	go app.TakeCommand()
	for src.cmdMu.Lock(); src.desks == 0; src.cmdMu.Lock() {
		src.cmdMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	src.cmdMu.Unlock()
	headless := must(local.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: work}))
	if res, _ := local.Command(LayoutCommand{Op: "pane.teleported", SID: headless.ID, Target: in.ID, Host: "tcNONE"}); res.Code != "pane_not_found" {
		t.Errorf("a session no pane has: %+v", res)
	}
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

// TestRestoreInBackground checks that the sessions past those the server
// started before it listened start in the background: the layout answers
// meanwhile, and an attach to one of them waits for it.
func TestRestoreInBackground(t *testing.T) {
	s, c := applyFixture(t)
	dir := os.Getenv("GOREX_DIR")
	seed(t, c, layout.Tab{ID: "a", Root: &layout.Node{SID: "later", Dir: dir}})
	lost := s.lostPanes()
	if len(lost) != 1 {
		t.Fatalf("lost %+v", lost)
	}
	got := make(chan *session, 1)
	go func() { got <- s.awaitRestore("later") }()
	start := time.Now()
	if _, err := c.Layout(); err != nil || time.Since(start) > time.Second {
		t.Fatalf("the layout waited %v for the restore: %v", time.Since(start), err)
	}
	select {
	case <-got:
		t.Fatal("the attach did not wait for the restore")
	case <-time.After(100 * time.Millisecond):
	}
	s.restoreLost(nil, lost)
	if ss := <-got; ss == nil || ss.id != "later" {
		t.Fatalf("the attach got %v", ss)
	}
	c.Kill("later")
}

// TestOrphansPlaced checks that a session of this machine no pane has gets
// a tab, after this machine's tabs and without the focus: at once when no
// app runs, as gorex new with the app closed; else as the app opens next,
// as before the server placed sessions; and once the server restored the
// rest, as of a layout.json lost.
func TestOrphansPlaced(t *testing.T) {
	s, c := applyFixture(t)
	cmd, dir := testShell()
	seed(t, c,
		layout.Tab{ID: "mine", Root: &layout.Node{SID: "1"}},
		layout.Tab{ID: "far", Root: &layout.Node{SID: "x", Host: "h"}})
	closed := must(c.Create(CreateOptions{Command: cmd, Dir: dir}))
	w := storeOf(t, c).Sessions[0]
	if len(w.Tabs) != 3 || w.Tabs[1].Root.SID != closed.ID || w.Tabs[2].ID != "far" || w.Active != 0 {
		t.Fatalf("with no app: %s", must(c.Layout()))
	}
	app := pipe(s, "")
	go app.TakeCommand()
	for s.cmdMu.Lock(); s.desks == 0; s.cmdMu.Lock() {
		s.cmdMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	s.cmdMu.Unlock()
	open := must(c.Create(CreateOptions{Command: cmd, Dir: dir}))
	if len(storeOf(t, c).Sessions[0].Tabs) != 3 {
		t.Fatalf("placed while the app runs: %s", must(c.Layout()))
	}
	app.Close()
	next := pipe(s, "")
	defer next.Close()
	go next.TakeCommand()
	st := waitStore(t, c, "the tab as the app opens", func(st layout.Store) bool { return len(st.Sessions[0].Tabs) == 4 })
	if st.Sessions[0].Tabs[2].Root.SID != open.ID {
		t.Errorf("the tabs: %s", must(c.Layout()))
	}

	// After a restore, as of a layout.json lost.
	late := must(c.Create(CreateOptions{Command: cmd, Dir: dir}))
	s.restoreLost(nil, nil)
	if _, ok := find(&layout.Store{Sessions: storeOf(t, c).Sessions}, "", late.ID); !ok {
		t.Errorf("the session left without a pane: %s", must(c.Layout()))
	}
}

// TestKilledPaneGoesAtOnce checks that the pane of a session that ends is
// out of the layout before anything could start it again: a kill's before
// its answer, an exit's at once, the refresh far off.
func TestKilledPaneGoesAtOnce(t *testing.T) {
	old := refreshWait.set(time.Minute)
	t.Cleanup(func() { refreshWait.set(old) })
	_, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	two := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	if err := c.Kill(one.New); err != nil {
		t.Fatal(err)
	}
	if _, ok := find(&layout.Store{Sessions: storeOf(t, c).Sessions}, "", one.New); ok {
		t.Error("the killed pane is in the layout after the kill answered")
	}
	if err := c.Input(two.New, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	waitStore(t, c, "the exited pane to go", func(st layout.Store) bool {
		_, ok := find(&st, "", two.New)
		return !ok
	})
}

// TestTeleportPending checks the teleport under way the layout notes, which
// the app opens no tab for: pane.teleporting notes it by the host's ID, a
// failed teleport and the end of the source drop it, and pane.teleported
// does once it shows the session.
func TestTeleportPending(t *testing.T) {
	s, c := applyFixture(t)
	writeFile(t, filepath.Join(s.dir, "hosts.json"), `{"hosts":[{"id":"h1","address":"tcDST"}]}`)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	command(t, c, LayoutCommand{Op: "pane.teleporting", SID: one.New, Target: "remote1", Host: "tcDST"})
	if tp := storeOf(t, c).Teleports; len(tp) != 1 || tp[0] != (layout.Teleport{Host: "h1", SID: "remote1", From: one.New}) {
		t.Fatalf("the teleports %+v", tp)
	}
	command(t, c, LayoutCommand{Op: "pane.teleporting", SID: one.New, Host: "tcDST"})
	if tp := storeOf(t, c).Teleports; len(tp) != 0 {
		t.Errorf("a failed teleport stays: %+v", tp)
	}
	command(t, c, LayoutCommand{Op: "pane.teleporting", SID: one.New, Target: "remote2", Host: "tcDST"})
	command(t, c, LayoutCommand{Op: "pane.teleported", SID: one.New, Target: "remote2", Host: "tcDST"})
	st := storeOf(t, c)
	if _, ok := findLeaf(&st, hostLeaf("h1", "remote2")); !ok || len(st.Teleports) != 0 {
		t.Errorf("after the teleport: %s", must(c.Layout()))
	}
	two := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	command(t, c, LayoutCommand{Op: "pane.teleporting", SID: two.New, Target: "remote3", Host: "tcDST"})
	c.Kill(two.New)
	if tp := storeOf(t, c).Teleports; len(tp) != 0 {
		t.Errorf("the teleport of a session that ended stays: %+v", tp)
	}
}

// TestStampAfterRun checks the program a pane ran before a crash: a shell
// that ran nothing since keeps it, and loses it once a program ran and
// went, a short one too; a title that animates changes no revision.
func TestStampAfterRun(t *testing.T) {
	quickRefresh(t)
	_, c := applyFixture(t)
	_, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: []string{"/bin/sh"}})
	st := storeOf(t, c)
	st.Sessions[0].Tabs[0].Root.Prog, st.Sessions[0].Tabs[0].Root.Args = "vim", []string{"vim"}
	raw, _ := json.Marshal(st)
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	c.Input(one.New, []byte("echo hi\n"))
	time.Sleep(300 * time.Millisecond)
	if n := storeOf(t, c).Sessions[0].Tabs[0].Root; n.Prog != "vim" {
		t.Fatalf("a shell that ran nothing lost the program: %+v", n)
	}
	c.Input(one.New, []byte("sleep 1\n"))
	waitStore(t, c, "the program run to go", func(st layout.Store) bool {
		n := st.Sessions[0].Tabs[0].Root
		return n.Prog == "" && len(n.Args) == 0
	})
	rev, lines := must(c.LayoutState()).Revision, journalLines()
	c.Input(one.New, []byte("i=0; while [ $i -lt 20 ]; do printf '\\033]2;t%s\\007' $i; i=$((i+1)); sleep 0.05; done\n"))
	time.Sleep(1500 * time.Millisecond)
	if got := must(c.LayoutState()).Revision; got != rev || journalLines() != lines {
		t.Errorf("an animating title changed the revision %d to %d, the journal %d to %d", rev, got, lines, journalLines())
	}
}
