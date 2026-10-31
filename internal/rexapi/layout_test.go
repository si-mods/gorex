package rexapi

import (
	"encoding/json"
	"strings"
	"testing"

	"gorex/internal/layout"
)

// blockLayout is a layout node of one shell block.
func blockLayout() map[string]any {
	return map[string]any{"block": map[string]any{"flavor": flavorShell}}
}

// splitLayout is a layout node of two blocks in a split.
func splitLayout(direction string, ratio float64) map[string]any {
	return map[string]any{"split": map[string]any{
		"direction": direction, "ratio": ratio,
		"before": blockLayout(), "after": blockLayout(),
	}}
}

// blockIDsOf returns the block IDs of a view's first window.
func blockIDsOf(t *testing.T, view map[string]any, window int) []string {
	t.Helper()
	windows := view["windows"].([]any)
	if window >= len(windows) {
		t.Fatalf("no window %d in %v", window, view)
	}
	w := windows[window].(map[string]any)
	layer := w["layers"].([]any)[0].(map[string]any)
	var out []string
	for _, raw := range layer["blocks"].([]any) {
		out = append(out, raw.(map[string]any)["block_id"].(string))
	}
	return out
}

// windowOf returns a view's window.
func windowOf(t *testing.T, view map[string]any, at int) map[string]any {
	t.Helper()
	return view["windows"].([]any)[at].(map[string]any)
}

// TestLayoutWithoutApp: a change of the layout needs the GoRex window.
func TestLayoutWithoutApp(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work"}}})
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sessionID("w")})
	for _, method := range []string{"session.create", "session.new_window", "session.destroy"} {
		payload := map[string]any{}
		if method != "session.create" {
			payload["session_id"] = sessionID("w")
			payload["layout"] = splitLayout("horizontal", 0.5)
		}
		e := failure(t, c.request("x"+method, method, payload))
		if e["code"] != "conflict" || !strings.Contains(e["message"].(string), "Open the GoRex window on") {
			t.Errorf("%s: %v", method, e)
		}
	}
}

// TestSessionCreateLayout: session.create makes a workspace with its
// windows, and the view shows them.
func TestSessionCreateLayout(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	startApp(f)
	c := f.dialControl()
	c.hello(apiRevision)
	client := welcomeID(t, c.welcome)
	res := callChecked(t, c, "session.create", map[string]any{
		"label": "work",
		"initial_windows": []any{
			map[string]any{"window_label": "editor", "layout": splitLayout("horizontal", 0.25)},
			map[string]any{"window_label": "logs", "layout": blockLayout()},
		},
	})
	sid := res["session_id"].(string)
	created := res["initial_windows"].([]any)
	if len(created) != 2 {
		t.Fatalf("initial windows: %v", created)
	}
	first := created[0].(map[string]any)
	if len(first["block_ids"].([]any)) != 2 {
		t.Errorf("first window blocks: %v", first)
	}
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	view := result(t, c.request("v", "session.view", map[string]any{"session_id": sid}))
	if view["label"] != "work" {
		t.Errorf("label: %v", view["label"])
	}
	if len(view["windows"].([]any)) != 2 {
		t.Fatalf("windows: %v", view["windows"])
	}
	if windowOf(t, view, 0)["label"] != "editor" || windowOf(t, view, 1)["label"] != "logs" {
		t.Errorf("window labels: %v", view["windows"])
	}
	w0 := windowOf(t, view, 0)
	if w0["window_id"] != first["window_id"] {
		t.Errorf("window ID %v, want %v", w0["window_id"], first["window_id"])
	}
	// The split is where the request put it, and the first block has the
	// focus.
	layer := w0["layers"].([]any)[0].(map[string]any)
	root := layer["layout"].(map[string]any)
	split := root["split"].(map[string]any)
	if split["direction"] != "horizontal" {
		t.Errorf("split: %v", split)
	}
	if ratio := split["ratio"].(float64); ratio < 0.2 || ratio > 0.3 {
		t.Errorf("ratio %v, want about 0.25", ratio)
	}
	blocks := blockIDsOf(t, view, 0)
	if w0["focused_block_id"] != blocks[0] {
		t.Errorf("focused %v, want %v", w0["focused_block_id"], blocks[0])
	}
	// The creator owns the blocks.
	list := callChecked(t, c, "session.list_blocks", map[string]any{"session_id": sid})
	if len(list["blocks"].([]any)) != 3 {
		t.Errorf("blocks: %v", list)
	}
	for _, raw := range list["blocks"].([]any) {
		b := raw.(map[string]any)
		if b["owner"] != client || b["placement"] != "placed" || b["creator_name"] != creatorName {
			t.Errorf("block: %v", b)
		}
	}
	// describe_block tells the block's methods.
	described := callChecked(t, c, "session.describe_block", map[string]any{
		"session_id": sid, "block_id": blocks[0],
	})
	api := described["api"].(map[string]any)
	if _, ok := api["methods"].(map[string]any)["write"]; !ok {
		t.Errorf("methods: %v", api["methods"])
	}
	if _, ok := api["events"].(map[string]any)["bell"]; !ok {
		t.Errorf("events: %v", api["events"])
	}
	// session.help describes the session's methods and creators.
	help := result(t, c.request("help", "session.help", map[string]any{"session_id": sid}))
	if _, ok := help["http"]; !ok {
		t.Error("session.help has no http")
	}
	var hasList bool
	for _, raw := range help["methods"].([]any) {
		if raw.(map[string]any)["name"] == "session.list_blocks" {
			hasList = true
		}
	}
	if !hasList {
		t.Error("session.help has no session.list_blocks")
	}
	// destroy closes it.
	callChecked(t, c, "session.destroy", map[string]any{"session_id": sid})
	callChecked(t, c, "session.list", nil)
	if e := failure(t, c.request("gone", "session.view", map[string]any{"session_id": sid})); e["code"] != "session_not_found" {
		t.Errorf("after destroy: %v", e)
	}
}

// emptyStore is a layout of no workspaces.
func emptyStore() layout.Store { return layout.Store{} }

// windowIDsOf returns a view's window IDs in order.
func windowIDsOf(t *testing.T, view map[string]any) []string {
	t.Helper()
	var out []string
	for _, raw := range view["windows"].([]any) {
		out = append(out, raw.(map[string]any)["window_id"].(string))
	}
	return out
}

// viewOf reads a session's view.
func viewOf(t *testing.T, c *testConn, sid string) map[string]any {
	t.Helper()
	return callChecked(t, c, "session.view", map[string]any{"session_id": sid})
}

// TestLayoutChanges makes the changes of the layout the API documents,
// through the fake window, and checks the view after each.
func TestLayoutChanges(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	startApp(f)
	c := f.dialControl()
	c.hello(apiRevision)
	created := callChecked(t, c, "session.create", map[string]any{
		"label": "layout",
		"initial_windows": []any{
			map[string]any{"window_label": "one", "layout": blockLayout()},
		},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	view := viewOf(t, c, sid)
	w1 := windowIDsOf(t, view)[0]
	b1 := blockIDsOf(t, view, 0)[0]

	// A new window with two blocks; the first has the focus.
	res := callChecked(t, c, "session.new_window", map[string]any{
		"session_id": sid, "window_label": "two",
		"layout": splitLayout("vertical", 0.5),
	})
	ids := res["block_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("new window blocks: %v", res)
	}
	view = viewOf(t, c, sid)
	if len(windowIDsOf(t, view)) != 2 || view["active_window_id"] != res["window_id"] {
		t.Fatalf("after new_window: %v", view)
	}
	if windowOf(t, view, 1)["focused_block_id"] != ids[0] {
		t.Errorf("new window focus: %v", windowOf(t, view, 1)["focused_block_id"])
	}
	w2 := res["window_id"].(string)
	b2 := ids[0].(string)

	// A new split beside the focused block, after it.
	split := callChecked(t, c, "session.new_split", map[string]any{
		"session_id": sid, "direction": "horizontal", "side": "after",
		"layout": blockLayout(),
	})
	if split["anchor_id"] != b2 || len(split["split_ids"].([]any)) == 0 {
		t.Errorf("new_split: %v", split)
	}
	newBlock := split["block_ids"].([]any)[0].(string)
	view = viewOf(t, c, sid)
	if !contains(blockIDsOf(t, view, 1), newBlock) {
		t.Errorf("the new block is not in the view: %v", view)
	}
	splitID := split["split_ids"].([]any)[0].(string)
	raw := windowOf(t, view, 1)["layers"].([]any)[0].(map[string]any)["layout"].(map[string]any)
	if raw["split"] == nil {
		t.Errorf("the window has no split: %v", raw)
	}
	if !strings.Contains(mustJSON(raw), splitID) {
		t.Errorf("split %s is not in the view: %v", splitID, raw)
	}

	// A block created before another one comes first in the layout.
	before := callChecked(t, c, "session.new_split", map[string]any{
		"session_id": sid, "anchor_id": b2, "direction": "vertical", "side": "before",
		"layout": blockLayout(),
	})
	view = viewOf(t, c, sid)
	order := blockIDsOf(t, view, 1)
	firstNew := before["block_ids"].([]any)[0].(string)
	if !contains(order, firstNew) || !contains(order, b2) {
		t.Fatalf("order: %v", order)
	}
	if indexOf(order, firstNew) > indexOf(order, b2) {
		t.Errorf("%s does not come before %s in %v", firstNew, b2, order)
	}

	// Focus moves.
	callChecked(t, c, "session.focus_block", map[string]any{"session_id": sid, "block_id": firstNew})
	if view = viewOf(t, c, sid); windowOf(t, view, 1)["focused_block_id"] != firstNew {
		t.Errorf("focus_block: %v", view)
	}
	callChecked(t, c, "session.focus_next_pane", map[string]any{"session_id": sid})
	callChecked(t, c, "session.focus_previous_pane", map[string]any{"session_id": sid})
	callChecked(t, c, "session.focus_direction", map[string]any{"session_id": sid, "direction": "right"})
	callChecked(t, c, "session.focus_window", map[string]any{"session_id": sid, "window_id": w1})
	if view = viewOf(t, c, sid); view["active_window_id"] != w1 {
		t.Errorf("focus_window: %v", view["active_window_id"])
	}
	callChecked(t, c, "session.focus_next_window", map[string]any{"session_id": sid})
	if view = viewOf(t, c, sid); view["active_window_id"] != w2 {
		t.Errorf("focus_next_window: %v", view["active_window_id"])
	}
	callChecked(t, c, "session.focus_previous_window", map[string]any{"session_id": sid})
	if view = viewOf(t, c, sid); view["active_window_id"] != w1 {
		t.Errorf("focus_previous_window: %v", view["active_window_id"])
	}

	// Zoom.
	zoom := callChecked(t, c, "session.zoom_block", map[string]any{"session_id": sid, "block_id": b2})
	if zoom["zoomed"] != true || zoom["block_id"] != b2 {
		t.Errorf("zoom: %v", zoom)
	}
	if view = viewOf(t, c, sid); windowOf(t, view, 1)["zoomed_block_id"] != b2 {
		t.Errorf("zoom view: %v", windowOf(t, view, 1))
	}
	zoom = callChecked(t, c, "session.zoom_block", map[string]any{"session_id": sid, "block_id": b2, "zoom": false})
	if zoom["zoomed"] != false {
		t.Errorf("unzoom: %v", zoom)
	}

	// Labels.
	callChecked(t, c, "session.set_label", map[string]any{"session_id": sid, "label": "ready"})
	callChecked(t, c, "session.set_window_label", map[string]any{"session_id": sid, "window_id": w2, "label": "right"})
	callChecked(t, c, "session.set_block_label", map[string]any{"session_id": sid, "block_id": b2, "label": "shell two"})
	view = viewOf(t, c, sid)
	if view["label"] != "ready" || windowOf(t, view, 1)["label"] != "right" {
		t.Errorf("labels: %v", view)
	}
	for _, raw := range windowOf(t, view, 1)["layers"].([]any)[0].(map[string]any)["blocks"].([]any) {
		b := raw.(map[string]any)
		if b["block_id"] == b2 && b["label"] != "shell two" {
			t.Errorf("block label: %v", b)
		}
	}

	// A pane's border moves.
	resize := callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "vertical", "ratio": 0.3,
		"before_block_id": firstNew, "after_block_id": b2,
	})
	if resize["revision"] == nil {
		t.Errorf("resize_pane: %v", resize)
	}
	// Swapping two blocks of one window.
	callChecked(t, c, "session.swap_blocks", map[string]any{
		"session_id": sid, "block_id": b2, "other_block_id": newBlock,
	})
	// Moving one into the other window.
	move := callChecked(t, c, "session.move_block", map[string]any{
		"session_id": sid, "block_id": newBlock, "anchor_block_id": b1,
		"direction": "horizontal", "side": "after", "ratio": 0.5,
	})
	if move["created_split_id"] == "" {
		t.Errorf("move_block: %v", move)
	}
	view = viewOf(t, c, sid)
	if !contains(blockIDsOf(t, view, 0), newBlock) {
		t.Errorf("moved block not in the first window: %v", blockIDsOf(t, view, 0))
	}

	// The window order.
	callChecked(t, c, "session.move_window", map[string]any{
		"session_id": sid, "window_id": w2, "before_window_id": w1,
	})
	view = viewOf(t, c, sid)
	if ids := windowIDsOf(t, view); ids[0] != w2 || ids[1] != w1 {
		t.Errorf("window order: %v", ids)
	}
	callChecked(t, c, "session.move_window", map[string]any{"session_id": sid, "window_id": w2})
	view = viewOf(t, c, sid)
	if ids := windowIDsOf(t, view); ids[len(ids)-1] != w2 {
		t.Errorf("window order after moving to the end: %v", ids)
	}

	// Bad arguments.
	for _, tc := range []struct {
		method  string
		payload map[string]any
		code    string
	}{
		{"session.new_split", map[string]any{"session_id": sid, "anchor_id": b2, "window_id": w1, "direction": "horizontal", "side": "after", "layout": blockLayout()}, "bad_request"},
		{"session.new_split", map[string]any{"session_id": sid, "direction": "sideways", "side": "after", "layout": blockLayout()}, "bad_request"},
		{"session.new_split", map[string]any{"session_id": sid, "direction": "horizontal", "side": "after", "ratio": 2, "layout": blockLayout()}, "bad_request"},
		{"session.new_window", map[string]any{"session_id": sid}, "bad_request"},
		{"session.new_window", map[string]any{"session_id": sid, "layout": map[string]any{"block": map[string]any{"flavor": "com.superlogical.other"}}}, "not_found"},
		{"session.zoom_block", map[string]any{"session_id": sid, "block_id": b2, "window_id": w1}, "bad_request"},
		{"session.zoom_block", map[string]any{"session_id": sid, "block_id": "block:nope"}, "not_found"},
		{"session.focus_block", map[string]any{"session_id": sid, "block_id": "block:nope"}, "not_found"},
		{"session.focus_direction", map[string]any{"session_id": sid, "direction": "nowhere"}, "bad_request"},
		{"session.set_block_label", map[string]any{"session_id": sid, "label": "x"}, "bad_request"},
		{"session.set_window_label", map[string]any{"session_id": sid, "window_id": "window:nope", "label": "x"}, "not_found"},
		{"session.swap_blocks", map[string]any{"session_id": sid, "block_id": b2, "other_block_id": b2}, "bad_request"},
		{"session.resize_pane", map[string]any{"session_id": sid, "direction": "horizontal", "ratio": 0.5, "before_block_id": b2, "after_block_id": b2}, "bad_request"},
		{"session.resize_pane", map[string]any{"session_id": sid, "direction": "horizontal", "ratio": 0, "before_block_id": b2, "after_block_id": newBlock}, "bad_request"},
		{"session.move_window", map[string]any{"session_id": sid, "window_id": w1, "before_window_id": w1}, "bad_request"},
		{"session.close_window", map[string]any{"session_id": sid, "window_id": "window:nope"}, "not_found"},
		{"block.close", map[string]any{"session_id": sid}, "bad_request"},
		{"block.close", map[string]any{"session_id": sid, "block_id": b2, "window_id": w1}, "bad_request"},
	} {
		e := failure(t, c.request("bad"+tc.method+tc.code, tc.method, tc.payload))
		if e["code"] != tc.code {
			t.Errorf("%s %v: %v, want %s", tc.method, tc.payload, e, tc.code)
		}
	}

	// Closing a window, a block, and the session.
	res = callChecked(t, c, "block.close", map[string]any{"session_id": sid, "block_id": newBlock})
	if res["block_id"] != newBlock {
		t.Errorf("block.close: %v", res)
	}
	view = viewOf(t, c, sid)
	if contains(blockIDsOf(t, view, 0), newBlock) {
		t.Errorf("the block is still in the view: %v", view)
	}
	callChecked(t, c, "session.close_window", map[string]any{"session_id": sid, "window_id": w2})
	view = viewOf(t, c, sid)
	if contains(windowIDsOf(t, view), w2) {
		t.Errorf("the window is still in the view: %v", view)
	}
	// keep_last keeps the only block of a window.
	if e := failure(t, c.request("keep", "block.close", map[string]any{
		"session_id": sid, "block_id": b1, "keep_last": true,
	})); e["code"] != "bad_request" {
		t.Errorf("keep_last: %v", e)
	}
	callChecked(t, c, "session.destroy", map[string]any{"session_id": sid})
}

// contains tells whether ids holds id.
func contains(ids []string, id string) bool { return indexOf(ids, id) >= 0 }

// indexOf returns the place of id in ids, -1 for none.
func indexOf(ids []string, id string) int {
	for i, other := range ids {
		if other == id {
			return i
		}
	}
	return -1
}

// mustJSON writes v as JSON for a test's messages.
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestNewWindowWithoutFocus: a window made without the focus leaves the
// active one as it was, with its first block focused.
func TestNewWindowWithoutFocus(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	startApp(f)
	c := f.dialControl()
	c.hello(apiRevision)
	created := callChecked(t, c, "session.create", map[string]any{
		"initial_windows": []any{map[string]any{"window_label": "one", "layout": blockLayout()}},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	active := viewOf(t, c, sid)["active_window_id"]
	res := callChecked(t, c, "session.new_window", map[string]any{
		"session_id": sid, "window_label": "two", "focus": false,
		"layout": splitLayout("horizontal", 0.5),
	})
	view := viewOf(t, c, sid)
	if view["active_window_id"] != active {
		t.Errorf("active window changed: %v", view["active_window_id"])
	}
	for _, raw := range view["windows"].([]any) {
		w := raw.(map[string]any)
		if w["window_id"] == res["window_id"] && w["focused_block_id"] != res["block_ids"].([]any)[0] {
			t.Errorf("new window focus: %v", w)
		}
	}
}

// TestResizePaneBorders: borders between blocks move, whichever side of
// them the blocks are on.
func TestResizePaneBorders(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	startApp(f)
	c := f.dialControl()
	c.hello(apiRevision)
	// ((A h B) h (C h D)) is built as A h (B h (C h D)): the blocks are
	// in order, the splits nest to the right.
	nested := map[string]any{"split": map[string]any{
		"direction": "horizontal",
		"before":    splitLayout("horizontal", 0.5),
		"after": map[string]any{"split": map[string]any{
			"direction": "horizontal",
			"before":    blockLayout(),
			"after":     blockLayout(),
		}},
	}}
	created := callChecked(t, c, "session.create", map[string]any{
		"initial_windows": []any{map[string]any{"layout": nested}},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	ids := blockIDsOf(t, viewOf(t, c, sid), 0)
	if len(ids) != 4 {
		t.Fatalf("blocks: %v", ids)
	}
	// The first two blocks share the top split.
	callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "horizontal", "ratio": 0.3,
		"before_block_id": ids[0], "after_block_id": ids[1],
	})
	// The second and the third share a split below it.
	callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "horizontal", "ratio": 0.6,
		"before_block_id": ids[1], "after_block_id": ids[2],
	})
	// The first and the last share the top split, which moves as well.
	callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "horizontal", "ratio": 0.3,
		"before_block_id": ids[0], "after_block_id": ids[3],
	})
}
