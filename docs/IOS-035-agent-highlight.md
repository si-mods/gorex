---
name: IOS-035
description: "Rex Go highlights agent-driven panes and rows with a calm blue outline and inline agent badge while respecting Reduce Motion, VoiceOver, contrast, and battery rules. Use when you change AgentHighlight.swift, AgentHighlightView.swift, or agentActor handling on iOS."
metadata:
  created: "2026-11-28"
  updated: "2026-11-28"
---

# IOS-035: Agent-driven session and pane highlight

Status: built.

This doc decides how Rex Go marks sessions and panes driven by an agent or CLI script, while keeping motion, VoiceOver, color contrast, and battery rules intact. It brings iOS parity to [`DSK-016`](DSK-016-agent-pane-highlight.md) (`#1281`) and extends [`IOS-005`](IOS-005-pane-switching.md) and [`REF-005`](REF-005-rex-api.md).

## Problem

1. **No visual signal on iPhone or iPad.** When an agent creates or writes to a session through the CLI or peer delegation (`agentActor: {createdBy, lastCliInputAt}`), Rex Go shows the row and pane identically to a human terminal. A user on iPhone or iPad cannot tell that an agent is actively driving the shell before typing into it.
2. **Naive ports break accessibility and battery rules.** Flashing a full border around a 50-60 pt list row creates visual noise on iPhone, continuous timers drain battery when off-screen or backgrounded, ignoring `UIAccessibility.isReduceMotionEnabled` breaks WCAG 2.3 / Apple HIG motion rules, and repeating VoiceOver announcements on every CLI write spams screen-reader users.

## Goal

- Parse `agentActor` (`createdBy`, `lastCliInputAt`, `agent`, `active`, `parentSession`, `tookOverAt`) on blocks across Rex API views, status/process payloads, and block events.
- Render a blue rounded outline on agent-driven panes in `TerminalScreen` (iPad split-view detail and multi-pane terminals) and on agent-driven tiles in `PaneBar` and `PaneMiniMap`.
- Render a compact inline `agent` badge on agent-driven rows in `WindowRow` and `StatusRow` without outlining the full row.
- Breathe on a calm 2.4 s cosine wave (`< 0.5 Hz`, well under 1 flash/s and WCAG 2.3.1's 3/s), switch immediately to a static blue outline with zero animation when `UIAccessibility.isReduceMotionEnabled` (or the `-reduceMotion YES` test default) is on, and pause all animation when off-screen or when `scenePhase != .active` with zero orphan timers.
- Label agent-driven elements for VoiceOver as `"…, operated by an agent"` (`"…，由 agent 操作"` in `zh-Hans`), and announce takeover state edges once without per-input spam.
- Keep dark-mode contrast `>= 3:1` against row and card backgrounds (`#6cb2ff`, matching desktop) and derive light mode in `OKLCH` at the identical hue with lower chroma (`#2f6094`, contrast `> 5:1`) rather than introducing a second hue.
- Clear immediately on human input in that pane (`TerminalSession.type`, file upload, or Live Activity answer) while ignoring terminal query replies, focus reports, and scroll-wheel motion; fade out across the final 3 seconds of the 60 s CLI idle window.
- Play zero haptics for ambient agent state.

## Non-goals

- Blocking human typing while an agent drives a pane. Human input always goes straight to the terminal and clears the highlight on the next frame.
- Changing terminal text or background colors inside Ghostty.
- Adding haptic feedback on agent start, pulse, or expiry.

## Design

### Behavior

1. **iPad and multi-pane outline (`AgentPaneOutline`, `PaneBar`, `PaneMiniMap`)**: When the displayed terminal pane in `TerminalScreen` has an active `AgentActor` within 60 s of `lastCliInputAt` (or creation for non-human `createdBy`), `AgentPaneOutline` draws an inset rounded blue border around the pane. In multi-pane windows (`paneCount > 1`), `PaneBar` and `PaneMiniMap` also outline each agent-driven pane tile in the same blue.
2. **iPhone and list row badge (`AgentWindowBadge`)**: `WindowRow` (in `HostListView`, `HostSection`, and `SessionSheet`) and `StatusRow` (in `StatusInbox`) show a compact `agent` pill before the trailing status mark. List rows never draw a full-row agent border.
3. **Motion and battery**: With motion allowed, ` phase == .active`, and the view on screen, `TimelineView` runs at 24 fps on a 2.4 s cosine wave and fades across `57s..60s`. When `UIAccessibility.isReduceMotionEnabled` is true (observed live via `UIAccessibility.reduceMotionStatusDidChangeNotification`), `TimelineView` stays paused and draws a steady static outline; a single structured `.task` sleeps until the 60 s deadline and cancels automatically on disappear or when `scenePhase != .active`.
4. **VoiceOver**: Agent-driven rows, pane-bar tiles, and `TerminalView` format their accessibility label with `"%@, operated by an agent"` (`"%@，由 agent 操作"`). `AgentVoiceOverAnnouncer` observes boolean state transitions per block after initial mount, posting one announcement when an agent starts operating a shown pane (`false -> true`) or when human input takes over (`true -> false`), with a cooldown gate and zero announcements when repeated CLI writes only advance `lastCliInputAt`.
5. **Color**: Dark mode uses `0x6CB2FF` (`OKLCH(l: 0.750, c: 0.133, h: 252.05)`), giving `5.1:1` contrast against dark cards (`0x363A47`) and `> 3.1:1` even at the minimum pulse opacity (`0.76`). Light mode derives `OKLCH(l: 0.48, c: dark.c * 0.75, h: dark.h)` (`0x2F6094`), keeping the exact `252.05°` hue at lower saturation with `5.4:1` contrast against light cards.
6. **Clear rules**: `TerminalSession.type`, `Host.upload`, and `Host.answerBlock` call `Host.clearAgentLocal(block:)`, latching `agentCleared` at the current `lastCliInputAt` so stale polls cannot re-light the mark until newer CLI input arrives. `AgentHighlight.hasHumanInputBytes` ignores DA/CPR/DECRPM/OSC terminal replies, CSI focus reports, and SGR wheel/motion events.

### Model

`AgentActor` in `ios/GoRex/Rex/AgentHighlight.swift` decodes both camelCase and snake_case keys from `SessionView.Block`, `com.superlogical.terminal.program_status`, `com.superlogical.terminal.process`, `program_status_changed`, `process_changed`, and `agent_actor_changed`. When `active` is omitted (as Go's `omitempty` writes `false`), `AgentActor` treats the mark as inactive when `tookOverAt` is present, and active when `lastCliInputAt` or a non-human `createdBy` is present without `tookOverAt`.

### Code

| File | Role |
| --- | --- |
| `internal/rex/session.go`, `internal/rexapi/seen.go`, `internal/rexapi/events_blocks.go`, `internal/rexapi/help.go` | Expose `agentActor` on `com.superlogical.terminal.program_status` and `program_status_changed`, and emit `agent_actor_changed` block events when CLI input arrives or human input takes over. |
| `ios/GoRex/Rex/AgentHighlight.swift` | `AgentActor`, 60 s window and 3 s fade math, 2.4 s cosine pulse, `OKLCH` color derivation and WCAG contrast helper, `hasHumanInputBytes`, and `AgentVoiceOverAnnouncer`. |
| `ios/GoRex/Views/AgentHighlightView.swift` | `AgentPaneOutline`, `AgentWindowBadge`, `AgentVoiceOverModifier`, live `UIAccessibility.isReduceMotionEnabled` tracking, and structured expiry tasks. |
| `ios/GoRex/Rex/SessionView.swift`, `ios/GoRex/Rex/Host.swift` | `agentActor` decoding on blocks, `Host.Block` local takeover latch and reconciliation, and `windowAgentState`. |
| `ios/GoRex/Terminal/TerminalSession.swift`, `ios/GoRex/Rex/ActivityAnswer.swift` | Immediate local clear on human terminal input, uploads, and Live Activity answers. |
| `ios/GoRex/Views/TerminalScreen.swift`, `ios/GoRex/Views/PaneSwitcher.swift`, `ios/GoRex/Views/HostListView.swift`, `ios/GoRex/Views/StatusInbox.swift` | Pane outline, multi-pane tile outline, inline row badge, and VoiceOver labels. |
| `ios/GoRex/Localizable.xcstrings`, `ios/WhatToTest.md` | English and Simplified Chinese VoiceOver strings and tester release notes. |

## Tests

- **Unit tests (`ios/GoRexTests/AgentHighlightTests.swift`)**: Verify `AgentActor` JSON decoding (`#1281` full shape, `omitempty` takeover shape, and `#1284` minimal `{createdBy, lastCliInputAt}` shape), 60 s active window and `57s..60s` fade, immediate clear on human input and re-arm on newer `lastCliInputAt`, terminal query/focus/wheel filtering in `hasHumanInputBytes`, `Reduce Motion` and `scenePhase` animation gating, `OKLCH` hue parity and `>= 3:1` dark/light contrast, and single-edge VoiceOver announcements without spam.
- **UI tests and simulator verification (`ios/GoRexUITests/AgentHighlightUITests.swift`)**: Capture simulator screenshots for iPad pane outline, iPhone row badge, Reduce Motion static outline (`-reduceMotion YES` and `UIAccessibility.isReduceMotionEnabled`), baseline disabled comparison (`-agentHighlight NO`), VoiceOver accessibility label dump, and clear-on-user-input in the live terminal.
