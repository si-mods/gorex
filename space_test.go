package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSpaceColorsReadable checks that every color a space takes leaves its
// text on the window's every background, and its accent on the face of
// controls, as readable as WCAG asks.
func TestSpaceColorsReadable(t *testing.T) {
	ids := make([]string, 0, len(spacePalette)+4)
	for _, p := range spacePalette {
		ids = append(ids, p.ID)
	}
	ids = append(ids, "#ffff00", "#000000", "#ffffff", "#808080")
	for _, dark := range []bool{false, true} {
		base := &lightColors
		if dark {
			base = &darkColors
		}
		for _, id := range ids {
			k := spaceColors(base, id, dark)
			bgs := []struct {
				name string
				c    ui.Color
			}{
				{"bgTop", k.bgTop}, {"bgMid", k.bgMid}, {"bgLow", k.bgLow}, {"bgBottom", k.bgBottom},
			}
			for _, bg := range bgs {
				if got := contrast(base.text, bg.c); got < 7 {
					t.Errorf("%s, dark %v: text on %s contrasts %.2f, want 7", id, dark, bg.name, got)
				}
			}
			if got := contrast(k.accent, base.surface); got < 4.5 {
				t.Errorf("%s, dark %v: accent on surface contrasts %.2f, want 4.5", id, dark, got)
			}
		}
	}
}

// TestSpaceColorsDefault checks that a space of no color, or of one that
// is none, keeps the window's own.
func TestSpaceColorsDefault(t *testing.T) {
	for _, dark := range []bool{false, true} {
		base := &lightColors
		if dark {
			base = &darkColors
		}
		for _, color := range []string{"", "nope", "#ff00", "BLUE"} {
			if got := spaceColors(base, color, dark); got != base {
				t.Errorf("color %q, dark %v: colors of its own", color, dark)
			}
		}
	}
}

// TestSpaceColorsCache checks that a color's colors are computed once, and
// that a space of another color, or in another theme, gets others.
func TestSpaceColorsCache(t *testing.T) {
	first := spaceColors(&lightColors, "teal", false)
	if again := spaceColors(&lightColors, "teal", false); again != first {
		t.Error("the same color computed twice")
	}
	if dark := spaceColors(&darkColors, "teal", true); dark == first {
		t.Error("a dark window shares the light one's colors")
	}
	if other := spaceColors(&lightColors, "blue", false); other == first {
		t.Error("two colors share their colors")
	}
}

// TestSpaceTint checks that a custom color is made a tint: saturated at
// most 85%, light between 35% and 65%.
func TestSpaceTint(t *testing.T) {
	for _, hex := range []string{"#ffff00", "#000000", "#ffffff", "#808080", "#0090ff"} {
		_, s, l := hslOf(spaceTint(ui.Hex(hex)))
		// An 8-bit channel rounds the clamped values by up to a step.
		if s > 0.85+1.0/255 || l < 0.35-1.0/255 || l > 0.65+1.0/255 {
			t.Errorf("%s tints with saturation %.2f, lightness %.2f", hex, s, l)
		}
	}
}
