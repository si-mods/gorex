package main

import (
	"slices"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestTabIDsLast checks that every tab has a lasting ID, that the layout
// keeps it through a save and a restore, and that a tab of a layout from
// before IDs gets one.
func TestTabIDsLast(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	res := clientCommand(a, rex.LayoutCommand{Op: "tab.create"})
	if res.Code != "" || res.Tab == "" {
		t.Fatalf("tab.create: %+v", res)
	}
	second := a.tabByUID(res.Tab)
	if first.UID == "" || second == nil || second == first {
		t.Fatalf("IDs: first %q, made %q", first.UID, res.Tab)
	}
	a.saveNow()
	raw, err := a.client.Layout()
	if err != nil {
		t.Fatal(err)
	}
	saved := layout.Parse(raw)
	tabs := saved.Sessions[0].Tabs
	if len(tabs) != 2 || tabs[0].ID != first.UID || tabs[1].ID != second.UID {
		t.Fatalf("saved %+v", tabs)
	}
	b := &App{client: a.client}
	if !b.reconcile(saved.Sessions[0].Layout) {
		t.Fatal("nothing restored")
	}
	if b.tabs[0].UID != first.UID || b.tabs[1].UID != second.UID {
		t.Fatalf("restored %q %q", b.tabs[0].UID, b.tabs[1].UID)
	}

	// A layout of before IDs: its tabs get new, distinct ones, which the
	// layout keeps from then on.
	old := saved.Sessions[0].Layout
	old.Tabs = append([]layout.Tab(nil), old.Tabs...)
	for i := range old.Tabs {
		old.Tabs[i].ID = ""
	}
	c := &App{client: a.client}
	if !c.reconcile(old) {
		t.Fatal("nothing restored")
	}
	if c.tabs[0].UID == "" || c.tabs[1].UID == "" || c.tabs[0].UID == c.tabs[1].UID {
		t.Fatalf("migrated %q %q", c.tabs[0].UID, c.tabs[1].UID)
	}
	if got := c.savedTab(c.tabs[0]).ID; got != c.tabs[0].UID {
		t.Fatalf("saved migrated %q, want %q", got, c.tabs[0].UID)
	}
}

// TestTabCommandsByID names tabs by their lasting ID: a rename and a
// close reach the tab named, and an unknown ID is not found.
func TestTabCommandsByID(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	made := clientCommand(a, rex.LayoutCommand{Op: "tab.create"})
	if made.Code != "" {
		t.Fatalf("tab.create: %+v", made)
	}
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.rename", Tab: first.UID, Name: "logs"}); res.Code != "" || first.Name != "logs" {
		t.Fatalf("rename: %+v %q", res, first.Name)
	}
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.close", Tab: made.Tab}); res.Code != "" || a.tabByUID(made.Tab) != nil || len(a.tabs) != 1 {
		t.Fatalf("close: %+v, %d tabs", res, len(a.tabs))
	}
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.rename", Tab: "no-such-tab", Name: "x"}); res.Code != "tab_not_found" {
		t.Fatalf("unknown tab: %+v", res)
	}
}

// TestCloseLastTabKeepsSpace closes the window's last tab from a client:
// its sessions end, but the space stays, as a Rex session goes on with no
// windows; the window, with no other space to show, leaves it.
func TestCloseLastTabKeepsSpace(t *testing.T) {
	a, _ := newTestApp(t)
	ws := a.ws
	only := a.tab()
	sid := only.Focus.SID
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.close", Tab: only.UID}); res.Code != "" {
		t.Fatalf("close: %+v", res)
	}
	if len(a.tabs) != 0 || a.st.workspaceByID(ws.ID) == nil {
		t.Fatalf("after: %d tabs, spaces %v", len(a.tabs), a.st.workspaces)
	}
	saved := savedWorkspaces(t, a.client)
	if w := saved.Space(ws.ID); w == nil || len(w.Tabs) != 0 {
		t.Fatalf("the server's space: %+v", w)
	}
	if infos, _ := a.client.List(); slices.ContainsFunc(infos, func(in rex.SessionInfo) bool { return in.ID == sid }) {
		t.Error("the closed tab's session runs")
	}
}
