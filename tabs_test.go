package main

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestFadeTextFitsWidth checks that a faded label, spaces and all, keeps
// its text within the width it is given: the end fades out, it does not
// spill past the box around it.
func TestFadeTextFitsWidth(t *testing.T) {
	registerFonts()
	const width = 171
	// The draw must run: a test that lays nothing out fails, not passes.
	spill := float32(1e9)
	tt := ui.NewTester(func(c *ui.Context) {
		// Measure with the font registerFonts registers, not the
		// system's: the metrics, and so the test, stay the same
		// wherever it runs.
		theme := *c.Theme()
		theme.Font = "JetBrains Mono"
		c.SetTheme(&theme)
		ui.Box(c).Width(width).Height(18).Draw(func(p *ui.Painter, r ui.Rect) {
			ink := ui.RGBA(255, 255, 255, 255)
			f := layoutFade(p, r.W, tabLabel("SYNC: Fix panel sync from remote worktree branches", "", ink, ink))
			w, _ := p.MeasureText(0, f.spans...)
			spill = w - r.W
		})
	}, 400, 60)
	tt.Frame()
	if spill > 1 {
		t.Errorf("a faded label takes %v DIPs past its width", spill)
	}
}

// TestTitleSpans checks that a title's private use glyphs, the icons
// programs head their titles with, take the terminals' font, and its text
// the window's.
func TestTitleSpans(t *testing.T) {
	oldFont := termFont
	t.Cleanup(func() { termFont = oldFont })
	termFont.Family = "Maple Mono NF CN, monospace"
	col := ui.Hex("#e6e6e6")
	ghost := ui.Span{Text: "\U000f0379 ", Size: 13.5, Weight: 500, Color: col, Font: termFont.Family}
	cases := []struct {
		name, text string
		want       []ui.Span
	}{
		{"no glyph", "Gorex", []ui.Span{
			{Text: "Gorex", Size: 13.5, Weight: 500, Color: col},
		}},
		{"no text", "", []ui.Span{
			{Text: "", Size: 13.5, Weight: 500, Color: col},
		}},
		{"headed by a glyph", "\U000f0379 SYNC: fix", []ui.Span{
			ghost,
			{Text: "SYNC: fix", Size: 13.5, Weight: 500, Color: col},
		}},
		{"a glyph within", "π \U000f0379 fix", []ui.Span{
			{Text: "π ", Size: 13.5, Weight: 500, Color: col},
			ghost,
			{Text: "fix", Size: 13.5, Weight: 500, Color: col},
		}},
		{"glyphs together", "\uf179\U000f0379 x", []ui.Span{
			{Text: "\uf179\U000f0379 ", Size: 13.5, Weight: 500, Color: col, Font: termFont.Family},
			{Text: "x", Size: 13.5, Weight: 500, Color: col},
		}},
	}
	for _, c := range cases {
		got := titleSpans(c.text, 13.5, 500, col)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: spans %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestPrivateUse checks the runes private use areas hold, and the runes
// around them.
func TestPrivateUse(t *testing.T) {
	for r, want := range map[rune]bool{
		0xE000:   true,
		0xF8FF:   true,
		0xF900:   false, // CJK compatibility ideographs
		0xF0000:  true,
		0xFFFFD:  true,
		0xFFFFE:  false, // the noncharacters after it
		0x100000: true,
		0x10FFFD: true,
		0x10FFFE: false,
		0x41:     false,
	} {
		if got := privateUse(r); got != want {
			t.Errorf("privateUse(%U) = %v, want %v", r, got, want)
		}
	}
}
