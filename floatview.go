package main

import (
	"cmp"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// floatCell places a float's pane over the tab's tiled ones, at its box of
// the room, which is bounds, with a grip that resizes it.
func (a *App) floatCell(c *ui.Context, k *colors, t *Tab, f *Float, bounds ui.Rect) {
	p := f.Pane
	if p == nil {
		return
	}
	cell := ui.Column(c.Key(cellKey(p.ID))).Absolute()
	anim := motion(c)
	if anim == &snappy {
		anim = &springBack
	}
	if f.built+1 != a.passes {
		// Not built in the last pass, as hidden or in another tab: it is
		// at rest.
		f.gliding, f.fling = false, nil
	}
	f.built = a.passes
	// Dragged past the room's edge, the float follows the pointer less and
	// less; let go, it glides from where it showed to its box, at the
	// pointer's velocity, and springs back in.
	s := share{f.X + rubber(f.overX), f.Y + rubber(f.overY), f.W, f.H}
	if fl := f.fling; fl != nil && !fl.started {
		f.gliding = false
		if anim != nil && !f.Zoom {
			fl.started = true
			// The place goes in fractions of the room: it rests within a
			// tenth of a DIP of a room 1000 DIPs across.
			glide := *anim
			glide.Spring.Precision = 1e-4
			cell.Jump(glideX, fl.x)
			cell.Jump(glideY, fl.y)
			// The throw starts it toward its rest, no faster than it
			// settles there. A velocity of zero would keep the one the
			// value had: the least one stands for none.
			rate := glideRate(anim)
			gx, gy := glide, glide
			gx.Spring.Velocity = cmp.Or(glideVelocity(fl.vx, fl.x, f.X, rate), 1e-9)
			gy.Spring.Velocity = cmp.Or(glideVelocity(fl.vy, fl.y, f.Y, rate), 1e-9)
			cell.AnimateTo(glideX, f.X, gx)
			cell.AnimateTo(glideY, f.Y, gy)
			f.gliding = true
		} else {
			f.fling = nil
		}
	}
	if f.gliding {
		if anim == nil || f.Zoom || f.dragging || f.fling == nil {
			f.gliding, f.fling = false, nil
		} else {
			glide := *anim
			glide.Spring.Precision = 1e-4
			s.x, s.y = cell.AnimateTo(glideX, f.X, glide), cell.AnimateTo(glideY, f.Y, glide)
			if s.x == f.X && s.y == f.Y {
				f.gliding, f.fling = false, nil
			} else {
				// Past the room's edges it goes a little way at most,
				// unless it was let go of past them.
				if bounds.W > 0 && f.fling.inside[0] {
					s.x = edgeBand(s.x, f.W, glideOver/bounds.W)
				}
				if bounds.H > 0 && f.fling.inside[1] {
					s.y = edgeBand(s.y, f.H, glideOver/bounds.H)
				}
			}
		}
	}
	f.shown = [2]float32{s.x, s.y}
	if f.Zoom {
		s = whole
	}
	cell.LeftPercent(s.x * 100).TopPercent(s.y * 100).WidthPercent(s.w * 100).HeightPercent(s.h * 100).
		Radius(a.cardR(k))
	// Under the glass setting a float is a pane of the system's Liquid
	// Glass, as the tiled panes are; where the system draws none, the
	// tiled panes blurred through its face on a GPU, and opaque elsewhere,
	// as before.
	if face := cardFace(c, k, t.Focus == p, true); glassOn() {
		tint := glassTint(c, k, t.Focus == p)
		cell.Material(ui.SystemGlass{Tint: tint, Fallback: paneGlass{face: tint, flat: face}})
	} else {
		cell.Background(face)
	}
	// Under the pointer, or gliding from it, it moves as it does; else, as
	// it is nudged by the keys, zooms, or floats from its place among the
	// tiled panes, it eases there.
	moves := false
	if anim != nil && !f.dragging && !f.gliding {
		cell.Transition(ui.ElementTransition{Animation: anim, Position: true, Size: true})
		moves = true
	}
	// Shadows as of a sheet lifted off the panes: a tight one where it
	// nearly touches them, a soft one under it, and a wide one of the
	// light around; deeper while it has the focus.
	lift := float32(1)
	if t.Focus == p {
		lift = 1.35
	}
	// Over light cards a soft tinted shadow lifts it; over dark ones, as
	// terminals mostly are, it takes a deep black one to show at all.
	ink := ui.RGBA(40, 30, 40, 1)
	alpha := [3]float32{0.10, 0.14, 0.16}
	if face := cardFace(c, k, false, true); c.Theme().Dark || int(face.R)+int(face.G)+int(face.B) < 3*110 {
		ink = ui.RGBA(0, 0, 0, 1)
		alpha = [3]float32{0.55, 0.5, 0.55}
	}
	// A canvas's boxes lie on its plane, over no panes: they lift off none.
	if !f.free() {
		cell.Shadow(0, 1, 3, 0, ink.Alpha(min(alpha[0]*lift, 1))).
			Shadow(0, 12*lift, 32*lift, -4, ink.Alpha(min(alpha[1]*lift, 1))).
			Shadow(0, 30*lift, 80*lift, -10, ink.Alpha(min(alpha[2]*lift, 1)))
	}
	rest := ui.Rect{
		X: bounds.X + s.x*bounds.W,
		Y: bounds.Y + s.y*bounds.H,
		W: s.w * bounds.W,
		H: s.h * bounds.H,
	}
	// It moves from the frame its box changes, as from its place among
	// the tiled panes, until it rests.
	// Gliding, it moves too.
	starts := p.restAt(rest) && moves
	moving := f.gliding || starts || moves && cell.Transitioning()
	// While the pointer resizes it or it eases to a new size, the terminal
	// keeps the size it rested at, and the program hears of the new one
	// once: else a shell redraws its prompt at every step, over itself.
	held := f.dragging || f.gliding || starts || cell.Transitioning()
	cell.Children(func() {
		// Moving, the card goes with the cell, its colors not fading: an
		// element of a Transition of its own would be laid out where the
		// cell goes, its terminal out of the cell on the way.
		if moving {
			prev := suppressFadeColors.Swap(true)
			defer suppressFadeColors.Store(prev)
		}
		a.paneCard(c, k, t, p, held).Grow(1)
		if f.Zoom {
			return
		}
		// Every edge and corner resizes it, on a band astride the edge.
		for _, e := range floatEdges {
			a.floatEdge(c, k, f, p, e, bounds)
		}
	})
}

// A float's edges, as the sides of its box a drag of each moves.
const (
	edgeLeft = 1 << iota
	edgeRight
	edgeTop
	edgeBottom
)

// floatEdges are the handles of a float's sides, then of its corners,
// which lie over them.
var floatEdges = []int{
	edgeLeft, edgeRight, edgeTop, edgeBottom,
	edgeLeft | edgeTop, edgeRight | edgeTop, edgeLeft | edgeBottom, edgeRight | edgeBottom,
}

// floatEdge is the handle of edges e of float f: dragging it moves those
// sides of its box, the others staying put.
func (a *App) floatEdge(c *ui.Context, k *colors, f *Float, p *Pane, e int, bounds ui.Rect) {
	const band, corner = 8, 16
	h := ui.Box(c.Key(floatGripKey(p.ID*16 + e))).Absolute()
	switch {
	case e&(edgeLeft|edgeRight) != 0 && e&(edgeTop|edgeBottom) != 0:
		h.Size(corner, corner)
		cur := ui.CursorResizeNWSE
		if e == edgeRight|edgeTop || e == edgeLeft|edgeBottom {
			cur = ui.CursorResizeNESW
		}
		h.Cursor(cur)
	case e&(edgeLeft|edgeRight) != 0:
		h.Width(band).TopPercent(0).HeightPercent(100).Cursor(ui.CursorResizeEW)
	default:
		h.Height(band).LeftPercent(0).WidthPercent(100).Cursor(ui.CursorResizeNS)
	}
	if e&edgeLeft != 0 {
		h.Left(-band / 2)
	}
	if e&edgeRight != 0 {
		h.Right(-band / 2)
	}
	if e&edgeTop != 0 {
		h.Top(-band / 2)
	}
	if e&edgeBottom != 0 {
		h.Bottom(-band / 2)
	}
	if dx, dy, ok := h.Dragged(); ok && bounds.W > 0 && bounds.H > 0 {
		f.resizeEdges(e, dx/bounds.W, dy/bounds.H)
		a.changed()
	}
	f.dragging = f.dragging || h.Dragging()
}

// minimizedRail lists the tab's minimized floats over its bottom right:
// one chip each, which shows that float again. A hidden float is a
// minimized layer to Rex's clients, and GoRex draws no pane for it, so
// this is where the desktop finds it.
func (a *App) minimizedRail(c *ui.Context, k *colors, floats []*Float) {
	rail := ui.Row(c.Key("minimized-rail")).Absolute().Bottom(10).Right(10).Gap(6).AlignItems(ui.Center)
	rail.Children(func() {
		for _, f := range floats {
			if f.Pane == nil {
				continue
			}
			name, _ := f.Pane.label()
			label := locales.FloatRestore(name)
			chip := ui.Row(c.Key(minimizedKey(f.Pane.ID))).Gap(6).Padding(4, 8).Radius(k.radius(radiusM)).
				AlignItems(ui.Center).Background(k.panel).Border(k.borderW(), k.panelBorder).
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
			chip.Tooltip(label)
			if chip.Clicked() {
				a.later(c, func() { a.showFloat(f) })
			}
			chip.Children(func() {
				glyphIcon(c, "square-terminal", 12, k.iconMuted)
				ui.Text(c, name).FontSize(fontFootnote).FontWeight(500).TextColor(k.text).
					SingleLine().Ellipsis("…").MaxWidth(180)
			})
		}
	})
}

// floatHandle is the row a float keeps when pane headers are off: a grip
// that moves it, and its minimize, zoom and close buttons.
func (a *App) floatHandle(c *ui.Context, k *colors, t *Tab, p *Pane, f *Float) {
	h := ui.Row(c).Height(14).Shrink(0).Padding(0, 4, 0, 6).AlignItems(ui.Center).Cursor(ui.CursorMove)
	a.dragFloat(c, f, h)
	if h.Pressed() {
		a.raiseFloat(f)
		t.setFocus(p)
		a.focusReq = p
	}
	h.Children(func() {
		ui.Spacer(c)
		if iconButton(c, k, "minus", locales.PaneMinimize(), 18, 12).Tooltip(locales.PaneMinimize()).Clicked() {
			a.later(c, func() { a.hideFloat(f) })
		}
		zoom, label := "maximize-2", locales.PaneZoom()
		if f.Zoom {
			zoom, label = "minimize-2", locales.PaneUnzoom()
		}
		if iconButton(c, k, zoom, label, 18, 12).Tooltip(label).Clicked() {
			t.setFocus(p)
			a.toggleZoom()
		}
		x := iconButton(c, k, "x", locales.CmdClosePane(), 18, 11).Tooltip(locales.CmdClosePane())
		if x.Clicked() {
			a.later(c, func() { a.askClosePane(p) })
		}
		a.closeConfirm(c, k, x, t, p)
	})
}

// dragFloat moves float f as its handle h is dragged, once the pointer
// went floatDragStart from where it pressed, and throws it once the drag
// ends, at the pointer's velocity (throwFloat).
//
// It is not MyGo's Draggable: that moves the element the pointer presses,
// while a float moves by its header, an element of its own (clicked,
// double-clicked, with a menu) inside the float's cell; and the cell's
// Draggable, moved by the app instead, would start its spring at one
// velocity for both axes.
func (a *App) dragFloat(c *ui.Context, f *Float, h ui.Element) {
	dx, dy, pressed := h.Dragged()
	if !pressed {
		f.travel = [2]float32{}
	} else {
		a.catchFloat(f)
		if !f.dragging {
			f.travel[0] += dx
			f.travel[1] += dy
			dx, dy = 0, 0
			if t := f.travel; t[0]*t[0]+t[1]*t[1] >= floatDragStart*floatDragStart {
				f.dragging, f.travel = true, [2]float32{}
				dx, dy = t[0], t[1]
			}
		}
	}
	room := a.floatRoom
	if f.free() {
		// The header's drags come in the canvas's DIPs.
		room = ui.Rect{W: f.Pane.Tab.canvas.W, H: f.Pane.Tab.canvas.H}
	}
	if f.dragging && (dx != 0 || dy != 0) {
		a.moveFloatPx(f, dx, dy, room)
	}
	if f.dragging && !pressed {
		vx, vy, _ := h.DragEnded()
		a.throwFloat(f, vx, vy, room, motion(c) == nil)
	}
}

// glideOver is how far, in DIPs, a gliding float shows past the room's
// edge at most.
const glideOver = 24

// glideRate is the rate, in radians per second, of the spring of anim: a
// glide starting no faster than the distance to its rest times it settles
// there without flying past.
func glideRate(anim *ui.Animation) float32 {
	d := anim.Spring.Duration
	if d <= 0 {
		d = 500 * time.Millisecond
	}
	return float32(2 * math.Pi / d.Seconds())
}

// The keys of the animated place of a float gliding to rest.
type glideKey int

const (
	glideX glideKey = iota
	glideY
)

// rubber is how far a float shows past the room's edge, dragged d past it:
// half as far at first, less and less as d grows, as a scroll view's
// rubber band.
func rubber(d float32) float32 {
	const most, give = 0.6, 0.55
	if d == 0 {
		return 0
	}
	m := most * (1 - 1/(abs(d)*give/most+1))
	if d < 0 {
		return -m
	}
	return m
}
