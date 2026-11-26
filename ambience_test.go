package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"

	"gorex/internal/config"
	"gorex/internal/theme"
)

// TestBackgroundAsBefore: the background shader, which has no glows any
// more, paints the pixels the one with the glows of
// docs/DSK-010-theme-ambience.md painted with none, byte for byte
// (costReference keeps that one): a theme without a background shader,
// and the built-in palette, change no pixel.
func TestBackgroundAsBefore(t *testing.T) {
	const w, h = 100, 80
	for _, k := range []*colors{&lightColors, &darkColors} {
		before := shader.Options{Uniforms: map[string]any{
			"Top": k.bgTop, "Mid": k.bgMid, "Low": k.bgLow, "Bottom": k.bgBottom, "Tint": k.bgTint, "Alpha": 1,
			"Glow": [16]float32{2: 1, 3: 1, 6: 1, 7: 1, 10: 1, 11: 1, 14: 1, 15: 1},
		}}
		old := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { costReference.Draw(p, r, before) })
		}, w, h).Image()
		now := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { background.Draw(p, r, backgroundOptions(k, 1)) })
		}, w, h).Image()
		if !bytes.Equal(old.Pix, now.Pix) {
			t.Fatal("the background paints other pixels than before the glows went")
		}
	}
}

// writeTestPNG writes a w×h PNG of one color.
func writeTestPNG(t *testing.T, path string, w, h int, c [4]uint8) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], c[:])
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// useThemeNow selects the theme id and waits until the decode of its
// assets is done; a decode that does not end in 5 s fails the test.
func useThemeNow(t *testing.T, id string) {
	t.Helper()
	prefs.Theme = id
	if err := loadTheme(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { artLoads.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the assets of theme %q did not load in 5 s", id)
	}
}

// TestThemeLooksDropped: GoRex draws no look of its own from a pack
// (docs/DSK-008-theme.md, Non-goals). A pack with the glows, watermark,
// sprites and brand row of DSK-010 installs and loads its colors, and the
// window paints the same pixels as with the same colors and none of those
// keys: no brand row, no glow, no watermark. It shows no ambience button.
func TestThemeLooksDropped(t *testing.T) {
	keepTheme(t)
	t.Setenv("GOREX_CONFIG", filepath.Join(t.TempDir(), "init.lua"))
	_, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old; applyTheme(nil) })
	prefs = settings{TabPlacement: "left", Animation: "off", NoGlass: true}
	shot := func() *image.RGBA {
		tt.Frame()
		tt.Frame()
		return tt.Image()
	}
	const colorsOnly = `"name": "Sea", "seeds": { "light": { "accent": "#3b62f6", "bgTop": "#dce8ff" } }`
	install := func(id, body string) {
		src := t.TempDir()
		os.Mkdir(filepath.Join(src, theme.AssetsDir), 0o755)
		writeTestPNG(t, filepath.Join(src, theme.AssetsDir, "side.png"), 40, 60, [4]uint8{20, 60, 200, 255})
		os.WriteFile(filepath.Join(src, theme.AssetsDir, "logo.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"/>`), 0o644)
		os.WriteFile(filepath.Join(src, theme.PackFile), []byte("{"+body+"}"), 0o644)
		if _, err := theme.Install(src, theme.Dir(config.Dir()), id); err != nil {
			t.Fatal(err)
		}
	}
	install("plain", colorsOnly)
	install("sea", colorsOnly+`,
  "assets": { "side": "side.png", "logo": "logo.svg", "fish": { "file": "side.png", "frames": 4, "columns": 2 } },
  "ambience": {
    "glows": [{ "x": 0.9, "y": 0.1, "rx": 0.5, "ry": 0.5, "color": "#ff000066" }],
    "images": [{ "asset": "side", "anchor": "bottomRight", "height": 0.7, "opacity": 0.25 }],
    "sprites": [{ "asset": "side", "y": 0.5, "height": 100, "seconds": 8, "opacity": 0.3 }]
  },
  "brand": { "icon": "logo", "wordmark": "Sea Whale" }`)

	useThemeNow(t, "plain")
	plain := shot()
	useThemeNow(t, "sea")
	sea := shot()
	if k := paletteOf(false); k.accent != ui.Hex("#3b62f6") {
		t.Errorf("the pack's accent %v did not load", k.accent)
	}
	if tt.HasText("Sea Whale") {
		t.Error("a brand row")
	}
	if _, ok := tt.Find("Turn Off Theme Ambience"); ok || themeAmbient() {
		t.Error("an ambience button for a pack with no shader")
	}
	if !sameSidebar(sea, plain, sea.Rect.Dx()/4) {
		t.Error("the pack paints other pixels than its colors alone")
	}
}

// sameSidebar reports whether a and b have the same pixels in the w
// pixels at the window's left: the sidebar's column, the window's
// background and the sidebar's rows, left of the panes, whose terminal
// draws on its own time.
func sameSidebar(a, b *image.RGBA, w int) bool {
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		o := a.PixOffset(0, y)
		if !bytes.Equal(a.Pix[o:o+4*w], b.Pix[o:o+4*w]) {
			return false
		}
	}
	return true
}

// TestAmbienceSettingsFile: appearance.ambience reads "still" and "off",
// and any other value as the default. A settings.json that still has
// appearance.mascot, which no build reads, loads as it is, and the next
// save drops the key.
func TestAmbienceSettingsFile(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range []struct{ file, ambience string }{
		{`{"appearance.theme":"sea","appearance.ambience":"off","appearance.mascot":"off"}`, "off"},
		{`{"appearance.theme":"sea","appearance.ambience":"still"}`, "still"},
		{`{"appearance.theme":"sea","appearance.ambience":"sideways","appearance.mascot":"on"}`, ""},
	} {
		next, ok := decodeSettings([]byte(tc.file))
		if !ok {
			t.Fatalf("decodeSettings(%s)", tc.file)
		}
		if next.Ambience != tc.ambience || next.Theme != "sea" {
			t.Errorf("%s: ambience %q, theme %q", tc.file, next.Ambience, next.Theme)
		}
		prefs = next
		saveSettings()
		b, err := os.ReadFile(settingsPath())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "appearance.mascot") || !strings.Contains(string(b), `"appearance.theme": "sea"`) {
			t.Errorf("%s saved as %s", tc.file, b)
		}
	}
}

// TestAmbienceCommand: Toggle Theme Ambience shows only while the theme
// has a shader that Ambience turns off or stops (docs/DSK-012-theme-shaders.md),
// and the sidebar shows no ambience toggle button.
func TestAmbienceCommand(t *testing.T) {
	keepTheme(t)
	t.Setenv("GOREX_CONFIG", filepath.Join(t.TempDir(), "init.lua"))
	app, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off", NoGlass: true, Ambience: ambienceStill}
	m, _, err := theme.Parse([]byte(`{"name":"Slate","seeds":{"light":{"accent":"#0a6bff"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	applyTheme(m)
	tt.Frame()
	if _, ok := tt.Find("Turn Off Theme Ambience"); ok || cmdAmbience.Available(app) {
		t.Error("an ambience button or command with a theme of colors only")
	}

	m, art := shaderTheme(t, `{
  "assets": { "bg": "bg.kage" },
  "shaders": { "api": 1, "background": { "source": "bg" } }
}`, map[string]string{"bg": solidBackground})
	if set := useShaders(t, m, art); set.background == nil {
		t.Fatalf("the background slot fails: %q", set.warn)
	}
	tt.Frame()
	if !cmdAmbience.Available(app) {
		t.Error("no Toggle Theme Ambience with a background shader")
	}
	if _, ok := tt.Find("Turn Off Theme Ambience"); ok {
		t.Fatal("sidebar still shows ambience toggle button")
	}
	app.toggleAmbience()
	tt.Frame()
	if prefs.Ambience != ambienceOff || themeBackground() != nil {
		t.Errorf("after toggle, ambience %q, background %v", prefs.Ambience, themeBackground())
	}
	app.toggleAmbience()
	tt.Frame()
	if prefs.Ambience != ambienceStill || themeBackground() == nil {
		t.Errorf("on again, ambience %q, want it still as it was", prefs.Ambience)
	}
}
