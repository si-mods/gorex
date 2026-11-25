---
name: MOD-012
description: "Community program icon packs: a declarative plugin format to supply and override program icons, similar to Zed icon themes. Use when designing or implementing icon packs."
metadata:
  created: "2026-11-23"
  updated: "2026-11-25"
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

### Updates

A registry entry changes when the registry moves its `ref` or its `version`. Before this design, GoRex wrote no record of what it installed, so it could not tell a user that an entry changed. The Extensions page showed "Installed" for each installed entry, and the CLI had no update command.

**Install record.** Each install from the registry writes the install record `.gorex-install.json` (`iconpack.Record`): the registry entry's `id`, `kind` (`icon-pack` or `theme`), `version` and `ref`. The `kind` is for a reader of the file; no code reads it. Each field is optional. The record is in the pack's own folder:

- an icon pack: `<configDir>/icon-packs/<id>/.gorex-install.json`;
- a theme: `<configDir>/themes/<id>.assets/.gorex-install.json`. Install makes the folder when the theme has no assets.

The CLI (`gorex pack install <id>`) and the Extensions page install through one function, `iconpack.Install`, so both write the record. An install from a local folder or a Git URL writes no record, and it removes the old record: the icon pack's folder and the theme's asset folder are replaced. A copy of an icon pack skips the record of its source.

**Update check.** `iconpack.HasUpdate(record, entry)` decides. When the entry has a `ref`, a different `ref` is an update. Else a different `version` is an update. The check looks for a difference, not a higher number: the registry's review decides the order. A pack with no record has an update. Its version shows as "version unknown". One update writes its record.

**Extensions page.** The check runs when the page loads the registry: when the page opens, and on **Refresh**. The page makes no other network call and polls nothing. It shows no notification. An installed entry with an update shows **Update** in place of the "Installed" badge, and a hint with the installed version ("Installed: 0.1.0" or "Installed: version unknown"). **Update** runs the steps of **Install** again, with these differences:

- An icon pack keeps its switch. Update does not turn on a pack that the user turned off.
- A theme keeps its file name, so `appearance.theme` still names it. When it is the theme in use, the page applies it again at once (`useTheme`).
- No other setting changes.

The page adds no switch or setting for updates.

**CLI.** `gorex pack update [id]` fetches the registry and installs again each installed pack with an update, or only `id`. It keeps the installed name, also a name that `--name` gave: it finds the registry entry by the record's `id`, else by the installed name. `--check` lists the updates and installs none. `gorex pack ls` shows a theme's recorded version. It does not fetch the registry, so it does not mark updates; `pack update --check` does ([`REF-001`](REF-001-cli.md)).

**Where the record lives, and the alternatives.**

- **A file in the pack's folder** (chosen): the record goes when any build removes or replaces the pack, so it never describes a pack that is gone or a later local install. No build lists it as a pack: `ListPacks` reads only `pack.json`, and `theme.List` reads only `<id>.json` files in `<configDir>/themes`. A theme loads only the assets its manifest names, so a file in `<id>.assets` changes nothing.
- **One `extensions.json` for all packs**: rejected. An older build that removes or reinstalls a pack leaves the entry stale, and then the record is wrong.
- **A file beside the theme, `<id>.install.json`**: rejected. `theme.List` of an older build lists it as a theme with the id `<id>.install`.
- **A field in `pack.json` or the theme's manifest**: rejected. The manifest is the author's file. An older build warns about an unknown key in a theme manifest.

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
- `internal/cli/pack.go`: `gorex pack ls|install|rm` to manage `<configDir>/icon-packs/` and, for theme packs, `<configDir>/themes/`; `ls` and `rm` cover both, and `ls` tells them apart by `kind`.
- `internal/iconpack/update.go`: `Record`, `ReadRecord`, `HasUpdate`, `LookupKind`, and `Install`, which installs a registry entry of either kind and writes its record.
- `iconpacksview.go`: the Extensions page (section `icons`).

## Alternatives

- **WASM plugin runtime (like full Zed extensions)**: Rejected because icon sets are static mappings plus vector/bitmap assets; WASM adds binary size, sandboxing complexity, and cross-platform build friction for no functional gain.
- **Accepting all community logos into the main repo**: Rejected because it bloats the app binary, slows review, and ties icon updates to GoRex release cycles.
- **Directory marker scanning**: Rejected to keep local and remote behavior identical without adding directory I/O or wire protocol fields.

## Security

- Icon paths in `pack.json` must resolve strictly inside the pack directory (`filepath.Rel` check, rejecting `..` and symlinks escaping the pack root).
- SVG and PNG files inside packs enforce the same 32 KiB size cap, `<script` / `on*=` rejection, and PNG dimension bounds (1–512 px) as [`MOD-011`](MOD-011-program-icons.md).

## Migration

Existing configurations without `icon-packs` behave identically to today.

The install record is a new, optional file. GoRex makes no migration step:

- A pack that an older build installed has no record. It loads and lists as before. The Extensions page and `pack update` show its version as unknown and offer the update. The update writes the record.
- An older build ignores the record: it does not list it as a pack, and it reads no field of it. When an older build installs or removes the pack, the record goes with the folder. The pack then has no record, as above.
- A record that a newer build writes with more fields still decodes: `ReadRecord` ignores unknown fields. A record that does not decode counts as none. Older servers and clients are unaffected because pack resolution runs on the client using existing `SessionInfo` fields.

## Tests

- `internal/iconpack/pack_test.go`: manifest parsing, precedence order, `light`/`dark` overrides, path-traversal rejection, and shared SVG/PNG validation.
- `program_icon_test.go`: `Pane.program()` integration with an active pack and `OSC 7502` precedence toggle.
- `iconpacksview_test.go`: `TestIconPacksSettingsView` lists both kinds with their kind, installs an icon pack and a theme pack from a local registry, uses the theme and goes back to the built-in palette. It waits for each install's own result, so no install still runs when the test's folders go. `TestRegistryEntriesOldIndex` reads an index with no `themes` list. `TestExtensionsUpdate` moves the registry's `ref` and checks that **Refresh** shows **Update** with the installed version, that an icon pack with no record shows "version unknown", that the theme in use stays in use with the new colors, and that an icon pack that is off stays off.
- `internal/iconpack/update_test.go`: `TestHasUpdate` (by `ref`, else by `version`; no record), `TestReadRecordOld` (no record, a bad record, an unknown field), `TestInstallRecords` (the record of each kind, a local install that drops it, `Remove`).
- `internal/cli/pack_test.go`: `TestPackUpdate` (no record, `--check`, one id, all, `ls` versions).

## Phases

1. Design doc (`MOD-012`, this doc).
2. `internal/iconpack` loader/resolver, `init.lua` wiring, and desktop rendering integration.
3. CLI `gorex pack` management and iOS/Web pack sync or matching.
