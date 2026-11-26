---
name: IOS-029
description: "The iPhone key bar uses two glass capsules instead of three, merges More and Compose into a single plus menu, puts Tab on the first page, hides the keyboard button while the keyboard is down, and collapses the key bar while typing in Composer Mode. Use when you change the iOS key bar, its pages, or how Composer Mode stacks above it."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# IOS-029: Two-capsule iPhone key bar

Status: accepted.

This doc replaces the three-capsule iPhone key bar layout of [`IOS-013`](IOS-013-key-bar-enter.md) and the key-bar stacking behavior of [`IOS-025`](IOS-025-composer.md). It gives the four core terminal keys full touch targets and removes the double-bar stack while typing in Composer Mode.

## Problem

On a 393 pt iPhone, `KeyBar` (`ios/GoRex/Terminal/TerminalView.swift`) draws three separate glass capsules in a 361 pt row: a 88 pt left capsule (`⌘` and Compose), a 150 pt right capsule (stick, mic, and keyboard), and the remaining 107 pt middle capsule. After 16 pt of inner margins, each of the four first-page keys gets 22.75 pt of width (18.25 pt on a 375 pt iPhone), which is below Apple's 28 pt minimum touch target.

Three secondary issues add clutter:

- The `⌘` icon opens a Paste, Add Photo, and Add File menu instead of acting as a Command modifier.
- The middle capsule spans three pages (11 keys), placing `Option` on page 1 while `Tab` sits on page 3, and includes `/` and `-` from the system keyboard's first symbol layer.
- In Composer Mode with the software keyboard raised, `ComposerBar` (72 pt + 6 pt gap) stacks on top of `KeyBar` (52 pt) for 130 pt above the keyboard, while `KeyBar` keeps a 52 pt left capsule solely for the Compose button even though `ComposerBar` already has a top grabber to swipe down and close.

## Goal

- Give each of the four primary terminal keys at least 44 pt of touch width on a 393 pt iPhone at rest.
- Replace the misleading `⌘` icon with a single `+` menu that holds Compose, Paste, Add Photo, and Add File.
- Show `Esc`, `Control`, `Tab`, and `Enter` on page 1, and reduce the key bar to two pages with subtle page dots.
- Show the keyboard dismiss button only when the software keyboard is up.
- Collapse `KeyBar` while the software keyboard is up in Composer Mode, without triggering a PTY resize.

## Non-goals

- Changing the iPad `inputAssistantItem` shortcut groups, beyond using the `plus` icon for the `More` menu.
- Adding user-customizable key layouts or macros.

## Design

### Behavior

- On iPhone, `KeyBar` uses two glass capsules separated by 8 pt: a flexible main capsule on the left and a compact tool capsule on the right.
- At rest (no software keyboard, Composer Mode off):
  - The main capsule starts with a 38 pt `+` button (`keybar.more`) and a 1 pt vertical divider, followed by four equal-width keys (`Esc`, `Control`, `Tab`, `Enter`). On a 393 pt iPhone, each key is about 50 pt wide.
  - Tapping `+` opens a menu with `Compose`, `Paste`, `Add Photo`, and `Add File`. While a file uploads, `+` shows an activity indicator.
  - Swiping the key area switches between two pages: page 1 (`Esc`, `Control`, `Tab`, `Enter`) and page 2 (`Shift`, `Option`, `~`, `Delete`). Two small dots at the bottom of the main capsule show the current page.
  - The right capsule holds the arrow stick and the mic (92 pt total, 46 pt each). The keyboard button stays hidden because tapping the terminal raises the keyboard.
- When the software keyboard is up (Composer Mode off):
  - The right capsule reveals the `keyboard.chevron.compact.down` button (`Hide Keyboard`) beside the stick and mic (132 pt total, 44 pt each). The main capsule keeps the same four keys in place so muscle memory does not shift.
- When Composer Mode is on and the software keyboard is down:
  - `ComposerBar` sits above `KeyBar`. `KeyBar` hides the `+` button, the divider, and the mic (which lives inside `ComposerBar`), leaving the four keys in the main capsule and the arrow stick alone (48 pt) in the right capsule. Swiping down on `ComposerBar`'s top grabber closes Composer Mode.
- When Composer Mode is on and the software keyboard is up:
  - `TerminalHost` collapses `KeyBar` (fading it to alpha 0 and disabling hit testing) and anchors `ComposerBar` directly above `keyboardLayoutGuide.topAnchor` with `ComposerBar.gap`. Bottom chrome drops from 130 pt to 72 pt, and `PaneBar` and `Reconnecting` move down with `ComposerBar`. Dismissing the keyboard restores `KeyBar`.
- While voice dictation runs outside Composer Mode, the voice capsule replaces the full two-capsule row.

### Model

- `KeyBar` tracks two orthogonal UI flags on iPhone: `composerOn` and `keyboardVisible`.
- Right capsule width is derived from visible items: 48 pt (1 item), 92 pt (2 items), or 132 pt (3 items).
- `TerminalHost` computes `keyBarCollapsed = composer != nil && keyboardVisible && UIDevice.current.userInterfaceIdiom != .pad`.
- `TerminalView.barCover` keeps counting `KeyBar.height` on iPhone even while `keyBarCollapsed` is true so Ghostty's bottom inset (`band`) never changes and no PTY resize runs. `TerminalView.covered` subtracts `KeyBar.height` while `keyBarCollapsed` is true so the terminal slides up only as far as the keyboard and `ComposerBar`.

### Code

- `ios/GoRex/Terminal/TerminalView.swift`:
  - `KeyBar` implements `UIScrollViewDelegate` to update the two-dot page indicator, merges `more` (`plus`) and divider into the main capsule, updates pages to `[[escape, ctrl, tab, enter], [shift, alt, tilde, delete]]`, and adds `setKeyboardVisible(_:)`.
  - `TerminalHost` switches `composerBottom` between `terminal.bar.topAnchor` and `keyboardLayoutGuide.topAnchor`, updates `terminal.keyBarCollapsed`, adjusts `placeBottomEdge()` and `reportedComposerInset`, and exposes `setKeyboardVisible(_:duration:options:)`.
  - `TerminalView.keyboard(_:)` forwards `keys > 0` to `TerminalHost.setKeyboardVisible(_:duration:options:)` before sliding.
- `ios/GoRexUITests/ComposerUITests.swift`: verifies `+` menu opening Composer Mode, `Tab` on page 1, `Hide Keyboard` visibility, and `KeyBar` collapsing when the keyboard is up in Composer Mode.

## Alternatives

- **Keep three capsules and shrink the left capsule to 44 pt (`Plan B`).** Leaves 38.25 pt per key at rest, keeps the redundant keyboard-show button at rest, and still stacks 130 pt while typing in Composer Mode. Lost.
- **Swap `Enter` out when the software keyboard is up.** Breaks muscle memory on page 1 when the keyboard opens and closes. Lost.

## Tests

- `xcodebuild` builds `GoRex` for `generic/platform=iOS Simulator`.
- `ComposerUITests` verifies the default two-capsule bar, opening Composer Mode via `+`, dismissing via grabber, and collapsing `KeyBar` while typing in Composer Mode.
- Simulator screenshots verify default rest, keyboard up, Composer rest, and Composer with keyboard on iPhone.
- No invariant of `docs/invariants.md` names `KeyBar`.
