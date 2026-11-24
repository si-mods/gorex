package main

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// spaceDeleteApp is a window with a project of this machine in space Work,
// shown, and a host offline, whose queue counts the deletes sent: each
// delete of Work the app sends queues one for it (sendSpaceRenames).
func spaceDeleteApp(t *testing.T) (*App, *ui.Tester, *Workspace) {
	t.Helper()
	return spaceDeleteAppOf(t, true)
}

// spaceDeleteAppOf is spaceDeleteApp, its host offline when away, else
// connected.
func spaceDeleteAppOf(t *testing.T, away bool) (*App, *ui.Tester, *Workspace) {
	t.Helper()
	a, tt, sock, _ := projectsApp(t)
	proxy := newHostProxy(t, sock)
	h := a.addHost(savedHost{ID: "h1", Name: "alice-laptop", Address: proxy.path})
	waitFor(t, tt, "the host", func() bool { return h.current() != nil })
	if away {
		proxy.setDown(true)
		waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	}
	work := "Work"
	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-api", SpaceName: &work})
	caughtUp(t, a, tt)
	api := a.st.byID(res.Space)
	if api == nil {
		t.Fatal("the project did not come")
	}
	setSpaceColorOf(work, "blue")
	a.showSpaceNamed(work)
	return a, tt, api
}

// sentDeletes counts the deletes of space Work the app sent.
func sentDeletes() int { return len(prefs.SpaceRenames["h1"]) }

// TestDeleteSpaceUndo: Undo of Delete Space brings the space back as it
// was, shown, with its color, and no host hears of the delete, then or
// later.
func TestDeleteSpaceUndo(t *testing.T) {
	a, tt, api := spaceDeleteApp(t)
	a.newSpace("Alpha")
	a.newSpace("Beta")
	a.showSpaceNamed("Work")
	order := slices.IndexFunc(prefs.Spaces, func(s clientSpace) bool { return s.Name == "Work" })
	a.removeSpace("Work")
	// Another space moves while the toast shows.
	a.st.moveSpace("Beta", -1)
	tt.Frame()
	if shownSpace() != "" || a.st.projectSpace(api) != "" {
		t.Fatalf("deleted: shown %q, the project in %q", shownSpace(), a.st.projectSpace(api))
	}
	click(t, tt, locales.ToastUndo())
	if shownSpace() != "Work" || a.st.projectSpace(api) != "Work" || spaceColor("Work") != "blue" {
		t.Errorf("after Undo: shown %q, the project in %q, color %q", shownSpace(), a.st.projectSpace(api), spaceColor("Work"))
	}
	if i := slices.IndexFunc(prefs.Spaces, func(s clientSpace) bool { return s.Name == "Work" }); i != order {
		t.Errorf("after Undo, Work is at %d of the order, was at %d", i, order)
	}
	if !tt.HasText(locales.SpaceRestored("Work")) {
		t.Errorf("no Restored toast: %q", tt.Texts())
	}
	// The pointer that pressed Undo rests on the new toast, and holds it.
	tt.Move(2, 2)
	tt.Frame()
	tt.Advance(toastBrief + time.Second)
	tt.Frame()
	if tt.HasText(locales.SpaceRestored("Work")) {
		t.Error("the Restored toast stayed")
	}
	tt.Advance(time.Minute)
	tt.Frame()
	a.st.flushToasts()
	if n := sentDeletes(); n != 0 || api.Space != "Work" {
		t.Errorf("after Undo, %d deletes sent, the project in %q", n, api.Space)
	}
}

// TestDeleteSpaceCommits: the delete goes once, as the toast ends by its
// time, by its close button, by another toast, by its window's close, or
// by the app's quit.
func TestDeleteSpaceCommits(t *testing.T) {
	for _, c := range []struct {
		name string
		end  func(a *App, tt *ui.Tester)
	}{
		{"time", func(a *App, tt *ui.Tester) { tt.Advance(toastActionLife + time.Second); tt.Frame() }},
		{"close", func(a *App, tt *ui.Tester) {
			if err := tt.Click(locales.ToastClose()); err != nil {
				panic(err)
			}
			tt.Frame()
		}},
		{"replaced", func(a *App, tt *ui.Tester) { a.toastError("Not connected to alice-laptop.") }},
		{"window", func(a *App, tt *ui.Tester) { a.st.closed(a) }},
		{"quit", func(a *App, tt *ui.Tester) { a.st.flushToasts() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, tt, api := spaceDeleteApp(t)
			a.removeSpace("Work")
			tt.Frame()
			if n := sentDeletes(); n != 0 {
				t.Fatalf("%d deletes sent before the toast ended", n)
			}
			c.end(a, tt)
			if n := sentDeletes(); n != 1 {
				t.Fatalf("%d deletes sent, want 1", n)
			}
			// Nothing sends it again.
			tt.Advance(time.Minute)
			tt.Frame()
			a.endToast()
			a.st.flushToasts()
			if n := sentDeletes(); n != 1 {
				t.Errorf("%d deletes sent in the end, want 1", n)
			}
			if api.Space != "" || clientSpaceOf("Work") != nil {
				t.Errorf("after the delete: the project in %q, the space kept %v", api.Space, clientSpaceOf("Work") != nil)
			}
		})
	}
}

// TestDeleteSpaceUndoInDialog: the trash button of Edit Spaces deletes
// the space, and the toast's Undo answers over the open dialog.
func TestDeleteSpaceUndoInDialog(t *testing.T) {
	a, tt, api := spaceDeleteApp(t)
	click(t, tt, locales.SpacePickerLabel("Work"))
	settle(tt) // the menu's parts take no clicks until they are in
	click(t, tt, locales.SpaceEdit())
	settle(tt)
	click(t, tt, locales.SpaceDelete())
	tt.Frame()
	if slices.Contains(a.st.spaceNames(), "Work") || a.spaces.dialog != "edit" {
		t.Fatalf("after the trash button: names %q, dialog %q", a.st.spaceNames(), a.spaces.dialog)
	}
	click(t, tt, locales.ToastUndo())
	if !slices.Contains(a.st.spaceNames(), "Work") || a.st.projectSpace(api) != "Work" || a.st.deleting["Work"] != nil {
		t.Errorf("after Undo: names %q, the project in %q; texts %q", a.st.spaceNames(), a.st.projectSpace(api), tt.Texts())
	}
	if n := sentDeletes(); n != 0 {
		t.Errorf("%d deletes sent", n)
	}
}

// TestDeleteSpaceQuitKeeps: a delete the quit sends is queued for each
// connected host too, once, so that the next connection sends it again if
// the quit cut it short.
func TestDeleteSpaceQuitKeeps(t *testing.T) {
	a, tt, api := spaceDeleteAppOf(t, false)
	a.removeSpace("Work")
	tt.Frame()
	a.st.quitting = true
	a.st.flushToasts()
	a.st.flushToasts()
	if q := prefs.SpaceRenames["h1"]; len(q) != 1 || q[0] != (spaceRename{From: "Work"}) {
		t.Errorf("the queue of the connected host: %+v", q)
	}
	waitFor(t, tt, "the delete here", func() bool {
		sw := spaceOfStore(savedWorkspaces(t, a.client), api.ID)
		return sw != nil && sw.Space == ""
	})
}

// TestSpaceDeleteResendHarmless: a delete that reaches a server again,
// as a queued one after a quit, changes nothing and fails nothing.
func TestSpaceDeleteResendHarmless(t *testing.T) {
	a, tt, _ := spaceDeleteAppOf(t, false)
	stay := "Stay"
	other := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-web", SpaceName: &stay})
	caughtUp(t, a, tt)
	for i := range 2 {
		if res := clientCommand(a, rex.LayoutCommand{Op: "spaces.delete", From: "Work"}); res.Code != "" {
			t.Fatalf("delete %d: %+v", i+1, res)
		}
	}
	if sw := spaceOfStore(savedWorkspaces(t, a.client), other.Space); sw == nil || sw.Space != stay {
		t.Errorf("the project of another space: %+v", sw)
	}
}

// TestNewSpaceCommitsPendingDelete: a space made with the name of one
// whose delete waits sends that delete first, and shows.
func TestNewSpaceCommitsPendingDelete(t *testing.T) {
	a, tt, _ := spaceDeleteApp(t)
	a.removeSpace("Work")
	tt.Frame()
	a.newSpace("Work")
	if n := sentDeletes(); n != 1 || a.st.deleting["Work"] != nil {
		t.Fatalf("%d deletes sent, pending %v", n, a.st.deleting["Work"] != nil)
	}
	if !slices.Contains(a.st.spaceNames(), "Work") || shownSpace() != "Work" || a.toastText() == locales.SpaceDeleted("Work") {
		t.Errorf("the new space: names %q, shown %q, toast %q", a.st.spaceNames(), shownSpace(), a.toastText())
	}
}

// TestHostItemsClearedOnLastClose: the menu bar's items an offline host
// disabled are enabled again once no window is left.
func TestHostItemsClearedOnLastClose(t *testing.T) {
	a, _ := newTestApp(t)
	oldItems, oldOff := hostItems, hostItemsOff
	t.Cleanup(func() { hostItems, hostItemsOff = oldItems, oldOff })
	mi := &mygo.MenuItem{Disabled: true}
	hostItems = map[*command]*mygo.MenuItem{&cmdNewTab: mi}
	hostItemsOff = map[*command]bool{&cmdNewTab: true}
	a.st.closed(a)
	if mi.Disabled || hostItemsOff[&cmdNewTab] {
		t.Errorf("New Tab stays disabled with no window: %v %v", mi.Disabled, hostItemsOff[&cmdNewTab])
	}
}
