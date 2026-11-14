# Updating the server without restarting the app

Status: Windows phase W1 built; the rest is design.

An update of GoRex today restarts the app before the session server gets
the new build. This design lets the server move to a new build while the
app's windows stay open, and lets the app restart later, when the user does
not see it. Rex works this way; GoRex follows its model.

## How GoRex updates today

The app and the server are one executable. The app starts the server as
`<app exe> -server` (`internal/rex/server.go`, `Spawn`): on macOS a job of
`launchd`'s, so that the app's end leaves the sessions alone
([session-server.md](session-server.md#how-the-server-starts-macos)), and
a detached process on Linux and Windows. Either way the server runs from
the app's bundle.

1. The updater plugin (`mygo/plugins/updater`) downloads the new version.
   With `AutomaticDownloads` on it does this in the background.
2. It installs the new version, then offers to relaunch (`offerRelaunch`).
   - On macOS, `Install` only stages the new bundle (`stageBundle`). A
     helper swaps it into place after the app exits. Until then, the
     bundle on disk is the old build.
   - On Linux and Windows, `Install` replaces the files in place.
3. The user relaunches. The new app connects, sees a server of an older
   build (`staleServer` in `main.go`), and replaces it with its own
   executable (`adopt` in `workspace.go`, `Client.Upgrade`). The server
   `exec`s the new build in the same process and keeps its sessions (see
   [session-server.md](session-server.md#in-place-upgrade-macos-linux)).
   On Windows the server cannot do this; the app asks, then restarts it.

So the server only gets a new build after the app restarts, and the app
only restarts when the user clicks Relaunch.

On macOS the running app cannot simply upgrade the server to the new
build: while the app runs, the new build is not at the app's path, only
in the updater's staging directory. Any early upgrade on macOS needs the
server to run from a copy of its own (step 1 below), as Rex does.

## How Rex does it

From the Rex Beta bundle (build 1887):

- **The server is its own bundle.** It ships as
  `Rex.app/Contents/Helpers/Rex Server.rex`, with the bundle id
  `com.superlogical.rex.server` and `LSUIElement`. The app copies it to
  `~/Library/Application Support/com.superlogical.rex/RexServer/Versions/<build>/Rex Server.app`
  and runs it from there. Sparkle can replace `Rex.app` while that server
  runs; the server's files do not move.
- **The app and the server update separately.**
  - The app relaunches into a new version "while Rex is not visible.
    Sessions keep going." (`RexAppUpdate.AutomaticInstallObserver`)
  - The server does not change with the app. The app shows "A newer server
    is available" and offers "Restart using the server included with this
    app". It also restarts an idle server by itself
    (`RexLocalServer.AutomaticRestartPolicy`, "Automatically restarting
    idle Rex server").
- **Old and new talk to each other.** The API has `api_revision` and
  `api_min_revision`, so an app can use an older or newer server.

## Goals

- When an update is installed, the server moves to the new build, and the
  app's windows stay open and attached.
- The app relaunches into its new build later, when no window shows.
- Sessions keep running through both steps, as they do today.

Not goals:

- Keeping the programs in the panes running through a server update on
  Windows in the first phase. Phase W2 does, by an upgrade in place of its
  own; see [Windows](#windows).
- Shipping the server as a separate product or on its own release
  schedule.

## Design

### 1. Run the server from a versioned copy (macOS) — built

On macOS the new executable does not exist at a stable path until the app
exits: the staged bundle sits in the updater's work directory and moves
when the helper swaps it in. A server that `exec`s the staged file would
report a path that soon goes away. So the server needs its own copy, as
Rex has. `servercopy_darwin.go` keeps it:

- The app copies its whole bundle to
  `~/Library/Application Support/GoRex/server/Versions/<build>/GoRex.app`,
  with a `Current` beside it, and the launchd job runs
  `…/Versions/Current/GoRex.app/Contents/MacOS/GoRex` (see
  [session-server.md](session-server.md#how-the-server-starts-macos)).
  - The whole bundle, not only `Contents/MacOS/GoRex`: a signed executable
    alone fails its code signature without the bundle's sealed resources,
    and macOS privacy permissions follow the signed bundle. The copy keeps
    the bundle id, the Team ID and the seal, so the user's grants stay.
  - `cp -Rpc`: a clone (APFS copy-on-write) where the volume has them, so
    the copy takes no space and no time; a plain copy otherwise. The times
    of the build are kept, so a newer build is told from an older one.
  - `Current` is copied into a temporary name and renamed, so a half copy
    never runs.
  - The copies of the builds before the current one are removed when a
    copy is made: a server that still runs from one keeps its files open.
- `rex.ServerExecutable()` is the target of an in-place upgrade: `adopt`
  upgrades a server of another build to this build's copy.
- Still to do: after the updater stages an update, the app copies the
  **staged** bundle to `Versions/<new build>/` and upgrades the server to
  it before the app restarts (step 2 below); today the copy is made at
  the next launch, from the installed app.
- On Linux, `Install` replaces the file in place and the path stays the
  same. No copy is needed; the server upgrades to the same path.

The open question the step opened — a copy of `GoRex.app`, or a small
helper bundle of its own as Rex has (`LSUIElement`, its own bundle id) —
is answered with the copy: it keeps the app's bundle id and Team ID, and
so the privacy grants the user gave the app, where a new bundle id would
ask for them once more. A Dock icon is not a risk: the server is a
`launchd` job, not an app LaunchServices opens.

### 2. Upgrade the server from the running app

Add a hook to the updater plugin that runs when an update is installed
(or staged). W1 added `updater.OnInstalled(func())` (MyGo's fork), which
needs no path on Windows and Linux; macOS needs the staged bundle's path,
which the hook or the updater must give then.

GoRex uses it in the app:

1. Probe the new executable (`-server -handoff-version`, as `probe` does).
2. If the server can upgrade (`Hello.Upgrade`) and the new build speaks a
   protocol this app speaks, call `Client.Upgrade` with the new path.
3. The connection closes. `store.watch` connects again and `adopt` takes
   the new server. Windows stay open; panes attach again, as after any
   upgrade.

`adopt` must then keep the newer server. `staleServer` calls it stale (its
`Exe` and `ExeTime` differ from the app's), but `adopt` upgrades only a
server older than the app (`at.After(ExeTime)`), so a release build keeps
it. Two things need care:

- Under `mygo dev`, `adopt` restarts any stale server. Limit that to a
  server older than the app too.
- The rule compares modification times. The versioned copy must keep the
  source bundle's times (`clonefile` and `cp -p` do), or the rule must
  compare versions instead.

### 3. Old app, new server

After step 2 the old app talks to a newer server until it relaunches. This
works only if the protocol stays compatible:

- Today any `ProtocolVersion` change makes the app show
  `ErrorServerVersion`. A release that changes `ProtocolVersion` does not
  upgrade the server early; it waits for the app to relaunch, as today.
- Within one `ProtocolVersion`, new features are capabilities
  (`Hello.Capabilities`). The new server must keep serving clients that do
  not ask for them. This is already the rule for remote clients and the
  iOS app.
- To decide, the app compares the new build's protocol version (add it to
  the `-handoff-version` probe output, or a new `-server -version` flag)
  with its own before it upgrades.

### 4. Relaunch the app when no one sees it

Add an automatic relaunch to the updater plugin, as Rex has:

- When an update is installed and automatic updates are on, relaunch when
  the app has no visible window, or when it has not been the active app for
  some minutes and no window is in front.
- Never relaunch while the user types, drags or has a modal open.
- The relaunched app restores its windows from the layout, as today.
- Keep the Relaunch button for users who want it now.

Because the server already runs the new build, the relaunched app finds a
current server and does not upgrade it again.

### 5. Windows and old servers

- Windows: see [Windows](#windows).
- A server that cannot upgrade (`Hello.Upgrade` false): no change; the new
  app handles it at relaunch.
- A server started from the app bundle by an older build: the first
  upgrade moves it to a versioned copy (step 1). No migration is needed.

## Windows

### What differs

- **There is no `exec`.** The server upgrades by starting the new build
  as another process that inherits the consoles' handles (Phase W2). When
  it cannot, a server update on Windows is a restart: the next server
  starts the layout's sessions again as new shells
  (`CapabilityShutdownRestart`), and what they printed is lost.
- **The new build is in place at once.** `Install` swaps the files in the
  executable's directory (`swapFiles` in MyGo): the running `GoRex.exe`
  is renamed to `.GoRex.exe.old-<pid>` and the new one takes its name.
  Windows lets a running executable be renamed, so the app and the server
  keep running the old image, and `GoRex.exe` is the new build while the
  old app is still open. macOS needs a server copy for this (design step
  1); Windows does not.
- **The server already outlives the app.** The app starts it with
  `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`, and with
  `CREATE_BREAKAWAY_FROM_JOB` when the app's job allows it
  (`detached` in `server_windows.go`).

Seen on pc024 on 2026-11-09: `Stop-Process` on the app ended the server
too. That app was started by a one-shot `schtasks /IT` task. Task
Scheduler runs a task in a job object, and if that job does not allow
breakaway, the server stays in it and ends with the task. This is likely,
not confirmed. An app started from Explorer or the Start menu is not in
such a job.

### Phase W1: keep the app, restart the server

Built. The app stays open; the server restarts on the new build. The
programs in the panes end, as they do today, but the user no longer loses
the windows and decides when the restart happens.

1. MyGo's updater plugin calls `updater.OnInstalled` once an update is
   installed, by the user or in the background. GoRex registers it.
2. The hook checks `serverBehind` (`workspace.go`): Windows, the server
   has `CapabilityShutdownRestart`, `GoRex.exe` is newer than the server's
   `ExeTime`, and `GoRex.exe -server -protocol-version` prints this app's
   `ProtocolVersion` (`rex.SpeaksProtocol`). A build of before the flag
   tries to serve, fails as a server runs, and prints nothing: no restart.
3. It asks with the dialog of `adopt` (`ServerRestartAsk*`). Restart calls
   `Client.Restart` and `adopt` takes the next server, as at launch; both
   share `askRestart`. `adopt` checks `serverBehind` too.
4. Later keeps the old server. The palette's **Restart Session Server**
   (`server.restart`, Windows only, refused from clients) asks again while
   the server is behind; the next launch asks too, as before.
5. Not built: the quiet notice, and the restart without asking when the
   server is idle (Rex's `AutomaticRestartPolicy`).

Checked on YIBO-PC (Windows 11) with two builds of a test app (its own
name, data directory and update key) and a local update feed: the real
updater installed the new build 10 s after launch.

- `os.Executable` gives `GoRex.exe`, not the renamed `.old` file, after
  `swapFiles`: Windows keeps the path the process started from. `Spawn`
  starts the new build without a change.
- After Restart: the server's PID changed, its image is the new
  `GoRex.exe` (by hash) while the app runs from `.GoRex.exe.old-<pid>`,
  the window stayed (same handle), the pane attached again with a new
  shell in its directory, and took input. `server.log` shows one
  `serving on` line per server.
- `cleanUpdateLeftovers` ignores a file it cannot remove: with an old
  server still running from `.GoRex.exe.old-<pid>` (and its
  `ghostty-vt.dll`), the new app starts, and the files go at the next
  launch after that server ended.
- The server outlived the app in every case: an app started from
  Explorer, ended by `Stop-Process`, `taskkill /F` and Alt+F4; and an
  app started by a task (`Register-ScheduledTask`, `schtasks /create /it`
  with the app as the action), ended by `Stop-Process` and by
  `schtasks /end`. An app started from Explorer is in a job that allows
  breakaway (the server is in no job); a task's job does not, and the
  server stays in it, but ending the app or the task does not end it.
  pc024's lost server (above) is not explained by Task Scheduler alone;
  `Stop-Process -Name GoRex` would end both processes, which share the
  name. No WMI start is needed for now.

### Phase W2: keep the programs

Built (`upgrade_windows.go`). A console does not belong to the process
that made it: conhost ends it only when its signal pipe breaks. An
`HPCON` points to `{hSignal, hPtyReference, hConPtyProcess}`
(microsoft/terminal, `src/winconpty/winconpty.h`).

1. The server reads the signal pipe from the `HPCON` when it makes a
   console (`consoleSignal`), and checks that it is a pipe and the third
   field a process. When not, `Hello.Upgrade` is false and W1 applies.
2. An upgrade pauses the sessions: `CancelIoEx` stops the blocked read of
   each output pipe, which takes nothing from the pipe. It writes
   `handoff.json` with copies of each console's input, output, signal
   pipe and process handles, closes the socket, and starts
   `GoRex.exe -server -resume` inheriting exactly those handles and a
   pipe of its own (`AdditionalInheritedHandles`).
3. The new server listens again, resumes the sessions and writes to that
   pipe; the old one exits then, without `ClosePseudoConsole`. Clients
   that dial meanwhile try again. When the new one exits first or takes
   more than 10 s, the old one kills it, listens again and resumes.
4. The old server holds the lock until it exits; the new one takes it
   then. A server spawned meanwhile that gets the lock finds the socket
   answering and exits (`Serve`).
5. A resumed console has no `HPCON`: a resize writes
   `PTY_SIGNAL_RESIZE_WINDOW` (8, cols, rows, little-endian `uint16`) to
   the signal pipe, and a hangup closes it.

`updater.OnInstalled` and `adopt` upgrade first; the restart dialog is
left for a server that cannot upgrade or failed to.

## Order of work

1. `adopt` keeps a newer server under `mygo dev` too (small).
2. Versioned server copies on macOS, with cleanup.
3. `updater.OnInstalled` in MyGo, and the early server upgrade in GoRex,
   guarded by the protocol check.
4. Automatic relaunch when hidden, in MyGo's updater plugin.
5. Windows W1: the early restart from the running app, and a check that
   the server outlives the app. Built.
6. Windows W2: upgrade in place. Built.

Steps 1 to 3 already give "the server updates, the app stays". Step 4
only finishes the app's own update.

## Tests

- Unit: `adopt` with an older, equal and newer server build, in release
  and dev builds.
- Unit: the versioned copy is atomic and cleanup keeps the running
  version.
- e2e on the Mac mini (real window, cua-driver): install an update with
  the app open, check that the server PID's executable is the new copy,
  the windows did not close, a running program kept its output, and the
  pane still takes input. Then relaunch and check that the server was not
  upgraded a second time.
- Linux: `gorex update` already upgrades the server in place; add a test
  that a running app reconnects to it.
- Windows (YIBO-PC or pc024, real desktop session, not Session 0): install
  an update with the app open; check that the windows stay, the server
  restarts on the new `GoRex.exe`, and the panes get new shells. End the
  app with `Stop-Process` and check that the server keeps running, for an
  app started from Explorer and one started by `schtasks`.

## Risks

- **Signatures and privacy grants.** A copied bundle must keep its code
  signature valid. Check `codesign --verify --deep` on the copy and that a
  pane's program keeps Full Disk Access after the upgrade.
- **Disk.** Without `clonefile`, each version is a full copy of the
  bundle. Cleanup must run.
- **Protocol drift.** Every release that changes the protocol loses the
  early upgrade. Prefer capabilities to a new `ProtocolVersion`.
- **Two apps.** A second copy of GoRex of an older build must not take the
  server back. `adopt` already refuses; keep it so, also for a copied
  server whose times came from the copy.
