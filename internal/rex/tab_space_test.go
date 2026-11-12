//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"reflect"
	"testing"

	"gorex/internal/layout"
)

// TestTabToSpace moves tabs between spaces with tab.to_space: a tab goes
// as it is, its splits, focus, zoom, name and floats, before a tab of the
// other space or at its end, that space's active tab staying; the space
// it leaves gives its active tab to the next, and goes with its last tab.
// A move to no space, to its own, before no tab, or of no tab changes
// nothing.
func TestTabToSpace(t *testing.T) {
	_, c := applyFixture(t)
	leaf := func(sid string) *layout.Node { return &layout.Node{SID: sid} }
	zoomed := layout.Tab{
		ID: "ta", Name: "editor", Root: &layout.Node{Ratio: 0.3, A: leaf("a1"), B: leaf("a2")}, Focus: 1, Zoom: true,
		Floats: []layout.Float{{Pane: leaf("a3"), X: 0.1, Y: 0.1, W: 0.4, H: 0.4, Hidden: true}},
	}
	st := layout.Store{Version: layout.Version, Windows: []string{"w", "v"}, Show: "w", Sessions: []layout.Workspace{
		{ID: "w", Name: "work", Layout: layout.Layout{Tabs: []layout.Tab{zoomed, {ID: "tb", Root: leaf("b")}}}},
		{ID: "v", Name: "play", Layout: layout.Layout{Tabs: []layout.Tab{{ID: "tc", Root: leaf("c")}, {ID: "td", Root: leaf("d")}}, Active: 1}},
	}}
	raw, _ := json.Marshal(st)
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	ids := func(w *layout.Workspace) (out []string) {
		for _, x := range w.Tabs {
			out = append(out, x.ID)
		}
		return out
	}

	// Nothing moves on a bad command.
	for _, x := range []struct {
		cmd  LayoutCommand
		code string
	}{
		{LayoutCommand{Op: "tab.to_space", Tab: "ta", ToSpace: "none"}, "space_not_found"},
		{LayoutCommand{Op: "tab.to_space", Tab: "ta", ToSpace: "w"}, "same_space"},
		{LayoutCommand{Op: "tab.to_space", Tab: "ta", ToSpace: "v", Target: "tb"}, "tab_not_found"},
		{LayoutCommand{Op: "tab.to_space", Tab: "none", ToSpace: "v"}, "tab_not_found"},
	} {
		if res, err := c.Command(x.cmd); err != nil || res.Code != x.code {
			t.Errorf("%+v: %+v %v, want %s", x.cmd, res, err, x.code)
		}
	}
	if got := storeOf(t, c); len(got.Sessions) != 2 || !reflect.DeepEqual(ids(&got.Sessions[0]), []string{"ta", "tb"}) {
		t.Fatalf("a refused move changed the layout: %+v", got.Sessions)
	}

	// Before a tab of the other space, named by a session of it.
	res := command(t, c, LayoutCommand{Op: "tab.to_space", Tab: "ta", Space: "w", ToSpace: "v", Target: "d"})
	if res.Tab != "ta" {
		t.Errorf("the answer names tab %q", res.Tab)
	}
	got := storeOf(t, c)
	w, v := got.Space("w"), got.Space("v")
	if !reflect.DeepEqual(ids(w), []string{"tb"}) || w.Active != 0 {
		t.Errorf("the space left has %v, active %d", ids(w), w.Active)
	}
	if !reflect.DeepEqual(ids(v), []string{"tc", "ta", "td"}) || v.Active != 2 {
		t.Fatalf("the space has %v, active %d; want tc ta td, td active", ids(v), v.Active)
	}
	moved := v.Tabs[1]
	if moved.Name != "editor" || !moved.Zoom || moved.Focus != 1 || len(moved.Floats) != 1 || !moved.Floats[0].Hidden ||
		moved.Root.A.SID != "a1" || moved.Root.B.SID != "a2" || moved.Floats[0].Pane.SID != "a3" || moved.Root.Ratio != 0.3 {
		t.Errorf("the tab changed as it moved: %+v", moved)
	}

	// The last tab, by its ID, to the end: its space goes.
	command(t, c, LayoutCommand{Op: "tab.to_space", Tab: "tb", ToSpace: "v"})
	got = storeOf(t, c)
	if got.Space("w") != nil || len(got.Sessions) != 1 || !reflect.DeepEqual(got.Windows, []string{"v"}) || got.Show != "" {
		t.Errorf("the emptied space stays: %+v, windows %v, show %q", got.Sessions, got.Windows, got.Show)
	}
	if v = got.Space("v"); !reflect.DeepEqual(ids(v), []string{"tc", "ta", "td", "tb"}) || v.Active != 2 {
		t.Errorf("the space has %v, active %d", ids(v), v.Active)
	}
}
