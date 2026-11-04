package rexapi

import (
	"testing"

	"gorex/internal/layout"
)

// floatLayout is a session whose window has two tiled panes and a
// floating one.
func floatLayout() layout.Store {
	return layout.Store{
		Sessions: []layout.Workspace{
			{
				ID: "work", Name: "work",
				Layout: layout.Layout{
					Active: 0,
					Tabs: []layout.Tab{{
						Name:   "editor",
						Root:   split(pane("s1"), pane("s2"), false, 0.5),
						Focus:  0,
						Floats: []layout.Float{{Pane: pane("f1"), X: 0.1, Y: 0.1, W: 0.5, H: 0.5}},
					}},
				},
			},
		},
	}
}

// layerOfBlock returns the layer that holds a block.
func layerOfBlock(t *testing.T, view map[string]any, block string) map[string]any {
	t.Helper()
	for _, raw := range windowOf(t, view, 0)["layers"].([]any) {
		layer := raw.(map[string]any)
		for _, b := range layer["blocks"].([]any) {
			if b.(map[string]any)["block_id"] == block {
				return layer
			}
		}
	}
	t.Fatalf("no layer holds %s: %v", block, view)
	return nil
}

// TestSetLayerState minimizes a floating layer, brings it back with the
// focus, and checks the errors the method documents.
func TestSetLayerState(t *testing.T) {
	f := startFixture(t)
	f.setLayout(floatLayout())
	startApp(f)
	c := f.dialControl()
	c.hello(apiRevision)
	sid := sessionIDs(t, c)["work"]
	f1 := blockID("f1")
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})

	view := viewOf(t, c, sid)
	layer := layerOfBlock(t, view, f1)
	if layer["kind"] != "floating" || layer["state"] != nil {
		t.Fatalf("float before minimizing: %v", layer)
	}
	layerID := layer["layer_id"].(string)

	res := callChecked(t, c, "session.set_layer_state", map[string]any{
		"session_id": sid, "layer_id": layerID, "state": "minimized",
	})
	if res["revision"] == nil {
		t.Errorf("set_layer_state: %v", res)
	}
	view = viewOf(t, c, sid)
	if layer = layerOfBlock(t, view, f1); layer["state"] != "minimized" {
		t.Errorf("float after minimizing: %v", layer)
	}

	// The state it already has changes nothing.
	again := callChecked(t, c, "session.set_layer_state", map[string]any{
		"session_id": sid, "layer_id": layerID, "state": "minimized",
	})
	if again["revision"] != view["revision"] {
		t.Errorf("minimizing again moved the revision: %v, view %v", again, view["revision"])
	}

	// Returning it to normal with focus gives it the focus.
	callChecked(t, c, "session.set_layer_state", map[string]any{
		"session_id": sid, "layer_id": layerID, "state": "normal", "focus": true,
	})
	view = viewOf(t, c, sid)
	if layer = layerOfBlock(t, view, f1); layer["state"] != nil {
		t.Errorf("float after returning it: %v", layer)
	}
	if got := windowOf(t, view, 0)["focused_block_id"]; got != f1 {
		t.Errorf("focused %v, want %v", got, f1)
	}

	tiled := windowOf(t, view, 0)["layers"].([]any)[0].(map[string]any)["layer_id"].(string)
	for _, bad := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"session_id": sid, "layer_id": tiled, "state": "minimized"}, "bad_request"},
		{map[string]any{"session_id": sid, "layer_id": layerID, "state": "minimized", "focus": true}, "bad_request"},
		{map[string]any{"session_id": sid, "layer_id": layerID, "state": "sideways"}, "bad_request"},
		{map[string]any{"session_id": sid, "layer_id": "layer:nope", "state": "normal"}, "not_found"},
		{map[string]any{"session_id": "session:nope", "layer_id": layerID, "state": "normal"}, "session_not_found"},
	} {
		if got := failure(t, c.request("bad", "session.set_layer_state", bad.payload))["code"]; got != bad.code {
			t.Errorf("%v: %v, want %s", bad.payload, got, bad.code)
		}
	}

	// The tiled layer has no state to set, but focus works on it.
	callChecked(t, c, "session.set_layer_state", map[string]any{
		"session_id": sid, "layer_id": tiled, "state": "normal", "focus": true,
	})
	view = viewOf(t, c, sid)
	if got := windowOf(t, view, 0)["focused_block_id"]; got == f1 {
		t.Errorf("focus stayed on the float: %v", got)
	}

	// A client that predates revision 9 does not know the method.
	old := f.dialControl()
	old.hello(8)
	if got := failure(t, old.request("old", "session.set_layer_state", map[string]any{
		"session_id": sid, "layer_id": layerID, "state": "normal",
	}))["code"]; got != "method_not_found" {
		t.Errorf("revision 8: %v", got)
	}
}
