---
name: IOS-015
description: "Rex Go remembers the panes, tabs and sessions the person viewed on this device, and a Back action returns to them without moving the shared focus. Use when you change pane navigation on iOS, the view record, or the Back button."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-015: Going back to the previous pane

Status: accepted. Not implemented.

This doc adds a Back action to Rex Go. It returns to the pane, tab or session the person viewed before, on this device only. It extends [IOS-005](IOS-005-pane-switching.md) and follows [DOC-001](DOC-001-design-docs.md).

## Problem

Rex Go shows one pane at a time. The person moves between panes with the pane switcher, between tabs with the session sheet, and between sessions with the session list. IOS-005 keeps the last viewed pane on each device, so a tab opens where the person left it.

There is no way back. A person who jumps to another session to answer an agent must find the pane again in a list. A pane that closes under the phone leaves the next pane, not the one before.

## Goal

- A Back action returns to the pane, tab or session the person viewed before.
- The walk works across panes, tabs and sessions on this device.
- A place that is gone, such as a closed pane, is skipped.
- The walk changes only what this device shows. It does not move the shared focus of the session.
- The walk survives an app restart.

## Non-goals

- The server's focus history, `focus_history.reverse` of [MOD-007](MOD-007-focus-history.md). It moves the focus of the session, which IOS-005 says the phone must not do.
- A history shared by devices. Each device has its own walk.
- A full app switcher with previews. This doc adds one Back action.
- History for hosts that are away. Their places stay until the host returns or the record is full.

## Design

### Behavior

- The terminal screen's toolbar and the pane switcher gain a **Back** button. It is disabled when the walk has nothing left.
- One tap moves to the last place the person viewed. A second tap moves to the place before that.
- The walk goes through the panes of this device's places in the order the person viewed them. A place holds a host, a session, a tab and a pane.
- A place that no longer exists is skipped, and the walk continues. A pane that closed is gone. A tab that closed is gone. A session that ended is gone.
- Moving to a place opens its host first when the host is not connected. The app shows the host's connecting state as it does for any open.
- The walk crosses hosts. A place on a host that is away opens that host first.
- Opening any pane, tab or session records the place the person leaves. Opening the same place twice does not record it twice.
- A closed pane under the current view records the pane that replaces it, as any change of view does. The person can then go back to the pane before the closed one.
- The Back button hides in the full-screen terminal when the toolbar hides, and returns with it.

### Model

- The record is a list of places in the order the person viewed them, newest first, with at most 32 places.
- A place holds the host ID, the session ID, the tab's block ID and the pane's block ID.
- The list lives in `UserDefaults` beside the last-view record of IOS-005. A record from an older build becomes a list of one.
- A place is added when the person opens a pane, a tab or a session. A place equal to the newest one is not added again.
- The walk starts below the current place. It skips a place that equals the current view. It also skips a place whose host, session, tab or pane is gone, or that this app cannot show.
- The list is not changed by another device. The server sends no part of it.
- A place is kept when its host is away, and skipped while the host is away or the session is not loaded.

### Code

- `ios/GoRex/Rex/ViewHistory.swift`: the list, its limit, its additions and its walk.
- `ios/GoRex/Rex/Host.swift`: the current view and the current host, as a place, and a check that a place still exists.
- `ios/GoRex/Views/TerminalScreen.swift`: the Back button and its action.
- `ios/GoRex/Views/PaneSwitcher.swift`: a second Back entry for the pane axis, if the design review keeps it.
- IOS-005's last-view record: read once to seed the list, then written from the list's newest place.

## Alternatives

- **Use `focus_history.reverse`**: it moves the shared focus and the desktop follows, which IOS-005 forbids. Lost.
- **Use the navigation stack of SwiftUI**: it covers screens, not changes of pane inside a screen. Lost.
- **A server-side history for the phone**: it duplicates MOD-007 for a device-local need, and it sends the person's viewing to the host. Lost.
- **No persistence**: a relaunch forgets the walk, which is where people notice it most. Lost.

## Migration

- There is no server change and no protocol change.
- The `UserDefaults` record of IOS-005 moves to the newest place of the list. A record it cannot read is dropped, and the walk starts empty.

## Tests

- A unit test records three places and walks back through them. Another adds the same newest place twice, and checks that it is recorded once.
- A test removes a pane, a tab and a session from a fake host and checks that the walk skips each.
- A test fills the list past 32 and checks the oldest place leaves.
- A UI check on the simulator and the Mac mini opens three panes, moves between them, and steps back with the button.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The list, its persistence and the walk.
2. The Back button, its disabled state and the toolbar behavior.
3. Host-away places, and the pane-switcher entry if the review keeps it.
