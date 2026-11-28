---
name: IOS-034
description: "Rex Go shows a short message in a glass toast at the top of the screen, in place of alerts that have only OK, and uses it for uploads and a Notifications row. Use when you change Toast.swift, an alert in the list or the terminal, or the upload feedback."
metadata:
  created: "2026-11-24"
  updated: "2026-11-28"
---

# IOS-034: Toasts

Status: proposed. The code is written. It is not yet compiled or run on a device.

This doc adds one toast to Rex Go. The toast replaces the alerts that have only an OK button. It also gives the uploads a visible result. It follows the same changes on the desktop and the web client.

## Problem

- **An alert for a small failure.** A failed rename or a failed new tab opens a modal alert with only OK. The user must tap OK before the next action. The alert hides the screen where the failure matters.
- **Uploads show no result.** Add Photo and Add File show a spinner on the key bar. A failure opens an alert. A success shows nothing, and the user does not know where the file went.
- **Notifications have no row in Settings.** The first notification asks for permission once. A user who said no cannot find the way to turn them on again.

## Goal

- One toast shows at a time. It shows over every screen of the app, including the terminal.
- A failure shows as an error toast with the title and the reason in one line.
- An upload shows Uploading, then Uploaded. A failed upload shows the error with Retry.
- Settings shows if notifications are on, and gives the way to turn them on.

## Non-goals

- Alerts that offer a choice stay alerts. Examples are Folder Not Found with Close Project, the questions about the speech model, and the rename fields.
- The toast does not queue. A new toast replaces the old one.
- The desktop and the web client have their own toasts.
- The toast does not take the focus. It has no gesture other than the swipe up.

## Design

### Behavior

**The toast.** A glass capsule shows at the top centre, under the safe area. In a terminal it shows at the bottom centre instead, above the key bar and the pane bar, where the Reconnecting note shows: at the top it would cover the terminal's header. It has two kinds.

| Kind | Content | Stays |
| --- | --- | --- |
| Status | An SF Symbol and one line. The error variant has `exclamationmark.circle.fill` in the system red. | 4 s. 1 s more for each 20 characters past 40. 8 s at most. |
| Progress | A spinner and one line. It becomes its result in the same place. The result can have a button, such as Retry. | While the work runs. Then 3 s for a success. An error with a button stays until the user uses it or closes it. |

- A line has one row. A longer line ends in an ellipsis. VoiceOver reads the full line.
- A swipe toward its edge dismisses the toast: up at the top, down in a terminal. The toast follows the finger.
- Only one place shows the toast: the latest screen that has one. A sheet shows it over a terminal, the terminal over the list.
- A new toast replaces the old one. The old toast ends first.
- Under Reduce Motion, the toast cross-fades for 150 ms. It does not move.
- VoiceOver gets one announcement for each text the toast shows. The toast never takes the focus. A progress that turns into its result announces the result.
- A progress that the user closed, or a new toast replaced, shows its result only when the result is an error. The user must hear of a failure.

**Failures (I4).** The alerts with only OK become error toasts. The text is the title, a colon and the first line of the reason. The reason is cut at 120 characters. The cases are in `HostSection.swift` (the new session and tab, the changes of a tab, a project, a session and the quick launchers), `TerminalScreen.swift` (split, close, new tab, Folder Not Found with only OK) and `HostListView.swift` (make and move a project).

**Uploads (I5).** The toast shows "Uploading <name>", or "Uploading <n> files" for more than one file. It becomes "Uploaded" for 3 s. The toast does not name a folder. The server answers a stream upload (`upload_begin` and the `rex.data.bytes.v1` stream) with no path. It types the path into the terminal and closes the stream. Only the HTTP upload of `/api/upload` answers a path, and the app does not use it. A failure turns the toast into "Can't Upload: <reason>" with Retry. Retry sends only the files that are staged and not yet sent. The server types each file's path as the file arrives, so a file sent twice is typed twice. The staged files go when the toast ends with no Retry. A failure while the files stage has no Retry: the folder goes, and the user picks again. The spinner on the key bar stays.

**Notifications (G4).** Settings has a Notifications row. The app reads the system's state when Settings appears and when the app becomes active.

| State | The row shows |
| --- | --- |
| Allowed or provisional | "On" |
| Not asked yet | A Turn On button. It asks for permission. |
| Denied | The Open Settings button, and the note "Notifications are off". The button opens the notification page of the Settings app. |

**Delete Space.** Delete Space keeps its confirm alert, by the owner's decision. It has no toast and no Undo.

### Model

- `Toasts` (`Toast.swift`) holds the one `current` toast. It is observable. The terminal's UIKit views and the SwiftUI screens use the same `Toasts.shared`, so no view needs plumbing.
- A toast has an optional button and an optional `commit`. `commit` runs once when the toast ends in any way but its button, as the tidy-up of a failed upload's Retry toast. The button runs when the user presses it.
- A timer ends only its own toast. A timer of a replaced toast does nothing.
- A sheet with its own host counts in `sheetHosts`. The root's host does not show the toast while a sheet's host does, so the toast shows once.

### Code

| File | Change |
| --- | --- |
| `ios/GoRex/Views/Toast.swift` | `Toast`, `Toasts`, the `toastHost()` modifier and the views. |
| `ios/GoRex/GoRexApp.swift` | `toastHost()` after `promptSheet`. |
| `HostSection.swift`, `TerminalScreen.swift`, `HostListView.swift` | Error toasts. The `failure` state and its alerts go. |
| `TerminalView+Upload.swift` | The progress toast, the result and Retry. |
| `SettingsView.swift`, `Notifier.swift` | The Notifications row. `Notifier.requestAuthorization()` is the question that `post` asked already. |
| `ios/GoRexTests/ToastTests.swift` | Tests of the toast model. |

A sheet is a view of its own, so the toast of the root is under it. The quick launchers sheet has its own host (`sheetToastHost()`). The other sheets raise no toast. A system alert or the photo picker also covers the toast. This is an open limit.

## Decisions

1. **A new toast replaces the old one.** A queue makes a late message. A user who does a second action does not need the message of the first.
2. **The toast does not name the upload folder.** The stream upload's answer has no path. A guess is worse than no name. A name needs a change of the server's stream, or a change to the HTTP upload. Both are outside this doc.

## Tests

- `ToastTests` use a clock that the test owns. They check the lifetimes, the replacement, the stale timer, the progress that becomes its result, and Retry.
- A device check is open: the toast over the terminal, the swipe up, VoiceOver, Reduce Motion, and the upload.
