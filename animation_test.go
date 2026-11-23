package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
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
	oldSettle := sizeSettle
	sizeSettle = 0
	t.Cleanup(func() { sizeSettle = oldSettle })
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
			// rest runs frames, the clock standing still, until the
			// terminal keeps a size other than from, the stream's: the
			// view asks the session for the size it rests at from a
			// goroutine, which a loaded machine runs late. Each frame
			// calls each.
			rest := func(what string, from [2]int, each func()) {
				t.Helper()
				var last [2]int
				steady := 0
				waitFor(t, tt, what, func() bool {
					each()
					c, r := p.term.Size()
					ac, ar := p.askedSize()
					if now := [2]int{c, r}; now != last || c != ac || r != ar {
						last, steady = now, 0
					} else {
						steady++
					}
					return last != from && steady >= 10
				})
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
				rest(what+": the terminal to rest at a new size", start, record)
				c, r = p.term.Size()
				end := [2]int{c, r}
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
			c, r := p.term.Size()
			if !a.floatPane(p) {
				t.Fatal("the pane did not float")
			}
			settle(tt)
			rest("the floating terminal to rest", [2]int{c, r}, func() {})
			check("float zoom", a.toggleZoom)
			check("float unzoom", a.toggleZoom)
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
		// At once: with the clock standing still, as the size reaches the
		// stream from a goroutine.
		waitFor(t, tt, "the terminal to resize", func() bool {
			c, _ := p.term.Size()
			return c != start
		})
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
// once the pointer lets go, not of each width on the way, with or without
// animations.
func TestDividerDragHoldsTerminals(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	oldSettle := sizeSettle
	sizeSettle = 0
	t.Cleanup(func() { sizeSettle = oldSettle })
	for _, st := range styles {
		t.Run(st.name, func(t *testing.T) {
			prefs = settings{Animation: st.style}
			a, tt := newTestApp(t)
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
			// Held, the drag outlasts a settle, which the terminal would
			// have asked for at each width on the way.
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
		})
	}
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
				// settle runs the test's clock, not the wall clock the
				// session's resize settles by: the terminal takes the
				// size it rests at after that.
				c, r := waitTermSize(t, tt, p)
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

// TestWorkingDotsAnimate: workingDots pulses across frames with a style of
// animation, and stands still with Off or when unfocused.
func TestWorkingDotsAnimate(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			prefs = settings{Animation: tc.style}
			tt := ui.NewTester(func(c *ui.Context) { workingDots(c, ui.RGBA(120, 120, 128, 1)) }, 40, 40)
			tt.Frame()
			before := slices.Clone(tt.Image().Pix)
			tt.Advance(300 * time.Millisecond)
			if pending := tt.FramePending(); pending != tc.moves {
				t.Errorf("frame pending %v, want %v", pending, tc.moves)
			}
			if changed := !bytes.Equal(before, tt.Image().Pix); changed != tc.moves {
				t.Errorf("workingDots changed %v, want %v", changed, tc.moves)
			}
		})
	}
	t.Run("unfocused", func(t *testing.T) {
		prefs = settings{}
		breathing = false
		t.Cleanup(func() { breathing = true })
		tt := ui.NewTester(func(c *ui.Context) { workingDots(c, ui.RGBA(120, 120, 128, 1)) }, 40, 40)
		tt.Frame()
		before := slices.Clone(tt.Image().Pix)
		tt.Advance(300 * time.Millisecond)
		if tt.FramePending() || !bytes.Equal(before, tt.Image().Pix) {
			t.Error("workingDots animates out of the focus")
		}
	})
	if p := dotPhase(time.Unix(0, int64(300*time.Millisecond)), 0); math.Abs(float64(p-1)) > 0.001 {
		t.Errorf("dotPhase(300ms, 0) = %v, want 1", p)
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

// TestTabSwitchSlideAnimates checks that switching tabs animates both the
// active tab pill and the tab content in the direction of the switch, and
// snaps at once when Animation is Off.
func TestTabSwitchSlideAnimates(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{Animation: tc.style}
			a.tab().Name = "one"
			a.newTab(os.TempDir()).Name = "two"
			tt.Frame()
			settle(tt)

			a.selectTab(0)
			tt.Frame()
			if a.tabSlideDir != -1 {
				t.Fatalf("tabSlideDir after 1->0 = %v, want -1", a.tabSlideDir)
			}
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
			mid10 := a.tabPill.X
			settle(tt)
			rest0 := a.tabPill.X
			if moving := abs(mid10-rest0) > 1; moving != tc.moves {
				t.Errorf("1->0 pill X mid=%v rest=%v: moving=%v, want %v", mid10, rest0, moving, tc.moves)
			}

			a.selectTab(1)
			tt.Frame()
			if a.tabSlideDir != 1 {
				t.Fatalf("tabSlideDir after 0->1 = %v, want 1", a.tabSlideDir)
			}
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
			mid01 := a.tabPill.X
			settle(tt)
			rest1 := a.tabPill.X
			if moving := abs(mid01-rest1) > 1; moving != tc.moves {
				t.Errorf("0->1 pill X mid=%v rest=%v: moving=%v, want %v", mid01, rest1, moving, tc.moves)
			}
			if rest1 <= rest0 {
				t.Errorf("rest1=%v <= rest0=%v, want pill on tab 1 to the right of tab 0", rest1, rest0)
			}
		})
	}
}

// TestTileBounce: the front tile of a tab whose pane needs the user
// bounces across frames with a style of animation, and stands still with
// Off; a quiet tab's tiles keep still either way.
func TestTileBounce(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			prefs = settings{Animation: tc.style}
			a := &App{}
			p := &Pane{attention: true}
			tab := &Tab{Root: &Node{Pane: p}, Focus: p}
			tt := ui.NewTester(func(c *ui.Context) {
				k := colorsOf(c)
				a.tiles(c, k, tab)
			}, 40, 40)
			tt.Frame()
			before := slices.Clone(tt.Image().Pix)
			moved := false
			// Over more than a cycle the hops show, whenever they fall.
			for range 10 {
				tt.Advance(170 * time.Millisecond)
				tt.Frame()
				moved = moved || !bytes.Equal(before, tt.Image().Pix)
			}
			if moved != tc.moves {
				t.Errorf("the tile moved %v, want %v", moved, tc.moves)
			}
			if pending := tt.FramePending(); pending != tc.moves {
				t.Errorf("frame pending %v, want %v", pending, tc.moves)
			}
		})
	}
	t.Run("quiet", func(t *testing.T) {
		prefs = settings{}
		a := &App{}
		p := &Pane{}
		tab := &Tab{Root: &Node{Pane: p}, Focus: p}
		tt := ui.NewTester(func(c *ui.Context) {
			k := colorsOf(c)
			a.tiles(c, k, tab)
		}, 40, 40)
		tt.Frame()
		before := slices.Clone(tt.Image().Pix)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
		if tt.FramePending() {
			t.Error("a quiet tab's tiles keep the window painting")
		}
		if !bytes.Equal(before, tt.Image().Pix) {
			t.Error("a quiet tab's tile moved")
		}
	})
	// tileBounceAt: at the cycle's start and in the rest it ends with the
	// tile sits still; at the hop's top it is up, and a cycle on it is
	// where it was, every needy tab with it.
	base := time.Unix(0, 0)
	if dy, sx, sy := tileBounceAt(tileAttention, base); dy != 0 || sx != 1 || sy != 1 {
		t.Errorf("at the cycle's start the tile is %v,%v,%v, want at rest", dy, sx, sy)
	}
	if dy, _, _ := tileBounceAt(tileAttention, base.Add(160*time.Millisecond)); dy != -4.333 {
		t.Errorf("at the hop's top dy = %v, want -4.333", dy)
	}
	if dy, sx, sy := tileBounceAt(tileAttention, base.Add(time.Second)); dy != 0 || sx != 1 || sy != 1 {
		t.Errorf("at rest the tile is %v,%v,%v, want 0,1,1", dy, sx, sy)
	}
	cycle := tileBounces[tileAttention].cycle
	got1, _, _ := tileBounceAt(tileAttention, base.Add(80*time.Millisecond))
	got2, _, _ := tileBounceAt(tileAttention, base.Add(80*time.Millisecond+cycle))
	if got1 != got2 {
		t.Errorf("a cycle on dy = %v, was %v", got2, got1)
	}
	// Only blocked, error, and bell bounce; done shows a corner dot instead
	// and stays still.
	status := func(state string) *Tab {
		p := &Pane{info: rex.SessionInfo{Statuses: []rex.ProgramStatus{{State: state, Updated: time.Now()}}}}
		return &Tab{Root: &Node{Pane: p}, Focus: p}
	}
	for _, tc := range []struct {
		name string
		tab  *Tab
		want tileMotion
	}{
		{"bell", &Tab{Root: &Node{Pane: &Pane{attention: true}}}, tileAttention},
		{"blocked", status("blocked"), tileAttention},
		{"error", status("error"), tileAttention},
		{"done", status("done"), tileStill},
		{"working", status("working"), tileStill},
		{"idle", status("idle"), tileStill},
		{"none", status(""), tileStill},
	} {
		t.Run("motion/"+tc.name, func(t *testing.T) {
			if m := tc.tab.tileMotion(); m != tc.want {
				t.Errorf("tileMotion = %v, want %v", m, tc.want)
			}
		})
	}
	t.Run("done-dot", func(t *testing.T) {
		prefs = settings{}
		a := &App{}
		draw := func(tab *Tab) ([]byte, bool) {
			tt := ui.NewTester(func(c *ui.Context) {
				k := colorsOf(c)
				a.tiles(c, k, tab)
			}, 40, 40)
			tt.Frame()
			pix := slices.Clone(tt.Image().Pix)
			tt.Advance(300 * time.Millisecond)
			tt.Frame()
			return pix, tt.FramePending() || !bytes.Equal(pix, tt.Image().Pix)
		}
		quietPix, quietMoved := draw(status("idle"))
		donePix, doneMoved := draw(status("done"))
		if quietMoved {
			t.Error("quiet tab's tile moved")
		}
		if doneMoved {
			t.Error("done tab's tile bounced; want still with corner dot")
		}
		if bytes.Equal(quietPix, donePix) {
			t.Error("done tab's tile looks identical to idle; want blue corner dot")
		}
	})
}

// TestOverlaysAnimate: a popover grows from its anchor and a dialog comes
// in as the palette does, and both go out with an exit of their own; with
// Off, or while the desktop asks for less motion, they show and go at
// once.
func TestOverlaysAnimate(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range []struct {
		name, style string
		reduce      bool
		moves       bool
	}{
		{"snappy", "", false, true},
		{"natural", "natural", false, true},
		{"off", "off", false, false},
		{"reduce motion", "", true, false},
	} {
		for _, kind := range []string{"popover", "dialog"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				prefs = settings{Animation: tc.style}
				open := false
				tt := ui.NewTester(func(c *ui.Context) {
					anchor := ui.Box(c.Key("anchor")).Size(80, 30).Margin(20).Background(ui.Hex("#888888"))
					body := func(e ui.Element) {
						e.Width(200).Height(120).Background(ui.Hex("#d02020"))
					}
					if kind == "popover" {
						popoverBase(c, anchor, &open, func(panel ui.Element, entering bool) {
							body(panel)
						})
						return
					}
					ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
						dialogEnter(c, backdrop, panel)
						backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
						body(panel)
					})
				}, 400, 300)
				tt.SetPreferences(ui.Preferences{ReduceMotion: tc.reduce})
				tt.Frame()
				shot := func() []byte { return slices.Clone(tt.Image().Pix) }
				closed := shot()
				open = true
				tt.Frame()
				tt.Advance(30 * time.Millisecond)
				tt.Frame()
				mid := shot()
				settle(tt)
				shown := shot()
				if bytes.Equal(shown, closed) {
					t.Fatal("the overlay does not show")
				}
				if got := !bytes.Equal(mid, shown); got != tc.moves {
					t.Errorf("opening: moving %v, want %v", got, tc.moves)
				}
				// Closed, it is on its way out for a moment, as the palette is.
				open = false
				tt.Frame()
				tt.Advance(30 * time.Millisecond)
				tt.Frame()
				if got := !bytes.Equal(shot(), closed); got != tc.moves {
					t.Errorf("closing: still shows %v, want %v", got, tc.moves)
				}
				// The exit is shorter than the entry's 150 ms.
				tt.Advance(durExit)
				tt.Frame()
				if !bytes.Equal(shot(), closed) {
					t.Error("the overlay was not gone after its exit")
				}
				// Opened again, it comes in again.
				open = true
				tt.Frame()
				tt.Advance(30 * time.Millisecond)
				tt.Frame()
				if got := !bytes.Equal(shot(), shown); got != tc.moves {
					t.Errorf("opening again: moving %v, want %v", got, tc.moves)
				}
			})
		}
	}
}

// TestPopoverOpensAbove: a popover that opens above its anchor, in a short
// window at the sidebar's foot, grows from the anchor: on no frame of its
// opening are its rows above where they rest, or outside the window, as
// they were when a Transition of the panel laid it out again and slid it
// down from above the window's top.
func TestPopoverOpensAbove(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left"}
	a.hello.Host.Name = "Testbook"
	tt.Frame()
	// Opened and closed before, as in a window used a while, and then
	// shortened while the copy of the panel is still leaving.
	a.hostOpen = true
	tt.Frame()
	a.hostOpen = false
	tt.Frame()
	tt.SetSize(900, 300)
	tt.Frame()
	a.hostOpen = true
	var rows [][2]float32
	for range 12 {
		tt.Frame()
		top, ok1 := tt.Find("Testbook")
		server, ok2 := tt.Find(locales.HostServer())
		if !ok1 || !ok2 {
			t.Fatalf("the panel does not show; texts %q", tt.Texts())
		}
		rows = append(rows, [2]float32{top.Y, server.Y})
		tt.Advance(20 * time.Millisecond)
	}
	settle(tt)
	top, _ := tt.Find("Testbook")
	server, _ := tt.Find(locales.HostServer())
	for i, r := range rows {
		if r[0] < 0 || r[0] < top.Y-0.5 || r[1] < server.Y-0.5 {
			t.Errorf("frame %d: the rows are at %v, above where they rest (%v, %v) or out of the window", i, r, top.Y, server.Y)
		}
	}
}

// TestPopoverKeyStaysOwn checks that popoverBase's key goes to its panel
// alone: closed, it builds none, and the element after it keeps its state
// as the popover opens and closes.
func TestPopoverKeyStaysOwn(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	open, fresh := false, 0
	tt := ui.NewTester(func(c *ui.Context) {
		anchor := ui.Box(c.Key("anchor")).Size(80, 30).Margin(20).Background(ui.Hex("#888888"))
		popoverBase(c, anchor, &open, func(panel ui.Element, entering bool) { panel.Width(200).Height(120) })
		next := ui.Box(c).Size(40, 40)
		ui.Local(next, "probe", func() bool { fresh++; return true })
	}, 400, 300)
	for _, o := range []bool{false, true, false, true, false} {
		open = o
		settle(tt)
	}
	if fresh != 1 {
		t.Errorf("the element after the popover was built anew %d times as it opened and closed, want once", fresh)
	}
}

// TestPopoverGatedReopenAnimates checks a popover whose caller skips
// popoverBase while it is closed, as closeConfirm does: with popoverIdle in
// those frames, its next opening grows again.
func TestPopoverGatedReopenAnimates(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	open := false
	tt := ui.NewTester(func(c *ui.Context) {
		anchor := ui.Box(c.Key("anchor")).Size(80, 30).Margin(20).Background(ui.Hex("#888888"))
		if !open {
			popoverIdle(c, anchor)
			return
		}
		popoverBase(c, anchor, &open, func(panel ui.Element, entering bool) {
			panel.Width(200).Height(120).Background(ui.Hex("#d02020"))
		})
	}, 400, 300)
	shot := func() []byte { return slices.Clone(tt.Image().Pix) }
	for i := range 2 {
		open = true
		tt.Frame()
		tt.Advance(30 * time.Millisecond)
		tt.Frame()
		mid := shot()
		settle(tt)
		if bytes.Equal(mid, shot()) {
			t.Errorf("opening %d: the panel is at rest 30 ms in, want it growing", i+1)
		}
		open = false
		settle(tt)
	}
}

// TestMenuRevealStaggers: menuReveal's parts come in a step apart as
// their menu opens: a late row waits while an early one is on the way
// already, and all rest together; with Off none waits.
func TestMenuRevealStaggers(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			prefs = settings{Animation: tc.style}
			open, hit := false, false
			tt := ui.NewTester(func(c *ui.Context) {
				anchor := ui.Box(c.Key("anchor")).Size(80, 30).Margin(20).Background(ui.Hex("#888888"))
				popoverBase(c, anchor, &open, func(panel ui.Element, entering bool) {
					ui.Column(c).Children(func() {
						menuReveal(c, ui.Box(c.Key("r0")).Size(60, 20).Background(ui.Hex("#ff0000")).Label("r0"), entering, 0)
						b3 := ui.Box(c.Key("r3")).Size(60, 20).Background(ui.Hex("#0000ff")).Label("r3").Cursor(ui.CursorPointer)
						if b3.Clicked() {
							hit = true
						}
						menuReveal(c, b3, entering, 3)
					})
				})
			}, 400, 300)
			at := func(r ui.Rect) bool {
				// The pixel at the box's heart differs from the bare
				// backdrop past its right edge once the box shows at all.
				img := tt.Image()
				cx, cy := int(r.X+r.W/2), int(r.Y+r.H/2)
				return img.RGBAAt(cx, cy) != img.RGBAAt(int(r.X+r.W)+8, cy)
			}
			open = true
			settle(tt)
			// Reopen, and watch the entrance: row 3 fades in steps behind
			// row 0, which the glass's opening leads.
			open = false
			settle(tt)
			hit = false
			open = true
			tt.Frame()
			tt.Advance(menuRows + menuStep)
			tt.Frame()
			r0, _ := tt.Find("r0")
			r3, _ := tt.Find("r3")
			if tc.moves {
				if at(r3) {
					t.Errorf("r3 shows before its step at %v", tt.Now())
				}
				if !at(r0) {
					t.Errorf("r0 still dark at %v", tt.Now())
				}
				// A part still dim takes no click.
				tt.ClickAt(r3.X+r3.W/2, r3.Y+r3.H/2)
				tt.Frame()
				if hit {
					t.Errorf("a dim row answered the click")
				}
			} else if !at(r3) {
				t.Errorf("with animations off r3 is not there at once")
			}
			settle(tt)
			if !at(r3) {
				t.Errorf("r3 never came in")
			}
			tt.ClickAt(r3.X+r3.W/2, r3.Y+r3.H/2)
			tt.Frame()
			if !hit {
				t.Errorf("a revealed row did not answer the click")
			}
		})
	}
}

// TestSpaceMenuStaggers: the space picker's menu reveals its parts once
// its glass is mostly open: the last is still dark where it stands on the
// opening frames, home at rest; with Off it is there at once.
func TestSpaceMenuStaggers(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	newRow := locales.SpaceNew()
	for _, tc := range styles {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTestApp(t)
			prefs = settings{Animation: tc.style}
			tt.Frame()
			settle(tt)
			a.spaces.open = true
			tt.Frame()
			tt.Advance(menuLead + 40*time.Millisecond)
			tt.Frame()
			inked := func() int {
				r, ok := tt.Find(newRow)
				if !ok {
					return -1
				}
				img := tt.Image()
				bg := img.RGBAAt(int(r.X)+2, int(r.Y+r.H/2))
				n := 0
				for x := int(r.X) + 10; x < int(r.X+r.W)-4; x++ {
					for y := int(r.Y) + 2; y < int(r.Y+r.H)-2; y++ {
						if img.RGBAAt(x, y) != bg {
							n++
						}
					}
				}
				return n
			}
			mid := inked()
			settle(tt)
			rest := inked()
			if tc.moves {
				if mid != 0 {
					t.Errorf("the last row shows %d px before its step", mid)
				}
				if rest < 20 {
					t.Errorf("at rest the last row shows %d px", rest)
				}
			} else if rest < 20 {
				t.Errorf("with animations off the last row shows %d px", rest)
			}
		})
	}
}
