package rexapi

import (
	"slices"
	"testing"
)

func TestGorexSpacesApply(t *testing.T) {
	f := startFixture(t)
	f.setLayout(spacesLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	features, _ := c.welcome["features"].([]any)
	if !slices.Contains(features, any(featureSpacesSync)) {
		t.Fatalf("welcome features %v lack %s", features, featureSpacesSync)
	}

	ids := sessionIDs(t, c)
	work := ids["work"]
	callChecked(t, c, "session.attach", map[string]any{"session_id": work})
	view := viewOf(t, c, work)
	logsWin := windowByLabel(t, view, "logs")
	layers := logsWin["layers"].([]any)
	blocks := layers[0].(map[string]any)["blocks"].([]any)
	logsBlock := blocks[0].(map[string]any)["block_id"].(string)

	// Bad requests.
	if e := failure(t, c.request("bad1", "gorex.spaces_apply", map[string]any{})); e["code"] != "bad_request" {
		t.Fatalf("missing spaces: got %v", e)
	}
	if e := failure(t, c.request("bad2", "gorex.spaces_apply", map[string]any{"spaces": []any{}, "extra": 1})); e["code"] != "bad_request" {
		t.Fatalf("extra arg: got %v", e)
	}

	// Apply a new space "infra" containing logsBlock (plus an unknown remote block).
	res := callChecked(t, c, "gorex.spaces_apply", map[string]any{
		"spaces": []map[string]any{
			{"name": "infra", "color": "blue", "block_ids": []string{logsBlock, "block:other-machine"}},
		},
	})
	if got := int(res["applied"].(float64)); got != 1 {
		t.Fatalf("applied = %d, want 1", got)
	}

	// Repeating the same apply is idempotent.
	res2 := callChecked(t, c, "gorex.spaces_apply", map[string]any{
		"spaces": []map[string]any{
			{"name": "infra", "color": "blue", "block_ids": []string{logsBlock}},
		},
	})
	if got := int(res2["applied"].(float64)); got != 0 {
		t.Fatalf("repeat applied = %d, want 0", got)
	}

	// Verify "infra" session now exists and holds "logs".
	m := f.g.modelNow()
	infra := findSessionByLabel(m, "infra")
	if infra == nil {
		t.Fatal("expected infra session to be created")
	}
	if s, _, _ := findBlockInModel(m, logsBlock); s == nil || s.id != infra.id {
		t.Fatalf("logsBlock not moved to infra: session=%v", s)
	}
}
