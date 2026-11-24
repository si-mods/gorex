package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// toastPrefs has the test run with animations off, as a toast shows and
// goes at once then, and puts the settings back after it.
func toastPrefs(t *testing.T) {
	t.Helper()
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs.Animation = "off"
}

// TestToastLife: a status toast shows 5 s, longer for a long text; an
// action toast at least 6 s; a toast of its own time keeps it.
func TestToastLife(t *testing.T) {
	long := strings.Repeat("x", 150)
	for _, c := range []struct {
		m    toastMsg
		want time.Duration
	}{
		{toastMsg{kind: toastStatus, text: "Saved"}, toastLife},
		{toastMsg{kind: toastError, text: long}, 10 * time.Second},
		{toastMsg{kind: toastAction, text: "Deleted Work"}, toastActionLife},
		{toastMsg{kind: toastStatus, text: "Restored Work", life: toastBrief}, toastBrief},
	} {
		if got := c.m.lifeOf(); got != c.want {
			t.Errorf("%d of %d runes: %v, want %v", c.m.kind, len(c.m.text), got, c.want)
		}
	}
}

// TestToastGoesByItself: an error toast shows the sentence it was given,
// with no banner, and goes once its time is over.
func TestToastGoesByItself(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	a.toastError("Couldn't run Split Right: no such pane.")
	tt.Frame()
	if !tt.HasText("Couldn't run Split Right: no such pane.") {
		t.Fatalf("no toast: %q", tt.Texts())
	}
	if a.err != "" || a.errMsg != nil {
		t.Errorf("the banner says %q", a.errText())
	}
	tt.Advance(toastLife - 100*time.Millisecond)
	tt.Frame()
	if !tt.HasText("Couldn't run Split Right: no such pane.") {
		t.Fatal("the toast went before its time")
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Couldn't run Split Right: no such pane.") || a.toastText() != "" {
		t.Errorf("the toast stayed: %q", tt.Texts())
	}
}

// TestToastReplaces: a window has one toast; a new one takes the place of
// an action toast, whose action commits first, once.
func TestToastReplaces(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	commits := 0
	a.showToast(&toastMsg{kind: toastAction, text: "Deleted Work", action: locales.ToastUndo(), commit: func() { commits++ }})
	tt.Frame()
	a.toastError("Not connected to alice-laptop.")
	if commits != 1 {
		t.Fatalf("the replaced toast committed %d times", commits)
	}
	tt.Frame()
	if tt.HasText("Deleted Work") || !tt.HasText("Not connected to alice-laptop.") {
		t.Errorf("the toasts: %q", tt.Texts())
	}
	tt.Advance(time.Minute)
	tt.Frame()
	a.endToast()
	if commits != 1 {
		t.Errorf("committed %d times", commits)
	}
}

// TestToastPauses: the time of a toast stops while the pointer rests on
// it, and runs on once the pointer leaves.
func TestToastPauses(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	a.toastError("Not connected to alice-laptop.")
	tt.Frame()
	r, ok := tt.Find("Not connected to alice-laptop.")
	if !ok {
		t.Fatalf("no toast: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(3 * toastLife)
	tt.Frame()
	if !tt.HasText("Not connected to alice-laptop.") {
		t.Fatal("the toast went under the pointer")
	}
	tt.Move(2, 2)
	tt.Frame()
	tt.Advance(toastLife + time.Second)
	tt.Frame()
	if tt.HasText("Not connected to alice-laptop.") {
		t.Error("the toast stayed once the pointer left")
	}
}

// TestToastAboveBanner: while the banner shows, the toast sits above it,
// not over it.
func TestToastAboveBanner(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	a.errMsg = locales.Messages.ErrorConnectionLost
	a.toastError("Couldn't start a session: timed out.")
	for range 3 {
		tt.Frame()
	}
	banner, ok1 := tt.Find(locales.ErrorConnectionLost())
	toast, ok2 := tt.Find("Couldn't start a session: timed out.")
	if !ok1 || !ok2 {
		t.Fatalf("banner %v, toast %v: %q", ok1, ok2, tt.Texts())
	}
	if toast.Y+toast.H >= banner.Y {
		t.Errorf("the toast (%v) over the banner (%v)", toast, banner)
	}
}

// TestToastUndo: an action toast's button runs its action and takes the
// place of its commit; its close button commits.
func TestToastUndo(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	var undone, commits int
	show := func() {
		a.showToast(&toastMsg{kind: toastAction, text: "Deleted Work", action: locales.ToastUndo(),
			run: func() { undone++ }, commit: func() { commits++ }})
		tt.Frame()
	}
	show()
	if err := tt.Click(locales.ToastUndo()); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(time.Minute)
	tt.Frame()
	if undone != 1 || commits != 0 || tt.HasText("Deleted Work") {
		t.Errorf("Undo: %d undone, %d commits, texts %q", undone, commits, tt.Texts())
	}
	show()
	if err := tt.Click(locales.ToastClose()); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if undone != 1 || commits != 1 || tt.HasText("Deleted Work") {
		t.Errorf("Close: %d undone, %d commits, texts %q", undone, commits, tt.Texts())
	}
}

// TestCommandFailureToasts: an action that fails says so in an error
// toast, a sentence of the catalog, not on the banner.
func TestCommandFailureToasts(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewTab, map[string]any{"dir": 5})
	want := locales.ToastActionFailed(cmdNewTab.Title(), "tab.new: dir: want string")
	if got := a.toastText(); got != want {
		t.Errorf("the toast says %q, want %q", got, want)
	}
	if a.err != "" || a.errMsg != nil {
		t.Errorf("the banner says %q", a.errText())
	}
	tt.Frame()
	if !tt.HasText(want) {
		t.Errorf("no toast: %q", tt.Texts())
	}
	if got := reasonOf(errors.New("rex: connection to the server lost.")); got != "connection to the server lost" {
		t.Errorf("the reason: %q", got)
	}
}

// TestOfflineHostActionsDisabled: while the host of the window's tab is
// offline, the palette shows what would make a session there disabled,
// saying why; a key that asks for one gets an error toast.
func TestOfflineHostActionsDisabled(t *testing.T) {
	toastPrefs(t)
	a, tt := newTestApp(t)
	h, proxy, p := remotePane(t, a, tt, t.TempDir())
	if a.tab() != p.Tab {
		a.selectTab(slices.Index(a.tabs, p.Tab))
	}
	p.Tab.setFocus(p)
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	why := locales.HostsNotConnectedTo(a.hostName(h))
	a.paletteList = listCommands
	items := a.paletteItems()
	for _, title := range []string{cmdSplitRight.Title(), cmdSplitDown.Title(), cmdNewFloat.Title(), cmdRestart.Title(), locales.CmdNewTabOn(a.hostName(h))} {
		found := false
		for _, it := range items {
			if it.title == title {
				found = true
				if !it.disabled || it.detail != why {
					t.Errorf("%s: disabled %v, detail %q", title, it.disabled, it.detail)
				}
			}
		}
		if !found {
			t.Errorf("no row %q", title)
		}
	}
	for _, it := range items {
		if it.title == cmdNewBrowser.Title() && it.disabled {
			t.Error("New Browser Tab, of this machine, is disabled")
		}
	}
	a.invokeCommand(&cmdSplitRight, nil)
	if got := a.toastText(); got != why || a.err != "" || a.errMsg != nil {
		t.Errorf("the key's split: toast %q, banner %q", got, a.errText())
	}
	// A client's New Floating Pane fails, saying so; the key's says why.
	if err := cmdNewFloat.invoke(a, nil); err == nil {
		t.Error("float.new from a client ran with the host offline")
	}
	a.toastError("")
	a.invokeCommand(&cmdNewFloat, nil)
	if got := a.toastText(); got != why {
		t.Errorf("the key's float: toast %q", got)
	}
}

// TestNewFloatNeedsTab: New Floating Pane fails with no tab, for a client,
// and the palette shows it disabled.
func TestNewFloatNeedsTab(t *testing.T) {
	a := &App{}
	if err := cmdNewFloat.invoke(a, nil); err == nil {
		t.Error("float.new ran with no tab")
	}
	for _, it := range a.paletteItems() {
		if it.title == cmdNewFloat.Title() && !it.disabled {
			t.Error("the palette offers New Floating Pane with no tab")
		}
	}
}

// TestNotificationsOffRow: Settings, Appearance, says so while macOS
// refuses GoRex's notifications, with a button to its settings; a
// notification shown later takes the row away.
func TestNotificationsOffRow(t *testing.T) {
	a, _ := newTestApp(t)
	st := ui.NewTester(a.settingsView, 780, 900)
	st.Frame()
	if st.HasText(locales.SettingsNotificationsOff()) {
		t.Fatal("the row shows with notifications allowed")
	}
	a.st.notified(errors.New("some other failure"))
	a.st.notified(mygo.ErrNotificationsDenied)
	st.Frame()
	if !st.HasText(locales.SettingsNotificationsOff()) || !st.HasText(locales.SettingsNotificationsOpen()) {
		t.Fatalf("no row: %q", st.Texts())
	}
	a.st.notified(nil)
	st.Frame()
	if st.HasText(locales.SettingsNotificationsOff()) {
		t.Error("the row stayed once a notification showed")
	}
}
