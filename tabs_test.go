package main

import (
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
