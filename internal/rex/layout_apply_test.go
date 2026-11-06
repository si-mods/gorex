//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gorex/internal/layout"
)

// applyFixture is a server with no window, its journal open, and a local
// client of it; every session ends with the test.
func applyFixture(t *testing.T) (*Server, *Client) {
	t.Helper()
	s, _ := testServer(t)
	s.journal = openJournal()
	c := pipe(s, "")
	t.Cleanup(func() {
		infos, _ := c.List()
		for _, in := range infos {
			c.Kill(in.ID)
		}
		c.Close()
		s.journal.close()
	})
	return s, c
}

// command makes c through the client, and fails the test unless it was
// made.
func command(t *testing.T, cl *Client, c LayoutCommand) LayoutCommand {
	t.Helper()
	res, err := cl.Command(c)
	if err != nil || res.Code != "" {
		t.Fatalf("%s: %+v %v", c.Op, res, err)
	}
	return res
}

// storeOf reads the layout the server keeps.
func storeOf(t *testing.T, c *Client) layout.Store {
	t.Helper()
	raw, err := c.Layout()
	if err != nil {
		t.Fatal(err)
	}
	return layout.Parse(raw)
}

// liveSession returns the session sid; ok is false once it is gone.
func liveSession(t *testing.T, c *Client, sid string) (SessionInfo, bool) {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == sid {
			return in, true
		}
	}
	return SessionInfo{}, false
}

// TestApplyWithoutApp makes tab.create, pane.split and pane.close with no
// window: the server starts and ends the sessions, in the directories and
// at the sizes the window would, and keeps the layout as the window would
// have saved it, in layout.json and the journal.
func TestApplyWithoutApp(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()

	// A layout of no space gets one, with the tab, of a session at the
	// size of a window's first pane.
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd, Name: "one"})
	st := storeOf(t, c)
	if len(st.Sessions) != 1 || len(st.Sessions[0].Tabs) != 1 || st.Sessions[0].Name == "" || st.Sessions[0].Color == "" {
		t.Fatalf("after the first tab: %+v", st)
	}
	tab := st.Sessions[0].Tabs[0]
	if tab.ID != one.Tab || tab.Name != "one" || tab.Root.SID != one.New || tab.Root.Dir != dir {
		t.Errorf("the first tab %+v, answered %+v", tab, one)
	}
	if in, ok := liveSession(t, c, one.New); !ok || in.Cols != 80 || in.Rows != 24 {
		t.Errorf("the first session %+v", in)
	}
	space := st.Sessions[0].ID

	// The active tab's focused pane, of 100x30, gives a new tab its size,
	// and the tab after it its directory.
	big, err := c.Create(CreateOptions{Command: cmd, Dir: os.TempDir(), Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	st.Sessions[0].Tabs = append(st.Sessions[0].Tabs, layout.Tab{ID: "big", Root: &layout.Node{SID: big.ID}})
	st.Sessions[0].Active = 1
	raw, _ := json.Marshal(st)
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	two := command(t, c, LayoutCommand{Op: "tab.create", Space: space, After: one.New, Command: cmd})
	st = storeOf(t, c)
	w := st.Sessions[0]
	if len(w.Tabs) != 3 || w.Tabs[1].Root.SID != two.New || w.Active != 2 {
		t.Fatalf("a tab after the first: %+v", w)
	}
	in, _ := liveSession(t, c, two.New)
	first, _ := liveSession(t, c, one.New)
	if in.Cols != 100 || in.Rows != 30 || w.Tabs[1].Root.Dir != first.Dir {
		t.Errorf("the second session %+v, in %q", in, w.Tabs[1].Root.Dir)
	}

	// A split down halves the rows; the new pane has the focus.
	split := command(t, c, LayoutCommand{Op: "pane.split", SID: two.New, Vertical: true, Command: cmd, Name: "logs"})
	t2 := storeOf(t, c).Sessions[0].Tabs[1]
	if r := t2.Root; !r.Vertical || r.Ratio != 0.5 || r.A.SID != two.New || r.B.SID != split.New || r.B.Name != "logs" || t2.Focus != 1 {
		t.Errorf("after the split: %+v", t2)
	}
	if in, _ := liveSession(t, c, split.New); in.Cols != 100 || in.Rows != 15 {
		t.Errorf("the split's session %+v", in)
	}

	// Closing it ends its session, and the pane beside takes its place
	// and the focus.
	command(t, c, LayoutCommand{Op: "pane.close", SID: split.New})
	t2 = storeOf(t, c).Sessions[0].Tabs[1]
	if t2.Root.SID != two.New || t2.Focus != 0 {
		t.Errorf("after the close: %+v", t2)
	}
	if _, ok := liveSession(t, c, split.New); ok {
		t.Error("the closed pane's session runs")
	}

	// The last pane of a tab takes the tab; the active one stays.
	command(t, c, LayoutCommand{Op: "pane.close", SID: two.New})
	if w := storeOf(t, c).Sessions[0]; len(w.Tabs) != 2 || w.Tabs[1].ID != "big" || w.Active != 1 {
		t.Errorf("after the tab's last pane: %+v", w)
	}

	// layout.json and the journal keep what the server has.
	state, err := c.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(Dir(), "layout.json")); err != nil || !bytes.Equal(b, state.Layout) {
		t.Errorf("layout.json %s, %v", b, err)
	}
	var last journalEntry
	for _, e := range readJournal() {
		if e.Event == eventLayout {
			last = e
		}
	}
	if last.Rev != state.Revision || !bytes.Equal(last.Layout, state.Layout) {
		t.Errorf("the journal's last layout is of revision %d, want %d", last.Rev, state.Revision)
	}
	// The layout keeps the first window's tabs for a client of one.
	var mirror layout.Layout
	json.Unmarshal(state.Layout, &mirror)
	if len(mirror.Tabs) != 2 || mirror.Tabs[1].ID != "big" {
		t.Errorf("the tabs of a client of one window: %+v", mirror)
	}
}

// TestApplySpaces makes the commands of spaces with no window, and those
// that need the window stay its.
func TestApplySpaces(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	work := command(t, c, LayoutCommand{Op: "space.create", Name: "work"}).Space
	command(t, c, LayoutCommand{Op: "space.rename", Space: work, Name: " ready\x01 "})
	tab := command(t, c, LayoutCommand{Op: "tab.create", Space: work, Dir: dir, Command: cmd})
	st := storeOf(t, c)
	if w := st.Space(work); w == nil || w.Name != "ready" || len(w.Tabs) != 1 {
		t.Fatalf("the space %+v", w)
	}
	command(t, c, LayoutCommand{Op: "tab.rename", Tab: tab.Tab, Name: "named"})
	if st = storeOf(t, c); st.Space(work).Tabs[0].Name != "named" {
		t.Errorf("the tab %+v", st.Space(work).Tabs[0])
	}
	command(t, c, LayoutCommand{Op: "space.close", Space: work})
	if st = storeOf(t, c); st.Space(work) != nil {
		t.Error("the space stays")
	}
	if _, ok := liveSession(t, c, tab.New); ok {
		t.Error("the closed space's session runs")
	}
	for _, x := range []LayoutCommand{
		{Op: "space.rename", Space: "none", Name: "x"},
		{Op: "pane.close", SID: "none"},
		{Op: "tab.close", Tab: "none"},
	} {
		if res, _ := c.Command(x); res.Code == "" {
			t.Errorf("%s of nothing: %+v", x.Op, res)
		}
	}
	for _, x := range []LayoutCommand{
		{Op: "action", Action: "tab.rename"},
		{Op: "config.reload"},
		{Op: "pane.teleported", SID: tab.New},
	} {
		if res, _ := c.Command(x); res.Code != "desktop_unavailable" {
			t.Errorf("%s with no window: %+v", x.Op, res)
		}
	}
}

// TestApplyAfterWindowless makes a command the app answers it has no
// window to make.
func TestApplyAfterWindowless(t *testing.T) {
	s, c := applyFixture(t)
	app := pipe(s, "")
	defer app.Close()
	seen := make(chan bool, 1)
	go func() {
		for {
			cmd, err := app.TakeCommand()
			if err != nil {
				return
			}
			if cmd != nil {
				app.CommandDone(LayoutCommand{ID: cmd.ID, Code: "desktop_unavailable", Error: "no GoRex window is open"})
				// The answer returns once the server made the change: a
				// window the app opens next reads it.
				raw, _ := app.Layout()
				seen <- bytes.Contains(raw, []byte(`"late"`))
			}
		}
	}()
	for {
		s.cmdMu.Lock()
		n := s.desks
		s.cmdMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	res := command(t, c, LayoutCommand{Op: "space.create", Name: "late"})
	if st := storeOf(t, c); st.Space(res.Space) == nil {
		t.Errorf("not made: %+v", res)
	}
	if !<-seen {
		t.Error("the app's answer returned before the server made the change")
	}
}

// seed gives the server a layout of one space, w, of tabs.
func seed(t *testing.T, c *Client, tabs ...layout.Tab) {
	t.Helper()
	st := layout.Store{Version: layout.Version, Sessions: []layout.Workspace{{ID: "w", Name: "work", Layout: layout.Layout{Tabs: tabs}}}, Windows: []string{"w"}}
	raw, _ := json.Marshal(st)
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
}

// TestApplyOnNewerLayout sets the whole layout between an apply's change
// and its commit, as a window's save may: the apply makes its change again
// on the new layout, which keeps both.
func TestApplyOnNewerLayout(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	seed(t, c)
	once := false
	applyHook = func() {
		if once {
			return
		}
		once = true
		seed(t, c, layout.Tab{ID: "saved", Root: &layout.Node{SID: "elsewhere", Host: "h"}})
	}
	defer func() { applyHook = nil }()
	res := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	st := storeOf(t, c)
	// The new tab of this machine goes before the host's.
	if w := st.Space("w"); w == nil || len(w.Tabs) != 2 || w.Tabs[1].ID != "saved" || w.Tabs[0].Root.SID != res.New {
		t.Fatalf("after a save during the apply: %+v", st)
	}
	if infos, _ := c.List(); len(infos) != 1 {
		t.Errorf("sessions %+v, want the one new pane's", infos)
	}
}

// TestLayoutReadWaitsForApply reads the layout while an apply makes its
// change: the read has it, as the app's read before it opens a window.
func TestLayoutReadWaitsForApply(t *testing.T) {
	s, c := applyFixture(t)
	seed(t, c)
	reader := pipe(s, "")
	defer reader.Close()
	release, read := make(chan struct{}), make(chan json.RawMessage, 1)
	applyHook = func() {
		go func() {
			raw, _ := reader.Layout()
			read <- raw
		}()
		<-release
	}
	defer func() { applyHook = nil }()
	done := make(chan LayoutCommand, 1)
	go func() {
		res, _ := c.Command(LayoutCommand{Op: "space.create", Name: "pending"})
		done <- res
	}()
	select {
	case raw := <-read:
		t.Fatalf("read before the apply: %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if raw := <-read; !bytes.Contains(raw, []byte(`"pending"`)) {
		t.Errorf("the read %s has not the change", raw)
	}
	<-done
}

// TestApplyNoChange makes commands that change nothing: the layout, an
// older one too, stays as it was, at its revision.
func TestApplyNoChange(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	in, err := c.Create(CreateOptions{Command: cmd, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	older := []byte(`{"active":0,"tabs":[{"root":{"sid":"` + in.ID + `"},"focus":0}]}`)
	if err := c.SetLayout(older); err != nil {
		t.Fatal(err)
	}
	before, _ := c.LayoutState()
	for _, x := range []LayoutCommand{
		{Op: "pane.resize", SID: in.ID, Direction: "left", Amount: 0.1},
		{Op: "pane.zoom", SID: in.ID, Mode: "off"},
		{Op: "pane.focus", SID: in.ID},
	} {
		if res := command(t, c, x); res.Op != "" {
			t.Errorf("%s: %+v", x.Op, res)
		}
	}
	if after, _ := c.LayoutState(); after.Revision != before.Revision || !bytes.Equal(after.Layout, older) {
		t.Errorf("after commands that change nothing: %d %s, was %d", after.Revision, after.Layout, before.Revision)
	}
}

// TestApplyLeavesRemotePanes closes a tab and a space that have a pane of
// another host with no window: the server, which cannot end that pane's
// session, leaves them to the window rather than drop the pane.
func TestApplyLeavesRemotePanes(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	in, err := c.Create(CreateOptions{Command: cmd, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, c, layout.Tab{ID: "mixed", Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: in.ID}, B: &layout.Node{SID: "far", Host: "h1"}}})
	for _, x := range []LayoutCommand{{Op: "tab.close", Tab: "mixed"}, {Op: "space.close", Space: "w"}} {
		if res, _ := c.Command(x); res.Code != "desktop_unavailable" {
			t.Errorf("%s: %+v", x.Op, res)
		}
	}
	if _, ok := liveSession(t, c, in.ID); !ok || len(storeOf(t, c).Sessions[0].Tabs) != 1 {
		t.Error("the tab or its session went")
	}
	// Its pane of this machine closes; the host's stays, with its tab.
	command(t, c, LayoutCommand{Op: "pane.close", SID: in.ID})
	if tabs := storeOf(t, c).Sessions[0].Tabs; len(tabs) != 1 || tabs[0].Root.SID != "far" {
		t.Errorf("after closing the local pane: %+v", tabs)
	}
}

// TestApplyEndsAfterCommit closes a tab of panes whose programs ignore the
// hangup, which take a second each to end: the layout has the change at
// once, and the sessions end side by side.
func TestApplyEndsAfterCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	s, c := applyFixture(t)
	var sids []string
	for range 3 {
		in, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", "trap '' HUP; while :; do sleep 1; done"}, Dir: "/tmp"})
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, in.ID)
	}
	seed(t, c, layout.Tab{ID: "slow", Root: &layout.Node{A: &layout.Node{SID: sids[0]}, B: &layout.Node{A: &layout.Node{SID: sids[1]}, B: &layout.Node{SID: sids[2]}}}}, layout.Tab{ID: "other", Root: &layout.Node{SID: "x"}})
	reader := pipe(s, "")
	defer reader.Close()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		command(t, c, LayoutCommand{Op: "tab.close", Tab: "slow"})
		close(done)
	}()
	for !bytes.Contains(must(reader.Layout()), []byte(`"other"`)) || bytes.Contains(must(reader.Layout()), []byte(`"slow"`)) {
		time.Sleep(10 * time.Millisecond)
	}
	if d := time.Since(start); d > 700*time.Millisecond {
		t.Errorf("the layout took %v to show the close", d)
	}
	<-done
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Errorf("the sessions took %v to end", d)
	}
	for _, sid := range sids {
		if _, ok := liveSession(t, c, sid); ok {
			t.Errorf("session %s runs", sid)
		}
	}
}

// TestApplyFloats minimizes the focused float of a canvas, whose focus goes
// to the topmost float in sight, and resizes a float at its bound, which
// changes nothing.
func TestApplyFloats(t *testing.T) {
	_, c := applyFixture(t)
	f := func(sid string, x float32) layout.Float {
		return layout.Float{Pane: &layout.Node{SID: sid}, X: x, Y: 0, W: 0.3, H: 1}
	}
	seed(t, c, layout.Tab{ID: "cv", Canvas: &layout.Canvas{W: 1000, H: 600, Scale: 1}, Floats: []layout.Float{f("a", 0), f("b", 0.35), f("c", 0.7)}, FocusFloat: 2})
	args, _ := json.Marshal(map[string]any{"sid": "b", "state": "minimized"})
	command(t, c, LayoutCommand{Op: "action", Action: "float.set_state", Args: args})
	tab := storeOf(t, c).Sessions[0].Tabs[0]
	if !tab.Floats[1].Hidden || tab.FocusFloat != 3 {
		t.Errorf("after minimizing the focused float: %+v", tab)
	}
	// A float of the room's whole height grows no taller.
	seed(t, c, layout.Tab{ID: "t", Root: &layout.Node{SID: "tiled"}, Floats: []layout.Float{{Pane: &layout.Node{SID: "f"}, X: 0, Y: 0, W: 0.5, H: 1}}})
	if res := command(t, c, LayoutCommand{Op: "pane.resize", SID: "f", Direction: "down", Amount: 0.1}); res.Changed || res.Reason != "unchanged" {
		t.Errorf("resize at the bound: %+v", res)
	}
	if res := command(t, c, LayoutCommand{Op: "pane.resize", SID: "f", Direction: "up", Amount: 0.1}); !res.Changed {
		t.Errorf("resize: %+v", res)
	}
}
