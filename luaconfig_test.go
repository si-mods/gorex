package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLuaConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "init.lua")
	body := `
gorex.mode("resize", { exclusive = true, once = true })
gorex.bind("ctrl+b>c", "tab.new")
gorex.bind("ctrl+x", "mode.enter", { name = "resize", once = true })
gorex.bind("resize/right", "pane.resize.right")
gorex.action{
  name = "scratch",
  title = "Scratch",
  run = function(ctx, args) gorex.invoke("tab.rename", { name = "lua" }) end,
}
gorex.bind("ctrl+shift+l", "scratch")
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOREX_CONFIG", path)
	a, _ := newTestApp(t)
	t.Cleanup(func() {
		setCustomCommands(nil)
		if a.luaConfig != nil {
			a.luaConfig.Close()
		}
	})
	if err := a.loadLuaConfig(); err != nil {
		t.Fatal(err)
	}
	cmd, _, ok := a.keys.handle(ui.Ctrl, ui.KeyB)
	if cmd != nil {
		t.Fatalf("prefix ran %s", cmd.ID)
	}
	if !ok {
		t.Fatal("prefix not taken")
	}
	if cmd, _, _ = a.keys.handle(0, ui.KeyC); cmd == nil || cmd.ID != "tab.new" {
		t.Fatalf("sequence: %v", cmd)
	}
	if cmd, _, _ = a.keys.handle(ui.Ctrl, ui.KeyX); cmd == nil || cmd.ID != "mode.enter" {
		t.Fatalf("mode enter: %v", cmd)
	}
	if err := cmd.invoke(a, map[string]any{"name": "resize", "once": true}); err != nil {
		t.Fatal(err)
	}
	if a.keys.currentMode() != "resize" {
		t.Fatalf("mode %q", a.keys.currentMode())
	}
	if cmd, _, _ = a.keys.handle(0, ui.KeyRight); cmd == nil || cmd.ID != "pane.resize.right" {
		t.Fatalf("mode binding: %v", cmd)
	}
	if a.keys.currentMode() != "" {
		t.Fatal("once did not leave the mode")
	}
	if cmd, _, _ = a.keys.handle(ui.Ctrl|ui.Shift, ui.KeyL); cmd == nil || cmd.ID != "scratch" {
		t.Fatalf("custom binding: %v", cmd)
	}
	if err := cmd.invoke(a, nil); err != nil {
		t.Fatal(err)
	}
	if a.tab().Name != "lua" {
		t.Fatalf("custom action did not run: %q", a.tab().Name)
	}
	if err := os.WriteFile(path, []byte(`gorex.bind(`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.loadLuaConfig(); err == nil {
		t.Fatal("bad config loaded")
	}
	if _, _, ok := a.keys.handle(ui.Ctrl, ui.KeyB); !ok {
		t.Fatal("the old keymap was lost")
	}
}
