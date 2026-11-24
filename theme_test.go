package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/theme"
)

// keepTheme puts the built-in palettes back after a test that applies a
// theme.
func keepTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { applyTheme(nil) })
}

// TestThemeNoneKeepsPalettes: with no theme the windows draw with the
// built-in palettes, field for field, so nothing moves.
func TestThemeNoneKeepsPalettes(t *testing.T) {
	keepTheme(t)
	m, _, err := theme.Parse([]byte(`{"seeds":{"dark":{"accent":"#ff0000"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	applyTheme(m)
	applyTheme(nil)
	if *paletteOf(false) != lightColors || *paletteOf(true) != darkColors {
		t.Error("no theme left a palette that is not the built-in one")
	}
	if themed(lightSeeds, &theme.Manifest{}, 0) != lightColors {
		t.Error("an empty manifest changed the light palette")
	}
}

// TestThemeLayers: the four layers of docs/DSK-008-theme.md, each over the
// one before: built-in seeds, the theme's seeds (through derive, so the
// ink follows them), the theme's roles, and the user's
// sidebar.colorCustomizations.
func TestThemeLayers(t *testing.T) {
	keepTheme(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	m, warn, err := theme.Parse([]byte(`{
  "name": "slate",
  "seeds": {
    "light": { "panel": "#f5f6f8", "text": "#1a1d21" },
    "dark":  { "accent": "#5b8def" }
  },
  "roles": { "sideBar.tabActiveBackground": "#5b8def33", "sideBar.trackBackground": "#123456" }
}`))
	if err != nil || len(warn) != 0 {
		t.Fatal(err, warn)
	}
	applyTheme(m)
	lightTheme, darkTheme := *paletteOf(false), *paletteOf(true)

	// 1. A seed the theme does not set keeps the built-in one.
	if lightTheme.accent != lightColors.accent || darkTheme.text != darkColors.text || darkTheme.panel != darkColors.panel {
		t.Error("an unset seed lost its built-in value")
	}
	// 2. The theme's seeds, and the ink derive makes from them.
	if lightTheme.panel != ui.Hex("#f5f6f8") || lightTheme.text != ui.Hex("#1a1d21") || darkTheme.accent != ui.Hex("#5b8def") {
		t.Error("a theme seed did not apply")
	}
	if lightTheme.panelGlass != ink(ui.Hex("#f5f6f8"), lightInk.panelGlass) || darkTheme.panelSel != ink(ui.Hex("#5b8def"), darkInk.panelSel) {
		t.Error("the ink did not follow the theme's seeds")
	}
	// 3. The theme's roles win over what derive makes.
	if darkTheme.tabActive != ui.Hex("#5b8def33") || lightTheme.track != ui.Hex("#123456") {
		t.Errorf("theme roles: tabActive %v, track %v", darkTheme.tabActive, lightTheme.track)
	}
	// 4. The user's override wins over the theme's role, and only there.
	prefs = settings{SidebarColors: map[string]json.RawMessage{"sideBar.tabActiveBackground": json.RawMessage(`"#e8e8ee"`)}}
	k := sidebarPalette(&darkTheme, true)
	if k.tabActive != ui.Hex("#e8e8ee") || k.track != ui.Hex("#123456") || k.accent != ui.Hex("#5b8def") {
		t.Errorf("user layer: tabActive %v, track %v, accent %v", k.tabActive, k.track, k.accent)
	}
	// The pairing reads the themed palettes: a light face in the dark
	// window gets the theme's light text.
	if s := surface(k, roleTabActiveBackground); s.text != ui.Hex("#1a1d21") {
		t.Errorf("a light face took text %v, want the theme's light text", s.text)
	}
}

// TestSeedAndRoleFields: each name of internal/theme's allowlist sets its
// own field, and no two names share one.
func TestSeedAndRoleFields(t *testing.T) {
	var s seeds
	seen := map[*ui.Color]bool{}
	for id := range theme.NumSeeds {
		f := seedField(&s, id)
		if f == nil || seen[f] {
			t.Errorf("seed %s: field nil or shared", id)
		}
		seen[f] = true
	}
	var k colors
	seenR := map[*ui.Color]bool{}
	for r := range theme.NumRoles {
		f := roleField(&k, r)
		if f == nil || seenR[f] {
			t.Errorf("role %s: field nil or shared", r)
		}
		seenR[f] = true
	}
}

// TestReduceTransparency: the system's Reduce Transparency changes no
// color, with no theme or with one: every face of the palette lies on the
// window's own opaque gradient, which nothing outside the window shows
// through (docs/DSK-008-theme.md, Opaque faces and materials).
func TestReduceTransparency(t *testing.T) {
	keepTheme(t)
	m, _, err := theme.Parse([]byte(`{"roles":{"sideBar.tabActiveBackground":"#5b8def33"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range []*theme.Manifest{nil, m} {
		applyTheme(mf)
		for _, dark := range []bool{false, true} {
			want := paletteOf(dark)
			saved := *want
			for _, reduce := range []bool{false, true} {
				var got *colors
				tt := ui.NewTester(func(c *ui.Context) { got = colorsOf(c) }, 200, 100)
				tt.SetDark(dark)
				tt.SetPreferences(ui.Preferences{ReduceTransparency: reduce})
				tt.Frame()
				if got != want || *got != saved {
					t.Errorf("theme %v, dark %v, reduce %v: the palette changed", mf != nil, dark, reduce)
				}
			}
		}
	}
}

// TestSettingsTerminalAndAppearanceTheme: a settings.json with both
// terminal.theme and appearance.theme keeps the Ghostty theme and selects
// the manifest of <config>/themes; the file keeps both keys. A bad entry
// of the manifest is dropped and the rest applies; a missing manifest
// leaves the built-in palettes, and the app goes on.
func TestSettingsTerminalAndAppearanceTheme(t *testing.T) {
	isolateSettings(t, "")
	keepTheme(t)
	cfg := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfg, "init.lua"))
	if err := os.MkdirAll(filepath.Join(cfg, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "themes", "slate.json"),
		[]byte(`{"name":"slate","seeds":{"dark":{"accent":"#5b8def","accent2":"#000000"},"light":{"text":"zz"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const file = `{"terminal.theme": "Nord", "appearance.theme": "slate"}`
	writeSettings(t, file)
	loadSettings()
	if prefs.TermTheme != "Nord" || prefs.Theme != "slate" {
		t.Fatalf("loaded terminal.theme %q, appearance.theme %q", prefs.TermTheme, prefs.Theme)
	}
	if paletteOf(true).accent != ui.Hex("#5b8def") || paletteOf(false).text != lightColors.text {
		t.Errorf("theme: dark accent %v, light text %v", paletteOf(true).accent, paletteOf(false).text)
	}
	saveSettings()
	raw := settingsOnDisk(t)
	if string(raw["terminal.theme"]) != `"Nord"` || string(raw["appearance.theme"]) != `"slate"` {
		t.Errorf("saved file lost a key: %v", raw)
	}

	writeSettings(t, `{"appearance.theme": "gone"}`)
	prefs = settings{}
	loadSettings()
	if *paletteOf(true) != darkColors || *paletteOf(false) != lightColors {
		t.Error("a missing theme did not leave the built-in palettes")
	}
	writeSettings(t, `{"appearance.theme": "../themes/slate"}`)
	prefs = settings{}
	loadSettings()
	if *paletteOf(true) != darkColors {
		t.Error("a theme name with a path loaded")
	}
}
