package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

func hasFound(tt *ui.Tester, label string) bool {
	_, ok := tt.Find(label)
	return ok
}

// countBlueBorderPixels counts pixels along the left and right edges of r
// where blue dominates red and green (the agent pulsing border).
func countBlueBorderPixels(img *image.RGBA, r ui.Rect) int {
	if img == nil || r.W <= 8 || r.H <= 20 {
		return 0
	}
	hits := 0
	y0 := int(r.Y) + int(r.H*0.25)
	y1 := int(r.Y) + int(r.H*0.75)
	for y := y0; y < y1; y++ {
		for _, baseX := range []int{int(r.X), int(r.X + r.W - 4)} {
			for dx := 0; dx < 4; dx++ {
				c := img.RGBAAt(baseX+dx, y)
				if int(c.B)-int(c.R) >= 35 && int(c.B)-int(c.G) >= 18 {
					hits++
				}
			}
		}
	}
	return hits
}

// TestAgentHighlightVisualAndTakeover checks that a CLI-created or CLI-driven
// pane renders the blue pulsing border and the top-right corner icon mark on
// the tab's program tile (with no text label), clears immediately when the
// user types in the UI, stays cleared across a stale server poll, and re-arms
// when new CLI input arrives.
func TestAgentHighlightVisualAndTakeover(t *testing.T) {
	oldPrefs := prefs
	t.Cleanup(func() { prefs = oldPrefs })
	prefs.AgentHighlight = nil

	a, tt := newTestApp(t)
	tt.SetWindowActive(true)
	waitFor(t, tt, "initial pane", func() bool {
		return a.tab() != nil && a.tab().Focus != nil && a.tab().Focus.SID != ""
	})
	tt.Advance(400 * time.Millisecond)

	p0 := a.tab().Focus
	if _, _, _, ok := a.tab().agentBadgeAt(tt.Now()); ok {
		t.Fatal("human pane unexpectedly reports active agent mark")
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("human pane unexpectedly has %d blue border pixels", n)
	}

	// Drive p0 from the CLI with --agent agy.
	if err := a.client.InputWithAgent(p0.SID, []byte(" "), "agy"); err != nil {
		t.Fatal(err)
	}
	refresh(a)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)

	if label, _, _, ok := a.tab().agentBadgeAt(tt.Now()); !ok || label != "agy" {
		t.Fatalf("tab agentBadgeAt = (%q, %v), want (agy, true)", label, ok)
	}
	if hasFound(tt, "agy") || hasFound(tt, "agent") {
		t.Fatalf("tab rendered a text label instead of the tile corner icon mark; texts=%v", tt.Texts())
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n == 0 {
		t.Fatalf("agent-driven pane drew no blue border pixels in bounds %+v", p0.bounds)
	}

	// Save the pre-takeover SessionInfo to simulate an in-flight stale poll later.
	stale := p0.info

	// Human presses a key in the focused terminal pane -> clears immediately on next frame.
	tt.Key(0, ui.KeyA)
	tt.Frame()
	if _, _, _, ok := a.tab().agentBadgeAt(tt.Now()); ok {
		t.Fatal("typing in UI did not clear tab agent mark immediately")
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("typing in UI left %d blue border pixels", n)
	}

	// A stale server poll carrying the same LastCLIInputAt must not flicker the highlight back on.
	a.apply(nil, map[string]rex.SessionInfo{p0.SID: stale})
	tt.Frame()
	if _, _, _, ok := a.tab().agentBadgeAt(tt.Now()); ok {
		t.Fatal("stale poll flickered agent mark back on after human takeover")
	}

	// New CLI input after takeover re-arms the tile mark and border with the new agent name.
	if err := a.client.InputWithAgent(p0.SID, []byte(" "), "omp"); err != nil {
		t.Fatal(err)
	}
	refresh(a)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if label, _, _, ok := a.tab().agentBadgeAt(tt.Now()); !ok || label != "omp" {
		t.Fatalf("new CLI input did not re-arm 'omp' mark; got (%q, %v)", label, ok)
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n == 0 {
		t.Fatal("new CLI input did not re-arm blue border")
	}
}

// TestAgentHighlightWindowExpiryAndOptOut checks the 60-second idle fade-out
// window on a human-created pane touched by CLI and the settings.json
// agentHighlight=false opt-out switch.
func TestAgentHighlightWindowExpiryAndOptOut(t *testing.T) {
	oldPrefs := prefs
	t.Cleanup(func() { prefs = oldPrefs })
	prefs.AgentHighlight = nil

	a, tt := newTestApp(t)
	tt.SetWindowActive(true)
	waitFor(t, tt, "initial pane", func() bool {
		return a.tab() != nil && a.tab().Focus != nil && a.tab().Focus.SID != ""
	})
	p0 := a.tab().Focus

	if err := a.client.Input(p0.SID, []byte(" ")); err != nil {
		t.Fatal(err)
	}
	refresh(a)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if label, _, _, ok := a.tab().agentBadgeAt(tt.Now()); !ok || label != "cli" {
		t.Fatalf("default CLI mark 'cli' missing; got (%q, %v)", label, ok)
	}

	// Opt-out via prefs.AgentHighlight = false hides both tile mark and border.
	off := false
	prefs.AgentHighlight = &off
	tt.Frame()
	if _, _, _, ok := a.tab().agentBadgeAt(tt.Now()); ok {
		t.Fatal("mark remained active with agentHighlight=false")
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("border remained visible with agentHighlight=false (%d px)", n)
	}

	// Re-enabling within the 60s window shows it again; advancing past 60s fades it out.
	prefs.AgentHighlight = nil
	tt.Frame()
	if _, _, _, ok := a.tab().agentBadgeAt(tt.Now()); !ok {
		t.Fatal("mark did not reappear while still within 60s window")
	}
	tt.Advance(61 * time.Second)
	tt.Frame()
	if _, _, _, ok := a.tab().agentBadgeAt(tt.Now()); ok {
		t.Fatal("mark remained active after 60s without CLI input on human-created pane")
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("border remained visible after 60s without CLI input (%d px)", n)
	}
}

// TestAgentCreatedHighlightPersistsUntilHumanTakeoverOrExit checks the lifetime
// rules in docs/DSK-016-agent-pane-highlight.md:
//  1. createdBy in {cli, delegate} with empty tookOverAt stays highlighted
//     indefinitely across working and idle states (no 60s decay),
//  2. human takeover (tookOverAt set) permanently clears the highlight,
//  3. session exit clears the highlight, and
//  4. the 60s idle fade window applies only to human-created panes touched by CLI.
func TestAgentCreatedHighlightPersistsUntilHumanTakeoverOrExit(t *testing.T) {
	t0 := time.Unix(1_760_000_000, 0)

	// 1. Agent-created panes (cli and delegate) stay highlighted indefinitely
	// whether lastCliInputAt is zero or hours old, and whether working or idle.
	for _, creator := range []string{rex.ActorCLI, rex.ActorDelegate} {
		in := rex.SessionInfo{
			Created:        t0,
			DelegatedAgent: "agy",
			AgentActor: rex.AgentActor{
				CreatedBy:      creator,
				CreatedAt:      t0,
				LastCLIInputAt: t0,
				Agent:          "agy",
				Active:         true,
			},
			Status: &rex.ProgramStatus{ID: "agy", State: "idle", Updated: t0.Add(10 * time.Minute)},
		}
		for _, dt := range []time.Duration{0, 61 * time.Second, 10 * time.Minute, 2 * time.Hour} {
			label, fade, rem, ok := agentInfoStateAt(in, t0.Add(dt))
			if !ok || label != "agy" || fade != 1 || rem != 0 {
				t.Fatalf("createdBy=%s at +%v = (%q, fade=%v, rem=%v, ok=%v), want (agy, 1, 0, true)",
					creator, dt, label, fade, rem, ok)
			}
		}

		// Also persistent when lastCliInputAt is zero (e.g. `gorex split` shell).
		noInput := in
		noInput.AgentActor.LastCLIInputAt = time.Time{}
		if label, fade, rem, ok := agentInfoStateAt(noInput, t0.Add(30*time.Minute)); !ok || label != "agy" || fade != 1 || rem != 0 {
			t.Fatalf("createdBy=%s without lastCliInputAt = (%q, %v, %v, %v), want persistent", creator, label, fade, rem, ok)
		}

		// 2. Human takeover (tookOverAt non-zero) permanently clears the highlight.
		taken := in
		taken.AgentActor.Active = false
		taken.AgentActor.TookOverAt = t0.Add(2 * time.Minute)
		if _, _, _, ok := agentInfoStateAt(taken, t0.Add(2*time.Minute+time.Second)); ok {
			t.Fatalf("createdBy=%s stayed highlighted after tookOverAt was set", creator)
		}

		// 3. Session exit clears the highlight.
		exited := in
		exited.Exited = true
		if _, _, _, ok := agentInfoStateAt(exited, t0.Add(5*time.Second)); ok {
			t.Fatalf("createdBy=%s stayed highlighted after session exit", creator)
		}
	}

	// 4. Human-created pane without CLI input stays unhighlighted; with CLI input
	// it fades across 57s..60s and expires at 60s.
	humanUntouched := rex.SessionInfo{
		Created:    t0,
		AgentActor: rex.AgentActor{CreatedBy: rex.ActorHuman, CreatedAt: t0},
	}
	if _, _, _, ok := agentInfoStateAt(humanUntouched, t0.Add(5*time.Second)); ok {
		t.Fatal("untouched human session unexpectedly highlighted")
	}

	humanScripted := rex.SessionInfo{
		Created: t0,
		AgentActor: rex.AgentActor{
			CreatedBy:      rex.ActorHuman,
			CreatedAt:      t0,
			LastCLIInputAt: t0.Add(10 * time.Second),
			Agent:          "cli",
			Active:         true,
		},
	}
	tWrite := humanScripted.AgentActor.LastCLIInputAt
	if _, fade, rem, ok := agentInfoStateAt(humanScripted, tWrite.Add(30*time.Second)); !ok || fade != 1 || rem != 30*time.Second {
		t.Fatalf("human scripted at +30s = (fade=%v, rem=%v, ok=%v), want (1, 30s, true)", fade, rem, ok)
	}
	if _, fade, _, ok := agentInfoStateAt(humanScripted, tWrite.Add(58500*time.Millisecond)); !ok || fade < 0.45 || fade > 0.55 {
		t.Fatalf("human scripted at +58.5s = (fade=%v, ok=%v), want (~0.5, true)", fade, ok)
	}
	if _, _, _, ok := agentInfoStateAt(humanScripted, tWrite.Add(60*time.Second)); ok {
		t.Fatal("human scripted highlight did not expire after 60s")
	}

	// Non-typing terminal shortcuts (scrollback, copy, select all, zoom) must not clear local agent state.
	for _, sc := range []struct {
		mods ui.Modifiers
		key  ui.Key
	}{
		{ui.Shift, ui.KeyPageUp},
		{ui.Ctrl | ui.Shift, ui.KeyC},
		{ui.Ctrl | ui.Shift, ui.KeyA},
		{ui.Ctrl | ui.Shift, ui.KeyPageDown},
		{ui.Ctrl, ui.KeyInsert},
		{ui.Ctrl, ui.KeyEqual},
		{ui.Ctrl, ui.KeyMinus},
		{ui.Ctrl, ui.Key0},
	} {
		if !isNonTypingTerminalShortcut(sc.mods, sc.key) {
			t.Fatalf("isNonTypingTerminalShortcut(%v, %v) = false, want true", sc.mods, sc.key)
		}
	}
	for _, typing := range []struct {
		mods ui.Modifiers
		key  ui.Key
	}{
		{0, ui.KeyA},
		{ui.Shift, ui.KeyA},
		{ui.Ctrl, ui.KeyC},
	} {
		if isNonTypingTerminalShortcut(typing.mods, typing.key) {
			t.Fatalf("isNonTypingTerminalShortcut(%v, %v) = true, want false", typing.mods, typing.key)
		}
	}

	// Remote unmounted tabs match leaf.Host == hostID; localized accessibility label appends suffix.
	remoteTab := savedTab{
		ID:    "tab-remote",
		Focus: 0,
		Root:  &layout.Node{SID: "sid-remote", Host: "host-remote"},
	}
	remoteInfos := map[string]rex.SessionInfo{
		"sid-remote": {
			Created: t0,
			AgentActor: rex.AgentActor{
				CreatedBy: rex.ActorDelegate,
				CreatedAt: t0,
				Agent:     "agy",
				Active:    true,
			},
		},
	}
	if gotLabel, _, _, ok := savedTabAgentBadgeAt(t0.Add(time.Minute), remoteTab, "host-remote", remoteInfos); !ok || gotLabel != "agy" {
		t.Fatalf("savedTabAgentBadgeAt(host-remote) = (%q, %v), want (agy, true)", gotLabel, ok)
	}
	if _, _, _, ok := savedTabAgentBadgeAt(t0.Add(time.Minute), remoteTab, "", remoteInfos); ok {
		t.Fatal("savedTabAgentBadgeAt(local) unexpectedly matched remote leaf")
	}
	if got := tabAccessLabel("gorex", "main", true); got != locales.TabAgentOperated("gorex main") {
		t.Fatalf("tabAccessLabel(active) = %q, want %q", got, locales.TabAgentOperated("gorex main"))
	}
}
