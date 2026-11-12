package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestCloseTiledPaneFocus checks that a closed tiled pane's sibling takes
// its split, and the focus goes to the pane used before it; closing
// another pane keeps the focus.
func TestCloseTiledPaneFocus(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	first, second, third := dropTree(t, a)
	tab.setFocus(first)
	tab.setFocus(third)
	a.focusReq = nil
	a.closePane(third)
	if got, want := shape(tab.Root), fmt.Sprintf("[%d %d]", first.ID, second.ID); got != want {
		t.Fatalf("tree %s, want %s", got, want)
	}
	if tab.Focus != first || a.focusReq != first {
		t.Errorf("focus %v, asked %v, want the pane used before", tab.Focus, a.focusReq)
	}
	a.closePane(second)
	if tab.Root.Pane != first || first.Node != tab.Root || tab.Root.Parent != nil {
		t.Fatalf("tree %s", shape(tab.Root))
	}
	if tab.Focus != first || len(a.tabs) != 1 {
		t.Errorf("focus %v, %d tabs", tab.Focus, len(a.tabs))
	}
}

// TestCloseZoomedPane checks that closing another pane keeps the zoom, and
// closing the zoomed one ends it.
func TestCloseZoomedPane(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	first, second, third := dropTree(t, a)
	tab.setFocus(first)
	a.zoom(second, true)
	if tab.Zoom != second || tab.Focus != second {
		t.Fatalf("zoom %v, focus %v", tab.Zoom, tab.Focus)
	}
	a.closePane(third)
	if tab.Zoom != second {
		t.Errorf("closing another pane changed the zoom to %v", tab.Zoom)
	}
	a.closePane(second)
	if tab.Zoom != nil || tab.Focus != first {
		t.Errorf("zoom %v, focus %v", tab.Zoom, tab.Focus)
	}
}

// TestCloseFloatFocus checks that a closed float gives the focus to the
// pane used before it in sight, past a hidden float, and leaves the tree.
func TestCloseFloatFocus(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	first := tab.Focus
	second := a.splitPane(first, false, os.TempDir())
	hidden := a.newFloat(nil)
	f := a.newFloat(nil)
	if second == nil || hidden == nil || f == nil {
		t.Fatal("no panes")
	}
	tab.setFocus(first)
	tab.setFocus(hidden)
	a.hideFloat(hidden.float)
	tab.setFocus(f)
	a.focusReq = nil
	a.closePane(f)
	if tab.Focus != first || a.focusReq != first {
		t.Errorf("focus %v, asked %v, want the tiled pane used before", tab.Focus, a.focusReq)
	}
	if len(tab.Floats) != 1 || tab.Floats[0] != hidden.float || f.float != nil {
		t.Errorf("%d floats left", len(tab.Floats))
	}
	if got, want := shape(tab.Root), fmt.Sprintf("[%d %d]", first.ID, second.ID); got != want {
		t.Errorf("tree %s, want %s", got, want)
	}
}

// TestCloseLastTiledPromotesTopFloat checks that the topmost float, not
// the focused one, takes the tree of a tab whose last tiled pane closes,
// with the focus.
func TestCloseLastTiledPromotesTopFloat(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	root := tab.Focus
	low := a.newFloat(nil)
	top := a.newFloat(nil)
	if low == nil || top == nil || tab.Floats[1] != top.float {
		t.Fatal("no floats")
	}
	tab.setFocus(low)
	a.closePane(root)
	if tab.Root == nil || tab.Root.Pane != top || top.float != nil || tab.Focus != top {
		t.Fatalf("root %+v, focus %v", tab.Root, tab.Focus)
	}
	if len(tab.Floats) != 1 || tab.Floats[0] != low.float || len(a.tabs) != 1 {
		t.Errorf("%d floats, %d tabs", len(tab.Floats), len(a.tabs))
	}
}

// TestCloseLastCanvasBox checks that a canvas tab goes with its last box,
// and the tab before it shows with its focus asked for.
func TestCloseLastCanvasBox(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	room := ui.Rect{W: 1000, H: 600}
	first := a.tab()
	a.syncCanvas(first, room)
	second := a.newTab(os.TempDir())
	a.syncCanvas(second, room)
	if a.tab() != second || second.canvas == nil || len(second.Floats) != 1 || second.Root != nil {
		t.Fatalf("second tab %+v", second)
	}
	a.focusReq = nil
	a.closePane(second.Focus)
	if len(a.tabs) != 1 || a.tab() != first || a.focusReq != first.Focus || first.Focus == nil {
		t.Errorf("%d tabs, active %v, asked %v", len(a.tabs), a.tab(), a.focusReq)
	}
}

// canvasWithTiled makes a canvas tab of one box, with a pane moved into it
// as its tiled pane, the canvas yet to make it a box, which has the focus.
func canvasWithTiled(t *testing.T, a *App) (cv *Tab, box, tiled *Pane) {
	t.Helper()
	cv = a.tab()
	a.syncCanvas(cv, ui.Rect{W: 1000, H: 600})
	box = cv.Focus
	tiled = a.newTab(os.TempDir()).Focus
	a.movePaneToTab(tiled, cv)
	if len(a.tabs) != 1 || cv.Root == nil || cv.Root.Pane != tiled || len(cv.Floats) != 1 || cv.Focus != tiled {
		t.Fatalf("%d tabs, root %+v, %d floats, focus %v", len(a.tabs), cv.Root, len(cv.Floats), cv.Focus)
	}
	return cv, box, tiled
}

// TestCloseCanvasTiledPane checks that a canvas whose tiled pane closes
// keeps its boxes, the focus going to one.
func TestCloseCanvasTiledPane(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	cv, box, tiled := canvasWithTiled(t, a)
	a.closePane(tiled)
	if len(a.tabs) != 1 || cv.Root != nil || len(cv.Floats) != 1 || cv.Focus != box || a.focusReq != box {
		t.Errorf("%d tabs, root %+v, focus %v, asked %v", len(a.tabs), cv.Root, cv.Focus, a.focusReq)
	}
}

// TestClosePaneInBackgroundTab checks that closing a pane of a tab not
// shown repairs that tab's focus and asks the view for nothing, and its
// last pane takes the tab away without changing the one shown.
func TestClosePaneInBackgroundTab(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	back := a.newTab(os.TempDir())
	p := back.Focus
	q := a.splitPane(p, false, os.TempDir())
	back.setFocus(p)
	back.setFocus(q)
	a.selectTab(0)
	if a.tab() != first || q == nil {
		t.Fatal("the first tab does not show")
	}
	a.focusReq = nil
	a.closePane(q)
	if back.Focus != p || a.focusReq != nil || a.tab() != first {
		t.Errorf("focus %v, asked %v, active %v", back.Focus, a.focusReq, a.tab())
	}
	a.closePane(p)
	if len(a.tabs) != 1 || a.tab() != first {
		t.Errorf("%d tabs, active %v", len(a.tabs), a.tab())
	}
}

// TestMoveCanvasTiledPane checks that a canvas whose tiled pane moves to a
// tab of its own gives the focus to its box, as it does when the pane
// closes.
func TestMoveCanvasTiledPane(t *testing.T) {
	canvasPrefs(t, true)
	a, _ := newTestApp(t)
	cv, box, tiled := canvasWithTiled(t, a)
	if !a.movePaneToNewTab(tiled, "") {
		t.Fatal("the pane did not move")
	}
	if len(a.tabs) != 2 || cv.Root != nil || len(cv.Floats) != 1 || cv.Focus != box {
		t.Errorf("%d tabs, root %+v, focus %v", len(a.tabs), cv.Root, cv.Focus)
	}
}
