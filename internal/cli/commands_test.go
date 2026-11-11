package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
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
	if code, _, _, _ = runMain(t, "remote", "relay", "https://derp.example.com"); code != ExitUsage {
		t.Errorf("remote relay with a URL: %d", code)
	}
	code, _, _, errOut = runMain(t, "remote", "relay", "derp.example.com")
	if code != ExitOK {
		t.Fatalf("remote relay: %d %s", code, errOut)
	}
	code, _, out, _ = runMain(t, "remote", "status")
	if !strings.Contains(out, "relay=derp.example.com") {
		t.Errorf("remote status: %q", out)
	}
	runMain(t, "remote", "relay", "default")
	code, _, out, _ = runMain(t, "remote", "status")
	if !strings.Contains(out, "relay=default") {
		t.Errorf("remote status after relay default: %q", out)
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

// TestFloatAction checks how `gorex float` names the action it runs, and
// where it puts the flags and the pairs.
func TestFloatAction(t *testing.T) {
	for _, c := range []struct {
		args []string
		want []string
		ok   bool
	}{
		{nil, []string{"run", "pane.float"}, true},
		{[]string{"dock"}, []string{"run", "pane.float", "mode=off"}, true},
		{[]string{"hide-all"}, []string{"run", "float.hide_all"}, true},
		{[]string{"new", "x=0.2", "w=0.5"}, []string{"run", "float.new", "x=0.2", "w=0.5"}, true},
		{[]string{"move", "dx=0.1", "-s", "abc", "--json"}, []string{"run", "-s", "abc", "--json", "float.move", "dx=0.1"}, true},
		{[]string{"-s=abc", "toggle"}, []string{"run", "-s=abc", "float.toggle"}, true},
		{[]string{"nope"}, nil, false},
	} {
		got, ok := floatAction(c.args)
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("floatAction(%q) = %q, %v; want %q, %v", c.args, got, ok, c.want, c.ok)
		}
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
	if code, handled, _, _ := runMain(t, "-S", "host", "server", "restart"); code != ExitUsage || !handled {
		t.Fatalf("-S server restart: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "server", "bogus"); code != ExitUsage || !handled {
		t.Fatalf("server bogus: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "teleport", "pane"); code != ExitUsage || !handled {
		t.Fatalf("teleport without a host: %d %v", code, handled)
	}
	if code, handled, _, _ := runMain(t, "-S", "host", "teleport", "pane", "other"); code != ExitUsage || !handled {
		t.Fatalf("-S teleport: %d %v", code, handled)
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
	if !strings.Contains(out, `"name": "studio"`) || strings.Contains(out, "network") {
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

// TestServerAlias: -S finds a saved host by its alias as by its name, and
// hosts lists both.
func TestServerAlias(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	// The one host's alias is the other's name: -S names both.
	body := `{"hosts":[{"id":"abc","name":"ws-01","alias":"Build Box","address":"not-an-address"},{"id":"def","name":"build box","address":"not-an-address"}]}`
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, handled, _, errOut := runMain(t, "-S", "BUILD BOX", "ls")
	if code != ExitTarget || !handled || !strings.Contains(errOut, "ambiguous") {
		t.Fatalf("-S by alias: %d %v %q", code, handled, errOut)
	}
	code, _, out, errOut := runMain(t, "hosts")
	if code != ExitOK || !strings.Contains(out, "ALIAS") || !strings.Contains(out, "ws-01") || !strings.Contains(out, "Build Box") {
		t.Fatalf("hosts: %d %q %s", code, out, errOut)
	}
	code, _, out, errOut = runMain(t, "hosts", "--json")
	if code != ExitOK || !strings.Contains(out, `"name": "ws-01"`) || !strings.Contains(out, `"alias": "Build Box"`) || strings.Count(out, `"alias"`) != 1 {
		t.Fatalf("hosts JSON: %d %q %s", code, out, errOut)
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

func TestConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(path, []byte(`gorex.bind("ctrl+b>c", "tab.new")`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOREX_CONFIG", path)
	code, handled, out, errOut := runMain(t, "config", "path")
	if code != ExitOK || !handled || !strings.Contains(out, path) {
		t.Fatalf("config path: %d %v %q %s", code, handled, out, errOut)
	}
	oldWarnings := GhosttyWarnings
	GhosttyWarnings = func() []string { return []string{"Ghostty option \"x\" is ignored"} }
	code, _, out, errOut = runMain(t, "config", "check")
	GhosttyWarnings = oldWarnings
	if code != ExitOK || !strings.Contains(out, "bindings: 1") || !strings.Contains(out, "warning: Ghostty option") {
		t.Fatalf("config check: %d %q %s", code, out, errOut)
	}
	if err := os.WriteFile(path, []byte(`gorex.bind(`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _, _ = runMain(t, "config", "check")
	if code != ExitFailed {
		t.Fatalf("bad config check: %d", code)
	}
}

func TestLayoutCommand(t *testing.T) {
	c := startServer(t)
	if err := c.SetLayout(json.RawMessage(`{"tabs":[]}`)); err != nil {
		t.Fatal(err)
	}
	code, handled, out, errOut := runMain(t, "layout", "--json")
	if code != ExitOK || !handled || !strings.Contains(out, `"tabs"`) {
		t.Fatalf("layout: %d %v %q %s", code, handled, out, errOut)
	}
}

// TestLayoutWithoutWindow splits, renames and closes panes with no GoRex
// window open: the server makes the changes.
func TestLayoutWithoutWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	c := startServer(t)
	in, err := c.Create(rex.CreateOptions{Command: []string{"sh"}, Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	l := layout.Store{Version: layout.Version, Sessions: []layout.Workspace{{ID: "w", Name: "work", Layout: layout.Layout{Tabs: []layout.Tab{{ID: "t", Root: &layout.Node{SID: in.ID}}}}}}, Windows: []string{"w"}}
	raw, _ := json.Marshal(l)
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	read := func() layout.Tab {
		raw, err := c.Layout()
		if err != nil {
			t.Fatal(err)
		}
		st := layout.Parse(raw)
		if len(st.Sessions) != 1 || len(st.Sessions[0].Tabs) != 1 {
			t.Fatalf("layout %s", raw)
		}
		return st.Sessions[0].Tabs[0]
	}
	code, _, out, errOut := runMain(t, "split", "-s", in.ID, "--down", "--json", "--", "sh")
	var res rex.LayoutCommand
	if code != ExitOK || json.Unmarshal([]byte(out), &res) != nil || res.New == "" {
		t.Fatalf("split: %d %q %s", code, out, errOut)
	}
	if tab := read(); tab.Root.B == nil || tab.Root.B.SID != res.New || !tab.Root.Vertical {
		t.Errorf("after the split: %+v", tab.Root)
	}
	if code, _, _, errOut := runMain(t, "rename", "-s", res.New, "logs"); code != ExitOK {
		t.Fatalf("rename: %d %s", code, errOut)
	}
	if tab := read(); tab.Root.B.Name != "logs" {
		t.Errorf("after the rename: %+v", tab.Root.B)
	}
	if code, _, _, errOut := runMain(t, "close", "-s", res.New); code != ExitOK {
		t.Fatalf("close: %d %s", code, errOut)
	}
	if tab := read(); tab.Root.SID != in.ID {
		t.Errorf("after the close: %+v", tab.Root)
	}
}
