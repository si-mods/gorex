package rexapi

import (
	"encoding/json"
	"strings"
	"testing"

	"gorex/internal/layout"
)

// layoutOfStore writes a store as the layout the app keeps.
func layoutOfStore(t *testing.T, store layout.Store) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// split returns a split of a and b, vertical when asked.
func split(a, b *layout.Node, vertical bool, ratio float32) *layout.Node {
	return &layout.Node{A: a, B: b, Vertical: vertical, Ratio: ratio}
}

func pane(sid string) *layout.Node { return &layout.Node{SID: sid} }

// TestViewMapsLayout checks what a hand-written layout becomes: one
// session per workspace, one window per tab, and one block per local
// pane, with Rex's rects and labels.
func TestViewMapsLayout(t *testing.T) {
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "work", Name: "work",
		Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{
			{
				Name:  "editor",
				Root:  split(pane("s1"), split(pane("s2"), pane("s3"), true, 0.25), false, 0.5),
				Focus: 0,
			},
			{
				Name:  "two",
				Root:  split(pane("s4"), &layout.Node{URL: "https://example.com"}, false, 0.5),
				Focus: 1,
			},
		}},
	}}}
	mp := newMapper()
	m, err := mp.build(layoutOfStore(t, store), "host:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.sessions) != 1 || m.sessions[0].label != "work" {
		t.Fatalf("sessions: %+v", m.sessions)
	}
	s := m.sessions[0]
	v := s.view("host:test")
	if v.Revision != 1 || v.SessionID != sessionID("work") || v.ActiveWindowID != s.windows[0].id {
		t.Errorf("view head: %+v", v)
	}
	if len(v.Windows) != 2 {
		t.Fatalf("windows: %d", len(v.Windows))
	}
	one := v.Windows[0]
	if one.Label != "editor" || one.Layers[0].Kind != "tiled" || one.Layers[0].Bounds != [4]float64{0, 0, 1, 1} {
		t.Errorf("window one: %+v", one)
	}
	// The focused pane is s1, the first block.
	if one.FocusedBlockID != blockID("s1") {
		t.Errorf("focused %q, want %q", one.FocusedBlockID, blockID("s1"))
	}
	if len(one.Layers[0].Blocks) != 3 {
		t.Fatalf("blocks: %+v", one.Layers[0].Blocks)
	}
	for i, sid := range []string{"s1", "s2", "s3"} {
		b := one.Layers[0].Blocks[i]
		if b.BlockID != blockID(sid) || b.Label != "shell" || b.CreatorName != creatorName ||
			b.Flavor != flavorShell || b.HostID != "host:test" {
			t.Errorf("block %d: %+v", i, b)
		}
	}
	root := one.Layers[0].Layout
	if root.Rect != [4]float64{0, 0, 1, 1} || root.Split == nil || root.Split.Direction != "horizontal" || root.Split.Ratio != 0.5 {
		t.Fatalf("root: %+v", root)
	}
	if got := root.Split.Before.Rect; got != [4]float64{0, 0, 0.5, 1} {
		t.Errorf("before rect %v", got)
	}
	after := root.Split.After
	if after.Rect != [4]float64{0.5, 0, 0.5, 1} || after.Split == nil || after.Split.Direction != "vertical" || after.Split.Ratio != 0.25 {
		t.Fatalf("after: %+v", after)
	}
	if got := after.Split.Before.Rect; got != [4]float64{0.5, 0, 0.5, 0.25} {
		t.Errorf("nested before rect %v", got)
	}
	if got := after.Split.After.Rect; got != [4]float64{0.5, 0.25, 0.5, 0.75} {
		t.Errorf("nested after rect %v", got)
	}
	// The browser pane is left out, and its side takes the split's place;
	// the focus named it, so the window has no focused block.
	two := v.Windows[1]
	if two.FocusedBlockID != "" {
		t.Errorf("window two focused %q", two.FocusedBlockID)
	}
	if len(two.Layers[0].Blocks) != 1 || two.Layers[0].Blocks[0].BlockID != blockID("s4") {
		t.Errorf("window two blocks: %+v", two.Layers[0].Blocks)
	}
	if two.Layers[0].Layout.Split != nil || two.Layers[0].Layout.Rect != [4]float64{0, 0, 1, 1} {
		t.Errorf("window two layout: %+v", two.Layers[0].Layout)
	}
}

// TestIDsStay checks that a tab keeps its window ID across a reorder and
// a split, and that block and session IDs never change.
func TestIDsStay(t *testing.T) {
	one := split(pane("a"), pane("b"), false, 0.5)
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "w", Name: "work",
		Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{
			{Name: "one", Root: one},
			{Name: "two", Root: pane("c")},
		}},
	}}}
	mp := newMapper()
	raw := layoutOfStore(t, store)
	m1, err := mp.build(raw, "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	windowOne, windowTwo := m1.sessions[0].windows[0].id, m1.sessions[0].windows[1].id

	// Reordered: the same tabs, now the other way around.
	store.Sessions[0].Tabs[0], store.Sessions[0].Tabs[1] = store.Sessions[0].Tabs[1], store.Sessions[0].Tabs[0]
	m2, err := mp.build(layoutOfStore(t, store), "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.sessions[0].windows[0].id; got != windowTwo {
		t.Errorf("reorder: %q, want %q", got, windowTwo)
	}
	if got := m2.sessions[0].windows[1].id; got != windowOne {
		t.Errorf("reorder: %q, want %q", got, windowOne)
	}

	// Splitting the focused pane of the first tab keeps its window ID.
	store.Sessions[0].Tabs[0], store.Sessions[0].Tabs[1] = store.Sessions[0].Tabs[1], store.Sessions[0].Tabs[0]
	store.Sessions[0].Tabs[1].Root = split(pane("c"), pane("d"), true, 0.5)
	m3, err := mp.build(layoutOfStore(t, store), "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m3.sessions[0].windows[0].id; got != windowOne {
		t.Errorf("split: %q, want %q", got, windowOne)
	}
	if len(m3.sessions[0].windows[0].blocks) != 2 {
		t.Errorf("the split made %d blocks", len(m3.sessions[0].windows[0].blocks))
	}
	if got := blockIDOf(m1, "a"); got != blockIDOf(m3, "a") {
		t.Errorf("block a changed: %q to %q", got, blockIDOf(m3, "a"))
	}
	// The session ID is the same for another mapper.
	other := newMapper()
	m4, err := other.build(raw, "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m4.sessions[0].id != m1.sessions[0].id {
		t.Errorf("session ID changed: %q to %q", m1.sessions[0].id, m4.sessions[0].id)
	}
}

// blockIDOf returns the block ID of a session in a model.
func blockIDOf(m *model, sid string) string {
	for _, b := range m.blocks {
		if b.sid == sid {
			return b.id
		}
	}
	return ""
}

// TestSessionRevision checks that a session's revision grows with its
// view and stays when nothing changed.
func TestSessionRevision(t *testing.T) {
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "w", Name: "work",
		Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{{Name: "one", Root: pane("a")}}},
	}}}
	mp := newMapper()
	raw := layoutOfStore(t, store)
	m1, _ := mp.build(raw, "h", nil)
	if m1.sessions[0].revision != 1 {
		t.Fatalf("revision %d", m1.sessions[0].revision)
	}
	m2, _ := mp.build(raw, "h", nil)
	if m2.sessions[0].revision != 1 {
		t.Errorf("an unchanged view moved to revision %d", m2.sessions[0].revision)
	}
	store.Sessions[0].Tabs[0].Name = "renamed"
	m3, _ := mp.build(layoutOfStore(t, store), "h", nil)
	if m3.sessions[0].revision != 2 {
		t.Errorf("a changed view stayed at revision %d", m3.sessions[0].revision)
	}
	events := diffModels(m2, m3)
	if len(events) != 2 || events[0].typ != "window_label_changed" || events[1].typ != "session_view_changed" {
		t.Errorf("events: %+v", events)
	}
}

// TestUnavailablePanes checks that a tab with nothing this server serves
// is not a window, and that a remote pane in a split leaves its side's
// place to the other.
func TestUnavailablePanes(t *testing.T) {
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{
			{Name: "far", Root: &layout.Node{SID: "f", Host: "h1"}},
			{Name: "mixed", Root: split(pane("l"), &layout.Node{SID: "f", Unavailable: true}, false, 0.5)},
		}},
	}}}
	m, err := newMapper().build(layoutOfStore(t, store), "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := m.sessions[0]
	if len(s.windows) != 1 || s.windows[0].label != "mixed" {
		t.Fatalf("windows: %+v", s.windows)
	}
	if s.active != s.windows[0].id {
		t.Errorf("active %q", s.active)
	}
	layer := s.windows[0].layout
	if layer.Split != nil || layer.Rect != [4]float64{0, 0, 1, 1} {
		t.Errorf("the kept side did not take the split's place: %+v", layer)
	}
}

// TestEmptyWorkspace checks the label fallback and the empty windows.
func TestEmptyWorkspace(t *testing.T) {
	store := layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: ""}}}
	m, _ := newMapper().build(layoutOfStore(t, store), "h", nil)
	s := m.sessions[0]
	if s.label == "" || !strings.HasPrefix(s.label, "Session ") {
		t.Errorf("label %q", s.label)
	}
	v := s.view("h")
	if len(v.Windows) != 0 || v.ActiveWindowID != "" {
		t.Errorf("view %+v", v)
	}
}

// TestViewMapsFloats checks that GoRex's floating panes become Rex's
// floating layers, after the tiled one and lowest first, that a hidden
// one is left out, and that the focus on a float names its block.
func TestViewMapsFloats(t *testing.T) {
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "work", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{{
			Root: split(pane("s1"), pane("s2"), false, 0.5),
			Floats: []layout.Float{
				{Pane: pane("f1"), X: 0.1, Y: 0.2, W: 0.5, H: 0.4},
				{Pane: pane("f2"), X: 0, Y: 0, W: 1, H: 1, Hidden: true},
				{Pane: pane("f3"), X: 0.25, Y: 0.25, W: 0.5, H: 0.5, Zoom: true},
			},
			FocusFloat: 3,
		}}},
	}}}
	m, err := newMapper().build(layoutOfStore(t, store), "host:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := m.sessions[0]
	v := s.view("host:test")
	layers := v.Windows[0].Layers
	if len(layers) != 3 {
		t.Fatalf("%d layers, want the tiled one and two floats: %+v", len(layers), layers)
	}
	if layers[0].Kind != "tiled" || len(layers[0].Blocks) != 2 {
		t.Errorf("tiled layer: %+v", layers[0])
	}
	f1, f3 := blockID("f1"), blockID("f3")
	if l := layers[1]; l.Kind != "floating" || l.Bounds != [4]float64{float64(float32(0.1)), float64(float32(0.2)), 0.5, float64(float32(0.4))} ||
		len(l.Blocks) != 1 || l.Blocks[0].BlockID != f1 || l.Layout == nil || l.Layout.BlockID != f1 {
		t.Errorf("first float: %+v", l)
	}
	if l := layers[2]; l.Kind != "floating" || l.Blocks[0].BlockID != f3 || l.LayerID == layers[1].LayerID {
		t.Errorf("second float: %+v", l)
	}
	if v.Windows[0].FocusedBlockID != f3 || v.Windows[0].ZoomedBlockID != f3 {
		t.Errorf("focus %q zoom %q, want the zoomed float %q", v.Windows[0].FocusedBlockID, v.Windows[0].ZoomedBlockID, f3)
	}
	if m.blocks[blockID("f2")] != nil {
		t.Error("a hidden float is a block")
	}
	if m.blocks[f1] == nil || m.blocks[f1].window != s.windows[0].id {
		t.Error("a float's block is not the window's")
	}
}
