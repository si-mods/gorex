package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// shape writes a tree as pane IDs: [a b] for a left and right split, [a/b]
// for an above and below one.
func shape(n *Node) string {
	if n.Pane != nil {
		return fmt.Sprint(n.Pane.ID)
	}
	sep := " "
	if n.Vertical {
		sep = "/"
	}
	return "[" + shape(n.A) + sep + shape(n.B) + "]"
}

// dropTree makes the tab [1 [2/3]] of three panes and returns them.
func dropTree(t *testing.T, a *App) (*Pane, *Pane, *Pane) {
	t.Helper()
	first := a.tab().Focus
	second := a.splitPane(first, false, os.TempDir())
	third := a.splitPane(second, true, os.TempDir())
	if first == nil || second == nil || third == nil {
		t.Fatal("no panes")
	}
	if got, want := shape(a.tab().Root), fmt.Sprintf("[%d [%d/%d]]", first.ID, second.ID, third.ID); got != want {
		t.Fatalf("tree %s, want %s", got, want)
	}
	return first, second, third
}

func TestPaneDropZone(t *testing.T) {
	tests := []struct {
		nx, ny float32
		center bool
		want   dropZone
	}{
		{0.5, 0.5, true, dropCenter},
		{0.35, 0.65, true, dropCenter},
		{0.75, 0.5, true, dropRight},
		{0.5, 0.29, true, dropTop},
		{0.05, 0.5, true, dropLeft},
		{0.95, 0.5, true, dropRight},
		{0.5, 0.05, true, dropTop},
		{0.5, 0.95, true, dropBottom},
		// A card wider than tall has its diagonals nearer the corners.
		{0.1, 0.05, true, dropTop},
		{0.05, 0.1, true, dropLeft},
		{0.9, 0.05, true, dropTop},
		{0.95, 0.9, true, dropRight},
		{0.1, 0.95, true, dropBottom},
		// Without a center, the middle goes to the nearest edge.
		{0.45, 0.5, false, dropLeft},
		{0.5, 0.4, false, dropTop},
		{0.6, 0.5, false, dropRight},
	}
	for _, tc := range tests {
		if got := paneDropZone(tc.nx, tc.ny, tc.center); got != tc.want {
			t.Errorf("(%v, %v) center %v: zone %d, want %d", tc.nx, tc.ny, tc.center, got, tc.want)
		}
	}
}

func TestPaneZoneInZeroSize(t *testing.T) {
	if _, ok := paneZoneIn(0, 0, ui.Rect{}, true); ok {
		t.Error("a card with no size took the drop")
	}
	if z, ok := paneZoneIn(10, 50, ui.Rect{W: 100, H: 100}, true); !ok || z != dropLeft {
		t.Errorf("zone %d, %v", z, ok)
	}
}

func TestDropPaneEdges(t *testing.T) {
	for _, tc := range []struct {
		zone     dropZone
		vertical bool
		first    bool // the dragged pane is the split's first
	}{
		{dropLeft, false, true}, {dropRight, false, false}, {dropTop, true, true}, {dropBottom, true, false},
	} {
		a, _ := newTestApp(t)
		first, second, third := dropTree(t, a)
		if !a.dropPaneOn(first, third, tc.zone) {
			t.Fatalf("zone %d: nothing moved", tc.zone)
		}
		// The first pane left the root's split: the second and third
		// are in [2 / 3] still, now with the first beside the third.
		root := a.tab().Root
		if root.Pane != nil || root.A.Pane != second && root.B.Pane != second {
			t.Fatalf("zone %d: tree %s", tc.zone, shape(root))
		}
		n := third.Node.Parent
		if n == nil || n.Vertical != tc.vertical || n.Ratio != 0.5 {
			t.Fatalf("zone %d: split %+v", tc.zone, n)
		}
		if (n.A.Pane == first) != tc.first || (n.B.Pane == first) == tc.first {
			t.Errorf("zone %d: tree %s", tc.zone, shape(root))
		}
		if first.Node.Parent != n || third.Node.Parent != n || a.tab().Focus != first || a.focusReq != first {
			t.Errorf("zone %d: nodes or focus not kept", tc.zone)
		}
		if !a.saveDue {
			t.Errorf("zone %d: the layout was not saved", tc.zone)
		}
		if len(a.tab().panes()) != 3 {
			t.Errorf("zone %d: %d panes", tc.zone, len(a.tab().panes()))
		}
	}
}

func TestDropPaneCenterSwaps(t *testing.T) {
	a, _ := newTestApp(t)
	first, second, third := dropTree(t, a)
	if !a.dropPaneOn(first, third, dropCenter) {
		t.Fatal("nothing moved")
	}
	if got, want := shape(a.tab().Root), fmt.Sprintf("[%d [%d/%d]]", third.ID, second.ID, first.ID); got != want {
		t.Fatalf("tree %s, want %s", got, want)
	}
	if a.tab().Focus != first {
		t.Error("the dragged pane lost the focus")
	}
}

func TestDropPaneRefusals(t *testing.T) {
	check := func(name string, a *App, p, target *Pane, z dropZone) {
		t.Helper()
		before := shape(a.tab().Root)
		if a.dropPaneOn(p, target, z) {
			t.Errorf("%s: moved", name)
		}
		if got := shape(a.tab().Root); got != before {
			t.Errorf("%s: tree %s, was %s", name, got, before)
		}
	}
	a, _ := newTestApp(t)
	first, second, third := dropTree(t, a)
	check("itself", a, first, first, dropLeft)
	check("itself, center", a, first, first, dropCenter)
	check("nil", a, first, nil, dropLeft)

	third.host = &hostConn{}
	check("another host", a, first, third, dropLeft)
	third.host = nil

	a.floatPane(second)
	check("float target", a, first, second, dropLeft)
	check("float dragged", a, second, third, dropLeft)

	b, _ := newTestApp(t)
	first, _, third = dropTree(t, b)
	b.tab().Zoom = third
	check("zoomed tab", b, first, third, dropLeft)
}

func TestDropPaneOnOnlySibling(t *testing.T) {
	for _, tc := range []struct {
		zone     dropZone
		vertical bool
		first    bool
	}{
		{dropLeft, false, true}, {dropRight, false, false}, {dropTop, true, true}, {dropBottom, true, false},
	} {
		for _, dragFirst := range []bool{true, false} {
			a, _ := newTestApp(t)
			p := a.tab().Focus
			q := a.splitPane(p, false, os.TempDir())
			dragged, target := p, q
			if !dragFirst {
				dragged, target = q, p
			}
			if !a.dropPaneOn(dragged, target, tc.zone) {
				t.Fatalf("zone %d: nothing moved", tc.zone)
			}
			root := a.tab().Root
			if root.Pane != nil || root.A == nil || root.B == nil || root.Parent != nil {
				t.Fatalf("zone %d: tree %s", tc.zone, shape(root))
			}
			if root.Vertical != tc.vertical || root.Ratio != 0.5 {
				t.Errorf("zone %d: split %+v", tc.zone, root)
			}
			wantA, wantB := target, dragged
			if tc.first {
				wantA, wantB = dragged, target
			}
			if root.A.Pane != wantA || root.B.Pane != wantB {
				t.Errorf("zone %d (dragged first %v): tree %s", tc.zone, dragFirst, shape(root))
			}
			if dragged.Node != root.A && dragged.Node != root.B || target.Node != root.A && target.Node != root.B ||
				root.A.Parent != root || root.B.Parent != root {
				t.Errorf("zone %d: nodes do not point at each other", tc.zone)
			}
			if a.tab().Focus != dragged || len(a.tab().panes()) != 2 {
				t.Errorf("zone %d: focus or panes", tc.zone)
			}
		}
	}
}

func TestDropPaneFromOtherTab(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	target := first.Focus
	a.newTab(os.TempDir())
	second := a.tab()
	p := second.Focus
	if a.dropPaneOn(p, target, dropCenter) {
		t.Error("a swap across tabs")
	}
	if len(a.tabs) != 2 {
		t.Fatalf("%d tabs", len(a.tabs))
	}
	if !a.dropPaneOn(p, target, dropRight) {
		t.Fatal("nothing moved")
	}
	if len(a.tabs) != 1 || a.tabs[0] != first || p.Tab != first || len(first.panes()) != 2 {
		t.Fatalf("tabs %d, panes %d: the emptied tab stayed", len(a.tabs), len(first.panes()))
	}
	if first.Root.A.Pane != target || first.Root.B.Pane != p {
		t.Errorf("tree %s", shape(first.Root))
	}
}

// A press on a pane's header, a move over the left edge of another pane,
// and a release there put the pane on its left.
func TestDragPaneToLeftEdge(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	settle(tt)
	first, second, third := dropTree(t, a)
	settle(tt)
	if first.termSize.W <= 0 || third.termSize.W <= 0 {
		t.Fatalf("panes not laid out: %v %v", first.termSize, third.termSize)
	}
	// A pane's header is above its terminal, past the body's padding.
	hx, hy := first.termSize.X+first.termSize.W/2, first.termSize.Y-5-headerH/2
	b := third.termSize
	tx, ty := b.X+4, b.Y+b.H/2
	// No save while the test runs, which would clear saveDue: a slow
	// machine takes more than the second between saves.
	a.saveDue, a.lastSave = false, time.Now().Add(time.Hour)
	tt.Press(hx, hy)
	tt.Frame()
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		tt.Move(hx+(tx-hx)*f, hy+(ty-hy)*f)
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	tt.Release(tx, ty)
	tt.Frame()
	settle(tt)
	n := third.Node.Parent
	if n == nil || n.Vertical || n.A.Pane != first || n.B.Pane != third {
		t.Fatalf("tree %s, want %d left of %d", shape(a.tab().Root), first.ID, third.ID)
	}
	if !a.saveDue {
		t.Error("the layout was not saved")
	}
	if a.tab().Focus != first {
		t.Error("the dragged pane lost the focus")
	}
	_ = second
}

// A pane dragged onto the center of another swaps with it once: the drop
// waits for the pass to end, as a swap made while the pass builds the
// panes would show the target's card again, still holding the drop, and
// swap the panes back.
func TestDragPaneToCenterSwaps(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	settle(tt)
	first, _, third := dropTree(t, a)
	settle(tt)
	fn, tn := first.Node, third.Node
	// The dragged pane comes after its target in the tree, so that the
	// pass reaches the target's node again after the swap.
	hx, hy := third.termSize.X+third.termSize.W/2, third.termSize.Y-5-headerH/2
	b := first.termSize
	tx, ty := b.X+b.W/2, b.Y+b.H/2
	tt.Press(hx, hy)
	tt.Frame()
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		tt.Move(hx+(tx-hx)*f, hy+(ty-hy)*f)
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	tt.Release(tx, ty)
	tt.Frame()
	settle(tt)
	if first.Node != tn || third.Node != fn {
		t.Fatalf("tree %s, want %d and %d swapped", shape(a.tab().Root), first.ID, third.ID)
	}
}
