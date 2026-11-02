package main

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// dropZone is the part of a pane that a dragged pane is over, and so where
// it goes: beside the pane on a side, or in its place.
type dropZone int

const (
	dropCenter dropZone = iota
	dropLeft
	dropRight
	dropTop
	dropBottom
)

// paneDropCenter is how far from the middle of a pane, as a fraction of its
// width and of its height, the center zone reaches.
const paneDropCenter = 0.2

// paneDropZone tells the zone at a point of a pane, given as fractions of
// its width and height. The center, a swap, is only there with center; else
// the nearest edge takes its room, and the diagonals divide the edges.
func paneDropZone(nx, ny float32, center bool) dropZone {
	if center && math.Abs(float64(nx-0.5)) < paneDropCenter && math.Abs(float64(ny-0.5)) < paneDropCenter {
		return dropCenter
	}
	left, right, top, bottom := nx, 1-nx, ny, 1-ny
	switch m := min(left, right, top, bottom); m {
	case left:
		return dropLeft
	case right:
		return dropRight
	case top:
		return dropTop
	}
	return dropBottom
}

// shownDrop is a drop zone shown over a pane.
type shownDrop struct {
	target *Pane
	zone   dropZone
}

// split tells how a zone splits the target: vertical is above and below,
// and before puts the dragged pane first, left or above. The center splits
// nothing, and answers false.
func (z dropZone) split() (vertical, before, ok bool) {
	switch z {
	case dropLeft:
		return false, true, true
	case dropRight:
		return false, false, true
	case dropTop:
		return true, true, true
	case dropBottom:
		return true, false, true
	}
	return false, false, false
}

// paneZoneIn tells the zone at a point of a card with these bounds, false
// for a card with no size.
func paneZoneIn(x, y float32, bounds ui.Rect, center bool) (dropZone, bool) {
	if bounds.W <= 0 || bounds.H <= 0 {
		return 0, false
	}
	return paneDropZone(x/bounds.W, y/bounds.H, center), true
}

// canDropPane tells whether the card of target takes the pane dragged: not
// itself, not a float either way, not from another host, not into a zoomed
// tab.
func canDropPane(dragged, target *Pane) bool {
	return dragged != nil && target != nil && dragged != target &&
		dragged.float == nil && target.float == nil &&
		dragged.Tab != nil && target.Tab != nil && dragged.host == target.host &&
		target.Tab.Zoom == nil
}

// paneDropZoneAt uses the pointer's position in a card and its
// previous-frame bounds. It answers false when the card does not take the
// pane, or the pointer is not over it.
func paneDropZoneAt(e ui.Element, dragged, target *Pane) (dropZone, bool) {
	if !canDropPane(dragged, target) {
		return 0, false
	}
	x, y, over := e.PointerPosition()
	if !over {
		return 0, false
	}
	return paneZoneIn(x, y, e.LayoutBounds(), dragged.Tab == target.Tab)
}

// dropPaneOn drops p on a zone of target: p swaps places with target for the
// center, else goes beside it, taking half of its room. It tells whether
// anything moved.
func (a *App) dropPaneOn(p, target *Pane, z dropZone) bool {
	if !canDropPane(p, target) {
		return false
	}
	if z == dropCenter {
		if p.Tab != target.Tab {
			return false
		}
		a.swapPanes(p, target)
		a.focusReq = p
		return true
	}
	vertical, before, ok := z.split()
	if !ok {
		return false
	}
	a.movePaneBeside(p, target, vertical, before, 0.5)
	return true
}

// paneDropRect is the part of a card that the pane dropped on zone would
// take, inset from the card's edge.
func paneDropRect(r ui.Rect, z dropZone) ui.Rect {
	const inset = float32(4)
	r = ui.Rect{X: r.X + inset, Y: r.Y + inset, W: max(r.W-2*inset, 0), H: max(r.H-2*inset, 0)}
	switch z {
	case dropLeft:
		r.W /= 2
	case dropRight:
		r.X += r.W / 2
		r.W /= 2
	case dropTop:
		r.H /= 2
	case dropBottom:
		r.Y += r.H / 2
		r.H /= 2
	}
	return r
}

// drawPaneDrop shows where the pane dragged over e would go. It paints
// with the card, not an effect: the card holds a terminal.
func drawPaneDrop(e ui.Element, k *colors, z dropZone) {
	e.DrawOver(func(p *ui.Painter, r ui.Rect) {
		d := paneDropRect(r, z)
		p.Fill(d, k.busy.Alpha(0.18), cardR-4)
		p.Stroke(d, k.busy.Alpha(0.6), cardR-4, 1.5)
	})
}
