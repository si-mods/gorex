package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/locales"
)

// canvasPrefs turns the canvas setting on for the test, and back after it.
func canvasPrefs(t *testing.T, on bool) {
	t.Helper()
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs.Canvas = on
}

// sids returns the sessions of the tab's panes, sorted.
func sids(t *Tab) []string {
	var out []string
	for _, p := range t.allPanes() {
		out = append(out, p.SID)
	}
	slices.Sort(out)
	return out
}

func TestCanvasSettingDefault(t *testing.T) {
	var s settings
	if s.Canvas {
		t.Fatal("the canvas is on by default")
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "canvas") {
		t.Errorf("an unset canvas is saved: %s", b)
	}
	s.Canvas = true
	b, _ = json.Marshal(s)
	var back settings
	if err := json.Unmarshal(b, &back); err != nil || !back.Canvas {
		t.Errorf("canvas did not round-trip: %s, %v", b, err)
	}
}

// TestCanvasKeepsBoxes checks that a tab becoming a canvas keeps each pane
// at the box it showed at, and every session.
func TestCanvasKeepsBoxes(t *testing.T) {
	canvasPrefs(t, false)
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	a.split(true)
	room := ui.Rect{W: 1000, H: 600}
	want := map[*Pane]ui.Rect{}
	tab.Root.shares(whole, func(p *Pane, s share) {
		top, right, bottom, left := cellInsets(s)
		want[p] = ui.Rect{X: s.x*room.W + left, Y: s.y*room.H + top, W: s.w*room.W - left - right, H: s.h*room.H - top - bottom}
	}, func(*Node, share) {})
	before, focus := sids(tab), tab.Focus

	prefs.Canvas = true
	a.syncCanvas(tab, room)
	if tab.Root != nil || tab.canvas == nil || len(tab.Floats) != 3 {
		t.Fatalf("root %v, canvas %v, %d floats", tab.Root, tab.canvas, len(tab.Floats))
	}
	if cv := tab.canvas; cv.W != room.W || cv.H != room.H || cv.Scale != 1 {
		t.Errorf("canvas %+v", *cv)
	}
	for p, r := range want {
		f := p.float
		if f == nil {
			t.Fatalf("pane %d is not on the canvas", p.ID)
		}
		got := ui.Rect{X: f.X * room.W, Y: f.Y * room.H, W: f.W * room.W, H: f.H * room.H}
		if !near(got.X, r.X) || !near(got.Y, r.Y) || !near(got.W, r.W) || !near(got.H, r.H) {
			t.Errorf("pane %d at %+v, showed at %+v", p.ID, got, r)
		}
	}
	if !slices.Equal(sids(tab), before) || tab.Focus != focus {
		t.Errorf("sessions %v, want %v; focus moved", sids(tab), before)
	}
	// Synced again, it stays as it is.
	a.syncCanvas(tab, ui.Rect{W: 500, H: 300})
	if tab.canvas.W != room.W || len(tab.Floats) != 3 {
		t.Errorf("synced again: %+v, %d floats", *tab.canvas, len(tab.Floats))
	}
}

// TestCanvasOffDocks checks that turning the canvas off docks every box,
// keeping its session and terminal.
func TestCanvasOffDocks(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	tab := a.tab()
	a.syncCanvas(tab, ui.Rect{W: 1000, H: 600})
	a.split(false)
	a.split(true)
	if a.newFloat(nil) == nil {
		t.Fatal("no new float")
	}
	a.hideFloat(tab.Floats[0])
	before := sids(tab)
	terms := map[*Pane]any{}
	for _, p := range tab.allPanes() {
		terms[p] = p.term
	}
	focus := tab.Focus
	if tab.Root != nil || len(tab.Floats) != 4 {
		t.Fatalf("canvas: root %v, %d floats", tab.Root, len(tab.Floats))
	}

	prefs.Canvas = false
	a.syncCanvas(tab, ui.Rect{W: 1000, H: 600})
	if tab.canvas != nil || len(tab.Floats) != 0 || len(tab.panes()) != 4 {
		t.Fatalf("docked: canvas %v, %d floats, %d tiled", tab.canvas, len(tab.Floats), len(tab.panes()))
	}
	if !slices.Equal(sids(tab), before) {
		t.Errorf("sessions %v, want %v", sids(tab), before)
	}
	for p, term := range terms {
		if p.term != term || p.Tab != tab || p.float != nil {
			t.Errorf("pane %d lost its terminal or tab", p.ID)
		}
	}
	if tab.Focus != focus {
		t.Error("the focus moved")
	}
}

// TestCanvasSplitPlacement checks that a split on a canvas puts a box of
// the focused one's size right of it or below it, past the boxes there,
// and pans to show it.
func TestCanvasSplitPlacement(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	tab := a.tab()
	room := ui.Rect{W: 1000, H: 600}
	a.floatRoom = room
	a.syncCanvas(tab, room)
	first := tab.Focus.float
	first.X, first.Y, first.W, first.H = 0.1, 0.1, 0.4, 0.4
	cols, rows := tab.Focus.paneSize()
	gx, gy := gap/tab.canvas.W, gap/tab.canvas.H

	a.split(false)
	right := tab.Focus.float
	if right == nil || right == first || tab.Root != nil {
		t.Fatal("the split made no box on the canvas")
	}
	if !near(right.X, first.X+first.W+gx) || right.Y != first.Y || right.W != first.W || right.H != first.H {
		t.Errorf("right box %+v of %+v", *right, *first)
	}
	if c, r := tab.Focus.paneSize(); cols > 0 && (c != cols || r != rows) {
		t.Errorf("new pane %dx%d, want %dx%d", c, r, cols, rows)
	}
	if tab.canvas.X != 0 {
		t.Errorf("the canvas panned to a box in sight: %+v", *tab.canvas)
	}

	tab.setFocus(first.Pane)
	a.split(true)
	below := tab.Focus.float
	if !near(below.Y, first.Y+first.H+gy) || below.X != first.X {
		t.Errorf("box below %+v of %+v", *below, *first)
	}

	// Right of the first is taken: the next goes past it.
	tab.setFocus(first.Pane)
	a.split(false)
	past := tab.Focus.float
	if !near(past.X, right.X+right.W+gx) || past.Y != first.Y {
		t.Errorf("box past %+v of %+v", *past, *right)
	}
	if cv := tab.canvas; !near(cv.X+room.W, (past.X+past.W)*cv.W+gap) {
		t.Errorf("the canvas did not pan to show the box past the room: %+v", *cv)
	}

	// Closing boxes keeps the tab while one is left.
	a.closePane(past.Pane)
	a.closePane(below.Pane)
	a.closePane(right.Pane)
	if len(a.tabs) != 1 || len(tab.Floats) != 1 || tab.Focus != first.Pane {
		t.Errorf("%d tabs, %d floats left", len(a.tabs), len(tab.Floats))
	}
}

// TestCanvasScroll checks that a scroll over the empty plane pans the
// canvas, Ctrl and a scroll zoom it around the pointer, and a scroll over
// a pane is the pane's.
func TestCanvasScroll(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	tab := a.tab()
	a.syncCanvas(tab, ui.Rect{W: 1000, H: 600})
	f := tab.Floats[0]
	f.X, f.Y, f.W, f.H = 0.1, 0.1, 0.2, 0.2
	cv := tab.canvas
	if a.canvasScroll(tab, ui.InputEvent{Kind: ui.InputScroll, X: 150, Y: 100, DY: 30}) {
		t.Error("a scroll over a pane panned")
	}
	if !a.canvasScroll(tab, ui.InputEvent{Kind: ui.InputScroll, X: 800, Y: 500, DX: 10, DY: 30}) || cv.X != 10 || cv.Y != 30 {
		t.Errorf("panned to %+v", *cv)
	}
	px, py := cv.X+800/cv.Scale, cv.Y+500/cv.Scale
	a.canvasScroll(tab, ui.InputEvent{Kind: ui.InputScroll, X: 800, Y: 500, DY: -100, Mods: ui.Ctrl})
	if cv.Scale <= 1 || !near(cv.X+800/cv.Scale, px) || !near(cv.Y+500/cv.Scale, py) {
		t.Errorf("zoomed to %+v, the point under the pointer moved", *cv)
	}
	for range 50 {
		a.canvasScroll(tab, ui.InputEvent{Kind: ui.InputScroll, X: 800, Y: 500, DY: -500, Mods: ui.Ctrl})
	}
	if cv.Scale != canvasMaxScale {
		t.Errorf("zoomed past the most, to %v", cv.Scale)
	}
	a.resetCanvasView()
	if cv.X != 0 || cv.Y != 0 || cv.Scale != 1 {
		t.Errorf("reset to %+v", *cv)
	}
}

// TestCanvasLayoutRoundTrip checks that a canvas's view and boxes, past
// the room's edges too, come back from the saved layout, and that an old
// layout, without a canvas, loads as before.
func TestCanvasLayoutRoundTrip(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	tab := a.tab()
	a.syncCanvas(tab, ui.Rect{W: 1000, H: 600})
	a.split(false)
	f := tab.Focus.float
	a.setFloatBox(f, 1.6, -0.5, 0.3, 0.4)
	if f.X != 1.6 || f.Y != -0.5 {
		t.Fatalf("a canvas box was kept in the room: %+v", *f)
	}
	tab.canvas.X, tab.canvas.Y, tab.canvas.Scale = 120, -40, 0.5

	raw, err := json.Marshal(a.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var l savedLayout
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if st := l.Tabs[0]; st.Root != nil || st.Canvas == nil || *st.Canvas != *tab.canvas {
		t.Fatalf("saved %s", raw)
	}
	for _, p := range tab.allPanes() {
		if p.term != nil {
			p.term.Close()
		}
	}
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	bt := b.tabs[0]
	if bt.canvas == nil || *bt.canvas != *tab.canvas || bt.Root != nil || len(bt.Floats) != 2 {
		t.Fatalf("restored canvas %v, root %v, %d floats", bt.canvas, bt.Root, len(bt.Floats))
	}
	if g := bt.Floats[1]; g.X != 1.6 || g.Y != -0.5 || g.W != 0.3 || g.H != 0.4 {
		t.Errorf("restored box %+v", *g)
	}
	if bt.Focus != bt.Floats[1].Pane {
		t.Error("the focused box lost the focus")
	}
	for _, p := range bt.allPanes() {
		if p.term != nil {
			p.term.Close()
		}
	}

	// With the setting off, the saved canvas docks.
	prefs.Canvas = false
	c := &App{client: a.client}
	if !c.restore(l, nil) {
		t.Fatal("nothing restored with the canvas off")
	}
	if ct := c.tabs[0]; ct.canvas != nil || len(ct.Floats) != 0 || len(ct.panes()) != 2 {
		t.Errorf("restored off: canvas %v, %d floats, %d tiled", ct.canvas, len(ct.Floats), len(ct.panes()))
	}
	for _, p := range c.tabs[0].allPanes() {
		if p.term != nil {
			p.term.Close()
		}
	}

	// An old layout has no canvas.
	var old layout.Tab
	if err := json.Unmarshal([]byte(`{"id":"x","root":{"sid":"s1"},"focus":0,"floats":[{"pane":{"sid":"s2"},"x":0.1,"y":0.1,"w":0.5,"h":0.5}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Canvas != nil || old.Root == nil || len(old.Floats) != 1 {
		t.Errorf("old layout loaded as %+v", old)
	}
	if b, _ := json.Marshal(layout.Tab{Root: &layout.Node{SID: "s"}}); strings.Contains(string(b), "canvas") {
		t.Errorf("a tiled tab saves a canvas: %s", b)
	}
}

// TestCanvasWindowResize checks that a canvas pane keeps its size, and its
// terminal its columns and rows, as the window resizes.
func TestCanvasWindowResize(t *testing.T) {
	canvasPrefs(t, true)
	a, tt := newTestApp(t)
	tt.Frame()
	settle(tt)
	tab := a.tab()
	p := tab.Focus
	if tab.canvas == nil || p.float == nil || p.term == nil {
		t.Fatalf("no canvas pane: canvas %v", tab.canvas)
	}
	waitFor(t, tt, "the pane's stream", func() bool { return p.stream != nil && p.stream.ClientID() != "" })
	settle(tt)
	f := p.float
	box, cv := [4]float32{f.X, f.Y, f.W, f.H}, *tab.canvas
	if cv.W <= 0 || cv.W > 1000 || cv.H <= 0 || cv.H > 620 {
		t.Errorf("the canvas is of %+v, not of the room", cv)
	}
	cols, rows := p.term.Size()
	tt.SetSize(700, 500)
	settle(tt)
	tt.SetSize(1300, 800)
	settle(tt)
	if [4]float32{f.X, f.Y, f.W, f.H} != box || *tab.canvas != cv {
		t.Errorf("box %+v, canvas %+v; were %+v, %+v", *f, *tab.canvas, box, cv)
	}
	if c, r := p.term.Size(); c != cols || r != rows {
		t.Errorf("terminal %dx%d, was %dx%d", c, r, cols, rows)
	}
}

// TestCanvasDrags checks that a drag on the empty plane pans the canvas,
// and that a drag of a box's header moves it by the canvas's DIPs, which
// the zoom scales.
func TestCanvasDrags(t *testing.T) {
	canvasPrefs(t, true)
	a, tt := newTestApp(t)
	tt.Frame()
	settle(tt)
	tab := a.tab()
	f, cv, room := tab.Focus.float, tab.canvas, a.floatRoom
	if f == nil || cv == nil {
		t.Fatal("no canvas")
	}
	f.X, f.Y, f.W, f.H = 0.1, 0.1, 0.3, 0.3
	settle(tt)

	drag := func(x, y, dx, dy float32) {
		tt.Press(x, y)
		tt.Frame()
		for i := 1; i <= 4; i++ {
			tt.Move(x+dx*float32(i)/4, y+dy*float32(i)/4)
			tt.Frame()
		}
		tt.Release(x+dx, y+dy)
		settle(tt)
	}
	drag(room.X+room.W-30, room.Y+room.H-30, -100, -50)
	if !near(cv.X, 100) || !near(cv.Y, 50) {
		t.Errorf("a drag on the plane panned to %+v", *cv)
	}

	cv.X, cv.Y, cv.Scale = 0, 0, 2
	settle(tt)
	x0, y0 := f.X, f.Y
	// Into the header, at the zoom.
	drag(room.X+(f.X*cv.W+f.W*cv.W*0.3)*2, room.Y+(f.Y*cv.H+8)*2, 80, 40)
	if !near((f.X-x0)*cv.W, 40) || !near((f.Y-y0)*cv.H, 20) {
		t.Errorf("the header's drag moved the box by %v, %v DIPs of the canvas", (f.X-x0)*cv.W, (f.Y-y0)*cv.H)
	}
}

// canvasApp opens a canvas whose one box is at 0.1, 0.1 of it, 0.3 wide
// and high, and shows it from x, y at scale.
func canvasApp(t *testing.T, x, y, scale float32) (*App, *ui.Tester, *Tab) {
	t.Helper()
	canvasPrefs(t, true)
	a, tt := newTestApp(t)
	tt.Frame()
	settle(tt)
	tab := a.tab()
	if tab.canvas == nil || tab.Focus == nil || tab.Focus.float == nil {
		t.Fatal("no canvas")
	}
	f := tab.Focus.float
	f.X, f.Y, f.W, f.H = 0.1, 0.1, 0.3, 0.3
	tab.canvas.X, tab.canvas.Y, tab.canvas.Scale = x, y, scale
	settle(tt)
	return a, tt, tab
}

// A right click on the empty plane offers new panes, and puts the one
// chosen at the canvas's point clicked, of the focused box's size,
// past the room's edge too.
func TestCanvasMenuPlacesBox(t *testing.T) {
	a, tt, tab := canvasApp(t, 400, 50, 2)
	cv, room, old := tab.canvas, a.floatRoom, tab.Focus
	x, y := room.X+room.W-60, room.Y+room.H-60
	tt.RightClickAt(x, y)
	tt.Frame()
	if !tt.HasText("New Terminal") || !tt.HasText("New Browser") || !tt.HasText("New Note") {
		t.Fatalf("the plane's menu is not shown: %q", tt.Texts())
	}
	if err := tt.Click("New Note"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	p := tab.Focus
	if p == old || p == nil || p.note == nil || p.float == nil || !slices.Contains(tab.Floats, p.float) {
		t.Fatalf("the focus is on %+v, not a new note's box", p)
	}
	f := p.float
	if wx, wy := 400+(x-room.X)/2, 50+(y-room.Y)/2; !near(f.X*cv.W, wx) || !near(f.Y*cv.H, wy) {
		t.Errorf("the box is at %v, %v of the canvas, want %v, %v", f.X*cv.W, f.Y*cv.H, wx, wy)
	}
	if !near(f.W, old.float.W) || !near(f.H, old.float.H) {
		t.Errorf("the box is %v by %v, want the focused one's %v by %v", f.W, f.H, old.float.W, old.float.H)
	}
}

// Space pressed over the empty plane pans it with a drag anywhere, over a
// box too, until it is let go of; pressed over a box, it is the box's.
func TestCanvasSpacePan(t *testing.T) {
	a, tt, tab := canvasApp(t, 0, 0, 1)
	cv, room, f := tab.canvas, a.floatRoom, tab.Focus.float
	bx, by := room.X+room.W-30, room.Y+room.H-30
	px, py := room.X+(f.X+f.W/2)*cv.W, room.Y+(f.Y+f.H/2)*cv.H

	tt.Move(px, py)
	tt.Frame()
	tt.KeyDown(ui.KeySpace)
	settle(tt)
	tt.KeyUp(ui.KeySpace)
	settle(tt)
	if a.canvasPan != nil {
		t.Fatal("Space over a box pans the canvas")
	}

	tt.Move(bx, by)
	tt.Frame()
	tt.KeyDown(ui.KeySpace)
	settle(tt)
	if a.canvasPan != tab {
		t.Fatalf("Space over the plane does not pan it; texts %q", tt.Texts())
	}
	tt.Press(px, py)
	tt.Frame()
	for i := 1; i <= 4; i++ {
		tt.Move(px-25*float32(i), py-10*float32(i))
		tt.Frame()
	}
	tt.Release(px-100, py-40)
	settle(tt)
	if !near(cv.X, 100) || !near(cv.Y, 40) {
		t.Errorf("a drag over the box with Space held panned to %v, %v", cv.X, cv.Y)
	}
	if !near(f.X, 0.1) || !near(f.Y, 0.1) {
		t.Errorf("the box moved to %v, %v", f.X, f.Y)
	}
	tt.KeyUp(ui.KeySpace)
	settle(tt)
	if a.canvasPan != nil {
		t.Error("letting go of Space does not end the pan")
	}
}

// A pinch over the empty plane zooms it around the pointer.
func TestCanvasPinch(t *testing.T) {
	a, tt, tab := canvasApp(t, 0, 0, 1)
	cv, room := tab.canvas, a.floatRoom
	x, y := room.X+room.W-30, room.Y+room.H-30
	tt.Pinch(x, y, 1.5)
	settle(tt)
	if !near(cv.Scale, 1.5) {
		t.Fatalf("a pinch zoomed to %v", cv.Scale)
	}
	if px := cv.X + (x-room.X)/cv.Scale; !near(px, x-room.X) {
		t.Errorf("the point under the pointer moved to %v of the canvas", px)
	}
}

// A canvas's boxes draw no shadow; a float of tiled panes does.
func TestCanvasNoShadow(t *testing.T) {
	_, _, tab := canvasApp(t, 0, 0, 1)
	if !tab.Focus.float.free() {
		t.Fatal("a canvas's box is not free")
	}
	canvasPrefs(t, false)
	a, tt := newTestApp(t)
	tt.Frame()
	if p := a.newFloat(nil); p == nil || p.float.free() {
		t.Fatal("a float of tiled panes is free")
	}
}

// As a user does it: the pointer moves to a point of the empty plane, the
// secondary button goes down and up there, and an item of the menu is
// chosen; the new box's top left shows at that point of the room, at any
// pan and scale.
func TestCanvasMenuAtPointer(t *testing.T) {
	for _, view := range []struct{ x, y, scale float32 }{{0, 0, 1}, {-300, 120, 0.5}} {
		a, tt, tab := canvasApp(t, view.x, view.y, view.scale)
		cv, room := tab.canvas, a.floatRoom
		// Off the box, which is at 0.1 to 0.4 of the canvas.
		x, y := room.X+room.W*0.75, room.Y+room.H*0.7
		tt.Move(room.X+room.W*0.6, room.Y+room.H*0.5)
		tt.Frame()
		tt.Move(x, y)
		tt.Frame()
		tt.RightClickAt(x, y)
		tt.Frame()
		if err := tt.Click("New Note"); err != nil {
			t.Fatalf("at %+v: %v; texts %q", view, err, tt.Texts())
		}
		settle(tt)
		f := tab.Focus.float
		if tab.Focus.note == nil || f == nil {
			t.Fatalf("at %+v: no new note's box", view)
		}
		// Where the box's top left shows, in the window.
		sx := room.X + (f.X*cv.W-cv.X)*cv.Scale
		sy := room.Y + (f.Y*cv.H-cv.Y)*cv.Scale
		if !near(sx, x) || !near(sy, y) {
			t.Errorf("at %+v: the box's top left shows at %v, %v, want %v, %v", view, sx, sy, x, y)
		}
	}
}

// A URL typed into a browser pane's address field loads at Enter, tiled
// and on a canvas, at any scale.
func TestCanvasBrowserAddress(t *testing.T) {
	for _, v := range []struct {
		name   string
		canvas bool
		scale  float32
	}{{"tiled", false, 1}, {"canvas", true, 1}, {"canvas-half", true, 0.5}} {
		t.Run(v.name, func(t *testing.T) {
			canvasPrefs(t, v.canvas)
			a, tt := newTestApp(t)
			tt.Frame()
			settle(tt)
			tab := a.tab()
			if v.canvas {
				tab.canvas.Scale = v.scale
				a.canvasNew(tab, "browser", 20, 20)
			} else {
				a.invokeCommand(&cmdSplitBrowser, nil)
			}
			settle(tt)
			p := tab.Focus
			if p == nil || p.browser == nil {
				t.Fatalf("focus is not a browser pane: %+v", p)
			}
			// Leave the field, as a user who looked at the page does, and
			// click back into it.
			p.browser.auto = false
			a.focusReq = nil
			settle(tt)
			r, ok := tt.Find(locales.BrowserAddress())
			if !ok {
				t.Fatalf("no address field: %q", tt.Texts())
			}
			tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
			tt.Frame()
			if !tt.Focused(locales.BrowserAddress()) {
				t.Fatal("the address field has no focus after a click")
			}
			tt.Key(ui.Ctrl, ui.KeyA)
			tt.Type("http://localhost:5173/doc/drafts")
			tt.Key(0, ui.KeyEnter)
			settle(tt)
			if got := p.browser.url; got != "http://localhost:5173/doc/drafts" {
				t.Fatalf("url %q, addr %q after Enter", got, p.browser.addr)
			}
		})
	}
}
