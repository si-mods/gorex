package main

import (
	"bytes"
	"image/png"
	"slices"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
	"gorex/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestPaneStopped keeps the pane of a session whose stream ended while the
// server still has it — the crash — and leaves the pane of one that exited
// to the server, which takes it out of the layout, with no window's help:
// the window follows.
func TestPaneStopped(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	a.newTab(testShell.dir)
	followApp(t, a)
	caughtUp(t, a, tt)

	// The server still has the session: the pane and its tab stay.
	a.paneStopped(p)
	time.Sleep(200 * time.Millisecond)
	if p.closed || len(a.tabs) != 2 {
		t.Fatal("the pane of a session the server has ended")
	}

	// The shell exits: the server takes its pane out, and the window
	// follows; the other tab stays.
	if err := a.client.Input(p.SID, []byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the exited pane to go", func() bool { return p.closed && len(a.tabs) == 1 })
	if slices.Contains(liveSIDs(t, a.client), p.SID) {
		t.Error("the exited session is still listed")
	}
}

// waitForThat runs until cond holds, or fails after a few seconds. Unlike
// waitFor it draws no frame: what it waits for is what the server says.
func waitForThat(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPaneLabelTitle checks what names a pane: a shell by its name at the
// prompt, and another program by the title it sets, even when it is the
// program the session started with.
func TestPaneLabelTitle(t *testing.T) {
	shell := &Pane{info: rex.SessionInfo{Program: "zsh", Shell: "zsh", Idle: true}, title: "~/repos/gorex"}
	if name, _ := shell.label(); name != "zsh" {
		t.Errorf("a shell is named %q", name)
	}
	mirror := &Pane{info: rex.SessionInfo{Program: "rex-mirror", Shell: "rex-mirror", Idle: true}, title: "Build the site"}
	if name, _ := mirror.label(); name != "Build the site" {
		t.Errorf("a mirror is named %q", name)
	}
	unnamed := &Pane{info: rex.SessionInfo{Program: "rex-mirror", Shell: "rex-mirror", Idle: true}}
	if name, _ := unnamed.label(); name != "rex-mirror" {
		t.Errorf("a mirror without a title is named %q", name)
	}
}

// TestPaneProgram checks how a pane's program shows: what it reported,
// else a shell's own tile, else the executable.
func TestPaneProgram(t *testing.T) {
	shell := &Pane{info: rex.SessionInfo{Program: "zsh", Shell: "zsh", Idle: true}}
	if got := shell.program().Name; got != "zsh" {
		t.Errorf("a shell shows as %q", got)
	}
	mirror := &Pane{info: rex.SessionInfo{
		Program: "rex-mirror", Shell: "rex-mirror", Idle: true,
		Status: &rex.ProgramStatus{State: "working", App: "omp"},
	}}
	if got := mirror.program().Name; got != "omp" {
		t.Errorf("a mirror of omp shows as %q", got)
	}
	claude := &Pane{info: rex.SessionInfo{
		Program: "rex-mirror", Shell: "rex-mirror", Idle: true,
		Status: &rex.ProgramStatus{State: "done", App: "claude-code"},
	}}
	if got := claude.program().Name; got != "Claude Code" {
		t.Errorf("a mirror of Claude Code shows as %q", got)
	}
}

// TestNotifyPane: a status notification names the program in its title,
// tells the record's message, else what a done program did last, and
// under the title the pane's tab and name but for the program, in the
// body when it has none. A
// session's notifications stack together, and a click brings the pane
// back from another tab. The notification holds no pane, only its ID.
func TestNotifyPane(t *testing.T) {
	a, _ := newTestApp(t)
	var got []mygo.NotificationOptions
	showNotification = func(o mygo.NotificationOptions) func() { got = append(got, o); return func() {} }
	t1 := a.tab()
	t1.Name = "work"
	p := t1.Focus
	name, _ := p.label()
	a.newTab(t.TempDir())
	a.notifyStatus(p, &rex.ProgramStatus{State: "blocked", App: "claude", Msg: "Allow edit?"})
	a.notifyStatus(p, &rex.ProgramStatus{State: "done", App: "claude", LastMsg: "Fixed the test"})
	a.notifyStatus(p, &rex.ProgramStatus{State: "done", App: name})
	t1.Name = "claude"
	a.notifyStatus(p, &rex.ProgramStatus{State: "error", App: "claude"})
	want := [][4]string{
		{locales.NotifyBlocked("claude"), "work · " + name, "Allow edit?", "|" + p.SID},
		{locales.NotifyFinished("claude"), "work · " + name, "Fixed the test", "|" + p.SID},
		{locales.NotifyFinished(name), "", "work", "|" + p.SID},
		{locales.NotifyFailed("claude"), "", name, "|" + p.SID},
	}
	for i, o := range got {
		if g := [4]string{o.Title, o.Subtitle, o.Body, o.Group}; i >= len(want) || g != want[i] {
			t.Fatalf("notification %d is %q", i, g)
		}
	}
	if len(got) != len(want) || got[0].ID == got[1].ID {
		t.Fatalf("%d notifications, IDs %q", len(got), got[0].ID)
	}
	a.focusedWin = true
	if a.tab() == t1 || a.shows(p) {
		t.Fatal("the pane shows from another tab")
	}
	a.st.notificationClicked(got[0].ID)
	if a.tab() != t1 || t1.Focus != p || !a.shows(p) {
		t.Fatalf("the click went to tab %d", a.active)
	}
	a.focusedWin = false
	if a.shows(p) {
		t.Fatal("the pane shows in a window without the focus")
	}
	// One status notification in 10 seconds; a program's own does not
	// keep one from showing, and waits after either.
	p.notified, p.told = time.Time{}, time.Time{}
	if !p.notifyDue(true) || p.notifyDue(true) || !p.notifyDue(false) || p.notifyDue(false) {
		t.Error("a program's notification kept the status's from showing, or two came at once")
	}
	p.told = time.Time{}
	if p.notifyDue(true) {
		t.Error("a program's notification came right after the status's")
	}
}

// TestNotificationImage: a program's notification shows its tile, as a
// PNG, the tile's color in its middle, once the tile is drawn, which the
// first notification does not wait for; one of a shell shows none.
func TestNotificationImage(t *testing.T) {
	a, tt := newTestApp(t)
	var got []mygo.NotificationOptions
	showNotification = func(o mygo.NotificationOptions) func() { got = append(got, o); return func() {} }
	p := a.tab().Focus
	a.focusedWin = false
	claude := programOf("claude")
	tile := waitTile(t, tt, claude, false)
	a.notifyStatus(p, &rex.ProgramStatus{State: "done", App: "claude"})
	p.notified = time.Time{}
	a.notifyProgram(p, "hi", "")
	if len(got) != 2 {
		t.Fatalf("%d notifications", len(got))
	}
	if !bytes.Equal(got[0].Image, tile) {
		t.Fatal("Claude Code's notification shows another image")
	}
	img, err := png.Decode(bytes.NewReader(got[0].Image))
	if err != nil {
		t.Fatalf("the image of Claude Code's notification: %v", err)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Min.X+b.Dx()/4, b.Min.Y+b.Dy()/2).RGBA()
	if want := claude.TileBg; absDiff(r>>8, uint32(want.R)) > 24 || absDiff(g>>8, uint32(want.G)) > 24 || absDiff(bl>>8, uint32(want.B)) > 24 {
		t.Errorf("the tile is %d,%d,%d, not %v", r>>8, g>>8, bl>>8, want)
	}
	if got[1].Image != nil {
		t.Error("a shell's notification has an image")
	}
}

// TestTileImageKey draws a tile for what it shows, not the program's
// name: two names of one tile share it, one name of two colors does not,
// nor do the dark appearance and the light, whose rims differ.
func TestTileImageKey(t *testing.T) {
	_, tt := newTestApp(t)
	py, ipy := programOf("python"), programOf("ipython")
	other := py
	other.TileBg = ui.Hex("#000000")
	light := waitTile(t, tt, py, false)
	if b := tileImage(ipy, false); !bytes.Equal(b, light) {
		t.Error("two names of one tile draw it twice")
	}
	if b := waitTile(t, tt, other, false); bytes.Equal(b, light) {
		t.Error("a tile of another color is the cached one")
	}
	if b := waitTile(t, tt, py, true); bytes.Equal(b, light) {
		t.Error("the dark appearance's tile is the light one")
	}
}

// waitTile waits for tileImage to have drawn the tile of prog.
func waitTile(t *testing.T, tt *ui.Tester, prog program, dark bool) []byte {
	t.Helper()
	var b []byte
	waitFor(t, tt, "the tile of "+prog.Name, func() bool { b = tileImage(prog, dark); return b != nil })
	return b
}

func absDiff(x, y uint32) uint32 { return max(x, y) - min(x, y) }

// TestNotificationClickFindsSession: a click on a notification shows its
// session though the pane it was of is gone: from the space no window
// shows, then in another window that attached it again.
func TestNotificationClickFindsSession(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	var got []mygo.NotificationOptions
	showNotification = func(o mygo.NotificationOptions) func() { got = append(got, o); return func() {} }
	first := a.ws
	a.newTab(t.TempDir())
	p := a.tabs[0].Focus
	a.notifyPane(p, "", program{}, "hi", "")
	id := got[0].ID
	a.newWorkspace()
	tt.Frame()
	if first.app != nil || a.ws == first {
		t.Fatal("the space still shows")
	}
	st.notificationClicked(id)
	if a.ws != first || a.tab().Focus != p {
		t.Fatalf("the click showed %s", a.ws.Name)
	}
	a.newWorkspace()
	tt.Frame()
	b := st.openWindow(st.addWorkspace())
	b.showWorkspace(first)
	b.selectTab(1)
	if !p.closed {
		t.Fatal("the kept pane is still open")
	}
	st.notificationClicked(id)
	if q := b.tab().Focus; b.active != 0 || q == p || q.SID != p.SID {
		t.Fatalf("the click went to tab %d", b.active)
	}
	st.notificationClicked("gorex||gone|1")
	st.notificationClicked("other")
	if st.clicked != "" {
		t.Fatal("a notification of no GoRex session waits")
	}
}

// TestNotificationClickWaits: a click with no window, as one that launched
// the app, waits for the windows' polls, which bring its pane forward.
func TestNotificationClickWaits(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	var got []mygo.NotificationOptions
	showNotification = func(o mygo.NotificationOptions) func() { got = append(got, o); return func() {} }
	p := a.tab().Focus
	a.notifyPane(p, "", program{}, "hi", "")
	a.newTab(t.TempDir())
	apps := st.apps
	st.apps = nil
	st.notificationClicked(got[0].ID)
	st.apps = apps
	if st.clicked == "" || a.active != 1 {
		t.Fatalf("the click went to tab %d, waits %v", a.active, st.clicked != "")
	}
	refresh(a)
	if st.clicked != "" || a.active != 0 || a.tab().Focus != p {
		t.Fatalf("the poll went to tab %d, waits %v", a.active, st.clicked != "")
	}
}

// TestNotificationsCloseSeen: a session's notifications go once its pane
// is seen, others stay; a program's own notification of a pane gone or
// on screen does not show.
func TestNotificationsCloseSeen(t *testing.T) {
	a, _ := newTestApp(t)
	closed := map[string]int{}
	shown := 0
	showNotification = func(o mygo.NotificationOptions) func() {
		shown++
		return func() { closed[o.Group]++ }
	}
	t1 := a.tab()
	p := t1.Focus
	q := a.newTab(t.TempDir()).Focus
	a.focusedWin = true
	a.notifyProgram(q, "", "on screen")
	a.notifyProgram(p, "", "one")
	a.notifyStatus(p, &rex.ProgramStatus{State: "blocked", App: "claude"})
	a.notifyStatus(q, &rex.ProgramStatus{State: "blocked", App: "claude"})
	if shown != 3 {
		t.Fatalf("%d notifications, want 3", shown)
	}
	a.selectTab(0)
	if closed[notifyKey(p)] != 2 || closed[notifyKey(q)] != 0 {
		t.Fatalf("seeing the pane closed %v", closed)
	}
	p.told = time.Time{}
	p.closed = true
	a.focusedWin = false
	a.notifyProgram(p, "", "gone")
	if shown != 3 {
		t.Fatal("a notification of a pane gone showed")
	}
	p.closed = false
}
