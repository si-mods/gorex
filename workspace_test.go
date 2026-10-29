package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// TestParseLayout checks that the layout of a GoRex of one window becomes
// a workspace, which its window shows, and that a layout of workspaces
// drops what it cannot use.
func TestParseLayout(t *testing.T) {
	one := `{"active":1,"tabs":[{"name":"a","root":{"sid":"s1","dir":"/x"},"focus":0},{"root":{"sid":"s2"},"focus":0}]}`
	s := parseLayout([]byte(one))
	if s.Version != layoutVersion || len(s.Sessions) != 1 || !slices.Equal(s.Windows, []string{s.Sessions[0].ID}) {
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
		if s := parseLayout([]byte(raw)); len(s.Sessions) != 0 || len(s.Windows) != 0 {
			t.Errorf("%q: %+v", raw, s)
		}
	}
	two := `{"version":2,"sessions":[{"id":"a","name":"icy-ridge","tabs":[]},{"id":"a","name":"again"},{"id":"","name":"none"},{"id":"b","name":" "}],"windows":["b","zz","a","b"]}`
	s = parseLayout([]byte(two))
	if len(s.Sessions) != 2 || s.Sessions[0].Name != "icy-ridge" || s.Sessions[1].ID != "b" || s.Sessions[1].Name == " " {
		t.Errorf("sessions %+v", s.Sessions)
	}
	if !slices.Equal(s.Windows, []string{"b", "a"}) {
		t.Errorf("windows %q", s.Windows)
	}
}

// TestSpaceColor checks that the layout keeps a space's color, that a layout
// of before colors parses, and is saved again without one.
func TestSpaceColor(t *testing.T) {
	s := parseLayout([]byte(`{"version":2,"sessions":[{"id":"a","name":"icy-ridge","color":"rose"}],"windows":["a"]}`))
	if len(s.Sessions) != 1 || s.Sessions[0].Color != "rose" {
		t.Fatalf("parsed %+v", s.Sessions)
	}
	older := parseLayout([]byte(`{"version":2,"sessions":[{"id":"a","name":"icy-ridge"}],"windows":["a"]}`))
	if len(older.Sessions) != 1 || older.Sessions[0].Color != "" {
		t.Fatalf("a layout of before colors parsed %+v", older.Sessions)
	}
	if b, err := json.Marshal(older); err != nil || strings.Contains(string(b), "color") {
		t.Errorf("saved %s: %v", b, err)
	}

	// A new space is of the first color the palette has, which the server
	// keeps with it.
	a, _ := newTestApp(t)
	st := a.st
	first := a.ws
	if first.Color != spacePalette[0].ID {
		t.Fatalf("the first space is of color %q", first.Color)
	}
	st.saveNow()
	if got := savedWorkspaces(t, a.client).Sessions[0].Color; got != first.Color {
		t.Errorf("the server keeps color %q, want %q", got, first.Color)
	}
	quit(st)
}

// TestSpaceColorsAssigned checks that new spaces take the palette's colors
// in order, and that the tenth, every one taken, takes a spare.
func TestSpaceColorsAssigned(t *testing.T) {
	st := &store{}
	for i, p := range spacePalette {
		if w := st.addWorkspace(); w.Color != p.ID {
			t.Fatalf("space %d is of color %q, want %q", i+1, w.Color, p.ID)
		}
	}
	if w := st.addWorkspace(); w.Color != spacePalette[0].ID {
		t.Errorf("the tenth space is of color %q, want %q", w.Color, spacePalette[0].ID)
	}
	// The least used, in the palette's order, once two are used twice.
	if w := st.addWorkspace(); w.Color != spacePalette[1].ID {
		t.Errorf("the eleventh space is of color %q, want %q", w.Color, spacePalette[1].ID)
	}
	// A custom color takes no preset.
	st.workspaces[0].Color = "#123456"
	if w := st.addWorkspace(); w.Color != spacePalette[0].ID {
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

// quit detaches every window, as the app quitting does.
func quit(st *store) {
	st.quitting = true
	for _, a := range st.apps {
		a.detach()
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
	a.newWorkspace()
	second := a.ws
	a.newTab(testShell.dir)
	secondSIDs := sidsOf(a)
	b := st.openWindow(st.addWorkspace(), false)
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
	for _, p := range n0.tabs[0].panes() {
		if p.restored {
			t.Errorf("pane %s started anew", p.SID)
		}
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
	b := st.openWindow(st.addWorkspace(), false)
	st.raise(a)
	mine := a.ws
	sids := sidsOf(a)
	a.showWorkspace(b.ws)
	if a.ws != mine || st.apps[0] != b || b.ws.app != b {
		t.Fatalf("a shows %v, the front window is %p (b %p)", a.ws.Name, st.apps[0], b)
	}
	hidden := st.addWorkspace()
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
	other := st.addWorkspace()
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
// forgets it, and shows a workspace no window shows.
func TestLastTabClosesWorkspace(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	first := a.ws
	a.newWorkspace()
	second := a.ws
	a.closeTab(a.tab())
	if a.ws != first || slices.Contains(st.workspaces, second) || len(a.tabs) != 1 {
		t.Errorf("after the last tab the window shows %v", a.ws)
	}
	quit(st)
}

// TestOrphansInFirstWorkspace checks that sessions no workspace names, as
// those of an app that crashed, get tabs in the first window's workspace,
// and those of a workspace no window shows do not.
func TestOrphansInFirstWorkspace(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	first := a.ws
	a.newWorkspace()
	hiddenSID := a.tab().Focus.SID
	a.showWorkspace(first)
	tt.Frame()
	orphan, err := a.client.Create(rex.CreateOptions{Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	st.saveNow()
	quit(st)

	next := &store{}
	next.start()
	if len(next.apps) != 1 {
		t.Fatalf("%d windows", len(next.apps))
	}
	got := sidsOf(next.apps[0])
	if next.apps[0].ws.ID != first.ID || len(got) != 2 || got[1] != orphan.ID || slices.Contains(got, hiddenSID) {
		t.Errorf("the first workspace has %q; the orphan is %s, the hidden workspace's %s", got, orphan.ID, hiddenSID)
	}
	quit(next)
}

// TestMoveTabToNewWindow checks that a tab moves to a new workspace in a
// window of its own, its sessions going on, and that the last tab stays.
func TestMoveTabToNewWindow(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	a.newTab(testShell.dir)
	moved := a.tab()
	sid := moved.Focus.SID
	a.moveTabToNewWindow(moved)
	if len(a.tabs) != 1 || len(st.apps) != 2 || len(st.workspaces) != 2 {
		t.Fatalf("%d tabs, %d windows, %d workspaces", len(a.tabs), len(st.apps), len(st.workspaces))
	}
	b := st.apps[0]
	if b == a || b.ws == a.ws || len(b.tabs) != 1 || b.tab().Focus.SID != sid || b.tab().Focus.restored {
		t.Fatalf("the new window has %q", sidsOf(b))
	}
	if !slices.Contains(liveSIDs(t, a.client), sid) {
		t.Error("the moved tab's session ended")
	}
	a.moveTabToNewWindow(a.tab())
	if len(a.tabs) != 1 || len(st.apps) != 2 {
		t.Errorf("the last tab moved: %d tabs, %d windows", len(a.tabs), len(st.apps))
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
	b := st.openWindow(st.addWorkspace(), false)
	bws := b.ws
	st.closed(b)
	if len(st.apps) != 1 || bws.app != nil || !slices.Contains(st.workspaces, bws) {
		t.Fatalf("closing a window: %d windows, its workspace shown by %p", len(st.apps), bws.app)
	}
	c := st.openWindow(st.addWorkspace(), false)
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
	if len(st.apps) != 1 || st.apps[0].ws != w || !slices.Equal(sidsOf(st.apps[0]), sids) {
		t.Errorf("a second launch opened %d windows", len(st.apps))
	}
	quit(st)
}

// TestLayoutWriterOrder checks that the layouts reach the server in the
// order written, the last last.
func TestLayoutWriterOrder(t *testing.T) {
	a, _ := newTestApp(t)
	var w layoutWriter
	for i := range 50 {
		w.write(a.client, []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	w.wait(5 * time.Second)
	if raw, _ := a.client.Layout(); string(raw) != `{"n":49}` {
		t.Errorf("the server keeps %s", raw)
	}
}

// TestSessionChip checks the session chip and its popover, above the tabs
// and in the sidebar: it lists the workspaces, shows another, renames, and
// the palette changes to one by its name.
func TestSessionChip(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	st := a.st
	first := a.ws
	first.Name = "icy-ridge"
	other := st.addWorkspace()
	other.Name = "quiet-harbor"
	tt.Frame()
	if err := tt.Click(locales.SessionLabel("icy-ridge")); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("quiet-harbor") || !tt.HasText(locales.SessionTabs(0)) || !tt.HasText(cmdNewSession.Title()) {
		t.Fatalf("no list of workspaces; texts %q", tt.Texts())
	}
	if err := tt.Click("quiet-harbor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if a.ws != other || a.wsOpen {
		t.Fatalf("the window shows %s", a.ws.Name)
	}
	if got := a.windowTitle(); !regexp.MustCompile(`^quiet-harbor — `).MatchString(got) {
		t.Errorf("window title %q", got)
	}

	// Rename Space edits the name in the chip.
	a.startWorkspaceRename()
	a.wsRenameText = ""
	tt.Frame()
	tt.Type("my-work")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if other.Name != "my-work" || a.wsRenaming {
		t.Errorf("renamed to %q", other.Name)
	}

	// The sidebar shows the chip too.
	prefs = settings{TabPlacement: "left"}
	tt.Frame()
	if _, ok := tt.Find(locales.SessionLabel("my-work")); !ok {
		t.Errorf("no session chip in the sidebar; texts %q", tt.Texts())
	}

	// Change Space… lists the workspaces by name.
	a.openSessionPalette()
	tt.Frame()
	if !tt.HasText("icy-ridge") || !a.paletteSessions {
		t.Fatalf("no workspaces in the palette; texts %q", tt.Texts())
	}
	tt.Type("icy")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.ws != first {
		t.Errorf("Change Space showed %s", a.ws.Name)
	}
	quit(st)
}

// TestSpaceColorPicker checks the popover's colors: a click takes one,
// which the layout keeps, and Default gives the window its own back.
func TestSpaceColorPicker(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	tt.Frame()
	if err := tt.Click(locales.SessionLabel(a.ws.Name)); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText(locales.SpaceColor()) || !tt.HasText(locales.SpaceColorDefault()) {
		t.Fatalf("no colors in the popover; texts %q", tt.Texts())
	}
	if err := tt.Click("Blue"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if a.ws.Color != "blue" {
		t.Errorf("the space is of color %q, want blue", a.ws.Color)
	}
	if !a.wsOpen {
		t.Error("choosing a color closed the popover")
	}
	st.saveNow()
	if got := savedWorkspaces(t, a.client).Sessions[0].Color; got != "blue" {
		t.Errorf("the server keeps color %q, want blue", got)
	}
	if err := tt.Click(locales.SpaceColorDefault()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.ws.Color != "" {
		t.Errorf("Default left the space of color %q", a.ws.Color)
	}

	// The field takes a custom #rrggbb, and leaves a color it cannot use.
	a.wsColorText = " #ABCDEF "
	a.useSpaceColorText()
	if a.ws.Color != "#abcdef" {
		t.Errorf("the field gave the space %q", a.ws.Color)
	}
	a.wsColorText = "nope"
	a.useSpaceColorText()
	if a.ws.Color != "#abcdef" {
		t.Errorf("an unusable color became %q", a.ws.Color)
	}
	quit(st)
}

// TestShowSpace checks that Show Space counts the spaces from 1, that 9 is
// the last, and that a number no space has does nothing.
func TestShowSpace(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	first := a.ws
	second := st.addWorkspace()
	third := st.addWorkspace()
	tt.Frame()
	a.showSpace(2)
	tt.Frame()
	if a.ws != second {
		t.Fatalf("Show Space 2 shows %s", a.ws.Name)
	}
	a.showSpace(9)
	tt.Frame()
	if a.ws != third {
		t.Fatalf("Show Space 9 shows %s", a.ws.Name)
	}
	a.showSpace(5)
	tt.Frame()
	if a.ws != third {
		t.Errorf("Show Space 5 shows %s", a.ws.Name)
	}
	a.showSpace(0)
	tt.Frame()
	if a.ws != third {
		t.Errorf("Show Space 0 shows %s", a.ws.Name)
	}
	a.showSpace(1)
	tt.Frame()
	if a.ws != first {
		t.Errorf("Show Space 1 shows %s", a.ws.Name)
	}
	quit(st)
}

// spaceDotLabel is the label of a space's dot in the sidebar's strip: its
// name, and its shortcut.
func spaceDotLabel(i int, w *Workspace) string {
	return w.Name + "  " + showSpaceKeys(i+1)
}

// TestSpaceStrip checks the dots at the foot of the sidebar: the 2nd shows
// its space, and the plus button makes a space, which the window shows.
func TestSpaceStrip(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	st := a.st
	a.ws.Name = "icy-ridge"
	a.ws.Color = "teal"
	second := st.addWorkspace()
	second.Name = "quiet-harbor"
	second.Color = "blue"
	third := st.addWorkspace()
	third.Name = "misty-canyon"
	third.Color = "#ff8800"
	tt.Frame()
	before := len(st.workspaces)
	if _, ok := tt.Find(spaceDotLabel(1, second)); !ok {
		t.Fatalf("no dot for %s; texts %q", second.Name, tt.Texts())
	}
	if err := tt.Click(spaceDotLabel(1, second)); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if a.ws != second {
		t.Fatalf("the 2nd dot shows %s", a.ws.Name)
	}
	// A right click on the window's own dot renames or closes its space;
	// another's has no menu.
	if err := tt.RightClick(spaceDotLabel(2, third)); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if got := tt.Menu(); got != nil {
		t.Errorf("another space's dot has the menu %q", got)
	}
	if err := tt.RightClick(spaceDotLabel(1, second)); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if got := tt.Menu(); !slices.Contains(got, cmdRenameSession.Title()) || !slices.Contains(got, cmdCloseSession.Title()) {
		t.Fatalf("the own dot's menu is %q", got)
	}
	if err := tt.ChooseMenuItem(cmdRenameSession.Title()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.wsRenaming {
		t.Error("Rename Space… did not open the space's name")
	}
	a.wsRenaming, a.wsRenameFocus = false, false
	tt.Frame()
	if err := tt.Click(cmdNewSession.Title()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if len(st.workspaces) != before+1 {
		t.Fatalf("the plus button made %d spaces", len(st.workspaces)-before)
	}
	if a.ws != st.workspaces[len(st.workspaces)-1] {
		t.Errorf("the plus button did not show the space it made")
	}
	quit(st)
}

// TestSpaceStripKeys checks that the shortcut of a space shows it, as the
// menu's keys do on the platform it runs on.
func TestSpaceStripKeys(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	st := a.st
	second := st.addWorkspace()
	st.addWorkspace()
	tt.Frame()
	k := showSpaceKey(2)
	if !tt.HasText(spaceDotLabel(1, second)) {
		t.Fatalf("no dot for %s; texts %q", second.Name, tt.Texts())
	}
	tt.Key(k.Mods, k.Key)
	tt.Frame()
	tt.Frame()
	if a.ws != second {
		t.Fatalf("%s shows %s", k.String(), a.ws.Name)
	}
	quit(st)
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
		a.wsOpen = true
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
	a.wsOpen = false
	quit(a.st)
}

// saveShot writes img as name in dir, magnified by scale when it is not
// 1 and cropped to r when r is not empty.
func saveShot(t *testing.T, dir, name string, img *image.RGBA, r image.Rectangle, scale int) {
	t.Helper()
	if r.Empty() {
		r = img.Bounds()
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx()*scale, r.Dy()*scale))
	for y := range r.Dy() * scale {
		for x := range r.Dx() * scale {
			dst.Set(x, y, img.At(r.Min.X+x/scale, r.Min.Y+y/scale))
		}
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, dst); err != nil {
		t.Fatal(err)
	}
}

// TestColorPickerShots renders the space popover's colors in the sidebar,
// whole and magnified, light and dark, into GOREX_SHOTS, to look at: the
// panel must be opaque behind the color label, the swatches and the
// field, whichever tab titles sit under it.
func TestColorPickerShots(t *testing.T) {
	dir := os.Getenv("GOREX_SHOTS")
	if dir == "" {
		t.Skip("GOREX_SHOTS names no directory")
	}
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	st := a.st
	a.ws.Name = "icy-ridge"
	for _, name := range []string{
		`pwsh C:\Users\yibo\repos\gorex`,
		`vim src/workspaceview.go`,
		`ssh studio -t tail -f build.log`,
	} {
		a.newTab(os.TempDir()).Name = name
	}
	os.MkdirAll(dir, 0o755)
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		tt.SetDark(dark)
		a.wsOpen = true
		for range 3 {
			tt.Frame()
		}
		img := tt.Image()
		saveShot(t, dir, fmt.Sprintf("color-picker-%s.png", theme), img, image.Rectangle{}, 1)
		label, ok := tt.Find(locales.SpaceColor())
		if !ok {
			t.Fatalf("no color section; texts %q", tt.Texts())
		}
		saveShot(t, dir, fmt.Sprintf("color-picker-%s-colors.png", theme), img,
			image.Rect(int(label.X)-10, int(label.Y)-8, int(label.X)+272, int(label.Y)+92), 2)
		// The fields with the focus: the popover's custom color, and the
		// space's name, both outlined in the space's accent.
		swatch, ok := tt.Find("Default")
		if !ok {
			t.Fatalf("no color swatches; texts %q", tt.Texts())
		}
		field := ui.Rect{X: label.X, Y: swatch.Y + swatch.H + 6, W: 254, H: 28}
		for _, c := range []string{"green", "pink"} {
			a.ws.Color = c
			for range 3 {
				tt.Frame()
			}
			tt.ClickAt(field.X+field.W/2, field.Y+field.H/2)
			for range 3 {
				tt.Frame()
			}
			saveShot(t, dir, fmt.Sprintf("focus-color-%s-%s.png", c, theme), tt.Image(),
				image.Rect(int(field.X)-8, int(field.Y)-8, int(field.X+field.W)+8, int(field.Y+field.H)+8), 3)
			a.wsRenaming, a.wsRenameText, a.wsRenameFocus = true, a.ws.Name, false
			for range 3 {
				tt.Frame()
			}
			tt.Key(ui.Cmd, ui.KeyA)
			for range 3 {
				tt.Frame()
			}
			chip, ok := tt.Find(locales.SessionLabel(a.ws.Name))
			if !ok {
				t.Fatalf("no session chip; texts %q", tt.Texts())
			}
			saveShot(t, dir, fmt.Sprintf("focus-rename-%s-%s.png", c, theme), tt.Image(),
				image.Rect(int(chip.X)-6, int(chip.Y)-6, int(chip.X)+214, int(chip.Y+chip.H)+6), 4)
			a.wsRenaming = false
		}
		a.ws.Color = ""
		a.wsOpen = false
	}
	quit(st)
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
		st.addWorkspace().Color = c
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
	st.addWorkspace().Name = "quiet-harbor"
	b := st.openWindow(st.addWorkspace(), false)
	b.ws.Name = "misty-canyon"
	os.MkdirAll(dir, 0o755)
	for _, place := range []string{"", "left"} {
		for _, dark := range []bool{false, true} {
			prefs = settings{TabPlacement: place}
			tt.SetDark(dark)
			a.wsOpen = true
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
	a.wsOpen = false
	a.askCloseWorkspace()
	tt.Frame()
	f, _ := os.Create(filepath.Join(dir, "sessions-close-dark.png"))
	png.Encode(f, tt.Image())
	f.Close()
	a.wsConfirm = false
	quit(st)
}

// TestAdoptAcrossWorkspaces checks that a host's session no tab shows
// opens in the window focused last only, and not one a workspace no
// window shows has a tab of.
func TestAdoptAcrossWorkspaces(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	h := newHostConn(savedHost{ID: "h1", Name: "studio"})
	a.hosts = []*hostConn{h}
	a.syncHosts()
	hidden := st.addWorkspace()
	hidden.layout = savedLayout{Tabs: []savedTab{{Root: &savedNode{SID: "r1", Host: "h1"}}}}
	b := st.openWindow(st.addWorkspace(), false)
	byID := map[string]rex.SessionInfo{"r1": {ID: "r1"}, "r2": {ID: "r2"}}
	for _, o := range a.peers() {
		o.apply(h, byID, hostLayout{})
	}
	count := func(o *App, sid string) int {
		n := 0
		for _, s := range sidsOf(o) {
			if s == sid {
				n++
			}
		}
		return n
	}
	if count(b, "r2") != 1 || count(a, "r2") != 0 {
		t.Errorf("r2 opened %d times in the front window, %d in the other", count(b, "r2"), count(a, "r2"))
	}
	if count(a, "r1")+count(b, "r1") != 0 {
		t.Error("a session of a hidden workspace opened again")
	}
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
	b := st.openWindow(st.addWorkspace(), false)
	a.ws.Name, b.ws.Name = "icy-ridge", "quiet-harbor"
	hidden := st.addWorkspace()
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
	b := st.openWindow(st.addWorkspace(), false)
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
	sids, old := sidsOf(a), st.client
	old.Close()
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
	// seen times its window recorded.
	w := a.st.addWorkspace()
	w.layout = savedLayout{Tabs: []savedTab{{Root: &savedNode{SID: p.SID}}}}
	a.st.infos = map[string]rex.SessionInfo{p.SID: p.info}
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
