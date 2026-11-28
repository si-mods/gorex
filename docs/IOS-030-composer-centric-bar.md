---
name: IOS-030
description: "On iPhone, Composer Mode replaces the separate bottom key bar with an adaptive Composer pill: a single-row 48 pt pill ([+, field, Mic, Send]) at rest that smoothly reflows into a Grok-style two-row card (full-width message field above a [+, …, Mic, Send] action row) as soon as text wraps or adds a newline, while + unfurls an inline 7-key quick-keys strip and a two-stage downward swipe dismisses the keyboard first and exits Composer Mode second. Use when you change Composer Mode's adaptive layout, quick keys, swipe-down behavior, or how Composer Mode hides KeyBar."
metadata:
  created: "2026-11-26"
  updated: "2026-11-29"
---

# IOS-030: Composer-centric iPhone action bar

Status: built (#1268, #1286).

This doc extends [`IOS-025`](IOS-025-composer.md) and [`IOS-029`](IOS-029-key-bar-two-capsules.md). When Composer Mode is on, iPhone hides the separate 52 pt `KeyBar` beneath `ComposerBar` and condenses `ComposerBar` itself into a single-row 48 pt pill, keeping every compose, voice, attachment, and terminal-key action reachable without taking extra vertical height from the terminal.

## Problem

Stacking a multi-row `ComposerBar` (with a dedicated top grabber row and separate bottom action row) takes 112 pt to 170 pt above the keyboard or home indicator, hiding three to five terminal lines even while the compose field holds a single short sentence or nothing at all. Most terminal keys in `KeyBar` are needed only occasionally while driving a coding agent from Composer Mode.

## Goal

- Reclaim both the separate 52 pt `KeyBar` and the second row + grabber row of `ComposerBar` on iPhone so resting Composer Mode uses a single 48 pt pill (`54 pt` with gap, matching `KeyBar` height) without triggering a PTY resize.
- Keep `+` (Attach & Paste), the message field, `Mic`, and `Send` on one horizontal row while empty or typing a short single line, then smoothly reflow into a Grok-style two-row card — full-width message field on top (growing up to `160 pt` before scrolling) and `[ +  …  Mic  Send ]` on the bottom action row — as soon as text wraps or includes a newline.
- Unfurl the full-width 7-key quick-keys strip (`Esc`, `Tab`, `Enter`, hold-to-repeat `Delete`, arrow stick `◎`, `⋯` More Keys, and `✕` collapse) inline across the bottom `36 pt` row via a left swipe on `ComposerBar`, `More Keys` in `+`, or VoiceOver custom actions (with a right swipe collapsing it back and focusing `field`), keeping `≥ 44 × 44 pt` tap targets and multi-line drafts visible above it.
- Keep `Hide Keyboard` and `Show Key Bar` one menu tap away in both `+` and `⋯`, and keep a two-stage downward swipe on `ComposerBar` (without hijacking cursor/selection drags or scrolling inside multi-line text) to dismiss the software keyboard first when up and exit Composer Mode second.

## Non-goals

- Changing the traditional `KeyBar` when Composer Mode is off (`IOS-029`).
- Changing the iPad `inputAssistantItem` shortcut bar.

## Design

### Behavior

- When Composer Mode is on (iPhone):
  - `KeyBar` is hidden and `ComposerBar` anchors directly above `keyboardLayoutGuide.topAnchor` with `ComposerBar.gap` (6 pt).
  - At rest or with a short single-line draft, `ComposerBar` is a single-row `48 pt` glass pill (`radius: 24`, `6 pt` concentric padding around `36 × 36 pt` circular controls): `+` on the left, the single-line message field in the middle, and circular `Mic` + accent `Send` on the right.
  - As soon as the draft contains a newline or wraps past the single-row slot width, `field` reflows in place (without re-parenting or dropping IME marked text) to span the full width of the card above a dedicated `36 pt` bottom action row (`[ +  …  Mic  Send ]`), growing smoothly up to `160 pt` (capped to viewport height in landscape) via an explicit height constraint before scrolling with automatic caret tracking. Clearing or deleting back within a single short line collapses the card smoothly back into the `48 pt` single-row pill. Staged attachments appear in the horizontal thumbnail strip above the text when present.
  - Tapping `+` opens a menu with `Paste` and (when supported) `Photo`, `Camera`, and `File`, followed by an inline terminal section with `More Keys` (`composer.keys.toggle`), `Hide Keyboard` (when the keyboard is up), and `Show Key Bar`.
  - Swiping left on `ComposerBar` (or choosing `More Keys` in `+`) unfurls `quickPill` full-width across the bottom `36 pt` action row (`36 pt` visual height with `44 pt` vertical hit target, `damping 0.82`) while temporarily fading out `+`, `Mic`, and `Send` (and visually hiding `field` via `FieldClipView` only when in single-row mode so an open software keyboard never resigns or drops, keeping multi-line drafts visible above), revealing 7 equal-slot controls `[ Esc  Tab  Enter  Delete  Stick  ⋯  ✕ ]` (each `≥ 44 pt` wide on all iPhones, with hold-to-repeat on `Delete`). Swiping right on `ComposerBar` collapses `quickPill` and focuses `field`, and tapping `✕` (`composer.keys.collapse`) collapses it without raising a closed keyboard. Under Reduce Motion, the transition cross-fades without bounce.
  - Tapping `Esc`, `Tab`, or `Enter` sends the key directly to the terminal, and holding `Delete` repeats backspace. Pressing and dragging `Stick` (`◎`) shows `StickPad` and sends repeated arrow keys, without triggering the bar's downward dismiss pan or push-to-talk long press.
  - Tapping `⋯` (`composer.keys`) opens a native menu with `Ctrl+C`, `Shift+Tab`, plus an inline section with `Hide Keyboard` (when the keyboard is up) and `Show Key Bar` (which turns Composer Mode off and restores `KeyBar`).
  - Dictation (via tapping `Mic` or pressing and holding the bar) cross-fades the bottom `36 pt` row inline into the voice bar (`[ Cancel  waveform + live transcript  Done ]`), keeping any multi-line draft visible above it; `Done` inserts text into the field and never auto-sends.
  - Swiping down on `ComposerBar`: if the compose field is first responder (and the gesture did not start inside an active multi-line text area where touches belong to selection/scrolling), dismisses the keyboard and keeps Composer Mode open; if the keyboard is already down, closes Composer Mode and restores `KeyBar` (where `Compose` reopens it in one tap). VoiceOver exposes `Hide Composer` and `More Keys` as custom accessibility actions on `ComposerBar`.
- When Composer Mode is off:
  - The traditional three-capsule `KeyBar` (`IOS-029`) shows as before.

### Model

- `ComposerBar.baseHeight` is `48 pt` (`6 + 36 + 6`) and `ComposerBar.baseInset` is `54 pt` (`48 + 6`).
- `ComposeTextView` pins `contentOffset` to `.zero` while non-scrolling so synchronous `UITextView` newline scrolling never shifts Line 1 before Auto Layout grows `fieldHeight`, and `ComposerBar` drives `fieldHeight` explicitly (`sizeThatFits` clamped to `[36, effectiveFieldMax]`) so enabling `isScrollEnabled` past `160 pt` never collapses `intrinsicContentSize`.
- `TerminalView.keyBarHiddenForComposer` is true on iPhone while `ComposerBar` is active. `TerminalView.barCover` continues to reserve `KeyBar.height` (`52 pt`) so Ghostty's `band` stays constant (no PTY resize), while `TerminalView.covered` subtracts `KeyBar.height` so the terminal slides up only by the keyboard + excess `ComposerBar` height (`2 pt` at rest, growing smoothly with multi-line text or attachments).
- `TerminalHost.reportedComposerInset` subtracts `KeyBar.height` on iPhone so `PaneBar` and `Reconnecting` sit 10 pt above `ComposerBar`.

### Code

- `ios/GoRex/Terminal/Composer.swift`: `ComposeTextView`, adaptive single-row (`compactConstraints`) ↔ Grok full-width multi-line (`expandedConstraints`) reflow inside `rowBox`, explicit `fieldHeight` + caret auto-scroll, prominent accent `Send` state, inline `quickPill` and `voiceHolder` in the bottom `36 pt` row, horizontal `swipeLeft` / `swipeRight` switching, `accessibilityCustomActions`, and two-stage `draggedDown(_:)`.
- `ios/GoRex/Views/TerminalScreen.swift`: single-row `VoiceCapsule` when `composer` is true, matching the `36 pt` inner height of `ComposerBar`, and initial `composerInset` accounting for `TerminalView.hasBar`.
- `ios/GoRex/Terminal/TerminalView.swift`: exposes `KeyBar.stick`, `TerminalView.sendCtrlC()`, `TerminalView.sendShiftTab()`, and `TerminalView.keyBarHiddenForComposer`; `TerminalHost` hides `terminal.bar` and anchors `ComposerBar` directly to `keyboardLayoutGuide.topAnchor` on iPhone.
- `ios/GoRexUITests/ComposerUITests.swift`: verifies the single-row `ComposerBar` height and button alignment at rest, full-width multi-line reflow above the bottom action row, non-collapsing scrollable height past `fieldMax`, collapse back to single-row on clear, inline quick-keys expansion from `+` and horizontal swipes, and swipe-down / `Show Key Bar` dismissal.

## Tests

- `xcodebuild test -only-testing:GoRexUITests/ComposerUITests/testNoComposerByDefault` on Mac mini iPhone 17 simulator.
- Simulator screenshots of the single-row Composer pill (light and dark, before and after) and inline quick-keys strip.
- No invariant of `docs/invariants.md` names `ComposerBar` or `KeyBar`.
