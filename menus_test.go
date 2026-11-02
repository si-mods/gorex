package main

import (
	"strings"
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
	if menuStyleOf(k, true) != menuStyleOf(k, true) {
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
	// The tab reads "<shell> …/dir"; the pane header's name is the shell.
	var tabLabel, shell string
	for _, s := range tt.Texts() {
		if name, rest, ok := strings.Cut(s, " …/"); ok && !strings.Contains(name, " ") && rest != "" {
			tabLabel, shell = s, name
		}
	}
	if tabLabel == "" {
		t.Fatalf("no tab; texts %q", tt.Texts())
	}
	open := func(label string) {
		t.Helper()
		if err := tt.RightClick(label); err != nil {
			t.Fatalf("%v; texts %q", err, tt.Texts())
		}
		tt.Frame()
		if got := tt.Menu(); got != nil {
			t.Fatalf("the system shows the menu %q", got)
		}
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
