# Design

How GoRex's UI is drawn, for anyone adding to it. Read this before you
build a new surface, overlay or visual state. GoRex's own drawn UI
(`style.go`, `view.go`, `tabs.go`, `host.go`) is the only style source: a new
window or page reuses its colors, cards, title bar and selected states, not
stock MyGo widgets.

## Layers

GoRex's design system has five layers. Each one is built from the one
before it, and a change starts at the lowest layer it touches.

| Layer | What it holds | Where |
|---|---|---|
| 1. Colors | Every color, in a light and a dark set | `style.go`, `colors` ([Colors](#colors)) |
| 2. Styles | Type sizes, spacing, corners, shadows, glass, shaders | `tokens.go`, `glass.go`, `frost.go`, `dim.go` ([Type](#type), [Spacing](#spacing), [Corners](#corners), [Shadows](#shadows)) |
| 3. Components | A control drawn the same way everywhere: a row, a field, a dot | [Components](#components) |
| 4. Patterns | The smallest unit that does a user's task: a dialog, a menu, a toast | [Patterns](#patterns) |
| 5. Decisions | Which of the above to use, and when not to | [Decisions](#decisions), and each section's "Where not" rules |

A value in UI code comes from a layer, not from a literal: a color from
`colors`, a type size, a radius or a shadow from `tokens.go`. Spacing is
the one literal allowed, on the grid ([Spacing](#spacing)).
`TestDesignTokens` (`tokens_test.go`) reads the root package's source
and fails on a literal outside these rules.

## Colors

`colors` in `style.go` holds every color of GoRex's own UI, once in
`lightColors` and once in `darkColors`; `colorsOf(c)` picks the set. A
new color is a new field in both sets, named by its role (`panelSel`,
`attention`), not by its hue. Brand marks and program colors
(`programs.go`) and the user's own colors (workspace colors, terminal
themes) are data, not part of this set.

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
| The window's material: what is behind the window, frosted by the system | `glass.go`, `glassVibrancy`, `WindowOptions.Vibrancy`, `Window.SetVibrancy` | The windows, main and Settings. `VibrancyGlass` on macOS (from 26; before, MyGo shows the popover material), `VibrancyGlass` on Windows 11 (Acrylic, which shows the desktop blurred), none on Linux. |
| Layers over the page: the system's glass, else MyGo's | `glass.go`, `glassPanel(e, flat)`, `paneGlass{face, flat}`, `glass_other.go` | Popovers, dialogs and the palette's panel: `ui.SystemGlass`, with `paneGlass` (MyGo's `glass.Glass` on a GPU off macOS) where the system draws none. |
| The system's glass in an element: AppKit's `NSGlassEffectView` under its box | `glass.go`, `glassPill`, `ui.SystemGlass{Tint, Fallback}` | The panes, tiled and floating, tinted by their face (`glassTint`), with `faceMaterial` and `paneGlass` where the system draws none. The active tab's pill, in the sidebar, the tab strip and Settings, as Rex's: no tint dark, `pillTint` light; `faceMaterial{k.pillFace}` where the system draws none (before macOS 26, Windows, Linux, tests). On the sidebar's current project face (`groupFace`) it is `liftedPill`: `pillLift` where the system draws none, `liftShadow` and a hairline. |
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

## Corners

Corners follow Apple's three shapes (WWDC25, "Get to know the new design
system"), and no radius is picked on its own:

- **Capsule**, half the height: the active and hovered tab, the tab
  strip's track, the sidebar's rows (`pillRadius`).
- **Concentric**, the container's radius less its border and padding
  (`concentric(outer, inset)`): what sits in a corner of a rounded
  container, as a menu's, popover's or the palette's rows, and the panes:
  `cardR` is the window's radius less the gap, and every pane, tiled or
  floating, takes it. The window's radius is the one macOS reports
  (`Window.CornerRadius`, read once a frame), and `windowR` (16, macOS
  27's) elsewhere and where macOS reports 0, as in full screen: Windows
  and Linux keep their panes' rounding (`cardR` 8) in every window state.
- **Fixed**, a radius of its own: what sits away from any corner, as a
  toast, a badge or a button in a dialog's padding. A fixed radius is one
  of five (`tokens.go`):

  | Token | Radius | Use it for |
  |---|---|---|
  | `radiusS` | 6 | Key caps, badges, breadcrumb steps, icon tiles up to 24 |
  | `radiusM` | 10 | Icon tiles from 30 to 40, buttons GoRex draws, cards inside a panel, lists in a sheet, the radius of a row with the glass setting off (`pillRadius`) |
  | `radiusL` | 12 | Menus, toasts and banners, icon tiles from 44 |
  | `radiusXL` | 14 | Popovers, Settings groups and cards |
  | `radiusXXL` | 16 | Dialogs and sheets |

- **A circle is a capsule.** A dot, a swatch or a round mark takes
  `capsule(h)`, half its height, not a radius picked to look round.

## Type

GoRex's UI text takes one of eight sizes (`tokens.go`). The ramp
follows macOS's, with two half steps that GoRex's dense rows use. A
new size is a change to this table, not a literal.

| Token | Size | Use it for |
|---|---|---|
| `fontCaption` | 11 | Sidebar section headers (upper case), notes beside a row, badges |
| `fontFootnote` | 11.5 | Secondary lines under a title, host details, hints |
| `fontSubhead` | 12 | Asides and key hints in the palette and menus, group headers |
| `fontCallout` | 12.5 | Label and value rows in cards and popovers |
| `fontBody` | 13 | Body text, rows, buttons, text fields |
| `fontTitle` | 15 | Dialog and popover titles, the palette's input |
| `fontCodeSmall` | 22 | A fingerprint shown to be compared |
| `fontCode` | 28 | A pairing code shown to be typed |

- **Weight carries the hierarchy within a size.** A title is 600 or
  700, a label 500, the rest the regular weight; do not step a size up
  to stand out.
- **Terminal text is not on the ramp.** The terminal's font size
  (`termFont.Size`) is the user's, and what follows it (the tab badge's
  `badgeSize`, sizes relative to `termFont.Size`) keeps its own rule.

## Spacing

Spacing (`Gap`, `Padding`, `Margin`) is on a 2px grid, with 4px steps
from 8 up: 0, 2, 4, 6, 8, 10, 12, 14, 16, 20, 24, 32. An odd value is
off the grid. Row heights are not spacing; they are named constants
next to their views (`tabH`, `sideRowH`, `titleH`). A pane's insets
(`paneCard`) are not spacing either: they size the terminal's grid, and
a change to them changes every pane's columns.

- **Insets by layer.** A dialog's padding is 20, a popover's 14, a
  menu's 6; rows inside them are padded 8 to 14 on the sides.
- **Gaps by kin.** Lines of one item stack 2 apart; an icon and its
  label, 6 to 8; items of a list, 8 to 12; groups, 16 to 20.

## Shadows

A raised layer takes one of four shadows (`tokens.go`), by how far it
floats over the page. Each is a function that adds the shadow to an
element, as `dialogShadow(e)`.

| Token | Shadow | Use it for |
|---|---|---|
| `raisedShadow` | (0, 1, 2) `k.shadow` | A card or group lying on the page: Settings groups |
| `toastShadow` | (0, 8, 24) `k.shadowFocused` | Toasts and banners over a pane |
| `popoverShadow` | (0, 12, 32) `k.shadowFocused` | Menus, popovers and pickers |
| `dialogShadow` | (0, 24, 60, -8) black 0.28, and (0, 2, 6) black 0.06 | Dialogs and sheets over a backdrop |

- **Not on panes.** A pane card's shadows (`paneCard`) and its lift while
  it is dragged (`floatCell`, animated) are part of the pane, and the
  shadows a painter draws (`tabs.go`) are part of their drawing; none is
  a token. The active tab's pill has shadows of its own (`glassPill`),
  as part of the pill.

## Components

A component is drawn by one function or one set of tokens, and every
use of it looks the same. Before you draw one of these, use the one
that exists.

| Component | Built from | Code |
|---|---|---|
| Sidebar and tab rows | `sideRowH`, `pillRadius`, `glassPill`, `hoverFace`, `fontBody` | `sidebar.go`, `projects_sidebar.go`, `tabs.go` |
| Text field | `field`, `fieldBorder`, `fontBody`, heights 22 and 28 | `fields.go` |
| Status dot | 7 or 8 square, `capsule`, `busy`, `attention`, `exited`, `danger` | `hostDot`, `view.go` |
| Key cap | `fontSubhead`, `radiusS`, `k.hover` | `commands.go`, `settingsview.go` |
| Icon tile | a square of `k.hover`, `radiusS`, `radiusM` or `radiusL` by size, an icon from `assets/icons/` | `host.go`, `settingsview.go` |
| Program tile | `tileW`×`tileH` rounded card, `drawTile`; the front tile bounces while a pane needs the user, `tileBounces` at iOS's timings | `tabs.go` |
| Chip | a capsule row 28 high, `pillRadius` | `projectpicker.go`, `spaces.go` |

## Patterns

A pattern is the smallest unit that does a user's task. A new one reuses
the parts of the one it is nearest to.

| Pattern | Parts | Example |
|---|---|---|
| Dialog | `glassPanel(k.panel)`, `radiusXXL`, padding 20, `fontTitle` title, `dialogShadow`, a backdrop of `k.backdrop` | `teleport.go`, `workspaceview.go` |
| Popover | `glassPanel(k.panel)`, `radiusXL`, padding 14, `popoverShadow` | `host.go` |
| Menu | `glassPanel(k.panel)`, `radiusL`, padding 6, rows `concentric(radiusL, 6)` | `menus.go`, `docs/drawn-menus.md` |
| Toast and banner | `k.panel`, `radiusL`, `toastShadow` | `view.go`, `upload.go` |
| Palette | frost or glass, `fontTitle` input, rows `concentric` | `commands.go` |
| Settings page | the window background, groups of `raisedShadow` and `radiusXL` | `settingsview.go` |

## Decisions

- **A dialog or a popover?** A choice the user must make before going
  on, over a backdrop, is a dialog. A choice they can leave by clicking
  away, next to what opened it, is a popover.
- **A toast or a banner?** A toast tells of something done and goes. A
  banner stays while its state lasts, as a pane that cannot attach.
- **Do** take the nearest token. **Don't** add a size, radius or shadow
  for one view: change the table, for every view.
- **Do** tell states apart by color and weight. **Don't** tell them apart
  by a size half a step bigger.
- **Do** dim what is unavailable with `dimmed`. **Don't** dim with
  `Opacity` where color is not the meaning ([Shaders](#where-not-to-use-them)).

## Icons

Icons are SVGs: lucide ones from `assets/icons/`, and brand marks from
`assets/brands/` (`programs.go` on the desktop, `icon(name)` in
`web/src/hosts.ts` on the web). A new icon is never a Unicode character
(✓ ✕ ⌄ +) standing in for one: when a lucide icon is missing, copy its SVG
into `assets/icons/`. One bitmap stands beside them: the settings
sidebar's server card shows the app's own icon (`resources/icon.png`,
the prism the iOS app carries too), embedded and decoded by `appIcon`.
