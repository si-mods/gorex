---
name: IOS-020
description: "How Rex Go shows a place with nothing in it: the page-size ContentUnavailableView, the row-size EmptyState view, and friendly copies for failed loads. Use when you add an empty, no-results or failed state to an iOS view."
metadata:
  created: "2026-11-22"
  updated: "2026-11-22"
---

# IOS-020: Empty, no-results and failed states

Status: built.

## Problem

A place with nothing in it used to show a blank, a raw error or a stale label. The host list's search left the area under the headers blank. Activity said `Nothing needs you` when the search hid every item. `RemoteHostsView`'s Hosts section kept a header with no row. Ports, the folder browser and the pair sheets showed `error.localizedDescription`, which a user cannot read.

## Goal

- Every place a search can empty names the state.
- Every card, row or section that can be empty names the state in one faint line.
- A failed load says what happened in the user's terms and offers the way to try again where one exists.
- A raw error stays where diagnostics surfaces it: `NetworkDiagnostics`, the developer page's debug info.

## Design

### The two sizes

The title always names the state in the app's voice: terse, sentence case, for example `No hosts yet` or `Couldn't load ports`. It does not describe the cause.

| Size | View | Use |
| --- | --- | --- |
| Page | `ContentUnavailableView`, the system's own | A whole page, a sheet's content or a search's no-results |
| Row | `EmptyState` in `ios/GoRex/Views/EmptyState.swift` | Inside a card, a list row or a section |

`EmptyState` is an `HStack`: an optional SF Symbol in hierarchical rendering, the title in `.subheadline` secondary, and an optional trailing `.glass` button for the way out. A caller in a `List` applies `.listRowBackground(Color.card)` as the other rows do.

`ContentUnavailableView.search(text:)` is the no-results state. Its system image arguments take no modifier, so those symbols stay as they are. Where the code builds the `Image` or `Label` itself, the symbol gets `.symbolRenderingMode(.hierarchical)` and `.secondary`.

### Where each shows

| Place | State | View |
| --- | --- | --- |
| Host list, search | No row of any section matches | `ContentUnavailableView.search` |
| Activity (`StatusInbox`), search | Nothing the search kept | `ContentUnavailableView.search` |
| Activity, no search | All done | `Nothing needs you` with `checkmark.circle`, no button |
| Settings, search | No row matches | `ContentUnavailableView.search` |
| Settings › Themes, filter | No theme matches | `EmptyState` `No matching themes` |
| Remote Hosts | No host added | `EmptyState` `No hosts yet` in the Hosts section |
| Session sheet | A session with no windows | `EmptyState` `No windows` in the card |
| Share on Hotspot | No device admitted | Faint row `No devices admitted yet` |
| Share on Hotspot | Web client download failed | `Couldn't download the web client.` and a `Try again` button |
| Ports, first load | Nothing read yet | Centered `ProgressView` |
| Ports, load failed | No list to show | `ContentUnavailableView` `Couldn't load ports`, `Try again` runs `fetch` |
| Folder browser | The call never reached the host | `Couldn't reach the host.` |
| Pair sheets | Pairing failed | `Couldn't pair. Try again on both devices.` |
| Host section, offline | The host cannot connect | `Offline`, then `Couldn't reach it. Trying again.` |

The host section in a space with no projects is the one exception: it shows its header and the `+` alone, with no line. The empty area is the host's own content, and the `+` beside it names the way to fill it.

### Failed loads

The view maps the error at the display site, not in the model. The model keeps the raw error for diagnostics. A URL error or a `RexError` of a call that never reached the server (`offline`, `timeout`, `closed`) reads as `Couldn't reach …`. A load that offers a retry puts a `Try again` button in the state.

`PortsView` keeps its inline warning row while a list is on screen: a failed refresh or a failed relay covers content that is still true.

## Non-goals

- No change to `ContentUnavailableView` call sites that name a state already (`FeaturesView`, `InputTraceView`, `No Tab Open`, `QRScanner`).
- No new filtering of rows that the search does not filter today, such as a host away or a session's header.

## Tests

- `xcodebuild` for the `GoRex` scheme compiles the new views.
- `ios/scripts/test-notes check` passes: `ios/WhatToTest.md` gains a bullet in each locale.
- The new strings have `en` and `zh-Hans` entries in `ios/GoRex/Localizable.xcstrings`.
- Screenshots compare the states before and after, in light and dark.
