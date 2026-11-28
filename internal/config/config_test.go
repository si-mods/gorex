package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorex/internal/homedir"
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
	if len(c.Actions) != 1 || len(c.Actions[0].Args) != 2 {
		t.Fatalf("actions %+v", c.Actions)
	}
	for _, a := range c.Actions[0].Args {
		if a.Name == "ratio" && !a.Optional {
			t.Fatalf("ratio is not optional: %+v", c.Actions[0].Args)
		}
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
	t.Setenv("GOREX_DIR", "")
	home := t.TempDir()
	defer stubHomeDirs(home, t.TempDir())()
	// A legacy file wins while the move has not run.
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", t.TempDir())
	// Neither a home file nor a legacy one: the home's init.lua.
	if got, want := Path(), filepath.Join(home, ".gorex", "init.lua"); got != want {
		t.Fatalf("path %q, want %q", got, want)
	}
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("-- x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg, "gorex", "init.lua"))
	if got, want := Path(), filepath.Join(cfg, "gorex", "init.lua"); got != want {
		t.Fatalf("path %q, want %q", got, want)
	}
	// The home's file wins over the legacy one.
	write(filepath.Join(home, ".gorex", "init.lua"))
	if got, want := Path(), filepath.Join(home, ".gorex", "init.lua"); got != want {
		t.Fatalf("path %q, want %q", got, want)
	}
}

// stubHomeDirs makes homedir resolve under home, with platform's
// configuration directory plat. It returns the restore.
func stubHomeDirs(home, plat string) func() {
	oldHome, oldCfg := homedir.UserHomeDir, homedir.UserConfigDir
	homedir.UserHomeDir = func() (string, error) { return home, nil }
	homedir.UserConfigDir = func() (string, error) {
		if plat == "" {
			return "", errors.New("none")
		}
		return plat, nil
	}
	return func() { homedir.UserHomeDir, homedir.UserConfigDir = oldHome, oldCfg }
}

func TestPathPlatformFallback(t *testing.T) {
	t.Setenv("GOREX_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	plat := t.TempDir()
	defer stubHomeDirs("", plat)()
	// No XDG and no HOME: the legacy file of the platform directory.
	if err := os.MkdirAll(filepath.Join(plat, "gorex"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(plat, "gorex", "init.lua")
	if err := os.WriteFile(p, []byte("-- x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Path(); got != p {
		t.Fatalf("path %q, want %q", got, p)
	}
	// No home and no legacy data directory (on Windows the same folder
	// as the legacy configuration one): nothing to fall back to.
	if err := os.RemoveAll(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	if got := Path(); got != "" {
		t.Fatalf("path %q", got)
	}
}

func TestCheckSandbox(t *testing.T) {
	path := write(t, `os.execute("true")`)
	if _, err := LoadCheck(path); err == nil {
		t.Fatal("config check ran os.execute")
	}
}

func TestLoadTimeout(t *testing.T) {
	path := write(t, `while true do end`)
	start := time.Now()
	if _, err := Load(path); err == nil {
		t.Fatal("an endless config loaded")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took %v", time.Since(start))
	}
}

func TestWarnings(t *testing.T) {
	path := write(t, `
gorex.bind("ctrl+b", "tab.new")
gorex.bind("ctrl+b>c", "tab.close")
gorex.bind("ctrl+x", "tab.new")
gorex.bind("ctrl+x", "tab.close")
`)
	c, err := LoadCheck(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Warnings) < 2 {
		t.Fatalf("warnings %v", c.Warnings)
	}
}

func TestEvent(t *testing.T) {
	path := write(t, `
gorex.on("program_status_changed", function(ev)
  got = ev.id .. ":" .. ev.state
end)
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Emit("program_status_changed", map[string]string{"id": "agent", "state": "blocked"})
	if got := c.state.GetGlobal("got").String(); got != "agent:blocked" {
		t.Fatalf("event got %q", got)
	}
}

func TestIconPackConfig(t *testing.T) {
	path := write(t, `
gorex.icon_packs({ "catppuccin-programs", "custom" })
gorex.icon_pack_priority("osc")
gorex.program_icon("terraform", { icon = "icons/tf.svg", mode = "mono", bg = "#7b42bc", fg = "#ffffff" })
`)
	c, err := LoadCheck(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.IconPacks) != 2 || c.IconPacks[0] != "catppuccin-programs" || c.IconPacks[1] != "custom" {
		t.Fatalf("IconPacks = %v", c.IconPacks)
	}
	if c.IconPackPriority != "osc" {
		t.Fatalf("IconPackPriority = %q, want osc", c.IconPackPriority)
	}
	if len(c.ProgramIcons) != 1 || c.ProgramIcons[0].Name != "terraform" || c.ProgramIcons[0].Bg != "#7b42bc" {
		t.Fatalf("ProgramIcons = %+v", c.ProgramIcons)
	}
}
