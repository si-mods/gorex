---
name: IOS-029
description: "The iPhone key bar aligns with official Rex: three glass capsules with ⌘ and Compose on the left, a continuous horizontally scrolling key strip with soft edge fades in the middle, and the arrow stick, mic, and keyboard toggle on the right. Use when you change the iOS key bar or its scrolling strip."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# IOS-029: Official Rex-aligned iPhone key bar

Status: built (#1200, aligned to official Rex in follow-up).

This doc replaces [`IOS-013`](IOS-013-key-bar-enter.md). It aligns the iPhone `KeyBar` with official Rex: three balanced glass capsules with a continuous, edge-faded scrolling key strip in the middle so every key keeps a fixed, comfortable touch width without pagination or squishing.

## Problem

Paginating the middle capsule into fixed four-key pages squeezed each key down to 22.75 pt on a 393 pt iPhone when the left capsule held both `⌘` and Compose. Merging `+` into the middle capsule and hiding Compose inside a menu made the non-Composer bar look lopsided and forced users who swiped Composer down to hunt for a way to reopen it.

## Goal

- Match official Rex's three-capsule visual balance on iPhone: `[⌘  Compose]` on the left, a continuous edge-faded horizontal key strip in the middle, and `[stick  mic  keyboard]` on the right.
- Keep `Compose` (`square.and.pencil`) one tap away at all times so swiping Composer down never strands the user.
- Give every key in the middle strip a fixed 36 pt touch width, eliminating squished 22 pt buttons and page dots.

## Design

### Behavior

- Left capsule (80 pt in both normal and Composer Mode, staying steady without jumping):
  - `⌘` (`keybar.more`) opens Paste, Add Photo, and Add File, and spins during uploads.
  - `Compose` (`keybar.compose`, `square.and.pencil`) toggles Composer Mode on or off in one tap.
- Middle capsule (flexible width, 12 pt outer row margins):
  - A continuous horizontal `UIScrollView` (`isPagingEnabled = false`) holding fixed 36 pt buttons with `Esc`, `Control`, `Tab`, and `Enter` in the initial visible area, followed by `Option`, `Shift`, `~`, `|`, `/`, `-`, and `Delete`.
  - A `CAGradientLayer` mask softly fades the trailing edge when more keys extend offscreen to the right and fades the leading edge once scrolled right, signaling scrollability without page dots.
- Right capsule (120 pt when Composer Mode is off, 84 pt when on):
  - Holds the arrow stick, the mic (hidden in Composer Mode because it moves into `ComposerBar`), and the keyboard show/hide toggle.
- Composer Mode pins `ComposerBar` above `KeyBar` as described in [`IOS-025`](IOS-025-composer.md).

### Code

- `ios/GoRex/Terminal/TerminalView.swift`: `KeyBar` uses a continuous horizontal `UIStackView` inside `UIScrollView` with `scrollFadeMask` (`CAGradientLayer`) updated in `layoutSubviews()` and `scrollViewDidScroll(_:)`, 12 pt row side margins, 80/44 pt left capsule, and 120/84 pt right capsule.
- `ios/GoRexUITests/ComposerUITests.swift`: verifies `keybar.more` (`⌘`), one-tap `keybar.compose` open/close, grabber dismiss restoring `⌘` and `Dictate`, and Composer Mode layout.

## Tests

- `xcodebuild test -only-testing:GoRexUITests/ComposerUITests` on Mac mini iPhone 17 simulator.
- Simulator screenshots verifying both default non-Composer and Composer Mode states against official Rex.
- No invariant of `docs/invariants.md` names `KeyBar`.
