package main

import (
	"encoding/json"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestTabIDsLast checks that every tab has a lasting ID, that the layout
// keeps it through a save and a restore, and that a tab of a layout from
// before IDs gets one.
func TestTabIDsLast(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	res := a.command(rex.LayoutCommand{Op: "tab.create"})
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
	if !b.reconcile(saved.Sessions[0].Layout, nil, nil) {
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
	if !c.reconcile(old, nil, nil) {
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
	made := a.command(rex.LayoutCommand{Op: "tab.create"})
	if made.Code != "" {
		t.Fatalf("tab.create: %+v", made)
	}
	if res := a.command(rex.LayoutCommand{Op: "tab.rename", Tab: first.UID, Name: "logs"}); res.Code != "" || first.Name != "logs" {
		t.Fatalf("rename: %+v %q", res, first.Name)
	}
	if res := a.command(rex.LayoutCommand{Op: "tab.close", Tab: made.Tab}); res.Code != "" || a.tabByUID(made.Tab) != nil || len(a.tabs) != 1 {
		t.Fatalf("close: %+v, %d tabs", res, len(a.tabs))
	}
	if res := a.command(rex.LayoutCommand{Op: "tab.rename", Tab: "no-such-tab", Name: "x"}); res.Code != "tab_not_found" {
		t.Fatalf("unknown tab: %+v", res)
	}
}

// TestCloseLastTabKeepsSpace closes the window's last tab from a client:
// its sessions end, but the space stays, the window showing it empty, as a
// Rex session goes on with no windows.
func TestCloseLastTabKeepsSpace(t *testing.T) {
	a, _ := newTestApp(t)
	ws := a.ws
	only := a.tab()
	sid := only.Focus.SID
	if res := a.command(rex.LayoutCommand{Op: "tab.close", Tab: only.UID}); res.Code != "" {
		t.Fatalf("close: %+v", res)
	}
	if len(a.tabs) != 0 || a.ws != ws || ws == nil || ws.app != a {
		t.Fatalf("after: %d tabs, space %v (was %v)", len(a.tabs), a.ws, ws)
	}
	if a.st.workspaceByID(ws.ID) == nil {
		t.Fatal("the space was forgotten")
	}
	// The kill is sent; the session ends soon after.
	for deadline := time.Now().Add(3 * time.Second); ; {
		infos, err := a.client.List()
		if err != nil {
			t.Fatal(err)
		}
		running := false
		for _, in := range infos {
			running = running || in.ID == sid && !in.Exited
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s still runs", sid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A client makes the next tab in the empty space.
	if res := a.command(rex.LayoutCommand{Op: "tab.create", Space: ws.ID}); res.Code != "" || len(a.tabs) != 1 {
		t.Fatalf("tab.create: %+v, %d tabs", res, len(a.tabs))
	}
	a.saveNow()
	raw, _ := a.client.Layout()
	var st layout.Store
	json.Unmarshal(raw, &st)
	if len(st.Sessions) == 0 || st.Sessions[0].ID != ws.ID {
		t.Fatalf("saved %s", raw)
	}
}
