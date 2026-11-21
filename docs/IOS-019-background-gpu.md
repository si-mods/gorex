---
name: IOS-019
description: "Rex Go closes a process-wide GPU gate when the app enters the background, so no terminal surface commits a frame after that call. Use when you change the iOS app's background handling, its Ghostty surfaces or its rendering."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-019: GPU presentation in the background

Status: built (#963).

## Problem

iOS does not allow GPU work in a suspended app. A frame that commits while the app is in the background can end the app with a watchdog kill, and the scene snapshot can capture a frame in the middle of its draw.

`TerminalView.hide` marks each surface occluded on `UIApplication.didEnterBackgroundNotification` (`ghostty_surface_set_occlusion`). Two gaps stay:

1. **The stop is not confirmed.** The occlusion is a message to the surface's renderer mailbox. A frame can still be in the GPU when iOS suspends the app.
2. **The signal is late.** `didEnterBackground` comes after `sceneWillResignActive`. iOS captures the scene snapshot during the transition, before the notification.

## Goal

- After the app enters the background, no terminal surface commits a frame to the GPU.
- The guarantee is synchronous: it holds when the handler returns, not when a mailbox drains.
- A frame already with the GPU at that moment finishes and presents. The snapshot shows the last frame.

## Non-goals

- A confirmed stop of each surface's display link. `ghostty_surface_drain_renderer_to_idle` gives that. See Alternatives.
- A change to what the program sees. The occlusion of `TerminalView` keeps its pause of the renderer and its visibility report (terminal mode 2033).
- The desktop app and the web client. They do not suspend.

## Design

### Behavior

Nothing the user sees changes. The terminal still freezes when the app leaves the foreground and draws again when it comes back.

### Model

GhosttyKit exposes `ghostty_presentation_set_allowed`, a process-wide gate on GPU presentation. Closing the gate is synchronous. After the call returns, no surface can commit a frame. A frame already sent to the GPU gets a bounded time to finish. A frame not yet committed is dropped.

The app closes the gate on `UIApplication.didEnterBackgroundNotification` and opens it on `UIApplication.willEnterForegroundNotification`. These are app-level notifications, as the gate is process-wide: a scene phase of one window must not stop the frames of a window that stays active.

### Code

- `ios/GoRex/Terminal/Ghostty.swift`: `Ghostty.app` registers the two observers in its lazy init. Each calls `ghostty_presentation_set_allowed`, which is safe from any thread.
- `TerminalView.hide` and `show` stay as they are.

## Alternatives

- **`ghostty_surface_drain_renderer_to_idle` on each live surface.** It stops the display link and waits until the renderer is idle before the snapshot. The gate already blocks every commit, so a late tick only draws a dropped frame. A list of the live surfaces and one bounded wait per surface add code for no new guarantee. The display-link wedge the call came with is fixed in the pinned GhosttyKit (rootshell `e9588e27a`, thread-safe link start and stop).
- **Close the gate on `.inactive`.** A transient inactive (Control Center, a banner) would freeze the terminal's animations for no gain. The risk starts in the background, not at resign-active.
- **Occlusion only, as today.** It is asynchronous and it arrives after the snapshot. Rejected.

## Tests

- `xcodebuild` builds the app for the simulator, as `docs/lessons.md` ("Any change under ios/**") asks.
- On a device or a simulator: a terminal with running output goes to the background and returns. The output continues, and no watchdog or GPU message shows in the device log.
- `docs/invariants.md` has no item for the renderer's background state.
