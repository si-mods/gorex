package rex

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// A hex color reads in both forms a Ghostty theme uses: the app accepted
// "#fff" and the server did not, so the same theme lost its colors on a
// host.
func TestParseHexColorTakesBothForms(t *testing.T) {
	short, ok := ParseHexColor("#fff")
	if !ok {
		t.Fatal(`ParseHexColor("#fff") refused`)
	}
	long, ok := ParseHexColor("#ffffff")
	if !ok {
		t.Fatal(`ParseHexColor("#ffffff") refused`)
	}
	if short != long {
		t.Errorf("#fff = %v, #ffffff = %v", short, long)
	}
	if want := ui.Hex("#ffffff"); short != want {
		t.Errorf("#fff = %v, want %v", short, want)
	}
	for _, bad := range []string{"", "#", "#ff", "#ffff", "#fffff", "#ggg", "#fffffff", "0xffffff"} {
		if c, ok := ParseHexColor(bad); ok {
			t.Errorf("ParseHexColor(%q) = %v, want refused", bad, c)
		}
	}
}

func TestThemeTakesShortHex(t *testing.T) {
	th := toTerminalTheme(&Theme{Foreground: "#abc", Background: "def"})
	if th == nil {
		t.Fatal("toTerminalTheme refused a theme of short hex colors")
	}
	if want := ui.Hex("#aabbcc"); th.Foreground != want {
		t.Errorf("foreground = %v, want %v", th.Foreground, want)
	}
	if want := ui.Hex("#ddeeff"); th.Background != want {
		t.Errorf("background = %v, want %v", th.Background, want)
	}
}
