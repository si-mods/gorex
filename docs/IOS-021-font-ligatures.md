---
name: IOS-021
description: "Rex Go can switch the terminal font's ligatures off, so pairs like != draw as separate characters. Use when you change the iOS terminal font settings or the Ghostty config it builds."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# IOS-021: A ligatures switch for the terminal font

Status: built.

## Problem

The terminal font's OpenType ligatures always draw. In JetBrains Mono and Maple Mono, pairs such as `!=`, `=>` and `>=` become one glyph. Some users read the separate characters better, and some programs align text with them in mind. Settings offered no way to turn the shaping off.

## Design

### Behavior

Settings › Terminal gains a Ligatures switch under the Font section. It is on by default. On keeps the behavior the app had: the font's ligatures and contextual alternates apply. Off draws each character as the font's plain glyph.

The switch applies to every open terminal at once, and to terminals opened later. A footnote under the switch says what ligatures do. The value syncs through iCloud to the user's other devices, as the font family and size do ([`IOS-008`](IOS-008-icloud-sync.md)).

### Code

`TerminalSettings` in `ios/GoRex/Views/SettingsView.swift` holds the switch in `@AppStorage("fontLigatures")`, which defaults to true. A change posts `.terminalFont`, the notification the font family and size already use, so no new notification exists.

`TerminalView.fontLigatures` reads the key from `UserDefaults.standard`. An absent key reads as on, so an existing install keeps its glyphs.

`TerminalView.setTheme` builds the Ghostty config text. When the switch is off it appends one `font-feature` line per feature, with a minus: `-liga`, `-clig`, `-dlig`, `-hlig` and `-calt`. Together they cover the programming ligatures the bundled fonts shape, which `calt` and `liga` implement. `fontChanged` rebuilds the same config on `.terminalFont`, so open terminals follow the switch at once. `TerminalSession` calls `setTheme` for every terminal, so a new one takes the lines too.

`CloudSettings.keys` lists `("fontLigatures", true, .terminalFont)`. A Bool in `UserDefaults` reads as `NSNumber`, so the sync stores it as a number, and a value that arrives from the cloud posts `.terminalFont`.

## Tests

- `testLocalPushedWhenCloudEmpty` in `ios/GoRexTests/CloudSettingsTests.swift` pushes a local `false` and finds `0.0` in the cloud.
- `testCloudWinsAtStart` takes a cloud `1.0` and finds the local Bool true.
- `GoRexTests` run in CI (`.github/workflows/ios-test.yml`).
- The shaping itself is checked on a device or the simulator: with the switch off, `!=` and `=>` in JetBrains Mono draw as separate characters.
