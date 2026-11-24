package main

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/theme"
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
	prefs = settings{SidebarRowHeight: 26, SidebarIndent: 10, SidebarTabIndent: 6, SidebarGroupPad: 8}
	if sidebarRowH() != 26 || sidebarLead() != 10 || sidebarTabLead() != 16 || sidebarGroupPad() != 8 {
		t.Fatalf("metrics %v %v %v %v", sidebarRowH(), sidebarLead(), sidebarTabLead(), sidebarGroupPad())
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

// TestSidebarThemePrefs: a theme's sidebar.* values sit under the user's
// and over the constants (docs/DSK-007, docs/DSK-008).
func TestSidebarThemePrefs(t *testing.T) {
	old, oldT := prefs, themeSidebar
	t.Cleanup(func() { prefs, themeSidebar = old, oldT })
	prefs = settings{}
	themeSidebar = theme.Sidebar{Indent: 14, TabIndent: 8, RowHeight: 30, GroupFace: "none", Icons: "text"}
	if sidebarLead() != 14 || sidebarTabLead() != 22 || sidebarRowH() != 30 {
		t.Fatalf("theme metrics %v %v %v", sidebarLead(), sidebarTabLead(), sidebarRowH())
	}
	if sidebarGroupFaced() {
		t.Error("the theme's \"none\" left the group faced")
	}
	if !sidebarIconsText() {
		t.Error("the theme's \"text\" kept the tiles")
	}
	prefs.SidebarIndent = 10
	prefs.SidebarGroupFace = "face"
	if sidebarLead() != 10 || sidebarTabLead() != 18 {
		t.Fatalf("the user's indent lost to the theme: %v %v", sidebarLead(), sidebarTabLead())
	}
	if !sidebarGroupFaced() {
		t.Error("the user's \"face\" lost to the theme's \"none\"")
	}
	prefs, themeSidebar = settings{}, theme.Sidebar{}
	if sidebarLead() != sideLead || sidebarRowH() != sideRowH {
		t.Fatalf("no layer left the constants: %v %v", sidebarLead(), sidebarRowH())
	}
}

// TestSidebarPaletteOverrides: sidebar.colorCustomizations colors a copy
// of the palette, per appearance, and leaves the shared sets alone.
func TestSidebarPaletteOverrides(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{SidebarColors: map[string]json.RawMessage{
		"sideBar.activeGroupBackground":    json.RawMessage(`"#112233"`),
		"sideBar.activeGroupTabBackground": json.RawMessage(`{"light":"#ffffff","dark":"#3a3a52"}`),
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
