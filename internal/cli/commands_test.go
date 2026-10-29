package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// startServer serves a session server on a directory of its own, and
// returns a client of it.
func startServer(t *testing.T) *rex.Client {
	t.Helper()
	t.Setenv("GOREX_DIR", t.TempDir())
	go func() { _ = rex.Serve() }()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(rex.SocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Shutdown() })
	return c
}

func runMain(t *testing.T, args ...string) (int, bool, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code, handled := Main(args, &out, &errOut, "1.2.3")
	return code, handled, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	code, handled, out, _ := runMain(t, "version", "--json")
	if code != ExitOK || !handled {
		t.Fatalf("version: %d %v", code, handled)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["version"] != "1.2.3" {
		t.Fatalf("version JSON %q: %v", out, err)
	}
}

func TestCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	c := startServer(t)

	code, _, out, errOut := runMain(t, "new", "--json")
	if code != ExitOK {
		t.Fatalf("new: %d %s", code, errOut)
	}
	var info rex.SessionInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil || info.ID == "" {
		t.Fatalf("new JSON %q: %v", out, err)
	}

	code, _, _, errOut = runMain(t, "send", "-s", info.ID, "--enter", "echo cli-marker")
	if code != ExitOK {
		t.Fatalf("send: %d %s", code, errOut)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _, out, errOut = runMain(t, "capture", "-s", info.ID, "--json")
		if code != ExitOK {
			t.Fatalf("capture: %d %s", code, errOut)
		}
		var capture struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(out), &capture); err == nil && strings.Contains(capture.Text, "cli-marker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture does not show the marker: %q", out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	code, _, out, errOut = runMain(t, "remote", "control", "on")
	if code != ExitOK {
		t.Fatalf("remote control on: %d %s", code, errOut)
	}
	code, _, out, errOut = runMain(t, "remote", "status", "--json")
	if code != ExitOK || !strings.Contains(out, `"remoteControl": true`) {
		t.Fatalf("remote status: %d %q %s", code, out, errOut)
	}
	code, _, out, errOut = runMain(t, "ls", "--json")
	if code != ExitOK || !strings.Contains(out, info.ID) {
		t.Fatalf("ls: %d %q %s", code, out, errOut)
	}
	code, _, _, errOut = runMain(t, "wait", "-s", info.ID, "--idle", "--timeout", "5s")
	if code != ExitOK {
		t.Fatalf("wait: %d %s", code, errOut)
	}
	code, _, _, errOut = runMain(t, "wait", "-s", info.ID, "--contains", "cli-marker", "--timeout", "5s")
	if code != ExitOK {
		t.Fatalf("wait --contains: %d %s", code, errOut)
	}
	code, _, out, errOut = runMain(t, "clients", "--json")
	if code != ExitOK || !strings.Contains(out, "[") {
		t.Fatalf("clients: %d %q %s", code, out, errOut)
	}

	code, _, out, errOut = runMain(t, "run", "--", "sh", "-c", "printf 'run-marker\\n'; exit 7")
	if code != 7 || !strings.Contains(out, "run-marker") {
		t.Fatalf("run: %d %q %s", code, out, errOut)
	}
	code, _, out, errOut = runMain(t, "run", "--json", "--", "sh", "-c", "printf 'json-run'")
	if code != ExitOK || !strings.Contains(out, `"text": "json-run"`) {
		t.Fatalf("run --json: %d %q %s", code, out, errOut)
	}

	code, _, out, errOut = runMain(t, "new", "--json", "--", "sh", "-c", "sleep 0.1; exit 5")
	if code != ExitOK {
		t.Fatalf("new command: %d %s", code, errOut)
	}
	var short rex.SessionInfo
	if err := json.Unmarshal([]byte(out), &short); err != nil {
		t.Fatal(err)
	}
	code, _, _, errOut = runMain(t, "wait", "-s", short.ID, "--exit", "--timeout", "5s")
	if code != 5 {
		t.Fatalf("wait --exit: %d %s", code, errOut)
	}
	code, _, out, errOut = runMain(t, "wait", "-s", short.ID, "--exit", "--json", "--timeout", "5s")
	if code != 5 || !strings.Contains(out, `"exitCode": 5`) {
		t.Fatalf("wait --json: %d %q %s", code, out, errOut)
	}
	c.Kill(short.ID)

	code, _, _, _ = runMain(t, "capture", "-s", "no-such-session")
	if code != ExitTarget {
		t.Fatalf("missing session: %d, want %d", code, ExitTarget)
	}
}

func TestMainLeavesAppArguments(t *testing.T) {
	if _, handled := Main([]string{"-NSWindowResizeTime", "0.1"}, &bytes.Buffer{}, &bytes.Buffer{}, "x"); handled {
		t.Error("an AppKit argument was handled as a command")
	}
	if _, handled := Main(nil, &bytes.Buffer{}, &bytes.Buffer{}, "x"); handled {
		t.Error("no arguments were handled")
	}
}

func TestUsageErrors(t *testing.T) {
	if code, handled, _, _ := runMain(t, "lsit"); code != ExitUsage || !handled {
		t.Fatalf("unknown command: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "capture", "--raw", "--json", "-s", "x"); code != ExitUsage || !handled {
		t.Fatalf("capture --raw --json: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "-S"); code != ExitUsage || !handled {
		t.Fatalf("-S without a host: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "-S", "", "ls"); code != ExitUsage || !handled {
		t.Fatalf("-S with an empty host: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "remote", "bogus"); code != ExitUsage || !handled {
		t.Fatalf("remote bogus: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "remote", "control"); code != ExitUsage || !handled {
		t.Fatalf("remote control without a value: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "-S", "not-an-address", "ls"); code != ExitServer || !handled {
		t.Fatalf("-S bad address: %d %v", code, handled)
	}
}

func TestHostsList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	body := `{"hosts":[{"id":"abc","name":"studio","address":"secret-address","network":true},{"id":"def","name":"evil\u001b]0;x\u0007","address":"secret-address"}]}`
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, handled, out, errOut := runMain(t, "hosts", "--json")
	if code != ExitOK || !handled {
		t.Fatalf("hosts: %d %v %s", code, handled, errOut)
	}
	if !strings.Contains(out, `"name": "studio"`) || !strings.Contains(out, `"network": true`) {
		t.Fatalf("hosts JSON %q", out)
	}
	if strings.Contains(out, "secret-address") {
		t.Fatalf("hosts printed the address: %q", out)
	}
	code, _, out, errOut = runMain(t, "hosts")
	if code != ExitOK || strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
		t.Fatalf("hosts table: %d %q %s", code, out, errOut)
	}
}

func TestSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	code, handled, out, errOut := runMain(t, "skill", "show")
	if code != ExitOK || !handled || !strings.Contains(out, "gorex send") {
		t.Fatalf("skill show: %d %v %q %s", code, handled, out, errOut)
	}
	code, _, _, errOut = runMain(t, "skill", "setup", "--agent", "claude")
	if code != ExitOK {
		t.Fatalf("skill setup: %d %s", code, errOut)
	}
	path := filepath.Join(home, ".claude", "skills", "gorex", "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), skillMarker) {
		t.Fatalf("skill file %q: %v", b, err)
	}
	code, _, out, errOut = runMain(t, "skill", "status", "--agent", "claude", "--json")
	if code != ExitOK || !strings.Contains(out, `"current"`) {
		t.Fatalf("skill status: %d %q %s", code, out, errOut)
	}
	code, _, _, errOut = runMain(t, "skill", "remove", "--agent", "claude")
	if code != ExitOK {
		t.Fatalf("skill remove: %d %s", code, errOut)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("skill file remains: %v", err)
	}
	victim := filepath.Join(home, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	code, _, _, errOut = runMain(t, "skill", "setup", "--agent", "claude")
	if code != ExitOK {
		t.Fatalf("skill setup with tmp symlink: %d %s", code, errOut)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep" {
		t.Fatalf("the tmp symlink was followed: %q %v", b, err)
	}
}

func TestTargetResolution(t *testing.T) {
	c := startServer(t)
	one, err := c.Create(rex.CreateOptions{Dir: t.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	two, err := c.Create(rex.CreateOptions{Dir: t.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(two.ID)
	defer c.Kill(one.ID)
	if got, err := resolveTarget(c, one.ID); err != nil || got != one.ID {
		t.Fatalf("exact: %q %v", got, err)
	}
	if got, err := resolveTarget(c, one.ID[len(one.ID)-4:]); err != nil || got != one.ID {
		t.Fatalf("suffix: %q %v", got, err)
	}
	if got, err := resolveTarget(c, "1"); err != nil || got != one.ID {
		t.Fatalf("position: %q %v", got, err)
	}
	if _, err := resolveTarget(c, "no-such-session"); !errors.Is(err, errNoSession) {
		t.Fatalf("missing: %v", err)
	}
	if got := codeFor(fmt.Errorf("x: %w", errAmbiguous)); got != ExitTarget {
		t.Fatalf("ambiguous code %d, want %d", got, ExitTarget)
	}
}

func TestLayoutLabels(t *testing.T) {
	raw := json.RawMessage(`{"sessions":[{"name":"work","active":0,"tabs":[{"name":"build","focus":1,"root":{"a":{"sid":"one"},"b":{"sid":"two"}}}]}]}`)
	labels := sessionLabels(raw)
	if labels["work"] != "two" || labels["build"] != "two" {
		t.Fatalf("labels %v", labels)
	}
}

func TestBrokenHostsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, handled, _, errOut := runMain(t, "-S", "studio", "ls")
	if code != ExitFailed || !handled || !strings.Contains(errOut, "not valid") {
		t.Fatalf("broken hosts: %d %v %q", code, handled, errOut)
	}
}

func TestHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	claude := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o700); err != nil {
		t.Fatal(err)
	}
	const old = `{"theme":"x","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"other-hook"}]},{"matcher":"x"}]}}`
	if err := os.WriteFile(claude, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _, errOut := runMain(t, "skill", "setup", "--agent", "claude", "--agent", "codex")
	if code != ExitOK {
		t.Fatalf("setup hooks: %d %s", code, errOut)
	}
	b, err := os.ReadFile(claude)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "__internal session-start --agent claude") ||
		!strings.Contains(string(b), "other-hook") || !strings.Contains(string(b), `"theme": "x"`) ||
		!strings.Contains(string(b), `"matcher": "x"`) {
		t.Fatalf("claude settings %s", b)
	}
	codex := filepath.Join(home, ".codex", "hooks.json")
	if b, err := os.ReadFile(codex); err != nil || !strings.Contains(string(b), "__internal session-start --agent codex") {
		t.Fatalf("codex hooks %q %v", b, err)
	}
	before, _ := os.ReadFile(claude)
	code, _, _, errOut = runMain(t, "skill", "setup", "--agent", "claude", "--agent", "codex")
	if code != ExitOK {
		t.Fatalf("second setup: %d %s", code, errOut)
	}
	if after, _ := os.ReadFile(claude); string(after) != string(before) {
		t.Fatal("a second setup changed the settings")
	}
	code, _, out, _ := runMain(t, "skill", "status", "--agent", "claude", "--json")
	if code != ExitOK || !strings.Contains(out, `"hook": "installed"`) {
		t.Fatalf("status hooks: %d %q", code, out)
	}
	code, _, _, errOut = runMain(t, "skill", "remove", "--agent", "claude", "--agent", "codex")
	if code != ExitOK {
		t.Fatalf("remove hooks: %d %s", code, errOut)
	}
	if b, _ := os.ReadFile(claude); strings.Contains(string(b), "__internal session-start") ||
		!strings.Contains(string(b), "other-hook") || !strings.Contains(string(b), `"matcher": "x"`) {
		t.Fatalf("claude settings after remove %s", b)
	}
	if b, _ := os.ReadFile(codex); strings.Contains(string(b), "__internal session-start") {
		t.Fatalf("codex hooks after remove %s", b)
	}
}

func TestSkillDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	code, _, _, errOut := runMain(t, "skill", "setup", "--agent", "claude", "--dry-run")
	if code != ExitOK {
		t.Fatalf("dry run: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gorex", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the skill: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b c"); got != `'a'\''b c'` {
		t.Fatalf("quote %q", got)
	}
}

func TestSessionStart(t *testing.T) {
	t.Setenv("GOREX_SESSION", "abc")
	t.Setenv("GOREX_HOST", "studio")
	code, _, out, _ := runMain(t, "__internal", "session-start", "--agent", "claude")
	if code != ExitOK || !strings.Contains(out, "abc") || !strings.Contains(out, "studio") {
		t.Fatalf("session-start: %d %q", code, out)
	}
}

func TestHookConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const bad = `{"hooks":"not-an-object"}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _, _ := runMain(t, "skill", "setup", "--agent", "claude")
	if code != ExitFailed {
		t.Fatalf("setup with bad hooks: %d", code)
	}
	if b, _ := os.ReadFile(path); string(b) != bad {
		t.Fatalf("settings changed: %s", b)
	}
}
