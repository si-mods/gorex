package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
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
	m, _, err := theme.Parse([]byte(`{"seeds":{"dark":{"accent":"#ff0000","accentText":"#000000"}}}`))
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

// TestThemeAccentText: a theme's accentText is the text of the faces that
// are the accent (a menu's highlight, the stock widgets' accent), and a
// theme with none leaves the built-in ink.
func TestThemeAccentText(t *testing.T) {
	keepTheme(t)
	if lightColors.accentText != ui.Hex("#ffffff") || darkColors.accentText != ui.Hex("#10151c") {
		t.Fatal("the built-in accentText is not what GoRex drew before")
	}
	m, _, err := theme.Parse([]byte(`{"seeds":{"light":{"accent":"#f29718","accentText":"#5e3801"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	applyTheme(m)
	k := paletteOf(false)
	want := ui.Hex("#5e3801")
	if k.accentText != want {
		t.Fatalf("accentText %v, want %v", k.accentText, want)
	}
	if got := menuStyleOf(k, false).HighlightText; got != want {
		t.Errorf("menu HighlightText %v, want %v", got, want)
	}
	if got := widgetTheme(&ui.Theme{}, k).AccentText; got != want {
		t.Errorf("widget AccentText %v, want %v", got, want)
	}
	m, _, err = theme.Parse([]byte(`{"seeds":{"light":{"accent":"#f29718"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	applyTheme(m)
	if got := paletteOf(false).accentText; got != lightColors.accentText {
		t.Errorf("a theme with no accentText gave %v, want the built-in %v", got, lightColors.accentText)
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

// keepTerm puts the terminals' colors back after a test that applies them.
func keepTerm(t *testing.T) {
	t.Helper()
	l, d, o, a := termLight, termDark, termOpaque, termAppearance
	t.Cleanup(func() { termLight, termDark, termOpaque, termAppearance = l, d, o, a })
}

// writeThemeFile installs a manifest as <config>/themes/<name>.json, under
// a config folder of the test's own.
func writeThemeFile(t *testing.T, name, body string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfg, "init.lua"))
	if err := os.MkdirAll(filepath.Join(cfg, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "themes", name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ghosttyThemeOf is the Ghostty theme of a name, or fails the test.
func ghosttyThemeOf(t *testing.T, name string) *terminal.Theme {
	t.Helper()
	th, err := terminal.GhosttyTheme(name)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

const ayuManifest = `{"name":"ayu","seeds":{"dark":{"accent":"#e6b450"}},"terminal":{"light":"Ayu Light","dark":"Ayu"}}`

// TestThemeTerminal: with no terminal.theme, the theme's terminal key
// colors the terminals of each appearance and replaces the Ghostty
// configuration's theme and colors, without forcing the window's
// appearance. The user's terminal.theme wins over it
// (docs/DSK-008-theme.md, Terminal).
func TestThemeTerminal(t *testing.T) {
	isolateSettings(t, "theme = Nord\nbackground = #102030\n")
	keepTheme(t)
	keepTerm(t)
	writeThemeFile(t, "ayu", ayuManifest)
	writeSettings(t, `{"appearance.theme": "ayu"}`)
	loadSettings()
	if themeTerm != [2]string{"Ayu Light", "Ayu"} {
		t.Fatalf("themeTerm %q", themeTerm)
	}
	if termLight.Background != ghosttyThemeOf(t, "Ayu Light").Background || termDark.Background != ghosttyThemeOf(t, "Ayu").Background {
		t.Errorf("terminals: light %v, dark %v, want the ayu themes", termLight.Background, termDark.Background)
	}
	if termDark.Palette != ghosttyThemeOf(t, "Ayu").Palette {
		t.Error("the dark terminal's palette is not Ayu's")
	}
	if termAppearance != "" {
		t.Errorf("the theme forced the window's appearance %q", termAppearance)
	}

	// The user's own terminal.theme wins over the theme's.
	writeSettings(t, `{"appearance.theme": "ayu", "terminal.theme": "Dracula"}`)
	prefs = settings{}
	loadSettings()
	if paletteOf(true).accent != ui.Hex("#e6b450") {
		t.Error("the chrome theme did not apply")
	}
	if termDark.Background != ghosttyThemeOf(t, "Dracula").Background || termLight.Background != ghosttyThemeOf(t, "Dracula").Background {
		t.Errorf("terminal.theme lost to the theme: light %v, dark %v", termLight.Background, termDark.Background)
	}

	// With no theme, Ghostty's configuration colors the terminals again.
	writeSettings(t, `{}`)
	prefs = settings{}
	loadSettings()
	if themeTerm != [2]string{} || ghostty.Theme != "Nord" || termDark.Background != ui.Hex("#102030") {
		t.Errorf("no theme: themeTerm %q, Ghostty theme %q, background %v", themeTerm, ghostty.Theme, termDark.Background)
	}
}

// TestUseThemeTerminal: using a theme on the Extensions page colors the
// terminals at once, and the built-in theme gives them back.
func TestUseThemeTerminal(t *testing.T) {
	isolateSettings(t, "")
	keepTheme(t)
	keepTerm(t)
	writeThemeFile(t, "ayu", ayuManifest)
	loadSettings()
	a := &App{}
	a.useTheme("ayu")
	if termDark.Background != ghosttyThemeOf(t, "Ayu").Background {
		t.Errorf("useTheme left the dark terminal %v", termDark.Background)
	}
	a.useTheme("")
	if termDark.Background != darkTerm.Background || termLight.Background != lightTerm.Background {
		t.Error("the built-in theme kept the theme's terminal colors")
	}
}

// TestThemeTerminalKnown: the app takes a terminal name of Ghostty's list,
// in any case, and not a name that only a device or no file has.
func TestThemeTerminalKnown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for name, want := range map[string]bool{"Ayu": true, "ayu mirage": true, "Ayu Light": true, "CON": false, "NUL": false, "No Such Theme": false} {
		if got := knownTerminalTheme(name); got != want {
			t.Errorf("knownTerminalTheme(%q) = %v", name, got)
		}
	}
}
