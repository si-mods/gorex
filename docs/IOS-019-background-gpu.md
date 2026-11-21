---
name: IOS-019
description: "Rex Go closes a process-wide GPU gate when the app leaves the foreground, so no terminal surface commits a frame while iOS could suspend or snapshot it. Use when you change the iOS app's background handling, its Ghostty surfaces or its rendering."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-019: GPU presentation in the background

Status: built (#963, #964).

## Problem

iOS does not allow GPU work in a suspended app. A frame that commits while the app is in the background can end the app with a watchdog kill, and the scene snapshot can capture a frame in the middle of its draw. The lock screen makes it sharper: iOS captures a secure snapshot between `willResignActive` and `didEnterBackground`, and a renderer still presenting there is killed (`0x2BAD45EC`, drawing while in secure mode).

`TerminalView.hide` marks each surface occluded on `UIApplication.didEnterBackgroundNotification` (`ghostty_surface_set_occlusion`). Three gaps stayed after the gate of #963:

1. **The occlusion is not confirmed.** It is a message to the surface's renderer mailbox. A frame can still be in the GPU when iOS suspends the app.
2. **The lock snapshot comes before the gate.** The gate of #963 closes on `didEnterBackground`. iOS takes the secure snapshot during the resign-active transition, before that notification.
3. **A background launch keeps the gate open.** The gate of #963 starts allowed and closes only on the notification. An app that launches straight into the background never gets one, and a surface made there (`initially_visible`) presents anyway.

## Goal

- From `willResignActive` until `didBecomeActive`, no terminal surface commits a frame to the GPU and every renderer is confirmed paused.
- The guarantee holds when the handler returns, not when a mailbox drains.
- A launch straight into the background keeps the gate closed until the first `didBecomeActive`.
- A surface created while the latch is armed starts occluded.
- A frame already with the GPU at that moment finishes and presents. The snapshot shows the last frame.

## Non-goals

- A change to what the program sees. The occlusion keeps its pause of the renderer and its visibility report (terminal mode 2033).
- The desktop app and the web client. They do not suspend.

## Design

### Behavior

Nothing the user sees changes. The terminal still freezes when the app leaves the foreground and draws again when it comes back. A transient resign, such as Control Center, pauses the terminal for the moment and puts it back.

### Model

`Ghostty.secureDrawProhibited` is the latch. It starts armed, because a background launch gets neither `willResignActive` nor `didEnterBackground`. It arms on both notifications and clears only on `didBecomeActive`: an unlock goes `willEnterForeground` while still locked, so the foreground notification cannot open it.

Every change of the latch is mirrored into `ghostty_presentation_set_allowed`, GhosttyKit's process-wide gate on GPU commits. Closing the gate is synchronous: after the call returns, no surface can commit a frame. A frame already sent to the GPU gets a bounded time to finish. A frame not yet committed is dropped.

On `willResignActive`, before the secure snapshot, every live surface is also occluded and drained (`ghostty_surface_drain_renderer_to_idle`, 100 ms each). The gate blocks commits, but the drain confirms each renderer paused and stops its display link, as rootshell's `pauseRenderersForSecureSnapshot` does. The surfaces come from `Ghostty.views`, the registry the actions already use.

On `didBecomeActive` the latch clears and each surface's occlusion goes back to its page. This also covers the resign that never became a background: Control Center sends no `willEnterForeground`, so the views' own `show()` cannot reach it.

A surface created while the latch is armed asks for `initially_visible = false`, so it starts occluded instead of presenting out of the foreground.

These are app-level notifications, as the gate is process-wide: a scene phase of one window must not stop the frames of a window that stays active.

### Code

- `ios/GoRex/Terminal/Ghostty.swift`: `Ghostty.secureDrawProhibited`, `watchBackground`, `secure` and `unsecure`. `Ghostty.app` registers the three observers in its lazy init.
- `ios/GoRex/Terminal/TerminalView.swift`: `open` reads the latch for `initially_visible`. `hide` and `show` stay as they are.

## Alternatives

- **Gate only, on `didEnterBackground` (#963).** It leaves the lock-screen snapshot window and the background launch open. This doc's design replaces it.
- **Drain without the gate.** The drain confirms the renderer idle but blocks no later commit: a direct draw or a queued occlusion could still present after it. The gate is the backstop every path goes through, so both stay.
- **Clear on `willEnterForeground`.** The notification fires while the device is still locked. A queued occlusion could then draw into a lock transition. Rejected; `didBecomeActive` is the only safe edge.
- **Occlusion only, as before #963.** It is asynchronous and arrives after the snapshot. Rejected.

## Tests

- `xcodebuild` builds the app for the simulator, as `docs/lessons.md` ("Any change under ios/**") asks.
- On a device or a simulator: a terminal with running output goes to the background, locks and unlocks, and returns. The output continues, and no watchdog or GPU message shows in the device log.
- `docs/invariants.md` has no item for the renderer's background state.
