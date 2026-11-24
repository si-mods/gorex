package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestParseDrops: a manifest with a bad hex, an unknown seed, an unknown
// role, an unknown appearance and an unknown key loads what is good and
// warns once for each entry it drops (docs/DSK-008-theme.md, Manifest).
func TestParseDrops(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "name": "slate",
  "seeds": {
    "light": { "bgTop": "#f5f6f8", "text": "#1a1d21", "accent": "nope", "background": "#ffffff" },
    "dark":  { "panel": "#16181d", "text": 7 },
    "dim":   { "text": "#888888" }
  },
  "roles": {
    "sideBar.tabActiveBackground": "#5b8def33",
    "sideBar.trackBackground": { "light": "#ffffff80", "dark": "#00000080" },
    "sideBar.tabSeparator": { "light": "#ffffff" },
    "editor.background": "#000000"
  },
  "command": "echo hi"
}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "slate" {
		t.Errorf("name %q", m.Name)
	}
	if got := m.Seeds[0]; len(got) != 2 || got[BgTop] != ui.Hex("#f5f6f8") || got[Text] != ui.Hex("#1a1d21") {
		t.Errorf("light seeds %v", got)
	}
	if got := m.Seeds[1]; len(got) != 1 || got[Panel] != ui.Hex("#16181d") {
		t.Errorf("dark seeds %v", got)
	}
	if m.Roles[0][TabActiveBackground] != ui.Hex("#5b8def33") || m.Roles[1][TabActiveBackground] != ui.Hex("#5b8def33") {
		t.Errorf("a plain role value is not the same in both: %v", m.Roles)
	}
	if m.Roles[0][TrackBackground] != ui.Hex("#ffffff80") || m.Roles[1][TrackBackground] != ui.Hex("#00000080") {
		t.Errorf("a light/dark pair: %v", m.Roles)
	}
	if _, ok := m.Roles[0][TabSeparator]; ok {
		t.Error("a pair without a dark value was kept")
	}
	want := []string{
		`roles.sideBar.tabSeparator: bad color`,
		`roles: unknown role "editor.background"`,
		`seeds.dark.text: bad color`,
		`seeds.light.accent: bad color`,
		`seeds.light: unknown seed "background"`,
		`seeds: unknown appearance "dim"`,
		`unknown key "command"`,
	}
	if !slices.Equal(warn, want) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, want)
	}
}

// TestParseFails: only input that is not a manifest object fails.
func TestParseFails(t *testing.T) {
	for _, in := range []string{``, `[]`, `{"seeds": []}`, `{"roles": {"sideBar.trackBackground": 1}`, strings.Repeat(" ", maxManifestBytes+1)} {
		if _, _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%.20q) did not fail", in)
		}
	}
}

// TestNames: every seed and role has a name, and the names map back.
func TestNames(t *testing.T) {
	for s := range NumSeeds {
		if got, ok := SeedByName(s.String()); !ok || got != s {
			t.Errorf("seed %d %q", s, s.String())
		}
	}
	for r := range NumRoles {
		if got, ok := RoleByName(r.String()); !ok || got != r || !strings.HasPrefix(r.String(), "sideBar.") {
			t.Errorf("role %d %q", r, r.String())
		}
	}
}

// TestPath: a theme name stays inside the themes folder.
func TestPath(t *testing.T) {
	dir := filepath.Join("cfg", "themes")
	if p, err := Path(dir, "slate"); err != nil || p != filepath.Join(dir, "slate.json") {
		t.Errorf("Path slate = %q, %v", p, err)
	}
	for _, bad := range []string{"", ".", "..", "../x", `..\x`, "a/b", "/abs", "C:x", "a b"} {
		if _, err := Path(dir, bad); err == nil {
			t.Errorf("Path(%q) did not fail", bad)
		}
	}
	if _, err := Path("", "slate"); err == nil {
		t.Error("Path with no config dir did not fail")
	}
}

// TestInstall: a theme pack's theme.json installs as <dir>/<name>.json,
// with the warnings of what it drops; one that is not a manifest does not.
func TestInstall(t *testing.T) {
	src, dir := t.TempDir(), filepath.Join(t.TempDir(), "themes")
	body := `{"name":"slate","seeds":{"dark":{"accent":"#5b8def","glow":"#fff"}}}`
	if err := os.WriteFile(filepath.Join(src, PackFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	warn, err := Install(src, dir, "slate")
	if err != nil {
		t.Fatal(err)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], `"glow"`) {
		t.Errorf("warnings %q", warn)
	}
	m, _, err := Load(filepath.Join(dir, "slate.json"))
	if err != nil || m.Seeds[1][Accent] != ui.Hex("#5b8def") {
		t.Fatalf("installed theme %+v, %v", m, err)
	}
	if err := os.WriteFile(filepath.Join(src, PackFile), []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(src, dir, "broken"); err == nil {
		t.Error("a pack that is not a manifest installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "broken.json")); err == nil {
		t.Error("a failed install left a file")
	}
	if _, err := Install(src, dir, "../escape"); err == nil {
		t.Error("a name outside the folder installed")
	}
	big := `{"name":"` + strings.Repeat("x", maxManifestBytes) + `"}`
	if err := os.WriteFile(filepath.Join(src, PackFile), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(src, dir, "big"); err == nil {
		t.Error("a theme.json over the size limit installed")
	}
	if _, _, err := Load(filepath.Join(src, PackFile)); err == nil {
		t.Error("Load read a theme.json over the size limit")
	}
	if _, err := Install(t.TempDir(), dir, "empty"); err == nil {
		t.Error("a pack with no theme.json installed")
	}
}

// TestListAndRemove: List shows each installed theme by ID with its name,
// and one that does not load with its error; Remove deletes one.
func TestListAndRemove(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"slate.json":  `{"name":"Slate"}`,
		"broken.json": `nope`,
		"notes.txt":   `{}`,
		"a b.json":    `{}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := List(dir)
	if len(got) != 2 || got[0].ID != "broken" || got[0].Error == "" || got[1] != (Summary{ID: "slate", Name: "Slate"}) {
		t.Fatalf("List = %+v", got)
	}
	if err := Remove(dir, "slate"); err != nil {
		t.Fatal(err)
	}
	if got := List(dir); len(got) != 1 {
		t.Errorf("after Remove: %+v", got)
	}
	if Remove(dir, "../x") == nil {
		t.Error("Remove took a path")
	}
	if List(filepath.Join(dir, "none")) != nil {
		t.Error("a missing folder listed themes")
	}
}
