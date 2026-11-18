# Projects: folders on each device, inside a space

Status: proposed. Issues #519 and #771. Replaces the design of v0.38.0
(#708), in which a project replaced a space. That design shipped in
v0.38.0 and v0.39.0; the parts it keeps are marked below.

## Problem

In v0.38.0 a project replaced a space: every project took one top-level
group, and the user made a group and then gave it a folder. In use this
splits what belongs together (#771):

- The user keeps few spaces, such as **Work** and **Life**. In Work they
  use several devices, and on each device several projects, and in each
  project several sessions: development, a server, its logs. Projects
  and their sessions are most of the use.
- A project is one folder on one device. It is made from the folder, by
  the device: not grouped by hand, and not a top-level group.
- A space that mixes projects, devices and windows by hand is a rare need.

The model is Codex's sidebar: each device shows its **Projects**, each
project its sessions, and `+` makes a project there.

## Decisions

Made by the user on 2026-11-18 (#771):

1. **Three levels.** Space (Work, Life) → device → project → session.
   The space is the group at the top of the sidebar, as before v0.38.0.
2. **A project lives on its device.** The device's server keeps it, as a
   Rex session with a folder. The desktop, Rex Go and the web client that
   connect to the device see the same projects, with no sync between
   clients.
3. **A project belongs to one space**, the one it was made in. The user
   can move it to another space. Work shows only Work's projects.
4. **A project is made from a folder.** The `+` of a device's Projects
   browses that device's folders and makes the project; the folder and
   the device do not change later (a project with another folder is
   another project). Its name can change.
5. **The tab bar shows the current project's sessions** when the tabs
   are on top. The title bar picks the device and the project.
6. **Projects of v0.38.0 and v0.39.0 become projects** on their home
   device, all in one space; their tabs on other devices become sessions
   with no project.
7. A session with no project stays possible, as a secondary entry.

Kept from v0.38.0: the folder browser and `dir.list`, the rule that a new
session of a project starts in its folder, the folder check, and the
Rex Go and web folder sheets.

## Model

### On each device

A device's layout (`internal/layout`) keeps its projects as
workspaces, as it kept spaces:

```go
type Workspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	// Space is the ID of the space the workspace shows in.
	Space string `json:"space,omitempty"`
	// Dir is the project's folder on this device, absolute. "" for the
	// sessions of Space that are in no project: one such workspace for
	// each space, at most.
	Dir string `json:"dir,omitempty"`
	Layout
}
```

- `Host` goes: a project's folder is always on the device whose layout
  keeps it. A layout of v0.38.0 is read once, as in Migration.
- A workspace with a `Dir` is a **project**. The one with no `Dir` holds
  the space's **sessions with no project** on this device.
- The Rex API shows each workspace as a Rex session, as today. Rex's own
  apps see a project as a session and its tabs as windows, unchanged.
- A session (a tab and its splits) moves between workspaces of one
  device and keeps its processes: Move to Project, and Move out of the
  project.

### Spaces

A space is an ID, a name, a color and an order, the same on every
client. Spaces change seldom.

- The list of spaces goes in the space sync that exists today (the
  desktop's `gorex.spaces_sync`, Rex Go's manifest in iCloud;
  `docs/ios-centralized-spaces.md`). The manifest keeps only the spaces'
  names, colors and order; it no longer keeps which windows a space has.
- Which space a project is in is on the project, on its device
  (`Workspace.Space`). So no client has to agree with another about
  membership.
- A project whose space no client knows shows in a space named
  "Unsorted" until the user moves it.
- The first space is **Default**. Its ID is `default` on every device, so
  a device that never heard of the space list still puts new projects
  somewhere all clients show.

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

### Sidebar

```
[Work ▾]                      ← space picker, as before v0.38.0
This Mac
  Projects                 +  ← + browses this device's folders
  ▾ gorex        ~/code/gorex
      dev
      logs
  ▸ notes        ~/notes
  Sessions with no project ▸
office-h800 ●
  Projects                 +
  ▾ trainer      ~/work/trainer
      train
      monitor
```

- A device shows when it is connected or keeps a project of the space.
  An offline device's projects show greyed, with their last sessions.
- A project row expands to its sessions. Its context menu: New Session,
  Rename, Color, Move to Space, Reveal Folder (this device only), Close
  Project.
- Projects of a device are in the order the user drags them; a new
  project goes first.
- **Sessions with no project** is collapsed by default and has New
  Session with No Project.
- Drag a session onto a project of its device to move it there, with its
  splits and processes.

### Tabs on top

- The title bar shows `device › project ▾`. Its menu lists the space's
  devices and their projects, New Project…, and Sessions with no project.
- The tab bar shows the sessions of the chosen project. ⌘T opens a
  session in the project's folder.

### New Project

- The device's `+` opens the folder sheet of v0.38.0, with the device
  already chosen. Create makes the project in the current space and
  opens its first session in the folder.
- New Project… in the palette and the File menu asks for the device
  first (this device by default).
- A folder that is already a project of this device and space: Create
  goes to that project instead of making a second one.

### New sessions

| Action | Device | Folder |
| --- | --- | --- |
| ⌘T, `+` of the tab bar, New Session of a project | the project's device | the project's folder |
| Split, floating pane | the pane's device | the pane's start directory, as today |
| New Session with No Project | the chosen device | the home directory |
| CLI `--dir`, Rex API `cwd` | as given | as given |

The folder check of v0.38.0 stays: a missing folder fails with a note
and offers Reveal or Close Project; an offline device fails with a note
that names it.

## Rex Go (iOS)

- The top strip shows the spaces, not the projects.
- Each connected device lists its projects of the space, then
  Sessions with no project. A project expands to its windows.
- The device's `+` opens the folder browser of v0.38.0 on that device
  and makes the project (`gorex.project.create`).
- New Window in a project opens on its device with `cwd` set to the
  folder.
- Move to Project on a window moves it, with `gorex.project.move`.

## Web client

The same as Rex Go, for the device the page is connected to and its
hosts.

## Rex API

- `gorex.project.create {name, dir, space}` → the new session. It checks
  the folder (as `dir.list` does) and makes the project's first window
  in it.
- `gorex.project.move {window_id, session_id}` moves a window between
  workspaces of this device. `session_id` "" moves it to the space's
  sessions with no project.
- `gorex.project.update {session_id, name?, color?, space?}`.
- Each session in a session list and a session view gains
  `project: {dir, space}`; the space's sessions with no project have
  `project: {space}` and no `dir`. Rex's apps ignore the field.
- `gorex.project.set_home` of v0.38.0 is removed: a project's folder does
  not change.
- The welcome lists `gorex.projects2`. A client that sees only
  `gorex.projects` talks to a device of v0.38.0 or v0.39.0 and shows its
  sessions as before, with no Projects section.

## Migration

On the first start of a version with this design:

- **This device's layout.** A workspace with a `Dir` and `Host` "" stays
  a project, in space `default`. A workspace with no `Dir` becomes the
  space's sessions with no project; its name becomes a space of the same
  name when it is not "Default" (the user's old spaces come back).
- **A workspace whose `Host` is another device** (a v0.38.0 project
  homed elsewhere): the desktop asks that device, when it connects, to
  make the project (`gorex.project.create`) and to move into it that
  device's sessions the workspace held. The workspace's sessions on this
  device become sessions with no project.
- Rex Go drops `home_machine` and `dir` from its manifest and reads
  projects from the devices.

The migration runs once and keeps the old layout as
`layout.json.v039`.

## Tests

- Model: a layout of v0.39.0 migrates (a project here, a project homed
  elsewhere, a space with no folder); a project round-trips with its
  space; a session moves between projects and keeps its process.
- Rex API: `gorex.project.create` checks the folder and opens a window
  in it; `gorex.project.move` keeps the window's panes; old clients see
  sessions as before.
- Desktop: the sidebar shows devices, projects and sessions with no
  project of the current space only; `+` makes a project on that device;
  ⌘T opens in the project's folder; UI tests with `ui.NewTester` and
  screenshots by the `GOREX_DEBUG` hook.
- Rex Go: projects under each device, create and move, on the simulator
  against a real gateway.

## Phases

Each phase is its own PR, run, reviewed and merged before the next.

1. **Model**: `Workspace.Space`, `Host` removed, the migration of a
   device's own layout, the Rex API methods and fields, and tests.
2. **Desktop sidebar and title bar**: devices, Projects, sessions with
   no project, move by drag and menu, the space picker.
3. **Desktop migration of projects homed on other devices.**
4. **Rex Go**: spaces in the strip, projects under each device, create
   and move.
5. **Web client.**
6. **Docs**: README, `docs/rex-api.md`, `docs/ios-centralized-spaces.md`.

## Open

- Whether a project can be pinned to show in more than one space. Not
  planned; Move to Space is the only change.
- Whether sessions with no project should show in every space.
