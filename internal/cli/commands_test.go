package cli

import (
	"bytes"
	"encoding/json"
	"os"
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
	startServer(t)

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
