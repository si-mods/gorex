---
name: DSK-012
description: "Theme packs get shader slots (window background, a moving layer and the faces of roles), metric seeds (corner scale, border width, UI font, shadow), a glass key and five new roles, so that a pack can rebuild a rich web theme such as Blue Window or Potion Workshop. Use when you add shaders or metrics to theme manifests, change the theme shader API, or port a theme from another app."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# DSK-012: Theme shaders and metrics

Status: phases 1 and 2 built (#1096, #1105). Phases 3 to 5 not built.

This doc decides how a theme pack changes more than colors and still images. It adds three kinds of shader slots, a set of metric seeds, a glass key and five roles. It extends [DSK-008](DSK-008-theme.md) (the manifest and the face resolver) and [DSK-010](DSK-010-theme-ambience.md) (assets, layers, the still rule). The probes are two themes of `freestylefly/codex-themes`, a theme app for the Codex desktop app: "Blue Window" and "Potion Workshop". The goal is to rebuild both as GoRex theme packs. Replaces: [DSK-008](DSK-008-theme.md), the glass non-goal, the glass alternative and the Security rule "A theme manifest is data". Also replaces: [DSK-010](DSK-010-theme-ambience.md), the non-goal "any code in a pack", the first Security item and the asset caps that the Assets section changes.

## Problem

A GoRex theme can set 31 color seeds, 7 sidebar roles, the `sidebar.*` metrics, a Ghostty theme name, glows, two still images and a brand row (`internal/theme/theme.go`, `Parse`). DSK-010 phases 2 and 3 add particles, sprites, a mascot and a ticker.

The probe themes need more. Their engine injects 175 KB of CSS and 63 KB of JavaScript into the host page (`assets/inject/dream-skin.css`, `renderer-inject.js`). A theme there is a palette of 11 colors, a layout, a hero image, a wallpaper, six effect strengths and five metric choices (`assets/presets/*/theme.json`). This table compares each need with GoRex now:

| Need | Blue Window | Potion Workshop | GoRex now |
| --- | --- | --- | --- |
| Palette, light and dark | 11 colors, dark equal to light | 11 colors for each | Seeds. A dark set equal to the light set gives a light-only theme |
| Vertical gradient on a face | Title bar, rows, buttons, pane header | No | Roles are flat colors |
| Grain or a paper texture | No | `noise: 0.38` | No layer |
| Wallpaper that fills the window | 1586×992 px at 0.2 | No | `ambience.images` anchors to a corner. Still images stop at 1024 px |
| Hero illustration, opaque | Mascot, `contain` | 1586×992 px, `cover`, the core of the theme | Images stop at opacity 0.25 |
| Motes that float | No | `particles: 0.12`, `float: 0.12` | DSK-010 phase 2, not built |
| Corner radius | `none` | `sm` | Constants in `tokens.go` |
| Border width | 1 px blue lines | Thin | Fixed |
| UI font | Tahoma, then system | `rounded` | Fixed |
| Shadow size | `sm` | `md`, an offset with no blur on notes | Fixed in `raisedShadow`, `toastShadow`, `popoverShadow` |
| Faces of the title bar, the sidebar, the pane header, the buttons | Yes | Yes | No role for them |
| Mascot and status lines | Mascot, `statusText`, `quote` | `statusText`, `quote` | DSK-010 phase 3, not built |

Each extra need is one of four kinds:

1. **Pixels**: the color of each point of an area, from its position, the time and images. Gradients, grain, textures, wallpaper, hero and motes are pixels.
2. **Metrics**: sizes that change the layout or the clip. Radius, border width, font and shadow size are metrics.
3. **Structure**: new parts of the window, such as a toolbar or a right rail.
4. **Behavior**: what a click does. The mascot is behavior.

A new manifest key for each pixel effect does not scale. DSK-010 shows this: glows, still images, particles and sprites are four formats with four parsers and four sets of caps. Each next theme asks for one more.

MyGo already has a general answer for pixels. `ui/shader` compiles a fragment shader at run time from source (`shader.Compile`). The language is MyGo's fork of Ebitengine's Kage: Go syntax, GLSL built-ins. The same source runs on Metal, Direct3D 11, OpenGL, WebGL 2 and the CPU. A shader reads up to four images. MyGo's fork accepts loops whose bounds are run-time expressions (`internal/shader/stmt.go`, "dynamic loops"). A theme shader thus needs its own loop check (see Checks at load). GoRex draws its window background (`background` in `style.go`), its dimmed controls (`dim.go`) and its frost (`frost.go`) with it now.

## Goal

- A theme pack can give a shader for the window background, a shader for a moving layer, and a shader for each of up to 8 faces.
- A theme pack can set the corner scale, the border width, the UI font and the shadow size.
- A theme pack can turn the glass on or off, over the user's Glass setting.
- Text on a face that a shader paints keeps 4.5:1 contrast. GoRex checks this at load, not the reviewer.
- A shader that costs too much, does not compile or fails the contrast check does not draw. The flat colors of the theme draw instead, with a warning.
- The CPU renderer never runs a theme shader.
- An older build loads a new pack as its colors, with warnings, and does not fail.
- The two probe themes can be rebuilt as packs. Each feature that is not rebuilt has a reason in this doc.

## Non-goals

- Structure. A theme does not add a toolbar, a rail or a panel. Blue Window's toolbar, friend list and right rail are not rebuilt. Potion Workshop's rotated notes are not rebuilt. A pane is a terminal, and a rotated terminal does not read.
- A shader over the terminal text, as Ghostty's `custom-shader` does. That is a choice of the user, not of a theme. A later TERM or DSK doc can add a `terminal.shader` setting.
- Font files in a pack. A font parser is a large attack surface, and most font licenses forbid redistribution in a theme. A theme names installed families only.
- The mascot and the ticker. DSK-010 phase 3 builds them without change.
- iOS and the web client. They can read the same keys later.

## Design

### Behavior

The user installs and selects a pack as DSK-008 says. With a pack that uses this doc, the user sees:

1. The corners, borders, shadows and UI font of the theme, everywhere in the chrome.
2. A window background that the theme draws: for example a wallpaper that fills the window, a paper grain, or a hero picture in the free area.
3. Faces that the theme draws: for example the title bar, the sidebar, the chosen row, the pane header and the buttons as XP gradients, or as paper with a stitched edge.
4. Motes or bubbles that move outside the panes. They stop by the still rule of DSK-010.

Text on each face stays readable. A theme shader that fails a check shows nothing new: GoRex draws the flat colors of the theme, and the Extensions page shows the warning under the theme.

### Manifest

Four new optional keys, `shaders`, `metrics`, `glass` and `description`, and five new roles, four of them faces:

```json
{
  "name": "Blue Window",
  "seeds": { "light": { "accent": "#2876c8" }, "dark": { "accent": "#2876c8" } },
  "roles": { "titleBar.background": "#2876c8", "pane.border": "#2876c880" },
  "assets": { "wall": "wallpaper.png", "bar": "xp-bar.kage", "row": "xp-row.kage", "bg": "wall.kage" },
  "shaders": {
    "api": 1,
    "background": { "source": "bg", "images": ["wall"], "params": { "Opacity": 0.2 } },
    "faces": {
      "titleBar.background": { "source": "bar", "params": { "Top": "#73b9ed", "Bottom": "#2876c8" } },
      "sideBar.tabActiveBackground": { "source": "row" }
    }
  },
  "metrics": { "radius": 0.2, "border": 1, "font": ["Tahoma", "Segoe UI", "system"], "shadow": { "scale": 0.5 } },
  "glass": false
}
```

- `description` is a line about the theme: a string of at most 160 characters, cleaned of control and bidirectional characters (`cleanText`), each change with a warning. The Extensions page shows it under the theme's name. A value that is not a string drops with a warning.
- `shaders.api` is the version of the shader API (see Shader API). This doc defines version 1. A build that does not know the version drops `shaders` with a warning.
- A slot names its shader source as an asset of kind `.kage` (see Assets). `images` names up to 4 image assets, which the shader reads as `image0` to `image3`. `params` sets the other uniforms of the shader: a number, a list of numbers, or a color as a role value (a hex string or a `{"light", "dark"}` pair).
- `shaders.background` and `shaders.ambient` take one slot each. `shaders.faces` maps a face role to a slot. A role that is not a face is dropped with a warning.
- A face that has a shader must also have a flat color: the role value of the theme, or of the built-in palette. The flat color selects the text ladder (DSK-008 pairing), and draws on the CPU renderer and after a failed check. The built-in palette has a flat color for the five sidebar faces only: a slot of `titleBar.background`, `sideBar.background` or `paneHeader.background` with no role in the manifest is dropped with a warning.
- Phase 2 drops `shaders.ambient` with a warning ("not drawn by this build"), until phase 3.

### Shader API

A theme shader is a Kage file with a `Fragment` entry point, as `ui/shader` documents. GoRex sets these uniforms by name. A shader declares only the ones it uses:

| Uniform | Type | Value |
| --- | --- | --- |
| `Time` | `float` | Seconds since the theme applied, modulo 3600. It does not change while ambience is still (DSK-010 still rule) |
| `Dark` | `float` | 1 in the dark appearance, 0 in the light |
| `Origin` | `vec2` | The top left corner of the drawn rectangle in the window, in DIPs |
| `Window` | `vec2` | The size of the window, in DIPs |
| `Seed<Name>` | `vec4` | The resolved seed, not premultiplied, for example `SeedAccent`, `SeedBgTop` |

`rectSize()`, `pixelScale()` and the image functions come from MyGo. `Origin` and `Window` let a shader draw one picture across the bands of the free region and across faces. Each other exported uniform is a param. A param that the manifest does not set is zero, with a warning. A shader returns its color premultiplied, as every MyGo shader does.

Version 1 is a promise to pack authors. A later version adds uniforms. It does not rename or remove one. A change that must break a shader takes a new `api` number, and the build reads both numbers for one year.

### Slots

| Slot | Layer (DSK-010) | Area | Moves |
| --- | --- | --- | --- |
| `background` | 1 | The whole window | Only if the shader reads `Time` |
| `ambient` | 3 | The free region: the window minus the pane cards and floats, in bands | Yes |
| `faces.<role>` | The face's own | The rectangle of the face, with its radius | Only if the shader reads `Time` |

- **background** replaces the built-in `background` shader. Under the glass setting GoRex draws it at `backgroundAlpha`, as the built-in one: it wraps the theme's `Fragment` in its own, which multiplies the color by the uniform `GorexAlpha`. A theme shader may not declare `GorexAlpha` or a function `themeFragment`. Glows and still images of DSK-010 draw over it, as now. With Ambience Off, the built-in background draws, as with the other layers of the theme.
- **ambient** draws once for each band of the free region. In the sidebar band it draws with the sidebar cap of DSK-010 (alpha at most 0.12). DSK-010 phase 2 draws its `particles` and `sprites` with two built-in ambient shaders. A pack with its own `ambient` slot draws that slot after them.
- **faces** draw where GoRex paints the face. Where the system draws a glass pill (`glassPill`, macOS 26 with the glass setting on), the system pill stays and the shader does not draw.
- `button.background` takes no shader. MyGo's stock widgets paint that face in the accent of their theme (`ui.Theme.Accent`), and GoRex has no hook to draw into it. The parser drops the slot with a warning.

Phase 2 draws the faces with these details:

- One function, `paintFace(e, k, role, face)`, gives an element its face: a material (`ui.Material`) that runs the shader on a GPU and fills the flat color elsewhere, or the flat color as the background. `faceMaterialOf(role, face)` is the same material for the fallback of a glass pill.
- The shader draws only where the face is the theme's flat color of the role. A color that the user's `sidebar.colorCustomizations` or a space's tint sets over the role draws flat.
- The faces that take a shader: the title bar (`titleBar`, the sidebar's head), the sidebar column, a pane's header, the current project's group face, the chosen row (`sidebarChosen`, `glassPill` and `liftedPill` without glass, and `pillLift` as the fallback of `liftedPill`), a hovered row of the sidebar and of the tab strip, and the tab strip's track.
- `Time` passes only while a window moves the shaders: at most 0.1 s from one moving frame to the next, so that it goes on where it stopped. It starts at 0 when the theme applies. `Origin` is the corner of the drawn rectangle in the painter's DIPs; `Window` is the size of the window whose background painted last.
- The still rule asks the same conditions as DSK-010 (`themeShaderMoves`): Ambience Moving, Animation on, no Reduce Motion, the window active and not hidden, not on battery, and a GPU.

A shader that reads `Time` asks for frames at 30 a second while ambience moves, as DSK-010 phase 2 does. While ambience is still, `Time` stops and GoRex asks for no frame.

### Checks at load

GoRex checks each shader on a goroutine after the assets load. Each check that fails drops the shader with a warning. The theme then draws the flat colors for that slot.

1. **Compile**: `shader.Compile`, with a recover. The source is at most 16 KiB. GoRex parses the source first: the entry point must be `Fragment`, not an effect entry point. A source that names `backdropAt` or `backdropSample` fails, as MyGo finds it (`readsBackdrop`). A theme shader thus cannot read what is under it, so it cannot read terminal text.
2. **Loops**: each `for` loop has constant bounds, and all loops of the shader run at most 256 times in total for a pixel. MyGo counts them at compile (MyGo change M2, `(*shader.Shader).LoopRuns`, si-mods/mygo#45): a loop of 8 in a loop of 4 counts 4 + 32 = 36. A loop whose bound is not a constant, whose body writes its counter, whose float counter stalls, or a recursion fails. The cost check runs only after this check, so it cannot meet a loop without end.
3. **Uniforms**: each uniform is in the API table or in `params`, with the right type.
4. **Cost**: GoRex draws the shader into 64×64 px with `ui.Render`, which runs the CPU interpreter, three times, and keeps the shortest time. It does the same for the built-in `background` shader. A ratio above 8 fails. The ratio counts the work of a pixel and does not depend much on the machine. The CPU interpreter runs the same program as the GPU, so a shader eight times the built-in background costs about eight backgrounds on the GPU.
5. **Contrast of a face**: GoRex draws the face shader at 96×32 px with `ui.Render` at each `Time`, `Origin` and `Window` sample of the list below. At each pixel, the text of the ladder that the flat color selects must keep 4.5:1, and `textMuted` 3:1. A pixel with alpha under 1 counts as laid over `panel`, as `surface` counts a face.
6. **Contrast of the sidebar band**: GoRex draws the background shader at 1280×800 DIPs, at 1/8 scale, at each `Time` sample of the list below. In the columns of the sidebar, `text` must keep 4.5:1 and `textMuted` 3:1 at each pixel.

Checks 4 to 6 run once for each theme load. They run on the CPU, on the goroutine that decodes the assets (`loadArt`), and the windows draw the flat colors until they are done. Phase 2 measured all six checks for a pack of one background shader and four face shaders (`BenchmarkThemeShaderChecks`): 230 to 520 ms on a Windows PC (Intel Core i9-12900), 3 rounds of 10 runs, with the samples below. This is over the 200 ms target; the windows draw the flat colors meanwhile, so it delays the shaders, not the window. The Mac is not measured.

Phase 2 builds the checks with these details:

- A failed check shows its warning on the Extensions page, under the theme in use, and in the log. The page names the slot in a localized line ("Theme shader not used: shaders.background", or "Theme shader note: …" for a slot that still draws), and the check's English detail follows it. The log is English.
- A panic in any check of a slot drops that slot with a warning.
- A shader can branch on `Time`, `Origin` and `Window`, so the checks draw several values of each. Check 4 draws each `Time` of 0, 1.7, 37, 600 and 3599 s with each `Window` of 64×64, 1280×800 and 3840×2160 (the uniform; the render stays 64×64 px), the `Origin` 0,0 and 900,500 in turn: once each, then the slowest the fastest of three. Check 5 draws `Time` 0, 1, 2, 3, 37, 600 and 3599 s, at origins 0,0, 900,500 and 200,40 and windows of 1280×800 and 3840×2160 in turn. Check 6 draws `Time` 0, 37 and 3599 s.
- Sampling does not prove every input: a shader can still be slow or unreadable at a value no sample hits. Two bounds hold for every input: its loops run at most 256 times a pixel (check 2), and its source is at most 16 KiB.
- The checks of a theme store their result only while that theme is still selected, under one lock with the theme's change, and a window draws a checked set only for the selected theme.
- Check 3: a uniform that no param sets is zero, with a warning; a param that the shader does not declare gives a warning. A uniform of the API table with another type, a `Seed<Name>` with no such seed, and a param that does not fit its uniform fail. A param may not set a uniform of the API table.
- Check 4 draws the built-in background with no glows.
- Check 5 reads a face only where text can sit: inside `faceTextInset`, 4 DIPs in from the left and right and 2 DIPs from the top and bottom. A face shader may thus draw an accent bar, an inner border, a stitched edge or a bottom line at the edge in any color. Inside, 4.5:1 and 3:1 hold at each pixel.
- Check 5 runs in both appearances, with the dark flat color and its ladder. A slot fails when either appearance fails.
- Check 6 composites a pixel with alpha under 1 over `bgBottom`, the window's root. It draws only the sidebar's columns (`sidebarWidthIn(1280)`, 160 px at 1/8), scaled by a transform: `ui.Render` at scale 1/8 runs the shader for each pixel of scale 1. When the theme's `sideBar.background` is opaque in an appearance, the text reads on that face, and the check skips the appearance.

### Metrics

`metrics` holds sizes. Each key is optional. A missing key keeps the built-in value.

| Key | Range | What it changes |
| --- | --- | --- |
| `radius` | 0 to 1, default 1 | Multiplies each radius token of `tokens.go` (`radiusS` to `radiusXXL`, `radiusTile`), each capsule (`capsule`, `pillRadius`) and the pane radius (`cardR`). `concentric` applies after the scale. The window's own corners stay the system's |
| `border` | 0, 1 or 2 DIP | The width of the border of a pane card, a panel and a field |
| `font` | A list of up to 4 family names, or the words `system`, `rounded` and `mono` | The family of the chrome text (`uiFont`). The first family installed wins. `system` always matches. Terminal text and `monoFamily` do not change |
| `shadow.scale` | 0 to 2, default 1 | Multiplies the offset and the blur of `raisedShadow`, `toastShadow`, `popoverShadow` and `dialogShadow` |
| `shadow.hard` | `true` or `false` | With `true`, the blur is 0, so a shadow is a hard offset, as paper on paper |

The type sizes of `DESIGN.md` stay. A theme changes the family, not the ramp. The row heights stay in the `sidebar` key of DSK-008.

Phase 1 builds the metrics with these details:

- `radius` also scales the corner of the stock widgets (`ui.Theme.Radius`: 9 in a window, 14 in the Settings window), so that a button is square when the panes are.
- `border` also sets the width of the border of a popover, a menu, a dialog and the frost (each border in `k.panelBorder`).
- `font`: the word `mono` is JetBrains Mono, which GoRex ships. A family that matches gets the system font after it (`uiFont`), for the characters that the family does not have.
- A program tile that GoRex draws once into an image for the hosts (`drawTileImage`) keeps the built-in corner: the image is cached for all themes.

`rounded` is the rounded design of the system font on macOS. MyGo has no API for it now (MyGo change M1). Off macOS, `rounded` falls to the next name of the list.

### Glass

The optional top-level key `glass` is `true` or `false`. With the key, the theme decides the glass, and the user's `noGlass` does not apply. With no key, the user's `noGlass` decides, as now.

- `glassOn` in `glass.go` returns the theme's value when the selected theme has one. Each caller of `glassOn` and `windowGlass` thus follows the theme with no other change.
- GoRex does not write the theme's value to `settings.json`. The user's `noGlass` stays in the file. When the user selects another theme or no theme, the user's own setting applies again.
- The Glass switch of the Settings page shows the value in use. While a theme decides it, the switch is off limits and shows "Set by the theme <name>". The user changes it by a change of theme.
- A theme change applies the glass at once with `applyGlass`, as the switch does now. On Windows, a window that opened with no material shows the glass only after it opens again (`DESIGN.md`, Glass). The window shows the flat background until then.
- The system's Reduce Transparency keeps the effect that it has now.

Both probes set `"glass": false`. Their backgrounds then draw opaque, not as a wash at `backgroundAlpha`.

### New roles

The face roles are the five faces that `surface` resolves now (`sideBar.activeGroupBackground`, `sideBar.activeGroupTabBackground`, `sideBar.trackBackground`, `sideBar.tabHoverBackground`, `sideBar.tabActiveBackground`) and the four new faces below. `shaders.faces` takes only these nine roles.

| Role | Kind | Face of |
| --- | --- | --- |
| `titleBar.background` | Face | The title bar row (`titleH`) across the window |
| `sideBar.background` | Face | The sidebar column, under its rows |
| `paneHeader.background` | Face | The header of a pane card (`headerColors`) |
| `button.background` | Face | A primary button, a chosen segment and a switch that is on: the faces that use `accent` now |
| `pane.border` | Color | The border of a pane card, now `cardBorder` |

The resolver pairs the text of each face as DSK-008 says. `button.background` uses `accentText` while its flat color is `accent`. When a theme sets another flat color, the ladder follows that color.

Under a theme the pairing guarantees contrast (`faceLadder` in `style.go`). A light-only theme sets its dark seeds equal to its light ones, so both of its ladders are dark text, and a dark face (a title bar of `#2876c8`) would get dark text at about 2.5:1. The resolver therefore takes, for the face laid over `panel`, the first of these ladders whose `text` reaches 4.5:1 and `textMuted` 3:1 on it:

1. The theme's ladder of the face's lightness.
2. The theme's other ladder.
3. The built-in ladder of the face's lightness (`lightColors` or `darkColors`).
4. The built-in other ladder.
5. Ink made for the face (`inkLadder`): `text` white or black, whichever contrasts more, and `textMuted` that ink laid over the face at the least alpha from 0.72 that keeps 3:1. Its text always reaches 4.5:1, so no face is left unreadable. On `#2876c8` the built-in dark ladder reaches only 4.2:1 (`#f2f2f7`), and the ink ladder, white, applies.

If none passes, the one that reads best wins. With no theme the resolver keeps the rule of DSK-008, so the built-in look does not change (`TestThemeNoneKeepsPalettes`). The face shader check (check 5) uses the same choice.

Phase 1 builds these roles with these details:

- The built-in palette has none of the five. An unset role draws nothing new.
- `button.background` sets the accent of the stock widgets' theme (`Accent`, its hover and pressed shades, `AccentText`). It thus colors the primary button, a switch that is on, and the other faces MyGo draws in the accent, such as a chosen radio. It does not color a chosen segment: MyGo draws that in the theme's `Background`, `surface` in GoRex. The focus ring, the selection and the menu highlight keep `accent`.
- `pane.border` is the border of a pane card with the focus and without it. The card's shadow shows the focus.
- A popover or a menu that opens from a face, as the space picker's from the title bar, lies on the window's panel. It keeps the window's text ladder, not the face's.
- `paneHeader.background` is the face of a terminal's and a note's header. A browser pane's header and a float's handle keep their faces.
- The user's `sidebar.colorCustomizations` take only the `sideBar.*` roles. The other roles are the theme's.

### Assets

DSK-010 caps stay, with these changes:

| Item | DSK-010 | This doc |
| --- | --- | --- |
| Formats | PNG, GIF, APNG, SVG | Adds `.kage` text files, 16 KiB each |
| Names | 16 | 24 |
| A still image that only shaders read | 1024 px on a side, 1 MiB | 2048 px on a side, 2 MiB |
| Pack | 4 MiB | 8 MiB |
| Decoded memory | 48 MiB | 48 MiB, no change |

An image that a still layer or the brand also reads keeps the DSK-010 caps, and decodes at the size the still layer needs; the shaders read that bitmap. An asset named `*.kage` must be UTF-8 text with no NUL byte.

A wallpaper that fills a 2560 px window at 1024 px looks soft. At 2048 px it is sharp enough at the opacity a wallpaper uses. The decoded cap holds the memory: three images of 2048×2048 px fill it.

### Code

- `internal/theme/theme.go`: parse `shaders`, `metrics` and `glass`, with caps and warnings. Add the five roles to the allowlist. Add `Kage` to the asset formats.
- `internal/theme/shaders.go` (new): the API table, the param parser and the uniform check.
- `themeshader.go` (new, root): compile, the checks at load, and the draw of each slot. A face draw goes through one function, `paintFace(e, k, role, face)`, which draws the shader on the GPU and the flat color elsewhere (see Slots).
- `style.go`: `paintBackground` draws the theme's background slot when one passed the checks. `surface` keeps its rule. The flat color still selects the ladder.
- `tokens.go`: the radius tokens and the shadow functions read the metrics of the palettes value (`palettes`). Call sites use `k.radius(radiusM)` and pass `k` to the shadow functions. `TestDesignTokens` checks that no call site uses a bare token. The metrics change with the theme, as the colors do, so a window reads one whole value.
- `menus.go` and each `uiFont` user: the family comes from the palettes value.
- DSK-010 phase 2: `ambience.go` draws particles and sprites as built-in ambient shaders with the same uniforms. Its manifest keys do not change.

## Port of the two probes

| Probe feature | Blue Window in GoRex | Potion Workshop in GoRex |
| --- | --- | --- |
| Palette | Seeds. Dark equals light | Seeds, light and dark |
| Gradients | `faces`: title bar, sidebar, chosen row, pane header, buttons | Not needed |
| Paper look | Not needed | `background`: grain and fibers. `faces`: chosen row and pane header as paper with a stitched edge and a torn edge in alpha |
| Wallpaper | `background` with `image0`, cover fit at 0.2 | Not needed |
| Hero | The mascot of DSK-010 phase 3 in the sidebar foot | `background` draws the picture in the free area, with focus 0.27, 0.56 as the probe. In a full tiled layout the panes cover most of it (see Limits) |
| Motes | Not needed | `ambient` with soft gold motes |
| Corners | `radius: 0.2` | `radius: 0.5` |
| Border | `border: 1`, `pane.border` blue | `border: 1`, brown at 26% |
| Font | `["Tahoma", "Segoe UI", "system"]` | `["rounded", "system"]` |
| Shadow | `scale: 0.5` | `scale: 1`, `hard: true` |
| Glass | `"glass": false` | `"glass": false` |
| Brand text | `brand.wordmark` | `brand.wordmark` |
| Status and quote | DSK-010 ticker | DSK-010 ticker |
| Toolbar, right rail, friend list | Not rebuilt (structure) | — |
| Rotated notes | — | Not rebuilt (structure) |

### Limits

- In a full tiled layout the panes cover most of the window. A background hero then shows in the gaps, the title bar, behind the sidebar at its cap, on an empty page and on the canvas. Potion Workshop thus looks closest to its preview on an empty page.
- A face that the system draws as glass does not take the face shader.
- Copy texts of the probes that name Codex (`CODEX 2007 · BLUE WINDOW`, the "Codex friends" heading) change to GoRex or to neutral text.

## Alternatives

- **GLSL, as Ghostty and Shadertoy use**: rejected. It needs a translator to MSL and HLSL (glslang and SPIRV-Cross, C++). MyGo already compiles Kage for each renderer and the CPU. A Shadertoy port to Kage is a short manual step.
- **A key for each effect (gradient roles, noise strength, cover wallpaper)**: rejected. It is the DSK-010 path. Each new theme adds keys, parsers and caps. One shader slot covers the pixel class once.
- **Shaders over the panes**: rejected, as DSK-010 rejects motion over the panes. Text under them does not read.
- **A shader that reads its backdrop**: rejected. Over a pane the backdrop holds terminal text. A theme has no need to read it.
- **Font files in a pack**: rejected (Non-goals).
- **Measure the cost on the GPU each frame**: rejected for phase 2. MyGo gives no GPU timer. The interpreter ratio is deterministic and testable. Phase 3 measures the real frame time once and writes it in its PR.
- **A per-theme budget that the user sets**: rejected. The Ambience setting (Moving, Still, Off) is already the control.

## Security

This doc changes one rule of DSK-008: "A theme manifest is data". After this doc, a pack holds data and shader programs. A shader program has these limits:

- It runs on the GPU or in the interpreter of MyGo. Kage has no file, network, clock or system call. Its only output is the color of its pixels.
- Its only inputs are the uniforms of the API table, its params and its own images. It cannot read the backdrop (check 1), so it cannot read terminal text or another app's pixels. It cannot read a file that the pack does not ship.
- Its loops have constant bounds and at most 256 runs a pixel (check 2). The cost check bounds the rest of the work of a pixel. A shader thus cannot hang the GPU by a loop. A slow driver can still be slow: the still rule and the Ambience setting stop each frame.
- The GPU driver compiles the code that MyGo generates. A driver bug can crash the app. The registry CI compiles and draws each listed shader on the renderers that its runners have (Direct3D 11 on Windows, Metal on the Mac runner, OpenGL on Linux). A local pack (`gorex pack install <folder>`) has no CI. That is the user's own risk, as with a local pack's images.
- A shader cannot show text, open a link, run a command or write a setting.

The rest of the DSK-008 and DSK-010 rules stay: no URL, no network at run time, the pinned `ref`, the asset name rules and the byte caps.

## Migration

- `shaders`, `metrics`, `glass` and the five roles are new optional keys with defaults. A manifest without them loads as now, with the same pixels (`TestThemeNoneKeepsPalettes`).
- An older build drops `shaders`, `metrics`, `glass` and `description` with an `unknown key` warning each. The user's Glass setting then applies. It drops the five role names with an `unknown role` warning each. It loads the colors and the DSK-010 layers. Its asset loader drops each asset that it cannot take, with a warning, and keeps the rest (`internal/theme/assets.go`): a `.kage` file (not a PNG, GIF or SVG), an image over 1024 px, an asset past 4 MiB for the pack, and a name past 16. It does not fail the pack.
- No settings key changes. `docs/irreversible.md` does not apply to the manifest keys. The shader API version 1 is a promise to pack authors (see Shader API).

## Tests

- **Parse**: `shaders` and `metrics` parse. A value out of range clamps, an unknown slot or role drops, a slot with no flat color takes the built-in one, and each gives a warning (`TestParseShaders`, `TestParseMetrics`).
- **Checks**: a shader that does not compile, has an effect entry point, reads its backdrop, has a loop whose runs are not constant or more than 256 loop runs, has a uniform of the wrong type, costs 9 times the background, or fails the face contrast, each drops with a warning, and the slot draws its flat color (`TestThemeShaderChecks`). A shader that branches on `Time > 4` or `Window.x > 200` to pass one sample is refused. A check of a theme the user left stores nothing, and a set of another theme draws nothing (`TestThemeShaderSwitch`). The param parser, the uniform check and the asset caps have their own tests (`TestParseShaders`, `TestShaderUniforms`, `TestShaderAssets`).
- **Contrast**: a light gradient face in a dark window gets dark text. A face shader that crosses the flip point fails (`TestFaceShaderContrast`). A light-only theme with a `#2876c8` title bar gets white text, and its gradient face shader passes (`TestFaceLadderLightOnly`). An accent bar 3 px wide at the left edge passes; one where text sits fails (`TestFaceShaderInset`).
- **Description**: `description` parses, is cleaned and cut to 160 characters (`TestParseDescription`), and shows under the theme's name (`TestThemeDescription`).
- **Background**: with no theme shader, the window background is byte-exact as now (`TestBackgroundShader`, and `TestThemeBackgroundShader` with a theme that has only face shaders). With one, the pixel at a known point has its color (`TestThemeBackgroundShader`).
- **CPU renderer**: on the CPU renderer, no theme shader draws, and each slot draws its flat color (`TestThemeShaderCPU`).
- **Still**: a background or face shader that reads `Time` asks for no frame under each condition of the DSK-010 still rule, and its `Time` stands (`TestThemeShaderStill`).
- **Metrics**: `radius: 0` gives square panes, rows and menus. `concentric` never goes below 0. `border` changes the pane card border. `shadow.hard` gives a blur of 0. A font list with no installed family uses the system font (`TestThemeMetrics`).
- **Tokens**: `TestDesignTokens` fails on a bare radius token in UI code.
- **Glass**: a theme with `"glass": false` turns the glass off over `noGlass: false`, and `"glass": true` turns it on over `noGlass: true`. `settings.json` keeps the user's value. With no theme, the user's value applies again. The Settings switch is off limits while a theme decides (`TestThemeGlass`).
- **Roles**: each new face role resolves with its ladder (`TestThemeLayers`, extended).
- **Old build**: a manifest of this doc parses under the DSK-010 parser with warnings and the same colors (`TestParseDrops`, extended).
- **Probes**: screenshots of both packs through the `GOREX_DEBUG` hook, light and dark, tiled, canvas and empty page, glass on and off, beside the probe's `preview.png`.

No item in `docs/invariants.md` covers the theme.

## Phases

1. **Metrics, roles and glass** (M): `metrics`, the five roles with flat colors, `k.radius` and the shadow functions, the font list, the `glass` key. MyGo M1 for `rounded`. Blue Window gets its square corners, borders and font.
2. **Shader runtime** (L): MyGo M2 (loop bounds), `.kage` assets, the API version 1, the `background` and `faces` slots, the checks at load, the new image caps, the CPU fallback. Measure the load checks on a Mac and a Windows PC. Built; the Windows measurement is in Checks at load.
3. **Ambient slot** (M): the `ambient` slot over the free region. It merges with DSK-010 phase 2: particles and sprites become built-in ambient shaders. Measure the frame cost as DSK-010 says.
4. **Mascot and ticker**: DSK-010 phase 3, no change.
5. **Two packs** (S each): `si-mods/gorex-theme-blue-window` and `si-mods/gorex-theme-potion-workshop`, own repos, listed in the registry with a pinned `ref`. They use the probe names and new art (User decisions, 1 and 3). Screenshots beside the probe previews.

## User decisions

The user decided the four open questions of the proposal on 2026-11-24, and corrected decisions 1 and 3 the same day:

1. **License of the probe art**: the packs ship new art in the style of the probes, made for GoRex. They ship no image of `freestylefly/codex-themes`. The packs carry the shaders, colors, metrics and the new art.
2. **Glass**: a theme can turn the glass on or off, over the user's setting (see Glass). This replaces the DSK-008 rule.
3. **Pack names**: the packs keep the probe names, "Blue Window" and "Potion Workshop". The pack description and the `NOTICE` credit the probe themes of `codex-themes` and their repo URL. The packs do not say that the author endorses them.
4. **Local packs with shaders**: a local pack (`gorex pack install <folder>`) can carry shaders from phase 2. The load checks apply to it. The registry CI does not.
