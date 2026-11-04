package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSettingsMaxWidth: in a wide window the settings stay at their
// column's width, not stretched to the window's.
func TestSettingsMaxWidth(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	a := &App{}
	settingsUI.section = "appearance"
	tt := ui.NewTester(a.settingsView, 1800, 700)
	tt.Frame()
	off, ok := tt.Find("Off")
	if !ok {
		t.Fatalf("no Off: %q", tt.Texts())
	}
	if right := off.X + off.W; right > settingsSidebarW+settingsPad+settingsMaxW {
		t.Fatalf("controls end at %v in a 1800 window", right)
	}
}
