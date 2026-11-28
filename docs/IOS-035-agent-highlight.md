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

- Parse `agentActor` (`createdBy`, `createdAt`, `lastCliInputAt`, `agent`, `active`, `parentSession`, `tookOverAt`) on blocks across Rex API views, status/process payloads, and block events.
- Render a blue rounded outline on agent-driven panes in `TerminalScreen` (iPad split-view detail and multi-pane terminals) and on agent-driven tiles in `PaneBar` and `PaneMiniMap`.
- Render a circular `sparkles` corner mark (`AgentTileBadge`) on the top-right corner of the program tile in `WindowRow` (`ProgramTiles`), `StatusRow`, and `TerminalScreen`'s header pill—with **no** inline text label or full-row border.
- Breathe on a calm 2.4 s cosine wave (`< 0.5 Hz`, well under 1 flash/s and WCAG 2.3.1's 3/s), switch immediately to a static blue outline with zero animation when `UIAccessibility.isReduceMotionEnabled` (or the `-reduceMotion YES` test default) is on, and pause all animation when off-screen or when `scenePhase != .active` with zero orphan timers.
- Label agent-driven elements for VoiceOver as `"…, operated by an agent"` (`"…，由 agent 操作"` in `zh-Hans`), and announce takeover state edges once without per-input spam.
- Keep dark-mode contrast `>= 3:1` against row and card backgrounds (`#6cb2ff`, matching desktop) and derive light mode in `OKLCH` at the identical hue with lower chroma (`#2f6094`, contrast `> 5:1`) rather than introducing a second hue.
- Keep the highlight **persistent** on agent-created panes (`createdBy` is `"cli"` or `"delegate"`) while `tookOverAt == nil`—never decaying over time across working, idle, or background-to-foreground transitions.
- Set `tookOverAt` and **permanently clear** the highlight only on genuine human input (`TerminalSession.type` keyboard/paste, `Host.upload` file upload, or `Host.answerBlock` Live Activity answer), and clear on session exit/close; reserve the 60 s CLI idle window (`57s..60s` 3 s fade-out) exclusively for human-created panes (`createdBy == "human"`) that a script wrote to once.
- Play zero haptics for ambient agent state.

## Non-goals

- Blocking human typing while an agent drives a pane. Human input always goes straight to the terminal and clears the highlight on the next frame.
- Changing terminal text or background colors inside Ghostty.
- Adding haptic feedback on agent start, pulse, or expiry.

## Design

### Behavior

#### Highlight lifetime and human takeover

Rex Go follows the exact same four lifetime rules as desktop GoRex ([`DSK-016`](DSK-016-agent-pane-highlight.md#highlight-lifetime-and-human-takeover)) so iOS and desktop never drift:

1. **Agent-created panes stay highlighted (`createdBy` in `{"cli", "delegate"}` and `tookOverAt == nil`)**: Panes opened by `gorex delegate`, `gorex new`, or `gorex split` belong to the agent from creation. Their program-tile top-right `sparkles` corner mark (`AgentTileBadge`) and blue pane outline remain resident (`isPersistent == true`, `fade == 1.0`) without decaying after 60 seconds—whether the agent inside is `working`, waiting `idle` at its prompt, or the app suspends to the background and returns later. Delegated agents (`gorex delegate`) receive their initial prompt once at launch and then work autonomously for minutes without further CLI writes, so a 60-second `lastCliInputAt` timer would falsely hide active agent panes.
2. **Only genuine human input sets `tookOverAt` and permanently clears the highlight**: Typing or pasting in `TerminalSession.type`, uploading a file (`Host.upload`), or answering a prompt from a Live Activity (`Host.answerBlock`) sets `tookOverAt` and flips `active` to `false`, permanently clearing the highlight for that takeover. Non-typing terminal bytes—query replies (`DA`, `CPR`, `DSR`, `DECRPM`, `XTWINOPS`, Kitty flags, `OSC`/`DCS`/`APC`), `OSC 5522` clipboard read denials (`EPERM`/`DONE`), focus reports (`CSI I`, `CSI O`), pointer/scroll/tap reports (`SGR`, `URXVT`, `X10`), and Kitty `:3` key-release or modifier-only events—never trigger takeover on either the client or the server.
3. **Session exit or close clears the highlight**: `child_exited` (`exited == true`) or closing the pane/window clears the badge and outline immediately.
4. **The 60-second CLI idle window applies only to human-created panes (`createdBy == "human"`) touched by a script**: When a person opens an interactive shell and a helper script sends input once (`lastCliInputAt != nil`, `tookOverAt == nil`), the terminal is still the person's own shell rather than a dedicated agent pane. Keeping a permanent blue outline on a human shell after a one-shot `gorex send` would be noisy, so `createdBy == "human"` panes fade out across the final 3 seconds (`57s..60s`) of the 60-second CLI idle window.

#### Surfaces, motion, VoiceOver, and color

1. **iPad and multi-pane outline (`AgentPaneOutline`, `PaneBar`, `PaneMiniMap`)**: When the displayed terminal pane in `TerminalScreen` has a non-nil `AgentHighlight.state`, `AgentPaneOutline` draws an inset rounded blue border around the pane. In multi-pane windows (`paneCount > 1`), `PaneBar` and `PaneMiniMap` also outline each highlighted pane tile in the same blue.
2. **Program tile corner mark (`AgentTileBadge`)**: `ProgramTiles` (in `WindowRow` across `HostListView`, `HostSection`, and `SessionSheet`), `StatusRow` (in `StatusInbox`), and the title capsule's `ProgramTile` in `TerminalScreen` place a 12 pt circular blue `sparkles` icon mark with a dark/light cutout rim on the top-right corner of the front program tile (shifting `DoneDot` slightly left if both are shown). Rows render no `"agent"` text label and never draw a full-row agent border.
3. **Motion and battery**: With motion allowed, `phase == .active`, and the view on screen, `TimelineView` runs at 24 fps on a 2.4 s cosine wave (and fades across `57s..60s` for human-created scripted panes). When `UIAccessibility.isReduceMotionEnabled` is true (observed live via `UIAccessibility.reduceMotionStatusDidChangeNotification`), `TimelineView` stays paused and draws a steady static outline. Persistent agent-created panes schedule **zero** expiry sleep tasks; human-created scripted panes schedule a single structured `.task` until their 60 s deadline that cancels automatically on disappear or when `scenePhase != .active`.
4. **VoiceOver**: Agent-driven rows, pane-bar tiles, and `TerminalView` format their accessibility label with `"%@, operated by an agent"` (`"%@，由 agent 操作"`). `AgentVoiceOverAnnouncer` observes boolean state transitions per block after initial mount, posting one announcement when an agent starts operating a shown pane (`false -> true`) or when human input takes over (`true -> false`), with a cooldown gate and zero announcements when repeated CLI writes only advance `lastCliInputAt`.
5. **Color**: Dark mode uses `0x6CB2FF` (`OKLCH(l: 0.750, c: 0.133, h: 252.05)`), giving `5.1:1` contrast against dark cards (`0x363A47`) and `> 3.1:1` even at the minimum pulse opacity (`0.76`). Light mode derives `OKLCH(l: 0.48, c: dark.c * 0.75, h: dark.h)` (`0x2F6094`), keeping the exact `252.05°` hue at lower saturation with `5.4:1` contrast against light cards.

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

- **Unit tests (`ios/GoRexTests/AgentHighlightTests.swift`)**: Verify `AgentActor` JSON decoding (`#1281` full shape, `omitempty` takeover shape, and `#1284` minimal `{createdBy, lastCliInputAt}` shape), persistent highlight across idle/working/hours for `createdBy` in `{"cli", "delegate"}` until `tookOverAt` or exit (`testAgentCreatedHighlightPersistsUntilHumanTakeoverOrExit`, `testHostKeepsAgentCreatedActiveAndExpiresOnlyHumanCreatedAfterSixtySeconds`), 60 s window and `57s..60s` fade restricted to `createdBy == "human"` scripted panes (`testSixtySecondWindowAppliesOnlyToHumanCreatedPanes`), immediate clear on human input with non-human query/focus/mouse/wheel/release filtering (`testUserInputClearsImmediatelyAndNewerCLIInputRearms`), `Reduce Motion` and `scenePhase` animation gating, `OKLCH` hue parity and `>= 3:1` dark/light contrast, and single-edge VoiceOver announcements without spam.
- **UI tests and simulator verification (`ios/GoRexUITests/AgentHighlightUITests.swift`)**: Capture simulator screenshots for iPad pane outline, iPhone row badge, Reduce Motion static outline (`-reduceMotion YES` and `UIAccessibility.isReduceMotionEnabled`), baseline disabled comparison (`-agentHighlight NO`), VoiceOver accessibility label dump, and clear-on-user-input in the live terminal.
