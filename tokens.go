package main

import "github.com/egoist/mygo/ui"

// tokens.go holds the styles layer of DESIGN.md: the type ramp, the
// fixed corner radii and the shadows a raised layer takes. A value in
// UI code comes from here, not from a literal; TestDesignTokens checks
// that. Spacing is the one literal allowed, on the grid.

// The type ramp (DESIGN.md, Type). Terminal text is not on it: sizes
// relative to termFont.Size keep their own rule.
const (
	fontCaption   = 11
	fontFootnote  = 11.5
	fontSubhead   = 12
	fontCallout   = 12.5
	fontBody      = 13
	fontTitle     = 15
	fontCodeSmall = 22
	fontCode      = 28
)

// The fixed radii (DESIGN.md, Corners): what sits away from any corner.
// UI code takes one through k.radius, which scales it by the theme's
// metrics.radius (docs/DSK-012-theme-shaders.md); TestDesignTokens checks
// that. A radius concentric with a container's is
// concentric(k.radius(outer), inset), and a circle or a pill is
// k.capsule(h).
const (
	radiusS   = 6
	radiusM   = 10
	radiusL   = 12
	radiusXL  = 14
	radiusXXL = 16
	// radiusTile is a program's tile (drawTile), and a project's folder
	// tile in the sidebar, which matches it.
	radiusTile = 4.5
)

// radius is the radius token r in k's corners: r scaled by the theme's
// metrics.radius, 1 with no theme.
func (k *colors) radius(r float32) float32 { return r * k.m.radius }

// capsule is the corner radius of a circle or a pill h DIPs high: half
// its height, scaled as k.radius scales a token.
func (k *colors) capsule(h float32) float32 { return k.radius(h / 2) }

// borderW is the width of the border of a pane card, a panel and a field:
// the theme's metrics.border, 1 with no theme.
func (k *colors) borderW() float32 { return k.m.border }

// lift gives e a shadow of the theme's metrics.shadow: its offset y and
// its blur scaled, and the blur 0 when the theme asks for hard shadows.
func (k *colors) lift(e ui.Element, y, blur, spread float32, col ui.Color) ui.Element {
	y, blur = k.shadowAt(y, blur)
	return e.Shadow(0, y, blur, spread, col)
}

// shadowAt is a shadow's offset y and blur under the theme's
// metrics.shadow.
func (k *colors) shadowAt(y, blur float32) (float32, float32) {
	if k.m.shadowHard {
		blur = 0
	}
	return y * k.m.shadowScale, blur * k.m.shadowScale
}

// raisedShadow is the shadow of a card or a group lying on the page, as
// a Settings group.
func raisedShadow(e ui.Element, k *colors) ui.Element {
	return k.lift(e, 1, 2, 0, k.shadow)
}

// toastShadow is the shadow of a toast or a banner over a pane.
func toastShadow(e ui.Element, k *colors) ui.Element {
	return k.lift(e, 8, 24, 0, k.shadowFocused)
}

// popoverShadow is the shadow of a menu, a popover or a picker.
func popoverShadow(e ui.Element, k *colors) ui.Element {
	return k.lift(e, 12, 32, 0, k.shadowFocused)
}

// dialogShadow is the shadow of a dialog or a sheet over a backdrop.
func dialogShadow(e ui.Element, k *colors) ui.Element {
	return k.lift(k.lift(e, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)), 2, 6, 0, ui.RGBA(0, 0, 0, 0.06))
}
