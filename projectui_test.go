package main

import (
	"path/filepath"
	"slices"
	"testing"

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
