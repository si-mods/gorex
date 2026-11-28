package main

import (
	"os"
	"runtime"
	"sync"

	"github.com/egoist/mygo/ui"
)

// drawMenusOn tells whether the windows draw their context menus
// themselves, as glass, rather than show the system's: on Windows and
// Linux always, and on macOS, whose menus are Liquid Glass already, only
// where GOREX_DRAW_MENUS is 1, to compare the two side by side.
func drawMenusOn(goos, env string) bool {
	return goos != "darwin" || env == "1"
}

// drawMenus is drawMenusOn for this process, worked out once at startup:
// the environment and the system do not change under a running app.
var drawMenus = drawMenusOn(runtime.GOOS, os.Getenv("GOREX_DRAW_MENUS"))

// menuStyleKey names a menu style: the colors of a window, in a theme.
type menuStyleKey struct {
	k    *colors
	dark bool
}

// The styles made, one for each palette a window has been given, so that
// a window's theme holds still from frame to frame: views that compare
// themes, as markdown's, take a new style for a change of theme. The
// windows' views may run on their own goroutines, as spaceColors' do, so
// the map has its lock.
var (
	menuStylesMu sync.Mutex
	menuStyles   = map[menuStyleKey]*ui.MenuStyle{}
)

// menuStyleOf is the look of a drawn menu in k's colors, in a dark window
// or a light one: the glass panel of the popovers, rounded as macOS 26's
// menu, with rows of the same shape. The highlight is k.accent, with the
// text k.accentText. It gives the same pointer for the same
// arguments.
func menuStyleOf(k *colors, dark bool) *ui.MenuStyle {
	w := k.window()
	menuStylesMu.Lock()
	defer menuStylesMu.Unlock()
	key := menuStyleKey{w, dark}
	if m, ok := menuStyles[key]; ok {
		return m
	}
	border := w.panelBorder
	if w.menuBorder.A > 0 {
		border = w.menuBorder
	}
	pad, rowH, rowPad, textSize := float32(6), float32(24), float32(10), float32(13)
	if w.m.radius == 0 {
		pad, rowH, rowPad, textSize = 3, 20, 6, 12
	}
	m := &ui.MenuStyle{
		Panel: func(panel ui.Element, level int) {
			p := panel.Radius(w.radius(radiusL)).Padding(pad).Border(w.borderW(), border)
			if w.menuBackground.A > 0 {
				popoverShadow(p.Background(w.menuBackground), w)
			} else {
				popoverShadow(glassPanel(p, w), w)
			}
		},
		RowHeight: rowH, RowRadius: concentric(w.radius(radiusL), pad), RowPadding: rowPad,
		Highlight: w.accent, HighlightText: w.accentText,
		Text: w.text, TextMuted: w.textMuted,
		Separator: border,
		TextSize:  textSize,
	}
	menuStyles[key] = m
	return m
}

// setupMenus turns the drawn menus on in c's window, where drawMenus says.
// Every window's view calls it each frame, before anything opens a menu.
// The look comes with the theme: widgetTheme's, or menuTheme's.
func setupMenus(c *ui.Context) {
	c.SetDrawMenus(drawMenus)
}

// menuTheme is base with the drawn menus' look in k's colors.
func menuTheme(base *ui.Theme, k *colors) *ui.Theme {
	t := *base
	applyWidgetControls(&t, k)
	return &t
}
