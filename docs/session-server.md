# The session server's terminals

The session server (`GoRex -server`, package `internal/rex`) runs every
shell of this machine in a pseudo-terminal of its own. Beside each PTY it
keeps a terminal emulator with no view, `session.vt`
(`internal/rex/session.go`): the program's output goes through it as it
goes to the windows. This document describes that emulator, what reads
it, and how long it lives.

Sizes are in [session-size.md](session-size.md), program status in
[osc-7501.md](osc-7501.md), Rex's apps in [rex-api.md](rex-api.md), and
moving a pane to another machine in [teleport.md](teleport.md).

## Why the server keeps a screen

- **Programs run with no window.** Quitting GoRex closes the windows, not
  the sessions. While no window is attached, the program still prints,
  and something must keep what it shows.
- **An attach shows the screen as it is.** A window that attaches gets a
  snapshot of the emulator, not a replay of the output. A full-screen
  program (vim, lazygit, htop) that redraws only what changed comes back
  exactly, at the size it drew for.
- **Rex's apps start a stream with a Ghostty snapshot (GHOSTSNP).** The
  server takes it from the same emulator (`GhosttySnapshot`).
- **Questions about a session need a screen.** `capture`, `format` (the
  screen as text or as escape sequences), `clear` and `reset` read or
  change the emulator; so do the CLI, rootshell's bridge and teleport's
  `scrollback.txt`. `modes` is answered from a separate tracker of the
  output (`decModes` in `paste.go`), not from the emulator.

## Which emulator

The emulator is libghostty-vt, through MyGo's terminal plugin
(`github.com/egoist/mygo/plugins/terminal`, replaced in `go.mod` by the
fork `si-mods/mygo`). The plugin loads the library at run time with no
cgo, and builds it with Zig for every platform. The server and the
app's windows use the same plugin: a window's terminal is a
`terminal.Terminal` with a view, the server's is one without
(`terminal.New` with a `Conn` that reads nothing, in `newVT`).

The desktop does not embed the whole of Ghostty:

- A window's terminal is a MyGo view, drawn in the app's own scene. The
  window's glass and backdrop shaders compose with it ([DESIGN.md](../DESIGN.md):
  a glass layer over a pane blurs its text, and the terminal's face fades
  by the Terminal opacity setting).
- The same code runs on macOS, Linux and Windows, where the PTY is a
  ConPTY (`pty_windows.go`).
- The server needs a terminal with no view at all.

The iOS app is the other way round. It shows each terminal in a GhosttyKit
surface (rootshell's prebuilt package, Metal renderer; `ios/project.yml`,
`ios/GoRex/Terminal/Ghostty.swift`). GhosttyKit cannot read a snapshot,
so the app also links libghostty-vt (`GhosttyVt.xcframework`, built by
`ios/scripts/build-ghostty-vt.sh` at the commit GoRex's binding uses):
`VTerminal.swift` decodes the GHOSTSNP a stream starts with and turns it
into the escape sequences that draw the same screen. See
[rootshell.md](rootshell.md) for rootshell itself.

## Data flow

```
              program (shell, vim, claude …)
                   │ PTY (forkpty, or ConPTY on Windows)
                   ▼
   session.read ── graphics.filter: Kitty file images → in-band, recorded
                   │
                   ├─► vt.Feed           screen + ~8 MiB scrollback
                   ├─► modes, batch      bracketed paste; mode 2026
                   ├─► status.feed       OSC 7501 records, OSC 133;A
                   └─► each attached client's queue
                                │
      attach: snapshot (+ title, Kitty graphics), then the live output
           ┌────────────────────┼─────────────────────┐
     window of this app   GoRex of another host   rexapi gateway
     (escape sequences)   (escape sequences)      (GHOSTSNP) → Rex apps
```

`session.read` reads up to 64 KiB at a time. Under the session's lock it
feeds the emulator, follows the modes, and queues the same bytes for
every attached client (`attached.send`). Each client has a goroutine that
writes its queue (`attached.writer`), so a slow client does not hold up
the others. A client too far behind (2048 queued chunks; for a remote
one also 8 MiB) is dropped. A remote host's stream attaches again to a new
snapshot (`hostStream` in `hosts.go`); a local window's stream ends, and
its pane closes (`attached.send`).

## Attaching

A client opens a connection whose first line is an `Attach`
(`internal/rex/proto.go`). When the attach gives a size, the server gives
it to the session first (`resizeForAttach`), except while the client that
typed or claimed focus last is still attached, or while a size lock is
held. Then it answers `{"ok":true}`, and sends:

1. **The snapshot** (`snapshotLocked`). If the program is in the middle of
   a synchronized update (mode 2026), the server waits for it to end, at
   most 100 ms (`waitBatchLocked`), so the snapshot is not half a frame.
   - By default: the emulator's escape sequences (`vt.Snapshot`), the
     screen, the scrollback, the cursor and the modes, with the title
     before them and the Kitty graphics after them. `History` cuts the
     oldest scrollback to about that many bytes.
   - With `Snapshot: "ghostty"`: the GHOSTSNP bytes, then the title and
     the Kitty graphics as output. A server that cannot take one sends
     the escape sequences and leaves the format empty.
   - With `Framed`, a `SnapshotHeader` line comes first: the snapshot's
     length, the size and the format, for a client with no emulator of its
     own to know where the snapshot ends.
2. **The live output**, as the program prints it.

What the client writes on the connection is input to the program.

The clients:

| Client | How it attaches |
|---|---|
| A window of this app | `rex.Stream` (`client.go`), the `Conn` of the pane's terminal: escape sequences, not framed. The terminal lays the bytes out at the session's size (`ResizeByConn`). |
| GoRex on another host | the same, over the remote listener (`hosts.go`, `hostStream`); the queue and the writes have limits there. |
| rootshell, through `GoRex control` | framed, with `History` (`internal/control/attach.go`). |
| Rex's apps, through `internal/rexapi` | framed GHOSTSNP (`startStream` in `data.go`). The gateway refuses a stream from a server without `snapshot.ghostty`, or one that fell back to escape sequences. |

### Kitty graphics

A program may name an image by its file path. The window cannot read that
path when the session runs on another host, so the server rewrites
file-medium images to in-band chunks, reading the file where the program
runs (`graphics.filter`, `imageDir`). Images up to 4 MiB are rewritten;
one read adds at most 6 MiB. The server records the graphics sequences,
the last 8 MiB of them (`graphics.record`), and sends them after every
snapshot (`graphics.replay`): the snapshot carries the cells, not the
images. An upgrade hands this history over beside the screen
(`sessionState.Images`).

### After a resize

A window resizes its terminal while the program's output is on its way,
so output drawn for one size can land in a screen of another. Full-screen
programs never repair that. 150 ms after the session's last resize, if
the screen is the alternate one, the server sends every client the
emulator's screen again (`resyncScreen`). That is all the server mends.

The main screen is mended by the app. When a pane's terminal takes a
size the session's screen was not laid out for (it follows another
client's size, or a divider drag ends), the app asks the server for the
screen with `capture` (`resyncPane` in `state.go`): it tries every 50 ms,
up to 20 times, until the capture has the pane's size, then resets the
pane's terminal and feeds it the session's screen and the last 64 KiB of
scrollback (`resyncHistory`). `capture` itself only reads. See
[session-size.md](session-size.md).

## Terminal queries

A program asks its terminal questions: the cursor's position, the device
attributes, the colors, the modes. Every terminal attached to a session
would answer, and the program would read one answer per terminal.

- **The server's emulator answers nothing.** Its `Conn` (`discard`) drops
  what it writes.
- **One client's answers reach the program: the authority's**
  (`authorityLocked`, the client holding the size; see
  [session-size.md](session-size.md)). `admit` passes the authority's
  input as it is. From any other client it removes the answers
  (`dropReplies`): CPR, DA, DSR, DECRPM, the window's size, the keyboard
  protocol's flags, and whole OSC, DCS and APC strings, except OSC 5522
  (Kitty's clipboard), which is a paste.
- **Input that is not a client's** (the `input`, `key`, `mouse`,
  `focusReport` and `paste` ops, `inputProgram`) has its answers removed
  whatever its source.
- **OSC 7501's query the server answers itself**, once per query
  (`statusReply`), since the windows' terminals do not.

### Input encoding

Clients with no terminal of their own, as Rex's apps, send events, not
bytes. The server encodes them with the emulator's modes
(`internal/rex/input.go`): `Key` with `vt.EncodeKey` (legacy sequences,
modifyOtherKeys or the Kitty keyboard protocol, as the program asked),
`Mouse` with `vt.EncodeMouse`, `FocusReport` with `vt.EncodeFocus`. Key
names are Rex's (`key_a`), turned into W3C `KeyboardEvent.code` names
(`KeyCode`). `paste` writes text bracketed when the program asked for
bracketed paste (`session.modes`), which is how a paste reaches a ConPTY
console that drops the terminal's paste event.

## Size

A session has one size, which its emulator and its PTY share
(`resizeLocked` resizes both). The authority's size wins; the rules, the
size lock, focus claims and presence are in
[session-size.md](session-size.md).

## Status records

The server reads OSC 7501 and OSC 133;A from the output in Go
(`status.go`), not in the emulator: MyGo's terminal plugin hands no OSC
7501 reports to Go. The records live in the session (`session.status`),
at most 64 of them. `SessionInfo.Statuses` lists them, and `Status` names
the most urgent. A prompt (133;A), the foreground going back to the shell
(`info`) and the program's exit drop all but `done` and `error`; RIS
(`ESC c`) clears them. The rules and the plan to move to libghostty-vt
are in [osc-7501.md](osc-7501.md).

## Lifetimes

### A session that ends

When the process ends, the server reads the rest of its output, then
keeps what the emulator showed (`run`): the screen as escape sequences
with the Kitty graphics, as GHOSTSNP, as text, and the title. It closes
the emulator. An attach, `capture` or `format` after that gets the kept
screen. After an upgrade, the new server makes the GHOSTSNP and the text
again from the escape sequences (`resumeSession`).

### In-place upgrade (macOS, Linux)

`gorex update` and a newer app replace a running server with a newer
executable in the same process (`upgrade_unix.go`):

1. The new executable must answer `-server -handoff-version` with a
   version this server writes (`probe`).
2. Each session stops reading its PTY (`pause`). What the program prints
   meanwhile waits in the kernel. The attached clients are dropped; they
   attach again to the new server.
3. The server writes `handoff.json` and `exec`s the new executable with
   `-server -resume`. The process keeps its children and the descriptors
   of the listener, the lock and every PTY.

A session's handoff (`sessionState`) carries: the PID and PTY
descriptor, the directory it started in, the size, the screen as the
emulator's snapshot, the graphics history and the title, the output and
bell counters, the last input and output times, the exit state, and the
status records. The new server makes a new emulator, feeds it the title
and the screen (`resumeSession`), reads the program's modes from the
screen again (`decModes`), and reads the PTY again. The programs keep
running, and their screens, modes and images come back.

The handoff takes the escape-sequence form, not GHOSTSNP: the plugin's
documentation gives GHOSTSNP no compatibility guarantee across versions
of libghostty-vt, and the new executable may bind another.

### Restart and crash

`gorex server restart`, a crash, a kill or a reboot end the processes.
The journal (`journal.go`, `layout.jsonl`) keeps the layout. The next
server starts the panes' sessions again as new shells, with the same ids,
at the panes' directories and sizes (`restoreLost`, `restorePane`), and
the windows attach to them. Nothing of the old screens comes back: each
new session starts with an empty emulator. Status records are not in the
journal.

### Windows

A ConPTY console belongs to the process that made it, so a Windows server
does not upgrade in place (`upgrade_windows.go`, `canUpgrade = false`).
A newer app asks the user, then restarts the server (`adopt` in
`workspace.go`); the sessions come back as new shells, as after a
restart.

### Teleport

A teleport packs the pane's text (`format` text, its last MiB) as
`scrollback.txt`. The target feeds it, dimmed, into the new session's
emulator before the program's first output (`CreateOptions.prelude`). See
[teleport.md](teleport.md).

## Limits

- **The scrollback is about 8 MiB of output** per session
  (`scrollback`), and the graphics replay 8 MiB.
- **Two emulators per window.** A window's terminal and the server's
  emulator each parse the output. They agree because both are the same
  libghostty-vt at the same size. When they drift, the server mends the
  alternate screen (`resyncScreen`), and the app the pane it resized
  (`resyncPane`); nothing mends the main screen of a pane whose size did
  not change.
- **Queries go unanswered while the authority answers nothing.** The
  server's emulator drops its answers. The rexapi gateway's attach
  (`startStream`) sends no size and no client id, and never writes: Rex's
  apps send input through the `input` and `key` ops, whose answers are
  removed. The authority is the client holding the size lock, else the
  one that typed or claimed focus last, else the newest attach
  (`authorityLocked`). So a program asking the cursor's position or the
  device attributes gets no answer, and waits for its own timeout, while
  no window or rootshell is attached; while a Rex app holds the size; and
  while the gateway attached last and no window has typed or claimed
  focus since.
- **What looks like an answer is removed** from a client that is not the
  authority, as Shift+F3 (`CSI 1;2R`, a CPR's shape) (`dropReplies`).
- **Screens survive an upgrade, not a restart.** On Windows, every update
  of the server is a restart.
- **A sequence half read at an upgrade is lost.** What the server before
  read of a sequence a read cut, the modes tracker's (`decModes`), the
  graphics filter's (`graphics.pending`) and the emulator's, does not
  reach the new server. The rest of a Kitty transfer whose start it read
  is passed on, not recorded (`graphics.cut`).
- **GHOSTSNP is tied to one libghostty-vt.** The iOS app's
  `GhosttyVt.xcframework` must be built at the commit the server's MyGo
  binds.
