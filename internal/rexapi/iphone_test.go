package rexapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestControlNotCompressed: the control connection never negotiates
// permessage-deflate, which Rex's iPhone app offers but cannot read on a
// large message.
func TestControlNotCompressed(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, f.srv.URL+"/api/control", &websocket.DialOptions{
		Subprotocols:    []string{controlProtocol},
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Fatalf("negotiated %q", ext)
	}
	c := newTestConn(t, ws)
	c.hello(apiRevision)
	creators := callChecked(t, c, "block_creator.list", nil)
	if len(creators["creators"].([]any)) != 1 {
		t.Fatalf("creators: %v", creators)
	}
}

// TestKeymapLayers: client.keymap stacks the client's defaults and
// overrides, the actions it sends accepted, a removal taking a binding
// off the layers beneath, and lists the terminal's actions.
func TestKeymapLayers(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	res := callChecked(t, c, "client.keymap", map[string]any{
		"actions": []any{map[string]any{"name": "client.settings", "kind": "client", "title": "Settings…"}},
		"defaults": []any{map[string]any{"name": "default", "bindings": []any{
			map[string]any{"keys": []any{"cmd+,"}, "action": "client.settings"},
			map[string]any{"keys": []any{"cmd+k"}, "action": "com.superlogical.terminal.clear"},
			map[string]any{"keys": []any{"cmd+j"}, "action": "pane.down", "mode": "nav"},
		}}},
		"overrides": []any{map[string]any{"name": "settings",
			"bindings": []any{map[string]any{"keys": []any{"cmd+e"}, "action": "pane.split"}},
			"removals": []any{map[string]any{"keys": []any{"cmd+k"}}},
		}},
	})
	var got []string
	for _, b := range res["bindings"].([]any) {
		bm := b.(map[string]any)
		got = append(got, bm["action"].(string)+"@"+bm["source"].(string))
	}
	want := "pane.split@settings pane.down@default client.settings@default"
	if strings.Join(got, " ") != want {
		t.Errorf("bindings %v, want %s", got, want)
	}
	modes, _ := res["modes"].([]any)
	if len(modes) != 1 || modes[0].(map[string]any)["name"] != "nav" {
		t.Errorf("modes %v", res["modes"])
	}
	found := false
	for _, a := range res["actions"].([]any) {
		if a.(map[string]any)["name"] == "com.superlogical.terminal.clear" {
			found = a.(map[string]any)["title"] == "Clear Terminal"
		}
	}
	if !found {
		t.Errorf("actions lack the terminal's clear: %v", res["actions"])
	}
	// A layer name used twice is a bad request.
	m := c.request("dup", "client.keymap", map[string]any{
		"defaults":  []any{map[string]any{"name": "x"}},
		"overrides": []any{map[string]any{"name": "x"}},
	})
	if failure(t, m)["code"] != "bad_request" {
		t.Errorf("duplicate layer: %v", m)
	}
}
