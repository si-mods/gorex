package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// omarchyFixture makes an Omarchy desktop, a temp home and a temp GoRex
// home in the environment, and returns the GoRex home. The palette is a
// dark one.
func omarchyFixture(t *testing.T) (gorex string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "omarchy")
	writeFixture(t, filepath.Join(root, "version"), "3.0.0\n")
	writeFixture(t, filepath.Join(root, "bin", "omarchy-theme-current"), "#!/bin/sh\n")
	state := filepath.Join(t.TempDir(), "state")
	writeFixture(t, filepath.Join(state, "omarchy", "current", "theme", "colors.toml"), `# Tokyo Night
mode = "dark"
accent = "#7aa2f7"
background = "#1a1b26"
foreground = "#c0caf5"
muted = "#565f89"
selection = "#283457"
lighter_background = "#24283b"
`)
	writeFixture(t, filepath.Join(state, "omarchy", "current", "theme.name"), "Tokyo Night\n")
	gorex = filepath.Join(t.TempDir(), "gorex")
	t.Setenv("OMARCHY_PATH", root)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("GOREX_DIR", gorex)
	t.Setenv("GOREX_CONFIG", filepath.Join(gorex, "init.lua"))
	return gorex
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// statusJSON is the object `omarchy status --json` prints.
type statusJSON struct {
	Omarchy          bool     `json:"omarchy"`
	Root             string   `json:"root"`
	Theme            string   `json:"theme"`
	Mode             string   `json:"mode"`
	Following        bool     `json:"following"`
	Binding          string   `json:"binding"`
	BindingInstalled bool     `json:"bindingInstalled"`
	Warnings         []string `json:"warnings"`
}

// TestOmarchyStatus reports the desktop, the theme, following and the
// binding, in text and in JSON, with or without Omarchy.
func TestOmarchyStatus(t *testing.T) {
	omarchyFixture(t)
	code, handled, out, errOut := runMain(t, "omarchy", "status")
	if code != ExitOK || !handled {
		t.Fatalf("status: %d %v %s", code, handled, errOut)
	}
	for _, want := range []string{"omarchy: yes", "theme: Tokyo Night (dark)", "following: yes", "binding: none"} {
		if !strings.Contains(out, want) {
			t.Errorf("status has no %q:\n%s", want, out)
		}
	}

	code, _, out, errOut = runMain(t, "omarchy", "status", "--json")
	if code != ExitOK {
		t.Fatalf("status --json: %d %s", code, errOut)
	}
	var r statusJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("status JSON %q: %v", out, err)
	}
	if !r.Omarchy || r.Theme != "Tokyo Night" || r.Mode != "dark" || !r.Following ||
		r.BindingInstalled || r.Binding != "" {
		t.Errorf("status JSON %q", out)
	}
	if r.Root == "" {
		t.Error("status JSON has no root")
	}

	// A machine without Omarchy still reports, and follows none.
	t.Setenv("OMARCHY_PATH", t.TempDir())
	code, _, out, errOut = runMain(t, "omarchy", "status", "--json")
	if code != ExitOK || !strings.Contains(out, `"omarchy": false`) || !strings.Contains(out, `"following": false`) {
		t.Errorf("status without Omarchy: %d %q %q", code, out, errOut)
	}
}

// TestOmarchyFollowing is the precedence of settings: an explicit theme
// wins, appearance.omarchy false disables, and a machine without Omarchy
// follows none.
func TestOmarchyFollowing(t *testing.T) {
	gorex := omarchyFixture(t)
	settings := filepath.Join(gorex, "settings.json")
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"an explicit theme wins", `{"appearance.theme": "slate"}`, false},
		{"appearance.omarchy false disables", `{"appearance.omarchy": false}`, false},
		{"appearance.omarchy true follows", `{"appearance.omarchy": true}`, true},
		{"absent means true", `{}`, true},
		{"the theme wins over true", `{"appearance.theme": "slate", "appearance.omarchy": true}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, settings, tc.body)
			code, _, out, errOut := runMain(t, "omarchy", "status", "--json")
			if code != ExitOK {
				t.Fatalf("status: %d %s", code, errOut)
			}
			var r statusJSON
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatalf("status JSON %q: %v", out, err)
			}
			if r.Following != tc.want {
				t.Errorf("following = %v, want %v", r.Following, tc.want)
			}
		})
	}
	// The CLI reads the settings and never writes them.
	before := readFixture(t, settings)
	if code, _, _, errOut := runMain(t, "omarchy", "status"); code != ExitOK {
		t.Fatalf("status: %d %s", code, errOut)
	}
	if after := readFixture(t, settings); after != before {
		t.Errorf("status rewrote settings.json:\n%s", after)
	}

	// Without Omarchy, the settings do not make the window follow.
	t.Setenv("OMARCHY_PATH", t.TempDir())
	writeFixture(t, settings, `{"appearance.omarchy": true}`)
	_, _, out, _ := runMain(t, "omarchy", "status")
	if !strings.Contains(out, "following: no") {
		t.Errorf("status without Omarchy:\n%s", out)
	}
}

// TestOmarchyBind adds, replaces and removes the block, and keeps every
// other line and the settings.
func TestOmarchyBind(t *testing.T) {
	gorex := omarchyFixture(t)
	settings := filepath.Join(gorex, "settings.json")
	writeFixture(t, settings, `{"appearance.theme": ""}`)
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hypr", "bindings.lua")
	user := "o.bind(\"SUPER + RETURN\", \"Terminal\", \"alacritty\")\n"
	writeFixture(t, path, user)

	code, handled, out, errOut := runMain(t, "omarchy", "bind")
	if code != ExitOK || !handled {
		t.Fatalf("bind: %d %v %s", code, handled, errOut)
	}
	if !strings.Contains(out, "wrote ") || !strings.Contains(out, path) {
		t.Errorf("bind printed %q", out)
	}
	want := user + "-- gorex omarchy: begin\n" +
		"o.bind(\"SUPER + SHIFT + R\", \"GoRex\", \"gorex\")\n" +
		"-- gorex omarchy: end\n"
	if got := readFixture(t, path); got != want {
		t.Fatalf("bindings.lua:\n%s\nwant:\n%s", got, want)
	}
	if code, _, out, errOut := runMain(t, "omarchy", "bind"); code != ExitOK || !strings.Contains(out, "unchanged ") {
		t.Errorf("the second bind: %d %q %q", code, out, errOut)
	}

	code, _, _, errOut = runMain(t, "omarchy", "bind", "--keys", "SUPER + G")
	if code != ExitOK {
		t.Fatalf("bind --keys: %d %s", code, errOut)
	}
	got := readFixture(t, path)
	if strings.Count(got, "-- gorex omarchy: begin") != 1 || !strings.Contains(got, `"SUPER + G"`) || !strings.HasPrefix(got, user) {
		t.Errorf("the block was not replaced:\n%s", got)
	}

	code, _, out, errOut = runMain(t, "omarchy", "unbind")
	if code != ExitOK {
		t.Fatalf("unbind: %d %s", code, errOut)
	}
	if !strings.Contains(out, "removed ") {
		t.Errorf("unbind printed %q", out)
	}
	if got := readFixture(t, path); got != user {
		t.Errorf("after unbind:\n%q\nwant:\n%q", got, user)
	}
	code, _, out, errOut = runMain(t, "omarchy", "unbind", "--json")
	if code != ExitOK {
		t.Fatalf("unbind --json: %d %s", code, errOut)
	}
	var c struct {
		Path   string `json:"path"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("unbind JSON %q: %v", out, err)
	}
	if c.Path != path || c.Action != "unchanged" {
		t.Errorf("the second unbind: %+v", c)
	}

	// Nothing but the bindings file changed, and nothing under the
	// desktop's configuration.
	if got := readFixture(t, settings); got != `{"appearance.theme": ""}` {
		t.Errorf("settings.json changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "omarchy")); !os.IsNotExist(err) {
		t.Errorf("the command wrote under ~/.config/omarchy: %v", err)
	}
}

// TestOmarchyNotDetected: bind and unbind exit 1 with the message; status
// still reports.
func TestOmarchyNotDetected(t *testing.T) {
	omarchyFixture(t)
	t.Setenv("OMARCHY_PATH", t.TempDir())
	for _, args := range [][]string{
		{"omarchy", "bind"},
		{"omarchy", "unbind"},
	} {
		code, handled, _, errOut := runMain(t, args...)
		if code != ExitFailed || !handled || !strings.Contains(errOut, "gorex omarchy: not an Omarchy system") {
			t.Errorf("%v: %d %v %q", args, code, handled, errOut)
		}
	}
	if code, _, _, errOut := runMain(t, "omarchy", "status"); code != ExitOK {
		t.Errorf("status: %d %q", code, errOut)
	}
}

// TestOmarchyUsage reports a usage error with 2.
func TestOmarchyUsage(t *testing.T) {
	omarchyFixture(t)
	for _, args := range [][]string{
		{"omarchy"},
		{"omarchy", "bogus"},
		{"omarchy", "status", "extra"},
		{"omarchy", "bind", "extra"},
		{"omarchy", "bind", "--keys", ""},
		{"omarchy", "bind", "--keys", "bad\"keys"},
		{"omarchy", "bind", "--json"},
		{"omarchy", "unbind", "extra"},
		{"omarchy", "unbind", "--keys", "SUPER + G"},
	} {
		if code, handled, _, _ := runMain(t, args...); code != ExitUsage || !handled {
			t.Errorf("%v: %d %v", args, code, handled)
		}
	}
	if code, _, _, _ := runMain(t, "-S", "studio", "omarchy", "status"); code != ExitUsage {
		t.Errorf("remote omarchy: %d", code)
	}
}

// TestOmarchyUsageText names the command in the help and in the guide.
func TestOmarchyUsageText(t *testing.T) {
	const line = "gorex omarchy status [--json] | bind [--keys KEYS] | unbind [--json]"
	if _, _, out, _ := runMain(t, "help"); !strings.Contains(out, line) {
		t.Errorf("usage:\n%s", out)
	}
	if _, _, out, _ := runMain(t, "skill", "show"); !strings.Contains(out, line) {
		t.Errorf("the agent guide has no omarchy line")
	}
}
