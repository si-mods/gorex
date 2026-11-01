package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestBackgroundShader checks that the background shader, run on the CPU
// at a small size, lands within the dither of the gradients it replaces.
func TestBackgroundShader(t *testing.T) {
	const w, h = 100, 80
	for _, k := range []*colors{&lightColors, &darkColors} {
		shaded := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { background.Draw(p, r, backgroundOptions(k)) })
		}, w, h).Image()
		plain := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k) })
		}, w, h).Image()
		worst := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a, b := shaded.PixOffset(x, y), plain.PixOffset(x, y)
				if shaded.Pix[a+3] != 255 {
					t.Fatalf("pixel %d,%d is not opaque: %v", x, y, shaded.Pix[a:a+4])
				}
				for i := 0; i < 3; i++ {
					d := int(shaded.Pix[a+i]) - int(plain.Pix[b+i])
					worst = max(worst, max(d, -d))
				}
			}
		}
		if worst > 3 {
			t.Errorf("the shader differs from the gradients by %d steps, want at most 3", worst)
		}
	}
}

// TestDimmed checks that a dimmed element draws faded on the CPU renderer,
// as Opacity does, where the shader does not run.
func TestDimmed(t *testing.T) {
	px := func(dim bool) [4]uint8 {
		img := ui.NewTester(func(c *ui.Context) {
			b := ui.Box(c).Fill().Background(ui.Hex("#ff0000"))
			if dim {
				dimmed(b, 0.5)
			}
		}, 40, 40).Image()
		o := img.PixOffset(10, 10)
		return [4]uint8(img.Pix[o : o+4])
	}
	if lit, dim := px(false), px(true); dim == lit {
		t.Errorf("a dimmed red reads %v, want it fainter than %v", dim, lit)
	}
}
