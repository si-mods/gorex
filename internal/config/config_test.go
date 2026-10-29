package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := write(t, `
gorex.mode("resize", { exclusive = true, once = true })
gorex.bind("ctrl+b>c", "tab.new")
gorex.bind("resize/left", "pane.resize.left", { amount = 2 })
gorex.action{
  name = "scratch",
  title = "Scratch",
  args = { dir = "string", ratio = "number?" },
  run = function(ctx, args) gorex.invoke("tab.new", { dir = args.dir }) end,
}
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Bindings) != 2 || c.Bindings[1].Mode != "resize" || c.Bindings[1].Args["amount"] != float64(2) {
		t.Fatalf("bindings %+v", c.Bindings)
	}
	if len(c.Modes) != 1 || !c.Modes[0].Exclusive || !c.Modes[0].Once {
		t.Fatalf("modes %+v", c.Modes)
	}
	if len(c.Actions) != 1 || len(c.Actions[0].Args) != 2 || !c.Actions[0].Args[1].Optional {
		t.Fatalf("actions %+v", c.Actions)
	}
	var called string
	c.SetInvoke(func(id string, args map[string]any) error {
		called = id
		if args["dir"] != "/tmp" {
			t.Errorf("args %v", args)
		}
		return nil
	})
	if err := c.Invoke("scratch", map[string]any{"origin": "test"}, map[string]any{"dir": "/tmp"}); err != nil {
		t.Fatal(err)
	}
	if called != "tab.new" {
		t.Fatalf("invoked %q", called)
	}
}

func TestBadSyntax(t *testing.T) {
	path := write(t, `gorex.bind(`)
	c, err := Load(path)
	if err == nil || c != nil {
		t.Fatalf("bad syntax: %v", err)
	}
}

func TestMissingIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.lua"))
	if err != nil || c == nil || len(c.Bindings) != 0 {
		t.Fatalf("missing: %+v %v", c, err)
	}
	c.Close()
}

func TestPath(t *testing.T) {
	t.Setenv("GOREX_CONFIG", "/tmp/x.lua")
	if Path() != "/tmp/x.lua" {
		t.Fatalf("path %q", Path())
	}
	t.Setenv("GOREX_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/cfg")
	if got := Path(); got != "/tmp/cfg/gorex/init.lua" {
		t.Fatalf("path %q", got)
	}
}
