package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// animations are the values of the Animation style setting, in the order
// of its control: Snappy, Natural, Off.
var animations = []string{"", "natural", "off"}

// The springs of the animation styles: Snappy's quick with a little
// bounce, Natural's slower and settling without one.
var (
	snappy  = ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond, Bounce: 0.12}}
	natural = ui.Animation{Spring: ui.SpringOptions{Duration: 500 * time.Millisecond}}
	// springBack is how a float let go past the room's edge comes back,
	// with a bounce one feels.
	springBack = ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond, Bounce: 0.3}}
)

// The timings of what the springs do not move: a layer that fades in or
// out, a color that follows the pointer. An entry eases out, an exit eases
// in and is shorter than its entry; DESIGN.md, Motion, says which to use.
const (
	// durEnter is how long a popover or a dialog takes to come in.
	durEnter = 150 * time.Millisecond
	// durExit is how long a popover, a dialog or the palette takes to go.
	durExit = 120 * time.Millisecond
	// durExitMove is how long what a spring brought in takes to go: a pane,
	// a tab, a row, the banner, the tab being left.
	durExitMove = 180 * time.Millisecond
	// durHover is how long a color takes to follow the pointer.
	durHover = 120 * time.Millisecond
)

// exitIn makes the Exit of t ease in over d, in place of its entry's
// spring or curve.
func exitIn(t ui.ElementTransition, d time.Duration) ui.ElementTransition {
	t.ExitDuration, t.ExitEase = d, ui.EaseIn
	return t
}

// hoverFade is the transition of a color that follows the pointer.
var hoverFade = ui.ElementTransition{Colors: true, Duration: durHover, Ease: ui.EaseOut}

// popFrom is where a popover goes as it closes, and popPose where it grows
// from as it opens: a little smaller, and faded.
var (
	popFrom = &ui.Motion{Scale: 0.96, Opacity: 0}
	popPose = ui.Pose{Scale: 0.96, Fade: 1}
)

// popoverRec is what popoverBase keeps of a popover from frame to frame:
// whether it was open, whether its panel has been asked to rest, and the
// key of its panel.
type popoverRec struct {
	open, entered bool
	key           string
}

// menuStep is how far a menu's parts come in after one another while its
// panel opens: the iOS picker's panel staggers its rows the same way
// (docs/MOD-002-projects.md).
const menuStep = 30 * time.Millisecond

// menuReveal is the entrance of a menu's part, the i'th of them: the
// frame the panel opens it shows nothing, and on the way to its place it
// fades up a step after the part before it, so the menu comes in as its
// parts arriving rather than the one block the panel's own pose would
// show. Nothing while the desktop asks for less motion, nor past the
// opening frames: e then rests where the layout puts it.
func menuReveal(c *ui.Context, e ui.Element, entering bool, i int) ui.Element {
	if motion(c) == nil {
		return e
	}
	pose := ui.Pose{}
	if entering {
		pose = ui.Pose{Fade: 1, Y: -4}
	}
	return e.AnimatePose(pose, ui.Animation{Duration: durEnter, Ease: ui.EaseOut, Delay: time.Duration(i) * menuStep})
}

// popoverBase is ui.PopoverBase with motion: the panel grows from the
// corner of its anchor that it meets, and shrinks back to it as it closes,
// nothing while the desktop asks for less motion. It grows with a pose,
// which does not touch the layout: an Enter of a Transition lays the
// panel out at each size on the way, and its rows squeeze. It goes with
// the Exit of a Transition, which keeps a copy of it leaving.
//
// A panel opened while the copy of one it closed still leaves comes back
// from where the copy shows (MyGo's Transition), which is no place for it
// where the window or the anchor moved meanwhile: its rows would slide in
// from there. Each opening has a key of the window's size and the anchor's
// place, so that it is a new panel then. Call it in every frame, open or
// not; a caller that skips it while closed calls popoverIdle instead.
// fn gets entering, the panel's first frame, for the parts it builds to
// come in one after another (menuReveal).
func popoverBase(c *ui.Context, anchor ui.Element, open *bool, fn func(panel ui.Element, entering bool)) {
	if motion(c) == nil {
		ui.PopoverBase(c, anchor, open, func(panel ui.Element) { fn(panel, false) })
		return
	}
	rec := ui.Local(anchor, "popover-motion", func() popoverRec { return popoverRec{} })
	if *open && !rec.open {
		w, h := c.Size()
		rec.key, rec.entered = fmt.Sprint("popover ", w, h, anchor.Bounds()), false
	}
	rec.open = *open
	// The key is the panel's: closed, PopoverBase builds none, and the key
	// would go to whatever the caller builds next.
	pc := c
	if *open {
		pc = c.Key(rec.key)
	}
	ui.PopoverBase(pc, anchor, open, func(panel ui.Element) {
		fn(panel, !rec.entered)
		pose, anim := ui.Pose{}, ui.Animation{Duration: durEnter, Ease: ui.EaseOut}
		if !rec.entered {
			// Posed small for a frame, then at rest on the way.
			pose, rec.entered = popPose, true
			c.AnimationFrame()
		}
		panel.TransformOrigin(popoverOrigin(c, anchor, panel)).AnimatePose(pose, anim)
		panel.Transition(exitIn(ui.ElementTransition{Colors: true, Exit: popFrom}, durExit))
	})
}

// popoverIdle tells popoverBase that the popover of anchor is closed in a
// frame its caller does not call popoverBase, so that its next opening
// grows again, with a key of its own.
func popoverIdle(c *ui.Context, anchor ui.Element) {
	if motion(c) == nil {
		return
	}
	ui.Local(anchor, "popover-motion", func() popoverRec { return popoverRec{} }).open = false
}

// popoverOrigin is the corner of the panel that meets its anchor, as
// fractions of the panel: the top where it opens below the anchor, the
// bottom above it. Its first frame has no bounds yet, and opens below the
// anchor in the window's upper half, above in its lower.
func popoverOrigin(c *ui.Context, anchor, panel ui.Element) (x, y float32) {
	ab, pb := anchor.Bounds(), panel.Bounds()
	_, winH := c.Size()
	switch {
	case pb.W > 0 && pb.H > 0:
		x = min(max((ab.X+ab.W/2-pb.X)/pb.W, 0), 1)
		if pb.Y+pb.H/2 < ab.Y+ab.H/2 {
			y = 1
		}
	case ab.Y+ab.H/2 > float32(winH)/2:
		y = 1
	}
	return x, y
}

// dialogMotion makes a dialog come in as the palette does, its panel from a
// little above and smaller, the dimmed window behind it fading in, and
// both go out fading, nothing while the desktop asks for less motion.
// Call it in the dialog's function; fresh tells that the dialog opens in
// this frame, as dialogFresh does.
func dialogMotion(c *ui.Context, backdrop, panel ui.Element, fresh bool) {
	anim := motion(c)
	if anim == nil {
		return
	}
	backdrop.Transition(exitIn(ui.ElementTransition{Duration: durEnter, Ease: ui.EaseOut, Colors: true, Enter: paletteFade, Exit: paletteFade}, durExit))
	pose := ui.Pose{}
	if fresh {
		pose = paletteFrom
	}
	panel.TransformOrigin(0.5, 0).AnimatePose(pose, *anim)
}

// dialogFresh tells whether the dialog whose panel it is opens in this
// frame, and asks for the frame after, which starts the panel moving.
func dialogFresh(c *ui.Context, panel ui.Element) bool {
	shown := ui.Local(panel, "dialog-shown", func() bool { return false })
	if *shown {
		return false
	}
	*shown = true
	c.AnimationFrame()
	return true
}

// dialogEnter is dialogMotion for a dialog that has no state of its own
// to tell it opens.
func dialogEnter(c *ui.Context, backdrop, panel ui.Element) {
	if motion(c) == nil {
		return
	}
	dialogMotion(c, backdrop, panel, dialogFresh(c, panel))
}

// motion is the spring that moves the panes and the sidebar: the user's
// style, nil for none, as while the desktop asks for less motion.
func motion(c *ui.Context) *ui.Animation {
	if c.Preferences().ReduceMotion {
		return nil
	}
	switch prefs.Animation {
	case "off":
		return nil
	case "natural":
		return &natural
	}
	return &snappy
}

// pop is where a pane comes from as it opens, and goes as it closes.
var pop = &ui.Motion{Scale: 0.92, Opacity: 0}

// layoutSettle is how long the panes move to their places after a tab's
// layout changes, longer than the springs take. Otherwise they follow the
// room they have at once, as the window or the sidebar resizes it.
const layoutSettle = 1200 * time.Millisecond

// paneTransition is how the panes of tab t move, open and close, nil for
// not at all: a change of their layout that is not a divider's drag moves
// them to their new places.
func (a *App) paneTransition(c *ui.Context, t *Tab) *ui.ElementTransition {
	drag := a.dividerDrag
	a.dividerDrag = false
	now := c.Now()
	if l := t.layout(); l != t.laid {
		if t.laid != "" && !drag {
			t.settle = now.Add(layoutSettle)
		}
		t.laid = l
	}
	anim := motion(c)
	if anim == nil {
		return nil
	}
	moving := !drag && now.Before(t.settle)
	// Colors, which a cell has none of, when nothing else: none set would
	// move them all.
	tr := exitIn(ui.ElementTransition{Animation: anim, Position: moving, Size: moving, Colors: !moving, Enter: pop, Exit: pop}, durExitMove)
	return &tr
}

// fadeColors tells whether the elements being built fade their colors as
// they change: not in a pane that moves, as MyGo lays out an element with
// a Transition of its own where its parent goes, not where it shows on
// the way.
var fadeColors = true

// layout describes the tree of the tab's panes, their shares, and the one
// zoomed.
func (t *Tab) layout() string {
	var b strings.Builder
	if t.Zoom != nil {
		fmt.Fprintf(&b, "z%d ", t.Zoom.ID)
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		switch {
		case n == nil:
		case n.Pane != nil:
			fmt.Fprintf(&b, "p%d ", n.Pane.ID)
		default:
			fmt.Fprintf(&b, "(%t %.4f ", n.Vertical, n.Ratio)
			walk(n.A)
			walk(n.B)
			b.WriteString(") ")
		}
	}
	walk(t.Root)
	return b.String()
}

// animated is the value under key of e going to target as the panes move,
// or target at once when they do not.
func animated(c *ui.Context, e ui.Element, key string, target float32) float32 {
	if anim := motion(c); anim != nil {
		return e.AnimateTo(key, target, *anim)
	}
	return e.Jump(key, target)
}
