package main

import (
	"cmp"
	"math"
	"slices"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
)

// The infinite canvas, an experiment of the Canvas setting: a tab has no
// tiled panes, and every pane is a float on an unbounded plane, which the
// wheel and a drag on the empty plane pan, and Ctrl or Cmd and the wheel
// zoom.
//
// A float's X, Y, W, H are fractions of the canvas's W, H, the tab's room
// as it became a canvas and fixed from then on: the window's size changes
// no pane's, so no terminal resizes with it, and an older client, which
// reads the fractions as of its room, still shows every pane. The plane
// shows through a transform, which changes no pane's laid-out size: a
// terminal keeps its columns and rows at every zoom.

// The least and the most a canvas zooms.
const (
	canvasMinScale = 0.25
	canvasMaxScale = 2
)

// canvasDefault is the room of a saved canvas that has none.
var canvasDefault = ui.Rect{W: 1280, H: 800}

// The keys of a canvas's background and of its layer of panes.
type (
	canvasBackKey  int
	canvasLayerKey int
)

// free reports whether f is a float of a canvas, its box not kept inside
// the room.
func (f *Float) free() bool {
	return f != nil && f.Pane != nil && f.Pane.Tab != nil && f.Pane.Tab.canvas != nil
}

// shownFloats returns the panes of the tab's floats not hidden, the lowest
// first.
func (t *Tab) shownFloats() []*Pane {
	var out []*Pane
	for _, f := range t.Floats {
		if !f.Hidden && f.Pane != nil {
			out = append(out, f.Pane)
		}
	}
	return out
}

// loadCanvas returns a saved tab's canvas, with a size and a scale that
// make sense, nil for a tab of tiled panes.
func loadCanvas(s *layout.Canvas) *layout.Canvas {
	if s == nil {
		return nil
	}
	cv := *s
	if float32IsBad(cv.W) || float32IsBad(cv.H) || cv.W <= 0 || cv.H <= 0 {
		cv.W, cv.H = canvasDefault.W, canvasDefault.H
	}
	if float32IsBad(cv.X) || float32IsBad(cv.Y) {
		cv.X, cv.Y = 0, 0
	}
	if float32IsBad(cv.Scale) || cv.Scale <= 0 {
		cv.Scale = 1
	}
	cv.Scale = min(max(cv.Scale, canvasMinScale), canvasMaxScale)
	return &cv
}

// syncCanvas makes t a canvas, or tiled again, as the setting says, room
// being the tab's room. Tiled panes added to a canvas, as a new tab's or
// one moved there, become floats where they show.
func (a *App) syncCanvas(t *Tab, room ui.Rect) {
	switch {
	case prefs.Canvas && (t.canvas == nil || t.Root != nil):
		a.toCanvas(t, room)
	case !prefs.Canvas && t.canvas != nil:
		a.leaveCanvas(t)
	}
}

// syncCanvases syncs every tab of the window with the setting.
func (a *App) syncCanvases() {
	for _, t := range a.tabs {
		a.syncCanvas(t, a.floatRoom)
	}
}

// setCanvas turns the canvas on or off, keeps it, and shows it in every
// window.
func (a *App) setCanvas(on bool) {
	a.setWindowPref(func(s *settings) { s.Canvas = on })
	for _, o := range a.peers() {
		o.do(o.syncCanvases)
	}
}

// toCanvas makes t a canvas: each tiled pane a float at the box it shows
// at in room, below the floats it had. It waits for a room measured.
func (a *App) toCanvas(t *Tab, room ui.Rect) {
	if room.W <= 0 || room.H <= 0 {
		return
	}
	if t.canvas == nil {
		t.canvas = &layout.Canvas{W: room.W, H: room.H, Scale: 1}
	}
	cv := t.canvas
	var made []*Float
	t.Root.shares(whole, func(p *Pane, s share) {
		top, right, bottom, left := cellInsets(s)
		x, y := s.x*room.W+left, s.y*room.H+top
		w, h := s.w*room.W-left-right, s.h*room.H-top-bottom
		f := &Float{Pane: p,
			X: (cv.X + x/cv.Scale) / cv.W, Y: (cv.Y + y/cv.Scale) / cv.H,
			W: w / cv.Scale / cv.W, H: h / cv.Scale / cv.H,
			Zoom: t.Zoom == p}
		p.Node, p.float = &Node{ID: a.id(), Pane: p}, f
		f.clamp()
		made = append(made, f)
	}, func(*Node, share) {})
	t.Root, t.Zoom = nil, nil
	t.Floats = append(made, t.Floats...)
	a.changed()
}

// leaveCanvas makes t tiled again: every float docks, left to right as
// they lay, and the panes share the room evenly.
func (a *App) leaveCanvas(t *Tab) {
	t.canvas = nil
	focus := t.Focus
	fs := slices.Clone(t.Floats)
	slices.SortStableFunc(fs, func(f, g *Float) int { return cmp.Or(cmp.Compare(f.X, g.X), cmp.Compare(f.Y, g.Y)) })
	for _, f := range fs {
		a.dockPane(f.Pane)
	}
	equalizeTree(t.Root)
	if focus != nil {
		t.setFocus(focus)
		if a.tab() == t {
			a.focusReq = focus
		}
	}
	a.changed()
}

// placeOnCanvas puts pane p on t's canvas, in a box of of's size right of
// it, or below it, the gap apart, and past the boxes there, and pans the
// canvas to show it.
func (a *App) placeOnCanvas(t *Tab, of *Float, p *Pane, below bool) {
	cv := t.canvas
	gx, gy := gap/cv.W, gap/cv.H
	f := &Float{Pane: p, X: of.X + of.W + gx, Y: of.Y, W: of.W, H: of.H}
	if below {
		f.X, f.Y = of.X, of.Y+of.H+gy
	}
	overlaps := func(g *Float) bool {
		return !g.Hidden && f.X < g.X+g.W && g.X < f.X+f.W && f.Y < g.Y+g.H && g.Y < f.Y+f.H
	}
	// shortcut: steps past the boxes in one direction only; a packing
	// search would fill holes, if a crowded canvas needs it.
	for range 64 {
		if !slices.ContainsFunc(t.Floats, overlaps) {
			break
		}
		if below {
			f.Y += f.H + gy
		} else {
			f.X += f.W + gx
		}
	}
	p.Tab, p.Node, p.float = t, &Node{ID: a.id(), Pane: p}, f
	t.Floats = append(t.Floats, f)
	a.revealFloat(t, f)
}

// revealFloat pans t's canvas as little as shows f in the window's room,
// its top left first when it is the larger.
func (a *App) revealFloat(t *Tab, f *Float) {
	cv, room := t.canvas, a.floatRoom
	if cv == nil || room.W <= 0 || room.H <= 0 {
		return
	}
	vw, vh := room.W/cv.Scale, room.H/cv.Scale
	x0, y0 := f.X*cv.W-gap, f.Y*cv.H-gap
	x1, y1 := (f.X+f.W)*cv.W+gap, (f.Y+f.H)*cv.H+gap
	if x1 > cv.X+vw {
		cv.X = x1 - vw
	}
	if y1 > cv.Y+vh {
		cv.Y = y1 - vh
	}
	cv.X, cv.Y = min(cv.X, x0), min(cv.Y, y0)
}

// resetCanvasView shows the active tab's canvas from its start, at its
// size.
func (a *App) resetCanvasView() {
	if t := a.tab(); t != nil && t.canvas != nil {
		t.canvas.X, t.canvas.Y, t.canvas.Scale = 0, 0, 1
		a.changed()
	}
}

// canvasRoom makes room, t's, a canvas's: what shows past it is cut off,
// and a scroll over its empty plane pans or zooms it.
func (a *App) canvasRoom(t *Tab, room ui.Element) {
	room.Clip().HandleInput(func(ev ui.InputEvent) bool {
		return ev.Kind == ui.InputScroll && a.canvasScroll(t, ev)
	})
}

// canvasContent lays out the panes of t, a canvas, in its room, of bounds:
// over a background a drag on pans, a layer of the floats, moved and
// scaled as the canvas shows, and a zoomed float over it, filling the
// room. The window's background shows through the plane.
func (a *App) canvasContent(c *ui.Context, k *colors, t *Tab, bounds ui.Rect) {
	cv := t.canvas
	back := ui.Box(c.Key(canvasBackKey(t.ID))).Absolute().Left(0).Top(0).WidthPercent(100).HeightPercent(100)
	if dx, dy, ok := back.Dragged(); ok && (dx != 0 || dy != 0) {
		cv.X -= dx / cv.Scale
		cv.Y -= dy / cv.Scale
		a.changed()
	}
	layer := ui.Box(c.Key(canvasLayerKey(t.ID))).Absolute().Left(0).Top(0).Size(cv.W, cv.H).PassThrough().
		TransformOrigin(0, 0).Transform(ui.Translation(-cv.X, -cv.Y).Then(ui.Scaling(cv.Scale, cv.Scale)))
	var zoomed *Float
	layer.Children(func() {
		for _, f := range slices.Clone(t.Floats) {
			switch {
			case f.Hidden:
			case f.Zoom:
				zoomed = f
			default:
				// Its box in the layer's DIPs, which the drags of its
				// header and edges come in.
				a.floatCell(c, k, t, f, ui.Rect{W: cv.W, H: cv.H})
			}
		}
	})
	if zoomed != nil {
		a.floatCell(c, k, t, zoomed, bounds)
	}
}

// canvasScroll pans t's canvas by a scroll over its empty plane, or zooms
// it around the pointer with Ctrl or Cmd held, and reports whether it did:
// a scroll over a pane is the pane's.
func (a *App) canvasScroll(t *Tab, ev ui.InputEvent) bool {
	cv := t.canvas
	if cv == nil {
		return false
	}
	px, py := cv.X+ev.X/cv.Scale, cv.Y+ev.Y/cv.Scale
	for _, f := range t.Floats {
		if f.Hidden {
			continue
		}
		if f.Zoom || px >= f.X*cv.W && px < (f.X+f.W)*cv.W && py >= f.Y*cv.H && py < (f.Y+f.H)*cv.H {
			return false
		}
	}
	if ev.Mods&(ui.Ctrl|ui.Super) != 0 {
		s := cv.Scale * float32(math.Exp(-float64(ev.DY)/300))
		s = min(max(s, canvasMinScale), canvasMaxScale)
		// The point under the pointer stays there.
		cv.X, cv.Y, cv.Scale = px-ev.X/s, py-ev.Y/s, s
	} else {
		dx, dy := ev.DX, ev.DY
		if ev.Mods&ui.Shift != 0 && dx == 0 {
			dx, dy = dy, 0
		}
		cv.X += dx / cv.Scale
		cv.Y += dy / cv.Scale
	}
	a.changed()
	return true
}
