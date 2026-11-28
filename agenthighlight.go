package main

import (
	"bytes"
	"cmp"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// Timings for the agent-driven pane highlight (docs/DSK-016-agent-pane-highlight.md).
const (
	agentHighlightWindow = 60 * time.Second
	agentPulsePeriod     = 2400 * time.Millisecond
	agentFadeDuration    = 3 * time.Second
)

// agentHighlightOn reports whether settings.json leaves agent pane highlighting
// enabled (absent or true).
func agentHighlightOn() bool {
	return prefs.AgentHighlight == nil || *prefs.AgentHighlight
}

// clearAgentLocal clears the pane's agent mark immediately on the UI thread
// when the user types or pastes in the pane, and suppresses stale polls until
// the server records the takeover or receives newer CLI input.
func (p *Pane) clearAgentLocal() {
	if p == nil {
		return
	}
	p.agentCleared = true
	p.agentClearedAt = p.info.AgentActor.LastCLIInputAt
	p.info.AgentActor.Active = false
}

// reconcileAgent updates the pane's local takeover latch against a fresh
// SessionInfo poll from the server.
func (p *Pane) reconcileAgent(in rex.SessionInfo) {
	if !in.AgentActor.Active {
		p.agentCleared = false
		return
	}
	if !p.agentCleared {
		return
	}
	if in.AgentActor.LastCLIInputAt.After(p.agentClearedAt) {
		p.agentCleared = false
		return
	}
	p.info.AgentActor.Active = false
}

// agentStateAt reports whether pane p should display the agent border and tab
// badge at now, returning the badge label, fade factor (0..1), and time left.
func (p *Pane) agentStateAt(now time.Time) (label string, fade float32, rem time.Duration, ok bool) {
	if !agentHighlightOn() || p == nil || p.closed || p.ext != nil || p.agentCleared {
		return "", 0, 0, false
	}
	return agentInfoStateAt(p.info, now)
}

func agentInfoStateAt(in rex.SessionInfo, now time.Time) (label string, fade float32, rem time.Duration, ok bool) {
	act := in.AgentActor
	if !act.Active || in.Exited {
		return "", 0, 0, false
	}
	ref := act.LastCLIInputAt
	if act.CreatedBy != "" && act.CreatedBy != rex.ActorHuman && in.Created.After(ref) {
		ref = in.Created
	}
	if ref.IsZero() {
		return "", 0, 0, false
	}
	age := max(now.Sub(ref), 0)
	if age >= agentHighlightWindow {
		return "", 0, 0, false
	}
	rem = agentHighlightWindow - age
	fade = 1
	if rem < agentFadeDuration {
		fade = float32(rem) / float32(agentFadeDuration)
	}
	label = cmp.Or(
		rex.NormalizeAgentName(act.Agent),
		rex.NormalizeAgentName(in.DelegatedAgent),
		rex.ActorCLI,
	)
	return label, fade, rem, true
}

// agentBadgeAt returns the active agent badge for tab t at now, preferring the
// focused pane when active and otherwise the most recently driven pane in t.
func (t *Tab) agentBadgeAt(now time.Time) (label string, fade float32, rem time.Duration, ok bool) {
	if !agentHighlightOn() || t == nil {
		return "", 0, 0, false
	}
	if t.Focus != nil {
		if l, f, r, active := t.Focus.agentStateAt(now); active {
			return l, f, r, true
		}
	}
	for _, p := range t.allPanes() {
		l, f, r, active := p.agentStateAt(now)
		if active && (!ok || r > rem) {
			label, fade, rem, ok = l, f, r, true
		}
	}
	return label, fade, rem, ok
}

// savedTabAgentBadgeAt returns the active agent badge for an unmounted tab in
// another workspace shown in the sidebar.
func savedTabAgentBadgeAt(now time.Time, t savedTab, infos map[string]rex.SessionInfo) (label string, fade float32, rem time.Duration, ok bool) {
	if !agentHighlightOn() || len(infos) == 0 {
		return "", 0, 0, false
	}
	focus := focusedLeaf(t)
	if focus.Host == "" && focus.SID != "" {
		if in, found := infos[focus.SID]; found {
			if l, f, r, active := agentInfoStateAt(in, now); active {
				return l, f, r, true
			}
		}
	}
	for _, root := range t.Roots() {
		for _, leaf := range layout.Leaves(root) {
			if leaf.Host != "" || leaf.SID == "" {
				continue
			}
			in, found := infos[leaf.SID]
			if !found {
				continue
			}
			l, f, r, active := agentInfoStateAt(in, now)
			if active && (!ok || r > rem) {
				label, fade, rem, ok = l, f, r, true
			}
		}
	}
	return label, fade, rem, ok
}

// agentPulseWave returns a smooth 0..1..0 cosine wave with period
// agentPulsePeriod (2.4 s).
func agentPulseWave(now time.Time) float32 {
	phase := float64(now.UnixNano()) / float64(agentPulsePeriod)
	return float32(0.5 - 0.5*math.Cos(2*math.Pi*phase))
}

// drawAgentPaneBorder overlays a calm blue pulsing border on card while p is
// agent-owned or within the 60 s CLI input window.
func drawAgentPaneBorder(c *ui.Context, card ui.Element, k *colors, p *Pane, cardR float32) {
	_, _, rem, ok := p.agentStateAt(c.Now())
	if !ok {
		return
	}
	col := k.agent
	if col.A == 0 {
		col = k.accent
	}
	animates := breathing && motion(c) != nil
	if !animates {
		c.After(rem + 20*time.Millisecond)
	}
	card.DrawOver(func(pt *ui.Painter, r ui.Rect) {
		if r.W <= 2 || r.H <= 2 {
			return
		}
		if animates {
			pt.AnimationFrameRate(24)
		}
		now := pt.Now()
		_, curFade, _, still := p.agentStateAt(now)
		if !still {
			return
		}
		wave := float32(0.55)
		if animates {
			wave = agentPulseWave(now)
		}
		d := ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}
		rad := concentric(cardR, 1)
		glowAlpha := (0.10 + 0.18*wave) * curFade
		lineAlpha := (0.42 + 0.46*wave) * curFade
		pt.Stroke(d, col.Alpha(glowAlpha), rad, 3.5)
		pt.Stroke(d, col.Alpha(lineAlpha), rad, 1.5)
	})
}

// agentBadge renders the compact agent pill on a tab in the title bar or
// sidebar.
func agentBadge(c *ui.Context, k *colors, label string, fade float32, rem time.Duration) {
	col := k.agent
	if col.A == 0 {
		col = k.accent
	}
	c.After(rem + 20*time.Millisecond)
	ui.Text(c, label).FontSize(badgeSize).FontWeight(600).
		TextColor(col.Alpha(0.95*fade)).
		Padding(2, 6).Radius(k.radius(radiusS)).
		Background(col.Alpha(0.16 * fade)).
		SingleLine().Ellipsis("…").MaxWidth(badgeMaxW + 12).Shrink(0).
		Tooltip("Agent: " + label)
}

// hasHumanInputBytes reports whether p holds user-typed terminal bytes rather
// than terminal query replies (DA, CPR, DECRPM, OSC) or window focus reports.
func hasHumanInputBytes(p []byte) bool {
	if len(p) == 0 {
		return false
	}
	if bytes.IndexByte(p, 0x1b) < 0 {
		return true
	}
	for i := 0; i < len(p); {
		if bytes.HasPrefix(p[i:], []byte("\x1b[I")) || bytes.HasPrefix(p[i:], []byte("\x1b[O")) {
			i += 3
			continue
		}
		if n := termReplyLen(p[i:]); n > 0 {
			i += n
			continue
		}
		return true
	}
	return false
}

func termReplyLen(p []byte) int {
	if len(p) < 3 || p[0] != 0x1b {
		return 0
	}
	switch p[1] {
	case ']', 'P', '_':
		if bytes.HasPrefix(p, []byte("\x1b]5522;")) {
			return 0
		}
		for i := 2; i < len(p); i++ {
			switch {
			case p[i] == 0x07 && p[1] == ']':
				return i + 1
			case p[i] == 0x1b:
				if i+1 < len(p) && p[i+1] == '\\' {
					return i + 2
				}
				return 0
			}
		}
	case '[':
		i, mark := 2, byte(0)
		if p[i] == '?' || p[i] == '>' {
			mark = p[i]
			i++
		}
		for i < len(p) && (p[i] >= '0' && p[i] <= '9' || p[i] == ';') {
			i++
		}
		if i == len(p) {
			return 0
		}
		switch f := p[i]; {
		case f == '$':
			if i+1 < len(p) && p[i+1] == 'y' {
				return i + 2
			}
		case f == 'R', f == 'n',
			f == 'c' && mark != 0,
			f == 't' && mark == 0,
			f == 'u' && mark == '?':
			return i + 1
		}
	}
	return 0
}
