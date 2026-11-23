---
name: DSK-008
description: "A theme framework for the desktop chrome: color seeds derive semantic roles, and every face resolves with its text colors as one value. Use when you change the palette, add a surface role, or ship theme packs."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# DSK-008: Desktop theme framework

Status: proposal.

This doc decides the shape of theming for the desktop chrome. The chrome is the sidebar, the cards of the panes, the dialogs and the menus. This doc replaces no doc. It extends the palette rules of `DESIGN.md` and the settings keys of [DSK-007](DSK-007-sidebar-style.md). The layers come from the design-system pass note of the Vibe Design Tips series (vibedesign.tips). The material and ink model comes from tip 010 of that series (liquid glass).

## Problem

The palette is the `colors` struct in `style.go`. It has 47 color fields, with literal values in `lightColors` and `darkColors`. The literals have no relation to each other. A face and the text on it are two fields, and no code links them.

Only the pane header adapts its text to its face. `headerColors` returns the palette of the other appearance when the face of the card has the other lightness. No other surface does this.

[DSK-007](DSK-007-sidebar-style.md) lets the user set `sidebar.colorCustomizations`. With `sidebar.selection` set to `"fill"`, `sidebarChosen` paints the chosen row at `tabActive` and changes only the face. The row keeps the text of the palette. A light `tabActive` in a dark window thus puts light text on a light face. Each other face can lose contrast in the same way.

The literals also hide the structure. Most values are derivations. A hover is the text at a low alpha. A border is the text at a higher alpha. A shadow is black at a fraction. A user who writes a theme must write 47 values where seven are sufficient.

## Goal

- A small set of **seeds** makes the palette: background, text, muted text, accent, surface lift, shadow strength and radius scale.
- A face and the colors that show on it resolve **as one value**. Code cannot paint a face and keep text that does not fit the face.
- A **theme manifest** describes a theme as data. The registry that ships icon packs can then ship theme packs.
- The `sidebar.*` settings keep their meaning. Their color tokens become role names in the same resolution.

The target is one stable design language, not a full design system. It has three layers, and each layer uses the layer below it:

1. Seeds: colors and metrics.
2. Surface roles: a face with its text ladder.
3. Components: each component asks for a role, never for a raw value.

The work keeps the current look. It makes the values consistent. It does not change the style.

## Non-goals

- Terminal colors stay in the Ghostty configuration and in the terminal theme setting. [DSK-009](DSK-009-terminal-settings.md) moves that setting from `theme` to `terminal.theme`. The terminal is a second lightness domain. The framework adapts the chrome to the terminal. It does not change the terminal colors.
- The glass stays a user setting (`noGlass`). A theme does not change the glass setting.
- iOS and the web client keep their palettes. The first scope is the desktop chrome.
- No migration of `settings.json` occurs. Each key that the user has keeps its meaning.

## Design

### Behavior

A theme is a manifest of seeds and optional role overrides:

```json
{
  "name": "slate",
  "seeds": {
    "light": { "background": "#f5f6f8", "text": "#1a1d21", "accent": "#0a6bff" },
    "dark":  { "background": "#16181d", "text": "#e8eaed", "accent": "#5b8def" }
  },
  "roles": { "surfaceSelected": "#5b8def33" }
}
```

The user selects a theme with a new settings key, `appTheme`. Its value is the name of a manifest. The name of a Ghostty theme stays in the terminal settings, `theme` now and `terminal.theme` after [DSK-009](DSK-009-terminal-settings.md). Open question 1 records the key name.

Resolution has four layers. Each layer overwrites the layer before it:

1. The built-in seeds for the appearance.
2. The seeds of the selected theme.
3. The role overrides of the theme.
4. The overrides in the user's settings: `sidebar.colorCustomizations`, and each later `*.colorCustomizations` key.

Code asks for a **surface**, not for a color. The resolver returns the face and a text ladder that shows on the face:

```go
s := surface(k, roleSurfaceSelected)
row.Background(s.face)
ui.Text(c, name).TextColor(s.text)
```

The value `s` holds the `face` and the text ladder: `text`, `textMuted`, `textFaint` and `iconMuted`. The ladder has the lightness that fits the face. When the lightness of the face is not the lightness of the palette, the resolver gives the ladder of the other appearance. `headerColors` does the same for a pane header now. The caller cannot get the face without the text.

### Opaque faces and materials

An opaque face and a material are two different types.

- An opaque face resolves to a color. Its lightness is known.
- A material resolves to a tint over a backdrop. Its text resolves as **ink**: the same ladder, as fixed alpha steps of black or white. A glass surface usually has titles in black at 86% and secondary text in black at 60%. On a dark backdrop, the steps are white at 94% and white at 64%.

The resolver selects the ink as follows:

- When the backdrop is a palette color, such as the window face or a card, the resolver reads its lightness.
- When the code cannot read the backdrop, such as glass over live terminal output, the material uses the ink of the theme at its densest tint.
- When the user selects an override, the override must be an opaque face. A setting cannot override a material.

A backdrop is a palette color or an unknown texture. The resolver does not sample pixels. Thus the ink changes only when the palette changes, and the ink does not flicker.

The system setting for less transparency also applies. MyGo gives it as `ui.Preferences.ReduceTransparency`: Reduce Transparency on macOS, and no transparency effects on Windows. When it is true, a material role resolves to the same face at full opacity. GoRex reads no such setting now, so this is new behavior.

### Model

Fixed rules make the palette from the seeds. One seed set always gives the same palette:

| Role | Derivation |
| --- | --- |
| `textMuted`, `textFaint` | `text` at fixed alpha steps |
| `hover`, `pressed` (opaque) | `text` at fixed alpha steps |
| `hover` (material) | the tint, made brighter by a fixed fraction, with no shadow |
| `cardBorder`, `tabSep` | `text` at fixed alpha steps |
| `surface` | `background`, made lighter by `surfaceLift` |
| `panelSel`, `tabActive` | `accent` at fixed alpha steps, or explicit roles |
| `shadow`, `shadowFocused` | black at `shadowStrength` |
| `radiusS`, `radiusM`, `radiusL`, `radiusXL` | the `radius` seed at fixed steps |

A nested corner keeps the current rule, `concentric(outer, inset)`. It is the outer radius minus the inset, and never less than 0.

The rules cannot make all roles. Each such role stays explicit in the palette, as now. The `colors` struct stays as the result of the resolution. The framework changes the source of the values. It does not change what the widgets read. Two literals that are almost equal merge into the nearer role. A new role is added only when no role fits a surface.

Each face has a pairing rule. The resolver attaches the text ladder that fits the lightness of the resolved face. The rule applies to built-in faces, theme faces and user overrides. It runs at resolve time, not at seed time.

### Code

- `style.go`: The `colors` struct stays. A `seeds` struct and `derive(seeds) colors` make the two palettes. `surface(k, role)` returns the face and its ladder. `headerColors` becomes part of the resolver.
- `internal/theme` (new): This package parses and validates a manifest. It checks the seed hex values. It checks the role names against an allowlist, as `checkSidebarColors` does.
- `settings.go`: The new `appTheme` key names a manifest in the config directory or in a pack. Each `*.colorCustomizations` key maps onto roles.
- Registry: A pack of kind `theme` carries the manifest. It uses the install path of the icon packs.
- Call sites move to the resolver one surface at a time. The chosen sidebar row moves first, because it shows the contrast bug.

## Alternatives

- **Keep the flat palette and rely on review**: rejected. Nothing prevents a `tabActive` override with unreadable text. The construction must prevent it.
- **Raw token overrides only, as CSS variables**: rejected. The face and the text stay separate, and that causes the bug. The user must also write 47 values.
- **One fixed pairing for each appearance, with no lightness check**: rejected. A dark terminal in a light window is a supported configuration. A fixed pairing fails on it.
- **Theme the terminal too**: rejected as out of scope. The Ghostty configuration owns the terminal colors.
- **A theme that changes the glass setting**: rejected. The glass changes the cost of drawing, and the user decides it with `noGlass`.
- **Sample the pixels of the backdrop to select the ink**: rejected. It costs a read of the frame. Ink from sampled pixels also flickers near the flip point.

## Security

A theme manifest is data: hex values and enum names. It contains no code, no paths and no commands. The load step removes unknown roles and bad colors, as `checkSidebarColors` does.

## Migration

No settings change is necessary. The built-in seeds make the current palettes again. The Ghostty theme name stays in `theme`, or in `terminal.theme` after DSK-009. Each `sidebar.colorCustomizations` token maps onto a role name. The seven DSK-007 tokens keep their names as roles. An older build ignores the `appTheme` key, because `loadSettings` ignores unknown keys.

## Tests

- **Derivation**: `derive` on the built-in seeds gives the current `lightColors` and `darkColors` values, within rounding.
- **Pairing**: A light `tabActive` override in a dark window resolves dark text for the chosen row. A dark override keeps the text of the palette.
- **Ink**: A material over a backdrop that the code cannot read resolves the ink of the theme at the dense tint.
- **Reduce Transparency**: With `ReduceTransparency` true, a material role resolves to an opaque face.
- **Manifest**: The load step removes a manifest with a bad seed or an unknown role and logs a warning. It does not stop the app.
- **Settings**: A `settings.json` with both `terminal.theme` and `appTheme` keeps the Ghostty theme and selects the manifest.

No item in `docs/invariants.md` covers the palette.

## Phases

1. **Pairing**: Add the `surface` resolver. Move the chosen sidebar row to it first, then the other chrome. This fixes the contrast for the current overrides.
2. **Seeds**: `derive` replaces the literal palettes, and `colors` becomes a derived value.
3. **Manifest**: Add `internal/theme`, the `appTheme` settings key and a `theme` pack kind on the registry.

## Open questions

1. **Settings key for the theme**: The user decides. The options are:
   1. A new key `appTheme` (recommended). Nothing breaks. An older build ignores it.
   2. A new dotted key `appearance.theme`, as the DSK-007 keys are. Nothing breaks. It starts an `appearance.*` group with one key.
   3. Use `theme` for the manifest, after DSK-009 moves the Ghostty theme name to `terminal.theme`. An older build still reads `theme` as a Ghostty theme name. It then does not find the theme, and during the DSK-009 transition the two values collide in one key.
2. **Role names**: Keep the DSK-007 tokens (`tabActive`, `groupFace`) or rename them to tokens in the style of VS Code (`sideBar.*`). A rename breaks settings. Keeping the tokens keeps compatibility but also keeps flat names. The user decides.
3. **Seed count**: Seven seeds is an estimate. The derivation table gives the real count in phase 2.
4. **Motion in the manifest**: Easing curves and durations are tokens, as colors are. The manifest can carry them later. The `animation` setting can already stop all motion. This is deferred to phase 3.
5. **Web reuse**: The web client is TypeScript that `web/build.mjs` builds. A manifest is JSON, so the web client can read it later. A WEB doc decides this when the work starts.
