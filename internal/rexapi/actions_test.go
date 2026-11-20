package rexapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorex/internal/config"
	"gorex/internal/rex"
)

// writeConfig writes the Lua configuration of a test and points the
// gateway at it.
func writeConfig(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOREX_CONFIG", path)
	return path
}

// TestConfigKeymapListsActions: client.keymap lists the file's actions,
// bindings, modes and diagnostics.
func TestConfigKeymapListsActions(t *testing.T) {
	path := writeConfig(t, `
gorex.action{name = "worktree", title = "New Worktree", description = "Ask and open", args = {branch = "string"}, run = function(ctx, args) end}
gorex.bind("cmd+shift+w", "worktree")
gorex.mode("work", {exclusive = true})
`)
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	r := result(t, c.request("k", "client.keymap", nil))

	var found bool
	for _, raw := range r["actions"].([]any) {
		action := raw.(map[string]any)
		if action["name"] != "worktree" {
			continue
		}
		found = true
		if action["kind"] != "config" || action["title"] != "New Worktree" {
			t.Errorf("action = %v", action)
		}
		schema := action["args"].(map[string]any)
		if schema["type"] != "object" {
			t.Errorf("args = %v", schema)
		}
	}
	if !found {
		t.Fatalf("no worktree action in %v", r["actions"])
	}
	var bound bool
	for _, raw := range r["bindings"].([]any) {
		binding := raw.(map[string]any)
		if binding["action"] != "worktree" {
			continue
		}
		bound = true
		if binding["source"] != "init.lua" {
			t.Errorf("source = %v", binding["source"])
		}
		keys := binding["keys"].([]any)
		if len(keys) != 1 || keys[0] != "cmd+shift+w" {
			t.Errorf("keys = %v", keys)
		}
	}
	if !bound {
		t.Fatalf("no worktree binding in %v", r["bindings"])
	}
	var mode bool
	for _, raw := range r["modes"].([]any) {
		m := raw.(map[string]any)
		if m["name"] == "work" && m["exclusive"] == true {
			mode = true
		}
	}
	if !mode {
		t.Fatalf("no work mode in %v", r["modes"])
	}
	cfg := r["config"].(map[string]any)
	if cfg["path"] != path || cfg["exists"] != true || cfg["hash"] == "" || cfg["generation"].(float64) < 1 {
		t.Fatalf("config = %v", cfg)
	}
	if cfg["path"] != config.Path() {
		t.Errorf("path %v, want %v", cfg["path"], config.Path())
	}
}

// TestConfigActionRunsBuiltin: a configured action runs a built-in that
// the gateway performs.
func TestConfigActionRunsBuiltin(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "newtab", run = function(ctx, args) gorex.invoke("tab.new") end}
`)
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	created := callChecked(t, c, "session.create", map[string]any{
		"label": "actions",
		"initial_windows": []any{
			map[string]any{"window_label": "one", "layout": blockLayout()},
		},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	before := len(viewOf(t, c, sid)["windows"].([]any))
	callChecked(t, c, "client.action", map[string]any{"action": "newtab", "session_id": sid})
	after := len(viewOf(t, c, sid)["windows"].([]any))
	if after != before+1 {
		t.Fatalf("windows %d, want %d", after, before+1)
	}
}

// TestConfigActionNamesUnsupportedBuiltin: an app-only built-in fails,
// and the message names it.
func TestConfigActionNamesUnsupportedBuiltin(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "palette", run = function(ctx, args) gorex.invoke("palette.open") end}
`)
	f := startFixture(t)
	c, sid, _ := f.sessionWith(t, rex.CreateOptions{})
	e := failure(t, c.request("a", "client.action", map[string]any{"action": "palette", "session_id": sid}))
	if !strings.Contains(e["message"].(string), "not supported by the gateway: palette.open") {
		t.Fatalf("error = %v", e)
	}
}

// TestConfigActionChecksArgs: an action's declared args are checked
// before it runs.
func TestConfigActionChecksArgs(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "pick", args = {dir = "string", count = "integer?"}, run = function(ctx, args) end}
`)
	f := startFixture(t)
	c, sid, _ := f.sessionWith(t, rex.CreateOptions{})
	for _, tc := range []struct {
		args map[string]any
		code string
	}{
		{map[string]any{}, "bad_request"},
		{map[string]any{"dir": 3}, "bad_request"},
		{map[string]any{"dir": "/tmp", "nope": true}, "bad_request"},
		{map[string]any{"dir": "/tmp", "count": 1.5}, "bad_request"},
		{map[string]any{"dir": "/tmp", "count": 2.0}, ""},
	} {
		res := c.request("a", "client.action", map[string]any{
			"action": "pick", "session_id": sid, "args": tc.args,
		})
		if tc.code == "" {
			result(t, res)
			continue
		}
		if e := failure(t, res); e["code"] != tc.code {
			t.Errorf("args %v: %v", tc.args, e)
		}
	}
}

// TestConfigReloadKeepsLastGood: a bad file keeps the actions, and the
// diagnostics name the error.
func TestConfigReloadKeepsLastGood(t *testing.T) {
	path := writeConfig(t, `
gorex.action{name = "one", run = function(ctx, args) end}
`)
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	result(t, c.request("k1", "client.keymap", nil))

	if err := os.WriteFile(path, []byte("gorex.action{name = "), 0o600); err != nil {
		t.Fatal(err)
	}
	reload := result(t, c.request("r1", "server.config_reload", nil))
	diags := reload["diagnostics"].([]any)
	if len(diags) == 0 {
		t.Fatalf("no diagnostics: %v", reload)
	}
	if reload["generation"].(float64) < 2 {
		t.Fatalf("generation = %v", reload["generation"])
	}
	k := result(t, c.request("k2", "client.keymap", nil))
	if !hasConfigAction(k, "one") {
		t.Fatalf("the action went away: %v", k["actions"])
	}

	if err := os.WriteFile(path, []byte(`gorex.action{name = "two", run = function(ctx, args) end}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reload = result(t, c.request("r2", "server.config_reload", nil))
	if len(reload["diagnostics"].([]any)) != 0 {
		t.Fatalf("diagnostics = %v", reload["diagnostics"])
	}
	k = result(t, c.request("k3", "client.keymap", nil))
	if hasConfigAction(k, "one") || !hasConfigAction(k, "two") {
		t.Fatalf("actions = %v", k["actions"])
	}
}

// hasConfigAction tells whether client.keymap lists a config action.
func hasConfigAction(k map[string]any, name string) bool {
	actions, _ := k["actions"].([]any)
	for _, raw := range actions {
		if a, ok := raw.(map[string]any); ok && a["name"] == name && a["kind"] == "config" {
			return true
		}
	}
	return false
}

// TestConfigActionTimesOut: an action that does not finish fails with
// timeout.
func TestConfigActionTimesOut(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "spin", run = function(ctx, args) while true do end end}
`)
	old := actionDeadline
	actionDeadline = 100 * time.Millisecond
	defer func() { actionDeadline = old }()
	f := startFixture(t)
	c, sid, _ := f.sessionWith(t, rex.CreateOptions{})
	e := failure(t, c.request("a", "client.action", map[string]any{"action": "spin", "session_id": sid}))
	if e["code"] != "timeout" {
		t.Fatalf("error = %v", e)
	}
}
