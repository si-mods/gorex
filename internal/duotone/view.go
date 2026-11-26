package duotone

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Options configures how Icon.View renders a duotone icon.
type Options struct {
	// Size is the icon's side in DIPs (default 20).
	Size float32
	// Primary is the accent color used for key details and strokes.
	Primary ui.Color
	// Secondary is the body color; derived via Secondary(Primary, Dark) when zero.
	Secondary ui.Color
	// Dark selects dark-surface secondary color derivation when Secondary is zero.
	Dark bool
	// Hovered drives the hover motion from a parent element such as a row.
	Hovered bool
	// Still disables pointer-driven hover on the icon itself (Hovered still applies).
	Still bool
	// NoMotion keeps the icon at its rest pose even when hovered.
	NoMotion bool
	// Label sets an accessibility label; empty leaves the icon decorative.
	Label string
}

type hoverState struct {
	hovered  bool
	entering bool
	from     float64
	start    time.Time
	enterDur time.Duration
}

func (h *hoverState) update(hovered bool, now time.Time, reduceMotion bool) {
	if reduceMotion {
		h.hovered = hovered
		h.entering = hovered
		if hovered {
			h.from = 1
		} else {
			h.from = 0
		}
		h.start = now
		return
	}
	if hovered == h.hovered {
		return
	}
	cur, _, _ := h.progress(now)
	h.hovered = hovered
	h.entering = hovered
	h.from = cur
	h.start = now
}

func (h *hoverState) progress(now time.Time) (q float64, entering bool, moving bool) {
	if h.hovered {
		if h.from >= 1 || h.enterDur <= 0 {
			return 1, true, false
		}
		rem := time.Duration(float64(h.enterDur) * (1 - h.from))
		if rem <= 0 {
			return 1, true, false
		}
		t := float64(now.Sub(h.start)) / float64(rem)
		if t >= 1 {
			return 1, true, false
		}
		if t < 0 {
			t = 0
		}
		return h.from + (1-h.from)*t, true, true
	}
	if h.from <= 0 || leaveDuration <= 0 {
		return 0, false, false
	}
	rem := time.Duration(float64(leaveDuration) * h.from)
	if rem <= 0 {
		return 0, false, false
	}
	t := float64(now.Sub(h.start)) / float64(rem)
	if t >= 1 {
		return 0, false, false
	}
	if t < 0 {
		t = 0
	}
	return h.from * (1 - t), false, true
}

// View returns an element displaying the icon with hover animation.
func (ic *Icon) View(c *ui.Context, o Options) ui.Element {
	size := o.Size
	if size <= 0 {
		size = 20
	}
	primary := o.Primary
	if primary == (ui.Color{}) {
		primary = ui.Hex("#5A76F2")
	}
	secondary := o.Secondary
	if secondary == (ui.Color{}) {
		secondary = Secondary(primary, o.Dark)
	}

	box := ui.Box(c).Size(size, size).Shrink(0)
	if o.Label != "" {
		box = box.Label(o.Label)
	}
	hovered := !o.NoMotion && (o.Hovered || (!o.Still && box.Hovered()))
	now := c.Now()
	reduce := o.NoMotion || c.Preferences().ReduceMotion
	st := ui.Local(box, "duotone.hover", func() hoverState {
		from := 0.0
		if hovered {
			from = 1
		}
		return hoverState{
			hovered:  hovered,
			entering: hovered,
			from:     from,
			start:    now,
			enterDur: ic.enterDur,
		}
	})
	st.enterDur = ic.enterDur
	st.update(hovered, now, reduce)

	return box.Draw(func(p *ui.Painter, r ui.Rect) {
		q, entering, moving := st.progress(p.Now())
		if moving {
			p.AnimationFrame()
		}
		s := ic.SVG(primary, secondary, q, entering)
		p.Image(s, r, ui.Contain)
	})
}
