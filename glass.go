package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// The terminals' background opacity under the glass setting: its range,
// and what it is until set.
const (
	minTermOpacity     = 0.3
	maxTermOpacity     = 1.0
	defaultTermOpacity = 0.8
)

// glassWash is how much of the window's own background is painted over
// the window's glass: enough to keep its colors, the workspace's tint
// among them, as a faint wash, and little enough for the desktop to show.
const glassWash = 0.3

// glassOn tells that the glass setting is on, as it is until turned off.
func glassOn() bool { return !prefs.NoGlass }

// termOpacity is the terminals' background opacity the setting asks for,
// in its range, or the default while unset.
func termOpacity() float32 {
	if prefs.TermOpacity == 0 {
		return defaultTermOpacity
	}
	return min(max(prefs.TermOpacity, minTermOpacity), maxTermOpacity)
}

// glassVibrancy is the material the windows show behind their pages for
// the setting: Liquid Glass on macOS (from macOS 26; before, MyGo shows
// the popover material in its place), Mica on Windows 11, as its own
// apps' main windows have, and none on Linux, which has no materials, or
// with the setting off.
func glassVibrancy() mygo.Vibrancy {
	if !glassOn() {
		return mygo.VibrancyNone
	}
	switch runtime.GOOS {
	case "darwin":
		return mygo.VibrancyGlass
	case "windows":
		return mygo.VibrancyMica
	}
	return mygo.VibrancyNone
}

// glassTransparent tells whether a window is made transparent for its
// glass: on macOS, whose glass shows the desktop only through a window
// that is not opaque. Windows' backdrops show through its windows as they
// are.
func glassTransparent() bool { return glassOn() && runtime.GOOS == "darwin" }

// windowGlass tells whether c's window shows its glass behind the page:
// the setting is on and the system draws the material, which it does not
// on Linux, before Windows 11 22H2, in a Windows window created without
// one, or in a test. Elsewhere the page paints as it always did.
func windowGlass(c *ui.Context) bool { return glassOn() && c.Vibrancy() }

// backgroundAlpha is how opaque the window's background paints in c's
// window: a wash over the window's glass, else whole.
func backgroundAlpha(c *ui.Context) float32 {
	if windowGlass(c) {
		return glassWash
	}
	return 1
}

// rootBackground is the root's face: bg, or clear over the window's
// glass, which shows wherever the page paints nothing.
func rootBackground(c *ui.Context, bg ui.Color) {
	if windowGlass(c) {
		bg = ui.Transparent
	}
	c.Root().Background(bg)
}

// applyGlass shows the setting's material in every open window, the
// settings window too. On Windows only a window created with a material
// can show one: a window opened while the setting was off shows its glass
// once opened again.
func (a *App) applyGlass() {
	v := glassVibrancy()
	for _, o := range a.peers() {
		if o.win != nil {
			o.win.SetVibrancy(v)
		}
	}
	if w := settingsUI.win; w != nil {
		w.SetVibrancy(v)
	}
}

// paneGlass is the material of a layer floating over the window under the
// glass setting: Liquid Glass, and face over it, where the frame draws on
// a GPU; elsewhere, as on the CPU renderer and in tests, where the glass
// is slow, the flat color flat, as the layer had before. The frame being
// drawn is known only as it paints, so the material decides then.
type paneGlass struct {
	face, flat ui.Color
}

func (g paneGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	if !p.Accelerated() {
		p.Fill(box, g.flat, radii[0])
		return
	}
	glass.Glass{}.PaintMaterial(p, box, radii)
	if g.face.A > 0 {
		p.Fill(box, g.face, radii[0])
	}
}

// glassPanel gives e, a popover's or a dialog's panel or the active tab's
// pill, the glass under the setting, and the flat face flat without it.
func glassPanel(e ui.Element, flat ui.Color) ui.Element {
	if glassOn() {
		return e.Material(paneGlass{flat: flat})
	}
	return e.Background(flat)
}

// pillRadius is the corner radius of a tab's row h DIPs high, the same
// hovered as active: a capsule's under the glass setting, as the active
// tab's pill is, and flat otherwise without it.
func pillRadius(h, flat float32) float32 {
	if glassOn() {
		return h / 2
	}
	return flat
}

// glassPill gives e, the active tab's pill h DIPs high, its look: under
// the glass setting a capsule of the system's Liquid Glass, as Rex's
// selected tab; without it the flat face and shadows it had before. It is
// not glass.Glass, which reads only what the page painted: under the
// sidebar the page paints nothing over the window's glass, and it would
// turn dark.
func glassPill(e ui.Element, k *colors, h float32) {
	if !glassOn() {
		e.Background(k.tabActive).Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 2, 8, 0, k.shadow)
		return
	}
	// The system's own Liquid Glass, as Rex's selected tab is: no tint in a
	// dark window, a grey one in a light one, and its shadow none in a
	// dark window, a faint light under it in a light one. Where the system
	// draws no glass (before macOS 26, on Windows and Linux, in tests) it
	// is the flat face of light over the window.
	e.Radius(h/2).Material(ui.SystemGlass{Tint: k.pillTint, Fallback: faceMaterial{k.pillFace}}).
		Shadow(0, 1.5, 3.5, 0, k.pillShadow)
}

// faceMaterial is a material of one color: the pill's where the system
// draws no glass.
type faceMaterial struct{ c ui.Color }

func (f faceMaterial) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	p.Fill(box, f.c, radii[0])
}

// hoverFace is a hovered tab's face: Rex's under the glass setting, the
// plain hover without it.
func hoverFace(k *colors) ui.Color {
	if glassOn() {
		return k.tabHover
	}
	return k.hover
}
