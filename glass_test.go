package main

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestGlassDefaults checks that the glass is on until turned off, with the
// terminals' background at its default opacity, and that a window asks
// for the material of its platform.
func TestGlassDefaults(t *testing.T) {
	isolateSettings(t, "")
	if !glassOn() {
		t.Fatal("the glass is off by default")
	}
	if got := termOpacity(); got != defaultTermOpacity {
		t.Errorf("the default terminal opacity is %v, want %v", got, defaultTermOpacity)
	}
	want := map[string]mygo.Vibrancy{"darwin": mygo.VibrancyGlass, "windows": mygo.VibrancyMica}[runtime.GOOS]
	if got := glassVibrancy(); got != want {
		t.Errorf("the window's material is %q, want %q", got, want)
	}
	prefs.NoGlass = true
	if got := glassVibrancy(); got != mygo.VibrancyNone {
		t.Errorf("with the glass off the window's material is %q, want none", got)
	}
}

// TestGlassPersisted checks that the setting and the opacity are kept in
// settings.json, and that an opacity out of range counts as none.
func TestGlassPersisted(t *testing.T) {
	isolateSettings(t, "")
	a := &App{}
	a.setGlass(false)
	a.setWindowPref(func(s *settings) { s.TermOpacity = 0.5 })
	prefs = settings{}
	loadSettings()
	if glassOn() || termOpacity() != 0.5 {
		t.Errorf("read back glass %v, opacity %v; want off, 0.5", glassOn(), termOpacity())
	}
	a.setGlass(true)
	a.setWindowPref(func(s *settings) { s.TermOpacity = 5 })
	prefs = settings{}
	loadSettings()
	if !glassOn() || prefs.TermOpacity != 0 || termOpacity() != defaultTermOpacity {
		t.Errorf("read back glass %v, opacity %v; want on, the default", glassOn(), prefs.TermOpacity)
	}
}

// TestGlassPanelFlat checks that a glass panel paints its old face where
// the frame is not drawn on a GPU, as in tests, the setting on or off.
func TestGlassPanelFlat(t *testing.T) {
	isolateSettings(t, "")
	face := ui.Hex("#3366cc")
	px := func(glassy bool) [4]uint8 {
		prefs.NoGlass = !glassy
		img := ui.NewTester(func(c *ui.Context) {
			glassPanel(ui.Box(c).Size(40, 40).Radius(8), face)
		}, 60, 60).Image()
		o := img.PixOffset(20, 20)
		return [4]uint8(img.Pix[o : o+4])
	}
	on, off := px(true), px(false)
	if on != off || on != [4]uint8{face.R, face.G, face.B, 255} {
		t.Errorf("the panel reads %v with the glass, %v without; want both %v", on, off, face)
	}
}

// TestGlassWindow checks the main window over its material: with the
// glass on, the background is a wash the material shows through and the
// terminals' face fades by the setting's opacity; with it off, or with no
// material shown, the window paints as it always did.
func TestGlassWindow(t *testing.T) {
	isolateSettings(t, "")
	_, tt := newTestApp(t)
	// The gap at the window's bottom left edge shows the background; the
	// middle of the window, the pane's face.
	at := func(x, y int) [4]uint8 {
		tt.Frame()
		img := tt.Image()
		o := img.PixOffset(x, y)
		return [4]uint8(img.Pix[o : o+4])
	}
	edge, pane := [2]int{2, 615}, [2]int{500, 600}
	plainEdge, plainPane := at(edge[0], edge[1]), at(pane[0], pane[1])
	if plainEdge[3] != 255 {
		t.Fatalf("without a material the background reads %v, want opaque", plainEdge)
	}
	tt.SetVibrancy(true)
	if got := at(edge[0], edge[1]); got[3] >= 255 || got[3] == 0 {
		t.Errorf("over the glass the background reads %v, want a translucent wash", got)
	}
	glassPane := at(pane[0], pane[1])
	if glassPane == plainPane {
		t.Errorf("over the glass the pane reads %v, as without it", glassPane)
	}
	prefs.NoGlass = true
	if got := at(edge[0], edge[1]); got != plainEdge {
		t.Errorf("with the glass off the background reads %v, want %v", got, plainEdge)
	}
	if got := at(pane[0], pane[1]); got != plainPane {
		t.Errorf("with the glass off the pane reads %v, want %v", got, plainPane)
	}
}
