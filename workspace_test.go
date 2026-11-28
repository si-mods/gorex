package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// TestParseLayout checks that the layout of a GoRex of one window becomes
// a workspace, which its window shows, and that a layout of workspaces
// drops what it cannot use.
func TestParseLayout(t *testing.T) {
	one := `{"active":1,"tabs":[{"name":"a","root":{"sid":"s1","dir":"/x"},"focus":0},{"root":{"sid":"s2"},"focus":0}]}`
	s := layout.Parse([]byte(one))
	if s.Version != layout.Version || len(s.Sessions) != 1 || !slices.Equal(s.Windows, []string{s.Sessions[0].ID}) {
		t.Fatalf("migrated to %+v", s)
	}
	w := s.Sessions[0]
	if w.Active != 1 || len(w.Tabs) != 2 || w.Tabs[0].Name != "a" || w.Tabs[0].Root.SID != "s1" || w.Tabs[1].Root.SID != "s2" {
		t.Errorf("migrated workspace %+v", w)
	}
	if !regexp.MustCompile(`^[a-z]+-[a-z]+$`).MatchString(w.Name) {
		t.Errorf("migrated workspace named %q", w.Name)
	}
	for _, raw := range []string{"", "null", "not json", `{"active":0,"tabs":[]}`, `{"version":2}`} {
		if s := layout.Parse([]byte(raw)); len(s.Sessions) != 0 || len(s.Windows) != 0 {
			t.Errorf("%q: %+v", raw, s)
		}
	}
	two := `{"version":2,"sessions":[{"id":"a","name":"icy-ridge","tabs":[]},{"id":"a","name":"again"},{"id":"","name":"none"},{"id":"b","name":" "}],"windows":["b","zz","a","b"]}`
	s = layout.Parse([]byte(two))
	if len(s.Sessions) != 2 || s.Sessions[0].Name != "icy-ridge" || s.Sessions[1].ID != "b" || s.Sessions[1].Name == " " {
		t.Errorf("sessions %+v", s.Sessions)
	}
	if !slices.Equal(s.Windows, []string{"b", "a"}) {
		t.Errorf("windows %q", s.Windows)
	}
}

// TestSpaceColorsAssigned checks that new spaces take the palette's colors
// in order, and that the tenth, every one taken, takes a spare.
func TestSpaceColorsAssigned(t *testing.T) {
	st := &store{}
	for i, p := range layout.Palette {
		if w := st.newTestSpace(); w.Color != p.ID {
			t.Fatalf("space %d is of color %q, want %q", i+1, w.Color, p.ID)
		}
	}
	if w := st.newTestSpace(); w.Color != layout.Palette[0].ID {
		t.Errorf("the tenth space is of color %q, want %q", w.Color, layout.Palette[0].ID)
	}
	// The least used, in the palette's order, once two are used twice.
	if w := st.newTestSpace(); w.Color != layout.Palette[1].ID {
		t.Errorf("the eleventh space is of color %q, want %q", w.Color, layout.Palette[1].ID)
	}
	// A custom color takes no preset.
	st.workspaces[0].Color = "#123456"
	if w := st.newTestSpace(); w.Color != layout.Palette[0].ID {
		t.Errorf("with a custom color, rose is of color %q", w.Color)
	}
}

// TestSpaceColorHex checks which colors spaceColorHex reads.
func TestSpaceColorHex(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"rose", "#e54666", true},
		{"pink", "#d6409f", true},
		{"#ABCDEF", "#abcdef", true},
		{"#0090ff", "#0090ff", true},
		{"#abc", "", false},
		{"#1234567", "", false},
		{"#gggggg", "", false},
		{"123456", "", false},
		{"red", "", false},
		{"", "", false},
	} {
		got, ok := spaceColorHex(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("spaceColorHex(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// sidsOf are the sessions of a window's panes, in order.
func sidsOf(a *App) []string {
	var out []string
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			out = append(out, p.SID)
		}
	}
	return out
}

// quit detaches every window, and closes the panes they keep of other
// spaces, as closing the windows does (the quitting app skips both).
func quit(st *store) {
	st.quitting = true
	for _, a := range st.apps {
		a.detach()
		st.unkeepAll(a)
	}
}

// appAt returns the window in a place.
func appAt(t *testing.T, st *store, slot int) *App {
	t.Helper()
	for _, a := range st.apps {
		if a.slot == slot {
			return a
		}
	}
	t.Fatalf("no window in place %d", slot)
	return nil
}

// TestWorkspacesSaved checks that the layout keeps every workspace, shown
// or not, and the windows, and that the next launch opens those windows
// on the same sessions.
func TestWorkspacesSaved(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	first := a.ws
	a.split(false)
	a.tab().Name = "one"
	firstSIDs := sidsOf(a)
	// A second workspace, which no window shows once the first is back,
	// and a third in a window of its own.
	a.showWorkspace(a.st.newTestSpace())
	second := a.ws
	a.newTab(testShell.dir)
	secondSIDs := sidsOf(a)
	b := st.openWindow(st.newTestSpace())
	third := b.ws
	a.showWorkspace(first)
	tt.Frame()
	if !slices.Equal(sidsOf(a), firstSIDs) {
		t.Fatalf("the first workspace came back on %q, not %q", sidsOf(a), firstSIDs)
	}
	st.saveNow()

	s := savedWorkspaces(t, a.client)
	if len(s.Sessions) != 3 || s.Sessions[1].Name != second.Name || len(s.Sessions[1].Tabs) != 2 {
		t.Fatalf("saved %+v", s.Sessions)
	}
	if !slices.Equal(s.Windows, []string{first.ID, third.ID}) {
		t.Errorf("saved windows %q, want %q", s.Windows, []string{first.ID, third.ID})
	}
	// A GoRex from before workspaces finds the first window's tabs.
	raw, _ := a.client.Layout()
	var older savedLayout
	if json.Unmarshal(raw, &older); len(older.Tabs) != 1 || older.Tabs[0].Name != "one" {
		t.Errorf("an older GoRex finds %+v", older)
	}

	quit(st)
	next := &store{}
	next.start()
	if len(next.apps) != 2 || len(next.workspaces) != 3 {
		t.Fatalf("the next launch opened %d windows of %d workspaces", len(next.apps), len(next.workspaces))
	}
	n0, n1 := appAt(t, next, 0), appAt(t, next, 1)
	if n0.ws.ID != first.ID || n1.ws.ID != third.ID || n0.ws.Name != first.Name {
		t.Fatalf("the windows show %s and %s", n0.ws.ID, n1.ws.ID)
	}
	if n0.ws.Color != first.Color || n1.ws.Color != third.Color {
		t.Errorf("the spaces came back of colors %q and %q", n0.ws.Color, n1.ws.Color)
	}
	if !slices.Equal(sidsOf(n0), firstSIDs) || n0.tabs[0].Name != "one" {
		t.Errorf("the first window attached to %q, not %q", sidsOf(n0), firstSIDs)
	}
	// The workspace no window showed is kept, and shows on its sessions.
	n0.showWorkspace(next.byID(second.ID))
	if !slices.Equal(sidsOf(n0), secondSIDs) {
		t.Errorf("the second workspace came back on %q, not %q", sidsOf(n0), secondSIDs)
	}
	quit(next)
}

// TestShowWorkspaceInOtherWindow checks that showing a workspace another
// window shows focuses that window, and that Next Space passes over it.
func TestShowWorkspaceInOtherWindow(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	b := st.openWindow(st.newTestSpace())
	st.raise(a)
	mine := a.ws
	sids := sidsOf(a)
	a.showWorkspace(b.ws)
	if a.ws != mine || st.apps[0] != b || b.ws.app != b {
		t.Fatalf("a shows %v, the front window is %p (b %p)", a.ws.Name, st.apps[0], b)
	}
	hidden := st.newTestSpace()
	a.cycleWorkspace(1)
	if a.ws != hidden || mine.app != nil || len(mine.layout.Tabs) != 1 {
		t.Fatalf("Next Space shows %s", a.ws.Name)
	}
	a.cycleWorkspace(1)
	if a.ws != mine || !slices.Equal(sidsOf(a), sids) {
		t.Errorf("Next Space came back to %s on %q, not %q", a.ws.Name, sidsOf(a), sids)
	}
	a.cycleWorkspace(-1)
	if a.ws != hidden {
		t.Errorf("Previous Space shows %s", a.ws.Name)
	}
	quit(st)
}

// liveSIDs are the sessions the server runs.
func liveSIDs(t *testing.T, c *rex.Client) []string {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, in := range infos {
		if !in.Exited {
			out = append(out, in.ID)
		}
	}
	return out
}

// TestCloseWorkspace checks that Close Space asks, ends the workspace's
// sessions, and shows another workspace, or a new one when none is left.
func TestCloseWorkspace(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	a.split(true)
	first, sids := a.ws, sidsOf(a)
	other := st.newTestSpace()
	a.askCloseWorkspace()
	tt.Frame()
	if !tt.HasText(locales.SessionCloseTitle(first.Name)) || !tt.HasText(locales.SessionCloseBody(2)) {
		t.Fatalf("no question; texts %q", tt.Texts())
	}
	if err := tt.Click(locales.SessionCancel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.wsConfirm || a.ws != first {
		t.Fatal("Cancel closed the workspace")
	}
	a.askCloseWorkspace()
	tt.Frame()
	if err := tt.Click(cmdCloseSession.Title()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if a.ws != other || slices.Contains(st.workspaces, first) || len(a.tabs) != 1 {
		t.Fatalf("after Close Space the window shows %v, of %d tabs", a.ws, len(a.tabs))
	}
	waitFor(t, tt, "the sessions to end", func() bool {
		live := liveSIDs(t, a.client)
		return !slices.Contains(live, sids[0]) && !slices.Contains(live, sids[1])
	})
	// Without another workspace, the window gets a new one.
	a.closeWorkspace()
	if len(st.workspaces) != 1 || a.ws == other || a.ws == nil || len(a.tabs) != 1 {
		t.Errorf("closing the last workspace left %d, the window showing %v", len(st.workspaces), a.ws)
	}
	quit(st)
}

// TestLastTabClosesWorkspace checks that closing a workspace's last tab
// keeps it, as its host keeps a project of no tab (docs/MOD-002-projects.md), and
// shows a workspace no window shows.
func TestLastTabClosesWorkspace(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	first := a.ws
	a.showWorkspace(a.st.newTestSpace())
	second := a.ws
	a.closeTab(a.tab())
	if a.ws != first || !slices.Contains(st.workspaces, second) || len(a.tabs) != 1 {
		t.Errorf("after the last tab the window shows %v", a.ws)
	}
	quit(st)
}

// TestMoveTabToSpace checks that a tab moves to another space, its
// session going on: into the layout of one no window shows, into the
// window that shows one, and that the last tab's move leaves the space,
// which stays (docs/MOD-002-projects.md).
func TestMoveTabToSpace(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	first := a.ws
	hidden := st.newTestSpace()
	a.newTab(testShell.dir)
	moved := a.tab()
	sid := moved.Focus.SID
	a.moveTabToSpace(moved, hidden)
	if len(a.tabs) != 1 || len(hidden.layout.Tabs) != 1 || hidden.layout.Tabs[0].Root.SID != sid {
		t.Fatalf("%d tabs left, the hidden space has %+v", len(a.tabs), hidden.layout.Tabs)
	}
	if !slices.Contains(liveSIDs(t, a.client), sid) {
		t.Error("the moved tab's session ended")
	}
	b := st.openWindow(hidden)
	if len(b.tabs) != 1 || b.tab().Focus.SID != sid {
		t.Fatalf("the space's window has %q", sidsOf(b))
	}
	lastTab := a.tab()
	last, uid := lastTab.Focus.SID, lastTab.UID
	a.moveTabToSpace(lastTab, hidden)
	if len(b.tabs) != 2 || b.tabs[1].Focus.SID != last || b.tabs[1].UID != uid {
		t.Fatalf("the shown space has %q, want %s then %s, of tab ID %s", sidsOf(b), sid, last, uid)
	}
	if !slices.Contains(st.workspaces, first) || !slices.Contains(liveSIDs(t, a.client), last) {
		t.Errorf("after its last tab moved, the space stays or the session ended")
	}
	quit(st)
}

// TestWindowsCloseAndReopen checks that a window that closes keeps its
// workspace, that the last one's is saved as the window to open, and that
// a second launch opens it then, or else shows the window focused last.
func TestWindowsCloseAndReopen(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	st.started = true
	b := st.openWindow(st.newTestSpace())
	bws := b.ws
	st.closed(b)
	if len(st.apps) != 1 || bws.app != nil || !slices.Contains(st.workspaces, bws) {
		t.Fatalf("closing a window: %d windows, its workspace shown by %p", len(st.apps), bws.app)
	}
	c := st.openWindow(st.newTestSpace())
	st.raise(a)
	st.secondInstance()
	if st.apps[0] != a || len(st.apps) != 2 {
		t.Errorf("a second launch fronted %p, not %p", st.apps[0], a)
	}
	st.closed(c)
	w, sids := a.ws, sidsOf(a)
	st.closed(a)
	if len(st.apps) != 0 || st.lastClosed != w {
		t.Fatalf("%d windows after closing the last", len(st.apps))
	}
	if s := savedWorkspaces(t, a.client); !slices.Equal(s.Windows, []string{w.ID}) || len(s.Sessions) != 3 {
		t.Errorf("saved windows %q of %d workspaces", s.Windows, len(s.Sessions))
	}
	st.secondInstance()
	if len(st.apps) != 1 || st.apps[0].ws.ID != w.ID || !slices.Equal(sidsOf(st.apps[0]), sids) {
		t.Errorf("a second launch opened %d windows", len(st.apps))
	}
	quit(st)
}

// TestOutboxOrder checks that the commands reach the server in the order
// sent, the last last.
func TestOutboxOrder(t *testing.T) {
	a, _ := newTestApp(t)
	var o outbox
	o.use(a.client, func(fn func()) { go fn() }, nil)
	id := a.tab().UID
	for i := range 50 {
		o.add(rex.LayoutCommand{Op: "tab.rename", Tab: id, Name: fmt.Sprint("n", i)}, nil)
	}
	o.kick()
	o.wait(5 * time.Second)
	if name := savedWorkspaces(t, a.client).Sessions[0].Tabs[0].Name; name != "n49" {
		t.Errorf("the server's tab is %q", name)
	}
}

// TestSpaceColorShots renders a window whose space is teal, light and
// dark, into GOREX_SHOTS, to look at.
func TestSpaceColorShots(t *testing.T) {
	dir := os.Getenv("GOREX_SHOTS")
	if dir == "" {
		t.Skip("GOREX_SHOTS names no directory")
	}
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	a.ws.Color = "teal"
	os.MkdirAll(dir, 0o755)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		a.pickerOpen = true
		for range 3 {
			tt.Frame()
		}
		name := fmt.Sprintf("space-teal-%s.png", map[bool]string{false: "light", true: "dark"}[dark])
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, tt.Image())
		f.Close()
	}
	a.pickerOpen = false
	quit(a.st)
}

// TestSpaceStripShots renders the sidebar's strip of four colored spaces,
// light and dark, into GOREX_SHOTS, to look at.
func TestSpaceStripShots(t *testing.T) {
	dir := os.Getenv("GOREX_SHOTS")
	if dir == "" {
		t.Skip("GOREX_SHOTS names no directory")
	}
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	st := a.st
	a.ws.Color = "teal"
	for _, c := range []string{"blue", "violet", ""} {
		st.newTestSpace().Color = c
	}
	os.MkdirAll(dir, 0o755)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		for range 3 {
			tt.Frame()
		}
		name := fmt.Sprintf("strip-%s.png", map[bool]string{false: "light", true: "dark"}[dark])
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, tt.Image())
		f.Close()
	}
	quit(st)
}

// TestRenameFieldShots renders the fields that rename a space and a tab,
// in the sidebar and above the tabs, light and dark, cropped and
// magnified into GOREX_SHOTS, to look at: their text must show whole,
// descenders too, inside the field.
func TestRenameFieldShots(t *testing.T) {
	dir := os.Getenv("GOREX_SHOTS")
	if dir == "" {
		t.Skip("GOREX_SHOTS names no directory")
	}
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	st := a.st
	a.ws.Name = "Vinda gyp"
	os.MkdirAll(dir, 0o755)
	shot := func(name string, r image.Rectangle, scale int) {
		t.Helper()
		src := tt.Image()
		dst := image.NewRGBA(image.Rect(0, 0, r.Dx()*scale, r.Dy()*scale))
		for y := range r.Dy() * scale {
			for x := range r.Dx() * scale {
				dst.Set(x, y, src.At(r.Min.X+x/scale, r.Min.Y+y/scale))
			}
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, dst)
		f.Close()
	}
	for _, place := range []string{"left", ""} {
		for _, dark := range []bool{false, true} {
			prefs = settings{TabPlacement: place}
			tt.SetDark(dark)
			theme := map[bool]string{false: "light", true: "dark"}[dark]
			space := map[string]string{"left": "sidebar", "": "top"}[place]
			chip, tab := image.Rect(0, 40, 240, 78), image.Rect(0, 78, 240, 114)
			if place == "" {
				chip, tab = image.Rect(185, 0, 350, 44), image.Rect(340, 0, 700, 44)
			}
			a.wsRenaming, a.wsRenameText, a.wsRenameFocus = true, "Vinda gyp", false
			for range 3 {
				tt.Frame()
			}
			shot(fmt.Sprintf("rename-space-%s-%s.png", space, theme), chip, 6)
			a.wsRenaming = false
			a.renaming, a.renameText, a.renameFocus = a.tab(), "Vinda gyp", false
			for range 3 {
				tt.Frame()
			}
			shot(fmt.Sprintf("rename-tab-%s-%s.png", space, theme), tab, 6)
			a.renaming = nil
			find, pane := image.Rect(250, 42, 700, 68), image.Rect(250, 8, 540, 40)
			if place == "" {
				find, pane = image.Rect(14, 78, 470, 104), image.Rect(14, 46, 310, 76)
			}
			a.openFind()
			for range 3 {
				tt.Frame()
			}
			shot(fmt.Sprintf("rename-find-%s-%s.png", space, theme), find, 4)
			a.closeFind()
			a.startPaneRename(a.tab().Focus)
			for range 3 {
				tt.Frame()
			}
			shot(fmt.Sprintf("rename-pane-%s-%s.png", space, theme), pane, 4)
			a.paneRenaming, a.paneNameFocus = nil, false
			for range 2 {
				tt.Frame()
			}
		}
	}
	quit(st)
}

// TestSessionShots renders the session popover, above the tabs and in the
// sidebar, light and dark, into GOREX_SHOTS, to look at.
func TestSessionShots(t *testing.T) {
	dir := os.Getenv("GOREX_SHOTS")
	if dir == "" {
		t.Skip("GOREX_SHOTS names no directory")
	}
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	st := a.st
	a.ws.Name = "icy-ridge"
	a.newTab(testShell.dir)
	st.newTestSpace().Name = "quiet-harbor"
	b := st.openWindow(st.newTestSpace())
	b.ws.Name = "misty-canyon"
	os.MkdirAll(dir, 0o755)
	for _, place := range []string{"", "left"} {
		for _, dark := range []bool{false, true} {
			prefs = settings{TabPlacement: place}
			tt.SetDark(dark)
			a.pickerOpen = true
			for range 3 {
				tt.Frame()
			}
			name := fmt.Sprintf("sessions-%s-%s.png", map[string]string{"": "top", "left": "sidebar"}[place], map[bool]string{false: "light", true: "dark"}[dark])
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, tt.Image())
			f.Close()
		}
	}
	a.pickerOpen = false
	a.askCloseWorkspace()
	tt.Frame()
	f, _ := os.Create(filepath.Join(dir, "sessions-close-dark.png"))
	png.Encode(f, tt.Image())
	f.Close()
	a.wsConfirm = false
	quit(st)
}

// TestDebugVerbs checks the debug hook's verbs of windows and workspaces:
// "state" describes them, "win" targets a window by place, "focus-win"
// fronts one, and "session", "rename-session", "cancel" and "confirm" act
// on the target.
func TestDebugVerbs(t *testing.T) {
	a, _ := newTestApp(t)
	old := debugMain
	debugMain = func(fn func()) { fn() }
	t.Cleanup(func() { debugMain = old })
	st := a.st
	b := st.openWindow(st.newTestSpace())
	a.ws.Name, b.ws.Name = "icy-ridge", "quiet-harbor"
	hidden := st.newTestSpace()
	hidden.Name = "misty-canyon"
	dir := t.TempDir()
	state := func() debugState {
		t.Helper()
		st.debug(dir, "state s.json")
		raw, err := os.ReadFile(filepath.Join(dir, "s.json"))
		if err != nil {
			t.Fatal(err)
		}
		var s debugState
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := state()
	if s.PID != os.Getpid() || len(s.Windows) != 2 || len(s.Sessions) != 3 {
		t.Fatalf("state %+v", s)
	}
	w1, w2 := s.Windows[0], s.Windows[1]
	if w1.Index != 1 || w1.StateKey != "main" || w1.Session.Name != "icy-ridge" || w1.Focused || len(w1.Tabs) != 1 || w1.ActiveTab != 1 {
		t.Errorf("window 1 %+v", w1)
	}
	if w2.Index != 2 || w2.StateKey != "main-2" || w2.Session.ID != b.ws.ID || !w2.Focused {
		t.Errorf("window 2 %+v", w2)
	}
	if s.Sessions[1].Window != 2 || s.Sessions[2].Name != "misty-canyon" || s.Sessions[2].Window != 0 || s.Sessions[2].Tabs != 0 {
		t.Errorf("sessions %+v", s.Sessions)
	}

	st.debug(dir, "focus-win 1")
	if st.apps[0] != a || !state().Windows[0].Focused {
		t.Error("focus-win 1 did not front the first window")
	}
	st.debug(dir, "win 2")
	st.debug(dir, "rename-session my-work")
	st.debug(dir, "session misty-canyon")
	if b.ws != hidden || st.byID(s.Sessions[1].ID).Name != "my-work" {
		t.Errorf("window 2 shows %s; renamed %q", b.ws.Name, st.byID(s.Sessions[1].ID).Name)
	}
	st.debug(dir, "win 9")
	if st.debugWin != 2 {
		t.Errorf("win 9 targets %d", st.debugWin)
	}
	st.debug(dir, "win 0") // the window focused last: the first
	first := a.ws
	a.askCloseWorkspace()
	st.debug(dir, "cancel")
	if a.wsConfirm || a.ws != first {
		t.Error("cancel closed the workspace")
	}
	a.askCloseWorkspace()
	st.debug(dir, "confirm")
	if a.ws == first || slices.Contains(st.workspaces, first) {
		t.Error("confirm kept the workspace")
	}
	quit(st)
}

// TestHostsSavedThroughStore checks that a host that saves itself after
// the window that started it closed writes the app's hosts, not that
// window's: a host added since in another window stays saved.
func TestHostsSavedThroughStore(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	h1 := a.addHost(savedHost{ID: "h1", Name: "one", Address: "x1"})
	t.Cleanup(h1.close)
	b := st.openWindow(st.newTestSpace())
	st.closed(a)
	h2 := b.addHost(savedHost{ID: "h2", Name: "two", Address: "x2"})
	t.Cleanup(h2.close)
	h1.kill("gone") // saves the hosts, h1's kill with them
	hs, err := loadHosts()
	if err != nil || len(hs) != 2 || hs[1].ID != "h2" || !slices.Contains(hs[0].Kills, "gone") {
		t.Fatalf("hosts.json has %+v, %v", hs, err)
	}
	quit(st)
}

// TestStartOnce checks that a second launch or an activation that comes
// before the app is ready opens no window of its own, and that starting
// twice opens the windows once.
func TestStartOnce(t *testing.T) {
	a, _ := newTestApp(t)
	a.st.saveNow()
	quit(a.st)
	next := &store{}
	next.secondInstance() // before WhenReady: the start serves it
	next.reopen()
	next.openNew()
	if len(next.apps) != 0 {
		t.Fatalf("%d windows before the start", len(next.apps))
	}
	next.start()
	next.start()
	if len(next.apps) != 1 || len(next.workspaces) != 1 {
		t.Errorf("%d windows of %d workspaces", len(next.apps), len(next.workspaces))
	}
	quit(next)
}

// TestReconnectUpdatesWindows checks that a window opened after the
// server's connection was lost connects again, and that the windows open
// then use the new connection, on the same sessions.
func TestReconnectUpdatesWindows(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	st.newTestSpace()
	st.saveNow()
	sids, old := sidsOf(a), st.client
	old.Close()
	<-old.Closed() // its reader ends after Close returns
	st.openNew()
	t.Cleanup(func() { st.client.Shutdown() })
	if st.client == old || len(st.apps) != 2 {
		t.Fatal("no new connection")
	}
	if a.client != st.client {
		t.Fatal("the window open before keeps the closed connection")
	}
	if !slices.Equal(sidsOf(a), sids) {
		t.Errorf("the window came back on %q, not %q", sidsOf(a), sids)
	}
	quit(st)
}

func TestWorkspaceStatus(t *testing.T) {
	a, _ := newTestApp(t)
	p := a.tab().Focus
	p.info.Statuses = []rex.ProgramStatus{{ID: "a", State: "blocked"}}
	if s := a.st.status(a.ws); s == nil || s.State != "blocked" {
		t.Fatalf("window status %+v", s)
	}
	// Seen done work no longer asks for the workspace.
	p.info.Statuses = []rex.ProgramStatus{{ID: "a", State: "done", Updated: time.Now()}}
	p.seen = time.Now().Add(time.Second)
	p.see()
	if s := a.st.status(a.ws); s != nil && s.State == "done" {
		t.Fatalf("seen status %+v", s)
	}
	// A hidden workspace's sessions come from the last list, with the
	// seen times the window recorded, for a server that keeps none, and
	// those the server keeps.
	w := a.st.newTestSpace()
	w.layout = savedLayout{Tabs: []savedTab{{Root: &savedNode{SID: p.SID}}}}
	a.st.infos = map[string]rex.SessionInfo{p.SID: p.info}
	if s := a.st.status(w); s != nil && s.State == "done" {
		t.Fatalf("hidden status seen by the window %+v", s)
	}
	a.st.seen = nil
	if s := a.st.status(w); s == nil || s.State != "done" {
		t.Fatalf("hidden status unseen %+v", s)
	}
	seenInfo := p.info
	seenInfo.StatusSeen = p.info.Statuses[0].Updated
	a.st.infos = map[string]rex.SessionInfo{p.SID: seenInfo}
	if s := a.st.status(w); s != nil && s.State == "done" {
		t.Fatalf("hidden seen status %+v", s)
	}
	a.st.infos[p.SID] = rex.SessionInfo{ID: p.SID, Statuses: []rex.ProgramStatus{{ID: "a", State: "error", Updated: time.Now().Add(time.Second)}}}
	if s := a.st.status(w); s == nil || s.State != "error" {
		t.Fatalf("hidden status %+v", s)
	}
	// Another session's unseen done record still asks, though this pane's
	// was seen later.
	w.layout = savedLayout{Tabs: []savedTab{{Root: &savedNode{SID: p.SID}}, {Root: &savedNode{SID: "other"}}}}
	a.st.infos[p.SID] = rex.SessionInfo{ID: p.SID, Statuses: []rex.ProgramStatus{{ID: "a", State: "done", Updated: time.Now().Add(time.Second)}}}
	a.st.infos["other"] = rex.SessionInfo{ID: "other", Statuses: []rex.ProgramStatus{{ID: "b", State: "done", Updated: time.Now().Add(2 * time.Second)}}}
	if s := a.st.status(w); s == nil || s.ID != "b" {
		t.Fatalf("other hidden status %+v", s)
	}
}

func TestStatusEvents(t *testing.T) {
	p := &Pane{SID: "s1", info: rex.SessionInfo{Program: "claude", Dir: "/tmp"}}
	ev := statusEvents(p, nil, []rex.ProgramStatus{{ID: "a", State: "blocked", Kind: "question", Title: "T", Progress: 40}})
	if len(ev) != 1 || ev[0]["state"] != "blocked" || ev[0]["session"] != "s1" || ev[0]["progress"] != "40" {
		t.Fatalf("events %+v", ev)
	}
	ev = statusEvents(p, []rex.ProgramStatus{{ID: "a", State: "blocked"}}, nil)
	if len(ev) != 1 || ev[0]["state"] != "clear" {
		t.Fatalf("clear %+v", ev)
	}
	if ev := statusEvents(p, nil, nil); ev != nil {
		t.Fatalf("no change %+v", ev)
	}
}

// TestReopenShowsSpace closes the last window and has a client make a tab
// with the focus in a space: the window opened again shows that space,
// and the server forgets the request. One asked longer ago than showFor
// is dropped: the window shows the space it showed.
func TestReopenShowsSpace(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	st.started = true
	other, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, stale := range []bool{false, true} {
		last := st.apps[0].ws
		st.closed(st.apps[0])
		made, err := other.Command(rex.LayoutCommand{Op: "space.create"})
		if err != nil {
			t.Fatal(err)
		}
		space := made.Space
		if res, err := other.Command(rex.LayoutCommand{Op: "tab.create", Space: space, Dir: os.TempDir(), Focus: true}); err != nil || res.Code != "" {
			t.Fatalf("tab.create: %+v %v", res, err)
		}
		saved := savedWorkspaces(t, other)
		if saved.Show != space {
			t.Fatalf("the server asks to show %q, not %s", saved.Show, space)
		}
		if stale {
			saved.ShowAt = time.Now().Add(-2 * showFor)
			raw, _ := json.Marshal(saved)
			if err := other.SetLayout(raw); err != nil {
				t.Fatal(err)
			}
		}
		st.reopen()
		want := space
		if stale {
			want = last.ID
		}
		if len(st.apps) != 1 || st.apps[0].ws.ID != want {
			t.Fatalf("stale %v: the window opened again shows %s, not %s", stale, st.apps[0].ws.ID, want)
		}
		st.saveNow()
		if s := savedWorkspaces(t, other); s.Show != "" {
			t.Fatalf("stale %v: the server still asks to show %s", stale, s.Show)
		}
	}
}

// TestReopenReadsServerChanges closes the last window, has the server make
// a tab with no window, and opens a window again: it shows the tab, and
// saves no layout of before over it. A layout it cannot read leaves the
// workspaces as they were.
func TestReopenReadsServerChanges(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	st.started = true
	w := a.ws
	st.closed(a)
	other, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	res, err := other.Command(rex.LayoutCommand{Op: "tab.create", Space: w.ID, Dir: os.TempDir()})
	if err != nil || res.Code != "" {
		t.Fatalf("tab.create with no window: %+v %v", res, err)
	}
	st.reopen()
	if len(st.apps) != 1 || st.apps[0].ws.ID != w.ID || !slices.Contains(sidsOf(st.apps[0]), res.New) {
		t.Fatalf("the window opened again shows %v", sidsOf(st.apps[0]))
	}
	st.saveNow()
	if s := savedWorkspaces(t, a.client); len(s.Sessions) != 1 || len(s.Sessions[0].Tabs) != 2 {
		t.Errorf("saved %+v", s.Sessions)
	}

	before := st.workspaces
	closed, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	live := st.client
	st.client = closed
	if err := st.reload(); err == nil || !slices.Equal(st.workspaces, before) {
		t.Errorf("a reload that failed: %v, workspaces %v, were %v", err, st.workspaces, before)
	}
	st.client = live
	quit(st)
}

// countLists connects to the test's server through a socket of its own,
// which counts the lists asked through it.
func countLists(t *testing.T) (*rex.Client, *atomic.Int32) {
	t.Helper()
	dir := testDir(t)
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var lists atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s, err := net.Dial("unix", rex.SocketPath())
			if err != nil {
				c.Close()
				continue
			}
			go func() { io.Copy(c, s); c.Close() }()
			go func() {
				r := bufio.NewReaderSize(c, 1<<20)
				for {
					line, err := r.ReadBytes('\n')
					if bytes.Contains(line, []byte(`"op":"list"`)) {
						lists.Add(1)
					}
					if _, werr := s.Write(line); err != nil || werr != nil {
						s.Close()
						return
					}
				}
			}()
		}
	}()
	c, err := rex.ConnectSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, &lists
}

// TestPollServesEveryWindow checks that one poll of this machine's server
// serves every window: a round asks for the list once, and both windows
// take it. A round of the same list draws no window again, one of a new
// list draws each. With one window closed the other is still served, and
// with the last closed the poll stops.
func TestPollServesEveryWindow(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	b := st.openWindow(st.newTestSpace())
	client, lists := countLists(t)
	stop := make(chan struct{})
	st.polling = stop
	rounds, done := make(chan func()), make(chan struct{})
	go func() {
		defer close(done)
		st.poll(client, stop, func(fn func()) {
			select {
			case rounds <- fn:
			case <-stop:
			}
		})
	}()
	posted := func(a *App) int {
		a.postMu.Lock()
		defer a.postMu.Unlock()
		return len(a.posted)
	}
	// round runs the next round, and tells whether it drew the windows.
	n := 0
	round := func() (drew bool) {
		t.Helper()
		select {
		case fn := <-rounds:
			n++
			was := posted(a)
			fn()
			if got := lists.Load(); got != int32(n) {
				t.Fatalf("%d lists asked in %d rounds", got, n)
			}
			return posted(a) != was
		case <-time.After(5 * time.Second):
			t.Fatal("no round")
		}
		return false
	}
	pa, pb := a.tab().Focus, b.tab().Focus
	if !round() || st.infos[pa.SID].ID == "" || pa.info.ID != pa.SID || pb.info.ID != pb.SID {
		t.Fatalf("the first round drew nothing, or a window took nothing: %q %q", pa.info.ID, pb.info.ID)
	}
	// The shells settle at their prompts: the list then stays the same.
	quiet := func() {
		t.Helper()
		for i := 0; round(); i++ {
			if i == 20 {
				t.Fatal("every round draws the windows")
			}
		}
	}
	quiet()
	typed := func(p *Pane) bool {
		t.Helper()
		if err := client.Input(p.SID, []byte(" ")); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			if round() {
				return !p.info.LastInput.IsZero()
			}
		}
		t.Fatal("no round drew the windows after a change")
		return false
	}
	if !typed(pa) || !typed(pb) {
		t.Fatal("a window did not take the new list")
	}
	if pa.info.LastInput != st.infos[pa.SID].LastInput || pb.info.LastInput != st.infos[pb.SID].LastInput {
		t.Error("the windows took another list than the store's")
	}

	st.closed(b)
	quiet()
	if was := pa.info.LastInput; !typed(pa) || !pa.info.LastInput.After(was) {
		t.Error("the window left does not take the list")
	}
	st.closed(a)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll goes on with no window")
	}
	if st.polling != nil {
		t.Error("a poll is still noted")
	}
	quit(st)
}
