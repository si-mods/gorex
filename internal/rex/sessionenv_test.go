package rex

import (
	"strings"
	"testing"
)

func TestSessionEnvDropsStarterMarkers(t *testing.T) {
	drop := []string{
		"CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "AI_AGENT",
		"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_TOKEN",
		"REX_SESSION", "REX_BLOCK", "REX_SERVER", "REX_BIN_DIR",
		"TMUX", "ITERM_PROFILE",
	}
	keep := map[string]string{"FOO": "bar", "CLAUDE_CONFIG_DIR": "/cfg"}
	for _, k := range drop {
		t.Setenv(k, "leak")
	}
	for k, v := range keep {
		t.Setenv(k, v)
	}
	t.Setenv("GOREX_SESSION", "stale")

	got := map[string]string{}
	for _, kv := range sessionEnv("abc", nil) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, k := range drop {
		if _, ok := got[k]; ok {
			t.Errorf("%s leaked into the session environment", k)
		}
	}
	for k, v := range keep {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"HOME", "PATH"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s was dropped", k)
		}
	}
	if got["GOREX_SESSION"] != "abc" {
		t.Errorf("GOREX_SESSION = %q, want abc", got["GOREX_SESSION"])
	}
}
