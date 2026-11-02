package main

import (
	"os"
	"runtime"
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

func TestMenuStyle(t *testing.T) {
	var m *ui.MenuStyle
	var k *colors
	var drawn bool
	tt := ui.NewTester(func(c *ui.Context) {
		k = colorsOf(c)
		setupMenus(c, k)
		m, drawn = c.Theme().Menu, c.DrawMenus()
		ui.Column(c).Fill().Children(func() {})
	}, 200, 100)
	tt.Frame()
	if m == nil {
		t.Fatal("Theme.Menu is nil")
	}
	if m.Panel == nil || m.RowHeight != 24 || m.RowRadius != 7 || m.RowPadding != 10 || m.TextSize != 13 ||
		m.Highlight != k.accent || m.HighlightText != ui.Hex("#ffffff") || m.Separator != k.panelBorder {
		t.Errorf("menu style %+v", *m)
	}
	if want := drawMenusOn(runtime.GOOS, os.Getenv("GOREX_DRAW_MENUS")); drawn != want {
		t.Errorf("DrawMenus = %v, want %v", drawn, want)
	}
}

// TestDrawnMenus checks that, with drawn menus on, the tab's and the pane
// header's menus open in the window, not as the system's, and that
// choosing Rename starts the rename.
func TestDrawnMenus(t *testing.T) {
	t.Setenv("GOREX_DRAW_MENUS", "1")
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
