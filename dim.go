package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// dim is the shader of an inactive element: its pixels turned toward their
// luminance by Grey (1 is full grey), and faded by Fade. The content
// arrives premultiplied, and the shader returns it premultiplied.
var dim = shader.MustCompile(`package main

var Grey, Fade float

func ColorEffect(pos vec2, color vec4) vec4 {
	if color.a <= 0 {
		return color
	}
	rgb := color.rgb / color.a
	l := dot(rgb, vec3(0.2126, 0.7152, 0.0722))
	rgb = mix(rgb, vec3(l), Grey)
	return vec4(rgb*color.a, color.a) * Fade
}
`)

// dimExtraFade is how much more of an element's color a dimmed element
// keeps than the plain opacity would: grey reads as inactive by itself, so
// it needs less fade.
const dimExtraFade = 0.15

// dimmed shows e as inactive. On a GPU it turns the element grey and fades
// it to plain+dimExtraFade; where the frame is not drawn on one, as on the
// CPU renderer, where a shader runs slowly, it fades it to plain, as
// Opacity does. The frame being drawn is known only as it paints, so the
// effect decides then.
func dimmed(e ui.Element, plain float32) {
	e.Effect(dimEffect{
		gpu:   dim.Effect(shader.Options{Uniforms: map[string]any{"Grey": 1.0, "Fade": min(plain+dimExtraFade, 1)}}),
		plain: plain,
	})
}

// dimEffect is the effect of dimmed: the shader where the frame is
// accelerated, the plain fade where not.
type dimEffect struct {
	gpu   shader.Effect
	plain float32
}

func (dimEffect) EffectMargin() ui.Point { return ui.Point{} }

func (d dimEffect) PaintEffect(p *ui.Painter, box ui.Rect, content *ui.Bitmap, inset ui.Point) {
	if p.Accelerated() {
		d.gpu.PaintEffect(p, box, content, inset)
		return
	}
	s := p.Scale()
	p.ImageRegion(content, ui.Rect{X: inset.X, Y: inset.Y, W: box.W * s, H: box.H * s}, box,
		ui.ImageOptions{Tint: ui.RGBA(255, 255, 255, d.plain)})
}
