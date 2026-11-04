package main

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// TestFloatClamp checks that a float's box stays in the tab's room.
func TestFloatClamp(t *testing.T) {
	for _, f := range []Float{
		{},
		{X: -1, Y: -1, W: -1, H: -1},
		{X: 0.9, Y: 0.9, W: 0.5, H: 0.5},
		{X: 0, Y: 0, W: 2, H: 2},
		{X: 0.5, Y: 0.5, W: 0.01, H: 0.01},
		{X: float32(math.NaN()), Y: float32(math.Inf(1)), W: float32(math.NaN()), H: float32(math.Inf(-1))},
	} {
		f.clamp()
		if f.X < 0 || f.Y < 0 || f.W < floatMin || f.H < floatMin || f.W > 1 || f.H > 1 ||
			f.X+f.W > 1 || f.Y+f.H > 1 {
			t.Errorf("clamped %+v is outside the room", f)
		}
	}
	nan := Float{X: float32(math.NaN()), Y: float32(math.Inf(1)), W: float32(math.NaN()), H: float32(math.Inf(-1))}
	nan.clamp()
	if nan.X != 0.15 || nan.Y != 0.12 || nan.W != 0.7 || nan.H != 0.72 {
		t.Errorf("NaN and Inf clamped to %+v, not the defaults", nan)
	}
}

// TestFloatPaneDock floats a pane and docks it again.
func TestFloatPaneDock(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	if a.floatPane(tab.Focus) {
		t.Fatal("the only tiled pane floated")
	}
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	if len(tab.panes()) != 1 || len(tab.Floats) != 1 || p.float == nil {
		t.Fatalf("%d tiled, %d floats", len(tab.panes()), len(tab.Floats))
	}
	if tab.Root.Pane == nil || tab.Root.Pane == p {
		t.Fatalf("the tree kept %+v", tab.Root)
	}
	if tab.Focus != p {
		t.Error("the float does not have the focus")
	}
	if !a.dockPane(p) {
		t.Fatal("the float did not dock")
	}
	if len(tab.panes()) != 2 || len(tab.Floats) != 0 || p.float != nil {
		t.Fatalf("docked: %d tiled, %d floats", len(tab.panes()), len(tab.Floats))
	}
}

// TestFloatZoomKeepsTiledZoom checks that a zoomed tiled pane stays zoomed
// while floats open, move and close.
func TestFloatZoomKeepsTiledZoom(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	zoomed := tab.panes()[0]
	a.zoom(zoomed, true)
	if tab.Zoom != zoomed {
		t.Fatal("the pane did not zoom")
	}
	f1 := a.newFloat(nil)
	if f1 == nil || tab.Zoom != zoomed {
		t.Fatalf("a new float changed the zoom to %v", tab.Zoom)
	}
	other := tab.panes()[1]
	if !a.floatPane(other) {
		t.Fatal("the other pane did not float")
	}
	if tab.Zoom != zoomed {
		t.Error("floating a pane changed the zoom")
	}
	a.hideFloat(f1.float)
	if tab.Zoom != zoomed {
		t.Error("hiding a float changed the zoom")
	}
	a.dropPane(other, true)
	if tab.Zoom != zoomed {
		t.Error("closing a float changed the zoom")
	}
}

// TestZoomFocusedFloat checks that zooming a float fills its own room, and
// leaves the tab's tiled zoom alone.
func TestZoomFocusedFloat(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	if tab.Zoom != nil {
		t.Fatal("floating zoomed the tab")
	}
	if err := a.zoomFocused("toggle"); err != nil {
		t.Fatal(err)
	}
	if !p.float.Zoom || tab.Zoom != nil {
		t.Fatalf("float zoom %v, tab zoom %v", p.float.Zoom, tab.Zoom)
	}
	if err := a.zoomFocused("off"); err != nil {
		t.Fatal(err)
	}
	if p.float.Zoom {
		t.Error("the float stayed zoomed")
	}
}

// TestDropRootPromotesFloat checks that a tab whose only tiled pane closes
// keeps its topmost float as the new tree.
func TestDropRootPromotesFloat(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	root := tab.Root.Pane
	if root == nil {
		t.Fatalf("the tree is %+v", tab.Root)
	}
	a.closePane(root)
	if len(a.tabs) != 1 || a.tab() != tab {
		t.Fatalf("the tab closed: %d tabs", len(a.tabs))
	}
	if tab.Root == nil || tab.Root.Pane != p || len(tab.Floats) != 0 || p.float != nil {
		t.Fatalf("the float did not take the tree: root %+v, floats %d", tab.Root, len(tab.Floats))
	}
	if tab.Focus != p {
		t.Error("the promoted float does not have the focus")
	}
}

// TestHideFloatToggle checks that hiding a float keeps its session and
// moves the focus, and that the toggle brings the same float back.
func TestHideFloatToggle(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	a.hideFloat(p.float)
	if p.closed {
		t.Fatal("hiding closed the pane")
	}
	if !p.float.Hidden || len(tab.Floats) != 1 {
		t.Fatalf("hidden %v, floats %d", p.float.Hidden, len(tab.Floats))
	}
	if tab.Focus == nil || tab.Focus.float != nil {
		t.Fatalf("the focus stayed on the float: %v", tab.Focus)
	}
	a.toggleFloat()
	if p.float.Hidden || tab.Focus != p {
		t.Fatalf("the toggle did not bring the float back: hidden %v, focus %v", p.float.Hidden, tab.Focus)
	}
}

// TestFloatLayoutRoundTrip checks that the layout keeps floats' boxes, their
// hidden flag and z order, and the float with the focus, and that a restore
// brings them back.
func TestFloatLayoutRoundTrip(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p1 := tab.Focus
	if !a.floatPane(p1) {
		t.Fatal("the pane did not float")
	}
	p2 := a.newFloat(nil)
	if p2 == nil {
		t.Fatal("no new float")
	}
	a.setFloatBox(p1.float, 0.1, 0.2, 0.3, 0.4)
	a.setFloatBox(p2.float, 0.5, 0.1, 0.25, 0.3)
	a.hideFloat(p1.float)
	tab.setFocus(p2)

	raw, err := json.Marshal(a.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var l savedLayout
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	st := l.Tabs[0]
	if len(st.Floats) != 2 {
		t.Fatalf("%d floats saved", len(st.Floats))
	}
	if st.FocusFloat != 2 {
		t.Errorf("focus float %d, want 2", st.FocusFloat)
	}
	if !st.Floats[0].Hidden || st.Floats[1].Hidden {
		t.Errorf("hidden flags %v %v", st.Floats[0].Hidden, st.Floats[1].Hidden)
	}
	if f := st.Floats[0]; f.X != 0.1 || f.Y != 0.2 || f.W != 0.3 || f.H != 0.4 {
		t.Errorf("first box %+v", f)
	}

	for _, p := range tab.allPanes() {
		if p.term != nil {
			p.term.Close() // detach, as when the app quits
		}
	}
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	if len(b.tabs) != 1 {
		t.Fatalf("%d tabs restored", len(b.tabs))
	}
	bt := b.tabs[0]
	if len(bt.Floats) != 2 {
		t.Fatalf("%d floats restored", len(bt.Floats))
	}
	if !bt.Floats[0].Hidden || bt.Floats[1].Hidden {
		t.Errorf("restored hidden flags %v %v", bt.Floats[0].Hidden, bt.Floats[1].Hidden)
	}
	if f := bt.Floats[0]; f.X != 0.1 || f.Y != 0.2 || f.W != 0.3 || f.H != 0.4 {
		t.Errorf("restored first box %+v", f)
	}
	if bt.Focus != bt.Floats[1].Pane {
		t.Error("the visible float does not have the focus")
	}
	if bt.Floats[1].Pane.Tab != bt || bt.Floats[1].Pane.float != bt.Floats[1] {
		t.Error("a restored float pane is not its tab's float")
	}
}

// TestFloatDockButton checks that a float draws, and that its header's dock
// button puts it back among the tiled panes.
func TestFloatDockButton(t *testing.T) {
	a, tt := newTestApp(t)
	a.split(false)
	p := a.tab().Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	tt.Frame()
	if err := tt.Click(locales.CmdDockPane()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if len(a.tab().Floats) != 0 || len(a.tab().panes()) != 2 {
		t.Fatalf("the dock button left %d floats, %d tiled", len(a.tab().Floats), len(a.tab().panes()))
	}
}

// TestLayoutTabWithoutFloats checks that a tab of no floats marshals as it
// did before floats existed.
func TestLayoutTabWithoutFloats(t *testing.T) {
	got, err := json.Marshal(layout.Tab{Name: "x", Root: &layout.Node{SID: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"x","root":{"sid":"s"},"focus":0}`; string(got) != want {
		t.Errorf("layout %s, want %s", got, want)
	}
}

// A tab zoomed while a float has the focus keeps its zoom through the saved
// layout.
func TestFloatFocusKeepsSavedZoom(t *testing.T) {
	a, tab := floatZoomFixture(t)
	if tab.Focus.float == nil || tab.Zoom == nil {
		t.Fatal("fixture: want a zoomed tab and a focused float")
	}
	st := a.savedTab(tab)
	if !st.Zoom || st.FocusFloat == 0 {
		t.Fatalf("saved tab = zoom %v focusFloat %d, want zoom with a focused float", st.Zoom, st.FocusFloat)
	}
	if got := tab.panes()[st.Focus]; got != tab.Zoom {
		t.Fatalf("saved focus names %v, want the zoomed pane", got.ID)
	}
}

// Lowering the focused float sticks: drawing does not raise it again.
func TestLowerFocusedFloatSticks(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	f := a.newFloat(nil).float
	a.newFloat(nil)
	tab.setFocus(f.Pane)
	a.focusReq = f.Pane
	tt.Frame()
	a.lowerFloat(f)
	tt.Frame()
	tt.Frame()
	if tab.Floats[0] != f {
		t.Fatalf("the lowered float rose again")
	}
}

// floatZoomFixture is a tab of two tiled panes, the first zoomed, and a
// float with the focus.
func floatZoomFixture(t *testing.T) (*App, *Tab) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	a.zoom(tab.panes()[0], true)
	if a.newFloat(nil) == nil {
		t.Fatal("no float")
	}
	return a, tab
}

// Splitting a float splits the tab's tiled pane, and focuses the new tiled
// pane: the float's own node stays a leaf.
func TestSplitFloatTargetsTiled(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	if len(tab.panes()) != 1 || len(tab.Floats) != 1 {
		t.Fatalf("%d tiled, %d floats", len(tab.panes()), len(tab.Floats))
	}
	res := a.command(rex.LayoutCommand{Op: "pane.split", SID: p.SID, Dir: t.TempDir()})
	if res.Code != "" || res.New == "" {
		t.Fatalf("split %+v", res)
	}
	ps := tab.panes()
	if len(ps) != 2 || len(tab.Floats) != 1 || p.float == nil {
		t.Fatalf("split from a float: %d tiled, %d floats", len(ps), len(tab.Floats))
	}
	if p.Node == nil || p.Node.Pane != p || p.Node.A != nil || p.Node.B != nil {
		t.Fatalf("the float's node was split: %+v", p.Node)
	}
	if tab.Focus == nil || tab.Focus.float != nil || tab.Focus.SID != res.New {
		t.Fatalf("the split pane did not take the tiled focus: %v", tab.Focus)
	}
	for _, q := range ps {
		if q == p {
			t.Fatal("the float is in the tab's tree")
		}
	}
}

// Cycling floats visits every visible one without raising them, from the
// topmost down to the tiled pane.
func TestCycleFloatVisitsVisible(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	if !a.floatPane(tab.Focus) {
		t.Fatal("the pane did not float")
	}
	f1 := tab.Floats[0]
	if a.newFloat(nil) == nil {
		t.Fatal("no second float")
	}
	f2 := tab.Floats[1]
	if a.newFloat(nil) == nil {
		t.Fatal("no third float")
	}
	f3 := tab.Floats[2]
	a.hideFloat(f2)
	tiled := tab.panes()[0]
	tab.setFocus(tiled)

	a.cycleFloat()
	if tab.Focus != f3.Pane {
		t.Fatalf("from a tiled pane the focus went to %v, want the topmost float", tab.Focus)
	}
	a.cycleFloat()
	if tab.Focus != f1.Pane {
		t.Fatalf("the focus went to %v, want the next visible float below", tab.Focus)
	}
	if len(tab.Floats) != 3 || tab.Floats[0] != f1 || tab.Floats[1] != f2 || tab.Floats[2] != f3 {
		t.Fatalf("cycling reordered the floats: %+v", tab.Floats)
	}
	a.cycleFloat()
	if tab.Focus != tiled {
		t.Fatalf("from the lowest float the focus went to %v, want the tiled pane", tab.Focus)
	}
}

// A restore focuses the float named by its saved place, even when a float
// before it did not come, and promotes the topmost float of a tab whose
// tiled pane is missing.
func TestRestoreFocusesSavedFloat(t *testing.T) {
	a, _ := newTestApp(t)
	root := a.tab().Focus
	for _, p := range a.tab().allPanes() {
		if p.term != nil {
			p.term.Close() // detach, as when the app quits
		}
	}
	l := savedLayout{Tabs: []savedTab{
		{
			Name:  "x",
			Root:  &savedNode{SID: root.SID},
			Focus: 0,
			Floats: []layout.Float{
				{Pane: &savedNode{Host: "missing", SID: "gone"}, X: 0.1, Y: 0.1, W: 0.2, H: 0.2},
				{Pane: &savedNode{URL: "https://a.example/"}, X: 0.2, Y: 0.2, W: 0.2, H: 0.2},
				{Pane: &savedNode{URL: "https://b.example/"}, X: 0.3, Y: 0.3, W: 0.2, H: 0.2},
			},
			FocusFloat: 3,
		},
		{
			Name: "y",
			Floats: []layout.Float{
				{Pane: &savedNode{URL: "https://c.example/"}, X: 0.1, Y: 0.1, W: 0.2, H: 0.2},
				{Pane: &savedNode{URL: "https://d.example/"}, X: 0.2, Y: 0.2, W: 0.2, H: 0.2},
			},
		},
	}}
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	if len(b.tabs) != 2 {
		t.Fatalf("%d tabs restored", len(b.tabs))
	}
	x := b.tabs[0]
	if len(x.Floats) != 2 {
		t.Fatalf("%d floats of the tab with a tree", len(x.Floats))
	}
	if x.Focus != x.Floats[1].Pane {
		t.Errorf("the focus went to %v, want the float saved third", x.Focus)
	}
	y := b.tabs[1]
	if y.Root == nil || y.Root.Pane == nil || y.Root.Pane.browser == nil ||
		y.Root.Pane.browser.url != "https://d.example/" {
		t.Errorf("the tree did not take the topmost float: %+v", y.Root)
	}
	if len(y.Floats) != 1 || y.Floats[0].Pane.browser.url != "https://c.example/" {
		t.Errorf("floats after the promotion: %+v", y.Floats)
	}
}

// Focusing a hidden float brings it back and raises it, from the palette,
// a layout command's focus and an action naming its session.
func TestRevealFloatFocus(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	p.Name = "floater"
	a.hideFloat(p.float)

	var run func()
	for _, item := range a.paletteItems() {
		if item.title == "floater" {
			run = item.run
			break
		}
	}
	if run == nil {
		t.Fatal("the palette has no row for the float")
	}
	run()
	if p.float.Hidden || tab.Focus != p || tab.Floats[len(tab.Floats)-1] != p.float {
		t.Fatalf("the palette did not reveal and raise the float: hidden %v", p.float.Hidden)
	}

	a.hideFloat(p.float)
	if res := a.command(rex.LayoutCommand{Op: "pane.focus", SID: p.SID}); res.Code != "" {
		t.Fatalf("pane.focus: %+v", res)
	}
	if p.float.Hidden || tab.Focus != p {
		t.Fatalf("pane.focus did not reveal the float: hidden %v", p.float.Hidden)
	}

	a.hideFloat(p.float)
	if res := a.command(rex.LayoutCommand{Op: "action", Action: "pane.zoom", SID: p.SID}); res.Code != "" {
		t.Fatalf("action: %+v", res)
	}
	if p.float.Hidden || tab.Focus != p {
		t.Fatalf("the action did not reveal the float: hidden %v", p.float.Hidden)
	}
}

// A float's zoom is the float's own: pane.zoom reports and toggles it.
func TestZoomFloatCommand(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	res := a.command(rex.LayoutCommand{Op: "pane.zoom", SID: p.SID, Mode: "on"})
	if !res.Zoomed || !p.float.Zoom || tab.Zoom != nil {
		t.Fatalf("zoom on: %+v, float %v, tab %v", res, p.float.Zoom, tab.Zoom)
	}
	res = a.command(rex.LayoutCommand{Op: "pane.zoom", SID: p.SID, Mode: "off"})
	if res.Zoomed || p.float.Zoom {
		t.Fatalf("zoom off: %+v, float %v", res, p.float.Zoom)
	}
}

// Resizing a float past the right or bottom edge keeps X and Y, and only
// moving it keeps the size as it moves the box back in.
func TestFloatResizeKeepsCorner(t *testing.T) {
	a, _ := newTestApp(t)
	f := &Float{X: 0.2, Y: 0.3, W: 0.2, H: 0.2}
	a.resizeFloat(f, 0.3, -0.1)
	if f.X != 0.2 || f.Y != 0.3 || math.Abs(float64(f.W-0.5)) > 1e-6 || f.H != floatMin {
		t.Errorf("resize moved the corner or skipped the least size: %+v", f)
	}
	f = &Float{X: 0.85, Y: 0.85, W: 0.15, H: 0.15}
	a.resizeFloat(f, 0.4, 0.4)
	if f.X != 0.85 || f.Y != 0.85 || math.Abs(float64(f.W-0.15)) > 1e-6 || math.Abs(float64(f.H-0.15)) > 1e-6 ||
		f.X+f.W > 1.000001 || f.Y+f.H > 1.000001 {
		t.Errorf("resize past the edge: %+v", f)
	}
	f = &Float{X: 0.95, Y: 0.95, W: 0.1, H: 0.1}
	a.resizeFloat(f, 0, 0)
	if f.W != floatMin || f.H != floatMin || f.X != 1-floatMin || f.Y != 1-floatMin {
		t.Errorf("a least size that does not fit did not move the box: %+v", f)
	}
	f = &Float{X: 0.85, Y: 0.1, W: 0.15, H: 0.2}
	a.moveFloat(f, 0.3, 0)
	if f.W != 0.15 || f.X != 1-f.W {
		t.Errorf("moving changed the size: %+v", f)
	}
}

// pane.resize resizes a float as the focused one resizes: right and down
// grow it, left and up shrink it.
func TestResizeFloatLayoutCommand(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	a.setFloatBox(p.float, 0.3, 0.3, 0.3, 0.3)
	step := func(direction string) float32 {
		res := a.command(rex.LayoutCommand{Op: "pane.resize", SID: p.SID, Direction: direction, Amount: 0.05})
		if res.Code != "" || !res.Changed {
			t.Fatalf("resize %s: %+v", direction, res)
		}
		return p.float.W
	}
	if w := step("right"); math.Abs(float64(w-0.35)) > 1e-6 {
		t.Errorf("right: W %v, want 0.35", w)
	}
	if w := step("left"); math.Abs(float64(w-0.3)) > 1e-6 {
		t.Errorf("left: W %v, want 0.3", w)
	}
	res := a.command(rex.LayoutCommand{Op: "pane.resize", SID: p.SID, Direction: "down", Amount: 0.05})
	if !res.Changed || math.Abs(float64(p.float.H-0.35)) > 1e-6 {
		t.Errorf("down: %+v, H %v, want 0.35", res, p.float.H)
	}
	res = a.command(rex.LayoutCommand{Op: "pane.resize", SID: p.SID, Direction: "up", Amount: 0.05})
	if !res.Changed || math.Abs(float64(p.float.H-0.3)) > 1e-6 {
		t.Errorf("up: %+v, H %v, want 0.3", res, p.float.H)
	}
	if p.float.X != 0.3 || p.float.Y != 0.3 {
		t.Errorf("a resize moved the corner: %+v", p.float)
	}
}

// A float dragged past the room's edge keeps its box inside, shows a
// little of the overshoot, and lets it go when the drag ends.
func TestFloatDragOvershoot(t *testing.T) {
	a := &App{}
	f := &Float{X: 0.2, Y: 0.2, W: 0.5, H: 0.5, dragging: true}
	room := ui.Rect{W: 1000, H: 1000}
	a.moveFloatPx(f, 900, 0, room)
	if f.X != 0.5 {
		t.Fatalf("X = %v, want 0.5 at the edge", f.X)
	}
	if f.overX <= 0 {
		t.Fatalf("overX = %v, want the overshoot kept", f.overX)
	}
	if r := rubber(f.overX); r <= 0.1 || r >= f.overX {
		t.Fatalf("rubber(%v) = %v, want a damped share that still shows", f.overX, r)
	}
	a.moveFloatPx(f, -900, 0, room)
	if f.overX != 0 || f.X > 0.5 || f.X < 0 {
		t.Fatalf("back inside: X %v overX %v", f.X, f.overX)
	}
}

// Each edge resizes its side of a float's box and leaves the opposite one
// where it was, within the room and the least size.
func TestFloatResizeEdges(t *testing.T) {
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-5 }
	f := &Float{X: 0.2, Y: 0.2, W: 0.5, H: 0.5}
	f.resizeEdges(edgeLeft, -0.1, 0)
	if !near(f.X, 0.1) || !near(f.X+f.W, 0.7) {
		t.Fatalf("left: X %v right %v", f.X, f.X+f.W)
	}
	f.resizeEdges(edgeLeft, 0.9, 0)
	if !near(f.W, floatMin) || !near(f.X+f.W, 0.7) {
		t.Fatalf("left past the least: W %v right %v", f.W, f.X+f.W)
	}
	f.resizeEdges(edgeTop|edgeRight, 0.9, -0.9)
	if f.Y != 0 || !near(f.Y+f.H, 0.7) || !near(f.X+f.W, 1) {
		t.Fatalf("top right: %+v", *f)
	}
	f.resizeEdges(edgeBottom, 0, 0.9)
	if !near(f.Y+f.H, 1) {
		t.Fatalf("bottom: %+v", *f)
	}
}

// throwApp floats the focused pane of a new app, with the animation style
// given, at a box of a third of the room and at rest.
func throwApp(t *testing.T, style string, reduce bool) (*App, *ui.Tester, *Float) {
	t.Helper()
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{Animation: style}
	tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
	tt.Frame()
	a.split(false)
	waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
	p := a.tab().Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	f := p.float
	f.X, f.Y, f.W, f.H = 0.3, 0.3, 0.3, 0.3
	a.changed()
	settle(tt)
	if a.floatRoom.W <= 0 || a.floatRoom.H <= 0 {
		t.Fatalf("no room: %+v", a.floatRoom)
	}
	return a, tt, f
}

// throwBy drags f by its header dx, dy DIPs in n moves step apart, then
// pauses for pause and lets go, in the frame it lets go.
func throwBy(tt *ui.Tester, f *Float, dx, dy float32, n int, step, pause time.Duration) {
	b := f.Pane.bounds
	x, y := b.X+b.W*0.3, b.Y+8
	tt.Press(x, y)
	tt.Frame()
	for i := 1; i <= n; i++ {
		tt.Advance(step)
		tt.Move(x+dx*float32(i)/float32(n), y+dy*float32(i)/float32(n))
		tt.Frame()
	}
	tt.Advance(pause)
	tt.Release(x+dx, y+dy)
	tt.Frame()
}

// shownX is where the view shows f across the room, as a fraction of it.
func shownX(a *App, f *Float) float32 { return (f.Pane.bounds.X - a.floatRoom.X) / a.floatRoom.W }

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }

// A float let go of at rest rests where the pointer left it; thrown, it
// glides on from there, further, and comes to rest at its new box.
func TestFloatThrowGlides(t *testing.T) {
	a, tt, f := throwApp(t, "", false)
	room := a.floatRoom
	throwBy(tt, f, 60, 0, 6, 100*time.Millisecond, 200*time.Millisecond)
	slow := f.X - 0.3
	if want := 60 / room.W; !near(slow, want) {
		t.Fatalf("let go at rest, it moved %v of the room, want %v", slow, want)
	}
	settle(tt)
	f.X, f.Y = 0.3, 0.3
	a.changed()
	settle(tt)
	throwBy(tt, f, 60, 0, 6, 16*time.Millisecond, 0)
	fast := f.X - 0.3
	if fast <= slow+0.05 {
		t.Fatalf("thrown, it moved %v of the room, no further than %v let go at rest", fast, slow)
	}
	if !f.gliding {
		t.Fatal("thrown, it does not glide")
	}
	// It glides on from where the pointer left it.
	tt.Advance(16 * time.Millisecond)
	tt.Frame()
	if x := shownX(a, f); x <= 0.3+slow || x >= f.X {
		t.Fatalf("gliding, it shows at %v, not between %v and %v", x, 0.3+slow, f.X)
	}
	settle(tt)
	if f.gliding || !near(shownX(a, f), f.X) {
		t.Fatalf("at rest, it shows at %v (gliding %v), not at %v", shownX(a, f), f.gliding, f.X)
	}
}

// A float thrown fast to within snapDistance of the room's edges comes to
// rest against them, in their corner; one dropped slowly, thrown short of
// them, or let go of with animations off rests where it goes, in the room,
// however large it is.
func TestFloatThrowSnaps(t *testing.T) {
	a := &App{}
	room := ui.Rect{W: 1000, H: 800}
	// Project carries a throw about half a second's worth.
	for _, tc := range []struct {
		name         string
		x, y, w      float32
		vx, vy       float32
		still        bool
		wantX, wantY float32
	}{
		{"dropped near an edge", 0.66, 0.02, 0.3, 0, 0, false, 0.66, 0.02},
		{"dropped slowly near an edge", 0.66, 0.3, 0.3, -200, 0, false, 0.66 + ui.Project(0, -0.2), 0.3},
		{"short", 0.3, 0.3, 0.3, 400, -100, false, 0.3 + ui.Project(0, 0.4), 0.3 + ui.Project(0, -0.125)},
		{"near a corner", 0.3, 0.3, 0.3, 761.5, 609.2, false, 0.7, 0.7},
		{"near an edge", 0.3, 0.3, 0.3, -570, 0, false, 0, 0.3},
		{"far past a corner", 0.3, 0.3, 0.3, 8000, -8000, false, 0.7, 0},
		{"large, dropped", 0.04, 0.3, 0.9, 0, 0, false, 0.04, 0.3},
		{"large, thrown short", 0.04, 0.3, 0.9, 100, 0, false, 0.04 + ui.Project(0, 0.1), 0.3},
		{"off", 0.66, 0.02, 0.3, 5000, 5000, true, 0.66, 0.02},
	} {
		f := &Float{X: tc.x, Y: tc.y, W: tc.w, H: 0.3, dragging: true}
		a.throwFloat(f, tc.vx, tc.vy, room, tc.still)
		if !near(f.X, tc.wantX) || !near(f.Y, tc.wantY) {
			t.Errorf("%s: rests at %v, %v, want %v, %v", tc.name, f.X, f.Y, tc.wantX, tc.wantY)
		}
		if f.dragging || (f.fling == nil) != tc.still {
			t.Errorf("%s: let go as %+v", tc.name, *f)
		}
		if fl := f.fling; fl != nil && (fl.x != tc.x || fl.y != tc.y) {
			t.Errorf("%s: glides from %v, %v", tc.name, fl.x, fl.y)
		}
	}
	// Thrown toward a corner through the view, it comes to rest there.
	a, tt, f := throwApp(t, "", false)
	throwBy(tt, f, 120, 120, 6, 16*time.Millisecond, 0)
	settle(tt)
	if f.X != 0.7 || f.Y != 0.7 {
		t.Fatalf("thrown toward the bottom right, it rests at %v, %v", f.X, f.Y)
	}
	if b, r := f.Pane.bounds, a.floatRoom; !near(b.X+b.W, r.X+r.W) || !near(b.Y+b.H, r.Y+r.H) {
		t.Fatalf("in the corner it shows at %+v in %+v", b, r)
	}
}

// A click on a float's header that jitters a DIP does not move it.
func TestFloatClickDoesNotMove(t *testing.T) {
	_, tt, f := throwApp(t, "", false)
	b := f.Pane.bounds
	x, y := b.X+b.W*0.3, b.Y+8
	tt.Press(x, y)
	tt.Frame()
	tt.Advance(8 * time.Millisecond)
	tt.Move(x+1, y+1)
	tt.Frame()
	tt.Release(x+1, y+1)
	tt.Frame()
	if f.X != 0.3 || f.Y != 0.3 || f.gliding || f.dragging {
		t.Fatalf("clicked, it went to %v, %v (gliding %v)", f.X, f.Y, f.gliding)
	}
}

// A glide starts toward where the float rests, never away, and no faster
// than it settles there.
func TestGlideVelocity(t *testing.T) {
	for _, tc := range []struct{ v, x, to, want float32 }{
		{1, 0.2, 0.5, 1},
		{-1, 0.2, 0.5, 0},
		{1, 0.5, 0.5, 0},
		{50, 0.2, 0.5, 0.3 * 20},
		{-50, 0.5, 0.2, -0.3 * 20},
	} {
		if got := glideVelocity(tc.v, tc.x, tc.to, 20); !near(got, tc.want) {
			t.Errorf("glideVelocity(%v, %v, %v) = %v, want %v", tc.v, tc.x, tc.to, got, tc.want)
		}
	}
}

// Thrown hard into a corner, a float never shows more than glideOver past
// the room's edges on its way there.
func TestFloatThrowStaysNearRoom(t *testing.T) {
	for _, style := range []string{"", "natural"} {
		a, tt, f := throwApp(t, style, false)
		r := a.floatRoom
		b := f.Pane.bounds
		x, y := b.X+b.W*0.3, b.Y+8
		tt.Press(x, y)
		tt.Frame()
		for i := 1; i <= 4; i++ {
			tt.Advance(8 * time.Millisecond)
			tt.Move(x+float32(i)*40, y+float32(i)*40)
			tt.Frame()
		}
		tt.Release(x+160, y+160)
		tt.Frame()
		if !f.gliding {
			t.Fatalf("%q: thrown, it does not glide", style)
		}
		most := float32(0)
		for range 120 {
			tt.Advance(8 * time.Millisecond)
			tt.Frame()
			b := f.Pane.bounds
			most = max(most, b.X+b.W-(r.X+r.W), b.Y+b.H-(r.Y+r.H))
		}
		if most > glideOver {
			t.Errorf("%q: it showed %v DIPs past the room", style, most)
		}
		if f.gliding || f.X != 0.7 || f.Y != 0.7 {
			t.Errorf("%q: it rests at %v, %v (gliding %v)", style, f.X, f.Y, f.gliding)
		}
	}
}

// Dragged past the room's edge, a float shows past it, and let go, it
// springs back in.
func TestFloatRubberBandsBack(t *testing.T) {
	a, tt, f := throwApp(t, "", false)
	room := a.floatRoom
	b := f.Pane.bounds
	x, y := b.X+b.W*0.3, b.Y+8
	tt.Press(x, y)
	tt.Frame()
	for i := 1; i <= 10; i++ {
		tt.Advance(50 * time.Millisecond)
		tt.Move(x+room.W*0.08*float32(i), y)
		tt.Frame()
	}
	if f.X != 0.7 || f.overX <= 0 {
		t.Fatalf("dragged past the edge: X %v over %v", f.X, f.overX)
	}
	if past := shownX(a, f) - 0.7; past <= 0 || past >= f.overX {
		t.Fatalf("dragged %v past the edge, it shows %v past it", f.overX, past)
	}
	tt.Advance(200 * time.Millisecond)
	tt.Release(x+room.W*0.8, y)
	tt.Frame()
	tt.Advance(16 * time.Millisecond)
	tt.Frame()
	if x := shownX(a, f); x <= 0.7 || !f.gliding {
		t.Fatalf("let go, it shows at %v (gliding %v), not on its way back", x, f.gliding)
	}
	settle(tt)
	if f.X != 0.7 || f.overX != 0 || !near(shownX(a, f), 0.7) {
		t.Fatalf("back in, X %v over %v shows at %v", f.X, f.overX, shownX(a, f))
	}
}

// With animations off, or while the desktop asks for less motion, a thrown
// float rests at once where it was let go.
func TestFloatThrowOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		style  string
		reduce bool
	}{
		{"off", "off", false},
		{"reduce motion", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tt, f := throwApp(t, tc.style, tc.reduce)
			throwBy(tt, f, 60, 0, 6, 16*time.Millisecond, 0)
			if want := 0.3 + 60/a.floatRoom.W; !near(f.X, want) {
				t.Fatalf("thrown, it rests at %v, not where it was let go, %v", f.X, want)
			}
			tt.Frame()
			if f.gliding || !near(shownX(a, f), f.X) {
				t.Fatalf("it shows at %v (gliding %v), not at once at %v", shownX(a, f), f.gliding, f.X)
			}
		})
	}
}

// While a thrown float glides, its terminal keeps the size it rested at,
// and takes a new one once it rests.
func TestFloatTerminalHeldWhileGliding(t *testing.T) {
	a, tt, f := throwApp(t, "", false)
	p := f.Pane
	cols, rows := p.term.Size()
	throwBy(tt, f, 0, 40, 4, 16*time.Millisecond, 0)
	if !f.gliding {
		t.Fatal("thrown, it does not glide")
	}
	// Resized on the way, as by the keys, it keeps its size until it rests.
	a.resizeFloat(f, 0.1, 0)
	for range 5 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		if !f.gliding {
			break
		}
		if c, r := p.term.Size(); c != cols || r != rows {
			t.Fatalf("gliding, the terminal took %dx%d, not %dx%d", c, r, cols, rows)
		}
	}
	settle(tt)
	waitFor(t, tt, "the terminal to take the new size", func() bool {
		c, _ := p.term.Size()
		return c > cols
	})
}

// A press catches a gliding float where it shows.
func TestFloatCatch(t *testing.T) {
	a, tt, f := throwApp(t, "", false)
	throwBy(tt, f, 60, 0, 6, 16*time.Millisecond, 0)
	tt.Advance(32 * time.Millisecond)
	tt.Frame()
	shown := shownX(a, f)
	b := f.Pane.bounds
	tt.Press(b.X+b.W*0.3, b.Y+8)
	tt.Frame()
	if f.gliding || !near(f.X, shown) {
		t.Fatalf("caught at %v, it rests at %v (gliding %v)", shown, f.X, f.gliding)
	}
	tt.Release(b.X+b.W*0.3, b.Y+8)
	settle(tt)
	if !near(shownX(a, f), shown) {
		t.Fatalf("caught at %v, it shows at %v", shown, shownX(a, f))
	}
}

// A press on the terminal of a gliding float catches it where it shows.
func TestFloatCatchOnTerminal(t *testing.T) {
	a, tt, f := throwApp(t, "", false)
	throwBy(tt, f, 60, 0, 6, 16*time.Millisecond, 0)
	tt.Advance(32 * time.Millisecond)
	tt.Frame()
	shown := shownX(a, f)
	b := f.Pane.bounds
	tt.Press(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
	if f.gliding || !near(f.X, shown) {
		t.Fatalf("caught at %v, it rests at %v (gliding %v)", shown, f.X, f.gliding)
	}
	tt.Release(b.X+b.W/2, b.Y+b.H/2)
	settle(tt)
	if !near(shownX(a, f), shown) {
		t.Fatalf("caught at %v, it shows at %v", shown, shownX(a, f))
	}
}

// A float hidden, or left in another tab, as it glides is at rest when it
// shows again.
func TestFloatGlideEndsOutOfSight(t *testing.T) {
	a, tt, f := throwApp(t, "", false)
	throwBy(tt, f, 60, 0, 6, 16*time.Millisecond, 0)
	a.hideFloat(f)
	tt.Frame()
	if f.gliding || f.fling != nil {
		t.Fatal("hidden, it still glides")
	}
	a.showFloat(f)
	tt.Frame()
	if f.gliding || !near(shownX(a, f), f.X) {
		t.Fatalf("shown again, it shows at %v (gliding %v), not at %v", shownX(a, f), f.gliding, f.X)
	}
	settle(tt)
	f.X = 0.3
	a.changed()
	settle(tt)
	throwBy(tt, f, 60, 0, 6, 16*time.Millisecond, 0)
	if !f.gliding {
		t.Fatal("thrown, it does not glide")
	}
	home := a.active
	a.newTab("")
	waitFor(t, tt, "the new tab", func() bool { return a.active != home })
	tt.Frame()
	a.selectTab(home)
	tt.Frame()
	if f.gliding || f.fling != nil || !near(shownX(a, f), f.X) {
		t.Fatalf("back in its tab, it shows at %v (gliding %v), not at %v", shownX(a, f), f.gliding, f.X)
	}
}

// TestFloatState checks the action Rex's session.set_layer_state runs:
// minimizing hides a float, and bringing it back with the focus shows it
// above the tab's other floats and gives it and its tab the focus.
func TestFloatState(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	p0 := tab.Focus
	a.split(false)
	p := tab.Focus
	if !a.floatPane(p) {
		t.Fatal("the pane did not float")
	}
	if p.SID == "" {
		t.Fatal("the pane has no session ID")
	}
	cmd := lookupCommand("float.set_state")
	if cmd == nil {
		t.Fatal("float.set_state is not an action")
	}
	state := func(state string, focus bool) {
		t.Helper()
		if err := cmd.invoke(a, map[string]any{"sid": p.SID, "state": state, "focus": focus}); err != nil {
			t.Fatal(err)
		}
	}

	state("minimized", false)
	if !p.float.Hidden {
		t.Error("the float is not hidden")
	}
	if tab.Focus == p {
		t.Error("a hidden float keeps the focus")
	}

	// A second float, so that returning the first one can raise it.
	tab.setFocus(p0)
	a.split(false)
	q := tab.Focus
	if !a.floatPane(q) {
		t.Fatal("the second pane did not float")
	}
	if p.float.Hidden != true || q.float.Hidden {
		t.Fatalf("floats before returning: %+v %+v", p.float, q.float)
	}
	state("normal", true)
	if p.float.Hidden {
		t.Error("the float is still hidden")
	}
	if tab.Focus != p || a.focusReq != p {
		t.Error("the float did not take the focus")
	}
	if last := tab.Floats[len(tab.Floats)-1]; last != p.float {
		t.Error("the float was not raised above the others")
	}
}
