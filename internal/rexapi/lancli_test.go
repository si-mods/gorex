package rexapi

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestLANCommand sets the listener from the command line, with no GoRex
// server: the state changes, for the server to follow when it runs.
func TestLANCommand(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Setenv("GOREX_REXAPI_SOCKET", shortSocket(t))
	quiet(t)
	dir := TailnetDir()
	for _, step := range []struct {
		args []string
		code int
		want func(LANConfig) bool
	}{
		{[]string{"listen", "192.168.88.241:7297"}, 0, func(c LANConfig) bool { return c.Listen == "192.168.88.241:7297" && !c.Enabled }},
		{[]string{"allow", "10.6.0.2"}, 0, func(c LANConfig) bool { return slices.Equal(c.Allow, []string{"10.6.0.2/32"}) }},
		{[]string{"allow", "192.168.88.0/24"}, 0, func(c LANConfig) bool { return len(c.Allow) == 2 }},
		{[]string{"allow", "10.6.0.2"}, 0, func(c LANConfig) bool { return len(c.Allow) == 2 }},
		{[]string{"on"}, 0, func(c LANConfig) bool { return c.Enabled && c.Listen == "192.168.88.241:7297" }},
		{[]string{"deny", "10.6.0.2/32"}, 0, func(c LANConfig) bool { return slices.Equal(c.Allow, []string{"192.168.88.0/24"}) }},
		{[]string{"off"}, 0, func(c LANConfig) bool { return !c.Enabled && len(c.Allow) == 1 }},
		{[]string{"allow", "everyone"}, 2, func(c LANConfig) bool { return len(c.Allow) == 1 }},
		{[]string{"listen", "studio"}, 2, func(c LANConfig) bool { return c.Listen == "192.168.88.241:7297" }},
		{[]string{"status"}, 0, func(LANConfig) bool { return true }},
		{[]string{"sideways"}, 2, func(LANConfig) bool { return true }},
	} {
		if code := lanMain(step.args); code != step.code {
			t.Fatalf("lan %v: exit %d, want %d", step.args, code, step.code)
		}
		c, err := LoadLAN(dir)
		if err != nil || !step.want(c) {
			t.Fatalf("after lan %v: %+v, %v", step.args, c, err)
		}
	}
}

// TestLANTokenCommands shows and rotates the pairing token, keeps the
// legacy list to single addresses, and puts the token in lan qr's link.
func TestLANTokenCommands(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Setenv("GOREX_REXAPI_SOCKET", shortSocket(t))
	dir := TailnetDir()
	run := func(code int, args ...string) string {
		t.Helper()
		var got int
		out := captureStdout(t, func() { got = lanMain(args) })
		if got != code {
			t.Fatalf("lan %v: exit %d, want %d (%s)", args, got, code, out)
		}
		return out
	}

	run(0, "listen", "192.0.2.10:7297")
	run(0, "allow", "192.0.2.0/24")
	c, _ := LoadLAN(dir)
	if len(c.Token) != 43 {
		t.Fatalf("no token made with the settings: %+v", c)
	}
	if out := strings.TrimSpace(run(0, "token", "show")); out != c.Token {
		t.Errorf("token show = %q, want %q", out, c.Token)
	}
	if out := run(0, "status"); strings.Contains(out, c.Token) {
		t.Errorf("status shows the token: %s", out)
	}
	run(0, "token", "rotate")
	rotated, _ := LoadLAN(dir)
	if rotated.Token == c.Token || len(rotated.Token) != 43 {
		t.Errorf("token rotate: %q, was %q", rotated.Token, c.Token)
	}
	if out := strings.TrimSpace(run(0, "token", "show")); out != rotated.Token {
		t.Errorf("token show after rotate = %q", out)
	}
	run(2, "token", "sideways")

	run(0, "legacy", "add", "192.0.2.77")
	run(0, "legacy", "add", "192.0.2.77/32")
	run(2, "legacy", "add", "192.0.2.0/24")
	run(2, "legacy", "add", "phone")
	if c, _ := LoadLAN(dir); !slices.Equal(c.LegacyOpen, []string{"192.0.2.77"}) {
		t.Errorf("legacy list = %v", c.LegacyOpen)
	}
	if out := strings.TrimSpace(run(0, "legacy", "list")); out != "192.0.2.77" {
		t.Errorf("legacy list printed %q", out)
	}
	run(0, "legacy", "remove", "192.0.2.77")
	if c, _ := LoadLAN(dir); len(c.LegacyOpen) != 0 {
		t.Errorf("legacy list after remove = %v", c.LegacyOpen)
	}
	if c, _ := LoadLAN(dir); c.Token != rotated.Token {
		t.Errorf("a legacy change moved the token")
	}

	saveLANStatus(dir, LANStatus{Running: true, WTListening: "192.0.2.10:7298", WTCertHash: "test-hash"})
	out := run(0, "qr", "--origin", "https://web.example")
	link, _, _ := strings.Cut(out, "\n")
	want := "https://web.example/#host=https%3A%2F%2F192.0.2.10%3A7298&wt=sha256-test-hash&k=" + rotated.Token
	if link != want {
		t.Errorf("lan qr link = %q, want %q", link, want)
	}
}

// captureStdout returns what f prints.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut := os.Stdout, os.Stderr
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout, os.Stderr = w, null
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() {
		os.Stdout, os.Stderr = out, errOut
		null.Close()
	}()
	f()
	w.Close()
	return <-done
}

// quiet sends what a command prints to nowhere for the test.
func quiet(t *testing.T) {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	t.Cleanup(func() {
		os.Stdout, os.Stderr = out, errOut
		null.Close()
	})
}
