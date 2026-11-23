---
name: IOS-021
description: "A long-press drag on a host's header in the list moves the host to a new place, saved in the order the drag leaves. Use when you change HostListView's host drag, HostStore.move(toOrder:) or the dev.gorex.host drag type."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# IOS-021: Dragging a host to reorder the list

Status: built.

## Problem

The host list shows the hosts in the order they were added. The user cannot move a host down to the end, for example one that is mostly offline. The only reorder is in Settings › Remote Hosts, behind an Edit button, and a user cannot find it. The user tries to drag the host's section in the list, and nothing moves.

## Goal

- A long-press drag on a host's section header lifts the section. The user moves it to a new place in the list.
- The sections that the drag passes over make way, as the tab rows do on a drag.
- The drop saves the order. `HostStore` keeps it in the `hosts` default, as the Settings list's Edit mode does.
- The drag does not start while the search filters the list. The demo host does not drag.

## Non-goals

- **A change to the saved format.** `HostStore.move` already writes the `hosts` default. Nothing in `docs/irreversible.md` changes.
- **A change to Settings › Remote Hosts.** Its Edit mode keeps its `.onMove` reorder.
- **A drag on the section's body.** The tab rows keep their own drags and the project rows their menus. The header lifts the host.

## Design

### Behavior

1. The user holds a host's header in the list, on the iPhone or in the iPad's sidebar. The section lifts, dimmed, and the hand feels a tap.
2. The drag passes over another section. The dragged host moves to that section's place in the order the list shows, and the other sections make way.
3. The user lets go on a section or on the drop zone at the list's end. The host takes its place. The order is saved.
4. The user lets go off the targets. The drag ends and the hosts keep their old order.

The drag's order holds every host, the ones a space hides too. A hidden host keeps its place when the order saves. The demo host stays at its place, as `HostStore.move` leaves it there.

### Code

- `HostListView` keeps `hostDrag`: the lifted host's id and the hosts' ids in the order the drag leaves them. `arranged` sorts the shown hosts by that order while a drag runs.
- `liftHost` makes the item, a `Lifted` over an `NSString`. A drop target sees only the types the item's object gives, and a type the source `register`s alone (`dev.gorex.host`, marked so the source knows its own) never reaches one — the string gives the item `public.text` on the drop side, where the section's targets can take it.
- `HostSection` takes `liftHost: (() -> NSItemProvider)?` and puts `.onDrag` on the header. `HostListView` passes nil for the demo host and while the search is not empty.
- The deepest drop target at a point is the one a drop goes to; a target wrapping the section never sees it. So the section's own `.text` targets — the catch-all over the section and each tab row's — take a `HostDrop`, the list's hand for a host's drag. While a host is dragged (`active`), their `pass` moves the host in the drag's order and their `drop` lands it; otherwise they answer as the rows' targets do.
- A drop zone under the last section takes the drag too, for a host that goes to the end. Its `RowDrop.validate` is `hostDrag != nil`, so a tab row's drag falls through to `PageDrop` instead.
- `dropHost` calls `HostStore.move(toOrder:)`, which orders the real hosts as the drag's order lists them and saves. The demo host keeps its place. An order that no longer matches the hosts does nothing.
- `Lifted.mark` gains a `type` parameter so `markHost` is not a second copy. `Lifted.ended`, `PageDrop` and `DragEnd` work as they do for the tab rows: a drop off the targets posts `ended` and `hostDrag` clears.

## Alternatives

- **A Move Up and Move Down item in the host's menu.** Lost: the user already tries the drag, and a menu asks for one tap per place moved.
- **An Edit mode on the list.** Lost: a second mode for one gesture the system already gives for free, and the drag of the tab rows needs no mode either.
- **A drag on the whole section.** Lost: a long-press on a project row would lift the host in place of the row's menu.
- **`.onMove` in a SwiftUI `List`.** Lost: the host list is a `LazyVStack` of sections, not a `List`, and the drop targets must let the tab rows' drags pass.

## Tests

- `HostStore.move(toOrder:)`: the order applies, the demo host keeps its place, and an order with a missing or a foreign id changes nothing.
- xcodebuild and a run in the simulator: a host dragged over another moves, a host let go at the end moves there, and a drop off the targets keeps the order.
