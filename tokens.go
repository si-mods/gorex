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
// A radius concentric with a container's is concentric(outer, inset),
// and a circle or a pill is capsule(h).
const (
	radiusS   = 6
	radiusM   = 10
	radiusL   = 12
	radiusXL  = 14
	radiusXXL = 16
)

// capsule is the corner radius of a circle or a pill h DIPs high: half
// its height.
func capsule(h float32) float32 { return h / 2 }

// raisedShadow is the shadow of a card or a group lying on the page, as
// a Settings group.
func raisedShadow(e ui.Element, k *colors) ui.Element {
	return e.Shadow(0, 1, 2, 0, k.shadow)
}

// toastShadow is the shadow of a toast or a banner over a pane.
func toastShadow(e ui.Element, k *colors) ui.Element {
	return e.Shadow(0, 8, 24, 0, k.shadowFocused)
}

// popoverShadow is the shadow of a menu, a popover or a picker.
func popoverShadow(e ui.Element, k *colors) ui.Element {
	return e.Shadow(0, 12, 32, 0, k.shadowFocused)
}

// dialogShadow is the shadow of a dialog or a sheet over a backdrop.
func dialogShadow(e ui.Element) ui.Element {
	return e.Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.06))
}
