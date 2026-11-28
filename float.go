package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
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

// floatBox is the box of a new float over the tab's tiled panes, each one
// offset from the last.
func floatBox(n int) (x, y, w, h float32) {
	d := float32(n) * 0.03
	return 0.15 + d, 0.12 + d, 0.7, 0.72
}

// floatOver makes pane p float over tab t, at the box of the saved float sf.
func (a *App) floatOver(t *Tab, p *Pane, sf savedFloat) *Float {
	f := &Float{Pane: p, X: sf.X, Y: sf.Y, W: sf.W, H: sf.H, Hidden: sf.Hidden, Zoom: sf.Zoom}
	f.clamp()
	p.Node = &Node{ID: a.id(), Pane: p}
	p.float = f
	t.Floats = append(t.Floats, f)
	return f
}

// floatPane makes a tiled pane of the tab float over its others. It does
// nothing for a pane that floats, or for the tab's only tiled pane.
func (a *App) floatPane(p *Pane) bool {
	if p == nil || p.float != nil || p.Tab == nil || p.Node == nil {
		return false
	}
	t := p.Tab
	if len(t.panes()) < 2 {
		return false
	}
	a.unlinkTiled(p)
	if t.Zoom == p {
		t.Zoom = nil
	}
	x, y, w, h := floatBox(len(t.Floats))
	a.floatOver(t, p, savedFloat{X: x, Y: y, W: w, H: h})
	t.setFocus(p)
	if a.tab() == t {
		a.focusReq = p
	}
	a.changed()
	return true
}

// dockPane puts a float back among its tab's tiled panes, beside the one
// focused last there.
func (a *App) dockPane(p *Pane) bool {
	// A canvas has no tiled panes to dock among.
	if p == nil || p.float == nil || p.Tab == nil || p.Tab.canvas != nil {
		return false
	}
	t, f := p.Tab, p.float
	target := t.tiledTarget()
	t.removeFloat(f)
	p.Node = nil
	a.placePane(t, p, target, false, false, 0.5)
	if a.tab() == t {
		a.focusReq = p
	}
	a.changed()
	return true
}

// newFloat opens a terminal session in the active tab as a float, on the
// tab's host, in the current directory.
func (a *App) newFloat(command []string) *Pane { return a.newFloatThen(command, nil) }

// newFloatThen is newFloat, then runs then with the float once the window
// shows it: of this machine, the server makes it, and it returns it only
// as tests have it, at once; of a host, it shows as the host made its
// session, unless the tab closed meanwhile.
func (a *App) newFloatThen(command []string, then func(*Pane)) *Pane {
	t := a.tab()
	if t == nil {
		return nil
	}
	h := t.host()
	if a.offline(h) {
		return nil
	}
	if serverMade(h) {
		return a.askPane(t.Focus, rex.LayoutCommand{Op: "pane.split", Float: true, Dir: a.currentDir(), Command: command}, func(p *Pane) {
			a.revealNew(p)
			if then != nil {
				then(p)
			}
		})
	}
	cols, rows := t.Focus.paneSize()
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	a.newPaneRun(h, a.currentDir(), cols, rows, command, func(p *Pane) bool {
		if !slices.Contains(a.tabs, t) {
			return false
		}
		p.Tab = t
		x, y, w, hh := floatBox(len(t.Floats))
		a.revealFloat(t, a.floatOver(t, p, savedFloat{X: x, Y: y, W: w, H: hh}))
		t.setFocus(p)
		if a.tab() == t {
			a.focusReq = p
		}
		a.changed()
		if then != nil {
			then(p)
		}
		return true
	})
	return nil
}

// raiseFloat puts f above the tab's other floats, and lowerFloat below
// them.
func (a *App) raiseFloat(f *Float) {
	if f == nil || f.Pane == nil || f.Pane.Tab == nil {
		return
	}
	t := f.Pane.Tab
	i := slices.Index(t.Floats, f)
	if i < 0 || i == len(t.Floats)-1 {
		return
	}
	t.Floats = append(slices.Delete(t.Floats, i, i+1), f)
	a.changed()
}

func (a *App) lowerFloat(f *Float) {
	if f == nil || f.Pane == nil || f.Pane.Tab == nil {
		return
	}
	t := f.Pane.Tab
	i := slices.Index(t.Floats, f)
	if i <= 0 {
		return
	}
	t.Floats = slices.Insert(slices.Delete(t.Floats, i, i+1), 0, f)
	a.changed()
}

// revealPane brings a pane into sight: a hidden float shows again, above
// the tab's other floats.
func (a *App) revealPane(p *Pane) {
	if p == nil || p.float == nil {
		return
	}
	p.float.Hidden = false
	a.raiseFloat(p.float)
}

// setFloatBox puts f at a box of the tab's room, kept inside it.
func (a *App) setFloatBox(f *Float, x, y, w, h float32) {
	if f == nil {
		return
	}
	f.X, f.Y, f.W, f.H = x, y, w, h
	f.clamp()
	a.changed()
}

// moveFloat moves f by fractions of the tab's room, and resizeFloat changes
// its size, both kept inside it.
func (a *App) moveFloat(f *Float, dx, dy float32) {
	if f == nil {
		return
	}
	f.X, f.Y = f.X+dx, f.Y+dy
	f.clamp()
	a.changed()
}

func (a *App) resizeFloat(f *Float, dw, dh float32) {
	if f == nil {
		return
	}
	f.W, f.H = f.W+dw, f.H+dh
	f.clampSize()
	a.changed()
}

// resizeEdges moves the sides e of f's box by dx, dy, fractions of the
// room, the other sides staying put: no side shorter than floatMin, and
// the box in the room.
func (f *Float) resizeEdges(e int, dx, dy float32) {
	right, bottom := f.X+f.W, f.Y+f.H
	if f.free() {
		// On a canvas there is no room's edge to keep inside.
		if e&edgeLeft != 0 {
			f.X = min(f.X+dx, right-floatMin)
			f.W = right - f.X
		}
		if e&edgeRight != 0 {
			f.W = max(f.W+dx, floatMin)
		}
		if e&edgeTop != 0 {
			f.Y = min(f.Y+dy, bottom-floatMin)
			f.H = bottom - f.Y
		}
		if e&edgeBottom != 0 {
			f.H = max(f.H+dy, floatMin)
		}
		return
	}
	if e&edgeLeft != 0 {
		f.X = min(max(f.X+dx, 0), right-floatMin)
		f.W = right - f.X
	}
	if e&edgeRight != 0 {
		f.W = min(max(f.W+dx, floatMin), 1-f.X)
	}
	if e&edgeTop != 0 {
		f.Y = min(max(f.Y+dy, 0), bottom-floatMin)
		f.H = bottom - f.Y
	}
	if e&edgeBottom != 0 {
		f.H = min(max(f.H+dy, floatMin), 1-f.Y)
	}
}

// moveFloatPx moves f by pixels over room.
func (a *App) moveFloatPx(f *Float, dx, dy float32, room ui.Rect) {
	if room.W <= 0 || room.H <= 0 {
		return
	}
	if !f.dragging {
		a.moveFloat(f, dx/room.W, dy/room.H)
		return
	}
	// The box goes where the pointer takes it, kept in the room; how far
	// past its edge the pointer went is kept apart, for the view to show.
	x, y := f.X+f.overX+dx/room.W, f.Y+f.overY+dy/room.H
	a.moveFloat(f, x-f.X, y-f.Y)
	f.overX, f.overY = x-f.X, y-f.Y
}

// hideFloat hides f without closing its session, and gives the focus to a
// pane still in sight when f had it.
func (a *App) hideFloat(f *Float) {
	if f == nil || f.Pane == nil || f.Pane.Tab == nil {
		return
	}
	t := f.Pane.Tab
	f.Hidden = true
	// Out of sight, it is at rest: it keeps no zoom, and comes back at
	// the box it was at.
	f.Zoom, f.gliding, f.fling = false, false, nil
	t.hiddenLast = f
	if t.Focus == f.Pane {
		t.Focus = nil
		next := t.nextFocus(f.Pane)
		t.setFocus(next)
		if a.tab() == t {
			a.focusReq = next
		}
	}
	a.changed()
}

// showFloat shows f above the tab's other floats, with the focus.
func (a *App) showFloat(f *Float) {
	if f == nil || f.Pane == nil || f.Pane.Tab == nil {
		return
	}
	t := f.Pane.Tab
	f.Hidden = false
	a.raiseFloat(f)
	t.setFocus(f.Pane)
	if a.tab() == t {
		a.focusReq = f.Pane
	}
	a.changed()
}

// setFloatState makes the floating pane sid the state Rex's
// session.set_layer_state asks for: hidden is a minimized layer. With
// focus, the pane is shown above the tab's others, and its tab and the
// pane take the focus.
func (a *App) setFloatState(sid, state string, focus bool) error {
	p := a.localPane(sid)
	if p == nil {
		return fmt.Errorf("no pane %s", sid)
	}
	f := p.float
	if f == nil {
		return fmt.Errorf("%s is not floating", sid)
	}
	t := p.Tab
	if t == nil {
		return fmt.Errorf("%s is not in a tab", sid)
	}
	if state == "minimized" {
		if f.Hidden {
			return nil
		}
		// A minimized pane is at rest: it keeps no zoom.
		f.Zoom = false
		if t.Zoom == p {
			t.Zoom = nil
		}
		a.hideFloat(f)
		return nil
	}
	if focus {
		if i := slices.Index(a.tabs, t); i >= 0 {
			a.selectTab(i)
		}
		a.revealPane(p)
		t.setFocus(p)
		a.focusReq = p
		a.changed()
		return nil
	}
	if f.Hidden {
		f.Hidden = false
		a.changed()
	}
	return nil
}

// toggleFloat hides the focused float, or shows the float hidden last, else
// the topmost hidden one, else opens a new one.
func (a *App) toggleFloat() {
	t := a.tab()
	if t == nil {
		return
	}
	if t.Focus != nil {
		if f := t.Focus.float; f != nil && !f.Hidden {
			a.hideFloat(f)
			return
		}
	}
	if f := t.hiddenLast; f != nil && slices.Contains(t.Floats, f) && f.Hidden {
		a.showFloat(f)
		return
	}
	for i := len(t.Floats) - 1; i >= 0; i-- {
		if t.Floats[i].Hidden {
			a.showFloat(t.Floats[i])
			return
		}
	}
	a.newFloat(nil)
}

// toggleAllFloats hides every float of the tab, or shows them all.
func (a *App) toggleAllFloats() {
	t := a.tab()
	if t == nil || len(t.Floats) == 0 {
		return
	}
	if slices.ContainsFunc(t.Floats, func(f *Float) bool { return !f.Hidden }) {
		for _, f := range t.Floats {
			f.Hidden = true
		}
		if t.Focus != nil && t.Focus.float != nil {
			t.Focus = nil
			next := t.nextFocus(nil)
			t.setFocus(next)
			if a.tab() == t {
				a.focusReq = next
			}
		}
	} else {
		for _, f := range t.Floats {
			f.Hidden = false
		}
	}
	a.changed()
}

// cycleFloat focuses the next visible float below the focused one, or the
// topmost visible float from a tiled pane, and the tiled pane from the
// lowest visible float. Floats keep their order as it goes.
func (a *App) cycleFloat() {
	t := a.tab()
	if t == nil {
		return
	}
	var visible []*Float
	for _, f := range t.Floats {
		if !f.Hidden {
			visible = append(visible, f)
		}
	}
	if len(visible) == 0 {
		return
	}
	focus := func(p *Pane) {
		t.setFocus(p)
		if a.tab() == t {
			a.focusReq = p
		}
		a.changed()
	}
	i := -1
	if t.Focus != nil && t.Focus.float != nil {
		i = slices.Index(visible, t.Focus.float)
	}
	switch {
	case i < 0:
		focus(visible[len(visible)-1].Pane)
	case i > 0:
		focus(visible[i-1].Pane)
	default:
		if next := t.tiledTarget(); next != nil {
			focus(next)
		}
	}
}

// promoteFloat makes the tab's topmost float its tiled pane, when the tab
// has lost the one it had. It reports whether it did.
func (a *App) promoteFloat(t *Tab) bool {
	// A canvas has no tiled panes to promote one to.
	if t == nil || t.Root != nil || len(t.Floats) == 0 || t.canvas != nil {
		return false
	}
	f := t.Floats[len(t.Floats)-1]
	t.Floats = t.Floats[:len(t.Floats)-1]
	if t.hiddenLast == f {
		t.hiddenLast = nil
	}
	f.Pane.float = nil
	n := f.Pane.Node
	if n == nil {
		n = &Node{ID: a.id()}
	}
	n.Pane, n.Parent, n.A, n.B = f.Pane, nil, nil, nil
	f.Pane.Tab, f.Pane.Node = t, n
	t.Root = n
	t.Zoom = nil
	t.setFocus(f.Pane)
	return true
}
