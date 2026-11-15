package rexapi

import (
	"testing"
)

func TestRevisionsMapCloneNoMutation(t *testing.T) {
	orig := map[string]any{
		"api_revision":     14,
		"api_min_revision": 0,
		"tailscale": map[string]any{
			"state":       "running",
			"control_url": "https://headscale.example.com",
		},
	}
	got := transformResult(9, "server.status", orig).(map[string]any)
	if _, ok := got["tailscale"].(map[string]any)["control_url"]; ok {
		t.Fatalf("rev 9 should hide control_url")
	}
	if orig["tailscale"].(map[string]any)["control_url"] == nil {
		t.Fatalf("transformResult mutated caller map")
	}
	if changes := changesFor(12, "server.status"); len(changes) != 0 {
		t.Fatalf("rev 12 fast-path returned changes: %v", changes)
	}
	if got14 := transformResult(14, "server.status", orig).(map[string]any); got14["api_revision"] != 14 {
		t.Fatalf("rev 14 modified payload: %v", got14)
	}
}
