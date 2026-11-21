package main

import (
	"os"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
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
// only after End Session or Enter, while Cancel and Escape keep it.
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
	for _, keep := range []func(){
		func() {
			if err := tt.Click("Cancel"); err != nil {
				t.Fatalf("%v; texts %q", err, tt.Texts())
			}
		},
		func() { tt.Key(0, ui.KeyEscape) },
	} {
		runs(p)
		a.closeActiveTab()
		tt.Frame()
		if !a.closeAsk.open || !tt.HasText("sleep is running") {
			t.Fatalf("closing a running tab did not ask; texts %q", tt.Texts())
		}
		if !slices.Contains(a.tabs, busy) || p.closed {
			t.Fatal("the tab closed before the answer")
		}
		anchored(t, tt, "Close Tab")
		keep()
		tt.Frame()
		tt.Frame()
		if a.closeAsk.open || !slices.Contains(a.tabs, busy) || p.closed {
			t.Fatalf("the question was kept: open %v, tab kept %v", a.closeAsk.open, slices.Contains(a.tabs, busy))
		}
	}

	runs(p)
	a.closeActiveTab()
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if slices.Contains(a.tabs, busy) || !p.closed {
		t.Fatalf("Enter did not end the session: tab kept %v; texts %q", slices.Contains(a.tabs, busy), tt.Texts())
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
	a.newTab(os.TempDir())
	tt.Frame()
	a.askCloseOthers(keep)
	tt.Frame()
	if !tt.HasText("Close 2 other tabs?") || len(a.tabs) != 3 {
		t.Fatalf("no question; %d tabs; texts %q", len(a.tabs), tt.Texts())
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
