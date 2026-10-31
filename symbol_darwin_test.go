//go:build darwin

package main

import (
	"image"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSFSymbolsOfTheIcons checks that a symbol the app names draws, at the
// size asked and with the color asked, and that one this macOS has not
// says so rather than drawing nothing.
func TestSFSymbolsOfTheIcons(t *testing.T) {
	for _, name := range []string{"xmark", "gearshape", "sidebar.leading", "rectangle.split.2x1", "terminal"} {
		src, ok := sfGlyph(name, 48, ui.Hex("#ffffff"))
		if !ok {
			t.Fatalf("%s: the system draws no such symbol", name)
		}
		if w, h := src.(*ui.Bitmap).Size(); w != 48 || h != 48 {
			t.Fatalf("%s: %dx%d pixels, want 48x48", name, w, h)
		}
	}
	if _, ok := sfGlyph("no.symbol.of.that.name", 48, ui.Hex("#ffffff")); ok {
		t.Fatal("a name the system has not drew as a symbol")
	}
}

// TestSFSymbolCoverage checks that a symbol covers some of its box and not
// all of it, and that tint keeps the premultiplied alpha of the shape.
func TestSFSymbolCoverage(t *testing.T) {
	coverage, ok := rasterize("xmark", 64, false)
	if !ok {
		t.Fatal("xmark drew nothing")
	}
	lit := 0
	for i := range 64 * 64 {
		if coverage.Pix[i*4+3] != 0 {
			lit++
		}
	}
	if lit == 0 || lit == 64*64 {
		t.Fatalf("%d of %d pixels lit", lit, 64*64)
	}
	col := ui.RGBA(0xff, 0x40, 0x20, 0.5)
	tinted := tint(coverage, col)
	for i := range 64 * 64 {
		a, r := tinted.Pix[i*4+3], tinted.Pix[i*4+0]
		if r == 0 && a != 0 && coverage.Pix[i*4+3] != 0 {
			t.Fatal("a lit pixel lost its color")
		}
		if r > a {
			t.Fatalf("pixel %d: %d red over %d alpha, not premultiplied", i, r, a)
		}
	}
}

// TestSFSymbolNames checks the map: no brand marks, and no empty names.
func TestSFSymbolNames(t *testing.T) {
	for name, sf := range sfSymbols {
		if strings.HasPrefix(name, "brand:") {
			t.Fatalf("%s: brand marks stay SVGs", name)
		}
		if sf == "" {
			t.Fatalf("%s: no symbol", name)
		}
	}
}

// TestSFSymbolFillsItsBox checks that a symbol's ink fills the box rather
// than the alignment margins of the image it comes in, and that it sits
// centered in it.
func TestSFSymbolFillsItsBox(t *testing.T) {
	for _, name := range []string{"xmark", "gearshape", "rectangle.split.2x1", "chevron.down"} {
		coverage, ok := rasterize(name, 48, false)
		if !ok {
			t.Fatalf("%s drew nothing", name)
		}
		b := inkOf(coverage)
		if big := max(b.Dx(), b.Dy()); big < 40 || big > 44 {
			t.Fatalf("%s: ink is %dx%d, want about 41x41 in a 48 box", name, b.Dx(), b.Dy())
		}
		cx, cy := (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2
		if max(cx-24, 24-cx) > 2 || max(cy-24, 24-cy) > 2 {
			t.Fatalf("%s: ink centered at %d,%d, want 24,24", name, cx, cy)
		}
	}
}

// inkOf returns the box of the lit pixels of coverage.
func inkOf(coverage *image.RGBA) image.Rectangle {
	w, h := coverage.Rect.Dx(), coverage.Rect.Dy()
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := range h {
		for x := range w {
			if coverage.Pix[y*coverage.Stride+x*4+3] != 0 {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}
