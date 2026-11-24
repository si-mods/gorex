---
name: DSK-010
description: "A theme pack can carry images and declare ambience: background glows, a watermark, particles, swimming sprites, a mascot with quotes and a popup, a brand row and a quote ticker, as data with caps. Use when you add image assets or motion to theme packs, change where ambience draws, or change the ambience and mascot settings."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# DSK-010: Theme assets and ambience

Status: accepted. Phase 1 built (#1083). The user decided its four questions (see User decisions).

This doc decides how a theme pack carries images and motion, and how GoRex draws them. It extends the manifest of [DSK-008](DSK-008-theme.md) and the theme pack kind of the registry. It builds on the optional `terminal` key that DSK-008 phase 4 adds. It does not change that key. The probe is a real theme for another app: `dafy-whale-theme` ("蓝色大肥鱼", a web theme for DeepSeek Harness). The user wants all of its features in GoRex. Where the framework cannot do a feature, this doc extends the framework.

## Problem

A theme manifest has colors only: `name`, `seeds`, `roles`, and soon `terminal` (`internal/theme/theme.go`, `Parse`). `Parse` drops every other key with a warning. `theme.Install` copies one file, `theme.json`, to `<config>/themes/<id>.json`. A pack thus cannot carry an image. `paintBackground` in `style.go` paints a fixed four-stop gradient and one tint. A theme can change its colors, not its shape. No code draws a theme's picture, motion, mascot or text.

The probe theme is a JavaScript plugin. It injects CSS and React components into the host page. It has these features:

| Id | Feature | How the probe builds it |
| --- | --- | --- |
| F1 | Ocean palette, light and dark | 13 CSS variables: brand, three backgrounds, overlay, two borders, two label colors, three status colors, sidebar fill |
| F2 | Sea-surface glow | 3 CSS radial gradients at fixed places (1100×620 px at 88%/-8%, 900×560 px at -6%/108%, 520×340 px at center), 14% to 30% alpha, `mix-blend-mode: soft-light` |
| F3 | Whale watermark | `whale_side.png` (222×340 px, 120 KB), bottom right, 74% of the window height, opacity 0.10 |
| F4 | Rising bubbles | 14 `span` circles, 5 to 17 px, radial white-to-blue fill, CSS `translateY` from the bottom to -108vh in 12 to 28 s, fade in and out |
| F5 | Fish school | 5 `img` sprites: `fish_idle.png` and `fish_happy.png` (768×832 px, about 500 KB each, shown 42 to 56 px wide), `whale_front.png` (255×340 px, shown 170 px wide). CSS `translateX` across the window in 64 to 120 s, some in reverse, opacity 0.10 to 0.16, a drop shadow on the big one |
| F6 | Mascot | Fixed at the bottom right, 108 px wide, z-index 30, over the content. Bobs 12 px and ±2° in 3.6 s. Grows 7% on hover. Animated GIFs for idle and waving. A caption under it. Falls back to an emoji when the image fails |
| F7 | Mascot click | Waving pose and a speech bubble with a random line of 6, for 3 s |
| F8 | Mascot double click | A popup with `meme_dafeiyu.png` (1254×1254 px, 1.5 MB), at most 300×230 px, for 3 s |
| F9 | Mascot hide | A ✕ shows on hover. A click hides the mascot until the page reloads |
| F10 | Quote ticker | A pill above the chat input. It shows one of 7 lines and changes every 9 s with a pop-in |
| F11 | Brand | CSS hides the host's logo and paints a whale logo (an SVG mask in the text color) and the wordmark "蓝色大肥鱼" |
| F12 | Ambience toggle | A button in the sidebar foot hides and shows F2 to F5. The state is in memory only |
| F13 | Selection and scrollbar colors | CSS `::selection` and scrollbar thumbs in the brand color |
| F14 | Asset server | A host route serves 9 allowed files. An install script fetches 3 GIFs from a third repo at install time |

## Goal

- A theme pack can carry images in a closed set of formats, under size caps. GoRex decodes them with limits and never fetches anything at runtime.
- A manifest can declare ambience layers as data: glows, still images, particles and sprites. It contains no code.
- A manifest can declare a mascot, a brand row and a quote ticker, with a fixed set of interactions.
- Nothing that a theme declares covers terminal text or takes the keyboard focus.
- Motion stops when the window is not active, is hidden, or when the user or the OS asks for less motion. The user can turn ambience and the mascot off.
- An older build loads a new pack as colors only, with warnings, and does not fail.
- Each probe feature has a row in the capability matrix with a decision.

## Non-goals

- Scripts, CSS or any code in a pack. DSK-008 Security stays true: a manifest is data.
- Remote images, or any network access at runtime. At install, GoRex clones only the repo that the registry lists, at its pinned commit.
- A third-party pack in the gorex repo or in `gorex-registry`. GoRex holds the framework. A third-party pack lives in its own repo, and the registry only lists it.
- Blend modes. A glow paints with normal alpha over the gradient. Soft light is not in the shader.
- Motion over the panes. Ambience paints below them.
- Sound, video files and animated WebP.
- iOS and the web client. They can read the same manifest later, in their own docs.
- Terminal colors. The `terminal` key of DSK-008 owns them. Terminal selection colors stay in the Ghostty theme.

## Design

### Behavior

The user installs a theme pack on the Extensions page or with `gorex pack install <id>`, and selects it, as DSK-008 says. With an ambience theme, the user sees:

1. The palette of the theme, as now.
2. Soft glows in the window gradient and a faint watermark image in a corner. They do not move.
3. Bubbles that rise and sprites that swim across the window. They move only outside the pane cards. In a tiled layout they show behind the sidebar at a lower alpha, and in the title bar and the gaps. On the canvas and on an empty page they show in the free area.
4. A brand row at the top of the sidebar: an icon and a wordmark.
5. A mascot in the sidebar foot, above the Settings button. It bobs. A click shows a speech bubble with a random quote. A double click shows a popup image. A ✕ on hover hides it until the theme changes or GoRex restarts. The terminal keeps the keyboard focus after each click.
6. A one-line quote ticker under the mascot. It changes every few seconds with a fade.
7. An ambience button in the sidebar foot, shown only when the theme has ambience. It turns ambience off and on.

The Window Theme group of the Extensions page shows two controls under the selected theme: **Ambience** (Moving, Still, Off) and **Mascot** (On, Off). The command palette has **Toggle Theme Ambience**.

### Manifest

The new keys are optional. A manifest without them is a DSK-008 manifest:

```json
{
  "name": "Dafy Whale",
  "seeds": { "light": { "accent": "#3b62f6" }, "dark": { "accent": "#6c8cff" } },
  "terminal": { "light": "Ayu Light", "dark": "Ayu" },
  "assets": {
    "whale": "whale_front.png",
    "side": "whale_side.png",
    "fish": "fish.png",
    "logo": "whale.svg",
    "wave": { "file": "whale_wave.png", "frames": 8, "columns": 4, "fps": 10 }
  },
  "ambience": {
    "glows": [{ "x": 0.88, "y": -0.08, "rx": 0.55, "ry": 0.5, "color": { "light": "#3b62f64d", "dark": "#6c8cff33" } }],
    "images": [{ "asset": "side", "anchor": "bottomRight", "height": 0.74, "opacity": 0.1 }],
    "particles": [{ "shape": "bubble", "count": 14, "size": [5, 17], "seconds": [12, 28], "color": "#78beff" }],
    "sprites": [{ "asset": "fish", "y": 0.14, "height": 56, "seconds": 64, "direction": "right", "opacity": 0.16 }]
  },
  "mascot": { "idle": "whale", "active": "wave", "height": 96, "caption": "大肥鱼", "quotes": ["咕噜咕噜～"], "popup": "whale" },
  "brand": { "icon": "logo", "wordmark": "蓝色大肥鱼" },
  "ticker": { "lines": ["鱼在深海里替您思考，请勿投喂。"], "seconds": 9 }
}
```

- `assets` maps a short name to a file of the pack's `assets/` folder. A value is a file name, or a sprite sheet object: `file`, `frames`, `columns` and `fps`. The other keys refer to assets by name only.
- A color is a hex string or a `{"light", "dark"}` pair, as a role value in DSK-008.
- A position or a size that is a fraction (0 to 1) is a fraction of the window. A size above 1 is in DIPs.
- An `anchor` is one of `topLeft`, `topRight`, `bottomLeft`, `bottomRight` and `center`.
- A `direction` is `left`, `right` or `up`. A sprite that swims left shows mirrored (`ImageOptions.FlipX`), unless it sets `"mirror": false`.
- A `mascot.idle` or `mascot.active` asset can be a still PNG, an animated GIF or APNG, or a sprite sheet. `active` shows for 3 s after a click. `popup` names an image for the double click. With no `popup`, a double click shows a quote.
- Text values are plain strings. GoRex shows them as they are, in one line, and ellipsizes them.

### Caps

The parser clamps a number to its range, drops an entry past a count cap, and logs a warning for each. The registry validator refuses a pack that is over a cap.

| Item | Cap |
| --- | --- |
| Manifest | 64 KiB, as now |
| `assets` | 16 names. A name has only letters, digits, `-` and `_`. A file name obeys `theme.ValidName` and has no path separator |
| Formats | PNG, GIF, APNG for images and sprite sheets. SVG for `brand.icon` only |
| File size | 1 MiB for each file, 4 MiB for the pack |
| Still image | 1024 px on each side. GoRex decodes it to at most 2 times its largest shown size, times the screen scale (`ui.DecodeBitmapScaled`) |
| Animated image | 512 px on each side, 64 frames, 16 MiB decoded. A sprite sheet has at most 64 frames |
| Decoded memory | 48 MiB for each theme |
| `glows` | 4. Alpha at most 0.4 |
| `images` | 2. Opacity at most 0.25 |
| `particles` | 2 layers, 40 particles in total. Size 2 to 32 DIP. 4 to 120 s for each trip. Alpha at most 0.6 |
| `sprites` | 8. Height 16 to 200 DIP. 8 to 600 s for each trip. Opacity at most 0.3 |
| Mascot | Height 48 to 128 DIP. 32 quotes |
| Ticker | 32 lines. 6 to 120 s for each line |
| Text | 80 characters for a quote or a line, 24 for a wordmark or a caption. Control characters and bidirectional overrides are removed |

### Layers

Each element draws in one layer. Nothing that the theme declares draws over a pane:

| Order | Layer | What draws there | Over a translucent pane |
| --- | --- | --- | --- |
| 1 | Window background | The gradient and the glows, in the one `background` shader | Shows through, as the gradient does now |
| 2 | Still images | The watermark | Shows through. It does not move |
| 3 | Moving layers | Particles and sprites | Clipped out. They never show under a pane |
| 4 | Panes | Cards, terminals, browsers, notes | Not changed |
| 5 | Sidebar | Brand row, mascot, ticker, ambience button | The sidebar does not overlap a pane |
| 6 | Overlay | The speech bubble and the popup, for 3 s | A popup can cover a pane for 3 s after a click. It takes no focus |

A terminal under the glass setting has a translucent background (`termOpacity`, 0.8 until set). Motion under terminal text makes the text hard to read. Layer 3 thus paints only in the **free region**: the window minus the bounds of each pane card and each float. The free region includes the sidebar. `Painter.Clip` clips to a rectangle only, so GoRex splits the free region into rectangles (horizontal bands) and draws each layer once for each band. The corners of a card are rounded, so a small wedge at each corner stays without motion. Nobody sees it.

**Behind the sidebar.** The sidebar rows have text on the window gradient. In the sidebar band, layer 3 paints at a lower alpha, so that the rows stay readable:

- The sidebar cap: GoRex multiplies the alpha of each sprite and particle in the sidebar band so that it is at most **0.12**. Outside the sidebar, the caps of the Caps table apply (0.3 for a sprite, 0.6 for a particle).
- The contrast check: for each appearance, `applyTheme` takes the worst case. That is a pixel of the most contrasting asset color at the sidebar cap, over `bgTop` and over `bgBottom`. `text` must keep a contrast ratio of 4.5:1 and `textMuted` 3:1 over it. If a theme fails the check, GoRex lowers the sidebar cap for that theme until it passes, and logs a warning. The cap never goes up.
- The brand row, the mascot, the ticker and each row face (chosen, hovered, current project) paint over layer 3, as now. No face changes.
- Ambience Still or Off applies to the sidebar band too.

### Motion and the budget

Moving layers and the mascot run in one of two states. **Moving** paints at 30 frames a second (`Painter.AnimationFrameRate(30)`). **Still** paints each layer at its last position and asks for no frame. Ambience is still when any of these is true:

- The user sets Ambience to Still.
- The Animation setting is Off, or the OS asks for less motion (`motion(c)` is nil).
- The window is not active (`Context.WindowActive`). The working dots stop on the same rule (`breathing`).
- The window is hidden or covered (`Context.WindowHidden`). MyGo already holds the frames then.
- The computer runs on its battery (`mygo.Power.IsOnBattery`). Each animation freezes at its last frame: the moving layers, the bob of the mascot, its GIF or sprite sheet, and the fade of the ticker. The ticker keeps its line and does not change it.
- The renderer is the CPU renderer (`Painter.Accelerated` is false).

With Ambience Off, layers 1 to 3 paint as with no theme ambience, and the ticker does not show. The mascot has its own setting.

A paint-only frame repaints the whole window on the GPU (`repaint` in MyGo's `ui/runtime.go`). The working dots already do this at 30 frames a second. The budget for ambience is:

| Measure | Budget |
| --- | --- |
| Frame rate | 30 frames a second at most. A GIF or a sprite sheet plays at its own rate, at most 30 |
| CPU time to paint the ambience | 0.5 ms for each frame, with all caps used |
| Draw calls | One instanced shader call for all particles (`shader.DrawInstances`). One `ImageRegion` call for each sprite and each band |
| Idle CPU of the app, window active, no output, ambience moving | 3% of one core more than with ambience Still |
| Idle CPU, window not active | No difference from a theme with no ambience: no frame, no timer |
| Ticker | One `Context.After` timer. None while the window is not active or hidden |

Phase 2 measures these numbers on a Windows PC and on a Mac and writes them in its PR. If the idle CPU is over the budget, phase 2 lowers the rate to 20 frames a second. If that is not sufficient, the MyGo change M2 (see Code) comes first.

### Mascot

The mascot is an element in the sidebar foot, not an overlay. It thus never covers a pane and needs no pass-through. When the sidebar is hidden, or the tabs are at the top, the mascot and the ticker do not show. Nothing takes their place (User decisions, 1).

- **Draw**: `ui.Image` for a still PNG. `ui.Video` with `ui.NewGIFDecoder` or `ui.NewAPNGDecoder` for an animated image. A sprite sheet draws its current frame with `Painter.ImageRegion`. The bob is a `Pose` of 4 DIP and 2° on a 3.6 s loop. Hover grows the mascot by 4%. In the still state, the mascot does not bob, a video pauses (`VideoPlayer.Pause`) and a sprite sheet shows frame 0.
- **Click** (`Element.Clicked`): show `active` for 3 s, and a speech bubble with a random quote above the mascot, for 3 s.
- **Double click** (`Element.DoubleClicked`): show the `popup` image in a bubble, at most 300×240 DIP, for 3 s.
- **✕**: shows on hover (`Element.Hovered`). A click hides the mascot until the theme changes or the app restarts. The Mascot setting hides it for good.
- **Bubbles**: `ui.TooltipBase` anchored to the mascot. A tooltip takes no focus and lets the pointer through. A bubble goes after 3 s, at the next click, or when the window is not active.
- **Focus**: the mascot has no focusable role. A click on it does not move the keyboard focus from the terminal.
- **Fallback**: an asset that does not load shows no mascot. No emoji.
- **Accessibility**: the mascot has the label of its caption, and the bubbles have the role of a status message.

### Brand row and ticker

- **Brand row**: a row of 28 DIP at the top of the sidebar list, above the space picker. An SVG icon draws in the text color of the sidebar (`ui.Icon` with `TextColor`), as the probe's CSS mask does. A PNG icon draws as it is. The wordmark uses the sidebar's title type size and ellipsizes.
- **Ticker**: one line in the sidebar foot, under the mascot, in `textMuted`. A new line comes in with a 150 ms fade (`ElementTransition`), or at once with less motion. A long line ellipsizes. It does not scroll.

### Model

- `internal/theme`: `Manifest` gets `Assets`, `Ambience`, `Mascot`, `Brand` and `Ticker`. Each is a plain struct of validated values. The parser applies the caps.
- `internal/theme`: `Assets` is the loader. It opens each file with `os.Lstat` (a regular file, no link), reads at most the cap, and checks the header before it decodes: `image.DecodeConfig` for a PNG, the logical screen and a count of the image blocks for a GIF, the `acTL` chunk for an APNG. It decodes on a goroutine, recovers a panic, and drops a bad asset with a warning. The rest of the theme loads.
- `style.go`: the `background` shader gets 4 glow uniforms (center, radii, color). With no glows, the shader paints the same pixels as now (`TestBackgroundShader`).
- `ambience.go` (new): the layer state, the free region, the particle and sprite paths, and the still rule. A path is a pure function of the layer, the window size and the time, so a test can check a position at a time.
- `settings.go`: two keys, `appearance.ambience` (`""` for Moving, `"still"`, `"off"`) and `appearance.mascot` (`""` for On, `"off"`).

### Code

- `internal/theme/theme.go`: parse the new keys with caps. Keep `name`, `seeds`, `roles` and `terminal` as they are.
- `internal/theme/assets.go` (new): the loader and its limits.
- `theme.Install`: copy `theme.json` to `<config>/themes/<id>.json`, as now, and copy each asset that the manifest names to `<config>/themes/<id>.assets/`. The suffix keeps the folder's name from being a manifest's: a theme name that ends in `.json` is refused. It writes to a temporary folder and renames it. `theme.Remove` removes both. `theme.List` reads only `.json` files, as now, so the folder does not show as a theme.
- `style.go`: glow uniforms in `background` and `backgroundOptions`.
- `ambience.go`: layers 2 and 3, drawn in the `Draw` of the root column, after `paintBackground`.
- `sidebar.go`: the brand row, the mascot, the ticker and the ambience button.
- `iconpacksview.go`: the Ambience and Mascot controls in the Window Theme group.
- `commands.go`: `appearance.ambience.toggle`.
- `locales/*.json`: the new labels, then `go tool mygo generate`.
- Pack repo: a third-party theme pack is its own public git repo. Its root holds `theme.json`, the `assets/` folder, `LICENSE`, `NOTICE` and each other license text. The gorex repo and `gorex-registry` hold no file of it.
- Registry entry: an entry of the `themes` list in `index.json` gives `repo` (the clone URL of the pack repo), no `subdir`, and a new key `ref`: the full 40-character commit SHA to install. The registry thus lists a reviewed state of the pack, and a later push to the pack repo changes nothing until a registry PR moves `ref`. A tag is not accepted, because the owner of the pack repo can move a tag. A first-party pack in `gorex-registry` (such as `slate`) can keep `subdir` and needs no `ref`.
- Install (`iconpack.Checkout`): with a `ref`, GoRex makes a shallow fetch of that commit (`git fetch --depth 1 origin <ref>`, then `git checkout --detach FETCH_HEAD`), and checks that `HEAD` is `ref`. If the fetch fails, the install fails. GoRex does not fall back to the default branch. A `ref` that is not 40 hex characters is refused.
- Validation: the registry CI validates. For each `themes` entry with a `repo` outside `gorex-registry`, CI clones the repo at `ref` and runs the pack validator on it: the caps, the formats, the asset names that the manifest uses, a `NOTICE` for a pack with assets, and each license file that the `NOTICE` names. A registry PR that adds an entry or moves its `ref` is thus checked on the files that users get. GoRex does not trust this check: at install and at each load, the parser and the loader apply the same caps (see Caps and Security). A local pack (`gorex pack install <folder>`) gets only the GoRex check.

MyGo has each primitive that the design needs (MyGo v0.10.1, `si-mods/mygo`): `DecodeBitmapScaled`, `Painter.ImageRegion` with `FlipX` and `Tint`, `NewGIFDecoder`, `NewAPNGDecoder` and `VideoPlayer`, `shader.DrawInstances`, `Painter.Clip`, `Painter.AnimationFrameRate`, `AnimatePose`, `TooltipBase`, `Clicked`, `DoubleClicked`, `Hovered`, `Context.WindowActive`, `Context.WindowHidden`, `Preferences.ReduceMotion` and `Power.IsOnBattery`. No MyGo change is necessary. Two changes are optional:

- **M1, decode limits** (optional): `DecodeBitmap`, `NewGIFDecoder` and `NewAPNGDecoder` have no pixel or frame cap. A GIF decodes all frames into memory. GoRex checks the headers before it calls them, so M1 is not necessary. M1 would add `ui.DecodeLimits` (max pixels, max frames) so that each MyGo app gets the check.
- **M2, partial repaint on the GPU** (only if the phase 2 budget fails): a paint-only frame repaints the whole window. M2 would repaint only the damaged rectangles, as the CPU renderer does now.

### Capability matrix

| Id | Feature | Now | Extension | MyGo change |
| --- | --- | --- | --- | --- |
| F1 | Palette | Yes: seeds (`accent`, `bg*`, `panel`, `field`, `fieldBorder`, `text`, `textMuted`, `danger`, `done`, `attention`) | None. The sidebar fill has no seed. The window gradient shows through the sidebar, so `bgTint` stands in | n |
| F2 | Sea-surface glow | No: the gradient has 4 fixed stops | `ambience.glows` as uniforms of the `background` shader. Normal alpha, no soft light | n |
| F3 | Watermark | No: a theme has no image | `assets` and `ambience.images` (layer 2) | n |
| F4 | Bubbles | No | `ambience.particles` with `shape: bubble`, one instanced draw, clipped to the free region | n |
| F5 | Fish school | No | `ambience.sprites`, `ImageRegion` with `FlipX`, clipped to the free region. No drop shadow | n |
| F6 | Mascot, idle animation | No | `mascot`: PNG, GIF, APNG or sprite sheet in the sidebar foot. Bob as a `Pose` loop | n |
| F7 | Click: wave and quote | No | `mascot.active` and `mascot.quotes`, bubble on `TooltipBase` | n |
| F8 | Double click: popup | No | `mascot.popup`, bubble on `TooltipBase`, 300×240 DIP at most | n |
| F9 | Hide with ✕ | No | ✕ on hover for the session. The Mascot setting for good | n |
| F10 | Quote ticker | No. GoRex has no chat input | `ticker` in the sidebar foot, fade on `ElementTransition` | n |
| F11 | Brand | No. GoRex shows no logo | `brand` row at the top of the sidebar. SVG icon in the text color | n |
| F12 | Ambience toggle | No | `appearance.ambience`, a sidebar button, a command. Kept in `settings.json` | n |
| F13 | Selection and scrollbar | Partial: UI selection follows `accent`. Terminal selection is in the Ghostty theme | None. A scrollbar role can come later in DSK-008 | n |
| F14 | Asset server and install fetch | Not needed: assets are files in the pack | Install copies the assets. No runtime fetch. No install-time fetch outside the registry | n |
| — | Decode limits | — | GoRex checks headers | Optional (M1) |
| — | Frame cost | — | Phase 2 budget | Only if the budget fails (M2) |

### User decisions

The user decided the four questions of the first draft on 2026-11-24:

1. **Mascot without a sidebar**: when the sidebar is hidden or the tabs are at the top, the mascot and the ticker do not show.
2. **Motion behind the sidebar**: moving layers show behind the sidebar, at the sidebar cap of 0.12 and with the contrast check (see Layers).
3. **Fish sprites**: the pack uses `fish_idle.png` and `fish_happy.png` of `YunYueSama/codex-deepseek-pet`, with the full license text and the credit that it requires (see Licensing).
4. **Battery**: on battery, each animation freezes (see Motion and the budget).

## Alternatives

- **A script plugin, as the probe is**: rejected. DSK-008 Security makes a manifest data. A script can read files and the network.
- **A general keyframe and CSS language**: rejected. It is large, and caps cannot bound it. A fixed set of layer kinds is small, testable and capped.
- **A floating mascot over the panes, as in the probe**: rejected. It covers terminal text at the bottom right, where a prompt often is. The user decided that the mascot does not show without a sidebar.
- **Motion that shows through translucent terminals**: rejected. Motion behind text makes the text hard to read.
- **Remote images by URL**: rejected. They leak the user's IP address to a third party, fail offline, and can change after review.
- **An install-time fetch from another repo, as `install.mjs` does**: rejected. It ships files that nobody can license, and the registry review does not see them.
- **An animated wallpaper or video**: rejected. It costs memory and frames for each pixel of the window.
- **A separate pack kind for ambience**: rejected for now. A palette and its ambience are designed together. A second kind can come later if users mix them.
- **Blend modes for the glows**: rejected. Soft light needs the backdrop in the shader. Normal alpha at a low step looks close.

## Security

- A pack contains data only: colors, numbers, enum names, text and image files. It contains no code and no URL.
- An asset value is a file name in the pack's own folder. A value with a path separator, `..`, a drive letter or `://` is dropped. The loader refuses a link and a file that is not regular.
- GoRex reads each file with a byte cap and checks its header before it decodes. Dimension and frame caps stop a decompression bomb. A decode runs on a goroutine and recovers a panic.
- GoRex makes no network request for a theme at runtime. Install clones the repo that the registry lists, at the pinned commit `ref` (`iconpack.Checkout`). A later push to that repo cannot change what users install until a registry PR moves `ref`.
- Text is shown as text. GoRex removes control characters and bidirectional overrides. A theme cannot open a link, run a command or write a setting.
- An interaction is one of the fixed set: quote, popup, hide. A theme cannot add a button or an action.
- A theme cannot cover a pane or take the keyboard focus. Layers and the focus rule enforce this, not the review.

## Licensing

The registry lists a pack only when the license of each asset allows redistribution. The pack repo keeps the license texts and a `NOTICE` beside the assets. GoRex and `gorex-registry` hold no third-party asset. The probe's NOTICE says that all of its PNG files are MIT. A check of the source repos on 2026-11-24 found these facts:

| Asset | Source | License now | Decision |
| --- | --- | --- | --- |
| `whale_front.png`, `whale_side.png`, `whale_icon.png` | `1190fasheqi/dafeiyu-pet` | MIT | Can ship, with the MIT text and the attribution. The character is fan art of a DeepSeek mascot. The pack does not use the DeepSeek name |
| `fish_idle.png`, `fish_happy.png` | `YunYueSama/codex-deepseek-pet` | MIT until 2026-09-13. Now a custom attribution license, "大肥鱼项目署名许可 1.0". Its `ASSET_LICENSE.md` says the license does not cover the underlying character design | Ship, by the decision of the user, with the full license text and the required credit (see below) |
| `meme_dafeiyu.png` | `YunYueSama/codex-deepseek-pet` | Not MIT. The source repo calls it a community reference picture whose author and license are not verified | Do not ship |
| `girl_idle.gif`, `girl_waving.gif`, `girl_running.gif` | `xpy12367/codex-pet-DeepSeek-girl` | No license | Do not ship and do not fetch |
| The whale logo SVG in the probe's CSS | DeepSeek's logo | Trademark | Do not ship. The pack draws its own whale icon |

The first pack is **Dafy Whale**, in its own public repo `si-mods/gorex-theme-dafy-whale`. It uses the MIT whale art and the two fish PNGs. The mascot uses `whale_front.png`. The popup uses `whale_front.png` or a self-made picture, not the meme. The brand icon is a self-made SVG.

The fish PNGs ship under "大肥鱼项目署名许可 1.0", as the user decided. The pack obeys its terms:

- The pack keeps the copyright notice and the full license text, unchanged, as `LICENSE-fish.txt` at the root of the pack repo.
- The `NOTICE` and the entry of the pack on the Extensions page give the credit: author `YunYueSama`, repo `https://github.com/YunYueSama/codex-deepseek-pet`. "Source: internet" or the author alone is not sufficient.
- The pack scales the PNGs down from 768×832 px to at most 256 px on a side, to fit the caps and save memory. The `NOTICE` says that the files are modified, and how. It does not call the modified files the work of the author.
- The pack does not say or suggest that the author or DeepSeek endorses it.
- The license grants rights only to the contribution of the author. It does not cover the underlying character design, which comes from community references. The `NOTICE` says this. This residual risk is the accepted choice of the user. If a rights holder objects, the pack repo removes the files in a new commit and the registry moves `ref` to it, or the registry removes the entry.

The animated mascot path still needs a test with a real animated asset. Phase 3 makes a sprite sheet and a GIF from `whale_front.png` (a bob and a wave, 8 frames). The MIT license allows a modified work. The pack's `NOTICE` says that the frames are modified. Neither the pack repo nor GoRex redistributes the DeepSeek GIFs, the meme or the DeepSeek logo. A user can put their own files in a local pack (`gorex pack install <folder>`) at their own risk. GoRex does not help to fetch them.

## Migration

- A new key has a default. `appearance.ambience` and `appearance.mascot` are new settings with a default, so `docs/irreversible.md` does not apply. With no key, ambience moves and the mascot shows when the theme has them.
- The manifest keeps its place, `<config>/themes/<id>.json`. The assets go to a new folder, `<config>/themes/<id>.assets/`. A single-file theme loads as now.
- An older build loads a new manifest. It drops `assets`, `ambience`, `mascot`, `brand` and `ticker` with an `unknown key` warning each, and uses the colors. `theme.List` of an older build skips the folder.
- An older build installs a pack from the registry with `theme.json` only. The theme then has colors and no ambience. This is harmless.
- An older build ignores `ref` and clones the default branch of the pack repo. It copies only `theme.json`, and its parser applies the DSK-008 checks. The colors can thus be newer than the reviewed commit. This is a known limit for older builds only.
- An older build that removes a theme leaves the asset folder. A newer build removes an `<id>.assets` folder that has no manifest when it installs or removes a theme. Listing the themes removes nothing.
- An older build that saves `settings.json` drops the two new keys, as DSK-008 says for `appearance.theme`. Ambience then moves again, and the user sets the key again.

## Tests

- **Parse**: each new key parses. A value past a cap clamps, an extra entry drops, a bad color drops, an unknown asset name drops, and each gives a warning (`TestParseAmbience`). A manifest with only DSK-008 keys gives the same `Manifest` as now.
- **Text**: a quote with control characters or a bidirectional override loses them. A long quote keeps its first 80 characters (`TestParseText`).
- **Assets**: a name with a path, a link, a file over the byte cap, a PNG over the pixel cap, a GIF over the frame cap and an unknown format each drop, without a full decode (`TestAssetLimits`). A test fixture is a crafted header, not a large file.
- **Install**: a registry pack and a local folder install the manifest and only the named assets. Remove deletes both. A single-file theme installs as now (`TestInstallThemeAssets`, `TestPackInstallTheme`).
- **Pinned ref**: with a local test repo of two commits, an entry with `ref` at the first commit installs the first commit. A `ref` that is not a full SHA, or that the repo does not have, fails the install with no fallback (`TestCheckoutRef`).
- **Background**: with no glows, the shader matches the gradient within 3/255 (`TestBackgroundShader`). With a glow, the pixel at its center has its color.
- **Free region**: bands for a tiled layout, a canvas and an empty page cover the window minus the panes, with no overlap (`TestFreeRegion`).
- **Still rule**: in `ui.NewTester`, ambience, the mascot and the ticker ask for no frame and no timer when the window is not active, is hidden, on battery, under ReduceMotion, with Animation Off, with Ambience Still, and on the CPU renderer (`TestAmbienceStill`).
- **Sidebar cap**: in the sidebar band, no sprite or particle paints above alpha 0.12. A theme whose worst case fails 4.5:1 for `text` or 3:1 for `textMuted` gets a lower cap and a warning (`TestSidebarAmbienceContrast`).
- **Paths**: a sprite and a particle are at the expected place at a given time, and a left sprite is mirrored (`TestAmbiencePaths`).
- **Mascot**: a click shows a quote from the list and the active pose. A double click shows the popup. ✕ hides the mascot. The terminal keeps the focus after each click (`TestMascot`).
- **Ticker**: the line changes after its interval, and not while the window is not active (`TestTicker`).
- **Brand**: the sidebar shows the icon and the wordmark. A long wordmark ellipsizes.
- **Settings**: Ambience Off draws no layer 1 to 3 extras and no ticker. Mascot Off draws no mascot (`TestAmbienceSettings`).
- **Screenshots**: the `GOREX_DEBUG` hook, light and dark, tiled and canvas, glass on and off, with made-up data.
- **Budget**: phase 2 measures the frame cost and the idle CPU on a Windows PC and on a Mac, and writes the numbers in its PR.
- **Registry**: the CI clones each listed pack repo at its `ref` and refuses an entry with no `ref`, a pack over a cap, a missing asset, or assets and no `NOTICE`. For `dafy-whale`, it checks that `LICENSE-fish.txt` is present and that the `NOTICE` has the author and the repo URL.

No item in `docs/invariants.md` covers the theme or ambience. The focus rule of the mascot gets a test (`TestMascot`).

## Phases

1. **Assets and still layers** (built, #1083; M, about 800 lines with tests): asset parsing, caps and loader, pack folder install and remove, the pinned `ref` in `iconpack.Checkout`, the registry CI that clones and validates each listed pack repo, glows in the background shader, still images, brand row, the two settings and the Extensions controls. The registry CI is si-mods/gorex-registry#6. Until its phase, the parser drops `particles`, `sprites`, `mascot` and `ticker` with a warning, as an older build does. The Ambience control shows under the theme in use when it has glows, images or a brand. The Mascot control comes with the mascot in phase 3; `appearance.mascot` is read from phase 1. `iconpack.Checkout` refuses a pack of a repo other than `gorex-registry` that has no `ref`. The assets decode for a screen scale of 2.
2. **Moving layers** (M, about 500 lines): particles, sprites, free region, the still rule, the toggle button and command, and the budget measurement.
3. **Mascot and ticker** (M, about 600 lines): still, animated and sprite sheet mascot, bob, click, double click, hide, bubbles and the ticker. A self-made animated whale validates GIF and sprite sheets.
4. **Dafy Whale pack** (S): a new public repo `si-mods/gorex-theme-dafy-whale`, not in gorex and not in `gorex-registry`. Its root has `theme.json`, `assets/` (the MIT whale art, the two fish PNGs scaled down, a self-made icon, the self-made animated whale), `LICENSE` (MIT), `NOTICE` and `LICENSE-fish.txt`. A registry PR adds one `themes` entry: `id` `dafy-whale`, `repo` `https://github.com/si-mods/gorex-theme-dafy-whale.git`, no `subdir`, and `ref` at the reviewed commit. Screenshots.

MyGo M1 and M2 are not in a phase. M2 starts only if phase 2 fails its budget.
