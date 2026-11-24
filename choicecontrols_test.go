package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// TestSpaceColorDotMenu: in Edit Spaces…, a left click on a space's color
// dot opens the colors, as a right click does, and changes none; choosing
// one sets it.
func TestSpaceColorDotMenu(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	setSpaceColorOf("", "")
	a.spaces.dialog, a.spaces.names = "edit", map[string]string{}
	tt.Frame()
	click(t, tt, locales.SpaceColor())
	m := tt.Menu()
	if m == nil {
		t.Fatalf("the click opened no menu; texts %q", tt.Texts())
	}
	if spaceColor("") != "" {
		t.Errorf("the click set the color %q", spaceColor(""))
	}
	if len(m) != len(colorChoices("")) || m[0] != locales.SpaceColorDefault() {
		t.Errorf("menu %q", m)
	}
	if err := tt.ChooseMenuItem("Blue"); err != nil {
		t.Fatal(err)
	}
	if spaceColor("") != "blue" {
		t.Errorf("color after the pick: %q", spaceColor(""))
	}
	// A right click lists them too.
	tt.Frame()
	rightClick(t, tt, locales.SpaceColor())
	if tt.Menu() == nil {
		t.Error("the right click opened no menu")
	}
	tt.CloseMenu()
}

// TestColorChoicesTick: a color menu ticks Default for no color, and the
// color its owner has after a pick. The tester shows no item's tick, so the
// choices both menus are built from are read.
func TestColorChoicesTick(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	ticked := func(cur string) []string {
		var out []string
		for _, ch := range colorChoices(cur) {
			if ch.checked {
				out = append(out, ch.label)
			}
		}
		return out
	}
	if got := ticked(""); len(got) != 1 || got[0] != locales.SpaceColorDefault() {
		t.Errorf("no color ticks %q", got)
	}
	setSpaceColorOf("", "rose")
	if got := ticked(spaceColor("")); len(got) != 1 || got[0] != "Rose" {
		t.Errorf("rose ticks %q", got)
	}
	// The project row's Color submenu lists the same choices.
	space := ""
	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-api", SpaceName: &space})
	caughtUp(t, a, tt)
	w := a.st.byID(res.Space)
	a.setProjectColor(w, "teal")
	rightClick(t, tt, "acme-api")
	if err := tt.ChooseMenuItem(locales.SpaceColor(), "Default"); err != nil {
		t.Fatal(err)
	}
	caughtUp(t, a, tt)
	if got := ticked(w.Color); w.Color != "" || len(got) != 1 || got[0] != locales.SpaceColorDefault() {
		t.Errorf("project color %q ticks %q", w.Color, got)
	}
}

// TestProjectSheetHostKeys: Down on the focused host button of the New
// Project sheet opens the host menu, and does not move the folder list's
// highlight.
func TestProjectSheetHostKeys(t *testing.T) {
	a, tt := newTestApp(t)
	root := folderTree(t, appTempDir(t, a))
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	for range 12 {
		if tt.Focused(locales.ProjectSheetHost()) {
			break
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	if !tt.Focused(locales.ProjectSheetHost()) {
		t.Fatal("Tab did not reach the host button")
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if tt.Menu() == nil {
		t.Error("Down on the host button opened no menu")
	}
	if a.sheet.sel != -1 {
		t.Errorf("Down on the host button highlighted folder %d", a.sheet.sel)
	}
}
