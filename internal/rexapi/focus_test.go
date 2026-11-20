package rexapi

import "testing"

// twoPanes starts a session with two panes in one window and returns the
// connection, the session, the first pane and the second.
func twoPanes(t *testing.T, f *fixture) (*testConn, string, string, string) {
	t.Helper()
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	created := callChecked(t, c, "session.create", map[string]any{
		"label": "focus",
		"initial_windows": []any{
			map[string]any{"window_label": "one", "layout": splitLayout("horizontal", 0.5)},
		},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	ids := blockIDsOf(t, viewOf(t, c, sid), 0)
	if len(ids) < 2 {
		t.Fatalf("panes = %v", ids)
	}
	return c, sid, ids[0], ids[1]
}

// TestFocusHistoryReverse walks back through two panes, and forward again
// without a cursor.
func TestFocusHistoryReverse(t *testing.T) {
	f := startFixture(t)
	c, sid, a, b := twoPanes(t, f)
	// A third pane, then A, B, C, A: three places, the newest one
	// repeated.
	split := callChecked(t, c, "session.new_split", map[string]any{
		"session_id": sid, "anchor_id": a, "direction": "horizontal", "side": "after",
		"layout": blockLayout(),
	})
	cc := split["block_ids"].([]any)[0].(string)
	for _, id := range []string{a, b, cc, a} {
		callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": id})
	}

	back := result(t, c.request("r1", "focus_history.reverse", map[string]any{"session_id": sid}))
	if back["reversed"] != true || back["block_id"] != cc || back["session_id"] != sid {
		t.Fatalf("reverse = %v", back)
	}
	cursor := back["cursor"].(float64)

	// Without a cursor, the next call goes forward again, to where the
	// first one started.
	fwd := result(t, c.request("r2", "focus_history.reverse", map[string]any{"session_id": sid}))
	if fwd["reversed"] != true || fwd["block_id"] != a {
		t.Fatalf("second reverse = %v", fwd)
	}

	// With the first cursor, the walk skips what it already visited and
	// goes past it, to the place before.
	again := result(t, c.request("r3", "focus_history.reverse", map[string]any{"session_id": sid, "cursor": cursor}))
	if again["reversed"] != true || again["block_id"] != b {
		t.Fatalf("cursor reverse = %v", again)
	}
}

// TestFocusHistoryFilters: a window filter finds a pane of that window,
// and a filter with nothing left answers reversed false.
func TestFocusHistoryFilters(t *testing.T) {
	f := startFixture(t)
	c, sid, a, b := twoPanes(t, f)
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": a})
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": b})
	view := viewOf(t, c, sid)
	window := view["windows"].([]any)[0].(map[string]any)["window_id"].(string)

	inWindow := result(t, c.request("f1", "focus_history.reverse", map[string]any{"session_id": sid, "window_id": window}))
	if inWindow["reversed"] != true || inWindow["block_id"] != a {
		t.Fatalf("window filter = %v", inWindow)
	}
	other := result(t, c.request("f2", "focus_history.reverse", map[string]any{"session_id": sid, "not_window_id": window}))
	if other["reversed"] != false {
		t.Fatalf("not_window_id = %v", other)
	}
}

// TestFocusHistoryNeedsAttachment: a session_id needs the connection to
// hear of the session.
func TestFocusHistoryNeedsAttachment(t *testing.T) {
	f := startFixture(t)
	_, sid, _, _ := twoPanes(t, f)
	c := f.dialControl()
	c.hello(19)
	if e := failure(t, c.request("a", "focus_history.reverse", map[string]any{"session_id": sid})); e["code"] != "conflict" {
		t.Fatalf("unattached: %v", e)
	}
	if e := failure(t, c.request("b", "focus_history.reverse", map[string]any{"session_id": "session:nope"})); e["code"] != "session_not_found" {
		t.Fatalf("unknown session: %v", e)
	}
}

// TestFocusHistorySeparatePrincipals: another client's history is its own.
func TestFocusHistorySeparatePrincipals(t *testing.T) {
	f := startFixture(t)
	c, sid, a, b := twoPanes(t, f)
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": a})
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": b})
	other := f.dialControl()
	other.hello(19)
	callChecked(t, other, "session.attach", map[string]any{"session_id": sid})
	if r := result(t, other.request("r", "focus_history.reverse", map[string]any{"session_id": sid})); r["reversed"] != false {
		t.Fatalf("another principal walked a history: %v", r)
	}
}

// TestFocusReturnsOnClose: closing the focused pane returns focus to the
// pane the person was in before.
func TestFocusReturnsOnClose(t *testing.T) {
	f := startFixture(t)
	c, sid, a, b := twoPanes(t, f)
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": a})
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": b})
	callChecked(t, c, "block.close", map[string]any{"session_id": sid, "block_id": b})
	view := viewOf(t, c, sid)
	window := view["windows"].([]any)[0].(map[string]any)
	if window["focused_block_id"] != a {
		t.Fatalf("focus after close = %v, want %s", window["focused_block_id"], a)
	}
}
