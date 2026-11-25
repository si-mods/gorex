package main

import (
	"testing"

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

// TestColorDot: every item of a color menu gets a dot, made once.
func TestColorDot(t *testing.T) {
	for _, ch := range colorChoices("") {
		d := colorDot(ch.id)
		if d == nil || colorDot(ch.id) != d {
			t.Errorf("%s: dot %p, made again %p", ch.label, d, colorDot(ch.id))
		}
	}
}
