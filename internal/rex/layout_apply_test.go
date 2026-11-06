//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

// TestApplyWithApp makes a change of the layout while an app takes
// commands: the server makes it, and only an action reaches the app.
func TestApplyWithApp(t *testing.T) {
	s, c := applyFixture(t)
	app := pipe(s, "")
	defer app.Close()
	took := make(chan string, 4)
	go func() {
		for {
			cmd, err := app.TakeCommand()
			if err != nil {
				return
			}
			if cmd != nil {
				took <- cmd.Op
				app.CommandDone(LayoutCommand{ID: cmd.ID})
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
	command(t, c, LayoutCommand{Op: "action", Action: "tab.rename"})
	if op := <-took; op != "action" {
		t.Errorf("the app took %q, want only the action", op)
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

// TestApplyDropsRemotePanes closes a tab that has a pane of another host:
// the server ends this machine's session and drops the host's pane, whose
// session is the app's to end.
func TestApplyDropsRemotePanes(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	in, err := c.Create(CreateOptions{Command: cmd, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, c, layout.Tab{ID: "mixed", Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: in.ID}, B: &layout.Node{SID: "far", Host: "h1"}}})
	command(t, c, LayoutCommand{Op: "tab.close", Tab: "mixed"})
	if _, ok := liveSession(t, c, in.ID); ok || len(storeOf(t, c).Sessions[0].Tabs) != 0 {
		t.Error("the tab or its session stays")
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

// TestTabSet arranges tabs with tab.set: a tab of the layout's sessions
// changes in place, moves to another space and takes a session from
// another tab; a tab that would start, end or repeat a session is refused
// and changes nothing; another host's panes and panes of no session come
// and go; the server's facts of a pane stay its own.
func TestTabSet(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	two := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	split := command(t, c, LayoutCommand{Op: "pane.split", SID: two.New, Command: cmd})
	space := storeOf(t, c).Sessions[0].ID
	set := func(x LayoutCommand) LayoutCommand {
		t.Helper()
		x.Op = "tab.set"
		res, err := c.Command(x)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	refused := func(why string, x LayoutCommand) {
		t.Helper()
		before := must(c.Layout())
		if res := set(x); res.Code != "conflict" && res.Code != "bad_args" {
			t.Errorf("%s: %+v", why, res)
		}
		if after := must(c.Layout()); !bytes.Equal(before, after) {
			t.Errorf("%s changed the layout", why)
		}
	}

	// The second tab's split, its ratio and its names change; the active
	// tab stays, and the session's facts are the server's.
	tab := layout.Tab{ID: two.Tab, Name: "renamed", Focus: 1, Root: &layout.Node{Ratio: 0.3,
		A: &layout.Node{SID: two.New, Name: "left", Dir: "/nowhere", Cols: 3, Rows: 3, Prog: "fake"},
		B: &layout.Node{SID: split.New}}}
	if res := set(LayoutCommand{Space: space, Index: 1, TabLayout: &tab}); res.Code != "" {
		t.Fatalf("tab.set: %+v", res)
	}
	w := storeOf(t, c).Sessions[0]
	got := w.Tabs[1]
	in, _ := liveSession(t, c, two.New)
	if w.Active != 0 || got.Name != "renamed" || got.Root.Ratio != 0.3 || got.Root.A.Name != "left" || got.Focus != 1 {
		t.Errorf("the tab set: %+v, active %d", got, w.Active)
	}
	if a := got.Root.A; a.Dir != in.Dir || a.Cols != in.Cols || a.Rows != in.Rows || a.Prog != "" {
		t.Errorf("the facts of the pane: %+v, the session's %+v", a, in)
	}

	// Refused: a session left out, a session twice, one the server does
	// not run, and a tab with no pane.
	refused("a session left out", LayoutCommand{Space: space, Index: 1, TabLayout: &layout.Tab{ID: two.Tab, Root: &layout.Node{SID: two.New}}})
	refused("a session twice", LayoutCommand{Space: space, Index: 1, TabLayout: &layout.Tab{ID: two.Tab, Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: two.New}, B: &layout.Node{SID: two.New}}}})
	refused("a new session", LayoutCommand{Space: space, TabLayout: &layout.Tab{ID: "new", Root: &layout.Node{SID: "nosuch"}}})
	refused("no pane", LayoutCommand{Space: space, TabLayout: &layout.Tab{ID: two.Tab}})

	// Another host's pane and a browser come into the tab, as a new tab
	// first in the space; the first tab gives the second its session.
	mixed := layout.Tab{ID: "mixed", Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: "far", Host: "h1"}, B: &layout.Node{URL: "https://example.com", ID: "web"}}}
	if res := set(LayoutCommand{Space: space, TabLayout: &mixed, Focus: true}); res.Code != "" {
		t.Fatalf("a tab of no session of this machine: %+v", res)
	}
	moved := layout.Tab{ID: two.Tab, Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: two.New}, B: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: split.New}, B: &layout.Node{SID: one.New}}}}
	if res := set(LayoutCommand{Space: space, Index: 1, TabLayout: &moved}); res.Code != "" {
		t.Fatalf("taking a session from another tab: %+v", res)
	}
	w = storeOf(t, c).Sessions[0]
	if len(w.Tabs) != 2 || w.Tabs[0].ID != "mixed" || w.Tabs[1].ID != two.Tab || len(layout.Leaves(w.Tabs[1].Root)) != 3 || w.Active != 0 {
		t.Fatalf("after the move: %+v", w)
	}

	// The tab goes to another space; a session that ended may go.
	other := command(t, c, LayoutCommand{Op: "space.create", Space: "mine", Name: "other", Color: "teal"})
	if other.Space != "mine" {
		t.Errorf("space.create of an ID: %+v", other)
	}
	if err := c.Kill(one.New); err != nil {
		t.Fatal(err)
	}
	gone := layout.Tab{ID: two.Tab, Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: two.New}, B: &layout.Node{SID: split.New}}}
	if res := set(LayoutCommand{Space: "mine", TabLayout: &gone}); res.Code != "" {
		t.Fatalf("to another space, without an ended session: %+v", res)
	}
	st := storeOf(t, c)
	if len(st.Sessions[0].Tabs) != 1 || len(st.Space("mine").Tabs) != 1 || st.Space("mine").Color != "teal" {
		t.Errorf("after the move to another space: %+v", st)
	}
	// A split that floats goes over the tab's panes, with the focus.
	fl := command(t, c, LayoutCommand{Op: "pane.split", SID: two.New, Float: true, Command: cmd})
	st = storeOf(t, c)
	if tb := st.Space("mine").Tabs[0]; len(tb.Floats) != 1 || tb.FocusFloat != 1 || tb.Floats[0].Pane.SID != fl.New || tb.Floats[0].X != 0.15 || len(layout.Leaves(tb.Root)) != 2 {
		t.Errorf("a floating split: %+v", tb)
	}
	command(t, c, LayoutCommand{Op: "space.rename", Space: "mine", Color: "default"})
	if st := storeOf(t, c); st.Space("mine").Color != "" || st.Space("mine").Name != "other" {
		t.Errorf("space.rename to the default color: %+v", st.Space("mine"))
	}
}

// TestWindowsSet sets the spaces the windows show, and refuses a space the
// layout does not have.
func TestWindowsSet(t *testing.T) {
	_, c := applyFixture(t)
	seed(t, c)
	command(t, c, LayoutCommand{Op: "space.create", Space: "two"})
	command(t, c, LayoutCommand{Op: "windows.set", Spaces: []string{"two", "w", "two"}})
	if st := storeOf(t, c); !slices.Equal(st.Windows, []string{"two", "w"}) {
		t.Errorf("windows %v", st.Windows)
	}
	if res, _ := c.Command(LayoutCommand{Op: "windows.set", Spaces: []string{"nosuch"}}); res.Code != "space_not_found" {
		t.Errorf("an unknown space: %+v", res)
	}
}

// TestLayoutServerRemote refuses tab.set and windows.set to a remote
// client that may change the layout otherwise: the window's arrangement is
// this machine's.
func TestLayoutServerRemote(t *testing.T) {
	s, c := applyFixture(t)
	seed(t, c, layout.Tab{ID: "far", Root: &layout.Node{SID: "x", Host: "h"}})
	if err := c.ShareSessions(true); err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil || !h.Can(CapabilityLayoutServer) {
		t.Errorf("hello %+v %v", h, err)
	}
	peer := pipe(s, "peer")
	defer peer.Close()
	before := must(c.Layout())
	for _, x := range []LayoutCommand{
		{Op: "tab.set", TabLayout: &layout.Tab{ID: "far", Name: "theirs", Root: &layout.Node{SID: "x", Host: "h"}}},
		{Op: "windows.set", Spaces: []string{"w"}},
	} {
		if _, err := peer.Command(x); err == nil || !strings.Contains(err.Error(), x.Op) {
			t.Errorf("%s remotely: %v", x.Op, err)
		}
	}
	if !bytes.Equal(before, must(c.Layout())) {
		t.Error("a remote client changed the layout")
	}
	// Another command, which sharing allows, is made.
	if res, err := peer.Command(LayoutCommand{Op: "tab.rename", Tab: "far", Name: "named"}); err != nil || res.Code != "" {
		t.Errorf("tab.rename remotely: %+v %v", res, err)
	}
}

// TestTabSetBase changes a tab another client renamed meanwhile: a tab.set
// or pane.close built on the tab as it was is refused, and the rename
// stays; one built on the tab as it is now is made. The answers tell the
// tabs' revisions.
func TestTabSetBase(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	split := command(t, c, LayoutCommand{Op: "pane.split", SID: one.New, Command: cmd})
	if split.Revs[one.Tab] == 0 {
		t.Fatalf("the split's answer names no revision: %+v", split)
	}
	tab := storeOf(t, c).Sessions[0].Tabs[0]
	if tab.Rev != split.Revs[one.Tab] {
		t.Fatalf("the tab at %d, answered %d", tab.Rev, split.Revs[one.Tab])
	}
	base := tab.Rev
	renamed := command(t, c, LayoutCommand{Op: "tab.rename", Tab: one.Tab, Name: "theirs"})
	if renamed.Revs[one.Tab] != base+1 {
		t.Errorf("the rename's revision %v, want %d", renamed.Revs, base+1)
	}
	mine := tab
	mine.Name, mine.Root.Ratio = "mine", 0.3
	for _, x := range []LayoutCommand{
		{Op: "tab.set", TabLayout: &mine, Base: base},
		{Op: "pane.close", SID: split.New, Base: base},
		{Op: "tab.close", Tab: one.Tab, Base: base},
	} {
		if res, _ := c.Command(x); res.Code != "conflict" {
			t.Errorf("%s on the old tab: %+v", x.Op, res)
		}
	}
	if got := storeOf(t, c).Sessions[0].Tabs[0]; got.Name != "theirs" || got.Root.Ratio != 0.5 {
		t.Fatalf("the other client's rename went: %+v", got)
	}
	res := command(t, c, LayoutCommand{Op: "tab.set", TabLayout: &mine, Base: base + 1})
	if got := storeOf(t, c).Sessions[0].Tabs[0]; got.Name != "mine" || got.Rev != base+2 || res.Revs[one.Tab] != base+2 {
		t.Errorf("on the tab as it is: %+v, answered %+v", got, res)
	}
	// A tab.set that changes nothing counts nothing.
	if res := command(t, c, LayoutCommand{Op: "tab.set", TabLayout: &mine, Base: base + 2}); len(res.Revs) != 0 {
		t.Errorf("a tab.set of no change: %+v", res)
	}
}

// TestSplitByTab splits a tab whose focused pane is a browser's, which has
// no session, by the tab: the new pane goes beside it, tiled or floating.
func TestSplitByTab(t *testing.T) {
	_, c := applyFixture(t)
	cmd, dir := testShell()
	one := command(t, c, LayoutCommand{Op: "tab.create", Dir: dir, Command: cmd})
	tab := storeOf(t, c).Sessions[0].Tabs[0]
	tab.Root = &layout.Node{Ratio: 0.5, A: tab.Root, B: &layout.Node{URL: "https://example.com/", ID: "web"}}
	tab.Focus = 1
	command(t, c, LayoutCommand{Op: "tab.set", TabLayout: &tab})
	tiled := command(t, c, LayoutCommand{Op: "pane.split", Tab: one.Tab, Dir: dir, Command: cmd, Vertical: true})
	got := storeOf(t, c).Sessions[0].Tabs[0]
	web := layout.Parent(got.Root, layout.Find(got.Root, tiled.New))
	if web == nil || web.A.URL == "" || !web.Vertical {
		t.Errorf("the split of the browser pane: %s", got.Arrangement())
	}
	fl := command(t, c, LayoutCommand{Op: "pane.split", Tab: one.Tab, Command: cmd, Float: true})
	if got := storeOf(t, c).Sessions[0].Tabs[0]; len(got.Floats) != 1 || got.Floats[0].Pane.SID != fl.New {
		t.Errorf("a float by the tab: %s", got.Arrangement())
	}
}

// TestSetLayoutNeedsCapability refuses a whole layout from a client that
// does not name layout.server, as an app of before it, at any revision.
func TestSetLayoutNeedsCapability(t *testing.T) {
	_, c := applyFixture(t)
	seed(t, c)
	ls, err := c.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range []uint64{0, ls.Revision} {
		err := c.call(Request{Op: "setLayout", Layout: json.RawMessage(`{"version":2,"sessions":[]}`), LayoutRevision: rev}, nil)
		if err == nil || !strings.Contains(err.Error(), "update GoRex") {
			t.Errorf("at revision %d: %v", rev, err)
		}
	}
	if st := storeOf(t, c); len(st.Sessions) != 1 {
		t.Errorf("the layout changed: %+v", st)
	}
}
