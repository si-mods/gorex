---
name: MOD-013
description: "GoRex keeps one home directory, ~/.gorex, for its configuration and its data, on every desktop platform, and moves there on its own. Use when you change config.Path, rex.Dir, iconpack.Dir, theme.Dir, the server launch job, or anything that reads or writes a file of the app."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# MOD-013: One home directory for configuration and data

Status: built (this PR).

This doc decides the directory that holds every file GoRex reads or writes on the desktop: the configuration (`init.lua`, themes, icon packs) and the data (`settings.json`, `layout.json`, the server's socket and log). It changes `config.Path` in `internal/config/config.go` and `Dir` in `internal/rex/proto.go`, and it adds a one-time move of the two legacy directories.

## Problem

GoRex keeps two directories that no user can tell apart:

- The **data directory** (`Dir` in `internal/rex/proto.go`, overridable with `GOREX_DIR`) holds `settings.json`, `layout.json`, `layout.jsonl`, `hosts.json`, the notes, the `bin` shim folder, `app.log`, the server's socket and `server/Versions/<build>/`. Its default is the platform configuration directory: `~/Library/Application Support/GoRex` on macOS, `~/.config/GoRex` on Linux, `%APPDATA%\GoRex` on Windows. A development build adds ` Dev` (`dataDir` in `main.go`).
- The **configuration directory** (`config.Dir` in `internal/config/config.go`, overridable with `GOREX_CONFIG` naming `init.lua`) holds `init.lua`, `themes/` and `icon-packs/`. Its default is `~/.config/gorex` on macOS and Linux, `%APPDATA%\gorex` on Windows.

A user who looks for the theme they installed, the settings they wrote, or the log the app kept finds two trees on different rules. The documentation names both. The data directory mixes files a user edits (`settings.json`) with files a user never opens (the socket, `layout.jsonl`), and the XDG split of "configuration" from "data" is a distinction that terminal tools do not ask their users to make. Claude Code puts everything in `~/.claude`.

## Goal

- One directory, `~/.gorex` on macOS and Linux and `%USERPROFILE%\.gorex` on Windows, holds every file of the desktop app, the CLI and the session server.
- An existing installation moves itself to `~/.gorex` on the next start of the session server, with no file lost and no restart forced. A running server is never touched.
- `GOREX_DIR` and `GOREX_CONFIG` keep their meaning and keep winning over every default.
- A build from before the move still finds the moved files.

## Non-goals

- The iOS app's container does not change.
- The directory of the legacy Rex app (`~/Library/Application Support/rex`, read by `internal/rexpeer`) does not move. A new Rex installation follows this doc on its own.
- Ghostty's configuration keeps its own paths (`GOREX_GHOSTTY_CONFIG`, [REF-011](REF-011-ghostty-config.md)).
- The agent configuration directories that skills and hooks install into (`~/.claude`, `~/.config/devin`, [REF-001](REF-001-cli.md)) do not change.
- No file format changes. The move changes paths only.

## Design

### Behavior

The user sees one folder. After an update that ships this change, `~/Library/Application Support/GoRex` and `~/.config/gorex` are gone and `~/.gorex` holds what they held:

```
~/.gorex/
  init.lua            # was ~/.config/gorex/init.lua
  settings.json       # was <data>/settings.json
  layout.json, layout.jsonl, hosts.json, notes/, app.log
  bin/                # the gorex shim (GOREX_BIN_DIR)
  themes/, <id>.assets/, icon-packs/   # were <config>/themes/, <config>/icon-packs/
  server/Versions/<build>/, Current    # the session server's copies
  server.sock                          # the live socket, while the server runs
```

A development build keeps a directory of its own, `~/.gorex-dev`, so developing never touches the home of the installed app.

The move is lazy. It happens once, the first time a session server starts and finds the legacy trees and no `~/.gorex`. A user who never stops the server keeps the legacy directories for as long as that server runs, and the app, the CLI and the server all keep working through that time, because every process resolves the directory the same way (see Model). The move is quiet: it writes one line to `app.log` in the new directory, and a failure writes the reason and keeps the legacy paths.

### Model

The **home directory** `home` is the root of every path the app owns. `GOREX_DIR` names it directly. `GOREX_CONFIG` names `init.lua`, so it names the parent of the file, as today.

Every process resolves the home the same way, in this order:

1. `GOREX_DIR`, when it is set.
2. `~/.gorex`, when the directory exists. A symbolic link there is the user's own choice and the resolution follows it.
3. The legacy data directory, when it exists.
4. `~/.gorex`, for a fresh installation.

The configuration file resolves in parallel:

1. `GOREX_CONFIG`, when it is set.
2. `~/.gorex/init.lua`, when the file exists.
3. The legacy `init.lua` (`<XDG_CONFIG_HOME>/gorex/init.lua`, or the platform path), when the file exists.
4. `~/.gorex/init.lua`, for a fresh installation.

`iconpack.Dir` and `theme.Dir` already derive from `config.Dir` and follow it. The socket path, the journal and the asset folders already derive from `rex.Dir` and follow it.

### The move

The **migration** runs in the code path that starts the session server (the app spawning it, `gorex server`, the launchd job's own start), before the socket is bound, and only when all of these hold:

- `GOREX_DIR` and `GOREX_CONFIG` are not set. A user who names the directories keeps them.
- `~/.gorex` does not exist.
- No live server answers at the legacy socket. A running server owns the directory and the move would drop its socket path.

The steps, each of which may fail without breaking the next:

1. Merge the legacy configuration directory into the legacy data directory: `init.lua`, `themes/`, `icon-packs/` move over. A name that the data directory already has stays, and the legacy file is left in place with a warning.
2. Rename the legacy data directory to `~/.gorex`. When the rename fails across devices, copy the tree and remove the source after a full copy.
3. Rewrite the launchd job of the session server when its plist names the legacy path: `ProgramArguments` and the working directory point at `server/Versions/Current` under the new home. No server runs at this step, so loading the job again is safe.
4. Link the legacy paths to the new home: a symbolic link at the legacy data directory and at the legacy configuration directory, so a build, a script or a user that still looks at the old paths finds the files. A link that cannot be made (a filesystem without links, Windows without the privilege) is a warning, not an error.
5. Remove nothing else. When a step above dropped a file, the legacy directory stays and the user sees it.

When `~/.gorex` exists already and a legacy directory exists too (an interrupted move, a user who made the directory by hand), the home resolves to `~/.gorex` by the order above, the legacy directory is left untouched, and a log line names it. There is no second merge: the user may have put different files in the two places on purpose.

### Code

- `internal/rex/proto.go`: `Dir` gets the resolution order. New `home.go` (or `internal/homedir`) holds the platform paths, the order, the merge and the move, so `internal/config` and `main` share it.
- `internal/config/config.go`: `Path` gets the parallel order for `init.lua`.
- `main.go`: `dataDir` maps the dev build to `~/.gorex-dev` rather than `GoRex Dev`.
- `internal/rex/servercopy_darwin.go`: the launchd plist rewrite of step 3, next to the code that writes the job (`spawnJob` in `internal/rex/job_darwin.go`).
- The call site of the migration is the server-start path: `Serve` in `internal/rex` and the app's `spawnJob`, before the socket. A client-only process (a `gorex` command that only attaches) resolves the home but never migrates.
- `internal/rexpeer`: unchanged. It reads the legacy Rex app's directory, which this doc does not cover.
- Docs to update in the code PR: [REF-002](REF-002-lua.md) and [REF-014](REF-014-develop.md) name the paths, and [REF-001](REF-001-cli.md) and [DSK-004](DSK-004-app-server-update.md) name the data directory.

## Alternatives

- **Read the old paths, write the new ones**: no move, but every read carries two paths forever, and the two trees never become one. Rejected: the split stays visible to the user.
- **Move at app start, not server start**: the app can run while a server from an older build still runs. Moving the directory drops that server's socket path, and the CLI can no longer find it. The migration waits for a moment when no server runs.
- **Migrate now, unconditionally**: kill or upgrade the server and move at once. Rejected by the upgrade rule: a running server's shells die with it, and nothing brings them back.
- **Keep the XDG split on Linux**: a Linux user may expect `~/.config`. Rejected for the same reason the split goes away elsewhere: one entry point beats platform purity, and `~/.gorex` is what the other platforms get.

## Security

The move copies and renames only paths under the user's home and the platform directories that GoRex itself created. It follows no symbolic link inside the trees it moves: an entry that is a link is copied as a link, and the copy of a directory tree does not resolve links. `~/.gorex` itself as a symbolic link is a configuration the user made and the resolution accepts it, so a user can put the home on another volume.

The socket keeps its permissions: `0700` on the directory, as today.

## Migration

- A **new build with a running old-build server** resolves to the legacy directory through rule 3 and attaches as before. The move waits for the server's next start. `gorex server upgrade` keeps the process and its directory, so the move lands on the next fresh server start, usually the next login or reboot.
- A **build from before the move** finds the symbolic links at the legacy paths and works without a change. Where the links could not be made, an old build starts over: it makes a fresh directory at the legacy path and sees no sessions, which is the same as an old build on a clean machine. The moved data stays in `~/.gorex` and a new build picks it up again.
- A **downgrade after the move**: the old build resolves `~/.gorex` through nothing, but finds the links. Without links it starts over as above.
- **Windows**: the legacy paths are `%APPDATA%\GoRex` and `%APPDATA%\gorex`. A directory junction links them to `~\.gorex`; a failure to make the junction is the no-link case.

## Tests

- `TestHomeResolve`: a temporary `HOME` with each combination of `~/.gorex`, the legacy data directory and `GOREX_DIR` resolves the expected root.
- `TestConfigPathResolve`: the parallel order for `init.lua`, including `GOREX_CONFIG`.
- `TestMigrate`: a planted legacy tree (data + configuration) becomes `~/.gorex`, the configuration entries merge, a name conflict keeps the data file, the links point at the new home, and a second run does nothing.
- `TestMigrateKeepsRunningServer`: a live socket at the legacy path leaves every file in place.
- `TestMigrateLaunchdPlist` (macOS): a plist that names the legacy `server/Versions` path is rewritten.
- `TestMigrateExistingHome`: `~/.gorex` and a legacy directory both present leaves the legacy directory untouched.
- End to end: `GOREX_DIR` unset, planted legacy trees, `gorex server` start on a scratch `HOME`, then `gorex` attaches through the link.

## Phases

1. **This doc** (PR: this one).
2. **The move**: the resolution order, the merge, the move, the plist rewrite and the links, in the release after this doc.
