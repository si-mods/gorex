//go:build darwin || linux || windows

package rex

import "testing"

// TestParseKeyChord: a key named on the command line reads as the key
// event the server encodes.
func TestParseKeyChord(t *testing.T) {
	cases := []struct {
		in   string
		want KeyInput
	}{
		{"alt+m", KeyInput{Key: "KeyM", Mods: "alt", Unshifted: 'm'}},
		{"Option+M", KeyInput{Key: "KeyM", Mods: "alt", Unshifted: 'm'}},
		{"ctrl+c", KeyInput{Key: "KeyC", Mods: "ctrl", Unshifted: 'c'}},
		{"m", KeyInput{Key: "KeyM", Text: "m", Unshifted: 'm'}},
		{"shift+m", KeyInput{Key: "KeyM", Mods: "shift", Text: "M", Consumed: "shift", Unshifted: 'm'}},
		{"alt+1", KeyInput{Key: "Digit1", Mods: "alt", Unshifted: '1'}},
		{"/", KeyInput{Key: "Slash", Text: "/", Unshifted: '/'}},
		{"space", KeyInput{Key: "Space", Text: " ", Unshifted: ' '}},
		{"escape", KeyInput{Key: "Escape"}},
		{"esc", KeyInput{Key: "Escape"}},
		{"enter", KeyInput{Key: "Enter"}},
		{"shift+tab", KeyInput{Key: "Tab", Mods: "shift"}},
		{"cmd+ctrl+up", KeyInput{Key: "ArrowUp", Mods: "super+ctrl"}},
		{"pagedown", KeyInput{Key: "PageDown"}},
		{"f5", KeyInput{Key: "F5"}},
		{"F24", KeyInput{Key: "F24"}},
		{"KeyM", KeyInput{Key: "KeyM", Text: "m", Unshifted: 'm'}},
		{"alt+KeyM", KeyInput{Key: "KeyM", Mods: "alt", Unshifted: 'm'}},
		{"Digit1", KeyInput{Key: "Digit1", Text: "1", Unshifted: '1'}},
		{"ctrl+Slash", KeyInput{Key: "Slash", Mods: "ctrl", Unshifted: '/'}},
		{"arrow_up", KeyInput{Key: "ArrowUp"}},
	}
	for _, c := range cases {
		got, err := ParseKeyChord(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseKeyChord(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "alt+", "hyper+m", "shift+1", "f25", "ff", "nokey", "é"} {
		if k, err := ParseKeyChord(bad); err == nil {
			t.Errorf("ParseKeyChord(%q) = %+v, want an error", bad, k)
		}
	}
}
