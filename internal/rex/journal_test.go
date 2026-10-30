//go:build darwin || linux

package rex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"gorex/internal/layout"
)

// testLayout is a layout of one workspace, its tab split by panes.
func testLayout(panes ...*layout.Node) json.RawMessage {
	root := panes[0]
	if len(panes) > 1 {
		root = &layout.Node{A: panes[0], B: panes[1], Vertical: true, Ratio: 0.5}
	}
	w := layout.Workspace{
		ID: "w1", Name: "icy-ridge",
		Layout: layout.Layout{Tabs: []layout.Tab{{Root: root}}},
	}
	b, _ := json.Marshal(layout.Store{Version: 2, Sessions: []layout.Workspace{w}, Windows: []string{w.ID}})
	return b
}

// connectTest connects to a server of a directory of its own, and ends it
// when the test does.
func connectTest(t *testing.T) (*Client, Hello) {
	t.Helper()
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Shutdown()
		c.Close()
		waitStopped(t)
	})
	return c, h
}

// journalStopped tells whether the run before this one stopped cleanly: the
// last entry it left is a stop, not a layout it kept or a start.
func journalStopped(entries []journalEntry) bool {
	if len(entries) == 0 {
		return false
	}
	return entries[len(entries)-1].Event == eventStop
}

// waitStopped waits for the server to stop, which takes its socket with it.
func waitStopped(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		c, err := ConnectExisting()
		if err != nil {
			return
		}
		c.Close()
	}
	t.Fatal("the server answers still")
}

// TestJournalKeepsLayouts checks that every layout the app sends is in the
// journal, the newest last, after the layout.json it also keeps.
func TestJournalKeepsLayouts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	c, _ := connectTest(t)
	first := testLayout(&layout.Node{SID: "one", Dir: "/tmp"})
	second := testLayout(&layout.Node{SID: "two", Dir: dir})
	for _, l := range []json.RawMessage{first, second} {
		if err := c.SetLayout(l); err != nil {
			t.Fatal(err)
		}
	}
	entries := readJournal()
	if len(entries) == 0 || entries[0].Event != eventStart {
		t.Fatalf("the journal starts with %+v", entries)
	}
	if got := lastLayout(entries); string(got) != string(second) {
		t.Errorf("the last layout is %s, not %s", got, second)
	}
	if b, err := os.ReadFile(journalPath()); err != nil || len(b) == 0 {
		t.Fatalf("the journal on disk: %v", err)
	}
	if raw, err := c.Layout(); err != nil || string(raw) != string(second) {
		t.Errorf("the server keeps %s, %v", raw, err)
	}
}

// TestCrashRestoresSessions kills a server the way a crash does: the server
// that starts in its place starts the sessions of the last layout again, at
// the directories of their panes, with their ids, so that the windows find
// them where they were.
func TestCrashRestoresSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	c, h := connectTest(t)
	one, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: "/tmp", Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	two, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	l := testLayout(
		&layout.Node{SID: one.ID, Dir: "/tmp", Cols: 100, Rows: 30},
		&layout.Node{SID: two.ID, Dir: dir, Cols: 80, Rows: 24},
	)
	if err := c.SetLayout(l); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := syscall.Kill(h.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitStopped(t)
	if journalStopped(readJournal()) {
		t.Fatal("the run that was killed left a stop record")
	}

	next, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		next.Shutdown()
		next.Close()
	}()
	infos, err := next.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SessionInfo{}
	for _, in := range infos {
		got[in.ID] = in
	}
	for _, want := range []struct {
		id  string
		dir string
	}{{one.ID, "/tmp"}, {two.ID, dir}} {
		in, ok := got[want.id]
		if !ok {
			t.Fatalf("session %s is not back: %+v", want.id, infos)
		}
		if in.Exited {
			t.Errorf("session %s came back exited", want.id)
		}
		// The directory of a process is the one it resolved to.
		got, _ := filepath.EvalSymlinks(in.Dir)
		wantDir, _ := filepath.EvalSymlinks(want.dir)
		if got != wantDir {
			t.Errorf("session %s is in %q, not %q", want.id, in.Dir, want.dir)
		}
	}
	if raw, err := next.Layout(); err != nil || string(raw) != string(l) {
		t.Errorf("the layout is %s, %v; not %s", raw, err, l)
	}
	// A session of the layout starts at its pane's size.
	for _, in := range infos {
		if in.ID == one.ID && (in.Cols != 100 || in.Rows != 30) {
			t.Errorf("session %s came back at %dx%d", in.ID, in.Cols, in.Rows)
		}
	}
}

// TestCleanStopRestoresNothing checks that a server that was told to stop
// leaves a record of it, and that the server after it starts no session: the
// sessions ended with the run, as it said.
func TestCleanStopRestoresNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	c, _ := connectTest(t)
	one, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	l := testLayout(&layout.Node{SID: one.ID, Dir: dir, Cols: 80, Rows: 24})
	if err := c.SetLayout(l); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	waitStopped(t)
	entries := readJournal()
	if !journalStopped(entries) {
		t.Fatalf("no stop record in the journal: %+v", entries)
	}
	stop := entries[len(entries)-1]
	if stop.Reason != "request" || len(stop.Sessions) != 1 || stop.Sessions[0].ID != one.ID || stop.Sessions[0].Dir != dir {
		t.Errorf("the stop record is %+v", stop)
	}

	next, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		next.Shutdown()
		next.Close()
	}()
	if infos, err := next.List(); err != nil || len(infos) != 0 {
		t.Fatalf("the server after it has %+v, %v", infos, err)
	}
}

// TestLayoutFromJournal checks that the journal of a run whose layout.json is
// gone still gives the server the layout it kept last.
func TestLayoutFromJournal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	l := testLayout(&layout.Node{SID: "gone", Dir: dir})
	j := openJournal()
	j.started()
	j.layout(4, l)
	j.stopped("request", nil)
	j.close()

	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c.Shutdown()
		c.Close()
	}()
	if raw, err := c.Layout(); err != nil || string(raw) != string(l) {
		t.Fatalf("the layout is %s, %v; not %s", raw, err, l)
	}
}

// TestCompactJournal checks that a journal past its limit keeps its last
// entries, which say how the runs ended.
func TestCompactJournal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	j := openJournal()
	last := ""
	for i := range journalKeep + 100 {
		b, _ := json.Marshal(map[string]int{"rev": i})
		last = string(b)
		j.layout(uint64(i), b)
	}
	j.stopped("request", nil)
	j.close()
	compactJournal(journalPath())
	entries := readJournal()
	if len(entries) > journalKeep || len(entries) == 0 {
		t.Fatalf("the journal kept %d entries", len(entries))
	}
	// What the last run left says how it ended, and keeps its last layout.
	if !journalStopped(entries) {
		t.Errorf("the compacted journal does not say how the run ended: %+v", entries)
	}
	if got := lastLayout(entries); string(got) != last {
		t.Errorf("the last layout is %s, not %s", got, last)
	}
	if st, err := os.Stat(journalPath()); err != nil || st.Size() == 0 {
		t.Fatalf("the compacted journal: %v", err)
	}
}

// TestJournalChangedLayoutsOnly checks that a layout sent again is not
// journaled again: a run keeps each layout once.
func TestJournalChangedLayoutsOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	c, _ := connectTest(t)
	l := testLayout(&layout.Node{SID: "one", Dir: dir})
	for range 3 {
		if err := c.SetLayout(l); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, e := range readJournal() {
		if e.Event == eventLayout {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the journal kept the same layout %d times", n)
	}
}

// TestJournalTornLine checks that a line a run cut short does not take the
// entry after it with it.
func TestJournalTornLine(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	torn := `{"at":"2026-10-29T00:00:00Z","event":"layout","layout":{"version":2`
	if err := os.WriteFile(journalPath(), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}
	j := openJournal()
	j.started()
	j.close()
	entries := readJournal()
	if len(entries) != 1 || entries[0].Event != eventStart {
		t.Fatalf("the journal holds %+v", entries)
	}
}

// TestJournalOwners checks that the journal remembers which client created a
// session, the newest record of one last.
func TestJournalOwners(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	j := openJournal()
	j.session("a", "pc-one", "zsh", "/tmp")
	j.session("b", "", "zsh", dir)
	j.session("a", "pc-two", "zsh", "/tmp")
	j.session("c", "pc-one", "zsh", dir)
	j.close()
	owners := journalOwners(readJournal())
	if owners["a"] != "pc-two" || owners["b"] != "" || owners["c"] != "pc-one" || len(owners) != 3 {
		t.Fatalf("the owners are %+v", owners)
	}
}

// TestRestoreMost checks that a run's layout starts no more sessions than
// restoreMost, however many panes it names.
func TestRestoreMost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	l := testLayout(
		&layout.Node{SID: "one", Dir: dir},
		&layout.Node{SID: "two", Dir: dir},
	)
	var store layout.Store
	if err := json.Unmarshal(l, &store); err != nil {
		t.Fatal(err)
	}
	// A third tab, so that two panes are not all the layout names.
	store.Sessions[0].Tabs = append(store.Sessions[0].Tabs, layout.Tab{Root: &layout.Node{SID: "three", Dir: dir}})
	b, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	old := restoreMost
	restoreMost = 2
	t.Cleanup(func() { restoreMost = old })

	// The server runs in this process, so the limit is this process's.
	done := make(chan struct{})
	go func() {
		defer close(done)
		Serve()
	}()
	c, err := dialUntil(time.Now().Add(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(c, dialSocket)
	defer func() {
		client.Shutdown()
		client.Close()
		<-done
	}()
	infos, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("the server started %d sessions, not 2", len(infos))
	}
}

// TestJournalEndsTheRun checks that a run's stop is its last entry: what
// comes after it — a layout, a session — is not kept, so that the run is not
// read as one that did not stop.
func TestJournalEndsTheRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	j := openJournal()
	j.layout(1, testLayout(&layout.Node{SID: "a", Dir: dir}))
	j.stopped("request", nil)
	j.layout(2, testLayout(&layout.Node{SID: "b", Dir: dir}))
	j.session("c", "", "zsh", dir)
	j.close()
	entries := readJournal()
	if len(entries) != 2 || entries[1].Event != eventStop {
		t.Fatalf("the journal holds %+v", entries)
	}
	if !journalStopped(entries) {
		t.Error("the run's stop is not its last entry")
	}
}

// TestJournalResumes checks which runs the server after one starts sessions
// for: one that did not stop, and one that stopped to be replaced; not one
// that quit or exited idle, whose sessions ended with it.
func TestJournalResumes(t *testing.T) {
	if !journalResumes(nil) {
		t.Error("a journal before the journal does not resume")
	}
	if !journalResumes([]journalEntry{{Event: eventStart}, {Event: eventLayout}}) {
		t.Error("a run that did not stop does not resume")
	}
	if !journalResumes([]journalEntry{{Event: eventStop, Reason: stopRestart}}) {
		t.Error("a run that stopped to be replaced does not resume")
	}
	for _, reason := range []string{stopRequest, "idle", "upgrade"} {
		if journalResumes([]journalEntry{{Event: eventStop, Reason: reason}}) {
			t.Errorf("a run that stopped for %q resumes", reason)
		}
	}
}

// TestRestartRestoresSessions checks that a server restarted — stopped so
// that a new one takes its place — starts the sessions of its layout again,
// as one that crashed does, and that one quit ends them.
func TestRestartRestoresSessions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restart bool
		reason  string
	}{
		{name: "restart", restart: true, reason: stopRestart},
		{name: "quit", restart: false, reason: stopRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("GOREX_DIR", dir)
			c, err := Connect()
			if err != nil {
				t.Fatal(err)
			}
			h, err := c.Hello()
			if err != nil {
				t.Fatal(err)
			}
			one, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			if err := c.SetLayout(testLayout(&layout.Node{SID: one.ID, Dir: dir, Cols: 80, Rows: 24})); err != nil {
				t.Fatal(err)
			}
			var next *Client
			if tc.restart {
				next, err = c.Restart(h.PID)
			} else {
				err = c.Shutdown()
				c.Close()
				waitStopped(t)
				if err == nil {
					next, err = Connect()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				next.Shutdown()
				next.Close()
			}()
			entries := readJournal()
			var stop journalEntry
			for _, e := range entries {
				if e.Event == eventStop {
					stop = e
				}
			}
			if stop.Event != eventStop || stop.Reason != tc.reason {
				t.Fatalf("the stop record is %+v", stop)
			}
			infos, err := next.List()
			if err != nil {
				t.Fatal(err)
			}
			back := false
			for _, in := range infos {
				if in.ID == one.ID {
					back = true
				}
			}
			if back != tc.restart {
				t.Fatalf("after %s the sessions are %+v", tc.name, infos)
			}
		})
	}
}

// TestStopToRestartRecordsRestart checks that StopToRestart stops the server
// as one a new server replaces: the journal records a stop for a restart,
// and the server after it starts the sessions of the layout again.
func TestStopToRestartRecordsRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	one, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetLayout(testLayout(&layout.Node{SID: one.ID, Dir: dir, Cols: 80, Rows: 24})); err != nil {
		t.Fatal(err)
	}
	if err := c.StopToRestart(); err != nil {
		t.Fatal(err)
	}
	c.Close()
	waitStopped(t)
	entries := readJournal()
	stop := entries[len(entries)-1]
	if stop.Event != eventStop || stop.Reason != stopRestart {
		t.Fatalf("the stop record is %+v", stop)
	}
	next, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		next.Shutdown()
		next.Close()
	}()
	infos, err := next.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != one.ID || infos[0].Exited {
		t.Fatalf("the server after it has %+v", infos)
	}
}
