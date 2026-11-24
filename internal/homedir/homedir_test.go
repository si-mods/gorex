package homedir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stubDirs makes the resolution work on temporary directories: the
// user's home, the platform's configuration directory and no live
// server. It returns the home and the platform directory.
func stubDirs(t *testing.T) (home, plat string) {
	t.Helper()
	home, plat = t.TempDir(), t.TempDir()
	oldHome, oldCfg, oldDial := UserHomeDir, UserConfigDir, Dial
	UserHomeDir = func() (string, error) { return home, nil }
	UserConfigDir = func() (string, error) { return plat, nil }
	Dial = func(string) bool { return false }
	t.Cleanup(func() { UserHomeDir, UserConfigDir, Dial = oldHome, oldCfg, oldDial })
	for _, v := range []string{"GOREX_DIR", "GOREX_CONFIG", "XDG_CONFIG_HOME"} {
		t.Setenv(v, "")
	}
	t.Setenv("HOME", home)
	return home, plat
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDataDirResolve(t *testing.T) {
	home, plat := stubDirs(t)
	root := filepath.Join(home, ".gorex")
	legacy := filepath.Join(plat, "GoRex")
	if got := DataDir(false); got != root {
		t.Fatalf("nothing: got %q, want %q", got, root)
	}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DataDir(false); got != legacy {
		t.Fatalf("legacy only: got %q, want %q", got, legacy)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DataDir(false); got != root {
		t.Fatalf("both: got %q, want %q", got, root)
	}
	t.Setenv("GOREX_DIR", "/env/dir")
	if got := DataDir(false); got != "/env/dir" {
		t.Fatalf("env: got %q", got)
	}
}

func TestDevDirResolve(t *testing.T) {
	home, plat := stubDirs(t)
	if got, want := DataDir(true), filepath.Join(home, ".gorex-dev"); got != want {
		t.Fatalf("nothing: got %q, want %q", got, want)
	}
	legacy := filepath.Join(plat, "GoRex Dev")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DataDir(true); got != legacy {
		t.Fatalf("legacy only: got %q, want %q", got, legacy)
	}
}

// TestConfigPathResolve: init.lua follows the data directory when no
// init.lua exists, so resolving it never makes the home before the move.
func TestConfigPathResolve(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home, plat string) string // returns the want
	}{
		{"fresh machine", func(t *testing.T, home, plat string) string {
			return filepath.Join(home, ".gorex", "init.lua")
		}},
		{"legacy data only", func(t *testing.T, home, plat string) string {
			write(t, filepath.Join(plat, "GoRex", "settings.json"), "{}")
			return filepath.Join(plat, "GoRex", "init.lua")
		}},
		{"home exists", func(t *testing.T, home, plat string) string {
			write(t, filepath.Join(plat, "GoRex", "settings.json"), "{}")
			write(t, filepath.Join(home, ".gorex", "settings.json"), "{}")
			return filepath.Join(home, ".gorex", "init.lua")
		}},
		{"legacy init.lua", func(t *testing.T, home, plat string) string {
			write(t, filepath.Join(plat, "GoRex", "settings.json"), "{}")
			write(t, filepath.Join(home, ".config", "gorex", "init.lua"), "-- x")
			return filepath.Join(home, ".config", "gorex", "init.lua")
		}},
		{"home init.lua", func(t *testing.T, home, plat string) string {
			write(t, filepath.Join(home, ".config", "gorex", "init.lua"), "-- x")
			write(t, filepath.Join(home, ".gorex", "init.lua"), "-- x")
			return filepath.Join(home, ".gorex", "init.lua")
		}},
		{"GOREX_DIR", func(t *testing.T, home, plat string) string {
			write(t, filepath.Join(plat, "GoRex", "settings.json"), "{}")
			d := filepath.Join(t.TempDir(), "data")
			t.Setenv("GOREX_DIR", d)
			return filepath.Join(d, "init.lua")
		}},
		{"GOREX_CONFIG wins", func(t *testing.T, home, plat string) string {
			write(t, filepath.Join(home, ".gorex", "init.lua"), "-- x")
			p := filepath.Join(t.TempDir(), "own.lua")
			t.Setenv("GOREX_CONFIG", p)
			return p
		}},
		{"GOREX_DIR and GOREX_CONFIG", func(t *testing.T, home, plat string) string {
			d := t.TempDir()
			t.Setenv("GOREX_DIR", d)
			t.Setenv("GOREX_CONFIG", filepath.Join(d, "config", "init.lua"))
			return filepath.Join(d, "config", "init.lua")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, plat := stubDirs(t)
			homeMade := false
			want := tc.setup(t, home, plat)
			if _, err := os.Lstat(filepath.Join(home, ".gorex")); err == nil {
				homeMade = true
			}
			if got := ConfigPath(); got != want {
				t.Fatalf("ConfigPath() = %q, want %q", got, want)
			}
			if _, err := os.Lstat(filepath.Join(home, ".gorex")); err == nil && !homeMade {
				t.Fatal("resolving made ~/.gorex")
			}
		})
	}
}

func TestMigrate(t *testing.T) {
	home, plat := stubDirs(t)
	root := filepath.Join(home, ".gorex")
	data := filepath.Join(plat, "GoRex")
	conf := filepath.Join(home, ".config", "gorex")
	write(t, filepath.Join(data, "settings.json"), `{"a":1}`)
	write(t, filepath.Join(data, "server", "v"), "x")
	write(t, filepath.Join(conf, "init.lua"), "-- conf")
	write(t, filepath.Join(conf, "themes", "x.json"), "{}")
	var after [2]string
	moved, err := Migrate(false, func(legacy, r string) { after = [2]string{legacy, r} })
	if err != nil || !moved {
		t.Fatalf("moved %v err %v", moved, err)
	}
	if after != [2]string{data, root} {
		t.Fatalf("afterMove %v", after)
	}
	if got := read(t, filepath.Join(root, "settings.json")); got != `{"a":1}` {
		t.Fatalf("settings %q", got)
	}
	if got := read(t, filepath.Join(root, "init.lua")); got != "-- conf" {
		t.Fatalf("init.lua %q", got)
	}
	if got := read(t, filepath.Join(root, "themes", "x.json")); got != "{}" {
		t.Fatalf("theme %q", got)
	}
	// The legacy paths link to the home.
	for _, p := range []string{data, conf} {
		st, err := os.Lstat(p)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s: not a link (%v)", p, err)
		}
		if target, _ := os.Readlink(p); target != root {
			t.Fatalf("%s -> %s", p, target)
		}
	}
	// A second run does nothing.
	if moved, err := Migrate(false, nil); moved || err != nil {
		t.Fatalf("second run: moved %v err %v", moved, err)
	}
}

// TestMigrateKeepsDataThemes: a theme installed into the legacy data
// directory before the move (ConfigPath follows the data there) moves
// with it, and the legacy init.lua joins it.
func TestMigrateKeepsDataThemes(t *testing.T) {
	home, plat := stubDirs(t)
	data := filepath.Join(plat, "GoRex")
	write(t, filepath.Join(data, "settings.json"), "{}")
	write(t, filepath.Join(data, "themes", "sea.json"), "{}")
	write(t, filepath.Join(home, ".config", "gorex", "init.lua"), "-- conf")
	if moved, err := Migrate(false, nil); err != nil || !moved {
		t.Fatalf("moved %v err %v", moved, err)
	}
	root := filepath.Join(home, ".gorex")
	if got := read(t, filepath.Join(root, "themes", "sea.json")); got != "{}" {
		t.Fatalf("theme %q", got)
	}
	if got := read(t, filepath.Join(root, "init.lua")); got != "-- conf" {
		t.Fatalf("init.lua %q", got)
	}
	if got := ConfigPath(); got != filepath.Join(root, "init.lua") {
		t.Fatalf("ConfigPath() = %q", got)
	}
}

// TestMigrateConflict: a name the data directory already has stays, and
// the legacy configuration file is left in place.
func TestMigrateConflict(t *testing.T) {
	home, plat := stubDirs(t)
	data := filepath.Join(plat, "GoRex")
	conf := filepath.Join(home, ".config", "gorex")
	write(t, filepath.Join(data, "init.lua"), "-- data")
	write(t, filepath.Join(conf, "init.lua"), "-- conf")
	if moved, err := Migrate(false, nil); err != nil || !moved {
		t.Fatalf("moved %v err %v", moved, err)
	}
	root := filepath.Join(home, ".gorex")
	if got := read(t, filepath.Join(root, "init.lua")); got != "-- data" {
		t.Fatalf("init.lua %q", got)
	}
	if got := read(t, filepath.Join(conf, "init.lua")); got != "-- conf" {
		t.Fatalf("leftover %q", got)
	}
}

func TestMigrateKeepsRunningServer(t *testing.T) {
	home, plat := stubDirs(t)
	data := filepath.Join(plat, "GoRex")
	write(t, filepath.Join(data, "settings.json"), "{}")
	Dial = func(string) bool { return true }
	if moved, err := Migrate(false, nil); moved || err != nil {
		t.Fatalf("moved %v err %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(data, "settings.json")); err != nil {
		t.Fatalf("data moved anyway: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".gorex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home exists anyway")
	}
}

func TestMigrateExistingHome(t *testing.T) {
	home, plat := stubDirs(t)
	root := filepath.Join(home, ".gorex")
	data := filepath.Join(plat, "GoRex")
	write(t, filepath.Join(root, "init.lua"), "-- new")
	write(t, filepath.Join(data, "settings.json"), "{}")
	if moved, err := Migrate(false, nil); moved || err != nil {
		t.Fatalf("moved %v err %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(data, "settings.json")); err != nil {
		t.Fatalf("legacy touched: %v", err)
	}
}

func TestMigrateConfigOnly(t *testing.T) {
	home, _ := stubDirs(t)
	conf := filepath.Join(home, ".config", "gorex")
	write(t, filepath.Join(conf, "init.lua"), "-- conf")
	if moved, err := Migrate(false, nil); err != nil || !moved {
		t.Fatalf("moved %v err %v", moved, err)
	}
	if got := read(t, filepath.Join(home, ".gorex", "init.lua")); got != "-- conf" {
		t.Fatalf("init.lua %q", got)
	}
}

func TestMigrateNamedDirectories(t *testing.T) {
	home, plat := stubDirs(t)
	data := filepath.Join(plat, "GoRex")
	write(t, filepath.Join(data, "settings.json"), "{}")
	for _, v := range []string{"GOREX_DIR", "GOREX_CONFIG"} {
		t.Setenv(v, "/own/path")
		if moved, err := Migrate(false, nil); moved || err != nil {
			t.Fatalf("%s: moved %v err %v", v, moved, err)
		}
		t.Setenv(v, "")
	}
	if _, err := os.Lstat(filepath.Join(home, ".gorex")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("home exists anyway")
	}
}

// TestMigrateDevKeepsConfig: a development build moves its own data
// directory only; the shared configuration directory is not its to take.
func TestMigrateDevKeepsConfig(t *testing.T) {
	home, plat := stubDirs(t)
	data := filepath.Join(plat, "GoRex Dev")
	conf := filepath.Join(home, ".config", "gorex")
	write(t, filepath.Join(data, "settings.json"), "{}")
	write(t, filepath.Join(conf, "init.lua"), "-- conf")
	if moved, err := Migrate(true, nil); err != nil || !moved {
		t.Fatalf("moved %v err %v", moved, err)
	}
	if got := read(t, filepath.Join(home, ".gorex-dev", "settings.json")); got != "{}" {
		t.Fatalf("settings %q", got)
	}
	if got := read(t, filepath.Join(conf, "init.lua")); got != "-- conf" {
		t.Fatalf("the shared config moved: %q", got)
	}
}
