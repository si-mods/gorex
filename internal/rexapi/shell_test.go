package rexapi

import (
	"encoding/json"
	"runtime"
	"slices"
	"testing"
)

// TestShellOptionsFromIPhone reads the options Rex's iPhone app sends with
// a new window, its theme among them, which GoRex once refused.
func TestShellOptionsFromIPhone(t *testing.T) {
	var o args
	raw := `{"theme":{"scheme":"dark","background":"#282A37","palette":["0=#20222D","15=#FDFDFA"],"foreground":"#F7F7EF","cursor":"#F7F7EF"}}`
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	so, aerr := parseShellOptions(o)
	if aerr != nil {
		t.Fatalf("parse: %v", aerr.Message)
	}
	if so.theme == nil || so.theme.Background != "#282A37" || so.theme.Scheme != "dark" || len(so.theme.Palette) != 2 {
		t.Errorf("theme = %+v", so.theme)
	}
	if so.command != nil {
		t.Errorf("command = %q, want the default shell", so.command)
	}
}

func TestShellOptionsRefuse(t *testing.T) {
	for _, raw := range []string{
		`{"colour":"red"}`,
		`{"command":["ls"],"args":["-l"]}`,
		`{"shell":"none"}`,
		`{"size":{"columns":0,"rows":10}}`,
		`{"theme":{"background":"blue"}}`,
		`{"exit":{"never":true}}`,
	} {
		var o args
		json.Unmarshal([]byte(raw), &o)
		if _, aerr := parseShellOptions(o); aerr == nil || aerr.Code != "bad_request" {
			t.Errorf("%s: %v, want bad_request", raw, aerr)
		}
	}
}

func TestPaneCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows panes take a command as given")
	}
	t.Setenv("SHELL", "/bin/zsh")
	for _, c := range []struct {
		shell   string
		login   bool
		command []string
		argv    []string
		prepend []string
		want    []string
	}{
		{"", true, nil, nil, nil, nil},
		{"none", true, []string{"htop", "-d", "5"}, nil, nil, []string{"htop", "-d", "5"}},
		{"", true, []string{"git", "log"}, nil, nil, []string{"/bin/zsh", "-l", "-c", `exec "$@"`, "/bin/zsh", "git", "log"}},
		{"fish", false, nil, nil, nil, []string{"fish"}},
		{"", true, nil, []string{"-i"}, nil, []string{"/bin/zsh", "-i"}},
		{"", true, nil, nil, []string{"/opt/bin"}, []string{"/bin/sh", "-c", `PATH="/opt/bin:$PATH"; export PATH; exec "$@"`, "sh", "/bin/zsh", "-l"}},
	} {
		if got := paneCommand(c.shell, c.login, c.command, c.argv, c.prepend); !slices.Equal(got, c.want) {
			t.Errorf("paneCommand(%q, %v, %q, %q, %q) = %q, want %q", c.shell, c.login, c.command, c.argv, c.prepend, got, c.want)
		}
	}
}
