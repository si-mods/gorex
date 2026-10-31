package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestHeaderColors checks that a pane header takes the palette of its
// background when the pane's terminal is of the other appearance, so that a
// hovered button does not turn black on a dark header, and keeps its own
// when the two match.
func TestHeaderColors(t *testing.T) {
	dark := headerColors(&lightColors, ui.Hex("#262626"))
	if dark.text != darkColors.text || dark.iconMuted != darkColors.iconMuted || dark.hover != darkColors.hover || dark.pressed != darkColors.pressed {
		t.Fatalf("a dark header kept the light palette: text %v, hover %v", dark.text, dark.hover)
	}
	if dark.accent != lightColors.accent || dark.card != lightColors.card {
		t.Fatal("headerColors changed colors that do not read on the header")
	}
	light := headerColors(&darkColors, ui.Hex("#fbfbfb"))
	if light.text != lightColors.text || light.hover != lightColors.hover {
		t.Fatalf("a light header kept the dark palette: text %v, hover %v", light.text, light.hover)
	}
	if got := headerColors(&lightColors, ui.Hex("#fbfbfb")); got != &lightColors {
		t.Fatal("a header on the window's own background took another palette")
	}
}
