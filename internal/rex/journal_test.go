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
	if len(entries) == 0 || entries[0].Event != journalStart {
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
	j.close()
	compactJournal(journalPath())
	entries := readJournal()
	if len(entries) > journalKeep || len(entries) == 0 {
		t.Fatalf("the journal kept %d entries", len(entries))
	}
	if got := lastLayout(entries); string(got) != last {
		t.Errorf("the last layout is %s, not %s", got, last)
	}
	if st, err := os.Stat(journalPath()); err != nil || st.Size() == 0 {
		t.Fatalf("the compacted journal: %v", err)
	}
}
