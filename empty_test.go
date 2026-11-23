package main

import (
	"testing"

	"gorex/locales"
)

// TestEmptyNoTab shows the main area's empty state when no tab is open
// (docs/DSK-006-empty-states.md): the title, the note, and the buttons
// that make the first tab and open the command palette.
func TestEmptyNoTab(t *testing.T) {
	a, tt := newTestApp(t)
	a.closeTab(a.tab())
	tt.Frame()
	if !tt.HasText(locales.EmptyNoTab()) || !tt.HasText(locales.EmptyNoTabNote()) {
		t.Fatalf("no empty state; texts %q", tt.Texts())
	}
	if err := tt.Click(locales.CmdNewTab()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "a tab", func() bool { return a.tab() != nil })

	// Back to no tab, the palette button opens the command palette.
	a.closeTab(a.tab())
	tt.Frame()
	if err := tt.Click(locales.CmdCommandPalette()); err != nil {
		t.Fatal(err)
	}
	if !a.paletteOpen {
		t.Error("the Command Palette button did not open the palette")
	}
}

// TestSidebarEmptyCard shows the sidebar's empty card when a host has no
// projects and no tabs: the row keeps a project row's height so the list
// reads as one rhythm (docs/DSK-006-empty-states.md).
func TestSidebarEmptyCard(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off"}
	a.closeTab(a.tab())
	tt.Frame()
	r, ok := tt.Find(locales.EmptyNoProjects())
	if !ok {
		t.Fatalf("no empty card; texts %q", tt.Texts())
	}
	if r.H != projectRowH {
		t.Errorf("empty card height %v, want %v (a project row's)", r.H, projectRowH)
	}
	// The + button at the row's end opens the New Project sheet.
	if err := tt.Click(cmdNewProject.Title()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the New Project sheet", func() bool { return a.sheet.open })
}

// TestPaletteNoMatchesFor names the query when the palette matches
// nothing.
func TestPaletteNoMatchesFor(t *testing.T) {
	a, tt := newTestApp(t)
	a.openPalette()
	tt.Frame()
	tt.Type("zzzz")
	tt.Frame()
	if !tt.HasText(locales.PaletteNoMatchesFor("zzzz")) {
		t.Fatalf("no named empty state; texts %q", tt.Texts())
	}
}

// TestFindNoMatches shows the find bar's no-result state: the query, not
// a count of zero.
func TestFindNoMatches(t *testing.T) {
	a, tt := newTestApp(t)
	a.openFind()
	tt.Frame()
	tt.Type("zzzz")
	waitFor(t, tt, "no matches", func() bool { return tt.HasText(locales.FindNoMatches()) })
}
