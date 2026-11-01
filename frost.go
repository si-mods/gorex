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
