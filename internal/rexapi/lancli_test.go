package rexapi

import (
	"os"
	"slices"
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
