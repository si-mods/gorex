//go:build !darwin

package main

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// paintLens paints MyGo's glass over box, rounded by radii: the floats'
// and panels' glass where the system has none of its own.
func paintLens(p *ui.Painter, box ui.Rect, radii [4]float32) bool {
	glass.Glass{}.PaintMaterial(p, box, radii)
	return true
}
