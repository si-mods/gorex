package main

import (
	"os"
	"testing"

	"gorex/locales"
)

// TestSidebarHeadHoldsChipUnderOwnControls checks that, beside a shown left
// sidebar under own controls, the space chip is in the sidebar's head and
// the title buttons are over the panes.
func TestSidebarHeadHoldsChipUnderOwnControls(t *testing.T) {
	old, oldOwn := prefs, ownControls
	t.Cleanup(func() { prefs, ownControls = old, oldOwn })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left"}
	ownControls = true
	a.newTab(os.TempDir())
	tt.Frame()
	if a.ws == nil {
		t.Fatal("no space")
	}
	chip, ok := tt.Find(locales.SessionLabel(a.ws.Name))
	if !ok {
		t.Fatalf("no space chip; texts %q", tt.Texts())
	}
	if chip.Y < 0 || chip.Y+chip.H > titleH {
		t.Errorf("chip at y %v..%v, want in the head band 0..%v", chip.Y, chip.Y+chip.H, titleH)
	}
	side := sidebarWidthIn(1000)
	for _, label := range []string{locales.PaletteTitle(), locales.CmdNewTab()} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q button", label)
		}
		if r.X <= side {
			t.Errorf("%q at x %v, want right of the sidebar (%v)", label, r.X, side)
		}
	}
}
