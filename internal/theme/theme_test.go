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

	// A manifest of a later doc loads as its colors here: the keys and
	// roles this build does not know drop, with a warning each, and so
	// does a shader API this build does not know; the rest applies.
	m, warn, err = Parse([]byte(`{
  "seeds": { "light": { "accent": "#2876c8" } },
  "roles": { "titleBar.background": "#2876c8", "pane.border": "#2876c880", "toolBar.background": "#ffffff" },
  "shaders": { "api": 2, "background": { "source": "bg" } },
  "metrics": { "radius": 0.2 },
  "glass": false
}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Seeds[0][Accent] != ui.Hex("#2876c8") || m.Roles[0][TitleBarBackground] != ui.Hex("#2876c8") || m.Roles[1][PaneBorder] != ui.Hex("#2876c880") {
		t.Errorf("colors of a later manifest: %v %v", m.Seeds, m.Roles)
	}
	if m.Metrics == nil || m.Metrics.Radius != 0.2 || m.Glass == nil || *m.Glass {
		t.Errorf("metrics %+v, glass %v", m.Metrics, m.Glass)
	}
	want = []string{`roles: unknown role "toolBar.background"`, `shaders.api: 2 is not an API this build knows (shaders dropped)`}
	if !slices.Equal(warn, want) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, want)
	}
}

// TestParseDescription: the description key keeps a line of text: control
// characters go and it is cut to 160 characters, each with a warning; a
// value that is not a string drops. List carries it.
func TestParseDescription(t *testing.T) {
	m, warn, err := Parse([]byte(`{"name": "Blue", "description": "Blue bars\u0007 and \u202egrey panels"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Description != "Blue bars and grey panels" || len(warn) != 1 || !strings.Contains(warn[0], "description") {
		t.Errorf("description %q, warnings %q", m.Description, warn)
	}
	m, warn, _ = Parse([]byte(`{"description": "` + strings.Repeat("é", 200) + `"}`))
	if n := len([]rune(m.Description)); n != maxDescription || len(warn) != 1 {
		t.Errorf("a long description keeps %d characters, warnings %q", n, warn)
	}
	m, warn, _ = Parse([]byte(`{"description": 3}`))
	if m.Description != "" || len(warn) != 1 || warn[0] != "description: not a string" {
		t.Errorf("a number: %q %q", m.Description, warn)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "blue.json"), []byte(`{"name": "Blue", "description": "Blue bars"}`), 0o644)
	if l := List(dir); len(l) != 1 || l[0].Description != "Blue bars" {
		t.Errorf("List %+v", l)
	}
}

// TestParseSidebar: the sidebar key carries the sidebar.* settings of
// docs/DSK-007 a theme suggests. A value that is not a number, a metric
// out of range, a bad enum and an unknown key drop with a warning each.
func TestParseSidebar(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "name": "slate",
  "sidebar": {
    "rowHeight": 28,
    "indent": 12,
    "tabIndent": 8,
    "fontSize": "big",
    "groupPadding": 99,
    "groupBy": "flat",
    "groupBackground": "none",
    "icons": "wobbly",
    "selection": "fill",
    "width": 300
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Sidebar{RowHeight: 28, Indent: 12, TabIndent: 8, GroupBy: "flat", GroupFace: "none", Selection: "fill"}
	if m.Sidebar != want {
		t.Errorf("sidebar %+v, want %+v", m.Sidebar, want)
	}
	wantWarn := []string{
		`sidebar.fontSize: not a number`,
		`sidebar.groupPadding: 99 outside 0–12`,
		`sidebar.icons: bad value`,
		`sidebar: unknown key "width"`,
	}
	if !slices.Equal(warn, wantWarn) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, wantWarn)
	}
	if _, warn, err := Parse([]byte(`{"sidebar": 7}`)); err != nil || !slices.Equal(warn, []string{`sidebar: not an object`}) {
		t.Errorf("a non-object sidebar: %v %q", err, warn)
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
		if got, ok := RoleByName(r.String()); !ok || got != r || !slices.ContainsFunc([]string{"sideBar.", "titleBar.", "paneHeader.", "button.", "pane."}, func(p string) bool { return strings.HasPrefix(r.String(), p) }) {
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
