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
// app destroys every session: none is left, and the window, with no space
// to show, leaves it; the next one the client makes is the layout's only.
func TestClientClosesLastSpace(t *testing.T) {
	a, _ := newTestApp(t)
	if res := clientCommand(a, rex.LayoutCommand{Op: "space.close", Space: a.ws.ID}); res.Code != "" {
		t.Fatalf("space.close: %+v", res)
	}
	if spaces := savedSpaces(t, a); len(spaces) != 0 {
		t.Fatalf("after closing the last space, the layout has %d: %+v", len(spaces), spaces)
	}
	if a.ws != nil || len(a.tabs) != 0 {
		t.Fatalf("the window shows %+v with %d tabs", a.ws, len(a.tabs))
	}
	res := clientCommand(a, rex.LayoutCommand{Op: "space.create", Name: "next"})
	if res.Code != "" || res.Space == "" {
		t.Fatalf("space.create: %+v", res)
	}
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.create", Space: res.Space}); res.Code != "" {
		t.Fatalf("tab.create: %+v", res)
	}
	spaces := savedSpaces(t, a)
	if len(spaces) != 1 || spaces[0].Name != "next" || len(spaces[0].Tabs) != 1 {
		t.Fatalf("layout = %+v, want only the space made", spaces)
	}
}

// TestDesktopClosesLastSpace keeps the desktop's way: the window opens a
// new space with a tab.
func TestDesktopClosesLastSpace(t *testing.T) {
	a, _ := newTestApp(t)
	a.closeWorkspace()
	if a.ws == nil || len(a.tabs) != 1 {
		t.Fatalf("the window shows %+v with %d tabs, want a new space with a tab", a.ws, len(a.tabs))
	}
	if spaces := savedSpaces(t, a); len(spaces) != 1 || len(spaces[0].Tabs) != 1 {
		t.Fatalf("layout has %+v, want 1 space of a tab", spaces)
	}
}
