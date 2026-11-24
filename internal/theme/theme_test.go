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

// TestParseTerminal: the terminal key names a Ghostty theme for each
// appearance. A name with a path, a name Ghostty does not have, a value
// that is not a string and an unknown appearance are dropped with a
// warning each; the rest of the manifest loads.
func TestParseTerminal(t *testing.T) {
	old := TerminalThemeKnown
	t.Cleanup(func() { TerminalThemeKnown = old })
	TerminalThemeKnown = func(name string) bool { return name == "Ayu" || name == "Ayu Light" }

	m, warn, err := Parse([]byte(`{"name":"ayu","terminal":{"light":"Ayu Light","dark":"Ayu"},"seeds":{"dark":{"accent":"#e6b450"}}}`))
	if err != nil || len(warn) != 0 {
		t.Fatalf("Parse: %v, warnings %q", err, warn)
	}
	if m.Terminal != [2]string{"Ayu Light", "Ayu"} || m.Seeds[1][Accent] != ui.Hex("#e6b450") {
		t.Errorf("terminal %q, seeds %v", m.Terminal, m.Seeds)
	}
	for _, tc := range []struct{ in, warn string }{
		{`{"light":"../../etc/passwd"}`, `terminal.light: bad theme name`},
		{`{"dark":"C:\\x"}`, `terminal.dark: bad theme name`},
		{`{"dark":"Ayu,light:Nord"}`, `terminal.dark: bad theme name`},
		{`{"dark":""}`, `terminal.dark: bad theme name`},
		{`{"dark":7}`, `terminal.dark: bad theme name`},
		{`{"dark":"No Such Theme"}`, `terminal.dark: unknown Ghostty theme "No Such Theme"`},
		{`{"dim":"Ayu"}`, `terminal: unknown appearance "dim"`},
	} {
		m, warn, err := Parse([]byte(`{"terminal":` + tc.in + `}`))
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if m.Terminal != [2]string{} || !slices.Equal(warn, []string{tc.warn}) {
			t.Errorf("%s: terminal %q, warnings %q, want %q", tc.in, m.Terminal, warn, tc.warn)
		}
	}
	// With no check set, only the form of a name counts.
	TerminalThemeKnown = nil
	if m, warn, _ := Parse([]byte(`{"terminal":{"dark":"Some User Theme"}}`)); m.Terminal[1] != "Some User Theme" || len(warn) != 0 {
		t.Errorf("unchecked name: %q, %q", m.Terminal, warn)
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
