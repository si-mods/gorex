package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/locales"
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

// TestDividerDragHoldsTerminals: dragging the divider between two panes
// moves them with the pointer, with no spring, while their terminals keep
// the size they had at rest: the program hears of one size, the drag's,
// once the pointer lets go, not of each width on the way.
func TestDividerDragHoldsTerminals(t *testing.T) {
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
	start, rows := p.term.Size()
	want := fmt.Sprintf("%dx%d", start, rows)
	waitSessionSize(t, tt, a.client, p.SID, want)
	d, ok := tt.Find("Divider")
	if !ok {
		t.Fatalf("no divider; texts %q", tt.Texts())
	}
	x, y := d.X+d.W/2, d.Y+d.H/2
	tt.Press(x, y)
	tt.Frame()
	for i := 1; i <= 12; i++ {
		tt.Move(x-float32(i*15), y)
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		// The divider is where the pointer is, not on its way there.
		now, ok := tt.Find("Divider")
		if !ok {
			t.Fatalf("step %d: no divider", i)
		}
		if at, want := now.X+now.W/2, x-float32(i*15); at-want > 1 || want-at > 1 {
			t.Errorf("step %d: divider at %v, the pointer at %v", i, at, want)
		}
		if c, r := p.term.Size(); c != start || r != rows {
			t.Fatalf("step %d: terminal %dx%d, want the %s it rested at", i, c, r, want)
		}
	}
	// Held, the drag outlasts a settle, which the terminal would have
	// asked for at each width on the way.
	time.Sleep(400 * time.Millisecond)
	tt.Frame()
	waitSessionSize(t, tt, a.client, p.SID, want)
	if c, r := p.term.Size(); c != start || r != rows {
		t.Fatalf("terminal %dx%d while the pointer stayed held, want the %s it rested at", c, r, want)
	}
	tt.Release(x-180, y)
	tt.Frame()
	waitFor(t, tt, "the terminal to take the drag's size", func() bool {
		c, r := p.term.Size()
		return c != start || r != rows
	})
	cols, lines := p.term.Size()
	if ac, ar := p.askedSize(); cols != ac || lines != ar {
		t.Errorf("terminal %dx%d after the drag, want the pane's %dx%d", cols, lines, ac, ar)
	}
	waitSessionSize(t, tt, a.client, p.SID, fmt.Sprintf("%dx%d", cols, lines))
}

// TestDividerDoubleClickAnimates: a double click on the divider puts it
// back in the middle with the panes' spring, as a press that does not move
// is no drag.
func TestDividerDoubleClickAnimates(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{}
	tt.Frame()
	settle(tt)
	a.split(false)
	waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
	a.tab().Root.Ratio = 0.3
	a.changed()
	tt.Frame()
	settle(tt)
	d, ok := tt.Find("Divider")
	if !ok {
		t.Fatalf("no divider; texts %q", tt.Texts())
	}
	x, y := d.X+d.W/2, d.Y+d.H/2
	for range 2 {
		tt.Press(x, y)
		tt.Frame()
		tt.Release(x, y)
		tt.Frame()
	}
	if r := a.tab().Root.Ratio; r != 0.5 {
		t.Fatalf("ratio %v after a double click, want 0.5", r)
	}
	tt.Advance(16 * time.Millisecond)
	tt.Frame()
	shown, _ := tt.Find("Divider")
	settle(tt)
	rest, _ := tt.Find("Divider")
	if shown.X == rest.X {
		t.Errorf("divider at %v at once, want it on its way to %v", shown.X, rest.X)
	}
}

// styles are the animation styles the motion tests run with: moving or
// not.
var styles = []struct {
	name, style string
	moves       bool
}{
	{"snappy", "", true},
	{"off", "off", false},
}

// TestFloatMorphs: a pane that floats or docks moves from its old box to
// its new one, as one element, its terminal keeping its size on the way;
// with Off it is there at once.
func TestFloatMorphs(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{Animation: tc.style}
			tt.Frame()
			a.split(false)
			waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
			settle(tt)
			p := a.tab().Focus
			// step floats or docks the pane, and checks where its button,
			// label, shows in the first frame against where it rests.
			step := func(what, label string, change func() bool) {
				t.Helper()
				c, r := p.term.Size()
				start := [2]int{c, r}
				if !change() {
					t.Fatalf("%s: nothing changed", what)
				}
				tt.Frame()
				shown, ok := tt.Find(label)
				if !ok {
					t.Fatalf("%s: no %q; texts %q", what, label, tt.Texts())
				}
				if tc.moves {
					for range 10 {
						tt.Advance(16 * time.Millisecond)
						tt.Frame()
						if c, r := p.term.Size(); [2]int{c, r} != start {
							t.Errorf("%s: the terminal took %dx%d on the way", what, c, r)
						}
					}
				}
				settle(tt)
				rest, _ := tt.Find(label)
				if moving := shown != rest; moving != tc.moves {
					t.Errorf("%s: moving %v (%v, at rest %v), want %v", what, moving, shown, rest, tc.moves)
				}
			}
			step("float", "Dock Pane", func() bool { return a.floatPane(p) })
			step("dock", "Float Pane", func() bool { return a.dockPane(p) })
		})
	}
}

// TestTabsOpenAndClose: with a style of animation a new tab unfolds along
// the tabs, and those after a closed one slide over, in the title bar and
// in the sidebar; with Off they are where they go at once.
func TestTabsOpenAndClose(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, place := range []string{"", "left"} {
		for _, tc := range styles {
			t.Run(place+tc.name, func(t *testing.T) {
				a, tt := newTestApp(t)
				prefs = settings{TabPlacement: place, Animation: tc.style}
				a.tab().Name = "first"
				tt.Frame()
				a.newTab(os.TempDir())
				a.tab().Name = "second"
				tt.Frame()
				opening, ok := tt.Find("second ")
				if !ok {
					t.Fatalf("no second tab; texts %q", tt.Texts())
				}
				settle(tt)
				open, _ := tt.Find("second ")
				folded := opening.W < open.W
				if place != "" {
					folded = opening.H < open.H
				}
				if folded != tc.moves {
					t.Errorf("opening %v, open %v: folded %v, want %v", opening, open, folded, tc.moves)
				}
				a.closeTab(a.tabs[0])
				waitFor(t, tt, "1 tab", func() bool { return len(a.tabs) == 1 })
				tt.Frame()
				sliding, _ := tt.Find("second ")
				settle(tt)
				rest, _ := tt.Find("second ")
				if moving := sliding != rest; moving != tc.moves {
					t.Errorf("after a close: moving %v (%v, at rest %v), want %v", moving, sliding, rest, tc.moves)
				}
			})
		}
	}
}

// TestPaletteComesAndGoes: with a style of animation the palette's rows
// come in as it opens, not as its query changes, and it fades out as it
// closes; with Off it shows and goes at once.
func TestPaletteComesAndGoes(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	// panel is what the palette's panel covers of the window, and its
	// pixels, as a shot shows them; the terminal's caret blinks outside.
	panel := func(tt *ui.Tester) []byte {
		img := tt.Image()
		var pix []byte
		for y := 90; y < 300; y++ {
			pix = append(pix, img.Pix[img.PixOffset(240, y):img.PixOffset(760, y)]...)
		}
		return pix
	}
	// row is a title of the palette that shows only there.
	row := func(t *testing.T, a *App, tt *ui.Tester) string {
		t.Helper()
		for _, it := range a.paletteItems() {
			if n := slices.Index(tt.Texts(), it.title); n >= 0 && slices.Index(tt.Texts()[n+1:], it.title) < 0 {
				return it.title
			}
		}
		t.Fatalf("no row of its own; texts %q", tt.Texts())
		return ""
	}
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{Animation: tc.style}
			p := a.tab().Focus
			waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
			// The terminal's caret blinks by the wall clock, not the
			// Tester's, and after a long prompt, as a CI machine's long
			// host name makes, it is in the region compared: hidden, it
			// leaves the palette alone to tell the shots apart.
			p.term.Feed([]byte("\x1b[?25l"))
			settle(tt)
			closed := panel(tt)
			a.openPalette()
			tt.Frame()
			title := row(t, a, tt)
			coming, _ := tt.Find(title)
			settle(tt)
			rest, _ := tt.Find(title)
			if moving := coming != rest; moving != tc.moves {
				t.Errorf("opening: moving %v (%v, at rest %v), want %v", moving, coming, rest, tc.moves)
			}
			// A query that changes the rows moves none.
			tt.Type("e")
			tt.Frame()
			title = row(t, a, tt)
			now, _ := tt.Find(title)
			settle(tt)
			if then, _ := tt.Find(title); now != then {
				t.Errorf("the query moved a row: %v, at rest %v", now, then)
			}
			// Escape closes it as the user does.
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if a.paletteOpen {
				t.Fatal("Escape did not close the palette")
			}
			going := !bytes.Equal(panel(tt), closed)
			settle(tt)
			if got := panel(tt); !bytes.Equal(got, closed) {
				n, x0, y0, x1, y1 := 0, 1<<30, 1<<30, -1, -1
				for i := 0; i < len(got); i += 4 {
					if !bytes.Equal(got[i:i+4], closed[i:i+4]) {
						x, y := 240+(i/4)%520, 90+(i/4)/520
						n, x0, y0, x1, y1 = n+1, min(x0, x), min(y0, y), max(x1, x), max(y1, y)
					}
				}
				t.Logf("%d pixels differ within %d,%d-%d,%d; the terminal shows %q", n, x0, y0, x1, y1, p.term.Text())
				t.Fatal("the window is not as before the palette opened")
			}
			if going != tc.moves {
				t.Errorf("closing: the palette still shows %v, want %v", going, tc.moves)
			}
		})
	}
}

// TestActivityDotBreathes: an activity dot's halo breathes, painting
// again, with a style of animation, and stands still with Off.
func TestActivityDotBreathes(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			prefs = settings{Animation: tc.style}
			tt := ui.NewTester(func(c *ui.Context) { activityDot(c, ui.RGBA(0, 120, 255, 1), 6) }, 40, 40)
			tt.Frame()
			before := slices.Clone(tt.Image().Pix)
			tt.Advance(600 * time.Millisecond)
			if pending := tt.FramePending(); pending != tc.moves {
				t.Errorf("frame pending %v, want %v", pending, tc.moves)
			}
			if changed := !bytes.Equal(before, tt.Image().Pix); changed != tc.moves {
				t.Errorf("the halo changed %v, want %v", changed, tc.moves)
			}
		})
	}
	t.Run("unfocused", func(t *testing.T) {
		prefs = settings{}
		breathing = false
		t.Cleanup(func() { breathing = true })
		tt := ui.NewTester(func(c *ui.Context) { activityDot(c, ui.RGBA(0, 120, 255, 1), 6) }, 40, 40)
		tt.Frame()
		before := slices.Clone(tt.Image().Pix)
		tt.Advance(600 * time.Millisecond)
		if tt.FramePending() || !bytes.Equal(before, tt.Image().Pix) {
			t.Error("the halo breathes out of the focus")
		}
	})
	if b := breath(time.Unix(0, 0)); b != 0 {
		t.Errorf("breath at the epoch %v, want 0", b)
	}
	if b := breath(time.Unix(0, int64(breathPeriod))); b != 1 {
		t.Errorf("breath a period on %v, want 1", b)
	}
}

// TestAttentionPops: a pane's dot of attention pops as the pane comes to
// ask for it, once, and not when it is built anew later; with Off it does
// not pop.
func TestAttentionPops(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			prefs = settings{Animation: tc.style}
			p := &Pane{ID: 7}
			show := true
			var dot ui.Rect
			tt := ui.NewTester(func(c *ui.Context) {
				k := colorsOf(c)
				ui.Row(c).Padding(10).Children(func() {
					if show && p.attention {
						attentionDot(c, k, p).Label("dot")
					}
				})
			}, 40, 40)
			tt.Frame()
			p.askAttention()
			tt.Frame()
			// Built anew on the way, as a tab's dot shown again, it shows
			// the pop where it is, not from its start.
			show = false
			tt.Frame()
			tt.Advance(140 * time.Millisecond)
			show = true
			tt.Frame()
			dot, _ = tt.Find("dot")
			if popped := dot.W > 10; popped != tc.moves {
				t.Errorf("popped %v (%v), want %v", popped, dot, tc.moves)
			}
			if pending := tt.FramePending(); pending != tc.moves {
				t.Errorf("popping, frame pending %v, want %v", pending, tc.moves)
			}
			tt.Advance(time.Second)
			tt.Frame()
			if dot, _ = tt.Find("dot"); dot.W != 7 {
				t.Errorf("at rest the dot is %v", dot)
			}
			// Hidden and shown again, it does not pop.
			show = false
			tt.Frame()
			show = true
			tt.Frame()
			tt.Advance(140 * time.Millisecond)
			tt.Frame()
			if dot, _ = tt.Find("dot"); dot.W != 7 {
				t.Errorf("built anew, the dot popped: %v", dot)
			}
		})
	}
}

// TestPaletteStaysPut: opening the palette again while it is open, as on
// the spaces, does not pose it anew; and the error banner showing over the
// window while it is open neither brings the palette in again nor takes
// its query field's focus.
func TestPaletteStaysPut(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	// The palette's rows, under its query field, whose caret blinks.
	panel := func() []byte {
		img := tt.Image()
		var pix []byte
		for y := 140; y < 300; y++ {
			pix = append(pix, img.Pix[img.PixOffset(240, y):img.PixOffset(760, y)]...)
		}
		return pix
	}
	a.openPalette()
	tt.Frame()
	settle(tt)
	rest := panel()
	a.openSessionPalette()
	a.openPalette()
	if a.paletteFresh {
		t.Error("opened again while open, the palette is to come in again")
	}
	for range 10 {
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		if !bytes.Equal(panel(), rest) {
			t.Fatal("opened again, the palette moved")
		}
	}
	before := panel()
	a.err = "boom"
	tt.Frame()
	if !tt.HasText("boom") {
		t.Fatalf("no banner; texts %q", tt.Texts())
	}
	if !bytes.Equal(panel(), before) {
		t.Error("the banner brought the palette in again")
	}
	tt.Type("x")
	tt.Frame()
	if a.paletteQuery != "x" {
		t.Errorf("the query is %q, not x: its field lost the focus", a.paletteQuery)
	}
}

// TestTabTrackFollows: in the title bar, the track's width follows its
// tabs on their spring: a tab after one that closes slides in whole, never
// cut by the track, and the track grows as a new tab unfolds rather than
// at once; with Off it takes its width at once.
func TestTabTrackFollows(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{Animation: tc.style}
			a.tab().Name = "one"
			a.newTab(os.TempDir()).Name = "two"
			a.newTab(os.TempDir()).Name = "three"
			tt.Frame()
			settle(tt)
			rest, ok := tt.Find("three ")
			if !ok {
				t.Fatalf("no third tab; texts %q", tt.Texts())
			}
			track, _ := tt.Find(locales.TabListLabel())
			a.closeTab(a.tabs[1])
			waitFor(t, tt, "2 tabs", func() bool { return len(a.tabs) == 2 })
			for range 30 {
				tt.Frame()
				if r, _ := tt.Find("three "); abs(r.W-rest.W) > 1 {
					t.Fatalf("closing, the next tab shows %v wide, %v at rest", r.W, rest.W)
				}
				tt.Advance(16 * time.Millisecond)
			}
			settle(tt)
			narrow, _ := tt.Find(locales.TabListLabel())
			if narrow.W >= track.W {
				t.Fatalf("the track is %v wide after a close, %v before", narrow.W, track.W)
			}
			a.newTab(os.TempDir()).Name = "four"
			tt.Frame()
			growing, _ := tt.Find(locales.TabListLabel())
			settle(tt)
			grown, _ := tt.Find(locales.TabListLabel())
			if moving := growing.W < grown.W; moving != tc.moves {
				t.Errorf("opening, the track is %v wide, %v at rest: growing %v, want %v", growing.W, grown.W, moving, tc.moves)
			}
		})
	}
}
