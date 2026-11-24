package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"

	"gorex/internal/config"
	"gorex/internal/theme"
	"gorex/locales"
)

// backgroundBefore is the background shader as it was before the glows of
// docs/DSK-010-theme-ambience.md. TestBackgroundNoGlows holds the shader,
// with no glows, to its pixels.
var backgroundBefore = shader.MustCompile(`package main

var Top, Mid, Low, Bottom, Tint vec4
var Alpha float

func hash(v uint) uint {
	s := v*747796405 + 2891336453
	w := ((s >> ((s >> 28) + 4)) ^ s) * 277803737
	return (w >> 22) ^ w
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	y := pos.y / size.y
	c := mix(Top.rgb, Mid.rgb, clamp(y/0.38, 0, 1))
	c = mix(c, Low.rgb, clamp((y-0.38)/0.32, 0, 1))
	c = mix(c, Bottom.rgb, clamp((y-0.7)/0.3, 0, 1))
	a := Tint.a * clamp((pos.x/size.x-0.35)/0.65, 0, 1)
	c = c*(1-a) + Tint.rgb*a
	n := hash(uint(dst.x) + hash(uint(dst.y)))
	c += (float(n)/4294967295.0 - 0.5) / 255.0
	return vec4(clamp(c, 0, 1)*Alpha, Alpha)
}
`)

// TestBackgroundNoGlows: with no glows, the background shader paints the
// pixels it painted before DSK-010, byte for byte: a theme without
// ambience, and the built-in palette, change no pixel. With a glow, the
// pixel at its center takes its color.
func TestBackgroundNoGlows(t *testing.T) {
	const w, h = 100, 80
	before := backgroundBefore.NewValues()
	u := backgroundBefore.Uniform
	for _, k := range []*colors{&lightColors, &darkColors} {
		before.Color(u("Top"), k.bgTop).Color(u("Mid"), k.bgMid).Color(u("Low"), k.bgLow).
			Color(u("Bottom"), k.bgBottom).Color(u("Tint"), k.bgTint).Float(u("Alpha"), 1)
		old := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { backgroundBefore.Draw(p, r, shader.Options{Values: before}) })
		}, w, h).Image()
		now := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { background.Draw(p, r, backgroundOptions(k, 1, nil)) })
		}, w, h).Image()
		if !bytes.Equal(old.Pix, now.Pix) {
			t.Fatal("the shader with no glows paints other pixels than before")
		}
		red := ui.RGBA(255, 0, 0, 0.4)
		lit := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				background.Draw(p, r, backgroundOptions(k, 1, []glow{{0.5, 0.5, 0.3, 0.3, red}}))
			})
		}, w, h).Image()
		o := lit.PixOffset(w/2, h/2)
		want := red.Over(ui.Color{R: now.Pix[o], G: now.Pix[o+1], B: now.Pix[o+2], A: 255})
		for i, c := range []uint8{want.R, want.G, want.B} {
			if d := int(lit.Pix[o+i]) - int(c); d > 3 || d < -3 {
				t.Errorf("glow center %v, want %v", lit.Pix[o:o+4], want)
				break
			}
		}
		if e := lit.PixOffset(2, 2); !bytes.Equal(lit.Pix[e:e+4], now.Pix[e:e+4]) {
			t.Error("a glow changed a pixel past its edge")
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

// installSea installs a theme pack with the keys of DSK-010 as "sea": a
// glow, a watermark and a brand row with an SVG icon.
func installSea(t *testing.T) {
	t.Helper()
	src := t.TempDir()
	os.Mkdir(filepath.Join(src, theme.AssetsDir), 0o755)
	writeTestPNG(t, filepath.Join(src, theme.AssetsDir, "side.png"), 40, 60, [4]uint8{20, 60, 200, 255})
	os.WriteFile(filepath.Join(src, theme.AssetsDir, "logo.svg"),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" fill="currentColor"/></svg>`), 0o644)
	os.WriteFile(filepath.Join(src, theme.PackFile), []byte(`{
  "name": "Sea",
  "seeds": { "light": { "accent": "#3b62f6" } },
  "assets": { "side": "side.png", "logo": "logo.svg" },
  "ambience": {
    "glows": [{ "x": 0.9, "y": 0.1, "rx": 0.5, "ry": 0.5, "color": { "light": "#3b62f666", "dark": "#6c8cff66" } }],
    "images": [{ "asset": "side", "anchor": "bottomRight", "height": 0.7, "opacity": 0.25 }]
  },
  "brand": { "icon": "logo", "wordmark": "Sea Whale" }
}`), 0o644)
	if _, err := theme.Install(src, theme.Dir(config.Dir()), "sea"); err != nil {
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

// sameSidebar reports whether a and b have the same pixels in the
// sidebar's column, the window's background and the sidebar's rows, left
// of the panes, whose terminal draws on its own time.
func sameSidebar(a, b *image.RGBA, w int) bool {
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		o := a.PixOffset(0, y)
		if !bytes.Equal(a.Pix[o:o+4*w], b.Pix[o:o+4*w]) {
			return false
		}
	}
	return true
}

// TestThemeAmbience: a theme with DSK-010's keys draws its glows, its
// watermark and its brand row; Ambience Off takes the glows and the
// watermark away and keeps the brand. A theme of colors only, and the
// built-in palette, draw what they drew before: the same pixels after an
// ambience theme as before it.
func TestThemeAmbience(t *testing.T) {
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
	side := int(sidebarWidthIn(1000)) // newTestApp's window is 1000 wide

	useThemeNow(t, "")
	builtin := shot()
	if tt.HasText("Sea Whale") {
		t.Fatal("a brand row with no theme")
	}

	// A theme of DSK-008's keys only has no look.
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, theme.PackFile), []byte(`{"name":"Slate","seeds":{"light":{"bgTop":"#eef0f4","accent":"#0a6bff"}}}`), 0o644)
	if _, err := theme.Install(src, theme.Dir(config.Dir()), "slate"); err != nil {
		t.Fatal(err)
	}
	useThemeNow(t, "slate")
	if look.Load() != nil {
		t.Error("a theme of colors only has a look")
	}
	slate := shot()

	installSea(t)
	useThemeNow(t, "sea")
	l := look.Load()
	if l == nil || l.art == nil || l.art.Icon == nil || l.art.Bitmaps["side"] == nil {
		t.Fatalf("the look of sea: %+v", l)
	}
	sea := shot()
	// A reload of the same theme, as a change of settings.json makes, keeps
	// the decoded assets while it decodes them again: nothing blinks.
	if err := loadTheme(); err != nil {
		t.Fatal(err)
	}
	if l := look.Load(); l == nil || l.art == nil {
		t.Error("a reload of the theme dropped its assets until the decode ends")
	}
	useThemeNow(t, "sea")
	if !tt.HasText("Sea Whale") {
		t.Errorf("no brand row; texts %q", tt.Texts())
	}
	brand, _ := tt.Find("Sea Whale")
	if brand.H <= 0 || brand.H > brandRowH {
		t.Errorf("brand row text %+v", brand)
	}

	prefs.Ambience = ambienceOff
	off := shot()
	if !tt.HasText("Sea Whale") {
		t.Error("Ambience Off hid the brand row")
	}
	w, h := sea.Rect.Dx(), sea.Rect.Dy()
	// The window's top right corner, in the glow; the bottom right one,
	// under the watermark.
	for _, pt := range [][2]int{{w - 3, 3}, {w - 3, h - 3}} {
		o := sea.PixOffset(pt[0], pt[1])
		if bytes.Equal(sea.Pix[o:o+4], off.Pix[o:o+4]) {
			t.Errorf("pixel %v: ambience on and off paint the same %v", pt, sea.Pix[o:o+4])
		}
	}
	prefs.Ambience = ""

	useThemeNow(t, "slate")
	if again := shot(); !sameSidebar(again, slate, side) {
		t.Error("a theme of colors only after an ambience theme paints other pixels than before it")
	}
	useThemeNow(t, "")
	if again := shot(); !sameSidebar(again, builtin, side) {
		t.Error("the built-in palette after an ambience theme paints other pixels than before it")
	}
	if look.Load() != nil {
		t.Error("the built-in palette kept a look")
	}
}

// TestAmbienceControls: the Window Theme group shows the Ambience control
// under a theme in use that draws more than colors, not under one of
// colors only, and no Mascot control before phase 3; a choice sets and
// saves appearance.ambience. The checks keep appearance.mascot's "off" and
// drop values neither key has.
func TestAmbienceControls(t *testing.T) {
	keepTheme(t)
	a, _ := newTestApp(t)
	t.Setenv("GOREX_CONFIG", filepath.Join(t.TempDir(), "init.lua"))
	old := prefs
	t.Cleanup(func() { prefs = old; resetIconPacksUI() })
	prefs = settings{}
	resetIconPacksUI()
	t.Setenv("GOREX_PACK_REGISTRY", "http://127.0.0.1:1/index.json")
	installSea(t)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, theme.PackFile), []byte(`{"name":"Slate","seeds":{"light":{"accent":"#0a6bff"}}}`), 0o644)
	theme.Install(src, theme.Dir(config.Dir()), "slate")

	settingsUI.section = "icons"
	tt := ui.NewTester(a.settingsView, 780, 1100)
	useThemeNow(t, "slate")
	tt.Frame()
	if tt.HasText(locales.SettingsThemeAmbience()) {
		t.Error("the Ambience control under a theme of colors only")
	}
	useThemeNow(t, "sea")
	tt.Frame()
	if !tt.HasText(locales.SettingsThemeAmbience()) {
		t.Fatalf("no Ambience control under sea; texts %q", tt.Texts())
	}
	if tt.HasText("Mascot") {
		t.Error("a Mascot control before the mascot draws")
	}
	if err := tt.Click(locales.SettingsAmbienceStill()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if prefs.Ambience != ambienceStill || string(settingsOnDisk(t)["appearance.ambience"]) != `"still"` {
		t.Errorf("Still: appearance.ambience %q", prefs.Ambience)
	}
	for _, tc := range []struct{ in, want settings }{
		{settings{Ambience: "off", Mascot: "off"}, settings{Ambience: "off", Mascot: "off"}},
		{settings{Ambience: "sideways", Mascot: "on"}, settings{}},
	} {
		s := tc.in
		checkSettings(&s)
		if s.Ambience != tc.want.Ambience || s.Mascot != tc.want.Mascot {
			t.Errorf("checkSettings(%q, %q) = %q, %q", tc.in.Ambience, tc.in.Mascot, s.Ambience, s.Mascot)
		}
	}
}
