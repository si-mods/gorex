package main

import (
	"encoding/json"
	"math"
	"testing"

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
