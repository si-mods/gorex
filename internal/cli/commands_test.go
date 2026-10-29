package cli

import (
	"bytes"
	"encoding/json"
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
}

func TestHostsList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	body := `{"hosts":[{"id":"abc","name":"studio","address":"secret-address","network":true}]}`
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
}
