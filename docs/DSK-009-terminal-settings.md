---
name: DSK-009
description: "The settings.json keys that override the Ghostty configuration move under a terminal.* prefix, with a read fallback to the old keys and a transition that writes both. Use when you add, rename or read a terminal setting in settings.json."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# DSK-009: Terminal settings under `terminal.*`

Status: proposal.

This doc decides the names of the `settings.json` keys that override the Ghostty configuration. It extends the dotted-key style of [DSK-007](DSK-007-sidebar-style.md). It frees the name `theme` from the terminal, which [DSK-008](DSK-008-theme.md) needs for the chrome. `settings.json` is a saved format in `docs/irreversible.md`, so this doc gives numbered options in Open questions. The user selects one before code work starts.

## Problem

Six top-level keys in `settings.json` override the Ghostty configuration. The `settings` struct in `settings.go` holds them, and `ghosttyConfig.withPrefs` applies them:

| Key now | Meaning |
| --- | --- |
| `font` | A font family |
| `theme` | The name of a Ghostty theme |
| `cursor` | `"block"`, `"bar"` or `"underline"` |
| `cursorBlink` | Cursor blink, or Ghostty's value when absent |
| `copyOnSelect` | Copy on selection, or Ghostty's value when absent |
| `minimumContrast` | The least contrast of text, from 1 to 21 |

These keys sit next to keys for the window, the sidebar and the spaces. Their names do not say that they belong to the terminal. The name `theme` is the clearest example. A reader expects it to change the look of the app, but it changes only the terminal colors. DSK-008 needs a key for the theme of the chrome, and `theme` is not available.

## Goal

- Each key that overrides the Ghostty configuration has the prefix `terminal.`.
- A `settings.json` from an older build keeps all its terminal settings after the upgrade.
- A user who goes back to an older build keeps the terminal settings during the transition.

## Non-goals

- `fontSize` stays a top-level key. It sets the text size of the terminals and of the notes.
- `terminalOpacity` stays a top-level key. It is a GoRex setting for the glass, not a Ghostty override. Open question 2 records this.
- The Settings window keeps its pages. The Terminal page and the Theme page write the new keys.
- The web client keeps its own settings (`gorex.settings`). The iOS app is not affected.

## Design

### Behavior

The user sees no change in the Settings window. The settings keep their values after the upgrade.

A user who edits `settings.json` by hand writes the new keys:

```json
{
  "terminal.font": "JetBrains Mono",
  "terminal.theme": "light:Dracula+,dark:Dracula+",
  "terminal.cursor": "bar",
  "terminal.cursorBlink": false,
  "terminal.copyOnSelect": true,
  "terminal.minimumContrast": 3
}
```

An old key still works when its new key is absent.

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

Read rules:

1. When the new key is present, GoRex uses its value.
2. When only the old key is present, GoRex copies its value to the new key.
3. When both keys are present with different values, the new key wins.

Write rules, during the transition:

1. `saveSettings` writes each new key.
2. `saveSettings` also writes the same value to the old key.

After the transition, `saveSettings` writes only the new keys. The read fallback stays, so a file from a much older build still loads.

The transition protects a downgrade. An older build reads only the old keys, and they have the current values. An older build also drops the dotted keys when it saves `settings.json`, as DSK-007 says. The next upgrade then reads the old keys through rule 2, so no value is lost.

Limit: during the transition, a hand edit of a new key does not change the old key until GoRex saves the file. If the user then goes back to an older build, that build shows the value before the edit.

### Code

- `settings.go`: The `settings` struct gets six fields with the dotted JSON names. The six old fields stay as legacy fields. `loadSettings` applies the read rules. `saveSettings` copies the new fields to the legacy fields before it marshals.
- `settings.go`: `ghosttyConfig.withPrefs` reads only the new fields.
- `settingsview.go`: The Terminal page and the Theme page write the new fields.
- `README.md`: The text that names `minimumContrast` names `terminal.minimumContrast`.

## Alternatives

- **A nested object, `"terminal": { "theme": ... }`**: rejected. `settings.json` then has two styles, nested and dotted. DSK-007 chose flat dotted keys, as VS Code does.
- **Write only the new keys from the start**: rejected as the default. A downgrade then loses the six terminal settings, and the terminal uses the Ghostty configuration. Open question 1 keeps it as an option.
- **Keep the top-level names**: rejected. `theme` stays ambiguous, and DSK-008 has no clear key.

## Migration

- An older `settings.json` loads with all values, through read rule 2.
- An older build reads the old keys, which the transition keeps current.
- After the transition, an older build no longer sees changes made in a newer build. It shows the last values from the transition.

## Tests

- **Old file**: A `settings.json` with only the old keys loads, and the six new fields have the values.
- **Both keys**: A file with different values in `theme` and `terminal.theme` uses `terminal.theme`.
- **Save**: After a save, the file has each new key, and each old key has the same value.
- **Downgrade cycle**: A file that has only old keys, as an older build saves it, loads again with the values.
- **Pointer fields**: An absent `terminal.cursorBlink` and an absent `cursorBlink` give `nil`, so Ghostty's value applies.

No item in `docs/invariants.md` covers settings keys.

## Phases

1. **Rename with transition**: New keys, read fallback, write of both keys, Settings window and README.
2. **End of transition**: `saveSettings` stops the write of the old keys. The read fallback stays.

## Open questions

1. **Format and transition**: The user decides. The options are:
   1. Dotted keys, and the transition writes both keys (recommended). A downgrade keeps the terminal settings. The file has six extra keys during the transition.
   2. Dotted keys, and GoRex writes only the new keys. The code is smaller. A downgrade loses the six terminal settings until the user sets them again.
   3. A nested object `"terminal": {...}`, and GoRex writes only the new object. The file has two styles. A downgrade loses the six terminal settings.
2. **`terminalOpacity`**: Keep it, or move it to `terminal.opacity` with the same transition. A move makes the group complete. The key is not a Ghostty override. The user decides.
3. **End of the transition**: The release that starts phase 2. One option is two minor releases after phase 1. The user decides.
