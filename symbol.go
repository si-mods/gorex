// Icons in the UI: SF Symbols on macOS, where the system has them, and the
// SVGs of assets/icons everywhere else, as Rex has them.
package main

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// sfSymbols maps an icon of assets/icons to the SF Symbol that says the
// same on macOS. A name here is drawn as a symbol where the system has it,
// and as its SVG where it does not: on an older macOS, and on Windows and
// Linux. Brand marks stay SVGs: SF Symbols has no logos.
var sfSymbols = map[string]string{
	"activity":                  "waveform.path.ecg",
	"app-window":                "macwindow",
	"check":                     "checkmark",
	"chevron-down":              "chevron.down",
	"command":                   "command",
	"copy":                      "document.on.document",
	"cpu":                       "cpu",
	"diff":                      "plus.forwardslash.minus",
	"file-code":                 "doc.text",
	"git-branch":                "arrow.triangle.branch",
	"globe":                     "globe",
	"hexagon":                   "hexagon",
	"keyboard":                  "keyboard",
	"laptop":                    "laptopcomputer",
	"layers":                    "square.3.layers.3d",
	"mac-studio":                "macstudio",
	"maximize-2":                "arrow.up.left.and.arrow.down.right",
	"minimize-2":                "arrow.down.right.and.arrow.up.left",
	"minus":                     "minus",
	"monitor":                   "display",
	"node-hex":                  "hexagon",
	"panel-left":                "sidebar.leading",
	"panel-right":               "sidebar.trailing",
	"pencil":                    "pencil",
	"plus":                      "plus",
	"plus-minus-circle":         "plusminus.circle",
	"rows-2":                    "rectangle.split.1x2",
	"search":                    "magnifyingglass",
	"server":                    "server.rack",
	"settings-2":                "gearshape",
	"sparkles":                  "sparkles",
	"square":                    "square",
	"square-arrow-out-up-right": "arrow.up.forward.square",
	"square-terminal":           "terminal",
	"terminal":                  "terminal",
	"trash-2":                   "trash",
	"x":                         "xmark",
	"columns-2":                 "rectangle.split.2x1",
	"float":                     "rectangle.on.rectangle",
}

// glyphIcon draws the icon named name, size DIPs square, in col: an SF
// Symbol on macOS where one is known and present, else the embedded SVG.
func glyphIcon(c *ui.Context, name string, size float32, col ui.Color) ui.Element {
	if src, ok := symbolFor(name, size, col); ok {
		return ui.Image(c, src).Size(size, size)
	}
	return ui.Icon(c, icon(name)).Size(size, size).TextColor(col)
}

// glyphIn draws the icon named name in r, in col, as glyphIcon draws it.
func glyphIn(p *ui.Painter, name string, r ui.Rect, col ui.Color) {
	if src, ok := symbolFor(name, max(r.W, r.H), col); ok {
		p.Image(src, r, ui.Contain)
		return
	}
	p.Icon(icon(name), r, col)
}

// symbolFor returns the bitmap of name's SF Symbol at size DIPs, in col,
// or false when name has no symbol or the system does not have it. It is
// drawn at three times that size, so that it stays sharp on any display.
func symbolFor(name string, size float32, col ui.Color) (ui.ImageSource, bool) {
	sf, ok := sfSymbols[name]
	if !ok {
		return nil, false
	}
	px := int(math.Round(float64(size) * 3))
	return sfGlyph(sf, px, col)
}
