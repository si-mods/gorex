package main

import (
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// activityBadge shows what a pane does: the dots of its record, or the
// dot of a pane asking for attention.
func (a *App) activityBadge(c *ui.Context, k *colors, p *Pane) {
	if s := p.status(); needsUser(s) || s != nil && s.State == "working" && !p.attention {
		statusDot(c, k, s, 6).Margin(0, 0, 0, 2)
		return
	}
	if p.attention {
		attentionDot(c, k, p).Margin(0, 0, 0, 2).Tooltip(locales.PaneNeedsAttention())
	}
}

// attentionPop is how a dot of attention pops as a pane comes to ask for
// it: up to half again its size and back.
var attentionPop = []ui.Keyframe{
	{At: 0, Value: 1},
	{At: 140 * time.Millisecond, Value: 1.5, Ease: ui.EaseOut},
	{At: 350 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
}

// attentionDot is the dot of a pane, or of a tab of panes, asking for
// attention. With a style of animation it pops as one of them comes to
// ask, from when a frame first shows that: every dot of the pane, as its
// tab's and its header's, at the same point of the pop, whenever it is
// built, and none once it is over.
func attentionDot(c *ui.Context, k *colors, panes ...*Pane) ui.Element {
	dot := ui.Box(c).Size(7, 7).Radius(k.capsule(7)).Background(k.attention)
	now := c.Now()
	var at time.Time
	for _, p := range panes {
		if !p.attention {
			continue
		}
		if p.popped != p.asked {
			p.popped, p.popAt = p.asked, now
		}
		if p.popAt.After(at) {
			at = p.popAt
		}
	}
	if motion(c) == nil || at.IsZero() {
		return dot
	}
	if since := now.Sub(at); since < attentionPop[len(attentionPop)-1].At {
		s := keyframeAt(attentionPop, since)
		dot.Scale(s, s)
		c.AnimationFrame()
	}
	return dot
}

// keyframeAt is the value of frames at d from their start, held at the
// last after them.
func keyframeAt(frames []ui.Keyframe, d time.Duration) float32 {
	for i := 1; i < len(frames); i++ {
		a, b := frames[i-1], frames[i]
		if d > b.At {
			continue
		}
		ease := b.Ease
		if ease == nil {
			ease = ui.Linear
		}
		return a.Value + (b.Value-a.Value)*ease(float32(max(d-a.At, 0))/float32(b.At-a.At))
	}
	return frames[len(frames)-1].Value
}

// needsUser tells a record of a program that waits for the user, or whose
// end the user did not see.
func needsUser(s *rex.ProgramStatus) bool {
	return s != nil && rex.StatusRank(s.State) >= rex.StatusRank("done")
}

// statusDot shows a record a program reported: three animated dots while
// working, else the dot of attention, red for an error.
func statusDot(c *ui.Context, k *colors, s *rex.ProgramStatus, size float32) ui.Element {
	if s.State == "working" {
		return workingDots(c, k.textMuted).Tooltip(statusTip(s))
	}
	col := k.attention
	if s.State == "error" {
		col = k.danger
	}
	return ui.Box(c).Size(7, 7).Radius(k.capsule(7)).Background(col).Shrink(0).Tooltip(statusTip(s))
}

// statusTip says what a record tells: the program, its state, why it
// waits, and its title and message.
func statusTip(s *rex.ProgramStatus) string {
	says := map[string]func() string{
		"idle": locales.StatusIdle, "working": locales.StatusWorking, "blocked": locales.StatusBlocked,
		"done": locales.StatusDone, "error": locales.StatusError,
		"permission": locales.StatusPermission, "question": locales.StatusQuestion, "auth": locales.StatusAuth,
	}
	var state string
	if f := says[s.State]; f != nil {
		state = f()
	}
	if f := says[s.Kind]; f != nil {
		state += ", " + f()
	}
	// The progress shows here only: beside the dot it squeezes the
	// tab's title.
	if s.Progress >= 0 {
		state += fmt.Sprintf(", %d%%", s.Progress)
	}
	var parts []string
	for _, p := range []string{s.App, state, s.Title, s.Message()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// unfocusedBreathing disables workingDots animation when the window being built
// lacks focus: out of it, a program working for hours would keep the window
// painting for nothing.
var unfocusedBreathing atomic.Bool

// workingDots draws three dots that pulse and hop left-to-right while a
// program is working, or stand still when unfocused or animation is off.
func workingDots(c *ui.Context, col ui.Color) ui.Element {
	const dotBase = float32(3.0)
	const gap = float32(2.5)
	e := ui.Box(c).Size(16, 10).Shrink(0)
	animates := !unfocusedBreathing.Load() && motion(c) != nil
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		if animates {
			p.AnimationFrameRate(30)
		}
		startX := r.X + (r.W-(3*dotBase+2*gap))/2 + dotBase/2
		cy := r.Y + r.H/2
		for i := range 3 {
			alpha := float32(0.75)
			d := dotBase
			dy := float32(0)
			if animates {
				phase := dotPhase(p.Now(), i)
				alpha = 0.35 + 0.65*phase
				d = dotBase * (0.85 + 0.2*phase)
				dy = -1.5 * phase
			}
			cx := startX + float32(i)*(dotBase+gap)
			p.Fill(ui.Rect{X: cx - d/2, Y: cy + dy - d/2, W: d, H: d}, col.Alpha(alpha), d/2)
		}
	})
	return e
}

// dotPhase returns the 0..1 wave value for dot index at now across a
// 1.2s cycle, staggered left-to-right.
func dotPhase(now time.Time, index int) float32 {
	const period = 1200 * time.Millisecond
	t := float64(now.UnixNano()) / float64(period)
	shifted := math.Mod(t-float64(index)*0.16, 1)
	if shifted < 0 {
		shifted += 1
	}
	if shifted >= 0.5 {
		return 0
	}
	return float32(0.5 - 0.5*math.Cos(2*math.Pi*(shifted/0.5)))
}
