package rexapi

import (
	"testing"

	"gorex/internal/layout"
)

// TestStructuralEvents checks the events of a label change, a window and
// block closing, and a session being destroyed.
func TestStructuralEvents(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	ids := sessionIDs(t, c)
	callChecked(t, c, "session.bootstrap", map[string]any{
		"event_scope": "session", "preferred_session_id": ids["work"],
	})
	// Bootstrap sent client_connected first.
	var last uint64
	seq, ev := nextEvent(t, c)
	expectSequence(t, &last, seq)
	if _, typ, _ := sessionEventOf(t, ev); typ != "client_connected" {
		t.Fatalf("first event: %v", ev)
	}

	// The session's own label.
	store := standardLayout()
	store.Sessions[0].Name = "renamed-work"
	f.setLayout(store)
	seq, ev = nextEvent(t, c)
	expectSequence(t, &last, seq)
	_, typ, fields := sessionEventOf(t, ev)
	if typ != "session_label_changed" || fields["label"] != "renamed-work" {
		t.Errorf("label: %v", ev)
	}
	seq, ev = nextEvent(t, c)
	expectSequence(t, &last, seq)
	if _, typ, _ := sessionEventOf(t, ev); typ != "session_view_changed" {
		t.Errorf("view after label: %v", ev)
	}

	// Closing the second tab closes its block and the window.
	store = standardLayout()
	store.Sessions[0].Name = "renamed-work"
	store.Sessions[0].Tabs = store.Sessions[0].Tabs[:1]
	f.setLayout(store)
	types := map[string]bool{}
	for range 3 {
		seq, ev = nextEvent(t, c)
		expectSequence(t, &last, seq)
		_, typ, fields = sessionEventOf(t, ev)
		types[typ] = true
		if typ == "block_closed" && fields["block_id"] != blockID("s3") {
			t.Errorf("closed block %v, want %v", fields["block_id"], blockID("s3"))
		}
	}
	if !types["block_closed"] || !types["window_closed"] || !types["session_view_changed"] {
		t.Errorf("close events: %v", types)
	}

	// Destroying the other workspace sends its session_destroyed, which
	// an attach_all connection receives.
	store.Sessions = store.Sessions[:1]
	f.setLayout(store)
	seq, ev = nextEvent(t, c)
	expectSequence(t, &last, seq)
	if ev["type"] != "session_destroyed" {
		t.Fatalf("destroyed: %v", ev)
	}
	if got := ev["payload"].(map[string]any)["session_id"]; got != ids["side"] {
		t.Errorf("destroyed %v, want %v", got, ids["side"])
	}
}

// TestBlockCreated checks block_created when a new pane joins.
func TestBlockCreated(t *testing.T) {
	f := startFixture(t)
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "w", Name: "w",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "one", Root: pane("a")}}},
	}}}
	f.setLayout(store)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.bootstrap", map[string]any{
		"event_scope": "session", "preferred_session_id": sessionID("w"),
	})
	var last uint64
	seq, _ := nextEvent(t, c) // client_connected
	expectSequence(t, &last, seq)
	store.Sessions = []layout.Workspace{{
		ID: "w", Name: "w",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "one", Root: split(pane("a"), pane("b"), true, 0.5)}}},
	}}
	f.setLayout(store)
	seq, ev := nextEvent(t, c)
	expectSequence(t, &last, seq)
	_, typ, fields := sessionEventOf(t, ev)
	if typ != "block_created" || fields["block_id"] != blockID("b") {
		t.Errorf("block_created: %v", ev)
	}
	seq, ev = nextEvent(t, c)
	expectSequence(t, &last, seq)
	if _, typ, _ := sessionEventOf(t, ev); typ != "session_view_changed" {
		t.Errorf("view: %v", ev)
	}
}
