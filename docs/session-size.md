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
   the size explicitly, such as the layout sheet or the iPhone app (see
   *A Rex API client: the iPhone*).
2. The client that **typed last**, or that claimed **focus** last (see
   below).
3. Otherwise, the client that **attached last**.

The authority also answers the program's terminal queries (cursor
position, colors). Only one terminal's answers reach the program. An
attach that answers none (`Attach.Mute`, the Rex API gateway's) leaves
them to the server's emulator while it is the authority, as does a
session with no client attached: the emulator is at the session's size,
so its cursor and size answers fit what the program draws for. Its
theme is not the windows', so it does not answer colors (OSC 4, 10, 11,
the color scheme): a program asking them then waits for its own
timeout. Which terminal answers is decided as the query is read, not as
the answer is written.

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

## A Rex API client: the iPhone

The iPhone app (`ios/`) does not attach as a window. It talks to the
Rex API gateway (`internal/rexapi`), which holds the session's size for
it with a **size lock**. So the iPhone is not an authority among the
windows: it is above them.

- **Ownership.** The gateway keeps an owner for each block (a pane's
  session), `owners.go`. When a block's terminal opens, or its stream
  comes back after a drop, the phone calls `session.own_block` with mode
  `take` (`TerminalSession.claim`). A `take` makes the phone the owner at
  once, even when another client owns the block. The gateway then takes
  the size lock (`lockSize`) for the phone, with the size it asked for
  last, and renews it every 5 seconds (`renewEvery`; the lease is 30
  seconds, `sizeLease`).
- **Resizes.** The phone sends `resize` with its grid once its size holds
  for 100 ms (`TerminalSession.fit`): after a rotation, a pinch that
  changes the font, or a new layout. The gateway records each client's
  size (`desired`). It applies the size at once when the client owns the
  block or no client does (`resizeBlock`, `LockedResize`). Otherwise the
  size waits until that client becomes the owner.
- **The phone never follows another size.** The keyboard does not
  resize the grid, and the phone ignores the `resize` messages of its
  stream: its grid is always its own view's, whatever size the session
  has.
- **The windows while the phone owns the block.** The lock overrides
  focus claims and resizes from windows (`followAuthorityLocked`). The
  window shows the session at the phone's grid, clipped or with empty
  space around it (see *What a window shows*). `SessionInfo.SizedBy` is
  empty and `SizeLock` names the phone's client.
- **A window takes the size back by typing.** A key or a paste in a
  local pane whose session is locked unlocks it at the pane's size
  (`App.takeSize`, `UnlockSize(sid, "", cols, rows)`), whoever holds the
  lock. Panes that see a lock end resend their size (`ResendSize`). Only
  a pane of the server's own machine does this: a pane of a remote host
  (`p.host != nil`) does not unlock, so a desktop that views the session
  as a remote host gets the size back only when the lock ends.
- **The gateway lets go.** The gateway does not learn of that unlock
  at once. Its next renewal (within 5 seconds, `renewOwned`) fails, and
  it lets the lock go: the block has no owner, and a
  `block_owner_changed` event with reason `taken` says so. The phone's
  `take` claim stays. A lease that ran out, when renewals stopped for 30
  seconds, ends the same way. The windows' resizes apply again.
- **The phone takes the size back by typing.** When a client that took a
  block (mode `take`) and lost it writes to it (`write`, or a key or a
  paste through `block.event` or `client.key`), the gateway takes the
  block for it again before the input (`takeOnInput`, reason `input`),
  with the size it asked for last. A client that only requested the
  block, or never claimed it, does not take the size by typing.
- **The end of the lock.** The phone gives up the block when its
  terminal closes (`session.disown_block`). When its connection drops,
  as when iOS suspends the app in the background, the gateway lets go of
  its blocks (`releaseBlocks`, reason `lost`) and remembers them. If the
  phone reconnects in time and no one took a block meanwhile, the block
  is the phone's again (`restoreBlocks`), and the phone sends its size.
  When another API client is in line, the newest `take` gets the block,
  else the newest `request`, with the size it asked for (`electLocked`).
  A lock that ends without a next owner unlocks with no size
  (`UnlockSize(sid, "", 0, 0)`); the windows' panes then resend their
  sizes, and the authority's size applies.

So with the phone and a desktop window on the same pane, whoever types
last holds the size: it goes to the phone each time it opens the
terminal, comes back from the background or types, and to the desktop
each time the desktop types. Both terminals show one grid, the size of
the last of these: the program in the session draws for one size. The
phone does not follow the desktop's size, so while the desktop's size
holds, the phone lays out a program's output that was drawn for a
different grid.

## What a window shows

A window does not lay the program's output out itself: its terminal takes
the size the program drew for, the session's, and the program's bytes lay
out there as they did on the server. Its view only asks the session for
the size the pane fits.

So each attach gives an id of its own (`Attach.Client`), and the server
names the authority by it (`SessionInfo.SizedBy`, empty while a device
holds the size, `SizeLock`): a window that is neither takes the session's
size rather than the pane's. When it takes a size the program drew for
another, it asks the server for the session's screen (`capture`) and shows
that, since what it laid out at the pane's size before, wrapped where the
program did not wrap and drawn over where the program moves its cursor, is
not the screen the server shows. The way back is mended alike: a pane
typing or pasting takes the authority at once, without waiting for the
next ask of the sessions, and the screen it takes back is the session's
at its own size.

The panes ask the server what runs in its sessions every half second, so
the size another machine gave may lag that long; the session's own replay
of a full-screen program arrives in between, and the capture that follows
the pane's new size replaces it.

## A divider drag

A drag of the divider between panes is one size, not one per frame. The
panes follow the pointer, and their terminals keep the size they had at
rest until the pointer lets go, as they do while a pane moves: at every
width on the way the program would lay its output out where it, told of
one size at the end, never wrapped it, and a program that draws in place,
as an agent's interface, would draw its screen again and again.

The terminal takes the drag's size once the pointer lets go, before the
session does (it settles, 150 ms): what the program prints meanwhile, as
a shell reparks its prompt, lays out at a size the session did not draw
for. Taking the size asks for the session's screen, as following another
client's size does, and cuts the pane's history to the session's screen
and the last 64 KB of it (`resyncHistory`). A press that ends where it
began takes no size, and asks for nothing.

## Limits

- A window that does not hold the size shows the program's grid, which the
  pane clips when the pane is smaller: the grid's right columns and its
  last rows, where agents draw their input, are out of view until the pane
  grows or the size returns. Nothing fits a grid into a smaller pane
  without scaling it.
- Presence is per machine, not per window. Someone using another app on
  the PC counts as present, so a GoRex window focused there could claim.
  In practice the window loses the focus to that other app.
- A size lock overrides all of this until it is released or its lease
  ends.
