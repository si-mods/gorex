---
name: IOS-027
description: "Rex Go replaces the eye button with a badged Needs You inbox entry and separates actionable items from background working tasks. Use when you change the iOS inbox button, badge count, or StatusInbox hierarchy."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# IOS-027: Needs You inbox badge and hierarchy

Status: built.

## Problem

The bottom bar of `HostListView` opens `StatusInbox` from an `eye` button labeled "Show Activity" with no badge. Users cannot see how many programs wait for them without opening the list. Inside `StatusInbox`, actionable states (`blocked`, `error`, `done`) and background `working` tasks share the same visual weight. When no item needs the user and a program still works, the list shows no "Nothing needs you" signal.

## Goal

- Show a count badge on the bottom bar inbox button when actionable items (`blocked`, `error`, or unseen `done`) exist across online hosts.
- Replace the `eye` icon and "Show Activity" copy with a `tray` icon (`tray.fill` when active) and "Needs You" ("待我处理") copy.
- Separate actionable items (`blocked`, `error`, `done`) from background `working` items inside `StatusInbox`.
- Show a compact "Nothing needs you" header state when no actionable items exist and background `working` items still run.

## Non-goals

- Inline approve/deny buttons, snooze, or server-side triage state. Those belong to later changes.
- Changes to the desktop app, the web client, or the Home Screen widget.
- Changes to `Inbox.groups(_:)` ordering or `Host.see(session:block:)` semantics.

## Design

### Behavior

- `Inbox` defines `actionableStates = ["blocked", "error", "done"]`. Background `working` records do not increment the badge.
- `Inbox.summary(_:)` computes `attentionCount` (count of actionable items), `hasError` (whether any item is `error`), and `workingCount` from `[InboxItem]`.
- `Inbox.sections(_:)` splits `[InboxItem]` into `attention` (non-empty groups of `blocked`, `error`, `done` in that order, newest first in each) and `working` (`working` items, newest first).
- The bottom bar button in `HostListView` shows `tray` while All Tabs is active and `tray.fill` while Needs You is active. Its label is "Needs You" when inactive and "Show All Tabs" when active.
- When `attentionCount > 0`, the bottom bar button displays a capsule badge at the top-trailing corner of the tray icon with the count (capped at `99+`). The badge uses red fill with white text when `hasError` is true, and `Color.attention` fill with black text otherwise.
- `StatusInbox` renders a top "Needs You" header with a count pill when `attentionCount > 0`.
- Actionable groups render first with a status dot, section title, item count, and collapse toggle. `blocked` and `error` rows carry a subtle semantic stroke (`Color.attention` or `.red` at 35% opacity).
- When `attention` is empty, `working` is non-empty, and `query` is empty, `StatusInbox` displays a compact "Nothing needs you" row above the `working` section.
- The `working` section renders below a subtle divider when actionable content sits above it, with a quieter header and softer card fill so background progress does not compete with items that wait for the user.
- When multiple hosts exist (`store.hosts.count > 1`), each `StatusRow` includes the host name next to the relative time.

### Model

`InboxToolbarButton` is a child view of `HostListView` that reads `store.hosts.flatMap(\.inbox)`. Because `Host.inbox` reads only `status`, `sessions`, and each block's `statuses` and `seen` (not titles or programs), frequent terminal title updates do not redraw the toolbar button.

## Tests

- `InboxTests` verifies `InboxItem.needsAttention`, `Inbox.summary(_:)`, `Inbox.sections(_:)`, and `Inbox.badgeText(_:)` (including `0`, normal counts, and `99+` cap).
- `xcodebuild` builds and runs `InboxTests` for `GoRex`.
- `ios/scripts/test-notes check` passes with new bullets in `ios/WhatToTest.md`.
- `go run ./tools/verify` passes.
