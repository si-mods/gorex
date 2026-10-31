package rexapi

import (
	"testing"
	"time"

	"gorex/internal/layout"
)

// nextEvent returns the next event's seq and inner payload.
func nextEvent(t *testing.T, c *testConn) (uint64, map[string]any) {
	t.Helper()
	m := c.nextType("event")
	seq, ok := m["seq"].(float64)
	if !ok {
		t.Fatalf("no seq: %v", m)
	}
	p, ok := m["payload"].(map[string]any)
	if !ok {
		t.Fatalf("no payload: %v", m)
	}
	return uint64(seq), p
}

// sessionEventOf returns a session_event's session, type and fields.
func sessionEventOf(t *testing.T, ev map[string]any) (string, string, map[string]any) {
	t.Helper()
	if ev["type"] != "session_event" {
		t.Fatalf("not a session event: %v", ev)
	}
	body := ev["payload"].(map[string]any)
	inner := body["event"].(map[string]any)
	fields, _ := inner["payload"].(map[string]any)
	return body["session_id"].(string), inner["type"].(string), fields
}

// expectSequence checks that a seq follows the one before it.
func expectSequence(t *testing.T, last *uint64, seq uint64) {
	t.Helper()
	if last != nil && seq != *last+1 {
		t.Errorf("seq %d, want %d", seq, *last+1)
	}
	*last = seq
}

// expectNoMessage fails if a message arrives within a moment.
func expectNoMessage(t *testing.T, c *testConn) {
	t.Helper()
	select {
	case m := <-c.msgs:
		t.Errorf("unexpected message: %v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestEvents checks the events an attached connection receives and the
// gap-free seq the doc promises.
func TestEvents(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	a := f.dialControl()
	a.hello(apiRevision)
	ids := sessionIDs(t, a)
	boot := result(t, a.request("boot", "session.bootstrap", map[string]any{
		"event_scope": "session", "preferred_session_id": ids["work"],
	}))
	if boot["selected_session"] == nil {
		t.Fatal("bootstrap chose no session")
	}
	// A second connection that only says hello hears no session event.
	b := f.dialControl()
	b.hello(apiRevision)

	var last uint64
	seq, ev := nextEvent(t, a)
	expectSequence(t, &last, seq)
	sid, typ, _ := sessionEventOf(t, ev)
	if typ != "client_connected" || sid != ids["work"] {
		t.Fatalf("first event: %s %s", typ, sid)
	}

	// A window's label change sends its event before the view's.
	f.setLayout(layoutWithTabName(t, "work", "renamed"))
	seq, ev = nextEvent(t, a)
	expectSequence(t, &last, seq)
	_, typ, fields := sessionEventOf(t, ev)
	if typ != "window_label_changed" || fields["label"] != "renamed" {
		t.Fatalf("label event: %v", ev)
	}
	seq, ev = nextEvent(t, a)
	expectSequence(t, &last, seq)
	_, typ, fields = sessionEventOf(t, ev)
	if typ != "session_view_changed" || fields["revision"].(float64) < 2 {
		t.Fatalf("view event: %v", ev)
	}
	reply := a.request("view", "session.view", map[string]any{"session_id": ids["work"]})
	view := result(t, reply)
	if view["revision"].(float64) < fields["revision"].(float64) {
		t.Errorf("view %v is older than event %v", view["revision"], fields["revision"])
	}
	if respSeq := uint64(reply["seq"].(float64)); respSeq > last {
		t.Errorf("response seq %d, after event %d", respSeq, last)
	}
	expectNoMessage(t, b)

	// A client that attaches sends client_connected, and one that leaves
	// sends client_disconnected.
	b.request("attach", "session.attach", map[string]any{"session_id": ids["work"]})
	seq, ev = nextEvent(t, a)
	expectSequence(t, &last, seq)
	if _, typ, _ := sessionEventOf(t, ev); typ != "client_connected" {
		t.Fatalf("b's attach: %v", ev)
	}
	b.request("detach", "session.detach", map[string]any{"session_id": ids["work"]})
	seq, ev = nextEvent(t, a)
	expectSequence(t, &last, seq)
	if _, typ, _ := sessionEventOf(t, ev); typ != "client_disconnected" {
		t.Fatalf("b's detach: %v", ev)
	}

	// A new workspace is a session_created, then its window and block.
	store := layoutWithTabName(t, "work", "renamed")
	store.Sessions = append(store.Sessions, layout.Workspace{
		ID: "new", Name: "new",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "first", Root: pane("s7")}}},
	})
	f.setLayout(store)
	seq, ev = nextEvent(t, a)
	expectSequence(t, &last, seq)
	if ev["type"] != "session_created" {
		t.Fatalf("new session: %v", ev)
	}
	if s := ev["payload"].(map[string]any)["session"].(map[string]any); s["label"] != "new" {
		t.Errorf("session_created: %v", ev)
	}
	var types []string
	for range 3 {
		seq, ev = nextEvent(t, a)
		expectSequence(t, &last, seq)
		_, typ, _ = sessionEventOf(t, ev)
		types = append(types, typ)
	}
	if types[0] != "window_created" || types[1] != "block_created" || types[2] != "session_view_changed" {
		t.Errorf("new session events: %v", types)
	}

	// An active window change has its own event.
	store.Sessions[0].Active = 1
	f.setLayout(store)
	_, ev = nextEvent(t, a)
	if _, typ, _ := sessionEventOf(t, ev); typ != "active_window_changed" {
		t.Errorf("active event: %v", ev)
	}
}

// TestAttachAllScope checks that a connection with only session.attach_all
// hears every session's events, including session_created, while one that
// attached to nothing hears none.
func TestAttachAllScope(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	watcher := f.dialControl()
	watcher.hello(apiRevision)
	callChecked(t, watcher, "session.attach_all", map[string]any{"event_scope": "session"})
	other := f.dialControl()
	other.hello(apiRevision)

	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "watch", Name: "watch",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "one", Root: pane("w1")}}},
	}}}
	f.setLayout(store)
	ev := watcher.nextType("event")
	if ev["payload"].(map[string]any)["type"] != "session_created" {
		t.Errorf("watcher: %v", ev)
	}
	expectNoMessage(t, other)

	// A connection told to hear all events also hears them, and can use a
	// session it never attached to.
	all := f.dialControl()
	all.hello(apiRevision)
	callChecked(t, all, "session.attach_all", map[string]any{"event_scope": "all"})
	view := callChecked(t, all, "session.view", map[string]any{"session_id": sessionID("watch")})
	if view["label"] != "watch" {
		t.Errorf("view: %v", view)
	}
	// But data.connect needs session.attach, not attach_all.
	e := failure(t, all.request("dc", "data.connect", map[string]any{
		"session_id": sessionID("watch"), "block_id": blockID("w1"), "data_ref_id": "terminal",
	}))
	if e["code"] != "conflict" {
		t.Errorf("data.connect on attach_all: %v", e)
	}
}
