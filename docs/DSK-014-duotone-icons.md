---
name: DSK-014
description: "Kubai two-tone icons with hover motion for the desktop Settings sidebar and large navigation surfaces. Use when you add or change two-tone icons, their color derivation, or their hover animation."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# DSK-014: Kubai two-tone icons

Status: built.

This doc introduces Kubai two-tone (duotone) icons to GoRex's desktop UI, starting with the Settings sidebar.

## Problem

The Settings sidebar (`settingsview.go`) draws each section as a white monochrome glyph inside a solid colored rounded square. That pattern imitates macOS System Settings tiles rather than GoRex's own visual language, and it stays static on hover. Meanwhile, Kubai Icons (`kubai087/kubai-icons`, MIT) provides a 16-grid two-tone solid icon set (`icons-duotone/`) with layer data and per-layer hover motion definitions. The community MyGo port (`yldm-tech/mygo-kubai-icons`) covers only the layered animated style, not the two-tone style.

## Goal

- Provide a self-contained Go package `internal/duotone` that embeds selected MIT icon definitions from `kubai087/kubai-icons/icons-duotone/`.
- Render each icon as a two-tone `*ui.SVG` using MyGo's native SVG renderer, with primary color and appearance-aware secondary color derivation.
- Animate each icon smoothly on hover using Kubai's duotone motion curves, respecting `ReduceMotion`.
- Replace the colored square tiles in the Settings sidebar with bare two-tone icons in each section's semantic hue, driven by the row's hover state.

## Design

### Scope

Two-tone icons apply to large navigation items and cards (18–26 DIPs), starting with the eight Settings sidebar sections:

| Section | Icon | Light primary | Dark primary |
| --- | --- | --- | --- |
| Appearance | `sun` | `#007AFF` | `#3B9EFF` |
| Terminal | `terminal` | `#3A3A3C` | `#AEAEB2` |
| Theme | `sparkles` | `#AF52DE` | `#BF6AF0` |
| Icon Packs | `layers` | `#FF9500` | `#FFA426` |
| Keyboard | `keyboard` | `#636366` | `#98989D` |
| Hosts | `monitor` | `#28A745` | `#3DD664` |
| Settings JSON | `file-code` | `#1FA0B8` | `#40C4DC` |
| About | `info` | `#636366` | `#98989D` |

Dense chrome icons (12–14 DIPs in toolbars, tabs, menus and pane headers) remain monochrome (`glyphIcon` / SF Symbols on macOS) so they stay crisp and native.

### Color derivation

Each icon has two tones: the secondary tone (`b`) fills the object body, and the primary tone (`a`) draws key details and strokes.

- On light surfaces, `Secondary(primary, false)` blends `primary` at 40% opacity over white, matching Kubai's `secondary(primary, 0.4)`.
- On dark surfaces, `Secondary(primary, true)` uses `primary` at 34% alpha (`fill-opacity="0.34"`), so the body stays muted and translucent over dark glass while primary details stand out clearly.

### Hover motion

`internal/duotone` implements Kubai's duotone hover motion runtime directly in Go:

- Enter curve: `cubic-bezier(0.34, 1.56, 0.64, 1)` over `480ms` plus `110ms` for each extra keyframe past the first (or `ms` when the icon sets one).
- Leave curve: `cubic-bezier(0.33, 1, 0.68, 1)` over `360ms` back to rest without overshoot.
- Supported pose properties: `x`, `y`, `r` (degrees), `s`, `sx`, `sy`, `a` (opacity), `o` (transform origin, default `[8, 8]`), and `rest` (initial rest values such as `{a: 0}` for hover-only layers).
- `Icon.View(c, opts)` stores animation state via `ui.Local` and schedules frames with `Painter.AnimationFrame()` only while moving. Frame progress is quantized to 48 steps so cached `*ui.SVG` frames are reused across repeated hovers.
- When `c.Preferences().ReduceMotion` is true, hover snaps directly between rest and end pose with no intermediate frames.
- Users can disable icon hover motion via the Appearance switch (`noIconMotion` in `settings.json`). Setting `animation` to `"off"` also disables icon hover motion.

### Licensing

Only the MIT-licensed `icons-duotone/*.json` layer data files and `LICENSE-ICONS` from `kubai087/kubai-icons` are embedded in `internal/duotone/`. The source-available skill scripts are not copied into the repository.

## Alternatives considered

- **`yldm-tech/mygo-kubai-icons`**: Ported only Kubai's layered style (`Color` / `Soft`), not the two-tone style requested here.
- **Static pre-exported SVGs**: Would require duplicating light and dark variants per color and would lose hover micro-interactions.
- **Replacing all 80 `assets/icons/*.svg` icons**: Solid two-tone shapes feel heavy at 12–14 DIPs in tab bars and toolbars, and would replace macOS SF Symbols where native monochrome icons fit better.

## Testing

- Unit tests in `internal/duotone` verify JSON loading for every embedded icon, `FIT` scaling, SVG parsing with zero `Unsupported()` features in MyGo, light and dark secondary color derivation, and motion interpolation at rest, midpoint, and hovered poses.
- A headless UI test verifies that Settings renders all sidebar sections with two-tone icons and responds to hover.
- Real-window screenshots on Mac mini verify light and dark appearance of the Settings sidebar before merge.
