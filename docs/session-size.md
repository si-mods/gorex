# Session size with several clients

A session has one terminal size: the program in it draws for one grid.
Several clients can attach to it at once, such as the Mac window and a
remote machine mirroring the same session through shared sessions. Their
windows have different sizes, and only one size can win.

If the size went to whichever client resized last, the PTY would flip
between the clients' sizes. The client whose size lost would then show a
program that draws for the other client's grid. The cursor lands in the
wrong place and the screen height keeps changing. Programs that draw
inline, such as Claude Code or a shell prompt, never get a full redraw to
repair it.

So the session has one **authority**, and its size is the session's size.

## The authority

The server (`internal/rex/session.go`, `authorityLocked`) picks the
authority in this order:

1. The client holding the **size lock** (`lockSize`): a device that took
   the size explicitly, such as the layout sheet.
2. The client that **typed last**, or that claimed **focus** last (see
   below).
3. Otherwise, the client that **attached last**.

The authority also answers the program's terminal queries (cursor
position, colors). Only one terminal's answers reach the program.

Every attached client records the size of its own terminal: the size it
attached at, then each resize it sends.

- A resize from the authority is applied once the size settles (150 ms,
  so a window drag resizes once).
- A resize from another client is only recorded.
- When the authority changes (a client types, claims focus or leaves),
  the session takes the new authority's recorded size at once.
- A client attaching does not resize a session that another client is
  typing in.

Focus reports that a program asks for (`CSI I` / `CSI O`) do not count as
typing: a terminal sends them whenever its window changes focus, without
the user doing anything in it.

## Focus claims

Typing is only an indirect sign of where the user is. The app also tells
the server where the user's focus is: it sends a `focus` request for a
session, or sets `focused` on an attach.

- The app checks each frame, and sends only on a change. When the window
  has the focus, every pane in its active tab claims its session. Panes
  in other tabs, or in a window without the focus, drop their claim.
- A pane whose stream attaches, or attaches again after the connection
  dropped, claims as it attaches, if it should.
- A server that does not know the `focus` request answers an error,
  which the app ignores.

## Presence: the idle policy

A window with the focus does not mean someone is using that machine. An
unattended machine whose only window is GoRex keeps that window focused.
Without a check, each of its reconnects, and its launch, would take the
size from the machine the user actually works on.

So a focus claim also needs the user to be **present on this machine**:
some keyboard or mouse input on it, anywhere in the system, within the
last **60 seconds** (`presentFor`).

| Platform | Source of idle time |
|---|---|
| macOS | `CGEventSourceSecondsSinceLastEventType` (HID system state, any input) |
| Windows | `GetLastInputInfo` and `GetTickCount` |
| Others | unknown: the claim is sent, as it was before presence existed |

The check is made when a claim would be sent: on a focus change, and when
a stream attaches.

The idle time is sampled at most once a second. While the window has the
focus, a user coming back to an idle machine makes the app claim its
panes again: no focus change happens, so nothing else would claim.

Typing claims the size whatever the idle time is: typing is presence.

## Examples

- **A Mac in use, and an unattended PC mirroring its sessions.** The
  PC's window may stay in the foreground, but nobody touched the PC for
  more than 60 seconds. Its launch and reconnects claim nothing, and its
  resizes are only recorded. The Mac keeps the size.
- **The user moves to the PC.** Any input there makes the PC present.
  The PC's window has the focus, so it claims on its next frame check,
  and the session takes the PC's size. Typing there would take it too.
- **The user comes back to the Mac.** Clicking or typing in the Mac
  window claims again, and the Mac's size returns at once.
- **The app restarts.** Its window gains the focus while the user is
  present, so its panes claim, and no key press is needed.

## Limits

- The client that does not hold the size still shows a program drawing
  for another grid. Nothing can fix that while two machines show one
  session at different sizes; the policy only makes sure it is the
  machine nobody is looking at.
- Presence is per machine, not per window. Someone using another app on
  the PC counts as present, so a GoRex window focused there could claim.
  In practice the window loses the focus to that other app.
- A size lock overrides all of this until it is released or its lease
  ends.
