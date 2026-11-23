package main

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSidebarLeavesRoomForRightControls: with the sidebar on the left and
// the window's controls on the right, as on Linux, the panes start under
// the title bar, so their buttons do not lie under the controls.
func TestSidebarLeavesRoomForRightControls(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	_, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", Animation: "off"}
	tt.SetTitleBar(ui.TitleBar{Height: titleH, Right: 120})
	tt.Frame()
	settle(tt)
	r, ok := tt.Find("Split Right")
	if !ok {
		t.Fatalf("no Split Right; texts %q", tt.Texts())
	}
	if r.Y < titleH {
		t.Errorf("the pane's button is at y %v, in the controls' band of %v", r.Y, float32(titleH))
	}
}

// TestSidebarMetricPrefs: the sidebar.* metric keys win over the
// constants, and unset keeps the constant (docs/DSK-007).
func TestSidebarMetricPrefs(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{SidebarRowHeight: 26, SidebarIndent: 10, SidebarGroupPad: 8}
	if sidebarRowH() != 26 || sidebarLead() != 10 || sidebarGroupPad() != 8 {
		t.Fatalf("metrics %v %v %v", sidebarRowH(), sidebarLead(), sidebarGroupPad())
	}
	if projectRowH() != 26 {
		t.Fatalf("projectRowH %v", projectRowH())
	}
	if sidebarFont(fontBody) != fontBody {
		t.Errorf("fontSize unset took %v", sidebarFont(fontBody))
	}
	prefs.SidebarFontSize = 12
	if sidebarFont(fontBody) != 12 {
		t.Errorf("fontSize 12 gave %v", sidebarFont(fontBody))
	}
}

// TestSidebarPaletteOverrides: sidebar.colorCustomizations colors a copy
// of the palette, per appearance, and leaves the shared sets alone.
func TestSidebarPaletteOverrides(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{SidebarColors: map[string]json.RawMessage{
		"groupFace": json.RawMessage(`"#112233"`),
		"pillLift":  json.RawMessage(`{"light":"#ffffff","dark":"#3a3a52"}`),
	}}
	l := sidebarPalette(&lightColors, false)
	if l.groupFace != ui.Hex("#112233") || l.pillLift != ui.Hex("#ffffff") {
		t.Fatalf("light overrides %v %v", l.groupFace, l.pillLift)
	}
	if l == &lightColors {
		t.Fatal("the shared palette was returned")
	}
	d := sidebarPalette(&darkColors, true)
	if d.pillLift != ui.Hex("#3a3a52") || d.groupFace != ui.Hex("#112233") {
		t.Fatalf("dark overrides %v %v", d.pillLift, d.groupFace)
	}
	if darkColors.groupFace == d.groupFace {
		t.Error("the shared palette was mutated")
	}
}
