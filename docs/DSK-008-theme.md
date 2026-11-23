---
name: DSK-008
description: "A theme framework for the desktop chrome: color seeds derive semantic roles, and every face resolves with its text colors as one value. Use when you change the palette, add a surface role, or ship theme packs."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# DSK-008: Desktop theme framework

Status: proposal.

This doc decides the shape of theming for the desktop chrome: the sidebar, the panes' cards, the dialogs and the menus. It supersedes no doc. It extends the palette rules of `DESIGN.md` and the settings keys of [DSK-007](DSK-007-sidebar-style.md). Outside input: the Vibe Design Tips series (vibedesign.tips) — its design-system pass note gives the layers, and tip 010 (liquid glass) gives the material and ink model.

## Problem

The palette is a struct of about forty color literals, `lightColors` and `darkColors`. The literals carry no relation to each other: a face and the text over it are two fields, and nothing links them. When [DSK-007](DSK-007-sidebar-style.md) let the user set `sidebar.colorCustomizations`, a light `tabActive` in the dark palette put dark-theme text on a light face, and the row became unreadable. The fix reused `headerColors`, which flips the text colors when a pane's terminal background has the other lightness. That mechanism exists for the pane header only. Every other face can drift out of contrast the same way.

Forty literals also hide the structure. Most values are derivations: a hover is the text at a low alpha, a border is the text at a higher alpha, a shadow is black at a fraction. The user who wants a theme must write forty values where eight would do.

## Goal

- A small set of **seeds** generates the palette: background, text, muted text, accent, surface lift, shadow strength, radius scale, material on or off.
- A face and the colors that read over it resolve **as one value**, so code cannot paint a face and keep text that does not fit it.
- A **theme manifest** describes a theme as data, so the registry that ships icon packs can ship theme packs.
- The `sidebar.*` settings keep working. Their color tokens become role names in the same resolution.

The target is one stable design language, not a full design system. It has the layers a design-system pass names, each built on the one below: seeds (colors and metrics), then surface styles, then the components that opt into them. A component asks for a role, never a raw value. The pass keeps today's look: it makes values consistent, it does not restyle.

## Non-goals

- Terminal colors stay in the Ghostty configuration. The terminal is a second lightness domain; the framework adapts chrome to it, it does not theme it.
- iOS and the web client keep their palettes. The framework is the desktop chrome first.
- No migration of `settings.json`. Keys the user has keep their meaning.

## Design

### Behavior

A theme is a manifest of seeds plus optional role overrides:

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

Resolution order, each layer overwriting the one before: built-in seeds for the appearance, the chosen theme's seeds, the theme's role overrides, the user's settings overrides (`sidebar.colorCustomizations` and any later `*.colorCustomizations`).

Code asks for a **surface**, not a color. The resolver returns the face and a palette whose text reads over it:

```go
s := surface(k, roleSurfaceSelected)
row.Background(s.face)
ui.Text(c, name).TextColor(s.text)
```

`s` is the pair: `face`, and the resolved text ladder (`text`, `textMuted`, `textFaint`, `iconMuted`) whose lightness fits the face. When the face's lightness differs from the palette's, the resolver supplies the text ladder of the other appearance, as `headerColors` does for a pane header. The caller cannot reach the face without the text.

An opaque face and a material are different types. A face resolves to a color whose lightness is known. A material resolves to a tint over a backdrop, so its text resolves as **ink**: the same ladder, expressed as fixed alpha steps of black or white, the way a glass surface in the wild carries `black 86%` titles and `black 60%` secondary text, or `white 94%` / `white 64%` in the dark case. Where the backdrop is a palette color — the window face, a card — the resolver reads its lightness. Where the backdrop is a texture the code cannot read — glass over live terminal content — the material falls back to the theme's ink at its densest tint. When the sampled lightness sits near the flip point, the resolver keeps the previous ink within a small band, so a surface does not flicker between light and dark text. Settings that override a role name an opaque face only.

### Model

Seeds generate the palette through fixed rules, so a seed set yields the same forty values every time:

| Role | Derivation |
| --- | --- |
| `textMuted`, `textFaint` | `text` at fixed alpha steps |
| `hover`, `pressed` (opaque) | `text` at fixed alpha steps |
| `hover` (material) | tint brightened a fixed fraction, not a lifted shadow |
| `cardBorder`, `tabSep` | `text` at fixed alpha steps |
| `surfaceRaised` | `background` lifted by `surfaceLift` |
| `panelSel`, `tabActive` | `accent` at fixed alpha steps, or explicit roles |
| shadows | `black` at `shadowStrength` |
| `radiusSm`, `radiusLg` | `radius` seed at fixed steps; a nested corner is `outer − padding`, floored at the smallest radius |

Roles the rules cannot derive stay explicit in the palette, as now. The `colors` struct remains the resolution result. The framework changes where the values come from, not what the widgets read. Near-duplicate literals merge into the nearest role; a new role joins only when a surface has none that fits.

A face keeps a pairing rule: resolution attaches the text ladder that fits the resolved lightness. The rule holds for built-in faces, theme faces and user overrides, because it runs at resolve time, not at seed time.

Reduced transparency resolves a material role to the same face at full opacity: the user who asks the OS for less glass gets a solid surface, not a thinner one.

### Code

- `style.go`: `colors` stays; a `seeds` struct and `derive(seeds) colors` build the two palettes; `surface(k, role)` returns the pair; `headerColors` generalizes into the resolver.
- `internal/theme` (new): manifest parse and validate — seed hex values, role names against a whitelist, the same discipline `checkSidebarColors` has.
- `settings.go`: a `theme` key names a manifest in the config dir or a pack; `*.colorCustomizations` maps onto roles.
- `iconpack`-adjacent install path reuses the registry; a pack of kind `theme` carries the manifest.
- Call sites migrate one surface at a time: the chosen sidebar row first, since it shows the bug.

## Alternatives

- **Keep the flat palette and rely on review**: rejected. The `tabActive` override already produced unreadable text; discipline did not prevent it, construction must.
- **CSS-style raw token override only**: rejected. It keeps the face/text split that causes the bug, and it asks the user for forty values.
- **Per-appearance only, no luminance adaptation**: rejected. A dark terminal in a light window is a supported configuration; fixed pairings break on it.
- **Theme the terminal too**: rejected as scope. The Ghostty configuration owns that domain.

## Security

A theme manifest is data: hex values and enum names. It carries no code, no paths, no commands. Validation drops unknown roles and bad colors at load, as `checkSidebarColors` does.

## Migration

No settings change is required: built-in seeds reproduce today's palettes. `sidebar.colorCustomizations` tokens map onto role names; the seven DSK-007 tokens keep their names as roles. An older build ignores the `theme` key.

## Tests

- **Derivation**: `derive` on the built-in seeds yields the current `lightColors`/`darkColors` values, within rounding.
- **Pairing**: a light `tabActive` override resolves dark-theme text for the row; a dark override keeps the palette's text.
- **Ink**: a material over an unreadable backdrop resolves the theme ink at the dense tint; a face at the flip point keeps its ink inside the hysteresis band.
- **Manifest**: a manifest with a bad seed or an unknown role is dropped with a warning, not a crash.

## Phases

1. **Pairing**: `surface` resolver plus `sidebarChosenFace`-style adoption across the chrome; fixes contrast for existing overrides.
2. **Seeds**: `derive` replaces the literal palettes; `colors` becomes derived.
3. **Manifest**: `internal/theme`, the `theme` settings key, a `theme` pack kind on the registry.

## Open questions

1. **Role names**: reuse the DSK-007 tokens (`tabActive`, `groupFace`...) or rename to VS Code-style tokens (`sideBar.*`)? A rename is a settings break; reuse keeps compatibility but keeps flat names.
2. **Seed count**: eight seeds is the estimate; the derivation table decides the real count.
3. **Motion in the manifest**: easing curves and durations are tokens the same way colors are. The manifest could carry them later; the `animation` setting already switches motion off. Deferred to the manifest phase.
4. **Web reuse**: `web/` styles from Go templates; a manifest could feed them later. Decided per WEB doc when reached.
