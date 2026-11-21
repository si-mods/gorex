---
name: IOS-016
description: "A pull down on the host list reconnects each host that is not online, with a tap of the hand at the pull's end. Use when you change HostListView's refresh gesture, HostStore.reconnectDown or Haptics."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-016: Pull to refresh in the host list

Status: built (#945).

## Problem

A host that cannot connect tries again on its own. `Host.scheduleRetry` waits longer after each failure, up to ten seconds. A refused host tries again only when something changes: the app returns to the foreground, or the network changes (`HostStore.networkChanged`, [`IOS-012`](IOS-012-background-and-network-changes.md)). The user cannot ask every host to connect now. The host list has no pull-to-refresh gesture.

## Goal

- A pull down on the host list starts a refresh. The hand feels a tap at the pull's end.
- Each host that is not online dials at once.
- The refresh control stays while a host still dials, for up to three seconds.

## Non-goals

- **A change to the reconnect logic.** `Host.resume` does the work. Nothing in `docs/irreversible.md` changes.
- **A re-dial of the online hosts.** Their sessions arrive on the live control connection. A connection that looks open while its path died heals through the ping (`ControlConnection.pingLoop`) or the network monitor (`NetworkPath`).
- **A fetch of the list's data.** Sessions, projects and program states arrive as events. There is nothing to read again.

## Design

### Behavior

1. The user pulls down on the host list, on the iPhone or in the iPad's sidebar.
2. When the pull lets go, the hand feels one medium tap. The refresh control shows.
3. Each real host that is not online connects anew. A host that waits in its retry backoff dials at once, not after up to ten seconds. A refused host dials again. A host in the middle of a dial starts the dial over.
4. The refresh control stays while a host still dials, for up to three seconds. A host that cannot connect keeps its backoff retries after the refresh ends.

The demo host does not count: it is always online.

### Code

- `HostListView.list` gets `.refreshable` on its `ScrollView`. The gesture then works in the narrow stack and in the wide sidebar.
- `refresh()` calls `Haptics.refreshed()`, then `HostStore.reconnectDown()`. It then waits while the `status` of a host is `.connecting`, for up to three seconds.
- `HostStore.reconnectDown()` calls `Host.resume()` on each host of `resumedOnRefresh`. These are the real hosts whose `status` is not `.online`. `resume()` ends the old connection and dials, as `networkChanged` does for the hosts it resumes.
- `Haptics.refreshed()` is a `UIImpactFeedbackGenerator(style: .medium)`. It sits beside the key bar's light tap.

## Alternatives

- **Resume only the hosts that wait in their backoff**, as `resumedOnNetworkChange` does. Lost: a refused host and a host mid-dial are not online either, and the pull asks for all of them.
- **Wait for each host to be online before the refresh ends.** Lost: a host that cannot come back would hold the refresh control for too long. The cap of three seconds keeps the gesture honest.
- **A tap at the moment the pull passes its threshold.** Lost: SwiftUI reports only that the refresh started. The tap at the start of `refresh()` is the same moment the pull lets go.

## Tests

- `HostStore.resumedOnRefresh` with `FakeRex`: the online host and the demo host stay, a host that waits to retry goes in, and a host that never connected goes in.
- `reconnectDown` makes a refused or a down host dial again: unit test with `FakeRex`.
- xcodebuild and a run in the simulator: the pull shows the refresh control and connects a host whose server was off. `docs/invariants.md` has no item for this gesture.
