---
name: IOS-012
description: "Rex Go keeps its host connections for a short grace on every trip to the background, not only from a terminal, and reconnects at once when the phone changes network. Use when you change the background grace, BackgroundGrace, HostStore.foreground, or how Rex Go notices a dead connection."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-012: Connections across the background and network changes

Status: proposal.

This doc extends the grace of [`IOS-006`](IOS-006-live-activities.md), section "Grace". It makes the grace app-wide, ties its end to the time that iOS gives, and adds a reconnect when the network changes. It compares Rex Go with rootshell, another iOS terminal, and takes two of its four methods. It does not keep the app running after the grace: [`IOS-006`](IOS-006-live-activities.md), Alternatives b, rejected that.

## Problem

Rex Go has a grace and a resume today. Three gaps stay.

1. **The grace needs an open terminal.** Only `TerminalScreen` starts the grace, through `TerminalLive.background`. When the user leaves the app from the host list, the pane gallery or the inbox, iOS gets no background task. It suspends the app in seconds. The control connections freeze, so a program that asks for input (`Host.blocked`, `Notifier`) posts no notification after that moment.
2. **The grace has a fixed length.** `BackgroundGrace` ends after 25 seconds (`limit`) or when iOS calls the expiration handler. It does not read `UIApplication.backgroundTimeRemaining`. When iOS gives less time, the expiration handler ends the grace. Then the paused update of the Live Activity gets no time to finish (`BackgroundGrace.expire`, `byIOS`). A memory warning in the background does not end the grace.
3. **A network change is found late.** A terminal's data stream (`DataConnection`) sends no pings. The control connection pings every 30 seconds and waits 15 seconds for the pong (`ControlConnection.pingEvery`, `pongWait`). After a change from Wi-Fi to cellular in the foreground, Rex Go can take up to 45 seconds to find the dead path. **[assumption]** During that time the terminal shows no output and holds what the user types. The tailnet engine already watches the network (`TailnetEngine.PathMonitor`), but the hosts do not use this signal.

### What rootshell does

rootshell keeps its connections with four methods. The code is in its repository: `BackgroundExecutionCoordinator`, `LocationDiaryManager`, and the `push/` directory.

| Method | rootshell | Rex Go today | This doc |
| --- | --- | --- | --- |
| 1. Short grace | One app-wide `beginBackgroundTask` when any remote session or tunnel is open. It ends 5 s before `backgroundTimeRemaining` runs out, and on a memory warning. | Only with an open terminal. Fixed 25 s. | Takes it. |
| 2. Long keep-alive | Background location updates ("Location Diary") keep the app running. | None. | Does not take it. [`IOS-006`](IOS-006-live-activities.md) rejected it. |
| 3. Resumable transport | Mosh and tssh resume after a suspension or a network change. | `RESUME` with the token and the byte count (`DataHandshake.resume`). The server keeps the screen (`REF-005`). A network change waits for the ping. | Takes the reconnect on a network change. |
| 4. Push | End-to-end encrypted push through a stateless relay. | Planned in [`IOS-006`](IOS-006-live-activities.md), phases 2 and 3. | Not here. |

rootshell also saves its window state and its scrollback on the device before the grace ends. Rex Go does not need this. The GoRex server keeps the screen and up to 256 KiB of scrollback, and a stream that comes back gets them (`REF-005`, "A terminal's data stream can start over").

## Goal

- When Rex Go goes to the background with any host connected, iOS gives it a background task. A terminal screen is not necessary.
- A program that asks for input during the grace posts a notification, from any screen.
- The grace ends 5 seconds before `backgroundTimeRemaining` runs out, or after 25 seconds, whichever comes first. The paused update of the Live Activity has those 5 seconds.
- A memory warning in the background ends the grace at once.
- After a change of network in the foreground, each connected host and each open terminal reconnects within 2 seconds of the change. **[assumption]** The time to measure is from the log line `network <name>` to the log line `resumed <block>`.
- No terminal loses output in these reconnects. A resume continues from the byte count.

## Non-goals

- **Keeping the app running after the grace.** See [`IOS-006`](IOS-006-live-activities.md), Non-goals.
- **Updates while the app is suspended.** These are the push phases of [`IOS-006`](IOS-006-live-activities.md).
- **A save of the screen on the device.** The server keeps it.
- **A change to the server or to the wire protocol.** The design uses `RESUME` as it is. Nothing in `docs/irreversible.md` changes.
- **The desktop app and the web client.** They do not suspend. The lesson "Path shared by local panes, remote hosts and iOS" does not apply, because only the iOS client changes.

## Design

### Behavior

1. The user opens Rex Go. One or more hosts connect.
2. The user goes to another app, from any screen.
3. For up to 25 seconds, Rex Go keeps running. The hosts stay connected. A program that waits for input posts a notification. An open terminal keeps its stream and its Live Activity, as today.
4. If the user comes back during the grace, nothing reconnects.
5. When the grace ends, the open terminal suspends its streams as today. The Live Activity says that it is paused.
6. When the user comes back after the grace, the hosts and the streams resume as today (`HostStore.foreground`).

On a network change in the foreground:

1. The phone changes from Wi-Fi to cellular, from one Wi-Fi to another, or from no network to a network.
2. Within 1 second, Rex Go reconnects each connected host and resumes each open terminal stream.
3. A reconnect that takes more than a second shows the reconnect note, as today. A shorter one shows nothing.

### Model

- **One grace for the app.** The app owns one `BackgroundGrace`. The grace starts on `.background` if a host has a control connection or a terminal has a stream. It does not start in Demo Mode with no real host. A host with no running program also starts it. The user chose this (2026-11-21) over a grace only for hosts with a running program, because one rule is simpler and a program can start to wait at any time.
- **Work at the end of the grace.** Owners register their work with the grace. `TerminalLive` registers the suspend of the streams and the paused update. The grace runs all the work, then ends its task when all the work has finished, or at once when iOS takes the time back.
- **The deadline.** The grace checks `backgroundTimeRemaining` every 5 seconds. It ends at `min(25 s, remaining − 5 s)`. A value that is not finite means that iOS gives no limit. Then the 25-second limit applies.
- **A memory warning** during the grace ends it, as a deadline does.
- **A network change** is a change of the interface or of its gateways, as `TailnetEngine.PathMonitor` already computes. The first value at start is not a change. Changes that come within 1 second of each other count as one change.
- **The reconnect** on a network change runs the path of a return after a suspension: `HostStore.foreground(suspended: true)`. It resumes the tailnet engine first for hosts that use it. Then each open terminal calls `TerminalSession.resume`. In the background, a network change does nothing. The return to the foreground handles it.

### Code

- `ios/GoRex/Terminal/TerminalLive.swift`: `BackgroundGrace` moves to its own file `ios/GoRex/BackgroundGrace.swift`. It gets a list of work items, the deadline from `backgroundTimeRemaining`, and `end(reason:)` for the memory warning. `TerminalLive` registers its work and does not own the grace.
- `ios/GoRex/TerminalActivityKit.swift`: the shared grace moves to an app-wide `BackgroundGrace.shared` with the UIKit `begin` and `end`.
- `ios/GoRex/GoRexApp.swift`: the `.background` handler starts the grace when `HostStore` has a connected host. `HostStore.suspended` keeps its use of `grace.awake`.
- `ios/GoRex/Views/TerminalScreen.swift`: the `.background` handler registers the suspend of its panes and no longer starts the grace.
- `ios/GoRex/Rex/NetworkPath.swift`, new: one `NWPathMonitor` for the app. It publishes a counter that goes up on each change. `TailnetEngine.PathMonitor` reads the same source, so the app has one monitor.
- `HostStore`: on each change of the counter, in the foreground, it calls `foreground(suspended: true)`.
- `TerminalScreen`: on each change of the counter, in the foreground, it calls `resume()` on each pane that is not leaving.
- Each reconnect writes `ConnectionLog.note("network changed: <name>")`, so a report shows the cause.

## Alternatives

- **Background location updates, as rootshell does.** The app runs for hours. [`IOS-006`](IOS-006-live-activities.md) rejected it for App Review Guideline 2.5.4 and the battery.
- **A ping on each data stream.** It finds a dead path after a ping interval, not at once. It adds traffic for each terminal. The network signal is faster and costs nothing.
- **A shorter ping on the control connection.** It costs traffic and battery on every host, all the time. It still finds the change seconds late.
- **`BGTaskScheduler`.** iOS runs it at times that it chooses, and it keeps no socket open. It cannot keep a stream.
- **Keep the grace with the terminal screen only.** This is today. It loses the notifications from the list and the gallery.
- **A grace only for hosts with a running program.** It saves some battery, but a program can start to wait at any time. The user rejected it (2026-11-21).

## Tests

- The grace runs the work of each owner, once, and ends its task after the last work: unit test in `GoRexTests`, with the fake `begin`, `end` and `schedule` that `TerminalLiveTests` uses today.
- The deadline is `min(25, remaining − 5)`, and an infinite remaining gives 25: unit test.
- A memory warning ends a running grace and does nothing with no grace: unit test.
- The grace starts with a connected host and no terminal screen, and does not start with no host: unit test on the `.background` decision.
- `TerminalLiveTests` keeps passing with the work moved out of `TerminalLive`.
- Changes within 1 second count as one, and the first path value is not a change: unit test with a fake path source.
- A network change in the foreground resumes the hosts and the terminals, and in the background does nothing: unit test on `HostStore` with fake hosts.
- End to end, on a device from TestFlight: open a terminal that prints each second, turn Wi-Fi off, and read the time from `network` to `resumed` in Diagnostics. Do the same from the host list, then go to another app for 10 seconds while a program asks for input, and check the notification.
- `docs/invariants.md`: no item covers the grace or the iOS reconnect.

## Phases

1. App-wide grace, deadline from `backgroundTimeRemaining`, end on a memory warning.
2. Reconnect on a network change, with the shared `NWPathMonitor`.

## Open questions

1. **Time to measure the reconnect.** The 2-second goal is an estimate. The phase 2 PR measures it on a device and updates the goal.
