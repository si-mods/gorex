package main

import "testing"

// TestCardRadius checks that the panes' corners are concentric with the
// window's, and that a window which tells no radius takes windowR's.
func TestCardRadius(t *testing.T) {
	for _, c := range []struct {
		name   string
		window float64
		want   float32
	}{
		{"none", 0, 8},
		{"windows 11", 8, 0},
		{"macos 26 toolbar", 26, 18},
	} {
		if got := cardRadius(c.window); got != c.want {
			t.Errorf("%s: cardRadius(%v) = %v, want %v", c.name, c.window, got, c.want)
		}
	}
	// A view without a window, as the tests', has the fallback.
	if got := (&App{}).cardR(); got != 8 {
		t.Errorf("no window: cardR() = %v, want 8", got)
	}
	if got := (&App{winR: 26}).cardR(); got != 18 {
		t.Errorf("winR 26: cardR() = %v, want 18", got)
	}
}
