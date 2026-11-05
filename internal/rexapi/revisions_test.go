package rexapi

import (
	"encoding/json"
	"testing"
)

func TestRevisionRequestRename(t *testing.T) {
	payload := json.RawMessage(`{"session_id":"s","anchor_block_id":"block:1"}`)
	got := transformRequest(0, "session.new_split", payload)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["anchor_id"] != "block:1" {
		t.Fatalf("anchor_id = %v, want block:1", m["anchor_id"])
	}
	if _, ok := m["anchor_block_id"]; ok {
		t.Fatal("anchor_block_id was not renamed")
	}
	// Revision 1 already knows anchor_id.
	got = transformRequest(1, "session.new_split", payload)
	if string(got) != string(payload) {
		t.Fatalf("revision 1 request changed: %s", got)
	}
}

func TestRevisionResultRename(t *testing.T) {
	out := map[string]any{"anchor_id": "block:1", "revision": 1}
	got := transformResult(0, "session.new_split", out).(map[string]any)
	if got["anchor_block_id"] != "block:1" {
		t.Fatalf("anchor_block_id = %v", got["anchor_block_id"])
	}
	if _, ok := got["anchor_id"]; ok {
		t.Fatal("anchor_id was not renamed back")
	}
	if got := transformResult(1, "session.new_split", out).(map[string]any); got["anchor_id"] != "block:1" {
		t.Fatalf("revision 1 result changed: %v", got)
	}
}

func TestRevisionHidesFields(t *testing.T) {
	status := map[string]any{"api_revision": 8, "api_min_revision": 0, "pid": 1}
	if got := transformResult(0, "server.status", status).(map[string]any); got["api_revision"] != nil || got["api_min_revision"] != nil {
		t.Fatalf("revision 0 still sees the revisions: %v", got)
	}
	if got := transformResult(2, "server.status", status).(map[string]any); got["api_revision"] != float64(8) {
		t.Fatalf("revision 2 lost api_revision: %v", got)
	}
	format := map[string]any{"id": "b", "total": 3, "eof": true, "args": map[string]any{"max_bytes": 1}}
	if got := transformResult(5, "com.superlogical.terminal.format", format).(map[string]any); got["id"] != nil || got["total"] != nil || got["eof"] != nil {
		t.Fatalf("revision 5 still sees paging: %v", got)
	}
	if got := transformResult(6, "com.superlogical.terminal.format", format).(map[string]any); got["id"] != "b" {
		t.Fatalf("revision 6 lost paging: %v", got)
	}
	bootstrap := map[string]any{"data_tickets": []any{}, "client_id": "c"}
	if got := transformResult(7, "session.bootstrap", bootstrap).(map[string]any); got["data_tickets"] != nil {
		t.Fatalf("revision 7 sees data_tickets: %v", got)
	}
	if got := transformResult(8, "session.bootstrap", bootstrap).(map[string]any); got["data_tickets"] == nil {
		t.Fatalf("revision 8 lost data_tickets: %v", got)
	}

	// Revision 9 adds a layer's state, which older clients do not see.
	view := map[string]any{"windows": []any{map[string]any{"layers": []any{
		map[string]any{"layer_id": "l", "state": "minimized"},
	}}}}
	if got := transformResult(8, "session.view", view).(map[string]any); layerOf(got)["state"] != nil {
		t.Fatalf("revision 8 sees a layer's state: %v", got)
	}
	if got := transformResult(9, "session.view", view).(map[string]any); layerOf(got)["state"] != "minimized" {
		t.Fatalf("revision 9 lost a layer's state: %v", got)
	}
	attached := map[string]any{"view": view}
	if got := transformResult(8, "session.attach", attached).(map[string]any); layerOf(got["view"].(map[string]any))["state"] != nil {
		t.Fatalf("revision 8 attach sees a layer's state: %v", got)
	}

	// Revision 10 adds Tailscale's control server, which older clients
	// do not see.
	tail := map[string]any{"tailscale": map[string]any{"state": "running", "control_url": "https://headscale.example.com"}}
	if got := transformResult(9, "server.status", tail).(map[string]any); got["tailscale"].(map[string]any)["control_url"] != nil {
		t.Fatalf("revision 9 sees control_url: %v", got)
	}
	if got := transformResult(10, "server.status", tail).(map[string]any); got["tailscale"].(map[string]any)["control_url"] == nil {
		t.Fatalf("revision 10 lost control_url: %v", got)
	}
	event := map[string]any{"state": "running", "control_url": "https://headscale.example.com"}
	if got := transformResult(9, "tailscale_status_changed", event).(map[string]any); got["control_url"] != nil {
		t.Fatalf("revision 9 event sees control_url: %v", got)
	}
}

// layerOf returns the first layer of a view-like object.
func layerOf(v map[string]any) map[string]any {
	return v["windows"].([]any)[0].(map[string]any)["layers"].([]any)[0].(map[string]any)
}

func TestMethodAvailability(t *testing.T) {
	if methodAvailable(2, "server.stop") {
		t.Fatal("revision 2 knows server.stop")
	}
	if !methodAvailable(4, "server.stop") {
		t.Fatal("revision 4 does not know server.stop")
	}
	if methodAvailable(2, "com.superlogical.terminal.modes") {
		t.Fatal("revision 2 knows terminal modes")
	}
	if !methodAvailable(3, "com.superlogical.terminal.modes") {
		t.Fatal("revision 3 does not know terminal modes")
	}
	if methodAvailable(8, "session.set_layer_state") {
		t.Fatal("revision 8 knows session.set_layer_state")
	}
	if !methodAvailable(9, "session.set_layer_state") {
		t.Fatal("revision 9 does not know session.set_layer_state")
	}
}

func TestHelpFollowsRevision(t *testing.T) {
	names := func(rev int, all bool) map[string]bool {
		var doc map[string]json.RawMessage
		if all {
			doc = helpAllDocument(rev)
		} else {
			doc = helpDocument(rev, false)
		}
		var methods []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(doc["methods"], &methods); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, m := range methods {
			out[m.Name] = true
		}
		return out
	}
	if names(2, false)["server.stop"] {
		t.Fatal("revision 2 help lists server.stop")
	}
	if !names(4, true)["server.stop"] {
		t.Fatal("revision 4 help.all does not list server.stop")
	}
	if names(2, true)["server.apple.privacy_status"] {
		t.Fatal("revision 2 help.all lists privacy_status")
	}
	if !names(5, true)["server.apple.privacy_status"] {
		t.Fatal("revision 5 help.all does not list privacy_status")
	}
	if names(8, false)["session.set_layer_state"] {
		t.Fatal("revision 8 help lists session.set_layer_state")
	}
	if !names(9, false)["session.set_layer_state"] {
		t.Fatal("revision 9 help does not list session.set_layer_state")
	}
}

func TestStatusHidesRevisionsOverWire(t *testing.T) {
	f := startFixture(t)
	old := f.dialControl()
	old.hello(1)
	resp := old.request("status", "server.status", nil)
	status := result(t, resp)
	if _, ok := status["api_revision"]; ok {
		t.Fatal("revision 1 sees api_revision")
	}
	if _, ok := status["api_min_revision"]; ok {
		t.Fatal("revision 1 sees api_min_revision")
	}
	if who, _ := resp["requester"].(string); who == "" {
		t.Fatal("response has no requester")
	}

	cur := f.dialControl()
	cur.hello(2)
	status = result(t, cur.request("status", "server.status", nil))
	if status["api_revision"] != float64(apiRevision) {
		t.Fatalf("revision 2 api_revision = %v", status["api_revision"])
	}
}
