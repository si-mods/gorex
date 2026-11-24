---
name: DSK-004
description: "An update moves the session server to the new build while the app windows stay open. Use when you change app updates or server upgrades."
metadata:
  created: "2026-11-09"
  updated: "2026-11-24"
---

# DSK-004: Updating the server without restarting the app

Status: phase 1 built (#366, #483, #572). The early server upgrade on macOS and Linux and the automatic relaunch of a hidden app are not built.

Today an update of GoRex restarts the app before the session server gets the new build. This design lets the server move to a new build while the app's windows stay open. The app then restarts later, when the user does not see it. Rex works this way and GoRex follows its model. On Windows the early server restart (W1, #366) and the upgrade in place (W2, #483) are built. On macOS the versioned server copy is built (#572). The updater plugin that downloads and installs the update is part of MyGo (`si-mods/mygo`), not of GoRex.

## Problem

The app and the server are one executable. The app starts the server as `<app exe> -server` (`internal/rex/server.go`, `Spawn`). On macOS the server is a job of `launchd`, so the end of the app leaves the sessions alone ([REF-006-session-server.md](REF-006-session-server.md#how-the-server-starts-macos)). On Linux and Windows the server is a detached process. In all cases the server runs from the app's bundle.

An update has these steps:

1. The updater plugin (`mygo/plugins/updater`) downloads the new version. With `AutomaticDownloads` on, it downloads in the background.
2. The plugin installs the new version, then offers to relaunch (`offerRelaunch` in MyGo's updater).
   - On macOS, `Install` only stages the new bundle (`stageBundle` in MyGo's updater). A helper swaps the bundle into place after the app exits. Until then, the bundle on disk is the old build.
   - On Linux and Windows, `Install` replaces the files in place.
3. The user relaunches. The new app connects and sees a server of an older build (`staleServer` in `main.go`). The app replaces the server with its own executable (`adopt` in `workspace.go`, `Client.Upgrade`). The server calls `exec` on the new build in the same process and keeps its sessions (see [REF-006-session-server.md](REF-006-session-server.md#in-place-upgrade-macos-linux)). On Windows the server cannot do this. The app asks the user, then restarts the server.

So the server gets a new build only after the app restarts. The app restarts only when the user clicks Relaunch.

On macOS the running app cannot upgrade the server to the new build at once. While the app runs, the new build is not at the app's path. It is only in the staging directory of the updater. Any early upgrade on macOS needs the server to run from a copy of its own (step 1 of the design), as Rex does.

## How Rex does it

This section comes from the Rex Beta bundle (build 1887).

- **The server is its own bundle.** It ships as `Rex.app/Contents/Helpers/Rex Server.rex`, with the bundle id `com.superlogical.rex.server` and `LSUIElement`. The app copies it to `~/Library/Application Support/com.superlogical.rex/RexServer/Versions/<build>/Rex Server.app` and runs it from there. Sparkle can replace `Rex.app` while that server runs. The server's files do not move.
- **The app and the server update separately.**
  - The app relaunches into a new version "while Rex is not visible. Sessions keep going." (`RexAppUpdate.AutomaticInstallObserver`)
  - The server does not change with the app. The app shows "A newer server is available" and offers "Restart using the server included with this app". The app also restarts an idle server by itself (`RexLocalServer.AutomaticRestartPolicy`, "Automatically restarting idle Rex server").
- **Old and new talk to each other.** The API has `api_revision` and `api_min_revision`, so an app can use an older or newer server.

## Goal

- When an update is installed, the server moves to the new build. The app's windows stay open and attached.
- The app relaunches into its new build later, when no window shows.
- Sessions keep running through both steps, as they do today.

## Non-goals

- Keeping the programs in the panes running through a server update on Windows in the first phase. Phase W2 does this with an upgrade in place of its own. See [Windows](#windows).
- Shipping the server as a separate product or on its own release schedule.

## Design

### 1. Run the server from a versioned copy (macOS)

Status of this step: built (#572).

On macOS the new executable has no stable path until the app exits. The staged bundle sits in the work directory of the updater. It moves when the helper swaps it in. A server that calls `exec` on the staged file would report a path that soon goes away. So the server needs its own copy, as Rex has. `servercopy_darwin.go` keeps the copy:

- The app copies its whole bundle to `~/.gorex/server/Versions/<build>/GoRex.app`, with a `Current` beside it. The launchd job runs `…/Versions/Current/GoRex.app/Contents/MacOS/GoRex` (see [REF-006-session-server.md](REF-006-session-server.md#how-the-server-starts-macos)).
  - The copy holds the whole bundle, not only `Contents/MacOS/GoRex`. A signed executable alone fails its code signature without the sealed resources of the bundle. Also, macOS privacy permissions follow the signed bundle. The copy keeps the bundle id, the Team ID and the seal, so the grants of the user stay.
  - The copy uses `cp -Rpc`. This makes a clone (APFS copy-on-write) where the volume has clones, so the copy takes no space and no time. Elsewhere it makes a plain copy. The copy keeps the times of the build, so the app can tell a newer build from an older build.
  - The app copies `Current` into a temporary name and renames it, so a half copy never runs.
  - The app removes the copies of the builds before the current one when it makes a copy. A server that still runs from one of them keeps its files open.
- `rex.ServerExecutable()` is the target of an in-place upgrade. `adopt` upgrades a server of another build to the copy of this build.
- Not built: after the updater stages an update, the app copies the **staged** bundle to `Versions/<new build>/`. The app then upgrades the server to it before the app restarts (step 2). Today the app makes the copy at the next launch, from the installed app.
- On Linux, `Install` replaces the file in place and the path stays the same. No copy is needed. The server upgrades to the same path.

### CLI updates install in place (Linux)

`gorex update` first required the per-user install of `install.sh`. It now
replaces the files beside the executable wherever it runs from, when the
user running the command can write that directory: `mygo.Updater.Install`
targets the directory of the executable on Linux, and the server that runs
from the same path takes the new build through the handoff of `gorex
server upgrade`. The install path stays the same, so no versioned copy is
needed, as with an app-initiated update. A build a dpkg package installed
is updated by that package, and the command names the package before it
installs. With no server running, the command installs and stops: the
next start runs the new build.

The session handoff needs the server the command connects to, and that is
the one of its `GOREX_DIR`: a service that keeps its data directory of its
own is updated with the same environment, as in `sudo env
GOREX_DIR=/var/lib/gorex /opt/gorex/gorex update`. Without it the files are
installed and the running server keeps its build; a restart starts the new
build with new shells, which is the session loss the update exists to
avoid. The same goes for a server the user cannot reach: the command fails
before it installs.

This step opened one question: a copy of `GoRex.app`, or a small helper bundle of its own as Rex has (`LSUIElement`, its own bundle id)? The answer is the copy. The copy keeps the bundle id and the Team ID of the app, and so keeps the privacy grants that the user gave the app. A new bundle id would ask for them again. A Dock icon is not a risk, because the server is a `launchd` job and not an app that LaunchServices opens.

### 2. Upgrade the server from the running app

Add a hook to the updater plugin. The hook runs when an update is installed or staged. W1 added `updater.OnInstalled(func())` (MyGo's fork). It needs no path on Windows and Linux. macOS needs the path of the staged bundle, which the hook or the updater must give at that time.

In `main.go`, GoRex registers the hook with `updater.OnInstalled`. The hook calls `store.upgradeServer` (`workspace.go`), and then `serverBehind` and `restartServer` if the upgrade did not happen. `upgradeServer` returns false when the platform is not Windows. So the early upgrade works only on Windows today. The design for all platforms is:

1. Probe the new executable (`-server -handoff-version`, as `probe` does).
2. If the server can upgrade (`Hello.Upgrade`) and the new build speaks a protocol that this app speaks, call `Client.Upgrade` with the new path.
3. The connection closes. `store.watch` connects again and `adopt` takes the new server. Windows stay open. Panes attach again, as after any upgrade.

`adopt` must then keep the newer server. `staleServer` calls it stale, because its `Exe` and `ExeTime` differ from the values of the app. But `adopt` upgrades only a server older than the app (`at.After(ExeTime)`), so a release build keeps it. Two points need care:

- Under `mygo dev`, `adopt` restarts any stale server. Limit this to a server older than the app too.
- The rule compares modification times. The versioned copy must keep the times of the source bundle (`clonefile` and `cp -p` do this). If it does not, the rule must compare versions instead.

### 3. Old app, new server

After step 2 the old app talks to a newer server until it relaunches. This works only if the protocol stays compatible:

- Today any `ProtocolVersion` change makes the app show `ErrorServerVersion`. A release that changes `ProtocolVersion` does not upgrade the server early. It waits for the app to relaunch, as today.
- Within one `ProtocolVersion`, new features are capabilities (`Hello.Capabilities`). The new server must keep serving clients that do not ask for them. This is already the rule for remote clients and the iOS app.
- To decide, the app compares the protocol version of the new build with its own before it upgrades. The version comes from the `-handoff-version` probe output, or from a new `-server -version` flag. On Windows, the `-server -protocol-version` flag and `rex.SpeaksProtocol` do this check today.

### 4. Relaunch the app when no one sees it

Not built. Add an automatic relaunch to the updater plugin, as Rex has:

- When an update is installed and automatic updates are on, relaunch when the app has no visible window. Also relaunch when the app has not been the active app for some minutes and no window is in front.
- Never relaunch while the user types, drags or has a modal open.
- The relaunched app restores its windows from the layout, as today.
- Keep the Relaunch button for users who want it now.

The server already runs the new build. So the relaunched app finds a current server and does not upgrade it again.

### 5. Windows and old servers

- Windows: see [Windows](#windows).
- A server that cannot upgrade (`Hello.Upgrade` false): no change. The new app handles it at relaunch.
- A server that an older build started from the app bundle: the first upgrade moves it to a versioned copy (step 1). No migration is needed.

### Windows

#### What differs

- **There is no `exec`.** The server upgrades by starting the new build as another process that inherits the handles of the consoles (Phase W2). When it cannot, a server update on Windows is a restart. The next server starts the sessions of the layout again as new shells (`CapabilityShutdownRestart`). What the old shells printed is lost.
- **The new build is in place at once.** `Install` swaps the files in the directory of the executable (`swapFiles` in MyGo's updater). The running `GoRex.exe` gets the name `.GoRex.exe.old-<pid>` and the new file takes its name. Windows lets a running executable be renamed. So the app and the server keep running the old image, and `GoRex.exe` is the new build while the old app is still open. macOS needs a server copy for this (design step 1). Windows does not.
- **The server already outlives the app.** The app starts it with `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`. It adds `CREATE_BREAKAWAY_FROM_JOB` when the job of the app allows it (`detached` in `internal/rex/server_windows.go`).

On a Windows test PC (`alice-pc`) on 2026-11-09, `Stop-Process` on the app ended the server too. A one-shot `schtasks /IT` task started that app. Task Scheduler runs a task in a job object. If that job does not allow breakaway, the server stays in the job and ends with the task. This is likely and not confirmed. An app that starts from Explorer or the Start menu is not in such a job.

#### Phase W1: keep the app, restart the server

Built (#366). The app stays open and the server restarts on the new build. The programs in the panes end, as they do today. But the user no longer loses the windows, and the user decides when the restart happens.

1. The updater plugin of MyGo calls `updater.OnInstalled` once an update is installed, by the user or in the background. GoRex registers the hook.
2. The hook checks `serverBehind` (`workspace.go`). It is true when all of these conditions are true:
   - The platform is Windows.
   - The server has `CapabilityShutdownRestart`.
   - `GoRex.exe` is newer than the `ExeTime` of the server.
   - `GoRex.exe -server -protocol-version` prints the `ProtocolVersion` of this app (`rex.SpeaksProtocol`).

   A build from before the flag tries to serve, fails as a server runs, and prints nothing. In that case GoRex does not restart the server.
3. The hook asks the user with the dialog of `adopt` (`ServerRestartAsk*`). Restart calls `Client.Restart`, and `adopt` takes the next server, as at launch. Both use `askRestart`. `adopt` checks `serverBehind` too.
4. Later keeps the old server. The palette command **Restart Session Server** (`server.restart`, Windows only, refused from clients) asks again while the server is behind. The next launch asks too, as before.
5. Not built: the quiet notice, and the restart without a question when the server is idle (Rex's `AutomaticRestartPolicy`).

The check ran on a Windows 11 test PC with two builds of a test app. The test app had its own name, data directory and update key. A local update feed served the builds. The real updater installed the new build 10 s after launch.

- `os.Executable` gives `GoRex.exe` after `swapFiles`, not the renamed `.old` file. Windows keeps the path that the process started from. `Spawn` starts the new build without a change.
- After Restart, the PID of the server changed. Its image is the new `GoRex.exe` (by hash), while the app runs from `.GoRex.exe.old-<pid>`. The window stayed (same handle). The pane attached again with a new shell in its directory, and the pane took input. `server.log` shows one `serving on` line for each server.
- `cleanUpdateLeftovers` (in MyGo's `app.go`) ignores a file that it cannot remove. An old server can still run from `.GoRex.exe.old-<pid>` (and its `ghostty-vt.dll`). The new app starts anyway, and the files go at the next launch after that server ended.
- The server outlived the app in every case.
  - An app that started from Explorer ended by `Stop-Process`, `taskkill /F` and Alt+F4.
  - An app that started by a task (`Register-ScheduledTask`, `schtasks /create /it` with the app as the action) ended by `Stop-Process` and by `schtasks /end`.
  - An app that starts from Explorer is in a job that allows breakaway, and the server is in no job. The job of a task does not allow breakaway, and the server stays in it. But ending the app or the task does not end the server.
  - The server lost on the test PC (above) is not explained by Task Scheduler alone. `Stop-Process -Name GoRex` would end both processes, which share the name. No WMI start is needed for now.

#### Phase W2: keep the programs

Built (#483, `internal/rex/upgrade_windows.go`). A console does not belong to the process that made it. Conhost ends a console only when its signal pipe breaks. An `HPCON` points to `{hSignal, hPtyReference, hConPtyProcess}` (microsoft/terminal, `src/winconpty/winconpty.h`).

1. The server reads the signal pipe from the `HPCON` when it makes a console (`consoleSignal`). It checks that the signal pipe is a pipe and that the third field is a process. If a check fails, `Hello.Upgrade` is false and W1 applies.
2. An upgrade pauses the sessions. `CancelIoEx` stops the blocked read of each output pipe, and takes nothing from the pipe. The server writes `handoff.json` with copies of the input, output, signal pipe and process handles of each console. It closes the socket. It starts `GoRex.exe -server -resume`, which inherits exactly those handles and a pipe of its own (`AdditionalInheritedHandles`).
3. The new server listens again, resumes the sessions and writes to that pipe. The old server then exits, without `ClosePseudoConsole`. Clients that dial in the meantime try again. If the new server exits first or takes more than 10 s, the old server kills it, listens again and resumes.
4. The old server holds the lock until it exits. The new server takes the lock then. A server that starts in the meantime and gets the lock finds the socket answering and exits (`Serve`).
5. A resumed console has no `HPCON`. A resize writes `PTY_SIGNAL_RESIZE_WINDOW` (8, cols, rows, little-endian `uint16`) to the signal pipe. A hangup closes the signal pipe.

`updater.OnInstalled` and `adopt` upgrade first. The restart dialog is for a server that cannot upgrade or that failed to upgrade.

### Risks

- **Signatures and privacy grants.** A copied bundle must keep its code signature valid. Check `codesign --verify --deep` on the copy. Check that the program of a pane keeps Full Disk Access after the upgrade.
- **Disk.** Without `clonefile`, each version is a full copy of the bundle. Cleanup must run.
- **Protocol drift.** Every release that changes the protocol loses the early upgrade. Prefer capabilities to a new `ProtocolVersion`.
- **Two apps.** A second copy of GoRex of an older build must not take the server back. `adopt` already refuses. Keep it so, also for a copied server whose times came from the copy.

## Tests

- Unit: `adopt` with an older, equal and newer server build, in release and dev builds.
- Unit: the versioned copy is atomic, and cleanup keeps the running version.
- End-to-end on a Mac (real window, cua-driver): install an update with the app open. Check that the executable of the server PID is the new copy. Check that the windows did not close, that a running program kept its output, and that the pane still takes input. Then relaunch and check that the app did not upgrade the server a second time.
- Linux: `gorex update` already upgrades the server in place. Add a test that a running app reconnects to it.
- Windows (a test PC with a real desktop session, not Session 0): install an update with the app open. Check that the windows stay, the server restarts on the new `GoRex.exe`, and the panes get new shells. End the app with `Stop-Process` and check that the server keeps running. Do this for an app that starts from Explorer and for an app that starts by `schtasks`.

## Phases

1. `adopt` keeps a newer server under `mygo dev` too (small).
2. Versioned server copies on macOS, with cleanup. Built (#572).
3. `updater.OnInstalled` in MyGo, and the early server upgrade in GoRex, with the protocol check as guard. Built on Windows only (#366, #483).
4. Automatic relaunch when hidden, in the updater plugin of MyGo. Not built.
5. Windows W1: the early restart from the running app, and a check that the server outlives the app. Built (#366).
6. Windows W2: upgrade in place. Built (#483).

Steps 1 to 3 already give "the server updates, the app stays". Step 4 only finishes the update of the app.
