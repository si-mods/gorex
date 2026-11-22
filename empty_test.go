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
