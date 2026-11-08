package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// frost is the shader of the palette's frosted glass: what the window drew
// behind, blurred, with Tint laid over it. Tint arrives not premultiplied,
// and the shader returns the color premultiplied.
var frost = shader.MustCompile(`package main

var Tint vec4

func Fragment(dst vec4, pos vec2) vec4 {
	b := backdropSample(pos)
	return b*(1-Tint.a) + vec4(Tint.rgb*Tint.a, Tint.a)
}
`)

// paintFrost paints r as frosted glass: the window behind it blurred by
// blur DIPs and tinted, its corners rounded by radius. Where the frame is
// not drawn on a GPU, as on the CPU renderer, where a shader runs slowly,
// it fills r with the flat color flat instead.
func paintFrost(p *ui.Painter, r ui.Rect, blur, radius float32, tint, flat ui.Color) {
	if !p.Accelerated() {
		p.Fill(r, flat, radius)
		return
	}
	frost.Draw(p, r, shader.Options{
		Uniforms:     map[string]any{"Tint": tint},
		BackdropBlur: blur,
		Radius:       radius,
	})
}

// frostPanel gives panel, a floating panel whose corners are rounded by
// radius, its face and its border, as the palette's: the system's glass
// under the glass setting, frosted glass without it. The frost is painted
// with Draw, after the element's own border: the border goes over it.
func frostPanel(panel ui.Element, k *colors, radius float32) ui.Element {
	if glassOn() {
		return glassPanel(panel.Border(1, k.panelBorder), k.panel)
	}
	return panel.Draw(func(p *ui.Painter, r ui.Rect) {
		paintFrost(p, r, 24, radius, k.panelGlass, k.panel)
		p.Stroke(r, k.panelBorder, radius, 1)
	})
}
