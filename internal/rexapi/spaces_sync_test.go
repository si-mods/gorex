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
	work, play := ids["work"], ids["play"]
	blockOf := func(sid, label string) string {
		t.Helper()
		callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
		layers := windowByLabel(t, viewOf(t, c, sid), label)["layers"].([]any)
		return layers[0].(map[string]any)["blocks"].([]any)[0].(map[string]any)["block_id"].(string)
	}
	logsBlock, p1Block := blockOf(work, "logs"), blockOf(play, "p1")

	// Bad requests.
	if e := failure(t, c.request("bad1", "gorex.spaces_apply", map[string]any{})); e["code"] != "bad_request" {
		t.Fatalf("missing spaces: got %v", e)
	}
	if e := failure(t, c.request("bad2", "gorex.spaces_apply", map[string]any{"spaces": []any{}, "extra": 1})); e["code"] != "bad_request" {
		t.Fatalf("extra arg: got %v", e)
	}

	// A space of a session: logs goes to play.
	res := callChecked(t, c, "gorex.spaces_apply", map[string]any{
		"spaces": []map[string]any{{"name": "play", "block_ids": []string{logsBlock, "block:other-machine"}}},
	})
	if got := int(res["applied"].(float64)); got != 1 {
		t.Fatalf("applied = %d, want 1", got)
	}
	if s, _, _ := findBlockInModel(f.g.modelNow(), logsBlock); s == nil || s.id != play {
		t.Fatalf("logs is in %v, not play", s)
	}
	// Repeating the same apply is idempotent.
	res = callChecked(t, c, "gorex.spaces_apply", map[string]any{
		"spaces": []map[string]any{{"name": "play", "block_ids": []string{logsBlock}}},
	})
	if got := int(res["applied"].(float64)); got != 0 {
		t.Fatalf("repeat applied = %d, want 0", got)
	}

	// A space no session is makes no workspace (docs/projects.md): its
	// windows go to the first workspace with no folder, work.
	res = callChecked(t, c, "gorex.spaces_apply", map[string]any{
		"spaces": []map[string]any{{"name": "infra", "color": "blue", "block_ids": []string{p1Block}}},
	})
	m := f.g.modelNow()
	if findSessionByLabel(m, "infra") != nil {
		t.Fatal("spaces_apply made a workspace infra")
	}
	if s, _, _ := findBlockInModel(m, p1Block); int(res["applied"].(float64)) != 1 || s == nil || s.id != work {
		t.Fatalf("p1 is in %v (applied %v), not work", s, res["applied"])
	}
}
