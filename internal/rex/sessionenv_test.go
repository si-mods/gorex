package rex

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
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
	if got["GOREX_SERVER"] != SocketPath() {
		t.Errorf("GOREX_SERVER = %q, want %q", got["GOREX_SERVER"], SocketPath())
	}
	if !strings.HasPrefix(got["PATH"], CLIDir()+string(os.PathListSeparator)) {
		t.Errorf("PATH %q does not start with the gorex directory", got["PATH"])
	}
	remote := envMap(sessionEnv("abc", nil, false))
	if _, ok := remote["GOREX_BIN_DIR"]; ok {
		t.Error("a remote session got the gorex shim directory")
	}
	if _, ok := remote["GOREX_SERVER"]; ok {
		t.Error("a remote session got the local server socket")
	}
	if remote["PATH"] != os.Getenv("PATH") {
		t.Errorf("remote PATH = %q, want the server's", remote["PATH"])
	}
}

func TestSessionEnvNamesGhostty(t *testing.T) {
	// TestSessionEnvNamesGhostty: a session names its terminal Ghostty, which
	// draws it, at the version GoRex binds, not the terminal the server
	// started from.
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("TERM_PROGRAM_VERSION", "3.5.0")
	got := envMap(sessionEnv("abc", nil, true))
	if got["TERM_PROGRAM"] != "ghostty" {
		t.Errorf("TERM_PROGRAM = %q, want ghostty", got["TERM_PROGRAM"])
	}
	if got["TERM_PROGRAM_VERSION"] != terminal.GhosttyVersion {
		t.Errorf("TERM_PROGRAM_VERSION = %q, want %q", got["TERM_PROGRAM_VERSION"], terminal.GhosttyVersion)
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

func TestCleanEnv(t *testing.T) {
	agentVars := []string{"NO_COLOR=1", "CI=true", "PAGER=cat", "GIT_TERMINAL_PROMPT=0", "npm_config_yes=true"}
	tests := []struct {
		name string
		in   []string
		drop []string
		keep []string
	}{
		{
			name: "omp marker drops agent variables",
			in:   append([]string{"OMPCODE=1", "PI_SESSION_ID=x", "CLAUDE_CONFIG_DIR=/cfg", "npm_config_cache=/c"}, agentVars...),
			drop: []string{"OMPCODE", "PI_SESSION_ID", "NO_COLOR", "CI", "PAGER", "GIT_TERMINAL_PROMPT", "npm_config_yes"},
			keep: []string{"CLAUDE_CONFIG_DIR", "npm_config_cache"},
		},
		{
			name: "claude marker drops agent variables",
			in:   append([]string{"CLAUDECODE=1", "FOO=bar"}, agentVars...),
			drop: []string{"CLAUDECODE", "NO_COLOR", "CI", "PAGER"},
			keep: []string{"FOO"},
		},
		{
			name: "AGENT=1 alone counts as an agent",
			in:   []string{"AGENT=1", "NO_COLOR=1", "CI=true", "FOO=bar"},
			drop: []string{"AGENT", "NO_COLOR", "CI"},
			keep: []string{"FOO"},
		},
		{
			name: "bare AGENT is no evidence",
			in:   []string{"AGENT=other", "NO_COLOR=1", "PAGER=cat"},
			keep: []string{"AGENT", "NO_COLOR", "PAGER"},
		},
		{
			name: "rex markers are no evidence but are dropped",
			in:   []string{"REX_SESSION=a", "REX_BLOCK=b", "REX_SERVER=c", "REX_BIN_DIR=d", "NO_COLOR=1", "PAGER=cat", "CI=true"},
			drop: []string{"REX_SESSION", "REX_BLOCK", "REX_SERVER", "REX_BIN_DIR"},
			keep: []string{"NO_COLOR", "PAGER", "CI"},
		},
		{
			name: "normal environment keeps user variables",
			in:   []string{"NO_COLOR=1", "CI=true", "PAGER=less", "EDITOR=vim", "CLAUDE_CONFIG_DIR=/cfg"},
			keep: []string{"NO_COLOR", "CI", "PAGER", "EDITOR", "CLAUDE_CONFIG_DIR"},
		},
		{
			name: "user values stay through an agent",
			in:   []string{"CLAUDECODE=1", "EDITOR=nvim", "PAGER=less", "CI=false", "GIT_EDITOR=code --wait", "EDITOR2=x"},
			drop: []string{"CLAUDECODE"},
			keep: []string{"EDITOR", "PAGER", "CI", "GIT_EDITOR", "EDITOR2"},
		},
		{
			name: "values the agent sets are dropped",
			in:   []string{"CLAUDECODE=1", "CI=1", "AWS_PAGER=", "GPG_TTY=not a tty", "EDITOR=true", "NO_COLOR=whatever"},
			drop: []string{"CI", "AWS_PAGER", "GPG_TTY", "EDITOR", "NO_COLOR"},
		},
		{
			name: "npm_config names match in any case",
			in:   []string{"CLAUDECODE=1", "NPM_CONFIG_YES=true", "NPM_CONFIG_AUDIT=false", "npm_config_fund=false", "NPM_CONFIG_CACHE=/c"},
			drop: []string{"NPM_CONFIG_YES", "NPM_CONFIG_AUDIT", "npm_config_fund"},
			keep: []string{"NPM_CONFIG_CACHE"},
		},
		{
			// A server cleaned before this fix has no markers, but still
			// has AGENT=1 and the agent's values. The upgrade re-exec
			// runs serverEnv in it.
			name: "tainted server without markers is cleaned on upgrade",
			in:   []string{"AGENT=1", "NO_COLOR=1", "CI=true", "PAGER=cat", "GIT_PAGER=cat", "EDITOR=true", "PATH=/bin", "HOME=/h"},
			drop: []string{"AGENT", "NO_COLOR", "CI", "PAGER", "GIT_PAGER", "EDITOR"},
			keep: []string{"PATH", "HOME"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := envMap(cleanEnv(tt.in))
			for _, k := range tt.drop {
				if _, ok := got[k]; ok {
					t.Errorf("%s was kept", k)
				}
			}
			for _, k := range tt.keep {
				if _, ok := got[k]; !ok {
					t.Errorf("%s was dropped", k)
				}
			}
		})
	}
}

func TestCleanEnvFoldsWindowsNames(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("names are case-sensitive here")
	}
	got := envMap(cleanEnv([]string{"CLAUDECODE=1", "Pager=cat", "no_color=1"}))
	for _, k := range []string{"Pager", "no_color"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s was kept", k)
		}
	}
}

// TestSessionEnvAfterServerEnv follows the real path: an agent starts the
// server, serverEnv cleans the environment, the server runs with it, and
// sessionEnv builds a session from it. The server itself never has a marker.
func TestSessionEnvAfterServerEnv(t *testing.T) {
	agent := []string{
		"CLAUDECODE=1", "AGENT=1", "NO_COLOR=1", "CI=true", "GIT_PAGER=cat",
		"PAGER=less", "EDITOR=nvim", "CLAUDE_CONFIG_DIR=/cfg",
	}
	names := []string{"CLAUDECODE", "AGENT", "NO_COLOR", "CI", "GIT_PAGER", "PAGER", "EDITOR", "CLAUDE_CONFIG_DIR"}
	for _, k := range names { // restore the real values after the test
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for _, kv := range cleanEnv(agent) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	got := envMap(sessionEnv("abc", nil, true))
	for _, k := range []string{"CLAUDECODE", "AGENT", "NO_COLOR", "CI", "GIT_PAGER"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s leaked into the session environment", k)
		}
	}
	for k, want := range map[string]string{"PAGER": "less", "EDITOR": "nvim", "CLAUDE_CONFIG_DIR": "/cfg"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
