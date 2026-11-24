package main

import (
	"testing"

	"gorex/internal/rex"
	"gorex/locales"
)

// fakeQuit has Quit and End All Sessions count its quits rather than end
// the server and the app.
func fakeQuit(t *testing.T) *int {
	t.Helper()
	n := new(int)
	old := endAndQuit
	endAndQuit = func(*rex.Client) { *n++ }
	t.Cleanup(func() { endAndQuit = old })
	return n
}

// TestQuitAndEndAsks: Quit and End All Sessions asks first while this
// machine has sessions; Cancel keeps them and the app, and the danger
// button quits.
func TestQuitAndEndAsks(t *testing.T) {
	quits := fakeQuit(t)
	a, tt := newTestApp(t)
	n := a.localSessions()
	if n == 0 {
		t.Fatal("the window's tab has no session")
	}
	a.invokeCommand(&cmdEndAll, nil)
	tt.Frame()
	if !tt.HasText(locales.QuitEndTitle(n)) || !tt.HasText(locales.QuitEndBody(n)) || *quits != 0 {
		t.Fatalf("no question, or it quit: %d quits, %q", *quits, tt.Texts())
	}
	click(t, tt, locales.SessionCancel())
	if a.quitAsk || *quits != 0 || a.quitting {
		t.Fatalf("Cancel: asking %v, %d quits", a.quitAsk, *quits)
	}
	if infos, _ := a.client.List(); len(infos) != n {
		t.Errorf("%d sessions after Cancel, want %d", len(infos), n)
	}
	a.invokeCommand(&cmdEndAll, nil)
	click(t, tt, locales.QuitEndConfirm())
	tt.Frame()
	waitFor(t, tt, "the quit", func() bool { return *quits == 1 })
}

// TestQuitAndEndNoSessions: with no session to end, Quit and End All
// Sessions quits at once.
func TestQuitAndEndNoSessions(t *testing.T) {
	quits := fakeQuit(t)
	a, tt := newTestApp(t)
	a.closeTab(a.tab())
	tt.Frame()
	if n := a.localSessions(); n != 0 {
		t.Fatalf("%d sessions left", n)
	}
	a.invokeCommand(&cmdEndAll, nil)
	tt.Frame()
	waitFor(t, tt, "the quit", func() bool { return *quits == 1 })
	if a.quitAsk {
		t.Error("it asked")
	}
}
