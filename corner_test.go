package main

import "testing"

// TestCardRadius checks that the panes' corners are concentric with the
// radius macOS reports, and that every other case takes windowR's.
func TestCardRadius(t *testing.T) {
	for _, c := range []struct {
		name   string
		goos   string
		window float64
		want   float32
	}{
		{"macos 26 toolbar", "darwin", 26, 18},
		{"macos full screen", "darwin", 0, 8},
		{"windows 11", "windows", 8, 8},
		{"windows maximised", "windows", 0, 8},
		{"linux", "linux", 0, 8},
	} {
		if got := cardRadius(c.window, c.goos); got != c.want {
			t.Errorf("%s: cardRadius(%v, %q) = %v, want %v", c.name, c.window, c.goos, got, c.want)
		}
	}
	// A view without a window, as the tests', has the fallback.
	if got := (&App{}).cardR(&lightColors); got != 8 {
		t.Errorf("no window: cardR() = %v, want 8", got)
	}
}
