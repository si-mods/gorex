---
name: MOD-012
description: "Community program icon packs: a declarative plugin format to supply and override program icons, similar to Zed icon themes. Use when designing or implementing icon packs."
metadata:
  created: "2026-11-23"
  updated: "2026-11-24"
---

# MOD-012: Community Program Icon Packs

Status: phase 2 built. Phase 3 (iOS and Web pack matching) not built.

This doc defines declarative community icon packs for GoRex programs. It builds on [`MOD-011`](MOD-011-program-icons.md) and [`REF-002`](REF-002-lua.md).

## Problem

[`MOD-011`](MOD-011-program-icons.md) lets programs emit their own icons at runtime via `OSC 7502` or `gorex icon`. Most existing CLI tools do not emit `OSC 7502`, and adding every niche tool to `programs.go`, `ios/Shared/Program.swift`, and `web/src/programs.ts` does not scale.

## Goal

- Let community authors publish standalone program icon packs as declarative files (`pack.json` and SVG/PNG assets), without compiling GoRex or shipping a WASM runtime.
- Match programs by process name, `OSC 7501` `app` name, or optional subcommand consistently for both local and remote sessions.
- Let users install, enable, and layer icon packs on top of built-in program tables, `OSC 7502`, and `init.lua` rules.

## Non-goals

- A general-purpose WASM plugin runtime or arbitrary native code execution.
- Scanning local or remote project directories for marker files (such as `Cargo.toml` or `package.json`).
- A hosted plugin marketplace or billing registry (initial distribution uses local directories or Git repositories).

## Design

### Behavior

Users place or install an icon pack under `<configDir>/icon-packs/<pack-id>/` and select active packs in `settings.json` (`iconPacks`) or `init.lua` (for example `gorex.icon_packs({ "catppuccin-programs" })`).

When rendering a pane header, tab tile, or command palette item, GoRex resolves the icon in order: explicit user/Lua override (`gorex.program_icon`), active community icon packs (in configured order), runtime `OSC 7502` icon (`SessionInfo.ProgramIcon`), built-in `programOf` table, and finally the generic terminal glyph. `iconPackPriority` in `settings.json` or `gorex.icon_pack_priority("osc")` in `init.lua` inverts the pack vs. `OSC 7502` precedence when users want self-reporting programs to win.

### Settings page

The Settings window's page for packs is named **Extensions** (zh-Hans: 扩展), in the style of VS Code. It holds every kind of pack the registry ships: icon packs, and theme packs ([DSK-008](DSK-008-theme.md)). Only the name in the UI changed. The CLI (`gorex pack ...`), the folders on disk (`<configDir>/icon-packs/`, `<configDir>/themes/`), the settings keys (`iconPacks`, `iconPackPriority`, `appearance.theme`) and the registry format stay as they were.

The page has four groups:

1. **Window Theme**: the built-in palette and each installed theme. The theme in use shows "In use". Each other one has **Use**, which saves `appearance.theme` and applies the theme to every window at once. An installed theme also has **Remove**. Removing the theme in use goes back to the built-in palette.
2. **Icon Packs**: the priority (icon packs or `OSC 7502` first), then each installed icon pack with its switch and **Remove**.
3. **Community Registry**: a search field and the registry's entries of every kind, icon packs first, then themes. Each entry's hint starts with its kind ("Icon pack" or "Theme"). **Install** installs an icon pack and turns it on. For a theme pack, **Install** runs the same steps as `gorex pack install` (`iconpack.Checkout`, then `theme.Install`) and does not use the theme. An index with no `themes` list, as the registry's before theme packs, lists its icon packs.

For both local and remote hosts, the client resolves pack entries locally from `SessionInfo.ProgramName()` and `SessionInfo.Args`, requiring zero directory I/O and zero extra bytes over the wire.

### Model

An icon pack directory contains `pack.json` and relative asset files under `icons/`:

```json
{
  "schema": 1,
  "id": "catppuccin-programs",
  "name": "Catppuccin Programs",
  "version": "0.1.0",
  "author": "community",
  "programs": {
    "terraform": {
      "icon": "icons/terraform.svg",
      "mode": "mono",
      "bg": "#7b42bc",
      "fg": "#ffffff"
    },
    "docker compose": {
      "icon": "icons/compose.svg",
      "mode": "mono",
      "bg": "#2496ed",
      "fg": "#ffffff"
    }
  }
}
```

Each entry supports `icon` (relative path to `.svg` or `.png`), `mode` (`mono` or `color`), `bg`, `fg`, and optional `light` / `dark` color overrides. Loaded assets are validated with the same rules as `OSC 7502` (`maxIconBytes = 32768`, SVG sanitization, PNG bounds) and converted into `rex.ProgramIcon` values cached by content hash.

### Code

- `internal/iconpack/pack.go`: `Pack`, `Entry`, `LoadPack`, `Registry.Resolve`, path traversal checks, and shared `rex.BuildProgramIcon` validation.
- `internal/config/config.go`: `Dir()`, `ProgramIconRule`, `gorex.icon_packs`, `gorex.icon_pack_priority`, and `gorex.program_icon`.
- `program_icon.go`, `luaconfig.go`, `settings.go`, `state.go`: load active `iconpack.Registry` and consult it inside `Pane.program()`.
- `internal/cli/pack.go`: `gorex pack ls|install|rm` to manage `<configDir>/icon-packs/`, and `gorex pack install` of a theme pack into `<configDir>/themes/`.
- `iconpacksview.go`: the Extensions page (section `icons`).

## Alternatives

- **WASM plugin runtime (like full Zed extensions)**: Rejected because icon sets are static mappings plus vector/bitmap assets; WASM adds binary size, sandboxing complexity, and cross-platform build friction for no functional gain.
- **Accepting all community logos into the main repo**: Rejected because it bloats the app binary, slows review, and ties icon updates to GoRex release cycles.
- **Directory marker scanning**: Rejected to keep local and remote behavior identical without adding directory I/O or wire protocol fields.

## Security

- Icon paths in `pack.json` must resolve strictly inside the pack directory (`filepath.Rel` check, rejecting `..` and symlinks escaping the pack root).
- SVG and PNG files inside packs enforce the same 32 KiB size cap, `<script` / `on*=` rejection, and PNG dimension bounds (1–512 px) as [`MOD-011`](MOD-011-program-icons.md).

## Migration

Existing configurations without `icon-packs` behave identically to today. Older servers and clients are unaffected because pack resolution runs on the client using existing `SessionInfo` fields.

## Tests

- `internal/iconpack/pack_test.go`: manifest parsing, precedence order, `light`/`dark` overrides, path-traversal rejection, and shared SVG/PNG validation.
- `program_icon_test.go`: `Pane.program()` integration with an active pack and `OSC 7502` precedence toggle.
- `iconpacksview_test.go`: `TestIconPacksSettingsView` lists both kinds with their kind, installs an icon pack and a theme pack from a local registry, uses the theme and goes back to the built-in palette. It waits for each install's own result, so no install still runs when the test's folders go. `TestRegistryEntriesOldIndex` reads an index with no `themes` list.

## Phases

1. Design doc (`MOD-012`, this doc).
2. `internal/iconpack` loader/resolver, `init.lua` wiring, and desktop rendering integration.
3. CLI `gorex pack` management and iOS/Web pack sync or matching.
