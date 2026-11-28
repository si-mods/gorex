package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
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
// pane renders the blue pulsing border and tab badge, clears immediately when
// the user types in the UI, stays cleared across a stale server poll, and
// re-arms when new CLI input arrives.
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
	if hasFound(tt, "agy") || hasFound(tt, "cli") {
		t.Fatalf("human pane unexpectedly shows an agent badge: %v", tt.Texts())
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("human pane unexpectedly has %d blue border pixels", n)
	}

	// Drive p0 from the CLI with --agent agy.
	if err := a.client.InputWithAgent(p0.SID, []byte(" "), "agy"); err != nil {
		t.Fatal(err)
	}
	refresh(a)
	tt.Advance(200 * time.Millisecond)

	if !hasFound(tt, "agy") {
		t.Fatalf("tab does not show agent badge 'agy'; texts=%v", tt.Texts())
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n == 0 {
		t.Fatalf("agent-driven pane drew no blue border pixels in bounds %+v", p0.bounds)
	}

	// Save the pre-takeover SessionInfo to simulate an in-flight stale poll later.
	stale := p0.info

	// Human presses a key in the focused terminal pane -> clears immediately on next frame.
	tt.Key(0, ui.KeyA)
	tt.Frame()
	if hasFound(tt, "agy") {
		t.Fatalf("typing in UI did not clear 'agy' badge immediately; texts=%v", tt.Texts())
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("typing in UI left %d blue border pixels", n)
	}

	// A stale server poll carrying the same LastCLIInputAt must not flicker the highlight back on.
	a.apply(nil, map[string]rex.SessionInfo{p0.SID: stale})
	tt.Frame()
	if hasFound(tt, "agy") {
		t.Fatal("stale poll flickered agent badge back on after human takeover")
	}

	// New CLI input after takeover re-arms the badge and border with the new agent name.
	time.Sleep(5 * time.Millisecond)
	if err := a.client.InputWithAgent(p0.SID, []byte(" "), "omp"); err != nil {
		t.Fatal(err)
	}
	refresh(a)
	tt.Advance(200 * time.Millisecond)
	if !hasFound(tt, "omp") {
		t.Fatalf("new CLI input did not re-arm 'omp' badge; texts=%v", tt.Texts())
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n == 0 {
		t.Fatal("new CLI input did not re-arm blue border")
	}
}

// TestAgentHighlightWindowExpiryAndOptOut checks the 60-second idle fade-out
// window and the settings.json agentHighlight=false opt-out switch.
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
	tt.Advance(200 * time.Millisecond)
	if !hasFound(tt, "cli") {
		t.Fatalf("default CLI badge 'cli' missing; texts=%v", tt.Texts())
	}

	// Opt-out via prefs.AgentHighlight = false hides both badge and border.
	off := false
	prefs.AgentHighlight = &off
	tt.Frame()
	if hasFound(tt, "cli") {
		t.Fatal("badge remained visible with agentHighlight=false")
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("border remained visible with agentHighlight=false (%d px)", n)
	}

	// Re-enabling within the 60s window shows it again; advancing past 60s fades it out.
	prefs.AgentHighlight = nil
	tt.Frame()
	if !hasFound(tt, "cli") {
		t.Fatal("badge did not reappear while still within 60s window")
	}
	tt.Advance(61 * time.Second)
	tt.Frame()
	if hasFound(tt, "cli") {
		t.Fatal("badge remained visible after 60s without CLI input")
	}
	if n := countBlueBorderPixels(tt.Image(), p0.bounds); n > 0 {
		t.Fatalf("border remained visible after 60s without CLI input (%d px)", n)
	}
}
