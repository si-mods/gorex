---
name: DSK-013
description: "Rules for when the desktop app shows a toast, the banner, a system notification or a state in place, and the toast component that follows them: error toasts for failed commands, disabled actions of an offline host, a question before Quit and End All Sessions, and a Settings row when macOS turns notifications off. Use when you add a message, an error or a confirmation to the desktop app."
metadata:
  created: "2026-11-24"
  updated: "2026-11-25"
---

# DSK-013: Toasts, the banner and other messages

Status: built. The visual decisions of issue #1095 are open.

This doc decides how the desktop app tells the user what happened. It sets the rules for each kind of message, adds a toast component, and moves four flows to the rules. It extends the "A toast or a banner?" decision of `DESIGN.md`.

## Problem

The desktop app has one message surface: the error banner at the bottom of the window (`errorBanner`, `view.go`). It shows two different things:

- A state that lasts, such as a lost connection or a Lua config error.
- The failure of an action that the user did a moment ago, as `err.Error()` text, such as "rex: connection to the server lost".

The second kind stays on the screen until the user closes it. The text is the raw error of the code, not a sentence. Other gaps:

- New Tab, Split, New Floating Pane, New Terminal on a canvas and Restart Shell show in the menus and the palette while their host is offline. A click shows the banner "Not connected to …".
- Quit and End All Sessions (`cmdEndAll`) ends every session at once, with no question. It cannot be undone.
- `showNotification` (`state.go`) ignores the error of `Notification.Show`. When the user turns notifications off for GoRex in macOS, no notification shows and the app does not say why.

## Goal

- Each message uses the surface that the rules below give it.
- A failed action of the user shows an error toast with a sentence: "Couldn't <action>: <reason>."
- An action that needs an offline host shows disabled in the menus and the palette.
- Quit and End All Sessions asks first when sessions run.
- Settings tells the user when macOS blocks notifications, with a button to the system setting.

## Non-goals

- No change to the web client or Rex Go. Their toasts have their own work.
- No new design tokens. The visual values follow the current tokens of `DESIGN.md`. Issue #1095 holds the open style decisions, such as the colors of the error toast and the shape of the timer.
- No queue of toasts. A window shows one toast at a time.

## Design

### Which surface

| Surface | Use it when | Goes |
| --- | --- | --- |
| Toast | It answers an action of the user, done just now: a result or a failure. | By itself, after its time. |
| Banner | A state lasts, or a failure must be fixed before the user can go on. | When the state ends, or the user closes it. |
| System notification | It is news that the user did not cause, as a program that finished in another tab. | By the rules of the system. |
| State in place | The state belongs to one control or row, as a disabled menu item or a Settings row. | When the state changes. |

These stay on the banner: Lua config errors (`luaconfig.go`), a lost connection, a read-only server or a server of another version, a missing project folder with Close Project (`folderMissing`), and a teleport failure.

### The toast

A toast is a small panel at the bottom centre of the window. It has the look of the banner: `k.panel`, `k.panelBorder`, `radiusL`, `toastShadow` and `fontCallout`. It comes in and goes with the user's spring (`motion(c)`), from `bannerMotion`, and it goes out in `durExitMove`. With less motion it shows and goes at once.

A toast has one kind: a status. It shows an icon and one line. The error variant has the danger dot. It shows for 5 s, longer for long text (15 characters each second).

Rules:

1. Each window has one toast slot. A new toast replaces the old one.
2. The time stops while the pointer is on the toast or the focus is in it.
3. Escape closes a toast that has the focus.
4. When the banner shows, the toast sits above it, not on it.
5. Screen readers read the text as it shows. The toast is a status, and an error toast is an alert.

The code is `toast.go`. It builds on MyGo's toasts (`ui.AddToast`, `ui.ToastViewportBase`, `ui.ToastBase`). MyGo keeps the time, stops it under the pointer and the focus, closes the toast on Escape, and announces the text. GoRex draws it and keeps the slot (`App.toast`). Code that has no `ui.Context` asks for a toast with `App.showToast`, and the next frame adds it.

### D1: failures of the user's commands

A failed command shows an error toast. The text is a sentence from the catalog with the reason after a colon:

| Where | Text |
| --- | --- |
| An action from a menu, the palette or a key (`invokeCommand`, `dispatchKey`) | "Couldn't run <action>: <reason>." |
| Reset of one shortcut or of all shortcuts (`shortcutsview.go`), in the Settings window's own toast slot (`settingsUI.toast`) | "Couldn't reset the shortcut: <reason>." |
| A session that the host does not make (`newPaneIn`) | "Couldn't start a session: <reason>." |
| A terminal that does not start (`attach`) | "Couldn't open the terminal: <reason>." |

A session in a missing project folder keeps its banner with Close Project. A custom action that another client asked for (`command`) is not the user's action: its failure keeps the banner. A Lua config that does not load as a window opens (`openWindow`) keeps the banner too, as all Lua config errors do.

Limit: a layout command that a server refuses (`askOn`), as a new tab or a split that the server makes, still shows its error on the banner. `askOn` serves every layout command, and some callers read its banner (`project.create` and its `exists` answer). A later change moves it.

### D7: actions of an offline host

A command can name the host that it needs (`command.Host`). While that host is offline:

- The palette shows the command disabled, with "Not connected to <host>." as its detail.
- The menu bar shows its item disabled (`syncHostItems`), as Toggle Note Preview does.
- The menus of a tab, a project and a canvas show New Tab and New Terminal disabled.

This applies to New Tab, New Tab on a host, Split Right, Split Down, New Floating Pane, New Terminal on a canvas and Restart Shell. With no window left, the menu bar enables the items again (`clearHostItems`). New Floating Pane also needs a tab: without one, it is disabled in the palette and fails for a client. Teleport already shows an offline host disabled. A key or a client can still ask for the action. Then `App.offline` stops it and shows "Not connected to <host>." in an error toast.

### G1: Quit and End All Sessions asks first

Quit and End All Sessions opens a dialog when this machine has sessions (`quitconfirm.go`):

- Title: "Quit and end N sessions?"
- Body: the programs in them end, and this can't be undone.
- Buttons: Cancel, which has the focus, and the danger button "Quit and End All".

It follows the dialog of `closeconfirm.go`: `DialogBase`, `dialogEnter`, `glassPanel` and `dangerButton`. With no sessions, the app quits at once.

### G4: notifications that macOS blocks

`showNotification` keeps the result of `Notification.Show`. When it is `mygo.ErrNotificationsDenied`, the store records it on the main thread (`store.notified`). A later notification that shows clears it.

While it is set, Settings, Appearance, shows a row under the Dock badge: "Notifications are off for GoRex." and "Turn them on in System Settings.", with a button that opens `x-apple.systempreferences:com.apple.Notifications-Settings.extension`. Only macOS reports the denial, so the row shows only there.

## Alternatives

- **A queue of toasts, as MyGo's default of three**: a stack of messages hides the window. The newest message is the one that answers the user.
- **Delete Space with Undo (D20)**: the owner decided against it. Delete Space keeps its confirm modal on every platform, as it is.

## Tests

- Toast time, replace, pause and place: `TestToastLife`, `TestToastGoesByItself`, `TestToastReplaces`, `TestToastPauses`, `TestToastAboveBanner` in `toast_test.go`, with `ui.NewTester` and `Tester.Advance`.
- D1: `TestCommandFailureToasts`: a failed action shows the error toast, and the banner stays empty.
- D7: `TestOfflineHostActionsDisabled`, `TestNewFloatNeedsTab`, `TestHostItemsClearedOnLastClose`: the palette rows of an offline host are disabled, and the menu bar items come back with no window.
- Delete Space: `TestDeleteSpace` (`projects_desktop_test.go`): the trash button opens the confirm modal. Cancel sends nothing. Only the confirm button sends the delete.
- G1: `TestQuitAndEndAsks`, `TestQuitAndEndNoSessions` (`quitconfirm_test.go`): the dialog asks, Cancel keeps the sessions, and with no sessions the app quits at once.
- G4: `TestNotificationsOffRow`: the row shows after a denial and goes after a notification that shows.
- Screenshots: the `GOREX_DEBUG` verbs `toast error [TEXT]`, `quit-confirm open|cancel` and `notifications off|on`.
- `docs/invariants.md`: no item changes.
