# Updating the server without restarting the app

Status: design, not built.

An update of GoRex today restarts the app before the session server gets
the new build. This design lets the server move to a new build while the
app's windows stay open, and lets the app restart later, when the user does
not see it. Rex works this way; GoRex follows its model.

## How GoRex updates today

The app and the server are one executable. The app starts the server as
`<app exe> -server` (`internal/rex/server.go`, `exec.Command(exe,
"-server")`), so the server runs from the app's bundle.

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

- Windows. Its server cannot upgrade in place (`upgrade_windows.go`); a
  restart there ends the programs in the panes. Windows keeps today's
  flow.
- Shipping the server as a separate product or on its own release
  schedule.

## Design

### 1. Run the server from a versioned copy (macOS)

On macOS the new executable does not exist at a stable path until the app
exits: the staged bundle sits in the updater's work directory and moves
when the helper swaps it in. A server that `exec`s the staged file would
report a path that soon goes away. So the server needs its own copy, as
Rex has.

- The app copies its whole bundle to
  `~/Library/Application Support/GoRex/Server/<version>/GoRex.app` and
  starts the server from that copy, not from its own bundle.
  - Copy the whole bundle, not only `Contents/MacOS/GoRex`. A signed
    executable alone fails its code signature without the bundle's sealed
    resources, and macOS privacy permissions (Full Disk Access, screen
    recording) follow the signed bundle. A copy with the same bundle id
    and Team ID keeps the user's grants.
  - Use `clonefile` (APFS copy-on-write) so the copy takes no space and no
    time. Fall back to a plain copy.
  - Copy into a temporary name, then rename, so a half copy never runs.
  - Remove versions that no server runs, on server start, keeping the
    current one.
- After the updater stages an update, the app copies the **staged**
  bundle to `Server/<new version>/` and upgrades the server to it with
  `Client.Upgrade`. Today's handoff does the rest.
- On Linux, `Install` replaces the file in place and the path stays the
  same. No copy is needed; the server upgrades to the same path.

Open question: whether the server should keep the `GoRex` bundle or get
a small helper bundle of its own, as Rex has (`LSUIElement`, its own
bundle id). A helper bundle stops the server from showing a Dock icon in
edge cases and makes Activity Monitor clearer, but it needs build and
notarization changes, and a new bundle id loses the privacy grants once.
Start with a copy of `GoRex.app`.

### 2. Upgrade the server from the running app

Add a hook to the updater plugin that runs when an update is installed
(or staged), for example `updater.OnInstalled(func(dir string))`, with the
path of the new bundle or executable.

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

- Windows: no change. The new app asks, then restarts the server.
- A server that cannot upgrade (`Hello.Upgrade` false): no change; the new
  app handles it at relaunch.
- A server started from the app bundle by an older build: the first
  upgrade moves it to a versioned copy (step 1). No migration is needed.

## Order of work

1. `adopt` keeps a newer server under `mygo dev` too (small).
2. Versioned server copies on macOS, with cleanup.
3. `updater.OnInstalled` in MyGo, and the early server upgrade in GoRex,
   guarded by the protocol check.
4. Automatic relaunch when hidden, in MyGo's updater plugin.

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
