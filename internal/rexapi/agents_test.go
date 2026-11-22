package rexapi

import (
	"io"
	"slices"
	"strings"
	"testing"

	"gorex/internal/rex"
)

func TestAgentsListMethod(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); !slices.Contains(features, any(featureAgents)) {
		t.Fatalf("welcome features %v lack %s", features, featureAgents)
	}
	r := callChecked(t, c, "gorex.agents.list", nil)
	if _, ok := r["agents"].([]any); !ok {
		t.Fatalf("gorex.agents.list result = %v, want agents array", r)
	}
	if e := failure(t, c.request("bad", "gorex.agents.list", map[string]any{"extra": 1})); e["code"] != "bad_request" {
		t.Errorf("bad args code = %v, want bad_request", e)
	}
	if schemaFor(t, "gorex.agents.list") == nil {
		t.Error("gorex.agents.list has no result schema")
	}
	res, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatalf("GET /llms.txt: %v", err)
	}
	defer res.Body.Close()
	if body, _ := io.ReadAll(res.Body); !strings.Contains(string(body), "### gorex.agents.list") {
		t.Error("/llms.txt does not describe gorex.agents.list")
	}
}

func TestAgentsListOldServer(t *testing.T) {
	f := startFixture(t)
	f.g.hello.Capabilities = slices.DeleteFunc(slices.Clone(f.g.hello.Capabilities), func(s string) bool {
		return s == rex.CapabilityAgents
	})
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); slices.Contains(features, any(featureAgents)) {
		t.Errorf("welcome lists %s without capability: %v", featureAgents, features)
	}
	if e := failure(t, c.request("old", "gorex.agents.list", nil)); e["code"] != "not_supported" {
		t.Errorf("gorex.agents.list on old server = %v, want not_supported", e)
	}
}

func TestProjectUpdateLaunchers(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	dir := t.TempDir()
	made := callChecked(t, c, "gorex.project.create", map[string]any{"name": "demo", "dir": dir})
	pid := made["project_id"].(string)

	// Pin only claude.
	r := callChecked(t, c, "gorex.project.update", map[string]any{"project_id": pid, "launchers": []string{"claude"}})
	pr, _ := r["project"].(map[string]any)
	if got, _ := pr["launchers"].([]any); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("pinned launchers = %v, want [claude]", pr["launchers"])
	}

	// Pin none (empty slice).
	r = callChecked(t, c, "gorex.project.update", map[string]any{"project_id": pid, "launchers": []string{}})
	pr, _ = r["project"].(map[string]any)
	if got, ok := pr["launchers"].([]any); !ok || len(got) != 0 {
		t.Fatalf("empty launchers = %#v, want []", pr["launchers"])
	}

	// Reset to default (null omits launchers).
	r = callChecked(t, c, "gorex.project.update", map[string]any{"project_id": pid, "launchers": nil})
	pr, _ = r["project"].(map[string]any)
	if _, exists := pr["launchers"]; exists {
		t.Fatalf("reset launchers = %#v, want omitted", pr["launchers"])
	}
}
