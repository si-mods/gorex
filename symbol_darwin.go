//go:build darwin

package main

// SF Symbols, read from the system: NSImage draws the symbol the running
// macOS has, with its own design and weight, and CGBitmapContext gives the
// pixels. Nothing of Apple's artwork is in the app or its build.
//
// The name of a symbol that this macOS does not have draws nothing, and
// sfGlyph says so, so that the callers fall back to assets/icons.

import (
	"image"
	"math"
	"runtime"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/ui"
)

var (
	selSymbolImage  = objc.RegisterName("imageWithSystemSymbolName:accessibilityDescription:")
	selSymbolConfig = objc.RegisterName("configurationWithPointSize:weight:")
	selSymbolApply  = objc.RegisterName("imageWithSymbolConfiguration:")
	selCGImage      = objc.RegisterName("CGImageForProposedRect:context:hints:")
)

// cgRect is CGRect, which CGContextDrawImage takes by value.
type cgRect struct {
	_          structs.HostLayout
	X, Y, W, H float64
}

// cg holds the CoreGraphics functions the rasterizer calls, once resolved.
var cg struct {
	once   sync.Once
	ok     bool
	space  func() uintptr
	bitmap func(data unsafe.Pointer, width, height, bitsPerComponent, bytesPerRow, space uintptr, info uint32) uintptr
	draw   func(ctx uintptr, rect cgRect, img uintptr)
	width  func(img uintptr) uintptr
	height func(img uintptr) uintptr
}

// cgPremultipliedLast and cgByteOrder32Big name the pixel format of the
// canvases: 8 bits of red, green, blue and alpha each, in that order.
const (
	cgPremultipliedLast = 1
	cgByteOrder32Big    = 4 << 12
)

func loadCG() bool {
	cg.once.Do(func() {
		if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			return
		}
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&cg.space, lib, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&cg.bitmap, lib, "CGBitmapContextCreate")
		purego.RegisterLibFunc(&cg.draw, lib, "CGContextDrawImage")
		purego.RegisterLibFunc(&cg.width, lib, "CGImageGetWidth")
		purego.RegisterLibFunc(&cg.height, lib, "CGImageGetHeight")
		cg.ok = true
	})
	return cg.ok
}

// sfKey names a drawn symbol: its name, its pixels square, and its color.
type sfKey struct {
	name       string
	px         int
	r, g, b, a uint8
	natural    bool
}

var (
	sfMu    sync.Mutex
	sfCache = map[sfKey]*ui.Bitmap{}
)

// sfGlyph draws the SF Symbol named name, px pixels square, in col, and
// returns it as an image source, or false when this macOS has no such
// symbol, or AppKit is not here to draw it.
func sfGlyph(name string, px int, col ui.Color) (ui.ImageSource, bool) {
	return sfGlyphAt(name, px, col, false)
}

// sfGlyphAt draws it as sfGlyph does, or with natural, at the size the
// symbol has beside the others of the same point size.
func sfGlyphAt(name string, px int, col ui.Color, natural bool) (ui.ImageSource, bool) {
	if !loadCG() {
		return nil, false
	}
	px = min(max(px, 8), 256)
	key := sfKey{name: name, px: px, r: col.R, g: col.G, b: col.B, a: col.A, natural: natural}
	sfMu.Lock()
	b, ok := sfCache[key]
	sfMu.Unlock()
	if ok {
		return b, true
	}
	coverage, ok := rasterize(name, px, natural)
	if !ok {
		return nil, false
	}
	b = ui.NewBitmap(tint(coverage, col))
	sfMu.Lock()
	sfCache[key] = b
	sfMu.Unlock()
	return b, true
}

// rasterize draws the symbol's outline into a px×px canvas, centered and
// inset a little, as the icons of assets/icons sit in their box. With
// natural, every symbol takes one scale, as AppKit draws them at one point
// size: a cross stays smaller than a wide rectangle, as SF Symbols balance
// them, rather than each filling the box.
func rasterize(name string, px int, natural bool) (*image.RGBA, bool) {
	img := objc.Send[objc.ID](objc.ID(objc.GetClass("NSImage")), selSymbolImage, nsString(name), nsString(""))
	if img == 0 {
		return nil, false
	}
	// A point size of px pixels renders at least 2× that on a Retina
	// screen: the canvas below scales it down, never up.
	cfg := objc.Send[objc.ID](objc.ID(objc.GetClass("NSImageSymbolConfiguration")), selSymbolConfig, float64(px), 0.0)
	img = objc.Send[objc.ID](img, selSymbolApply, cfg)
	cgImage := objc.Send[objc.ID](img, selCGImage, uintptr(0), uintptr(0), uintptr(0))
	if cgImage == 0 {
		return nil, false
	}
	w, h := int(cg.width(uintptr(cgImage))), int(cg.height(uintptr(cgImage)))
	if w <= 0 || h <= 0 {
		return nil, false
	}

	// The glyph inside its alignment margins, in a canvas of its own size.
	src := make([]byte, w*h*4)
	ctx := cg.bitmap(unsafe.Pointer(&src[0]), uintptr(w), uintptr(h), 8, uintptr(w*4), cg.space(), cgPremultipliedLast|cgByteOrder32Big)
	if ctx == 0 {
		return nil, false
	}
	cg.draw(ctx, cgRect{W: float64(w), H: float64(h)}, uintptr(cgImage))
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := range h {
		for x := range w {
			if src[y*w*4+x*4+3] != 0 {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		return nil, false
	}
	gw, gh := maxX-minX+1, maxY-minY+1

	// The glyph, scaled into the box and centered.
	dst := make([]byte, px*px*4)
	dctx := cg.bitmap(unsafe.Pointer(&dst[0]), uintptr(px), uintptr(px), 8, uintptr(px*4), cg.space(), cgPremultipliedLast|cgByteOrder32Big)
	if dctx == 0 {
		return nil, false
	}
	const inset = 0.85
	scale := math.Min(inset*float64(px)/float64(gw), inset*float64(px)/float64(gh))
	if natural {
		// rectangle.on.rectangle, of the widest, spans 2.43 times the point
		// size in the image AppKit draws: it, and so every other, fits.
		scale = math.Min(scale, 0.92/2.43)
	}
	dw, dh := float64(gw)*scale, float64(gh)*scale
	// The image holds the glyph inside alignment margins: scale the whole
	// image so that the glyph, and not the margins, fills the box.
	cg.draw(dctx, cgRect{
		X: (float64(px)-dw)/2 - float64(minX)*scale,
		Y: (float64(px)-dh)/2 - float64(minY)*scale,
		W: float64(w) * scale,
		H: float64(h) * scale,
	}, uintptr(cgImage))
	runtime.KeepAlive(src)

	out := image.NewRGBA(image.Rect(0, 0, px, px))
	for i := range px * px {
		a := dst[i*4+3]
		out.Pix[i*4+0], out.Pix[i*4+1], out.Pix[i*4+2], out.Pix[i*4+3] = a, a, a, a
	}
	return out, true
}

// tint colors coverage, whose alpha holds the shape, in col.
func tint(coverage *image.RGBA, col ui.Color) *image.RGBA {
	out := image.NewRGBA(coverage.Rect)
	for i := range len(coverage.Pix) / 4 {
		a := uint16(coverage.Pix[i*4+3]) * uint16(col.A) / 255
		out.Pix[i*4+0] = uint8(uint16(col.R) * a / 255)
		out.Pix[i*4+1] = uint8(uint16(col.G) * a / 255)
		out.Pix[i*4+2] = uint8(uint16(col.B) * a / 255)
		out.Pix[i*4+3] = uint8(a)
	}
	return out
}

// nsString makes an NSString for s. The bytes must outlive the message
// the caller sends.
func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return objc.Send[objc.ID](objc.ID(objc.GetClass("NSString")), objc.RegisterName("stringWithUTF8String:"), unsafe.Pointer(&b[0]))
}
