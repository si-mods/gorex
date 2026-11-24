---
name: WEB-007
description: "The web client gets one toast host for short notices with one action, and Delete Space uses it for Undo instead of a question. Use when you change web/src/toast.ts, web/src/deferdelete.ts or Delete Space in the web client."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# WEB-007: Toast with Undo in the web client

Status: built.

This doc adds a toast host to the web client ([REF-007](REF-007-web-client.md)). It uses the host for decision D20: Delete Space acts at once and offers Undo. It extends [MOD-002](MOD-002-projects.md), section "Delete Space".

## Problem

The web client has no place for a short notice with an action. The status line (`#status`) is one line of text with no button. The update panel (`.update`) is a one-use panel in the corner.

Delete Space asks a question first (`confirmDanger` in `web/src/projectops.ts`). The question stops the user for an action that loses little: the projects move to Default and no tab closes. Undo is better than a question for such an action.

## Goal

- One host shows one toast at a time. A toast has one line, one action button, a close button and a ring that shows the time left.
- Delete Space does not ask. It shows "Deleted <name>" with Undo. Undo shows "Restored <name>" for about 1.5 s.
- A screen reader hears each toast. Showing a toast never takes focus.

## Non-goals

- The status-line messages stay in the status line. No message moves to the toast now.
- Swipe to dismiss is not built. It adds gesture code, and the close button and Escape do the same job.
- The host uses `position: fixed`, as the update panel does. Issue #1095 tracks a move to `popover="manual"` for all such panels.

## Design

### Behavior

1. The user chooses Delete Space in the space picker. The space leaves the picker, and its projects show in Default. The toast "Deleted <name>" shows, with Undo.
2. Undo shows the space and its projects again. The toast changes to "Restored <name>" and goes after about 1.5 s.
3. If the user does not press Undo, the delete is sent when the toast ends. A toast ends by time, by the close button, by Escape in the toast, by a new toast, or when the page hides or unloads.
4. A new toast replaces the one on show. The old toast ends first, so a delete that waits is sent.
5. The toast stays at least 6 s. The time stops while the pointer is over the toast or focus is in it. The ring empties as the time goes, and it stops with the time.
6. The toast is at the bottom centre, above the status line and the update panel, and clear of the safe area. On a narrow screen it has the full width less 8 px margins.
7. The buttons are in the tab order. Escape in the toast closes it. The toast never takes focus.
8. With `prefers-reduced-motion: reduce`, the toast only fades in for 150 ms and does not move. The ring keeps its countdown, because it shows a state.

### Model

`SpaceStore.hidden` is a set of spaces that the user deleted on screen and that were not sent. It is not saved. While a space is in it, `mapSpace` returns Default for the space and `names` leaves the space out. Nothing else changes: the saved colour, order and current space stay until the delete is sent. A reload while a delete waits restores the space, because the page sent nothing.

Sending uses the code that Delete Space used before. Each connected host gets `gorex.space.delete`. A host that is away gets the delete when it connects.

New Space with the name of a space that waits sends that delete first.

### Code

- `web/src/toast.ts`: `ToastHost` and `startToasts`. The root `div.toast-host` is made at start and stays in the DOM. It holds one `role="status"` region (`aria-live="polite"`) that is hidden to the eye, and the panel. The region keeps its element, and only its text changes. The text in the panel is `aria-hidden`, so the region speaks once. `show` ends the old toast as `replaced`. The action button ends the toast with no `onEnd`, and runs `onAction`. `startToasts` ends the toast on `pagehide` and when `visibilitychange` makes the page hidden. This is best effort: a page can go with no event.
- `web/src/deferdelete.ts`: `deleteDeferred` hides the space, shows the toast, and calls `commit` once, from `onEnd`. Undo calls no `commit`.
- `web/src/projectops.ts`: `deleteSpace` calls `deleteDeferred`. `commitDelete` sends the delete.
- `web/index.html`: the `.toast` styles use the panel look of `.update` (`--panel`, `--panel-edge`, blur and shadow). The Glass-mode rules list `.toast` with the other panels, so the glass reskin and the light and dark variants apply. No new colour.
- `web/locales/en.json` and `zh-Hans.json`: `spaceDeleted`, `spaceRestored`, `toastUndo`, `toastClose`. The question strings `spaceDeleteTitle`, `spaceDeleteBody`, `spaceDeleteOffline` and `spaceDelete` are gone. The menu item reads "Delete Space" with no ellipsis, because no dialog follows.

## Alternatives

- **Send the delete at once and undo with a new space.** A host cannot restore the old space with its colour and order. The deferred delete loses nothing.
- **A live region for each toast.** Screen readers often miss a region that is new to the page. One kept region is more reliable.

## Tests

`web/test/toast.test.ts` uses a small fake DOM and fake timers:

- The live region stays and its text changes.
- A new toast replaces the old one, which ends once.
- An action toast stays at least 6 s.
- The pointer and focus stop the clock.
- Escape and the close button close the toast.
- Delete: the space hides and nothing is sent. Undo sends nothing. Timeout, close, replacement and page hide each send once.

Not checked by test: the look, the ring, the reduced-motion fade and the placement. A screenshot of the page checks them.
