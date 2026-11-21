package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorex/locales"
)

func TestDrawMenusOn(t *testing.T) {
	for _, c := range []struct {
		goos, env string
		want      bool
	}{
		{"windows", "", true},
		{"linux", "", true},
		{"windows", "0", true},
		{"darwin", "", false},
		{"darwin", "0", false},
		{"darwin", "1", true},
	} {
		if got := drawMenusOn(c.goos, c.env); got != c.want {
			t.Errorf("drawMenusOn(%q, %q) = %v, want %v", c.goos, c.env, got, c.want)
		}
	}
}

// windowMenu runs the app's view in a dark or light window, with the
// space of color, and gives the menu style it left in the theme.
func windowMenu(t *testing.T, color string, dark bool) *ui.MenuStyle {
	t.Helper()
	a, _ := newTestApp(t)
	a.ws.Color = color
	var m *ui.MenuStyle
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		m = c.Theme().Menu
	}, 400, 300)
	tt.SetDark(dark)
	tt.Frame()
	if m == nil {
		t.Fatal("Theme.Menu is nil")
	}
	return m
}

func TestMenuStyleHasWindowPalette(t *testing.T) {
	for _, c := range []struct {
		name  string
		color string
		dark  bool
		base  *colors
		text  ui.Color
	}{
		{"dark space", "teal", true, &darkColors, ui.Hex("#10151c")},
		{"light space", "teal", false, &lightColors, ui.Hex("#ffffff")},
		{"dark plain", "", true, &darkColors, ui.Hex("#10151c")},
		{"light plain", "", false, &lightColors, ui.Hex("#ffffff")},
	} {
		t.Run(c.name, func(t *testing.T) {
			k := spaceColors(c.base, c.color, c.dark)
			m := windowMenu(t, c.color, c.dark)
			if m.Highlight != k.accent || m.Separator != k.panelBorder {
				t.Errorf("highlight %v, separator %v; want the window's %v, %v", m.Highlight, m.Separator, k.accent, k.panelBorder)
			}
			if m.HighlightText != c.text {
				t.Errorf("HighlightText %v, want %v", m.HighlightText, c.text)
			}
			if c.color != "" && k == c.base {
				t.Fatal("the space has no colors of its own")
			}
			if m != menuStyleOf(k, c.dark) {
				t.Error("the style is not the one of the window's colors")
			}
		})
	}
	if windowMenu(t, "teal", true).Highlight == windowMenu(t, "", true).Highlight {
		t.Error("a space color leaves the stock highlight")
	}
}

func TestMenuStyleHoldsStill(t *testing.T) {
	k := spaceColors(&darkColors, "teal", true)
	s1, s2 := menuStyleOf(k, true), menuStyleOf(k, true)
	if s1 != s2 {
		t.Error("the same colors give two styles")
	}
	if menuStyleOf(k, true) == menuStyleOf(&darkColors, true) {
		t.Error("two palettes share a style")
	}
	var first, second *ui.MenuStyle
	a, _ := newTestApp(t)
	a.ws.Color = "teal"
	n := 0
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		if n == 0 {
			first = c.Theme().Menu
		} else {
			second = c.Theme().Menu
		}
		n++
	}, 400, 300)
	tt.Frame()
	tt.Frame()
	if first == nil || first != second {
		t.Errorf("styles of two frames differ: %p, %p", first, second)
	}
}

// TestDrawnMenus checks that, with drawn menus on, the tab's and the pane
// header's menus open in the window, not as the system's, and that
// choosing Rename starts the rename.
func TestDrawnMenus(t *testing.T) {
	old := drawMenus
	drawMenus = true
	t.Cleanup(func() { drawMenus = old })
	a, tt := newTestApp(t)
	tt.Frame()
	// The tab reads "<shell> <dir>"; the pane header's name is the shell.
	shell, dir := a.tab().label()
	tabLabel := shell + " " + dir
	if !slices.Contains(tt.Texts(), tabLabel) {
		t.Fatalf("no tab; texts %q", tt.Texts())
	}
	open := func(label string) {
		t.Helper()
		if err := tt.RightClick(label); err != nil {
			t.Fatalf("%v; texts %q", err, tt.Texts())
		}
		// Tester.Menu reports a drawn menu too: the menu's items among the
		// window's texts tell it is drawn.
		tt.Frame()
	}
	open(tabLabel)
	if !tt.HasText(locales.CmdRenameTab()) {
		t.Fatalf("the tab's menu is not drawn; texts %q", tt.Texts())
	}
	if err := tt.Click(locales.CmdRenameTab()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.renaming == nil {
		t.Error("Rename Tab did not start the rename")
	}
	a.renaming = nil
	tt.Frame()
	open(shell)
	if !tt.HasText(locales.PaneRename()) {
		t.Fatalf("the pane header's menu is not drawn; texts %q", tt.Texts())
	}
	if err := tt.Click(locales.PaneRename()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.paneRenaming == nil {
		t.Error("Rename Pane did not start the rename")
	}
}

// TestSpacePickerIsMenu checks that the space picker's rows are the drawn
// menus' rows (DESIGN.md, Menu): their height, and under the pointer their
// highlight.
func TestSpacePickerIsMenu(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs.Animation = "off"
	a.spaces.open = true
	tt.Frame()
	r, ok := tt.Find(locales.SpaceNew())
	if !ok {
		t.Fatalf("the picker has no New Space…: %q", tt.Texts())
	}
	m := menuStyleOf(&lightColors, false)
	if r.H != m.RowHeight {
		t.Errorf("New Space…'s row is %v high, want the menus' %v", r.H, m.RowHeight)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	img := tt.Image()
	// The row's padding, before its check's box, shows its face.
	o := img.PixOffset(int(r.X+m.RowPadding/2), int(r.Y+r.H/2))
	got := [3]uint8(img.Pix[o : o+3])
	if want := [3]uint8{m.Highlight.R, m.Highlight.G, m.Highlight.B}; got != want {
		t.Errorf("the hovered row reads %v, want the menus' highlight %v", got, want)
	}
}

// TestMenuRowInert checks that a menu row with nothing to run, as an away
// host's project in the project picker, takes no highlight under the
// pointer and runs nothing, as a drawn menu's disabled item.
func TestMenuRowInert(t *testing.T) {
	a := &App{}
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs.Animation = "off"
	m := menuStyleOf(&lightColors, false)
	for _, inert := range []bool{false, true} {
		ran := false
		var run func()
		if !inert {
			run = func() { ran = true }
		}
		tt := ui.NewTester(func(c *ui.Context) {
			c.Root().Background(lightColors.panel)
			ui.Column(c).Width(200).Padding(6).Children(func() {
				a.menuRow(c, &lightColors, "row", false, "acme-api", "", nil, run)
			})
		}, 220, 40)
		tt.Frame()
		r, ok := tt.Find("acme-api")
		if !ok {
			t.Fatal("no row")
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		img := tt.Image()
		o := img.PixOffset(int(r.X+m.RowPadding/2), int(r.Y+r.H/2))
		lit := [3]uint8(img.Pix[o:o+3]) == [3]uint8{m.Highlight.R, m.Highlight.G, m.Highlight.B}
		if lit == inert {
			t.Errorf("inert %v: the hovered row lit %v, want %v", inert, lit, !inert)
		}
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		if inert && ran {
			t.Error("the inert row ran")
		}
	}
}
