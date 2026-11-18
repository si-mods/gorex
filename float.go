package main

import (
	"math"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
)

// Float is a pane over its tab's tiled panes, at a box of the tab's room
// in fractions: X, Y, W, H span [X, X+W) and [Y, Y+H).
type Float struct {
	Pane       *Pane
	X, Y, W, H float32
	Hidden     bool
	// Zoom makes it fill the tab's room, over the tiled panes.
	Zoom bool
	// dragging tells that the pointer moves or resizes it, and overX,
	// overY how far past the room's edge the drag took its box.
	dragging     bool
	overX, overY float32
	// fling is how a float the pointer let go of goes to rest, for the
	// view to glide it there from where it showed; gliding tells that it
	// is on its way.
	fling   *floatFling
	gliding bool
	// shown is where the view showed its box last, as fractions of the
	// room: where a press catches it gliding. built is the pass of the
	// view (App.passes) that built it last.
	shown [2]float32
	built uint64
	// travel is how far the pointer pressing its handle went, in DIPs,
	// before it went far enough to drag it.
	travel [2]float32
}

// floatFling is how a float let go of goes to rest: from where it showed,
// x and y, at vx, vy, in fractions of the room and per second; started
// tells that the view started its glide, and inside along each axis that
// it showed within the room as it was let go.
type floatFling struct {
	x, y, vx, vy float32
	started      bool
	inside       [2]bool
}

// floatDragStart is how far, in DIPs, the pointer pressing a float's
// handle goes before it drags the float, as MyGo's drags: a click that
// jitters does not move it.
const floatDragStart = 4

// A float thrown faster than flingSnapSpeed DIPs per second along an axis
// comes to rest against the room's edge it would come within snapDistance
// DIPs of along that axis. A float dropped slowly rests where it is.
const (
	flingSnapSpeed = 300
	snapDistance   = 32
)

// maxFling is the fastest a thrown float goes, in DIPs per second, as
// MyGo's Draggable: moves the window delivers at once, after a slow frame,
// would otherwise throw it far away.
const maxFling = 8000

// throwFloat lets go of f, dragged by the pointer, at vx, vy DIPs per
// second over room. Thrown, its box goes where the throw would carry it
// (Project), in the room, and against an edge of the room it comes within
// snapDistance of; the view glides it there from where it showed. Still,
// as with no animations, it rests where it was let go, in the room.
func (a *App) throwFloat(f *Float, vx, vy float32, room ui.Rect, still bool) {
	x, y := f.X+rubber(f.overX), f.Y+rubber(f.overY)
	f.dragging, f.overX, f.overY = false, 0, 0
	// A canvas has no edge to throw it against: it rests where let go.
	still = still || f.free()
	if float32IsBad(vx) || float32IsBad(vy) || room.W <= 0 || room.H <= 0 {
		vx, vy = 0, 0
	}
	if n := float32(math.Hypot(float64(vx), float64(vy))); n > maxFling {
		vx, vy = vx*maxFling/n, vy*maxFling/n
	}
	if still {
		f.fling = nil
		a.moveFloat(f, x-f.X, y-f.Y)
		return
	}
	var fx, fy float32
	if vx != 0 || vy != 0 {
		fx, fy = vx/room.W, vy/room.H
	}
	f.fling = &floatFling{x: x, y: y, vx: fx, vy: fy,
		inside: [2]bool{x >= 0 && x <= 1-f.W, y >= 0 && y <= 1-f.H}}
	tx := restFloat(ui.Project(x, fx), f.W, vx, room.W)
	ty := restFloat(ui.Project(y, fy), f.H, vy, room.H)
	a.moveFloat(f, tx-f.X, ty-f.Y)
}

// restFloat returns where a side of a float, of length w, comes to rest
// thrown to x at v DIPs per second, the room being length DIPs along it:
// in the room, and against its edge when thrown fast to within
// snapDistance of it.
func restFloat(x, w, v, length float32) float32 {
	hi := max(1-w, 0)
	x = min(max(x, 0), hi)
	if abs(v) < flingSnapSpeed || length <= 0 {
		return x
	}
	switch lo, far := x*length, (hi-x)*length; {
	case lo <= far && lo <= snapDistance:
		return 0
	case far <= snapDistance:
		return hi
	}
	return x
}

// glideVelocity is the velocity, in fractions of the room per second, a
// float's glide starts at, thrown at v from x to rest at to: none away
// from to, and no faster than reaches it as a spring of rate rad/s would
// without going past it, so that it settles there rather than flying past
// the room's edge.
func glideVelocity(v, x, to, rate float32) float32 {
	d := to - x
	if v*d <= 0 {
		return 0
	}
	if most := abs(d) * rate; abs(v) > most {
		return most * v / abs(v)
	}
	return v
}

// edgeBand returns x, a side of a float, of length w, shown past the
// room's edges less and less far, never as far as limit, for a float
// gliding toward them.
func edgeBand(x, w, limit float32) float32 {
	band := func(over float32) float32 { return limit * (1 - 1/(over/limit+1)) }
	hi := max(1-w, 0)
	switch {
	case limit <= 0:
		return x
	case x < 0:
		return -band(-x)
	case x > hi:
		return hi + band(x-hi)
	}
	return x
}

// catchFloat stops f, gliding, where it shows, as a press on it does.
func (a *App) catchFloat(f *Float) {
	if f == nil || !f.gliding {
		return
	}
	f.X, f.Y, f.gliding, f.fling = f.shown[0], f.shown[1], false, nil
	f.clamp()
	a.changed()
}

// floatMin is the least share of the room a float's side takes.
const floatMin = layout.FloatMin

// clamp keeps a float's box in the room: each side at least floatMin and at
// most 1, and the box wholly inside [0,1]. NaN/Inf become the defaults.
func (f *Float) clamp() {
	if float32IsBad(f.X) {
		f.X = 0.15
	}
	if float32IsBad(f.Y) {
		f.Y = 0.12
	}
	if float32IsBad(f.W) {
		f.W = 0.7
	}
	if float32IsBad(f.H) {
		f.H = 0.72
	}
	if f.free() {
		f.W, f.H = max(f.W, floatMin), max(f.H, floatMin)
		return
	}
	f.W = min(max(f.W, floatMin), 1)
	f.H = min(max(f.H, floatMin), 1)
	f.X = min(max(f.X, 0), 1-f.W)
	f.Y = min(max(f.Y, 0), 1-f.H)
}

// clampSize keeps the box of a resized float in the room: each side at
// least floatMin and at most the room right of and below X, Y, which stay
// put unless the least size no longer fits. NaN/Inf become the defaults.
func (f *Float) clampSize() {
	if float32IsBad(f.W) {
		f.W = 0.7
	}
	if float32IsBad(f.H) {
		f.H = 0.72
	}
	if f.free() {
		f.W, f.H = max(f.W, floatMin), max(f.H, floatMin)
		return
	}
	f.W = min(max(f.W, floatMin), 1)
	f.H = min(max(f.H, floatMin), 1)
	f.W = min(f.W, 1-f.X)
	f.H = min(f.H, 1-f.Y)
	// The least size past the edge moves the box back in.
	if f.W < floatMin {
		f.W = floatMin
		f.X = 1 - f.W
	}
	if f.H < floatMin {
		f.H = floatMin
		f.Y = 1 - f.H
	}
}
