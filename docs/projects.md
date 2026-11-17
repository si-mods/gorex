# Projects: spaces bound to a folder

Status: approved. Issue #519. Replaces the earlier design in this file
(#570), which grouped tabs inside a space.

## Problem

A space groups tabs, on this machine and on remote hosts (`Spaces across
hosts` in `docs/rex-api.md`). Nothing ties a space to the work it holds:
a new tab starts on the host of the focused pane, in that pane's start
directory or the home directory. To open one more terminal of a project,
the user `cd`s there by hand, on the right host.

The user asked for projects as Codex has them: a project is bound to a
folder, and every new session made in the project starts in that folder.

## Decisions

Made by the user on 2026-11-17:

1. **A project replaces a space.** There is one grouping level, named
   Project in every client. There is no separate group of tabs inside a
   space.
2. **A project may hold terminals of several hosts**, as a space does
   today. Its folder is on one host, its *home host*.
3. **A split starts where the pane it splits started**, as today; it does
   not go back to the project's folder.
4. **The folder is chosen by browsing**, on remote hosts too. GoRex gets
   an operation that lists a directory.

## Model

A project is a space with a home: a host and a directory on it.

```go
type Workspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	// Host is the project's home host, as Node.Host: "" for this machine.
	Host string `json:"host,omitempty"`
	// Dir is the project's folder on Host, absolute. "" for a project
	// with no folder, as every space is today.
	Dir string `json:"dir,omitempty"`
	Layout
}
```

- The Go, Swift and wire names stay `Workspace`, `CentralSpace`,
  `gorex.spaces` and `/api/spaces`. Only what the user reads changes. A
  rename of the code would touch every package and the iOS sync format,
  for no gain to the user.
- A space of an older layout reads as a project with no folder, and works
  as a space does today. Nothing is migrated; the user can give it a
  folder later.
- An older GoRex that reads a newer layout drops `host` and `dir` and
  keeps the space.

### Across devices

The iOS manifest (`docs/ios-centralized-spaces.md`) carries the home, so
every device shows it and makes new sessions there:

```swift
struct CentralSpace {
    // ...
    var home_machine: String?   // rex.MachineID of the home host
    var dir: String?
}
```

`home_machine` is a machine ID, which is the same on every device; a
`hosts.json` ID is not. The desktop maps it to its own host entry
(`HostMachines`) when it applies the manifest (`gorex.spaces_sync`). A
desktop that does not know the machine keeps the folder but shows the
home host as offline.

## Where a new session starts

| Action | Host | Directory |
| --- | --- | --- |
| New Tab in a project with a folder | the home host | the folder |
| New Tab in a project with no folder | as today: the focused pane's host | as today |
| Split, floating pane | the pane's host, as today | the pane's start directory, as today (decision 3) |
| Rex Go or web: new window in a project | the home host | the folder |
| Any of these with a directory given by the caller (CLI `--dir`, Rex API `cwd`) | as given | as given |

- The rule is applied where the session is made, in the app
  (`newTabOn`) and in the gateway (`session.new_window`), so the Rex API,
  the CLI and Lua get it too.
- **Home host offline.** New Tab in the project fails with a note that
  names the host, and does not start a tab elsewhere.
- **Folder gone.** The server checks the directory before it starts the
  shell. A missing or unreadable folder fails the new tab with a note and
  a **Change Folder…** button; it does not fall back to the home
  directory. Today the server falls back silently (`sessionDir`,
  `internal/rex/session.go`). The check is for new sessions made in a
  project only: a session the server starts again from the layout after
  a restart keeps today's fallback to home.

## Listing a directory

A new server operation lists one directory, so a client can browse a
host's folders:

```go
// "dir.list": the directories in Path, on this machine.
type DirList struct {
	Op   string `json:"op"`             // "dir.list"
	Path string `json:"path,omitempty"` // "" for the home directory
	Hidden bool `json:"hidden,omitempty"`
}
type DirListResult struct {
	Path    string   `json:"path"`    // absolute, cleaned, symlinks resolved
	Parent  string   `json:"parent"`  // "" at a root
	Home    string   `json:"home"`
	Dirs    []string `json:"dirs"`    // names, sorted, at most 2000
	More    bool     `json:"more"`    // true when cut at 2000
	Code    string   `json:"code,omitempty"` // "not_found", "denied"
}
```

- Directories only, no files, no sizes: it is a folder picker, not a file
  manager.
- On Windows `Path` "" is the home directory, and a root lists the drives.
- The app calls it on a remote host through that host's client, as it
  sends other commands (`hostConn`).
- The Rex API serves it as `gorex.dir.list` (GoRex's own method; the
  welcome lists `gorex.dir_list`), for Rex Go and the web client. It is
  allowed to the same callers as `session.create`: a caller that can
  start a shell can already read the directory tree.

## Desktop

- **Wording.** Every "Space" a user reads becomes "Project": the sidebar,
  menus, the command palette, Settings, the CLI help and the docs.
  `locales/*.json` keys keep their names; their text changes.
- **New Project…** (sidebar `+`, palette, File menu) opens a sheet:
  1. **Host**: this machine and the hosts in `hosts.json`; offline hosts
     are shown but cannot be picked.
  2. **Folder**: a browser built on `dir.list`: the path as a breadcrumb,
     the folders below, Home, Up, and a field to type a path. For this
     machine, **Choose…** also opens the system folder picker.
  3. **Name**: the folder's name by default.
  4. **Create** makes the project and opens its first tab in the folder.
- **New Project with No Folder** stays, for a project that only groups
  terminals, as a space does.
- **Project settings** (the project's context menu): Rename, Color,
  **Change Folder…** (the same sheet), Remove Folder, Close Project.
- The sidebar's project header shows the folder under the name, with the
  home host's name when it is not this machine:
  `windflow · office-h800 · ~/code/windflow`.
- New UI follows `DESIGN.md`: the sheet uses `paintFrost`, Lucide icons
  only (`folder`, `folder-open`, `house`, `arrow-up`).

## Rex Go (iOS)

- "Space" becomes "Project" in every string (`Localizable.xcstrings`,
  Settings › Grouping).
- The manifest gains `home_machine` and `dir` (above); the strip shows the
  folder under the name.
- **New Project** gets the same three steps, the folder browser built on
  `gorex.dir.list` against the chosen host's gateway.
- **New Window** in a project with a folder opens on the home host's
  gateway with `cwd` set to the folder. With the home host unreachable it
  shows why and does not open elsewhere.

## Web client

The same wording, the same New Project steps and the same New Window rule,
through `gorex.dir.list` and `cwd`.

## Rex API

- Rex's own methods are unchanged: a project is a Rex session, as a
  space is.
- The welcome lists `gorex.projects`. Each session in a session list and
  in a session view gains `project: {home_machine, dir}` when the space has
  a folder. Rex's apps ignore the unknown field. Unconfirmed: that Rex's
  iOS app ignores it; check before release by connecting Rex's app to a
  gateway that sends it.
- `gorex.project.set_home {session_id, machine_id, dir}` sets or clears a
  project's home, for Rex Go and the web client.
- `session.new_window` without `cwd`, in a project whose home is this
  host, starts in the folder.

## Phases

Each phase is its own PR, verified by running it, reviewed and merged
before the next.

1. **`dir.list`**: the server operation, the host client call, the Rex API
   method `gorex.dir.list`, and tests (paths, home, Windows drives, limits,
   a missing and a denied directory).
2. **Model and rules**: `Workspace.Host` and `Dir`, the layout round trip,
   the new-session rule in the app and the gateway, the folder check, the
   Rex API fields and `gorex.project.set_home`, and `spaces_sync` with the
   manifest's new fields.
3. **Desktop**: the wording, the New Project sheet with the folder
   browser, project settings and the sidebar header; UI tests with
   `ui.NewTester` and screenshots by the `GOREX_DEBUG` hook.
4. **Rex Go**: the wording, the manifest fields, New Project with the
   folder browser, New Window in the folder; simulator checks on the Mac
   mini.
5. **Web client**.
6. **Docs**: README, `docs/rex-api.md`, `docs/ios-centralized-spaces.md`.

## Tests

- `dir.list`: home, a nested path, `..` and symlinks resolved, hidden
  folders only when asked, the 2000 limit, not found, denied, Windows
  drives.
- Layout: a layout with a home round-trips; an older layout reads as
  projects with no folder.
- New sessions: New Tab in a project with a folder starts on the home
  host in the folder; a split starts where its pane started; a caller's
  `cwd` wins; a missing folder fails with no shell started; an offline
  home host fails.
- Sync: a manifest home reaches a desktop and back; a desktop that does
  not know the home machine keeps the folder.
- UI: the New Project sheet browses a remote host's folders (a test
  server with a temporary directory tree), and Create opens a tab there.

## Decided later

Also by the user on 2026-11-17:

- A project whose home host is removed from `hosts.json` keeps its folder
  and shows the host as unknown; adding the host again restores it.
- The folder browser has no **New Folder**: `dir.list` only reads.
