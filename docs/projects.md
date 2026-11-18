# Projects: folders on each host, inside a space

Status: proposed. Issues #519 and #771. Replaces the design of v0.38.0
(#708), in which a project replaced a space. That design shipped in
v0.38.0 and v0.39.0; the parts it keeps are marked below.

## Problem

In v0.38.0 a project replaced a space: every project took one top-level
group, and the user made a group and then gave it a folder. In use this
splits what belongs together (#771):

- The user keeps few spaces, such as **Work** and **Life**. In Work they
  use several hosts, on each host several projects, and in each project
  several tabs: development, a server, its logs. Projects and their tabs
  are most of the use.
- A project is one folder on one host. It is made from the folder, on
  the host: not grouped by hand, and not a top-level group.
- A space that mixes projects, hosts and tabs by hand is a rare need.

The model is Codex's sidebar: each host shows its **Projects**, each
project its tabs, and `+` makes a project there.

## Levels and words

Five levels, with one word each in every client, in the docs and in
GoRex's own protocol (`gorex.*` methods, the session server):

| Level | Word (zh-Hans) | What it is | GoRex code | Rex API |
| --- | --- | --- | --- | --- |
| 1 | Space (空间) | Work, Life: the picker atop the sidebar | the space list | none (GoRex's own) |
| 2 | Host (主机) | this machine, office-h800 | a `hosts.json` entry | a gateway |
| 3 | Project (项目) | a folder on the host | a `Workspace` with a `Dir` | a session |
| 4 | Tab (标签页) | dev, logs | `Tab` | a window |
| 5 | Pane (分屏) | one shell of a tab's splits | `Pane` | a block |

- "Session" names no level in the UI or the docs. It stays only where
  Rex's own API says it (`session.*`, `session_id`), and in the docs
  only next to this table's mapping.
- Rex Go's "Window" (#760) becomes "Tab", and "Block" in any string
  becomes "Pane".
- GoRex's own methods and fields use `project`, `tab` and `pane`:
  `project_id`, `tab_id`, `pane_id`. Their values are the Rex IDs of the
  same things (`session:…`, `window:…`, `block:…`), so a client can pass
  an ID from a Rex method to a GoRex method unchanged.

## Decisions

Made by the user on 2026-11-18 (#771):

1. **Space → Host → Project → Tab → Pane.** The space is the group at the
   top of the sidebar, as before v0.38.0.
2. **A project lives on its host.** The host's server keeps it, as a Rex
   session with a folder. The desktop, Rex Go and the web client that
   connect to the host see the same projects, with no sync between
   clients.
3. **A project belongs to one space**, the one it was made in. The user
   can move it to another space. Work shows only Work's projects.
4. **A project is made from a folder.** The `+` of a host's Projects
   browses that host's folders and makes the project; its folder and
   host do not change later (another folder is another project). Its
   name can change.
5. **The tab bar shows the current project's tabs** when the tabs are on
   top. The title bar picks the host and the project.
6. **Projects of v0.38.0 and v0.39.0 become projects** on their home
   host, all in one space; their tabs on other hosts become tabs with no
   project.
7. A tab with no project stays possible, as a secondary entry.

Kept from v0.38.0: the folder browser and `dir.list`, the rule that a new
tab of a project starts in its folder, the folder check, and the Rex Go
and web folder sheets.

## Model

### On each host

A host's layout (`internal/layout`) keeps its projects as workspaces, as
it kept spaces:

```go
type Workspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	// Space is the ID of the space the workspace shows in.
	Space string `json:"space,omitempty"`
	// Dir is the project's folder on this host, absolute. "" for the
	// tabs of Space that are in no project: one such workspace for each
	// space, at most.
	Dir string `json:"dir,omitempty"`
	Layout
}
```

- The `Host` field goes: a project's folder is always on the host whose
  layout keeps it. A layout of v0.38.0 is read once, as in Migration.
- A workspace with a `Dir` is a **project**. The one with no `Dir` holds
  the space's **tabs with no project** on this host.
- The Rex API shows each workspace as a Rex session, a tab as a window
  and a pane as a block, as today. Rex's own apps see a project as a
  session, unchanged.
- A tab moves between workspaces of one host with its panes and their
  processes: Move to Project, and Move out of Project.

### Spaces

A space is an ID, a name, a color and an order, the same on every
client. Spaces change seldom.

- The list of spaces goes in the space sync that exists today (the
  desktop's `gorex.spaces_sync`, Rex Go's manifest in iCloud;
  `docs/ios-centralized-spaces.md`). The manifest keeps only the spaces'
  names, colors and order; it no longer keeps which tabs a space has.
- Which space a project is in is on the project, on its host
  (`Workspace.Space`). So no client has to agree with another about
  membership.
- A project whose space no client knows shows in a space named
  "Unsorted" until the user moves it.
- The first space is **Default**. Its ID is `default` on every host, so
  a host that never heard of the space list still puts new projects
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

The sidebar on the left, space Work, two hosts:

```
┌───────────────────────────────┬──────────────────────────────────────────┐
│ ● ● ●   [■ Work ▾]            │ gorex › dev                          ⋯   │
├───────────────────────────────┼──────────────────────────────────────────┤
│ [H] This Mac                  │ ~/code/gorex $ go test ./...             │
│   PROJECTS                  + │ ok   gorex   12.3s                       │
│   ▾ [F] gorex   ~/code/gorex  │                                          │
│       ▸ dev                   │ ◀ dev is the tab shown here              │
│         logs                  │                                          │
│         review                │                                          │
│   ▸ [F] notes   ~/notes       │                                          │
│   ▸ Tabs with no project (2)  │                                          │
│                               │                                          │
│ [H] office-h800  ●            │                                          │
│   PROJECTS                  + │                                          │
│   ▾ [F] trainer ~/work/trainer│                                          │
│         train                 │                                          │
│         monitor               │                                          │
│                               │                                          │
│ [H] old-laptop   ○ offline    │                                          │
│   PROJECTS                    │                                          │
│   ▸ [F] site    ~/site  (grey)│                                          │
├───────────────────────────────┤                                          │
│ +  New Project…          ⚙    │                                          │
└───────────────────────────────┴──────────────────────────────────────────┘
```

The sketches use text marks for icons. In the UI each is a lucide SVG
from `assets/icons/` (`DESIGN.md`), never the character:

| Mark | Icon |
| --- | --- |
| `[H]` | `monitor` |
| `[F]` | `folder` |
| `[S]` | `search` |
| `+` | `plus` |
| `▾` `▸` `⌄` `›` | `chevron-down`, `chevron-right` |
| `✓` | `check` |
| `⌂` `↑` | `house`, `arrow-up` |
| `⚙` | `settings` |
| `☰` | `menu` |
| `●` `○` | a host's state dot, drawn, not an icon |

`◀` and `←` mark notes on a sketch; they are not drawn.

The space picker:

```
┌──────────────────────┐
│ ✓ ■ Work             │
│   ■ Life             │
│ ──────────────────── │
│   New Space…         │
│   Edit Spaces…       │
└──────────────────────┘
```

A project's context menu:

```
┌──────────────────────────┐
│ New Tab              ⌘T  │
│ ──────────────────────── │
│ Rename…                  │
│ Color                  ▸ │
│ Move to Space          ▸ │
│ Reveal Folder            │   ← this machine only
│ ──────────────────────── │
│ Close Project…           │
└──────────────────────────┘
```

- The hosts are the ones the user added (`hosts.json`, read through
  `internal/hosts`); a machine the tailnet finds shows only in the Add
  Remote Host sheet (#761). A host shows in a space when it is connected
  or keeps a project of the space. An offline host's projects show
  greyed, with their last tabs.
- A project row expands to its tabs. Its context menu: New Tab, Rename,
  Color, Move to Space, Reveal Folder (this host only), Close Project.
- Projects of a host are in the order the user drags them; a new project
  goes first.
- **Tabs with no project** is collapsed by default and has New Tab with
  No Project.
- Drag a tab onto a project of its host to move it there, with its panes
  and their processes.

### Tabs on top

```
┌───────────────────────────────────────────────────────────────────────┐
│ ● ● ●  [■ Work ▾]  This Mac › gorex ▾ │ dev │ logs │ review │  +      │
├───────────────────────────────────────────────────────────────────────┤
│ ~/code/gorex $                                                        │
└───────────────────────────────────────────────────────────────────────┘
```

`This Mac › gorex ▾` opens:

```
┌───────────────────────────────────────┐
│ This Mac                              │
│   ✓ gorex        ~/code/gorex     3   │
│     notes        ~/notes          1   │
│     Tabs with no project          2   │
│ office-h800  ●                        │
│     trainer      ~/work/trainer   2   │
│ old-laptop   ○                        │
│     site         ~/site           0   │
│ ───────────────────────────────────── │
│ New Project…                    ⇧⌘N   │
└───────────────────────────────────────┘
```

- The title bar shows `host › project ▾`. Its menu lists the space's
  hosts and their projects, with tab counts, Tabs with no project, and
  New Project….
- The tab bar shows the tabs of the chosen project. ⌘T opens a tab in
  the project's folder.

### New Project

The `+ ` of a host's PROJECTS opens the folder sheet of v0.38.0 with the
host chosen:

```
┌───────────────────── New Project on office-h800 ─────────────────────┐
│  ⌂  ↑   / › home › alex › work                         [ path…    ]  │
│ ┌──────────────────────────────────────────────────────────────────┐ │
│ │ [F] Use this folder: work                                        │ │
│ │ [F] trainer                                                    › │ │
│ │ [F] datasets                                                   › │ │
│ │ [F] notebooks                                                  › │ │
│ └──────────────────────────────────────────────────────────────────┘ │
│  Name   [ work                    ]                                  │
│  Project folder: /home/alex/work                                     │
│                                       [ Cancel ]   [ Create ]        │
└──────────────────────────────────────────────────────────────────────┘
```

- Create makes the project in the current space and opens its first tab
  in the folder.
- New Project… in the palette and the File menu shows a host picker
  first (this machine by default):

```
┌──────── New Project ────────┐
│ On which host?              │
│   [H] This Mac         ✓    │
│   [H] office-h800      ●    │
│   [H] old-laptop  (offline) │
│              [ Continue ]   │
└─────────────────────────────┘
```

- A folder that is already a project of this host and space: Create goes
  to that project instead of making a second one.

### New tabs and panes

| Action | Host | Folder |
| --- | --- | --- |
| ⌘T, `+` of the tab bar, New Tab of a project | the project's host | the project's folder |
| Split, floating pane | the pane's host | the pane's start directory, as today |
| New Tab with No Project | the chosen host | the home directory |
| CLI `--dir`, Rex API `cwd` | as given | as given |

The folder check of v0.38.0 stays: a missing folder fails with a note
and offers Reveal or Close Project; an offline host fails with a note
that names it.

## Rex Go (iOS)

```
┌─────────────────────────────────┐
│ ☰        Work ▾             [S] │   ← space picker
├─────────────────────────────────┤
│ [H] This Mac                  + │   ← + makes a project here
│   [F] gorex  ~/code/gorex   3 ⌄ │
│       dev                    ›  │
│       logs                   ›  │
│       review                 ›  │
│       + New Tab                 │
│   [F] notes  ~/notes        1 › │
│   Tabs with no project     2 ›  │
│                                 │
│ [H] office-h800  ●            + │
│   [F] trainer ~/work/trainer 2 ›│
│                                 │
│ [H] old-laptop   ○ offline      │
│   [F] site   ~/site         0 › │
└─────────────────────────────────┘
```

- The top shows the space picker, not a strip of projects.
- Each host lists its projects of the space, then Tabs with no project.
  A project expands to its tabs and a New Tab row.
- The host's `+ ` opens the folder browser of v0.38.0 on that host and
  makes the project (`gorex.project.create`).
- New Tab in a project opens on its host with `cwd` set to the folder.
- A tab's menu has Move to Project, with `gorex.project.move`.

## Web client

The same as Rex Go, for the host the page is connected to and its
hosts.

## Rex API

Rex's own methods keep Rex's words (`session`, `window`, `block`).
GoRex's own methods use the words above:

- `gorex.project.create {name, dir, space_id}` → `{project_id, tab_id}`.
  The `dir` goes through `rex.VolumePath` first, so a device or pipe
  path is refused (`docs/lessons.md`, "device paths"); then the folder
  is checked as `dir.list` does, and the project's first tab starts in
  it.
- `gorex.project.move {tab_id, project_id}` moves a tab, with its panes,
  between workspaces of this host. `project_id` "" moves it to the
  space's tabs with no project.
- `gorex.project.update {project_id, name?, color?, space_id?}`.
- Each session in a session list and a session view gains
  `project: {dir, space_id}`; the space's tabs with no project have
  `project: {space_id}` and no `dir`. Rex's apps ignore the field.
- `gorex.project.set_home` of v0.38.0 is removed: a project's folder does
  not change.
- The welcome lists `gorex.projects2`. A client that sees only
  `gorex.projects` talks to a host of v0.38.0 or v0.39.0 and shows its
  tabs as before, with no Projects section.

## Migration

On the first start of a version with this design:

- **This host's layout.** A workspace with a `Dir` and `Host` "" stays a
  project, in space `default`. A workspace with no `Dir` becomes the
  space's tabs with no project; its name becomes a space of the same
  name when it is not "Default" (the user's old spaces come back).
- **A workspace whose `Host` is another host** (a v0.38.0 project homed
  elsewhere): the desktop asks that host, when it connects, to make the
  project (`gorex.project.create`) and to move into it that host's tabs
  the workspace held. The workspace's tabs on this machine become tabs
  with no project.
- Rex Go drops `home_machine` and `dir` from its manifest and reads
  projects from the hosts.

The server runs the migration once, as it loads the layout, and keeps
the old layout as `layout.json.v039`. It runs the same way after
`gorex server upgrade`, which keeps the sessions' shells: the migration
moves tabs between workspaces and stops no process.

## Tests

- Model: a layout of v0.39.0 migrates (a project here, a project homed
  elsewhere, a space with no folder); a project round-trips with its
  space; a tab moves between projects and keeps its panes' processes.
- Rex API: `gorex.project.create` checks the folder and opens a tab in
  it; `gorex.project.move` keeps the tab's panes; old clients see their
  sessions as before.
- Desktop: the sidebar shows hosts, projects and tabs with no project of
  the current space only; `+` makes a project on that host; ⌘T opens in
  the project's folder; UI tests with `ui.NewTester` and screenshots by
  the `GOREX_DEBUG` hook.
- Rex Go: projects under each host, create and move, on the simulator
  against a real gateway.

## Phases

Each phase is its own PR, run, reviewed and merged before the next.

1. **Model**: `Workspace.Space`, the `Host` field removed, the migration
   of a host's own layout, the Rex API methods and fields, and tests.
2. **Desktop sidebar and title bar**: hosts, Projects, tabs with no
   project, move by drag and menu, the space picker. The new UI goes in
   files of its own (`projects_sidebar.go` and the like), not in
   `state.go` (`docs/lessons.md`).
3. **Desktop migration of projects homed on other hosts.**
4. **Rex Go**: spaces in the strip, projects under each host, create and
   move; "Window" becomes "Tab".
5. **Web client.**
6. **Docs**: README, `docs/rex-api.md`, `docs/ios-centralized-spaces.md`.

## Open

- Whether a project can show in more than one space. Not planned; Move
  to Space is the only change.
- Whether tabs with no project should show in every space.
