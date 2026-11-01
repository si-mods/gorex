package main

import (
	"encoding/json"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// savedSpaces returns the spaces of the layout the server keeps.
func savedSpaces(t *testing.T, a *App) []layout.Workspace {
	t.Helper()
	a.saveNow()
	raw, err := a.client.Layout()
	if err != nil {
		t.Fatal(err)
	}
	var st layout.Store
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for _, id := range st.Windows {
		found := false
		for _, w := range st.Sessions {
			found = found || w.ID == id
		}
		if !found {
			t.Errorf("window shows %s, which the layout does not have", id)
		}
	}
	return st.Sessions
}

// TestClientClosesLastSpace closes every space from a client, as Rex's
// app destroys every session: none is left, rather than a new one coming
// back each time; the next one the client makes takes the window.
func TestClientClosesLastSpace(t *testing.T) {
	a, _ := newTestApp(t)
	if res := a.command(rex.LayoutCommand{Op: "space.close", Space: a.ws.ID}); res.Code != "" {
		t.Fatalf("space.close: %+v", res)
	}
	if spaces := savedSpaces(t, a); len(spaces) != 0 {
		t.Fatalf("after closing the last space, the layout has %d: %+v", len(spaces), spaces)
	}
	if a.ws == nil || !a.ws.unlisted || len(a.tabs) != 0 {
		t.Fatalf("the window shows %+v with %d tabs, want an unlisted empty space", a.ws, len(a.tabs))
	}
	placeholder := a.ws

	res := a.command(rex.LayoutCommand{Op: "space.create", Name: "next"})
	if res.Code != "" || res.Space == "" {
		t.Fatalf("space.create: %+v", res)
	}
	if res := a.command(rex.LayoutCommand{Op: "tab.create", Space: res.Space}); res.Code != "" {
		t.Fatalf("tab.create: %+v", res)
	}
	spaces := savedSpaces(t, a)
	if len(spaces) != 1 || spaces[0].Name != "next" || len(spaces[0].Tabs) != 1 {
		t.Fatalf("layout = %+v, want only the space made", spaces)
	}
	if a.st.workspaceByID(placeholder.ID) != nil {
		t.Error("the unlisted space stayed after the window went to another")
	}
}

// TestDesktopClosesLastSpace keeps the desktop's way: the window opens a
// new space with a tab.
func TestDesktopClosesLastSpace(t *testing.T) {
	a, _ := newTestApp(t)
	a.closeWorkspace()
	if a.ws == nil || a.ws.unlisted || len(a.tabs) != 1 {
		t.Fatalf("the window shows %+v with %d tabs, want a new listed space with a tab", a.ws, len(a.tabs))
	}
	if spaces := savedSpaces(t, a); len(spaces) != 1 {
		t.Fatalf("layout has %d spaces, want 1", len(spaces))
	}
}

// TestUnlistedSpaceListsWithFirstTab lists the space once the desktop
// opens a tab in it.
func TestUnlistedSpaceListsWithFirstTab(t *testing.T) {
	a, _ := newTestApp(t)
	a.command(rex.LayoutCommand{Op: "space.close", Space: a.ws.ID})
	id := a.ws.ID
	if res := a.command(rex.LayoutCommand{Op: "tab.create", Space: id}); res.Code != "" {
		t.Fatalf("tab.create: %+v", res)
	}
	spaces := savedSpaces(t, a)
	if len(spaces) != 1 || spaces[0].ID != id {
		t.Fatalf("layout = %+v, want the window's space listed", spaces)
	}
}
