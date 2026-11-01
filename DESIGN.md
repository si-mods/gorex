# Design

How GoRex's UI is drawn, for anyone adding to it. Read this before you
build a new surface, overlay or visual state.

## Shaders

GoRex draws its glass, its window background and its inactive controls
with MyGo's GPU shaders (`github.com/egoist/mygo/ui/shader`, docs:
<https://mygo.egoist.dev/docs/ui/shaders>). New UI uses them in the same
places and in the same way, and does not add a second way to do the same
thing.

### Where they are

| Look | Code | Use it for |
|---|---|---|
| Frosted glass: the window behind, blurred and tinted | `frost.go`, `paintFrost(p, r, blur, radius, tint, flat)` | Overlays: the palette's backdrop (blur 10, `k.backdrop`) and panel (blur 24, radius 16, `k.panelGlass`). A new dialog, popover or floating panel uses `paintFrost` from its `Draw`, without a `Background`. |
| Window background: the gradients, dithered | `style.go`, `paintBackground` | Every full-window page (the main view, Settings). A new page paints it the same way, and does not paint gradients of its own. |
| Inactive: grey and faded | `dim.go`, `dimmed(e, opacity)` | Disabled or in-flight controls (a dragged tab, a disabled nav button). Use it in place of `e.Opacity(x)` for "not available now". |

### Rules

- **The GPU, or the old look.** A shader runs only where `p.Accelerated()`
  is true. Elsewhere — the CPU renderer, WARP or llvmpipe, and
  `ui.NewTester` — the code paints the flat look it replaces: a fill, the
  gradients, or the plain opacity. There a shader runs interpreted at a few
  microseconds a pixel, seconds for a window, and the tests would crawl.
- **Effects decide as they paint.** `Element.Effect` is set as the view is
  built, when there is no painter to ask. Wrap the shader's effect in a
  small `ui.Effect` whose `PaintEffect` checks `p.Accelerated()`, as
  `dimEffect` does, rather than calling `shader.Effect` directly.
- **Compile once.** Shaders are package variables made with
  `shader.MustCompile`. Values set every frame go through `NewValues` and
  `Uniform`, made once (see `backgroundOptions`), not a new map each frame.
- **Premultiplied out, straight in.** A `ui.Color` uniform reaches the
  shader as a vec4 not premultiplied; `Fragment` and `ColorEffect` return
  premultiplied colors, and `ColorEffect` gets its content premultiplied.
  Unpremultiply before changing hue or luminance, and premultiply again.
- **Match the old pixels.** A shader that replaces a drawing does the same
  math: `LinearGradient` mixes in sRGB, not linear light, so the shader
  does too.
- **Backdrop shaders draw rectangles.** One that reads `backdropSample`
  draws with `Draw` (with `Options.Radius` for rounded corners), not
  `Fill`. The backdrop holds the Ghostty terminals, so glass over a pane
  shows its text blurred.

### Where not to use them

- **Not on terminal or browser panes.** An effect renders its element into
  a bitmap every frame it paints: a pane's worth, at the terminal's frame
  rate, and web views do not show through it.
- **Not on long-lived large elements.** An effect costs a bitmap the size
  of its element for as long as it is set. Use it on small or short-lived
  ones; paint a large surface with `Draw` instead.
- **Not where color is the meaning.** Greying hides what color tells: the
  workspace dots in the strip are told apart by color, so a workspace in
  another window keeps `Opacity(0.5)` rather than `dimmed`.
- **Not beyond the box without a margin.** An effect reads its content as
  transparent outside the element's box, so its shadows are cut off unless
  the effect's margin (`MaxSampleOffset`, `EffectMargin`) covers them.

### Testing

- Test the CPU path in `ui.NewTester`: that the flat look still paints.
- A shader that replaces a drawing can be checked against it, interpreted
  in a small tester (`TestBackgroundShader` in `shader_test.go` draws
  100×80 and compares within 3/255).
- The GPU look is checked in a real window on the Mac mini with
  cua-driver: screenshot the state over colored terminal output, so that
  blur and tint can be seen.
