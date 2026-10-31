//go:build !darwin

package main

import "github.com/egoist/mygo/ui"

// sfGlyph has no SF Symbols to draw here: glyphIcon and glyphIn use the
// SVGs of assets/icons.
func sfGlyph(name string, px int, col ui.Color) (ui.ImageSource, bool) {
	return nil, false
}

func sfGlyphAt(name string, px int, col ui.Color, natural bool) (ui.ImageSource, bool) {
	return nil, false
}
