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
)

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
	return &ui.ElementTransition{Animation: anim, Position: moving, Size: moving, Colors: !moving, Enter: pop, Exit: pop}
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
