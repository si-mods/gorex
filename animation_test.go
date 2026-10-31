package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestPanesMove: with a style of animation, the panes spring to their
// places as a split, a zoom and a close change the layout, and a new pane
// pops in: the focused pane's buttons show away from where they rest;
// with Off, or while the desktop asks for less motion, the layout changes
// at once.
func TestPanesMove(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range []struct {
		name   string
		style  string
		reduce bool
		moves  bool
	}{
		{"snappy", "", false, true},
		{"natural", "natural", false, true},
		{"off", "off", false, false},
		{"reduce motion", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{Animation: tc.style}
			tt.SetPreferences(ui.Preferences{ReduceMotion: tc.reduce})
			tt.Frame()
			settle(tt)
			// step changes the layout, and waits for the change to show
			// without the clock going on, as closing a pane waits for its
			// session to end.
			step := func(what string, change func(), done func() bool) {
				t.Helper()
				change()
				waitFor(t, tt, what, done)
				tt.Frame()
				// The focused pane's button, as the frame shows it, and
				// where it rests.
				shown, ok := tt.Find("Split Right")
				settle(tt)
				rest, _ := tt.Find("Split Right")
				if !ok {
					t.Fatalf("%s: no Split Right; texts %q", what, tt.Texts())
				}
				if moving := shown != rest; moving != tc.moves {
					t.Errorf("%s: moving %v (%v, at rest %v), want %v", what, moving, shown, rest, tc.moves)
				}
			}
			panes := func(n int) func() bool { return func() bool { return len(a.tab().panes()) == n } }
			zoomed := func(z bool) func() bool { return func() bool { return (a.tab().Zoom != nil) == z } }
			step("split", func() { a.split(true) }, panes(2))
			step("zoom", a.toggleZoom, zoomed(true))
			step("unzoom", a.toggleZoom, zoomed(false))
			// The pane above closes, and the focused one grows into its room.
			step("close", func() { a.closePane(a.tab().panes()[0]) }, panes(1))
		})
	}
}

// TestSidebarOff: with animations off, the sidebar hides at once.
func TestSidebarOff(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", Animation: "off"}
	tt.Frame()
	if _, ok := tt.Find("Tabs"); !ok {
		t.Fatal("no sidebar")
	}
	a.toggleSidebar()
	tt.Frame()
	if _, ok := tt.Find("Tabs"); ok {
		t.Error("the sidebar is still there with animations off")
	}
}

// settle runs the clock on until nothing moves.
func settle(tt *ui.Tester) {
	for range 20 {
		tt.Advance(100 * time.Millisecond)
		tt.Frame()
	}
}

// TestTerminalHoldsSizeWhileMoving: while a pane moves, or the sidebar
// changes the room it has, its terminal keeps the size it had at rest,
// and takes the new one once it rests; with animations off, it takes it
// at once.
func TestTerminalHoldsSizeWhileMoving(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range []struct{ name, style string }{
		{"snappy", ""},
		{"natural", "natural"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{TabPlacement: "left", Animation: tc.style}
			tt.Frame()
			settle(tt)
			a.split(false)
			waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
			settle(tt)
			p := a.tab().Focus
			if p == nil || p.term == nil {
				t.Fatal("no focused terminal")
			}
			// check changes the layout, and records each size the
			// terminal takes as the panes move and come to rest.
			check := func(what string, change func()) {
				t.Helper()
				c, r := p.term.Size()
				start := [2]int{c, r}
				var seen [][2]int
				record := func() {
					c, r := p.term.Size()
					if len(seen) == 0 || seen[len(seen)-1] != [2]int{c, r} {
						seen = append(seen, [2]int{c, r})
					}
				}
				change()
				for range 40 {
					tt.Advance(16 * time.Millisecond)
					tt.Frame()
					record()
				}
				for range 20 {
					tt.Advance(100 * time.Millisecond)
					tt.Frame()
					record()
				}
				c, r = p.term.Size()
				end := [2]int{c, r}
				if end == start {
					t.Fatalf("%s: the terminal did not resize (%v)", what, end)
				}
				if len(seen) > 2 {
					t.Errorf("%s: the terminal took %d sizes while moving: %v", what, len(seen), seen)
				}
				if seen[0] != start {
					t.Errorf("%s: it left %v at once, not when at rest", what, start)
				}
				if seen[len(seen)-1] != end {
					t.Errorf("%s: it rests at %v, not %v", what, end, seen[len(seen)-1])
				}
			}
			check("zoom", a.toggleZoom)
			check("unzoom", a.toggleZoom)
			check("sidebar", a.toggleSidebar)
		})
	}
	t.Run("off", func(t *testing.T) {
		a, tt := newTestApp(t)
		prefs = settings{TabPlacement: "left", Animation: "off"}
		tt.Frame()
		settle(tt)
		a.split(false)
		waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
		settle(tt)
		p := a.tab().Focus
		start, _ := p.term.Size()
		a.toggleZoom()
		tt.Frame()
		tt.Frame()
		atOnce, _ := p.term.Size()
		settle(tt)
		rest, _ := p.term.Size()
		if atOnce != rest || atOnce == start {
			t.Errorf("with animations off the terminal took %d columns at once, %d at rest, from %d", atOnce, rest, start)
		}
	})
}

// TestDividerDragResizesTerminals: dragging the divider between two panes
// moves them with the pointer, with no spring, and their terminals take
// each new size along the way rather than once the drag ends.
func TestDividerDragResizesTerminals(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{}
	tt.Frame()
	settle(tt)
	a.split(false)
	waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
	settle(tt)
	p := a.tab().Focus
	if p == nil || p.term == nil {
		t.Fatal("no focused terminal")
	}
	start, _ := p.term.Size()
	d, ok := tt.Find("Divider")
	if !ok {
		t.Fatalf("no divider; texts %q", tt.Texts())
	}
	x, y := d.X+d.W/2, d.Y+d.H/2
	tt.Press(x, y)
	tt.Frame()
	sizes := map[int]bool{}
	for i := 1; i <= 12; i++ {
		tt.Move(x-float32(i*15), y)
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		// The divider is where the pointer is, not on its way there.
		if now, _ := tt.Find("Divider"); now.X+now.W/2 > x-float32(i*15)+1 {
			t.Errorf("step %d: divider at %v behind the pointer at %v", i, now.X+now.W/2, x-float32(i*15))
		}
		c, _ := p.term.Size()
		sizes[c] = true
	}
	held, _ := p.term.Size()
	tt.Release(x-180, y)
	tt.Frame()
	if held <= start {
		t.Errorf("terminal %d columns while dragged, %d before: it waited for the drag to end", held, start)
	}
	if len(sizes) < 3 {
		t.Errorf("terminal took %d sizes over the drag, want it to follow each step", len(sizes))
	}
}
