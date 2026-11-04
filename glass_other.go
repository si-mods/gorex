//go:build !darwin

package main

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// lens is the Liquid Glass of the layers over the window, floats and
// panels, where the system has none of its own: macOS's transparent
// glass, as clear as a pane its size takes, so that what is under it
// reads through, blurred and bent along its rim, and not frosted over.
var lens = glass.Glass{Clarity: 0.6}

// paintLens paints MyGo's glass over box, rounded by radii.
func paintLens(p *ui.Painter, box ui.Rect, radii [4]float32) bool {
	lens.PaintMaterial(p, box, radii)
	return true
}
