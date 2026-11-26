---
name: IOS-030
description: "On iPhone, Composer Mode replaces the separate bottom key bar with a single Composer-centric card that places Esc, Enter, the arrow stick, and a More Keys menu (Ctrl+C, Tab, Shift+Tab, Delete, Hide Keyboard, Show Key Bar) in the Composer's bottom action row, and uses a two-stage downward swipe on the grabber to dismiss the keyboard first and exit Composer Mode second. Use when you change Composer Mode's quick keys, grabber swipe behavior, or how Composer Mode hides KeyBar."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# IOS-030: Composer-centric iPhone action bar

Status: accepted.

This doc extends [`IOS-025`](IOS-025-composer.md) and [`IOS-029`](IOS-029-key-bar-two-capsules.md). When Composer Mode is on, iPhone hides the separate 52 pt `KeyBar` beneath `ComposerBar` and places the four high-frequency terminal controls inside `ComposerBar`'s bottom action row, reclaiming 58 pt of vertical terminal height.

## Problem

With the two-row `ComposerBar` stacked above `KeyBar`, the bottom chrome uses 170 pt above the keyboard or home indicator while leaving over 200 pt of empty space in the middle of `ComposerBar`'s bottom action row between `+` and the mic/Send buttons. Most keys in `KeyBar` are rarely needed while driving a coding agent from Composer Mode.

## Goal

- Reclaim the 52 pt `KeyBar` + 6 pt gap on iPhone whenever Composer Mode is active, without triggering a PTY resize.
- Place `Esc`, `Enter`, the arrow stick (`◎`), and a `⌘` More Keys menu in the middle of `ComposerBar`'s bottom action row.
- Tuck less-frequent shortcuts (`Ctrl+C`, `Tab`, `Shift+Tab`, `Delete`, `Hide Keyboard`, and `Show Key Bar`) into the `⌘` menu.
- Make a downward swipe on `ComposerBar`'s grabber dismiss the software keyboard first when it is up, and exit Composer Mode back to the traditional `KeyBar` when the keyboard is already down.

## Non-goals

- Changing the traditional `KeyBar` when Composer Mode is off (`IOS-029`).
- Changing the iPad `inputAssistantItem` shortcut bar.

## Design

### Behavior

- When Composer Mode is on (iPhone):
  - `KeyBar` is hidden and `ComposerBar` anchors directly above `keyboardLayoutGuide.topAnchor` with `ComposerBar.gap` (6 pt).
  - `ComposerBar`'s bottom action row holds `+` (Attach) on the left, immediately followed by a left-aligned quiet muted `⌘` marker pill (`40 × 28 pt`, `composer.keys.toggle`), a flexible spacer, and `Mic` + `Send` on the right.
  - Tapping the quiet marker unfurls the pill rightward (`180 × 34 pt`, `damping 0.82`) from its left edge to reveal `[ Esc  Enter  Stick  ⋯  ✕ ]` while `+`, `Mic`, and `Send` stay fixed. Tapping `✕` smoothly collapses it leftward back into the quiet marker. Under Reduce Motion, the transition cross-fades without bounce.
  - Tapping `Esc` or `Enter` sends the key directly to the terminal. Pressing and dragging `Stick` (`◎`) shows `StickPad` and sends repeated arrow keys, without triggering the bar's downward dismiss pan or push-to-talk long press.
  - Tapping `⋯` (`composer.keys`) opens a native menu with `Ctrl+C`, `Tab`, `Shift+Tab`, `Delete`, plus an inline section with `Hide Keyboard` (when the keyboard is up) and `Show Key Bar` (which turns Composer Mode off and restores `KeyBar`).
  - Swiping down on `ComposerBar`'s grabber: if the compose field is first responder, dismisses the keyboard and keeps Composer Mode open; if the keyboard is already down, closes Composer Mode and restores `KeyBar` (where `Compose` reopens it in one tap).
- When Composer Mode is off:
  - The traditional three-capsule `KeyBar` (`IOS-029`) shows as before.

### Model

- `TerminalView.keyBarHiddenForComposer` is true on iPhone while `ComposerBar` is active. `TerminalView.barCover` continues to reserve `KeyBar.height` so Ghostty's `band` stays constant (no PTY resize), while `TerminalView.covered` subtracts `KeyBar.height` so the terminal slides up only by the keyboard + `ComposerBar` height.
- `TerminalHost.reportedComposerInset` subtracts `KeyBar.height` on iPhone so `PaneBar` and `Reconnecting` sit 10 pt above `ComposerBar`.

### Code

- `ios/GoRex/Terminal/Composer.swift`: adds `escBtn`, `enterBtn`, `stickBtn`, `keysMenuBtn`, `StickPad`, `steer(_:)`, and two-stage `draggedDown(_:)`; excludes `quickKeys` from `holdVoice` and `stickBtn` from `dismissPan`.
- `ios/GoRex/Terminal/TerminalView.swift`: exposes `KeyBar.stick`, adds `TerminalView.sendCtrlC()`, `TerminalView.sendShiftTab()`, and `TerminalView.keyBarHiddenForComposer`; updates `TerminalHost` to hide `terminal.bar` and anchor `ComposerBar` directly to `keyboardLayoutGuide.topAnchor` on iPhone.
- `ios/GoRex/Localizable.xcstrings`: adds `Show Key Bar`, `More Keys`, `Ctrl+C`, and `Shift+Tab` in English and Simplified Chinese.
- `ios/GoRexUITests/ComposerUITests.swift`: verifies the unified Composer card and its quick keys (`Esc`, `Enter`, `Arrow Keys`, `composer.keys`) and grabber dismiss back to `KeyBar`.

## Tests

- `xcodebuild test -only-testing:GoRexUITests/ComposerUITests/testNoComposerByDefault` on Mac mini iPhone 17 simulator.
- Simulator screenshots of the unified Composer card and traditional `KeyBar`.
- No invariant of `docs/invariants.md` names `ComposerBar` or `KeyBar`.
