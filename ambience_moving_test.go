package main

import (
	"bytes"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/theme"
)

// TestFreeRegion: the bands of a tiled layout beside a sidebar, of a
// canvas whose floats overlap, and of an empty page cover the window less
// the cards, with no overlap; those behind the sidebar, and only those,
// are marked so (docs/DSK-010-theme-ambience.md, Free region).
func TestFreeRegion(t *testing.T) {
	win := ui.Rect{W: 1000, H: 620}
	side := ui.Rect{W: 240, H: 620}
	for _, tc := range []struct {
		name  string
		cards []ui.Rect
		side  ui.Rect
	}{
		{"tiled", []ui.Rect{{X: 252, Y: 40, W: 366, H: 568}, {X: 626, Y: 40, W: 362, H: 280}, {X: 626, Y: 328, W: 362, H: 280}}, side},
		{"canvas", []ui.Rect{{X: 100.5, Y: 80.25, W: 300, H: 200}, {X: 300, Y: 200, W: 400, H: 300}, {X: 900, Y: 500, W: 300, H: 300}, {X: -50, Y: -20, W: 120, H: 90}}, ui.Rect{}},
		{"empty", nil, side},
	} {
		bands := freeRegion(win, tc.cards, tc.side)
		for i, b := range bands {
			if b.r.W <= 0 || b.r.H <= 0 {
				t.Errorf("%s: empty band %+v", tc.name, b)
			}
			for _, c := range tc.cards {
				if overlaps(b.r, c) {
					t.Errorf("%s: band %+v over card %+v", tc.name, b.r, c)
				}
			}
			for _, o := range bands[i+1:] {
				if overlaps(b.r, o.r) {
					t.Errorf("%s: bands %+v and %+v overlap", tc.name, b.r, o.r)
				}
			}
			if in := tc.side.W > 0 && b.r.X >= tc.side.X && b.r.X+b.r.W <= tc.side.X+tc.side.W; b.side != in {
				t.Errorf("%s: band %+v behind the sidebar %v, want %v", tc.name, b.r, b.side, in)
			}
		}
		// Each point of a grid is in a band or in a card, never both.
		for y := float32(0.5); y < win.H; y += 7 {
			for x := float32(0.5); x < win.W; x += 7 {
				pt := ui.Rect{X: x, Y: y, W: 0.01, H: 0.01}
				card := false
				for _, c := range tc.cards {
					card = card || overlaps(pt, c)
				}
				free := false
				for _, b := range bands {
					free = free || overlaps(pt, b.r)
				}
				if free == card {
					t.Fatalf("%s: point %v/%v in a card %v and free %v", tc.name, x, y, card, free)
				}
			}
		}
	}
	if got := freeRegion(win, nil, ui.Rect{}); len(got) != 1 || got[0].r != win {
		t.Errorf("a window without cards: %+v", got)
	}
}

// startAt returns the time at which an item of a trip of secs seconds, at
// offset, is at f of its trip.
func startAt(secs, offset, f float32) float64 {
	t := float64((f - offset) * secs)
	if t < 0 {
		t += float64(secs)
	}
	return t
}

// TestAmbiencePaths: a sprite is at the expected place at a given time,
// the same a trip later; a sprite that swims left is mirrored unless it says not to be.
func TestAmbiencePaths(t *testing.T) {
	const w, h = 800, 600
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

	right := theme.Sprite{Y: 0.25, Height: 40, Seconds: 64, Mirror: true}
	t0 := startAt(64, unit(-1, 0, 0), 0.5)
	r, flip := spriteAt(right, 0, 80, 40, w, h, t0)
	if want := (ui.Rect{X: -80 + 0.5*(w+80), Y: 0.25 * (h - 40), W: 80, H: 40}); !near(r.X, want.X) || !near(r.Y, want.Y) || r.W != 80 || flip {
		t.Errorf("right sprite half way %+v, flip %v, want %+v", r, flip, want)
	}
	if r2, _ := spriteAt(right, 0, 80, 40, w, h, t0+64); !near(r2.X, r.X) {
		t.Errorf("a trip later at %v, not %v", r2.X, r.X)
	}
	left := right
	left.Dir = theme.Left
	if r, flip := spriteAt(left, 0, 80, 40, w, h, t0); !near(r.X, w-0.5*(w+80)) || !flip {
		t.Errorf("left sprite half way %+v, mirrored %v", r, flip)
	}
	left.Mirror = false
	if _, flip := spriteAt(left, 0, 80, 40, w, h, t0); flip {
		t.Error(`a sprite with "mirror": false is mirrored`)
	}
	up := theme.Sprite{X: 0.5, Height: 40, Seconds: 64, Dir: theme.Up}
	if r, flip := spriteAt(up, 0, 80, 40, w, h, t0); !near(r.X, 0.5*(w-80)) || !near(r.Y, h-0.5*(h+40)) || flip {
		t.Errorf("up sprite half way %+v, flip %v", r, flip)
	}

	// A sheet of 8 frames in 4 columns at 10 a second.
	sheet := theme.Asset{Frames: 8, Columns: 4, FPS: 10}
	// Decoded smaller, its frames take whole pixels: 65 or 66 of 262.
	for i := range 8 {
		got := sheetFrame(sheet, 262, 130, float64(i)/10+0.01)
		if got.X != float32(int(got.X)) || got.W != float32(int(got.W)) || got.W < 65 || got.W > 66 || got.H != 65 {
			t.Errorf("frame %d of a sheet 262 px wide: %+v", i, got)
		}
	}
	if got := sheetFrame(sheet, 400, 200, 0.55); got != (ui.Rect{X: 100, Y: 100, W: 100, H: 100}) {
		t.Errorf("frame at 0.55 s %+v, want the sixth", got)
	}
	if got := sheetFrame(theme.Asset{}, 40, 20, 3); got != (ui.Rect{W: 40, H: 20}) {
		t.Errorf("an image's frame %+v", got)
	}
}

// movingLook stores a look with a sprite of bm, for the tests of layer 3,
// and puts the old one back after the test.
func movingLook(t *testing.T, bm *ui.Bitmap) *themeLook {
	t.Helper()
	old := look.Load()
	t.Cleanup(func() { look.Store(old) })
	l := &themeLook{
		sprites: []theme.Sprite{{Asset: "fish", Y: 0.5, Height: 50, Seconds: 60, Opacity: 0.3, Mirror: true}},
		art:     &theme.Art{Bitmaps: map[string]*ui.Bitmap{"fish": bm}},
		assets:  map[string]theme.Asset{"fish": {File: "fish.png"}},
		ambient: true,
		sideCap: [2]float32{sidebarCap, sidebarCap},
	}
	look.Store(l)
	return l
}

// whiteBitmap is a w×h bitmap of opaque white.
func whiteBitmap(t *testing.T, w, h int) *ui.Bitmap {
	t.Helper()
	dir := t.TempDir()
	writeTestPNG(t, filepath.Join(dir, "w.png"), w, h, [4]uint8{255, 255, 255, 255})
	b, err := os.ReadFile(filepath.Join(dir, "w.png"))
	if err != nil {
		t.Fatal(err)
	}
	bm, err := ui.DecodeBitmap(b)
	if err != nil {
		t.Fatal(err)
	}
	return bm
}

// stillSeams makes the GPU and the battery what the test says, and puts
// them back after it.
func stillSeams(t *testing.T, gpu, battery *bool) {
	oldGPU, oldBattery := gpuPaints, onBattery
	t.Cleanup(func() { gpuPaints, onBattery = oldGPU, oldBattery })
	gpuPaints = func(*ui.Painter) bool { return *gpu }
	onBattery = func() bool { return *battery }
}

// TestAmbienceStill: the moving layers ask for a frame at ambienceFPS
// while they move, and for no frame and no timer in each still case: the
// window not active, hidden, on battery, under less motion, with Animation
// Off, with Ambience Still, and on the CPU renderer. Still, they stay where
// they are; moving again, they go on from there.
func TestAmbienceStill(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	movingLook(t, whiteBitmap(t, 40, 20))
	gpu, battery := true, false
	stillSeams(t, &gpu, &battery)

	a := &App{}
	tt := ui.NewTester(func(c *ui.Context) {
		moving := a.movingLayers(c)
		ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
			if moving != nil {
				moving(p, r)
			}
		})
	}, 300, 400)
	tt.SetWindowActive(true)
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("moving layers ask for no frame")
	}
	for _, tc := range []struct {
		name       string
		set, unset func()
	}{
		{"not active", func() { tt.SetWindowActive(false) }, func() { tt.SetWindowActive(true) }},
		{"hidden", func() { tt.SetWindowHidden(true) }, func() { tt.SetWindowHidden(false) }},
		{"on battery", func() { battery = true }, func() { battery = false }},
		{"less motion", func() { tt.SetPreferences(ui.Preferences{ReduceMotion: true}) }, func() { tt.SetPreferences(ui.Preferences{}) }},
		{"Animation Off", func() { prefs.Animation = "off" }, func() { prefs.Animation = "" }},
		{"Ambience Still", func() { prefs.Ambience = ambienceStill }, func() { prefs.Ambience = "" }},
		{"CPU renderer", func() { gpu = false }, func() { gpu = true }},
	} {
		tc.set()
		tt.Frame()
		tt.Frame()
		if tt.FramePending() {
			t.Errorf("%s: a frame is pending", tc.name)
		}
		before := tt.Image()
		tt.Advance(2 * time.Second)
		tt.Frame()
		if !bytes.Equal(before.Pix, tt.Image().Pix) {
			t.Errorf("%s: the layers moved", tc.name)
		}
		tc.unset()
		tt.Frame()
		if !tt.FramePending() {
			t.Errorf("after %s: moving layers ask for no frame", tc.name)
		}
	}

	// The frames that only paint, which build no view, ask of the battery
	// too: unplugged, the layers stop at the next of them.
	battery = true
	tt.Advance(100 * time.Millisecond)
	if tt.FramePending() {
		t.Error("on battery, the frames that only paint go on")
	}
	battery = false
	tt.Frame()

	// A sprite whose image did not load draws nothing, and asks for no
	// frame.
	l := look.Load()
	l.art = &theme.Art{Bitmaps: map[string]*ui.Bitmap{}}
	tt.Frame()
	tt.Frame()
	if tt.FramePending() {
		t.Error("sprites without images ask for frames")
	}

	// The clock stops while still, and goes on from where it stopped.
	var f ambienceFrame
	t0 := time.Unix(1000, 0)
	at := f.clock(t0, true)
	f.clock(t0.Add(time.Second), false)
	if got := f.clock(t0.Add(5*time.Second), false); got != at+1 {
		t.Errorf("still at %v, want %v", got, at+1)
	}
	if got := f.clock(t0.Add(9*time.Second), true); got != at+1 {
		t.Errorf("moving again at %v, want %v", got, at+1)
	}
	if got := f.clock(t0.Add(10*time.Second), true); got != at+2 {
		t.Errorf("a second later at %v, want %v", got, at+2)
	}
}

// TestSidebarAmbienceContrast: behind the sidebar no sprite
// paints above alpha 0.12, beside it a sprite paints at its own; a theme
// whose worst case fails 4.5:1 for text or 3:1 for muted text gets a lower
// cap and a warning, and the built-in palettes keep 0.12.
func TestSidebarAmbienceContrast(t *testing.T) {
	keepTheme(t)
	l := movingLook(t, whiteBitmap(t, 40, 10))
	const w, h = 400, 300
	// The sprite, 200 by 50 DIPs, half way across: from 100 to 300.
	at := startAt(60, unit(-1, 0, 0), 0.5)
	img := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
			paintMoving(p, r, l, 0, []band{{ui.Rect{W: 200, H: h}, true}, {ui.Rect{X: 200, W: 200, H: h}, false}}, at)
		})
	}, w, h).Image()
	y := int(0.5*(h-50) + 25)
	capped, own := 31, 77 // sidebarCap and 0.3 of 255
	if got := int(img.Pix[img.PixOffset(150, y)]); got > capped+2 || got < capped-2 {
		t.Errorf("behind the sidebar the sprite paints %d of 255, want %d", got, capped)
	}
	if got := int(img.Pix[img.PixOffset(250, y)]); got < own-2 {
		t.Errorf("beside the sidebar the sprite paints %d of 255, want %d", got, own)
	}
	if got := img.Pix[img.PixOffset(50, y)]; got != 0 {
		t.Errorf("past the sprite %d", got)
	}

	inks := movingInks(theme.Ambience{Sprites: []theme.Sprite{{}}})
	for i, k := range []*colors{&lightColors, &darkColors} {
		if c := sidebarCapFor(k, inks); c != sidebarCap {
			t.Errorf("built-in palette %d: cap %v", i, c)
		}
	}
	// Text just at 4.5:1 and 3:1 on white: any ink lowers it.
	m, _, err := theme.Parse([]byte(`{"seeds":{"light":{"bgTop":"#ffffff","bgBottom":"#ffffff","text":"#767676","textMuted":"#949494"}},
"assets":{"fish":"fish.png"},"ambience":{"sprites":[{"asset":"fish"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	applyTheme(m)
	got := look.Load()
	if got == nil || got.sideCap[0] >= sidebarCap || got.sideCap[1] != sidebarCap {
		t.Fatalf("caps %+v, want light lowered", got)
	}
	k := paletteOf(false)
	over := ui.RGB(0, 0, 0).Alpha(got.sideCap[0]).Over(ui.RGB(255, 255, 255))
	if contrast(k.text, over) < min(4.5, contrast(k.text, ui.RGB(255, 255, 255))) {
		t.Errorf("text at %.2f:1 over the lowered cap", contrast(k.text, over))
	}
	if !strings.Contains(logs.String(), "behind the sidebar") {
		t.Errorf("no warning: %q", logs.String())
	}
}

// TestAmbienceSettings: Ambience Off draws no moving layer, as with no
// theme; Still draws them where they are. The sidebar's button, and Toggle
// Theme Ambience, show only with a theme that has ambience; the button
// turns it off, and on again as it was. A tiled tab's cards count for the
// free region.
func TestAmbienceSettings(t *testing.T) {
	keepTheme(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	movingLook(t, whiteBitmap(t, 40, 20))
	gpu, battery := false, false
	stillSeams(t, &gpu, &battery)
	a := &App{}
	shot := func() []byte {
		return ui.NewTester(func(c *ui.Context) {
			moving := a.movingLayers(c)
			ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Draw(func(p *ui.Painter, r ui.Rect) {
				if moving != nil {
					moving(p, r)
				}
			})
		}, 600, 400).Image().Pix
	}
	blank := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)) }, 600, 400).Image().Pix
	if bytes.Equal(shot(), blank) {
		t.Error("moving layers paint nothing")
	}
	prefs.Ambience = ambienceStill
	if bytes.Equal(shot(), blank) {
		t.Error("Ambience Still paints nothing")
	}
	prefs.Ambience = ambienceOff
	if !bytes.Equal(shot(), blank) {
		t.Error("Ambience Off paints moving layers")
	}

	t.Setenv("GOREX_CONFIG", filepath.Join(t.TempDir(), "init.lua"))
	app, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", Animation: "off", NoGlass: true, Ambience: ambienceStill}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, theme.PackFile), []byte(`{"name":"Slate","seeds":{"light":{"accent":"#0a6bff"}}}`), 0o644)
	if _, err := theme.Install(src, theme.Dir(config.Dir()), "slate"); err != nil {
		t.Fatal(err)
	}
	useThemeNow(t, "slate")
	tt.Frame()
	if _, ok := tt.Find("Turn Off Theme Ambience"); ok || cmdAmbience.Available(app) {
		t.Error("an ambience button or command with a theme of colors only")
	}
	installSea(t)
	useThemeNow(t, "sea")
	tt.Frame()
	tt.Frame()
	if !cmdAmbience.Available(app) {
		t.Error("no Toggle Theme Ambience with a theme of ambience")
	}
	if err := tt.Click("Turn Off Theme Ambience"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if prefs.Ambience != ambienceOff {
		t.Errorf("after the button, ambience %q", prefs.Ambience)
	}
	if err := tt.Click("Turn On Theme Ambience"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if prefs.Ambience != ambienceStill {
		t.Errorf("on again, ambience %q, want it still as it was", prefs.Ambience)
	}
	if n := len(app.ambience.cards); n != 1 || !app.ambience.hasSide {
		t.Errorf("the frame counted %d cards, sidebar %v", n, app.ambience.hasSide)
	}
}
