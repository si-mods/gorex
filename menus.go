package main

import (
	"os"
	"runtime"

	"github.com/egoist/mygo/ui"
)

// drawMenusOn tells whether the windows draw their context menus
// themselves, as glass, rather than show the system's: on Windows and
// Linux always, and on macOS, whose menus are Liquid Glass already, only
// where GOREX_DRAW_MENUS is 1, to compare the two side by side.
func drawMenusOn(goos, env string) bool {
	return goos != "darwin" || env == "1"
}

// menuStyle is the look of a drawn menu in k's colors: the glass panel of
// the popovers, rounded as macOS 26's menu, with rows of the same shape.
// The highlight is k.accent with white text.
func menuStyle(k *colors) *ui.MenuStyle {
	return &ui.MenuStyle{
		Panel: func(panel ui.Element, level int) {
			glassPanel(panel.Radius(12).Padding(5), k.panel).Border(1, k.panelBorder).
				Shadow(0, 12, 32, 0, k.shadowFocused)
		},
		RowHeight: 24, RowRadius: 7, RowPadding: 10,
		Highlight: k.accent, HighlightText: ui.Hex("#ffffff"),
		Separator: k.panelBorder,
		TextSize:  13,
	}
}

// The styles of the two palettes colorsOf gives, made once: a new one
// each frame would make the theme differ from the last frame's, which
// views that compare themes, as markdown's, take for a change of theme.
var darkMenu, lightMenu = menuStyle(&darkColors), menuStyle(&lightColors)

// setupMenus gives c's window the drawn menus' look, and turns them on
// where drawMenusOn says. Every window's view calls it each frame, before
// anything opens a menu.
func setupMenus(c *ui.Context, k *colors) {
	c.SetDrawMenus(drawMenusOn(runtime.GOOS, os.Getenv("GOREX_DRAW_MENUS")))
	t := *c.Theme()
	t.Menu = lightMenu
	if k == &darkColors {
		t.Menu = darkMenu
	}
	c.SetTheme(&t)
}
