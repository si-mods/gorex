package main

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTextFieldFocusAccent checks that a text field holding the focus
// outlines itself, and washes its selected text, in its space's accent,
// light and dark, where the stock widget theme would draw its own blue.
func TestTextFieldFocusAccent(t *testing.T) {
	registerFonts()
	for _, dark := range []bool{false, true} {
		base := &lightColors
		if dark {
			base = &darkColors
		}
		k := spaceColors(base, "green", dark)
		want := rgba(k.accent)
		var value string
		tt := ui.NewTester(func(c *ui.Context) {
			textField(c, k, &value, fieldRegular).Width(180).AutoFocus()
		}, 240, 60)
		tt.SetDark(dark)
		for range 2 {
			tt.Frame()
		}
		// The field is the view's only element, its box at the origin:
		// the top edge, midway along it, is its 1-DIP outline.
		if got := tt.Image().RGBAAt(90, 0); got != want {
			t.Fatalf("outline is %v, want the space's accent %v", got, want)
		}
		// Two spaces select as a solid wash, without a glyph over it.
		value = "  "
		for range 2 {
			tt.Frame()
		}
		tt.Key(ui.Cmd, ui.KeyA)
		for range 2 {
			tt.Frame()
		}
		img, washed := tt.Image(), false
		for y := 4; y < 24; y++ {
			for x := 2; x < 60; x++ {
				// The accent's wash over the field, greener than it is
				// blue; the theme's Selection is the other way round.
				if p := img.RGBAAt(x, y); p.G > p.B+8 && p.G > p.R+8 {
					washed = true
				}
			}
		}
		if !washed {
			t.Fatalf("selected text shows no green wash")
		}
	}
}

// rgba returns c as an image color.
func rgba(c ui.Color) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}
