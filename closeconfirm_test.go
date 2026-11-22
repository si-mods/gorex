package main

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

// runs makes the pane look as if a program ran in its foreground.
func runs(p *Pane) { p.info.Idle, p.info.Program = false, "sleep" }

// anchored checks that the question shows under the close button named
// label, as a popconfirm, not as a dialog in the middle of the window.
func anchored(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	x, ok := tt.Find(label)
	q, ok2 := tt.Find("sleep is running")
	if !ok || !ok2 {
		t.Fatalf("no %q or no question; texts %q", label, tt.Texts())
	}
	if q.Y < x.Y+x.H || q.Y > x.Y+x.H+48 {
		t.Errorf("the question at %+v is not under %s at %+v", q, label, x)
	}
}

// TestCloseTabAsksWhileRunning checks Close Tab: a tab of idle shells
// closes at once; one where a program runs asks, and ends its sessions
// only after End Session, while Cancel, Escape, and Enter or Space typed
// on for the terminal keep it.
func TestCloseTabAsksWhileRunning(t *testing.T) {
	a, tt := newTestApp(t)
	idle := a.newTab(os.TempDir())
	tt.Frame()
	a.closeActiveTab()
	tt.Frame()
	if slices.Contains(a.tabs, idle) || a.closeAsk.open {
		t.Fatalf("an idle tab did not close at once: asked %v", a.closeAsk.open)
	}

	busy := a.newTab(os.TempDir())
	p := busy.Focus
	tt.Frame()
	for name, keep := range map[string]func(){
		"Cancel": func() {
			if err := tt.Click("Cancel"); err != nil {
				t.Fatalf("%v; texts %q", err, tt.Texts())
			}
		},
		"Escape": func() { tt.Key(0, ui.KeyEscape) },
		"Enter":  func() { tt.Key(0, ui.KeyEnter) },
		"Space":  func() { tt.Key(0, ui.KeySpace) },
	} {
		runs(p)
		a.closeActiveTab()
		tt.Frame()
		if !a.closeAsk.open || !tt.HasText("sleep is running") {
			t.Fatalf("%s: closing a running tab did not ask; texts %q", name, tt.Texts())
		}
		if !slices.Contains(a.tabs, busy) || p.closed {
			t.Fatalf("%s: the tab closed before the answer", name)
		}
		anchored(t, tt, "Close Tab")
		keep()
		tt.Frame()
		tt.Frame()
		if a.closeAsk.open || !slices.Contains(a.tabs, busy) || p.closed {
			t.Fatalf("%s did not keep the tab: open %v, tab kept %v", name, a.closeAsk.open, slices.Contains(a.tabs, busy))
		}
	}

	runs(p)
	a.closeActiveTab()
	tt.Frame()
	if err := tt.Click("End Session"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if slices.Contains(a.tabs, busy) || !p.closed {
		t.Fatalf("End Session did not end the session: tab kept %v", slices.Contains(a.tabs, busy))
	}
}

// TestCloseAsksWhatRuns checks which panes count as running: a session
// started with a command asks though its program is in the foreground of
// none, and a shell at its prompt with a done or error record not seen
// yet does not.
func TestCloseAsksWhatRuns(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	p.info = rex.SessionInfo{Program: "npm", Idle: true, Command: true}
	if !p.running() {
		t.Error("a command session does not count as running")
	}
	for _, state := range []string{"done", "error"} {
		p.info = rex.SessionInfo{Program: "zsh", Idle: true, Statuses: []rex.ProgramStatus{{State: state, Updated: time.Now()}}}
		if p.running() {
			t.Errorf("a shell at its prompt with a %s record counts as running", state)
		}
	}
	for _, state := range []string{"working", "blocked"} {
		p.info = rex.SessionInfo{Program: "zsh", Idle: true, Statuses: []rex.ProgramStatus{{State: state, Updated: time.Now()}}}
		if !p.running() {
			t.Errorf("a %s record does not count as running", state)
		}
	}

	cmd := a.newTab(os.TempDir())
	cmd.Focus.info = rex.SessionInfo{Program: "npm", Idle: true, Command: true}
	tt.Frame()
	a.closeActiveTab()
	tt.Frame()
	if !a.closeAsk.open || !tt.HasText("npm is running") {
		t.Fatalf("closing a command session did not ask; texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	done := a.newTab(os.TempDir())
	done.Focus.info = rex.SessionInfo{Program: "zsh", Idle: true, Statuses: []rex.ProgramStatus{{State: "done", Updated: time.Now()}}}
	tt.Frame()
	a.closeActiveTab()
	tt.Frame()
	if slices.Contains(a.tabs, done) || a.closeAsk.open {
		t.Fatalf("an idle shell with a done record did not close at once")
	}
}

// TestClosePaneAsksWhileRunning checks a pane's close button: it asks
// while the pane runs a program, anchored to the button, and End Session
// closes that pane alone.
func TestClosePaneAsksWhileRunning(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	p := tab.Focus
	q := a.splitPane(p, false, os.TempDir())
	tt.Frame()
	a.askClosePane(p)
	tt.Frame()
	if !p.closed || a.closeAsk.open {
		t.Fatalf("an idle pane did not close at once")
	}
	runs(q)
	tab.setFocus(q)
	a.closeFocused()
	tt.Frame()
	if !a.closeAsk.open || a.closeAsk.pane != q || !tt.HasText("sleep is running") {
		t.Fatalf("closing a running pane did not ask; texts %q", tt.Texts())
	}
	anchored(t, tt, "Close Pane")
	if err := tt.Click("End Session"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if !q.closed || slices.Contains(a.tabs, tab) {
		t.Fatalf("End Session did not close the pane: closed %v", q.closed)
	}
}

// TestCloseOtherTabsAsks checks that Close Other Tabs asks first, how
// many tabs, and closes them only after the answer.
func TestCloseOtherTabsAsks(t *testing.T) {
	a, tt := newTestApp(t)
	keep := a.tab()
	a.newTab(os.TempDir())
	a.newPaneTab(a.newNotePane(""))
	tt.Frame()
	a.askCloseOthers(keep)
	tt.Frame()
	if !tt.HasText("Close 2 other tabs?") || len(a.tabs) != 3 {
		t.Fatalf("no question; %d tabs; texts %q", len(a.tabs), tt.Texts())
	}
	// The note is no terminal, and no program runs in the other tabs.
	if !tt.HasText("Its terminal ends. This can't be undone.") {
		t.Fatalf("the body counts other than the terminals; texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if len(a.tabs) != 3 || a.closeOthersOpen {
		t.Fatalf("Enter did not cancel: %d tabs, open %v", len(a.tabs), a.closeOthersOpen)
	}
	runs(a.tabs[1].Focus)
	a.askCloseOthers(keep)
	tt.Frame()
	if !tt.HasText("Its terminal ends, and the program running in it. This can't be undone.") {
		t.Fatalf("the body does not say the program ends; texts %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.tabs) != 3 || a.closeOthersOpen {
		t.Fatalf("Cancel closed tabs: %d left", len(a.tabs))
	}
	a.askCloseOthers(keep)
	tt.Frame()
	if err := tt.Click("Close Other Tabs"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0] != keep {
		t.Fatalf("%d tabs left, want the one kept", len(a.tabs))
	}
}

// TestCloseAskOutOfView checks the questions whose close button does not
// show: a tab's row scrolled out of the sidebar asks with the dialog, and
// a pane of a space the window no longer shows is not closed.
func TestCloseAskOutOfView(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs.TabPlacement = "left"
	tt.SetSize(1000, 320)
	first := a.tab()
	for range 14 {
		a.newTab(os.TempDir())
	}
	tt.Frame()
	tt.Frame()
	runs(first.Focus)
	a.askCloseTab(first)
	tt.Frame()
	tt.Frame()
	q, ok := tt.Find("sleep is running")
	if !ok || !a.closeAsk.open {
		t.Fatalf("no question; texts %q", tt.Texts())
	}
	// The row's button is clipped: the question is the dialog, over the
	// panes, not a popover from the button.
	if list := a.sideList; q.W == 0 || q.X < list.X+list.W {
		t.Errorf("the question at %+v is not the dialog beside the list %+v", q, list)
	}

	// The question outlives the space it asked about: answered after the
	// window shows another space, it closes nothing.
	p := a.tab().Focus
	runs(p)
	a.askClosePane(p)
	was := a.ws
	a.createProject(nil, appTempDir(t, a), "other")
	waitFor(t, tt, "the other project", func() bool { return a.ws != was })
	if slices.Contains(a.tabs, p.Tab) {
		t.Fatal("the window still shows the space")
	}
	a.endAsked()
	if p.closed {
		t.Error("a pane of a space no longer shown was closed")
	}
}

// TestConfirmButtonsMatch checks that a question's Cancel and its red
// button round their corners alike: Cancel is the stock button, whose
// theme radius is not dangerButton's radiusM unless cancelButton sets it.
func TestConfirmButtonsMatch(t *testing.T) {
	k := lightColors
	bg := ui.Hex("#00ff00")
	// corner counts the pixels of the button's top left 14 by 14 corner
	// that show the background alone, untouched by the button's edge.
	corner := func(button func(c *ui.Context)) int {
		img := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Size(200, 60).Background(bg).AlignItems(ui.Start).Children(func() { button(c) })
		}, 200, 60).Image()
		n := 0
		for y := range 14 {
			for x := range 14 {
				o := img.PixOffset(x, y)
				if p := img.Pix[o : o+3]; p[0] < 16 && p[1] > 240 && p[2] < 16 {
					n++
				}
			}
		}
		return n
	}
	cancel := corner(func(c *ui.Context) { cancelButton(c, "Cancel") })
	danger := corner(func(c *ui.Context) { dangerButton(c, &k, "End Session") })
	if d := cancel - danger; d < -2 || d > 2 {
		t.Errorf("Cancel's corner leaves %d pixels of the background, the red button's %d; want the same radius", cancel, danger)
	}
}
