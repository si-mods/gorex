package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/theme"
)

// applyManifest parses body and applies it, or fails the test.
func applyManifest(t *testing.T, body string) {
	t.Helper()
	m, warn, err := theme.Parse([]byte(body))
	if err != nil || len(warn) != 0 {
		t.Fatal(err, warn)
	}
	applyTheme(m)
}

// TestThemeMetrics: a theme's metrics (docs/DSK-012-theme-shaders.md)
// reach the drawing through k. radius 0 squares the panes, the rows and the
// menus, and concentric stays at 0, not below; border sets the width of
// the borders; shadow.hard gives a blur of 0; the font is the first family
// of the list that is installed, the system's when none is. A theme
// without metrics draws with the built-in sizes, the same numbers.
func TestThemeMetrics(t *testing.T) {
	keepTheme(t)
	registerFonts()
	k := paletteOf(false)
	if k.m != builtinMetrics || k.radius(radiusM) != radiusM || k.capsule(7) != 3.5 || k.borderW() != 1 || (&App{}).cardR(k) != cardRadius(0, "") {
		t.Fatalf("built-in metrics %+v", k.m)
	}
	if y, blur := k.shadowAt(12, 32); y != 12 || blur != 32 {
		t.Errorf("built-in shadow %v, %v", y, blur)
	}
	applyManifest(t, `{"seeds":{"light":{"accent":"#2876c8"}}}`)
	if paletteOf(false).m != builtinMetrics || paletteOf(true).m != builtinMetrics {
		t.Error("a theme without metrics changed the sizes")
	}

	applyManifest(t, `{"metrics":{"radius":0,"border":2,"font":["No Such Family Q7","rounded","JetBrains Mono","system"],"shadow":{"scale":0.5,"hard":true}}}`)
	for _, dark := range []bool{false, true} {
		k := paletteOf(dark)
		if k.radius(radiusXXL) != 0 || k.capsule(28) != 0 || (&App{}).cardR(k) != 0 || pillRadius(k, 28, k.radius(radiusM)) != 0 {
			t.Errorf("radius 0: a corner is not square")
		}
		if r := concentric(k.radius(radiusL), 6); r != 0 {
			t.Errorf("concentric of a square corner %v, want 0", r)
		}
		if r := menuStyleOf(k, dark).RowRadius; r != 0 {
			t.Errorf("menu row radius %v", r)
		}
		if k.borderW() != 2 {
			t.Errorf("border %v, want 2", k.borderW())
		}
		if y, blur := k.shadowAt(12, 32); y != 6 || blur != 0 {
			t.Errorf("hard shadow at half: %v, %v; want 6, 0", y, blur)
		}
		want := "JetBrains Mono"
		if uiFont != "" {
			want += ", " + uiFont
		}
		if k.m.font != want || widgetTheme(&ui.Theme{}, k).Font != want || menuTheme(&ui.Theme{}, k).Font != want {
			t.Errorf("font %q, want %q", k.m.font, want)
		}
	}
	for _, list := range [][]string{nil, {"No Such Family Q7"}, {"system", "JetBrains Mono"}, {"rounded"}} {
		if got := chromeFont(list); got != uiFont {
			t.Errorf("chromeFont(%q) = %q, want the system's %q", list, got, uiFont)
		}
	}

	// radius 0.5 halves a token and the panes' corners.
	applyManifest(t, `{"metrics":{"radius":0.5}}`)
	k = paletteOf(false)
	if k.radius(radiusM) != radiusM/2 || (&App{}).cardR(k) != cardRadius(0, "")/2 || k.m.font != uiFont || k.borderW() != 1 {
		t.Errorf("radius 0.5: %+v", k.m)
	}
}

// TestThemeGlass: a theme's glass key decides the glass over the user's
// setting, which settings.json keeps; with no theme, or a theme without
// the key, the user's setting applies again. The Settings switch shows the
// glass in use, says which theme sets it, and takes no change.
func TestThemeGlass(t *testing.T) {
	isolateSettings(t, "")
	keepTheme(t)
	writeThemeFile(t, "flat", `{"name":"Flat","glass":false}`)
	a := &App{}
	a.useTheme("flat")
	if glassOn() || prefs.NoGlass {
		t.Fatalf("glass false over noGlass false: glass %v, noGlass %v", glassOn(), prefs.NoGlass)
	}
	if _, ok := settingsOnDisk(t)["noGlass"]; ok {
		t.Error("the theme's glass went to settings.json")
	}

	tt := ui.NewTester(func(c *ui.Context) { a.appearanceGlass(c, colorsOf(c)) }, 600, 300)
	tt.Frame()
	if !tt.HasText("Set by the theme Flat.") {
		t.Errorf("the switch does not say what sets it: %q", tt.Texts())
	}
	// The switch is at the row's end, past its label.
	row, ok := tt.Find("Glass")
	if !ok {
		t.Fatal("no Glass row")
	}
	tt.ClickAt(560, row.Y+row.H/2)
	tt.Frame()
	if prefs.NoGlass || glassOn() {
		t.Error("the switch changed the glass while the theme decides")
	}

	writeThemeFile(t, "clear", `{"name":"Clear","glass":true}`)
	a.setGlass(false)
	a.useTheme("clear")
	if !glassOn() || !prefs.NoGlass {
		t.Errorf("glass true over noGlass true: glass %v, noGlass %v", glassOn(), prefs.NoGlass)
	}
	if string(settingsOnDisk(t)["noGlass"]) != "true" {
		t.Error("settings.json lost the user's noGlass")
	}
	a.useTheme("")
	if glassOn() {
		t.Error("with no theme the user's noGlass did not apply again")
	}
	tt.Frame()
	if tt.HasText("Set by the theme Clear.") {
		t.Error("the note stays with no theme")
	}
	// The same click turns the user's own setting, now that it decides.
	tt.ClickAt(560, row.Y+row.H/2)
	tt.Frame()
	if prefs.NoGlass || !glassOn() {
		t.Error("the switch did not turn the user's glass on")
	}
}

// TestThemedFacePopover: a popover opened from a face of the other
// lightness, as the space picker's menu from a dark titleBar.background in
// a light window, lies on the window's panel and takes the window's
// ladder, not the face's: its text keeps 4.5:1 on the panel.
func TestThemedFacePopover(t *testing.T) {
	keepTheme(t)
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs.NoGlass = true // the panel's flat face, k.panel
	applyManifest(t, `{"roles":{"titleBar.background":"#1c2a3d"}}`)
	k := paletteOf(false)
	if hk, _, _ := themedFace(k, roleTitleBarBackground); hk.text == k.text || hk.window() != k {
		t.Fatal("the dark face did not take the dark ladder, or lost its window")
	}
	tt.Frame()
	a.spaces.open = true
	for range 10 {
		tt.Advance(100 * time.Millisecond)
		tt.Frame()
	}
	r, ok := tt.Find("No projects")
	if !ok {
		t.Fatalf("the space menu did not open: %q", tt.Texts())
	}
	img := tt.Image()
	scale := float32(img.Bounds().Dx()) / 1000
	best := 0.0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			p := img.RGBAAt(int(x*scale), int(y*scale))
			best = max(best, contrast(ui.RGB(p.R, p.G, p.B), k.panel))
		}
	}
	if best < 4.5 {
		t.Errorf("the menu's text has %.2f:1 on the window's panel, want 4.5:1", best)
	}
}

// TestThemedFaceSamePointer: the face's palette is the same pointer from
// frame to frame, so that menuStyleOf keeps one style for it; applyTheme
// makes a new one.
func TestThemedFaceSamePointer(t *testing.T) {
	keepTheme(t)
	applyManifest(t, `{"roles":{"titleBar.background":"#1c2a3d"}}`)
	k := paletteOf(false)
	h1, _, _ := themedFace(k, roleTitleBarBackground)
	cp := *k // a copy, as sidebarPalette and a space's tint make each frame
	h2, _, _ := themedFace(&cp, roleTitleBarBackground)
	if h1 != h2 || menuStyleOf(h1.window(), false) != menuStyleOf(h2.window(), false) {
		t.Error("two frames gave two palettes for one face")
	}
	applyManifest(t, `{"roles":{"titleBar.background":"#1c2a3d"}}`)
	if h3, _, _ := themedFace(paletteOf(false), roleTitleBarBackground); h3 == h1 {
		t.Error("applyTheme kept the face's old palette")
	}
}
