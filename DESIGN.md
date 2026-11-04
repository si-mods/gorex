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
| Frosted glass: the window behind, blurred and tinted | `frost.go`, `paintFrost(p, r, blur, radius, tint, flat)` | A panel with the Glass setting off, as the palette's (blur 24, radius 16, `k.panelGlass`). A backdrop dims with a `Background` instead, as the palette's does (`k.backdrop`); a new dialog, popover or floating panel uses `glassPanel` ([Glass](#glass)). |
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

## Glass

GoRex's windows are of Liquid Glass, as macOS 26's: the desktop shows
through them, and what floats over the window floats as glass. The
Glass setting (Settings, Appearance; `noGlass` in settings.json) turns
it off, on by default, and with it off every layer paints as it did
before. Three things make it up:

| Glass | Code | Use it for |
|---|---|---|
| The window's material: what is behind the window, frosted by the system | `glass.go`, `glassVibrancy`, `WindowOptions.Vibrancy`, `Window.SetVibrancy` | The windows, main and Settings. `VibrancyGlass` on macOS (from 26; before, MyGo shows the popover material), `VibrancyMica` on Windows 11, none on Linux. |
| Layers over the page: the system's glass, else MyGo's | `glass.go`, `glassPanel(e, flat)`, `paneGlass{face, flat}`, `glass_other.go` | Popovers, dialogs and the palette's panel: `ui.SystemGlass`, with `paneGlass` (MyGo's `glass.Glass` on a GPU off macOS) where the system draws none. |
| The system's glass in an element: AppKit's `NSGlassEffectView` under its box | `glass.go`, `glassPill`, `ui.SystemGlass{Tint, Fallback}` | The panes, tiled and floating, tinted by their face (`glassTint`), with `faceMaterial` and `paneGlass` where the system draws none. The active tab's pill, in the sidebar, the tab strip and Settings, as Rex's: no tint dark, `pillTint` light; `faceMaterial{k.pillFace}` where the system draws none (before macOS 26, Windows, Linux, tests). |
| Frost: the window behind, blurred and tinted | `frost.go`, `paintFrost` | A panel with the glass setting off, as the palette's, whose backdrop only dims it. |

- **Ask the window whether its glass shows.** `windowGlass(c)` is the
  setting and `c.Vibrancy()`: the system draws no material on Linux,
  before Windows 11 22H2, in a Windows window created without one, nor
  in `ui.NewTester`. Only then does the root go clear
  (`rootBackground`), the background paint as a wash (`paintBackground`
  at `backgroundAlpha`, 0.3 of the gradients, which keeps the window's
  and the workspace's colors), and the terminals' face fade by the
  Terminal opacity setting (`terminalOpacity`, 0.3 to 1, 0.8 by default;
  in `cardFace`); terminal panes are the system's glass, tinted by that
  face at 0.4 (`glassTint`; a configured theme's whole). Browser panes
  keep their face.
- **macOS has only the system's glass.** Every glass layer there is
  `ui.SystemGlass`; `paneGlass`, its fallback, paints flat on macOS (before
  macOS 26), and is MyGo's glass only on Windows and Linux. MyGo's glass
  is imported only in `glass_other.go` (`!darwin`), so macOS builds cannot
  paint it.
- **MyGo's glass is the GPU's.** Off macOS `paneGlass` paints `glass.Glass`
  where `p.Accelerated()`, and its flat face elsewhere, as `paintFrost`
  does: on the CPU the glass runs slowly, and the tests see the old
  fills. The palette's backdrop only dims (`k.backdrop`), so that the
  glass is the panel's alone and blurs only what it covers: a blur over
  the whole window reads as a smeared window, not as glass. A new
  popover, dialog or floating panel takes `glassPanel(panel, k.panel)`
  in place of `Background(k.panel)`, with its radius and border as
  before.
- **Over the window's glass, the system's glass.** `glass.Glass` reads
  only what the page painted: under the clear sidebar and strip that is
  nothing, and it turns dark. Glass over them is `ui.SystemGlass`, which
  the system draws from what is behind the window. The page leaves a
  hole for it, so draw nothing of your own (insets, rims) over it, but
  a pane's 1px border, which lies on its edge.
- **Hover as Rex's, the same shape.** A hovered tab is a flat face
  (`hoverFace`, `k.tabHover`: white 0.10 dark, black 0.05 light), no
  glass, with the pill's capsule radius (`pillRadius`), so that hovered
  and active rows have one shape.
- **macOS windows are transparent.** The window's glass shows the
  desktop only through a window that is not opaque
  (`glassTransparent`, `WindowOptions.Transparent`); the Windows
  backdrops need none.
- **Live, and on Windows at creation.** Turning the setting sets every
  open window's material (`applyGlass`). Windows shows a material only
  in a window created with one: a window opened with the setting off
  shows its glass once opened again.
- **Not on everything.** The title bar, tab strip and sidebar paint no
  faces of their own, so the window's glass shows through them; only
  the chosen tab's pill is glass, the system's. Cards, rows and controls keep their
  colors, which read on the glass in both appearances.
