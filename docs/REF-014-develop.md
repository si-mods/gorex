---
name: REF-014
description: "Building and releasing GoRex: the mygo commands, signing, the release workflow, the data directory and a map of the code. Use when you cut a release, change the build or look for where a part lives."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-014: Build, release and the code map

Status: reference. It describes the code in main.

## Build

GoRex needs [Go](https://go.dev/dl/) 1.27+ and MyGo 0.7.2, whose CLI `go.mod` pins as a tool:

```sh
go tool mygo dev     # GoRex Dev, rebuilt and restarted as the code changes
go test ./...        # the server, and the view without a window
go tool mygo build   # build/darwin-arm64/GoRex.app and a .dmg
go run ./tools/mkicon -web  # redraw the web client's icons in web/pwa
```

`go tool mygo dev -background` starts the app and each rebuild without bringing it to the front. Use it for runs that no one is watching.

`go get -tool github.com/egoist/mygo/cmd/mygo@latest` updates MyGo and its CLI together.

`go tool mygo build` signs the app with the Developer ID that `macos.signingIdentity` names. macOS then keeps its privacy answers (such as media library access, which programs in the terminal ask for as GoRex) from one build to the next. An ad-hoc signature changes with every build. Without that identity, pass `-sign -`. The Release workflow signs with the same identity, from the secrets `MACOS_DEVELOPER_ID_P12` (the identity's .p12, base64) and `MACOS_DEVELOPER_ID_P12_PASSWORD`. It notarizes the disk image with the App Store Connect API key `ASC_KEY_P8`.

## Release

Releases update installed apps, signed with a key that only the maintainer holds. Once, run `go tool mygo keygen` and put the contents of `mygo-update.pub` in `updates.publicKey` of `mygo.json`. Keep `mygo-update.key` out of the repository. For each release, add a `## 0.1.0` section (the version of `mygo.json`) to `CHANGELOG.md`. The section becomes the release notes. Then run the Release workflow on `main`, which builds every platform, signs the updates with the repository secret `MYGO_UPDATER_PRIVATE_KEY` (the contents of `mygo-update.key`) and uploads them to a draft GitHub release. Publish the draft:

```sh
gh workflow run release.yml -R si-mods/gorex
```

Development builds and apps that cannot write where they are installed do not update. "Check for Updates…" is in the Shell menu and the command palette.

## The data directory

The app keeps its state in its home directory ([MOD-013](MOD-013-home-dir.md)): the server's socket and log, the app's own log `app.log` (kept as `app.log.1` at the next start once it passes 1 MiB), `layout.json` and `settings.json`, beside `init.lua`, `themes/` and `icon-packs/`. The directory is `~/.gorex` for the built app and `~/.gorex-dev` for `mygo dev`'s, so developing never touches the sessions of the app you use. `GOREX_DIR` names another directory. `layout.jsonl` beside the layout is a journal of every layout the app sent, and of each run's start and stop. A server that starts after one that left no stop record, a crash, a kill or a reboot, starts the sessions of the last layout again, the same ids in the same directories. The windows attach to them as they were. A pane whose session is lost keeps what ran there. A strip above its shell names the program and runs it again on a click, as the command the shell would take. For Claude Code, Codex, omp or Pi it continues the last conversation in the pane's directory, which the agent itself finds, so no conversation id has to be kept. The process itself does not survive a reboot. A run that stops says why and what it held, and the server after it starts nothing. The sessions outlive the app and a new build. On macOS and Linux, a build that finds a server an older build started has it run the new build in its place. For a headless server, run `gorex server upgrade` with the new executable and the existing server's data directory (`GOREX_DIR`). The server continues in the same process, its shells still running and their screens kept. On Windows a development build replaces such a server, ending its sessions. The sessions then start again in the same directories, and the built app keeps it.

## The code map

| | |
|---|---|
| `main.go` | the app, its window, and `-server` |
| `state.go`, `layoutcmd.go`, `adoptions.go`, `shown.go` | tabs, the tree of splits, panes, saving and restoring the layout. Also the layout commands the server applies, adopting another host's panes, and what each window shows |
| `app.go`, `float.go`, `browser.go`, `note.go`, `notesave.go` | the window's state and its posted changes. Also floating panes and how a throw comes to rest, the browser pane and its URLs, the note pane and its saved text |
| `canvas.go`, `panedrop.go`, `teleport.go` | the infinite canvas, a pane dropped on another, teleport to a host |
| `workspace.go`, `workspaceview.go`, `space.go`, `spaces.go`, `project.go`, `projects_sidebar.go`, `projectpicker.go`, `projectsheet.go` | projects of tabs (workspaces in the code) and the windows showing them. Also the project chip, its list, the sidebar rows, the picker and the folder sheet |
| `view.go`, `tabs.go`, `sidebar.go`, `menus.go`, `commands.go`, `keymap.go`, `host.go` | the interface: title bar, tabs and their sidebar, panes, menus and palette, key bindings and the host chip |
| `hosts.go`, `hostsview.go`, `hostlayout.go`, `hostrename.go`, `hostqr.go`, `remotetailnet.go`, `remoteview.go` | remote hosts: the saved list and its views, their layouts, renaming, the pairing QR, tailnet state and Settings › Server |
| `programs.go`, `program_icon.go`, `symbol.go` | how programs show: names, glyphs, tile colors and icons |
| `tokens.go`, `style.go`, `settings.go`, `luaconfig.go`, `ghostty.go`, `fonts.go` | the design tokens of DESIGN.md, colors, fonts and terminal themes, the user's choices over Ghostty's, the Lua configuration, reading Ghostty's config, the embedded fonts |
| `settingsview.go`, `shortcutsview.go`, `iconpacksview.go` | the Settings window: its pages, the shortcuts list and Extensions |
| `attention.go`, `presence.go`, `resume.go`, `closeconfirm.go`, `pollround.go`, `actionclock.go` | a pane that needs the user, whether anyone is at a machine, what a lost session ran, the quit question, the poll for program news, a rate limit |
| `find.go`, `fields.go`, `paste.go`, `promptsheet.go`, `upload.go` | search, text fields, paste, the agent prompt sheet and file transfers to a host |
| `empty.go`, `dim.go`, `frost.go`, `glass*.go`, `animation.go` | empty states and the visual effects |
| `uifont_other.go`, `uifont_windows.go` | the interface's font away from macOS |
| `quarantine_darwin.go`, `quarantine_other.go` | marking a downloaded font or update as quarantined |
| `debug.go`, `applog.go` | the debug hooks and the app's log |
| `reximport.go` | Import from Rex |
| `locales` | the messages of the interface, `en.json` and `zh-Hans.json`, and `catalog_gen.go` (written by `go tool mygo generate`. Commit it with the catalogs) |
| `tools/remotectl` | acts as a remote client without the app: pubkey, regenerate, pair, probe over tailcat |
| `CHANGELOG.md` | release notes: a `## <version>` section per release |
| `internal/cli` | the `gorex` command line |
| `internal/config` | the Lua configuration reader ([REF-002](REF-002-lua.md)) |
| `internal/layout` | the saved shape of windows, tabs and panes |
| `internal/rex` | the session server and its client: PTYs, foreground processes, headless screens, the socket protocol |
| `internal/rexproto` | the socket protocol, generated from `protocol.json` |
| `internal/hosts` | `hosts.json`: the hosts' schema, load, and atomic save, shared by the app, the CLI and the server |
| `internal/rexapi` | `GoRex rexapi`, the gateway that serves Rex's API to Rex clients from the server the app runs |
| `internal/rexpeer`, `internal/reximp` | Rex's API as a client, and importing a Rex session into GoRex as spaces, tabs and panes that mirror its blocks |
| `internal/pairaddr` | pairing by address: its wire format and the client half |
| `internal/tailnode` | the built-in Tailscale node |
| `internal/theme` | theme manifests of the desktop chrome ([DSK-008](DSK-008-theme.md)) |
| `internal/iconpack` | community program icon packs ([MOD-012](MOD-012-program-icon-packs.md)) |
| `internal/push` | the host's sender of Live Activity updates through the push relay ([NET-003](NET-003-push-relay.md)) |
| `internal/testrex` | a Rex server for tests, over GoRex's own session server, so the client and the importer have one to talk to |
