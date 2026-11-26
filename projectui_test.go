package main

import (
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gorex/internal/rex"
	"gorex/locales"
)

// TestProjectNoteDropped shows another project: a note about the one the
// window left goes; a note of no project stays.
func TestProjectNoteDropped(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.ws
	setProjectHome(t, a, tt, "", filepath.Join(appTempDir(t, a), "gone"))
	a.folderMissing("")
	if a.note() == nil {
		t.Fatal("no note")
	}
	other := a.st.newTestSpace()
	a.showWorkspace(other)
	tt.Frame()
	if a.errText() != "" {
		t.Errorf("the note of %s stays in %s: %q", first.Name, other.Name, a.errText())
	}
	a.err = "something else"
	a.showWorkspace(first)
	tt.Frame()
	if a.errText() != "something else" {
		t.Errorf("an error of no project went: %q", a.errText())
	}
}

// TestProjectAgentLaunchers verifies that on a host reporting installed
// agent CLIs an open project's New Tab row shows the pinned agents' glyphs
// and the chevron's menu of every installed one, and either opens a tab
// that runs the agent in the project (docs/MOD-010-agent-launchers.md).
func TestProjectAgentLaunchers(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}

	dir := appTempDir(t, a)
	setProjectHome(t, a, tt, "", dir)
	a.st.agents = []rex.AgentInfo{
		{ID: "claude", Name: "Claude Code", Command: []string{"/bin/sh", "-c", "sleep 5"}},
		{ID: "codex", Name: "Codex", Command: []string{"/bin/sh", "-c", "sleep 5"}},
	}
	tt.Frame()

	if _, ok := tt.Find(locales.CmdNewTab()); !ok {
		t.Fatalf("no New Tab row under the project; texts %q", tt.Texts())
	}
	if _, ok := tt.Find("Claude Code"); !ok {
		t.Fatalf("Claude Code launcher not found in sidebar; texts %q", tt.Texts())
	}
	if _, ok := tt.Find("Codex"); !ok {
		t.Fatalf("Codex launcher not found in sidebar; texts %q", tt.Texts())
	}

	before := len(a.tabs)
	tt.Click("Claude Code")
	tt.Frame()
	if len(a.tabs) != before+1 {
		t.Fatalf("clicking Claude Code launcher opened %d tabs (was %d)", len(a.tabs), before)
	}

	// The chevron's menu lists every installed agent, checked while pinned,
	// and the pin toggles; choosing an agent opens a tab that runs it.
	click(t, tt, "Quick Launchers")
	if m := tt.Menu(); !slices.Contains(m, "Claude Code") || !slices.Contains(m, "Codex") || !slices.Contains(m, "Quick Launchers") {
		t.Fatalf("the agents' menu %q", m)
	}
	before = len(a.tabs)
	if err := tt.ChooseMenuItem("Codex"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.tabs) != before+1 {
		t.Fatalf("choosing Codex in the menu opened %d tabs (was %d)", len(a.tabs), before)
	}

	// Pinning only Codex hides Claude Code's glyph and keeps Codex's.
	a.toggleProjectLauncher(a.ws, "claude")
	tt.Frame()
	if _, ok := tt.Find("Claude Code"); ok {
		t.Fatalf("Claude Code launcher should be hidden when unpinned")
	}
	if _, ok := tt.Find("Codex"); !ok {
		t.Fatalf("Codex launcher should remain visible when pinned")
	}
	// Resetting to nil restores all installed launchers.
	a.setProjectLaunchers(a.ws, nil)
	tt.Frame()
	if _, ok := tt.Find("Claude Code"); !ok {
		t.Fatalf("Claude Code launcher should reappear after reset")
	}
}

// TestProjectNewTabRowHover checks that the New Tab button in the project
// sidebar aligns its hover pill's leading edge with the tab rows above it
// (keeping concentric corners with the project group) and paints the hover
// face from that edge, even when sidebar.tabIndent is set.
func TestProjectNewTabRowHover(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}

	dir := appTempDir(t, a)
	setProjectHome(t, a, tt, "", dir)
	a.st.agents = []rex.AgentInfo{
		{ID: "claude", Name: "Claude Code", Command: []string{"/bin/sh", "-c", "sleep 5"}},
		{ID: "codex", Name: "Codex", Command: []string{"/bin/sh", "-c", "sleep 5"}},
	}
	tt.Frame()

	name, detail := a.tabs[0].label()
	tabRect, ok := tt.Find(name + " " + detail)
	if !ok {
		t.Fatalf("active tab row not found; texts %q", tt.Texts())
	}
	// Use a pinned agent button in the same row for the row's Y coordinate;
	// on Linux the sidebar header also carries a "+" button labeled CmdNewTab.
	claudeRect, ok := tt.Find("Claude Code")
	if !ok {
		t.Fatalf("Claude Code launcher not found; texts %q", tt.Texts())
	}

	// Hovering inside the leading edge aligned with the tab row above
	// (tabRect.X + 4, which sits inside sidebarTabLead) must hit the New Tab
	// button and paint its hover face.
	probeX := tabRect.X + 4
	probeY := claudeRect.Y + claudeRect.H/2
	beforePx := tt.Image().RGBAAt(int(probeX), int(probeY))
	tt.Move(probeX, probeY)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	afterPx := tt.Image().RGBAAt(int(probeX), int(probeY))
	if afterPx == beforePx {
		t.Fatalf("pixel at (%.0f,%.0f) did not change on hover: stayed %v", probeX, probeY, beforePx)
	}

	// With sidebar.tabIndent set, the hover pill's leading edge stays aligned
	// with the tab row.
	prefs.SidebarTabIndent = 12
	tt.Move(0, 0)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	tabIndented, ok := tt.Find(name + " " + detail)
	if !ok {
		t.Fatalf("active tab row not found after indent")
	}
	claudeIndented, ok := tt.Find("Claude Code")
	if !ok {
		t.Fatalf("Claude Code launcher not found after indent")
	}
	if math.Abs(float64(tabIndented.X-tabRect.X)) > 0.5 {
		t.Fatalf("tab row leading edge shifted from %.1f to %.1f", tabRect.X, tabIndented.X)
	}
	probeX = tabIndented.X + 4
	probeY = claudeIndented.Y + claudeIndented.H/2
	beforeIndented := tt.Image().RGBAAt(int(probeX), int(probeY))
	tt.Move(probeX, probeY)
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	afterIndented := tt.Image().RGBAAt(int(probeX), int(probeY))
	if afterIndented == beforeIndented {
		t.Fatalf("indented pixel at (%.0f,%.0f) did not change on hover: stayed %v", probeX, probeY, beforeIndented)
	}
}
