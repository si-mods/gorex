---
name: WEB-005
description: "The web client's panels and menus that float over the terminals are liquid glass: a tint, ink and density that follow the terminal under them, and a rim lens in Chromium. Use when you change the find bar, the update or loading panels, the upload panel, the selection or context menus, the floating dock, or the Glass tint settings in web/."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# WEB-005: Liquid glass over the terminals

Status: built (#1048).

This doc decides the material of the web client's surfaces that float over the terminal canvas. It extends [`REF-007`](REF-007-web-client.md), sections Themes and Settings. The desktop and Rex Go use the system's glass already (`DESIGN.md`, Glass); this doc changes only `web/`.

## Problem

The surfaces over the terminals were a flat panel (`--panel`, 78 % to 82 % opaque) with a 18 px to 24 px blur, the same in every place. Over a busy terminal the blur smeared the text into grey, and over a light terminal in the dark scheme the panel was a dark block. The find bar had no blur. Three tokens that the find bar, the palette and the floating dock use (`--fg`, `--ui`, `--bg`) were not defined, so their declarations were invalid: the find bar and the dock took the body's 13 px, not their 12 px and 11 px.

## Goal

- The seven surfaces below show the terminal through them, sharp enough to see that it is text, and keep their text at 4.5:1 or more at the default tint.
- In the light scheme, a surface over a dark part of the terminal turns to dark glass with light ink, and back, without flicker.
- Settings › Appearance has one Glass tint for the light scheme and one for the dark scheme.
- `--fg`, `--ui` and `--bg` are gone or defined.

## Non-goals

- Panels over the flat page (`.theme-panel`, `.corner-menu`, `.notice-card`, `.boot-card`) and dialogs over a dimming backdrop (`dialog.settings`, `dialog.confirm`, `dialog.viewer`, `.palette`) keep their frosted panel. A dimmed backdrop has no texture for glass to show.
- No change of markup, spacing, radii, fonts, icons, copy or layout. A surface changes its background, its backdrop filter, its box shadow and its text colour only. Its border keeps its width and becomes clear.

## Design

### Behavior

| Surface | Selector | Rim lens and colour split |
| --- | --- | --- |
| A new version is ready | `.update` | yes |
| A part of the page loads | `.chunk-load` | yes |
| A file uploads to a pane | `.pane-upload` | yes |
| Find in a terminal | `.find-bar` | yes |
| The selection's menu | `.select-menu` | no: a menu that opens and closes as its own layer |
| A right click's menu | `.context-menu` | no: the same |
| A minimized floating pane | `.float-dock-btn` | no: under 32 px tall |

The user sees the terminal through each surface, blurred by 2 px, with more saturation. Its text is dark on light glass, or light on dark glass. Over a busy part of the terminal the tint is denser. A hovered dock button is about 14 % denser. A hovered menu row is white at 42 % on light glass, white at 76 % when it is chosen (its submenu is open), with a white hairline. On dark glass the row is white at 12 % to 24 %.

With "Reduce transparency" or a forced-colours mode, each surface is solid, in its tint at full opacity, with no backdrop filter and no inner light.

### Model

There are three states. Each is one set of `--glass-*` tokens in `web/index.html`. `glassmath.ts`'s `materials` holds the same numbers for the tests.

| State | When | Tint | Alpha, thin to dense | Backdrop filter | Ink |
| --- | --- | --- | --- | --- | --- |
| Light glass | Light scheme, light backdrop | white | 0.06 to 0.22 | `blur(2px) saturate(180%) brightness(1.06)` | black 86 %, `rgb(60 60 67)` 70 % for secondary text, `multiply` |
| Dark glass | Light scheme, dark backdrop | `rgb(16 18 22)` | 0.10 to 0.62 | `blur(2px) saturate(170%) brightness(.85)` | white 94 %, `rgb(235 235 245)` 64 %, `plus-lighter` |
| Dark scheme | Dark scheme | `rgb(20 20 24)` | 0.16 to 0.90 | `blur(2px) saturate(160%) brightness(.8)` | as dark glass |

- **Edge.** A box shadow of five parts in every state, so that it glides: on light glass, white inset highlights at the top left and the bottom right, a 0.5 px white inner line, a 0.5 px dark outline and a soft drop shadow. Dark glass has a faint white top line and a darker outline. The dark scheme has the light glass's edge at about a third of the strength, with a darker outline and shadow.
- **Inner light.** A `::before` layer under the content: a radial white highlight from the top left (30 %, clear by 55 %), soft inset glows at the rim, brighter at the top and the left, and a little shade at the bottom. It has full strength on light glass and a third on dark glass. None of the seven surfaces had a `::before`, so no markup changes.
- **Ink.** The surface sets `--text`, `--muted`, `--hover` and `--panel-edge` to the glass's ink, secondary ink, row highlight and hairline, so the rows and controls inside follow it. Text leaves take `mix-blend-mode: multiply` on light glass and `plus-lighter` on dark glass. Icons are opaque, in the ink's colour, and fade with `opacity`. Focus rings, disabled states and danger colours keep their tokens.
- **Density.** `glass.ts` reads a 5 by 3 grid of points under the surface. It drops the 2 darkest and the 2 brightest, and averages the rest. Luminance is (0.299 R + 0.587 G + 0.114 B) / 255. Texture is the brightest sample's luminance less the darkest's. Chroma is the mean's largest channel less its smallest, over 255. Density = 0.55 × closeness of the luminance to the ink's + 0.7 × texture + 0.3 × chroma, from 0 to 1. The alpha moves from thin to dense with it.
- **Ink choice.** The dark scheme always takes light ink. The light scheme takes light ink under a luminance of 0.45 and dark ink over 0.55. Between, the ink stays as it was. Without a reading yet, the light scheme takes the ink it showed last (`gorex.glassInk` in `localStorage`, in `try`/`catch`).
- **Saturation kept.** Over a coloured backdrop the glass keeps less of its saturation: `max(0.45, 1 − 0.75 × chroma)` of it.
- **Glass tint.** Settings › Appearance has Glass tint, light and Glass tint, dark: 0 % to 150 %, in steps of 10 %, 100 % until set (`glassLight` and `glassDark` in `gorex.settings`). The scale multiplies the alpha. Under 100 % it also moves the brightness and saturation lift and the inner light towards none. At 0 % only the blur, the edge, the shadow and the rim lens stay. Each scheme keeps its own value. The tokens are registered with `@property`, so a change of scheme, of state or of the setting glides in 0.35 s.

### Reading the terminal

`glass.ts` reads the pixels of the terminal's canvas (`canvas.screen` in `pane.ts`), which is of the page's own origin.

- When the surface is wholly over one terminal canvas, one `drawImage` copies the region under it, not smoothed, into a 20 by 6 probe canvas: 4 by 2 points in each cell of the 5 by 3 grid. A cell's colour is the mean of its 8 points. The texture is the spread of all 120 points: 15 points miss the thin strokes of glyphs too often, and a smoothed copy averages them away.
- Else, each cell is the first painted background under its centre (`document.elementsFromPoint`, past clear elements). Over a part of a terminal, that is the terminal's background.
- One `getImageData` of the probe reads it all. When the read throws, the surface takes the theme's ink and a dense tint.

It reads again when:

1. a surface comes into the page, is shown, or moves (a `MutationObserver` on the surface's `hidden` and `style`; its place is compared, so its own style changes do nothing);
2. the scheme changes (`data-scheme` on `<html>`);
3. a terminal draws (`glassRedrawn`, from `TerminalView.redraw`). This read waits at least 1 s from the last one, then for an idle moment (`requestIdleCallback`). When no surface is shown, `glassRedrawn` returns at once, so typing costs nothing.

### Rim lens

On the four panels, the backdrop filter ends with `url(#gorex-lens-N)`. Each surface has its own `<svg>` with its own filter.

- The rim is `min(44 px, short side / 3.5)`, and at least 4 px. A point at a distance d inside the edge looks inward by 1.1 × rim × (1 − d / rim)^2.6. The map follows the surface's `border-radius`: in a corner the distance and the direction are to the arc.
- The map is a PNG (`displacementMap`): R bends x, G bends y, 128 is none. The filter is `feImage` (the map), then `feDisplacementMap` at scale 2 × the largest offset.
- The colour split takes three displacements: red 15 % more, green as is, blue 15 % less. An `feColorMatrix` keeps one channel of each, and two `feBlend mode="screen"` put them together.
- The filter goes on only after the map's image has decoded. The maps are kept by radius, rim and size (24 at most). When a surface changes size, its map stretches to the new size at once, and the right map comes 200 ms after the size settles.
- No lens on a surface under 32 px, or with `will-change`.
- The lens shows only where `CSS.supports('backdrop-filter', 'url(#a)')` is true, in Chromium. Firefox does not show an SVG filter there. Safari parses it but drops the whole backdrop filter, so the lens is off there too.

### Code

- `web/index.html`: the `--glass-*` tokens and their `@property` rules, the surfaces' rule, the inner light, the ink's blend, the row highlight, and the reduced-transparency and forced-colours rules. `--ui` is defined as the body's family. `var(--fg)` became `var(--text)`, and `var(--bg)` became `var(--hover)`, as `.sheet-field`'s.
- `web/src/glassmath.ts`: the reading, the ink, the density, the materials, the contrast model and the displacement map, with no DOM.
- `web/src/glass.ts`: `startGlass`, `glassRedrawn`, `applyGlassTint`, the sampling and the lens filters.
- `web/src/prefs.ts`, `web/src/settings.ts`, `web/src/main.ts` (`applyPrefs("glass")`), `web/src/pane.ts` (the redraw hook), `web/locales/*.json`.

## Alternatives

- **Derive the ink from the terminal theme's background only.** It is cheaper, but it cannot see a busy region, a program's own colours or a light status line in a dark theme. The reading costs one read of 120 pixels at most once a second while a surface shows, so we read.
- **The lens on the menus and the dock buttons.** The menus open and close in a frame. A map per opening would cost more than it shows. The dock buttons are under 32 px, where a rim of 4 px to 9 px bends nothing that the eye sees.
- **Raise the ink's opacity for contrast.** Denser tint keeps the ink's tone, so we raised the dense alpha of the dark materials (dark glass to 0.62, the dark scheme to 0.90).

## Tests

- `web/test/glass.test.ts`: the luminance, the trimmed mean, the texture and the chroma; the ink and its hysteresis; the density; the saturation kept; the tint scale; the rim width, the rim offset and the displacement map (the middle is neutral, each edge bends inward, the corners outside the arc are neutral).
- Contrast: the same file models the glass (backdrop filter, tint, inner light at its brightest) and the ink's blend. It checks 4.5:1 for the primary ink at the default tint over black, Gruvbox Dark, a busy Gruvbox Dark (7 of 15 points on text), white with Window set to Dark, a busy Gruvbox Light, red, and grey at 0.45, 0.5 and 0.55 with both inks. The contrast is to the glass over the reading's mean. One glyph's pixel under a 2 px blur can still be brighter. The secondary ink on light glass (`rgb(60 60 67)` at 70 %, multiplied) reaches about 4.4:1 at most even over white, so it is for secondary text only.
- Screenshots: a headless Chromium, before and after, in the light and the dark scheme, over a busy terminal, with the find bar, a loading panel, the selection's menu and a context menu.
- `docs/invariants.md`: no item is touched.
