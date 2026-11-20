---
name: IOS-013
description: "The iPhone key bar gets an Enter key as the last key of its first page, and Tab moves to the last page. Use when you change the keys or the pages of the iOS key bar."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-013: Enter key on the key bar

Status: proposal.

## Problem

The iPhone key bar has no Enter key. The bar's first page shows Esc, Control, Option and Tab (`KeyBar` in `ios/GoRex/Terminal/TerminalView.swift`). To send Enter the user must open the soft keyboard. Programs that ask for an answer, such as an agent's question or a shell prompt, need Enter often. One page-swipe for the key that ends most answers is too much.

## Design

The first page of the key bar shows Esc, Control, Option and Enter. Enter is the last key of the page. Its face is the SF Symbol `return`. It sends the `enter` key of `KeyEvent.named`, with the bar's held modifiers. Its accessibility label is "Enter", localized.

Tab moves to the last page, after ~ and |. The last page keeps one slot free. Tab works as before. The iPad bar does not change: its hardware keyboard has Enter, and its assistant keeps its groups.

## Alternatives

- **Replace Tab with Enter and keep no Tab.** Rejected. Programs need Tab for completion.
- **A fifth slot on the first page.** Rejected. Each page holds four slots of equal width. A fifth key would make every page narrower.
- **Enter on the right capsule, with the stick, the mic and the keyboard key.** Rejected. That capsule is 150pt wide for three items. A fourth item squeezes the stick and the mic.

## Tests

- `xcodebuild` builds the app for a simulator.
- A simulator screenshot shows the first page: Esc, Control, Option, Enter; and the last page: ~, |, Tab, one free slot.
- No invariant of `docs/invariants.md` names the key bar.
