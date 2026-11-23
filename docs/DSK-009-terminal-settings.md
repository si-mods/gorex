---
name: DSK-009
description: "The settings.json keys of the terminal move under a terminal.* prefix, and GoRex migrates the user's file at load. Use when you add, rename or read a terminal setting in settings.json, or change the settings migration."
metadata:
  created: "2026-11-23"
  updated: "2026-11-24"
---

# DSK-009: Terminal settings under `terminal.*`

Status: built (#1061).

This doc decides the names of the terminal keys in `settings.json`, and how GoRex migrates the keys that a user has now. It extends the dotted-key style of [DSK-007](DSK-007-sidebar-style.md). It frees the name `theme` from the terminal. `settings.json` is a saved format in `docs/irreversible.md`. The user selected the format, the migration and the scope.

## Problem

Seven top-level keys in `settings.json` set the terminal. Six of them override the Ghostty configuration, and `ghosttyConfig.withPrefs` applies them. The `settings` struct in `settings.go` holds all seven:

| Key now | Meaning |
| --- | --- |
| `font` | A font family |
| `theme` | The name of a Ghostty theme |
| `cursor` | `"block"`, `"bar"` or `"underline"` |
| `cursorBlink` | Cursor blink, or Ghostty's value when absent |
| `copyOnSelect` | Copy on selection, or Ghostty's value when absent |
| `minimumContrast` | The least contrast of text, from 1 to 21 |
| `terminalOpacity` | The opacity of the terminal background over the glass |

These keys sit next to keys for the window, the sidebar and the spaces. Most names do not say that they belong to the terminal. The name `theme` is the clearest example. A reader expects it to change the look of the app, but it changes only the terminal colors.

## Goal

- Each terminal key has the prefix `terminal.`.
- An upgrade keeps each terminal setting that the user has. The user does not edit the file.
- After the first load, the file of the user contains only the new keys.

## Non-goals

- `fontSize` stays a top-level key. It sets the text size of the terminals and of the notes.
- The Settings window keeps its pages. The Terminal page and the Theme page write the new keys.
- The web client keeps its own settings (`gorex.settings`). The iOS app is not affected.
- A downgrade does not keep the terminal settings. The user selected this cost to keep the file free of duplicate keys.

## Design

### Behavior

The user upgrades GoRex. The terminal looks the same as before, and the Settings window shows the same values.

GoRex changes `settings.json` at the first load:

```jsonc
// Before the upgrade
{ "theme": "Dracula+", "font": "JetBrains Mono", "terminalOpacity": 0.8 }

// After the first load
{ "terminal.theme": "Dracula+", "terminal.font": "JetBrains Mono", "terminal.opacity": 0.8 }
```

GoRex also keeps a copy of the file from before the migration, `settings.json.bak`. A user who goes back to an older build can copy it back.

A user who edits `settings.json` by hand later writes the new keys. If the user writes an old key, GoRex migrates it at the next load.

### Model

The mapping is one to one:

| Old key | New key |
| --- | --- |
| `font` | `terminal.font` |
| `theme` | `terminal.theme` |
| `cursor` | `terminal.cursor` |
| `cursorBlink` | `terminal.cursorBlink` |
| `copyOnSelect` | `terminal.copyOnSelect` |
| `minimumContrast` | `terminal.minimumContrast` |
| `terminalOpacity` | `terminal.opacity` |

`loadSettings` migrates the file in these steps:

1. Read the file.
2. If the file has no old key, stop. The migration does nothing.
3. Write the file as it is to `settings.json.bak`.
4. For each old key, copy its value to the new key if the new key is absent. If both are present, keep the new key.
5. Remove each old key.
6. Write the file with `writeFileAtomic`.

The migration is idempotent. A second load finds no old key and does nothing. Each later step, such as the clamps and checks in `loadSettings`, runs on the migrated values.

A downgrade has these results. An older build does not know the new keys. It uses the Ghostty configuration for the terminal. When it saves `settings.json`, it drops the dotted keys, as DSK-007 says. If the user then sets a terminal value in the older build, the next upgrade migrates that value.

If the migration cannot write the file, GoRex logs the error and uses the migrated values in memory. The next load tries again.

### Code

- `settings.go`: The `settings` struct replaces the seven fields with fields that have the dotted JSON names. A function `migrateSettings(raw map[string]json.RawMessage) bool` does steps 4 and 5 on the raw JSON object. `loadSettings` calls it before it decodes the struct, and writes the backup and the file when it returns true.
- `settings.go`: `ghosttyConfig.withPrefs` and the opacity code read the new fields.
- `settingsview.go`: The Terminal page and the Theme page write the new fields.
- `README.md`: The text that names `minimumContrast` names `terminal.minimumContrast`.

[DSK-008](DSK-008-theme.md) uses the same migration function for the renamed color tokens of `sidebar.colorCustomizations`.

## Alternatives

- **A nested object, `"terminal": { "theme": ... }`**: rejected. `settings.json` then has two styles, nested and dotted. DSK-007 chose flat dotted keys, as VS Code does.
- **Write both the old and the new keys for a transition**: rejected by the user. A downgrade then keeps the settings, but the file holds seven duplicate keys.
- **Read the old keys and never change the file**: rejected. The file then keeps the old names, and a user who reads it does not learn the new names.
- **Keep the top-level names**: rejected. `theme` stays ambiguous.

## Migration

- The first load of the new build migrates the file. The terminal and the Settings window keep each value.
- `settings.json.bak` keeps the file from before the migration.
- An older build after the migration uses the Ghostty configuration for the terminal. The user can copy `settings.json.bak` back to restore the settings.

## Tests

- **Migration**: A file with the seven old keys loads with the values in the seven new fields. The file on disk then has only new keys.
- **Backup**: After the migration, `settings.json.bak` has the bytes of the file from before.
- **Both keys**: A file with `theme` and `terminal.theme` keeps the value of `terminal.theme`, and the file loses `theme`.
- **Idempotent**: A second load does not write the file and does not change `settings.json.bak`.
- **Other keys**: The migration keeps each key that is not in the mapping, with its exact value.
- **Pointer fields**: An absent `cursorBlink` gives a `nil` `terminal.cursorBlink`, so Ghostty's value applies.
- **Write failure**: With a read-only directory, the load uses the migrated values and logs the error.

No item in `docs/invariants.md` covers settings keys.

## Phases

1. **Rename and migrate**: New keys, `migrateSettings`, the backup, the Settings window and README.
