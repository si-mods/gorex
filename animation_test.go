package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestPanesMove: with a style of animation, the panes spring to their
// places as a split, a zoom and a close change the layout, and a new pane
// pops in: the focused pane's buttons show away from where they rest; with Off, or while the desktop asks for less motion, the
// layout changes at once.
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
