package rex

import (
	"os"
	"strings"
	"testing"
)

var testMarkers = []string{
	"CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_BRIDGE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_EXECPATH", "CLAUDE_PROJECT_DIR", "CLAUDE_ENV_FILE", "AI_AGENT",
	"CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID",
	"REX_SESSION", "REX_BLOCK", "REX_SERVER", "REX_BIN_DIR",
}

var testKept = map[string]string{
	"FOO": "bar", "CLAUDE_CONFIG_DIR": "/cfg", "CLAUDE_CODE_USE_BEDROCK": "1",
	"CLAUDE_CODE_OAUTH_TOKEN": "tok", "REX_SUDO": "1", "GOREX_DIR": "/gorex",
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func setMarkerEnv(t *testing.T) {
	for _, k := range testMarkers {
		t.Setenv(k, "leak")
	}
	for k, v := range testKept {
		t.Setenv(k, v)
	}
}

func TestSessionEnvDropsStarterMarkers(t *testing.T) {
	setMarkerEnv(t)
	t.Setenv("TMUX", "x")
	t.Setenv("ITERM_PROFILE", "x")
	t.Setenv("GOREX_SESSION", "stale")
	got := envMap(sessionEnv("abc", nil, true))
	for _, k := range append(testMarkers, "TMUX", "ITERM_PROFILE") {
		if _, ok := got[k]; ok {
			t.Errorf("%s leaked into the session environment", k)
		}
	}
	for k, v := range testKept {
		if k == "GOREX_DIR" {
			continue // other GOREX_ variables are dropped from sessions
		}
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["PATH"]; !ok {
		t.Error("PATH was dropped")
	}
	if got["GOREX_SESSION"] != "abc" {
		t.Errorf("GOREX_SESSION = %q, want abc", got["GOREX_SESSION"])
	}
	if got["GOREX_BIN_DIR"] != CLIDir() {
		t.Errorf("GOREX_BIN_DIR = %q, want %q", got["GOREX_BIN_DIR"], CLIDir())
	}
	if !strings.HasPrefix(got["PATH"], CLIDir()+string(os.PathListSeparator)) {
		t.Errorf("PATH %q does not start with the gorex directory", got["PATH"])
	}
	remote := envMap(sessionEnv("abc", nil, false))
	if _, ok := remote["GOREX_BIN_DIR"]; ok {
		t.Error("a remote session got the gorex shim directory")
	}
	if remote["PATH"] != os.Getenv("PATH") {
		t.Errorf("remote PATH = %q, want the server's", remote["PATH"])
	}
}

func TestServerEnvDropsStarterMarkers(t *testing.T) {
	setMarkerEnv(t)
	got := envMap(serverEnv())
	for _, k := range testMarkers {
		if _, ok := got[k]; ok {
			t.Errorf("%s leaked into the server environment", k)
		}
	}
	for k, v := range testKept {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["PATH"]; !ok {
		t.Error("PATH was dropped")
	}
}
