package main

import (
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
	now := time.Now()
	p.agentCleared = true
	p.agentClearedAt = p.info.AgentActor.LastCLIInputAt
	p.info.AgentActor.Active = false
	if p.info.AgentActor.TookOverAt.IsZero() {
		p.info.AgentActor.TookOverAt = now
	}
}

// reconcileAgent updates the pane's local takeover latch against a fresh
// SessionInfo poll from the server.
func (p *Pane) reconcileAgent(in rex.SessionInfo) {
	if !in.AgentActor.Active || !in.AgentActor.TookOverAt.IsZero() {
		p.agentCleared = false
		return
	}
	if !p.agentCleared {
		return
	}
	if !in.AgentActor.LastCLIInputAt.IsZero() && in.AgentActor.LastCLIInputAt.After(p.agentClearedAt) {
		p.agentCleared = false
		return
	}
	p.info.AgentActor.Active = false
	if p.info.AgentActor.TookOverAt.IsZero() {
		p.info.AgentActor.TookOverAt = time.Now()
	}
}

func isAgentCreatedActor(createdBy string) bool {
	return createdBy == rex.ActorCLI || createdBy == rex.ActorDelegate
}

// agentStateAt reports whether pane p should display the agent border and tab
// badge at now, returning the badge label, fade factor (0..1), and time left
// (0 when the highlight is persistent on an agent-created pane).
func (p *Pane) agentStateAt(now time.Time) (label string, fade float32, rem time.Duration, ok bool) {
	if !agentHighlightOn() || p == nil || p.closed || p.ext != nil || p.agentCleared {
		return "", 0, 0, false
	}
	return agentInfoStateAt(p.info, now)
}

func agentInfoStateAt(in rex.SessionInfo, now time.Time) (label string, fade float32, rem time.Duration, ok bool) {
	act := in.AgentActor
	if !act.Active || !act.TookOverAt.IsZero() || in.Exited {
		return "", 0, 0, false
	}
	label = cmp.Or(
		rex.NormalizeAgentName(act.Agent),
		rex.NormalizeAgentName(in.DelegatedAgent),
		rex.ActorCLI,
	)
	// Agent-created panes (createdBy = "cli" or "delegate") stay highlighted
	// permanently while tookOverAt is empty and the session has not exited.
	if isAgentCreatedActor(act.CreatedBy) {
		return label, 1, 0, true
	}
	// Human-created panes touched by a script use the 60 s CLI idle fade window.
	if act.LastCLIInputAt.IsZero() {
		return "", 0, 0, false
	}
	age := max(now.Sub(act.LastCLIInputAt), 0)
	if age >= agentHighlightWindow {
		return "", 0, 0, false
	}
	rem = agentHighlightWindow - age
	fade = 1
	if rem < agentFadeDuration {
		fade = float32(rem) / float32(agentFadeDuration)
	}
	return label, fade, rem, true
}

func betterAgentRemaining(cand, cur time.Duration) bool {
	if cand == 0 {
		return cur != 0
	}
	if cur == 0 {
		return false
	}
	return cand > cur
}

// agentBadgeAt returns the active agent badge for tab t at now, preferring the
// focused pane when active and otherwise a persistent or freshest pane in t.
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
		if active && (!ok || betterAgentRemaining(r, rem)) {
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
			if active && (!ok || betterAgentRemaining(r, rem)) {
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
	animates := !unfocusedBreathing.Load() && motion(c) != nil
	if !animates && rem > 0 {
		if rem > 0 {
			c.After(rem + 20*time.Millisecond)
		}
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

// drawAgentTileMark draws the circular agent sparkles icon badge at the
// top-right corner of a program tile fr, framed with a cutout rim so it reads
// cleanly over any brand tile color without needing a text label.
func drawAgentTileMark(p *ui.Painter, fr ui.Rect, k *colors, fade float32) {
	if fade <= 0 {
		return
	}
	col := k.agent
	if col.A == 0 {
		col = k.accent
	}
	const markSize = float32(9)
	const rimPad = float32(1)
	r := ui.Rect{
		X: fr.X + fr.W + 2.5 - markSize,
		Y: fr.Y - 2.5,
		W: markSize,
		H: markSize,
	}
	rim := ui.Rect{
		X: r.X - rimPad,
		Y: r.Y - rimPad,
		W: r.W + 2*rimPad,
		H: r.H + 2*rimPad,
	}
	rimCol := k.panel.Alpha(0.92 * fade)
	inkCol := k.accentText.Alpha(fade)
	if int(col.R)+int(col.G)+int(col.B) > 450 {
		inkCol = k.panel.Alpha(fade)
	}
	p.Fill(rim, rimCol, rim.W/2)
	p.Fill(r, col.Alpha(fade), r.W/2)
	const iconSize = float32(6)
	ir := ui.Rect{
		X: r.X + (r.W-iconSize)/2,
		Y: r.Y + (r.H-iconSize)/2,
		W: iconSize,
		H: iconSize,
	}
	glyphIn(p, "sparkles", ir, inkCol)
}

// hasHumanInputBytes delegates to rex.HasHumanInputBytes so desktop UI streams
// and the session server share one exact human-input filter.
func hasHumanInputBytes(p []byte) bool {
	return rex.HasHumanInputBytes(p)
}

func isModifierOnlyKey(k ui.Key) bool {
	return k == ui.KeyUnknown || k == ui.KeyBack || k == ui.KeyForward
}

func isScrollbackKey(mods ui.Modifiers, k ui.Key) bool {
	return mods == ui.Shift && (k == ui.KeyPageUp || k == ui.KeyPageDown || k == ui.KeyHome || k == ui.KeyEnd)
}
