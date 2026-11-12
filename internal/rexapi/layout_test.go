package rexapi

import (
	"encoding/json"
	"math"
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

// TestSessionCreateLayout: session.create makes a workspace with its
// windows, and the view shows them.
func TestSessionCreateLayout(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
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
// on the server, which makes them itself, and checks the view after each.
func TestLayoutChanges(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
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
	c := f.dialControl()
	c.hello(apiRevision)
	// ((A h B) h (C h D)), built in its own shape.
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
	// The first two blocks share the left split.
	callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "horizontal", "ratio": 0.3,
		"before_block_id": ids[0], "after_block_id": ids[1],
	})
	// The second and the third share the top split.
	callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "horizontal", "ratio": 0.6,
		"before_block_id": ids[1], "after_block_id": ids[2],
	})
	// The first and the last share the top split too.
	callChecked(t, c, "session.resize_pane", map[string]any{
		"session_id": sid, "direction": "horizontal", "ratio": 0.3,
		"before_block_id": ids[0], "after_block_id": ids[3],
	})
}

// nestedLayouts are layouts whose splits nest in both directions, with
// uneven ratios.
func nestedLayouts() map[string]map[string]any {
	split := func(direction string, ratio float64, before, after map[string]any) map[string]any {
		return map[string]any{"split": map[string]any{"direction": direction, "ratio": ratio, "before": before, "after": after}}
	}
	b := blockLayout
	return map[string]map[string]any{
		"V{H{a,b},c}":      split("vertical", 0.5, split("horizontal", 0.5, b(), b()), b()),
		"H{V{a,b},c}":      split("horizontal", 0.5, split("vertical", 0.5, b(), b()), b()),
		"H{a,V{b,c}}":      split("horizontal", 0.5, b(), split("vertical", 0.5, b(), b())),
		"H{H{a,b},H{c,d}}": split("horizontal", 0.5, split("horizontal", 0.5, b(), b()), split("horizontal", 0.5, b(), b())),
		"V{H{a,V{b,c}},d}": split("vertical", 0.5, split("horizontal", 0.5, b(), split("vertical", 0.5, b(), b())), b()),
		"uneven":           split("vertical", 0.7, split("horizontal", 0.25, b(), split("vertical", 0.6, b(), b())), split("horizontal", 0.35, b(), b())),
		"uneven same way":  split("horizontal", 0.3, b(), split("horizontal", 0.8, b(), b())),
	}
}

// sameShape tells whether a GoRex tree has a layout node's splits, in
// their directions and with their ratios.
func sameShape(n *layout.Node, want map[string]any) bool {
	s, ok := want["split"].(map[string]any)
	if !ok || n.IsLeaf() {
		return !ok && n.IsLeaf()
	}
	return n.Vertical == (s["direction"] == "vertical") && math.Abs(float64(n.Ratio)-s["ratio"].(float64)) < 0.01 &&
		sameShape(n.A, s["before"].(map[string]any)) && sameShape(n.B, s["after"].(map[string]any))
}

// tabRoot returns the tree of a session's last tab, as GoRex keeps it.
func (f *fixture) tabRoot(t *testing.T, sid string) *layout.Node {
	t.Helper()
	raw, err := f.desk.Layout()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range layout.Parse(raw).Sessions {
		if sessionID(w.ID) == sid && len(w.Tabs) > 0 {
			return w.Tabs[len(w.Tabs)-1].Root
		}
	}
	t.Fatalf("no tab of %s", sid)
	return nil
}

// TestNestedLayoutShape: a layout of splits nested either way is built in
// its own shape, in a new window and beside a block.
func TestNestedLayoutShape(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	for name, tree := range nestedLayouts() {
		created := callChecked(t, c, "session.create", map[string]any{
			"initial_windows": []any{map[string]any{"layout": tree}},
		})
		sid := created["session_id"].(string)
		callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
		if root := f.tabRoot(t, sid); !sameShape(root, tree) {
			t.Errorf("session.create %s: built %s", name, mustJSON(root))
		}
		callChecked(t, c, "session.new_window", map[string]any{"session_id": sid, "layout": blockLayout()})
		callChecked(t, c, "session.new_split", map[string]any{
			"session_id": sid, "direction": "horizontal", "ratio": 0.4, "side": "after", "layout": tree,
		})
		beside := map[string]any{"split": map[string]any{"direction": "horizontal", "ratio": 0.4, "before": blockLayout(), "after": tree}}
		if root := f.tabRoot(t, sid); !sameShape(root, beside) {
			t.Errorf("session.new_split %s: built %s", name, mustJSON(root))
		}
	}
}

// moveLayout is a session, work, whose window editor is (s1 | (s2 / s3))
// with a floating f1, and whose window logs is s4.
func moveLayout(zoom bool) layout.Store {
	return layout.Store{Version: layout.Version, Sessions: []layout.Workspace{{
		ID: "work", Name: "work",
		Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{
			{
				Name:   "editor",
				Root:   split(pane("s1"), split(pane("s2"), pane("s3"), true, 0.5), false, 0.5),
				Floats: []layout.Float{{Pane: pane("f1"), X: 0.1, Y: 0.1, W: 0.5, H: 0.5}},
				Zoom:   zoom,
			},
			{Name: "logs", Root: pane("s4")},
		}},
	}}}
}

// viewShape names a view layout's blocks, by the sids of names, and its
// splits: "(h a b)" side by side, "(v a b)" one above the other.
func viewShape(l map[string]any, names map[string]string) string {
	if id, ok := l["block_id"].(string); ok {
		return names[id]
	}
	sp := l["split"].(map[string]any)
	d := "h"
	if sp["direction"] == "vertical" {
		d = "v"
	}
	return "(" + d + " " + viewShape(sp["before"].(map[string]any), names) + " " + viewShape(sp["after"].(map[string]any), names) + ")"
}

// viewSplit returns the split of a view layout that match takes, nil for
// none.
func viewSplit(l map[string]any, match func(sp, before, after map[string]any) bool) map[string]any {
	sp, ok := l["split"].(map[string]any)
	if !ok {
		return nil
	}
	before, after := sp["before"].(map[string]any), sp["after"].(map[string]any)
	if match(sp, before, after) {
		return sp
	}
	if s := viewSplit(before, match); s != nil {
		return s
	}
	return viewSplit(after, match)
}

// tiledLayout returns the layout of a view window's tiled layer.
func tiledLayout(t *testing.T, view map[string]any, window int) map[string]any {
	t.Helper()
	return windowOf(t, view, window)["layers"].([]any)[0].(map[string]any)["layout"].(map[string]any)
}

// TestMoveBlockSides moves a block to each side of another, in its own
// window and from another: the block keeps its ID and takes the focus,
// and the result names the split it made and the one it left.
func TestMoveBlockSides(t *testing.T) {
	f := startFixture(t)
	f.setLayout(moveLayout(false))
	c := f.dialControl()
	c.hello(apiRevision)
	sid := sessionIDs(t, c)["work"]
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	names := map[string]string{}
	for _, s := range []string{"s1", "s2", "s3", "s4", "f1"} {
		names[blockID(s)] = s
	}
	for _, tc := range []struct {
		name, block, anchor, direction, side string
		// want is the editor window's shape after; removed tells that the
		// result names the split that held the block.
		want    string
		removed bool
		windows int
	}{
		{name: "right in its window", block: "s1", anchor: "s3", direction: "horizontal", side: "after", want: "(v s2 (h s3 s1))", removed: true, windows: 2},
		{name: "left in its window", block: "s1", anchor: "s3", direction: "horizontal", side: "before", want: "(v s2 (h s1 s3))", removed: true, windows: 2},
		{name: "up in its window", block: "s1", anchor: "s3", direction: "vertical", side: "before", want: "(v s2 (v s1 s3))", removed: true, windows: 2},
		{name: "down in its window", block: "s1", anchor: "s3", direction: "vertical", side: "after", want: "(v s2 (v s3 s1))", removed: true, windows: 2},
		{name: "the defaults", block: "s1", anchor: "s3", want: "(v s2 (h s3 s1))", removed: true, windows: 2},
		{name: "before its sibling", block: "s3", anchor: "s2", direction: "vertical", side: "before", want: "(h s1 (v s3 s2))", removed: true, windows: 2},
		{name: "where it is", block: "s3", anchor: "s2", direction: "vertical", side: "after", want: "(h s1 (v s2 s3))", windows: 2},
		{name: "after, from another window", block: "s4", anchor: "s1", direction: "horizontal", side: "after", want: "(h (h s1 s4) (v s2 s3))", windows: 1},
		{name: "before, from another window", block: "s4", anchor: "s1", direction: "horizontal", side: "before", want: "(h (h s4 s1) (v s2 s3))", windows: 1},
		{name: "above, from another window", block: "s4", anchor: "s2", direction: "vertical", side: "before", want: "(h s1 (v (v s4 s2) s3))", windows: 1},
		{name: "a floating block", block: "f1", anchor: "s2", direction: "horizontal", side: "before", want: "(h s1 (v (h f1 s2) s3))", windows: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.setLayout(moveLayout(false))
			block, anchor := blockID(tc.block), blockID(tc.anchor)
			var held string
			if sp := viewSplit(tiledLayout(t, viewOf(t, c, sid), 0), func(_, before, after map[string]any) bool {
				return before["block_id"] == block || after["block_id"] == block
			}); sp != nil {
				held = sp["split_id"].(string)
			}
			payload := map[string]any{"session_id": sid, "block_id": block, "anchor_block_id": anchor, "ratio": 0.3}
			if tc.direction != "" {
				payload["direction"], payload["side"] = tc.direction, tc.side
			}
			res := callChecked(t, c, "session.move_block", payload)
			view := viewOf(t, c, sid)
			if n := len(windowIDsOf(t, view)); n != tc.windows {
				t.Errorf("%d windows, want %d", n, tc.windows)
			}
			l := tiledLayout(t, view, 0)
			if got := viewShape(l, names); got != tc.want {
				t.Errorf("editor is %s, want %s", got, tc.want)
			}
			first, second := anchor, block
			if tc.side == "before" {
				first, second = block, anchor
			}
			sp := viewSplit(l, func(_, before, after map[string]any) bool {
				return before["block_id"] == first && after["block_id"] == second
			})
			if sp == nil || res["created_split_id"] != sp["split_id"] {
				t.Fatalf("created_split_id %v, want the split of %s and %s: %v", res["created_split_id"], first, second, sp)
			}
			if r, _ := sp["ratio"].(float64); math.Abs(r-0.3) > 0.01 {
				t.Errorf("the new split's ratio %v, want 0.3 for its before side", sp["ratio"])
			}
			want := ""
			if tc.removed {
				want = held
			}
			if got, _ := res["removed_split_id"].(string); got != want {
				t.Errorf("removed_split_id %q, want %q", got, want)
			}
			if got := windowOf(t, view, 0)["focused_block_id"]; got != block {
				t.Errorf("focused %v, want %s", got, block)
			}
		})
	}

	// A move tells the attached client of the new view.
	f.setLayout(moveLayout(false))
	res := callChecked(t, c, "session.move_block", map[string]any{
		"session_id": sid, "block_id": blockID("s1"), "anchor_block_id": blockID("s3"), "side": "before",
	})
	for {
		_, ev := nextEvent(t, c)
		if _, typ, fields := sessionEventOf(t, ev); typ == "session_view_changed" && fields["revision"] == res["revision"] {
			break
		}
	}

	// The errors.
	for _, tc := range []struct {
		name    string
		zoom    bool
		payload map[string]any
		code    string
	}{
		{"beside itself", false, map[string]any{"block_id": blockID("s2"), "anchor_block_id": blockID("s2")}, "bad_request"},
		{"a side it does not know", false, map[string]any{"block_id": blockID("s1"), "anchor_block_id": blockID("s2"), "side": "above"}, "bad_request"},
		{"a direction it does not know", false, map[string]any{"block_id": blockID("s1"), "anchor_block_id": blockID("s2"), "direction": "diagonal"}, "bad_request"},
		{"beside a floating block", false, map[string]any{"block_id": blockID("s1"), "anchor_block_id": blockID("f1")}, "not_found"},
		{"beside no block", false, map[string]any{"block_id": blockID("s1"), "anchor_block_id": blockID("nope")}, "not_found"},
		{"in a zoomed window", true, map[string]any{"block_id": blockID("s1"), "anchor_block_id": blockID("s3"), "side": "before"}, "conflict"},
		{"into a zoomed window", true, map[string]any{"block_id": blockID("s4"), "anchor_block_id": blockID("s3")}, "conflict"},
	} {
		f.setLayout(moveLayout(tc.zoom))
		tc.payload["session_id"] = sid
		if got := failure(t, c.request("bad-move", "session.move_block", tc.payload))["code"]; got != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, got, tc.code)
		}
	}
}
