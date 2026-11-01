package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// TestLegacyHookReplaced checks that setup replaces the hook of a build
// before agent-hook, and remove removes it, leaving the user's hooks.
func TestLegacyHookReplaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-config"))
	path := filepath.Join(home, "claude-config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const old = `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"rex-hook"}]},` +
		`{"hooks":[{"type":"command","command":"/bin/sh -c '/old/gorex __internal session-start --agent claude 2>/dev/null || true # gorex'"}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _, errOut := runMain(t, "skill", "setup", "--agent", "claude"); code != ExitOK {
		t.Fatalf("setup: %d %s", code, errOut)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "__internal session-start") || strings.Count(string(b), "agent-hook --agent claude") != 1 ||
		!strings.Contains(string(b), "rex-hook") {
		t.Fatalf("settings after setup %s", b)
	}
	if code, _, _, errOut := runMain(t, "skill", "remove", "--agent", "claude"); code != ExitOK {
		t.Fatalf("remove: %d %s", code, errOut)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "# gorex") || !strings.Contains(string(b), "rex-hook") {
		t.Fatalf("settings after remove %s", b)
	}
}

// TestExtensionHooks installs the extension that is Pi's and omp's hook,
// in each agent's directory, again without change, and removes it,
// leaving the user's extensions.
func TestExtensionHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	pi := filepath.Join(home, ".pi", "agent", "extensions")
	omp := filepath.Join(home, ".omp", "agent", "extensions")
	other := filepath.Join(pi, "other.ts")
	if err := os.MkdirAll(pi, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, out, errOut := runMain(t, "skill", "setup", "--agent", "pi", "--agent", "omp", "--json")
	if code != ExitOK {
		t.Fatalf("setup: %d %s %s", code, out, errOut)
	}
	exe, _ := os.Executable()
	for _, dir := range []string{pi, omp} {
		b, err := os.ReadFile(filepath.Join(dir, "gorex", "index.ts"))
		if err != nil || !strings.HasPrefix(string(b), extensionMarker) || !strings.Contains(string(b), `"agent-hook"`) ||
			!strings.Contains(string(b), strconv.Quote(exe)) || !strings.Contains(string(b), "GOREX_SESSION") {
			t.Fatalf("extension in %s: %q %v", dir, b, err)
		}
	}
	before, _ := os.ReadFile(filepath.Join(pi, "gorex", "index.ts"))
	code, _, out, _ = runMain(t, "skill", "setup", "--agent", "pi", "--agent", "omp", "--json")
	var states []skillState
	if err := json.Unmarshal([]byte(out), &states); code != ExitOK || err != nil || len(states) != 2 {
		t.Fatalf("second setup: %d %q %v", code, out, err)
	}
	for _, st := range states {
		if st.Hook != "current" {
			t.Fatalf("second setup %+v", st)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(pi, "gorex", "index.ts")); string(after) != string(before) {
		t.Fatal("a second setup changed the extension")
	}
	code, _, out, _ = runMain(t, "skill", "status", "--agent", "omp", "--json")
	if code != ExitOK || !strings.Contains(out, `"hook": "installed"`) {
		t.Fatalf("status: %d %q", code, out)
	}
	if code, _, _, errOut := runMain(t, "skill", "remove", "--agent", "pi", "--agent", "omp"); code != ExitOK {
		t.Fatalf("remove: %d %s", code, errOut)
	}
	for _, dir := range []string{pi, omp} {
		if _, err := os.Stat(filepath.Join(dir, "gorex")); !os.IsNotExist(err) {
			t.Fatalf("the extension in %s stays: %v", dir, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("remove took the user's extension: %v", err)
	}

	// PI_CODING_AGENT_DIR names the agent directory, as both read it.
	agentDir := filepath.Join(home, "custom-agent")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	if code, _, _, errOut := runMain(t, "skill", "setup", "--agent", "pi"); code != ExitOK {
		t.Fatalf("setup with PI_CODING_AGENT_DIR: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "extensions", "gorex", "index.ts")); err != nil {
		t.Fatal(err)
	}

	// An extension named gorex that GoRex did not write stays.
	mine := filepath.Join(agentDir, "extensions", "gorex", "index.ts")
	if err := os.WriteFile(mine, []byte("// mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _, _ := runMain(t, "skill", "setup", "--agent", "pi"); code != ExitFailed {
		t.Fatalf("setup over the user's extension: %d", code)
	}
	if code, _, _, _ := runMain(t, "skill", "remove", "--agent", "pi"); code != ExitFailed {
		t.Fatalf("remove of the user's extension: %d", code)
	}
	if b, _ := os.ReadFile(mine); string(b) != "// mine\n" {
		t.Fatalf("the user's extension is now %q", b)
	}
}

// TestAgentHookOutside checks that the hook does nothing outside GoRex, and
// gives the session's context when no server answers.
func TestAgentHookOutside(t *testing.T) {
	t.Setenv("GOREX_SERVER", "")
	t.Setenv("GOREX_SESSION", "")
	if code, _, out, errOut := runMain(t, "agent-hook", "--agent", "claude"); code != ExitOK || out != "" || errOut != "" {
		t.Fatalf("outside GoRex: %d %q %q", code, out, errOut)
	}
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Setenv("GOREX_SESSION", "s1")
	code, _, out, _ := runMain(t, "agent-hook", "--agent", "pi", "--conversation", "c1", "--pid", "1")
	if code != ExitOK || !strings.Contains(out, "GoRex session s1") || !strings.Contains(out, "If the session has no name yet") {
		t.Fatalf("no server: %d %q", code, out)
	}
	// Bad flags never fail an agent's start.
	if code, _, _, _ := runMain(t, "agent-hook", "--bogus"); code != ExitOK {
		t.Fatalf("bad flags: %d", code)
	}
}

// TestAgentHook runs an agent in a session: its hook's report is kept,
// from the conversation id given or the hook's JSON input, and the hook
// tells the agent to name the session, or the name it has.
func TestAgentHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(dir, "ran")
	script := "#!/bin/sh\necho $$ > " + ran + ".tmp; mv " + ran + ".tmp " + ran + "\nsleep 600\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := startServer(t)
	// The session's server, as a session's environment names it.
	t.Setenv("GOREX_SERVER", rex.SocketPath())
	t.Setenv("GOREX_DIR", t.TempDir())
	in, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Input(in.ID, []byte(filepath.Join(bin, "pi")+"\r")); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(ran); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	if pid == 0 {
		t.Fatal("the agent did not run")
	}
	t.Cleanup(func() { c.Kill(in.ID) })
	t.Setenv("GOREX_SESSION", in.ID)
	agent := func() *rex.AgentSession {
		l, err := c.List()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range l {
			if s.ID == in.ID {
				return s.Agent
			}
		}
		return nil
	}

	code, _, out, _ := runMain(t, "agent-hook", "--agent", "pi", "--conversation", "conv-9", "--pid", strconv.Itoa(pid))
	if code != ExitOK || !strings.Contains(out, "GoRex session "+in.ID) || !strings.Contains(out, "Name the session") ||
		!strings.Contains(out, "GoRex resumes this conversation") {
		t.Fatalf("hook: %d %q", code, out)
	}
	if a := agent(); a == nil || a.Name != "pi" || a.Conversation != "conv-9" || a.PID != pid {
		t.Fatalf("agent %+v", a)
	}

	// Claude Code gives its session on the hook's input.
	stdin = strings.NewReader(`{"session_id":"7f1d1c1e-0000-4000-8000-000000000001","cwd":"/x","source":"startup"}`)
	t.Cleanup(func() { stdin = os.Stdin })
	l := `{"version":2,"sessions":[{"id":"w","tabs":[{"root":{"sid":"` + in.ID + `","name":"fix-login"}}]}],"windows":["w"]}`
	if err := c.SetLayout(json.RawMessage(l)); err != nil {
		t.Fatal(err)
	}
	code, _, out, _ = runMain(t, "agent-hook", "--agent", "claude", "--pid", strconv.Itoa(pid))
	if code != ExitOK || !strings.Contains(out, `named "fix-login"`) {
		t.Fatalf("claude hook: %d %q", code, out)
	}
	if a := agent(); a == nil || a.Name != "claude" || a.Conversation != "7f1d1c1e-0000-4000-8000-000000000001" {
		t.Fatalf("agent %+v", a)
	}
}
