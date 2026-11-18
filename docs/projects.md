# Projects: folders on each host, inside a space

Status: implemented in v0.41.0 (#776, #779, #778, #775). Issues #519 and
#771. Replaces the design of v0.38.0
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
| 1 | Space (空间) | Work, Life: the picker atop the sidebar | a name on each project | none (GoRex's own) |
| 2 | Host (主机) | this machine, office-h800 | a `hosts.json` entry | a gateway |
| 3 | Project (项目) | a folder on the host | a `Workspace` with a `Dir` | a session |
| 4 | Tab (标签页) | dev, logs | `Tab` | a window |
| 5 | Pane (分屏) | one shell of a tab's splits | `Pane` | a block |

- "Session" names no level in the UI or the docs. It stays only where
  Rex's own API says it (`session.*`, `session_id`), and in the docs
  only next to this table's mapping.
- Rex Go's "Window" (#760) becomes "Tab", and "Block" in any string
  becomes "Pane".
- Names: a space's is the name on its projects; a host's is its alias
  in `hosts.json` (`internal/hosts`, `Label`) or, for this machine, the
  name set in Settings; a project's is `Workspace.Name`, its folder's
  name by default; a tab's is its title as today. Each is renamed in
  place (the space picker's Edit Spaces…, a host's menu, a project's or
  a tab's Rename…), and a rename touches nothing else.
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
6. **Projects of v0.38.0 and v0.39.0 become projects** of this machine,
   in one space (but see 12).
7. A tab with no project stays possible, as a secondary entry.

Made by the user on 2026-11-18, after a review of this design:

8. **No space list to sync.** A space is a name on its projects. The
   `gorex.spaces_sync` manifest and its iCloud copy go.
9. **No `ShareLayout` flag.** A host's layout is where its projects and
   tabs live, so every client the host lets in reads it.
10. **One project per folder on a host**, whatever its space.
11. **One "Tabs with no project" for each host**, shown in every space.
12. **Projects homed on another host in v0.38.0** are made on that host
    when it connects, with the tabs they held there, and start no new
    shell (chosen by the user after the review of #773; see Migration).
13. **Clients cache each host's last projects**, to show an offline host;
    the cache is only shown, never changed.
14. **Projects are in order of last use**, as Codex lists them.

From the review of #773 by the author of #771:

15. **Every level has a name the user can change.** A space, a host
    (its alias), a project and a tab can each be renamed. A project's
    name is its folder's name when it is made; Rename changes only the
    name, never the folder or the tabs.
16. **A project row has its own `+`**, which opens a tab in the
    project's folder, as ⌘T does while the project is chosen.
17. **The migration keeps what runs.** No shell stops or starts; tabs
    keep their splits and their project as far as one host can hold
    them.

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
	// Space is the name of the space the project shows in; "" is
	// "Default". The workspace of tabs with no project has none.
	Space string `json:"space,omitempty"`
	// Dir is the project's folder on this host, absolute and unique
	// among the host's projects. "" for the host's one workspace of tabs
	// with no project.
	Dir string `json:"dir,omitempty"`
	// Used is when a tab of the project last took input or was made.
	Used time.Time `json:"used,omitzero"`
	Layout
}
```

- The `Host` field goes: a project's folder is always on the host whose
  layout keeps it. A layout of v0.38.0 is read once, as in Migration.
- A workspace with a `Dir` is a **project**. The one with no `Dir` holds
  the host's **tabs with no project**, which every space shows.
- The server refuses a second project of a folder; the caller gets the
  first one's ID (`code: "exists"`).
- The Rex API shows each workspace as a Rex session, a tab as a window
  and a pane as a block, as today. Rex's own apps see a project as a
  session, unchanged.
- A tab moves between workspaces of one host with its panes and their
  processes: Move to Project, and Move out of Project.

### Spaces

A space is only a name on projects. No list of spaces is kept or synced.

- A client's space picker lists the names its hosts' projects carry, and
  "Default" (a project with no `Space`). It keeps each space's color and
  order itself, in its own settings.
- **New Space…** adds a name to the picker on this client; the space
  stays there, empty, until a project moves into it.
- **Rename** a space: the client asks each connected host to rename it
  (`gorex.space.rename`). A host offline then keeps the old name; the
  client keeps the rename and sends it when the host connects.
- **Delete** a space (Rex Go's Edit Spaces…, after the user confirms):
  its projects move to Default and no tab closes. The client asks each
  connected host (`gorex.space.delete {name}` → `{projects}`); a host of
  v0.41.0, which answers `method_not_found`, is sent
  `gorex.space.rename {from: name, to: ""}` instead. A host offline
  takes it as it takes a rename, when it connects. The client forgets
  the space's color and place, and shows Default if it showed the space.
- A space no project carries any more drops from the picker, unless it
  was made on this client and is still empty.
- The `gorex.spaces_sync` manifest and its iCloud copy
  (`docs/ios-centralized-spaces.md`) are removed.

### The desktop window

The desktop's window shows the hosts' layouts; it no longer keeps a
layout of other hosts' panes.

- Each project's tabs, their splits, sizes, floating panes and browser
  panes are in the layout of the project's host, as Rex's windows and
  blocks are. Every client of the host sees the same tabs.
- This machine's layout keeps this machine's projects. It keeps no pane
  of another host: `Node.Host`, `Kills` and `Teleports` of other hosts'
  panes go with the migration.
- What stays on each client, in its own settings: the space shown, the
  project and tab shown in each window, the window's size, the sidebar's
  width, which projects are expanded, the spaces' colors and order.
- `ShareLayout` goes: a client the host lets in (the Remote Access list,
  a paired device) reads its layout and follows its changes, as it
  already attaches to its sessions.
- **Offline hosts.** Each client keeps a copy of each host's projects
  and their tabs' names as it last saw them, and shows them greyed while
  the host is away. Nothing in the copy can be changed or opened; it is
  replaced as the host connects.

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

The sidebar on the left, space Work, three hosts:

```
┌───────────────────────────────┬──────────────────────────────────────────┐
│ ● ● ●   [■ Work ▾]            │ gorex › dev                          ⋯   │
├───────────────────────────────┼──────────────────────────────────────────┤
│ THIS MAC  ●                 + │ ~/code/gorex $ go test ./...             │
│ ● gorex                   3 + │ ok   gorex   12.3s                       │
│ ┃ dev                         │ ◀ dev is the tab shown here              │
│ ┃ logs                        │                                          │
│ ┃ review                      │                                          │
│ ● notes                     1 │                                          │
│   Tabs with no project      2 │                                          │
│                               │                                          │
│ OFFICE-H800  ●              + │                                          │
│ ● trainer                   2 │                                          │
│                               │                                          │
│ OLD-LAPTOP  ○ offline         │                                          │
│ ● site (grey)               1 │                                          │
├───────────────────────────────┤                                          │
│ ⚙   +  New Project…           │                                          │
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
| `●` `○` | a host's state dot, a project's color dot, drawn, not icons |
| `┃` | the guide line from a project to its tabs, drawn |

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
  or keeps a project of the space.
- A host is a section header, not indented, small and in capitals, with
  its state dot and the `+` that makes a project there. Its context menu
  has Rename… (its alias) and New Tab with No Project. An offline host's
  header says "offline", and its projects show greyed, with their last
  tabs.
- A project row is flush with the header: the project's color dot, its
  name, and its tab count on the right. Its folder shows in its tooltip
  and, with the tabs on top, in the title bar (`host › project`). A click
  shows the project and expands it; on the project shown, or on the dot
  of any, it expands or collapses that project's tabs alone, which come
  one step in, joined to it by a thin guide line. Each project keeps its
  own state, per client (`sidebarOpen`); choosing another project, in
  any way, closes none. Its `+` (shown on the row under the pointer and on
  the chosen project's) opens a tab in the folder; ⌘T does the same while
  the project is chosen. Its context menu: New Tab, Rename, Color, Move to
  Space, Reveal Folder (this host only), Close Project.
- Projects of a host are in order of last use (`Used`), the latest
  first; those not used for 30 days go under a collapsed **Older** row,
  at the projects' level.
- **Tabs with no project** is a row at the projects' level, with no dot,
  last among the host's; it shows in every space, collapsed until chosen,
  and expands the same way.
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

The `+` of a host's header opens the folder sheet of v0.38.0 with the
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

- A folder that is already a project of this host: Create shows that
  project instead of making a second one; when it is in another space,
  it asks whether to move it to this space.

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
- Each host lists its projects of the space, in order of last use, then
  Tabs with no project. A project expands to its tabs and a New Tab row.
- An offline host shows its projects greyed, from the copy the app kept
  when it last saw them.
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

- `gorex.project.create {name, dir, space}` → `{project_id, tab_id}`.
  The `dir` goes through `rex.VolumePath` first, so a device or pipe
  path is refused (`docs/lessons.md`, "device paths"); then the folder
  is checked as `dir.list` does, and the project's first tab starts in
  it. A folder that is a project already answers `code: "exists"` with
  that project's ID.
- `gorex.project.move {tab_id, project_id}` moves a tab, with its panes,
  between workspaces of this host. `project_id` "" moves it to the
  space's tabs with no project.
- `gorex.project.adopt {project?: {name, dir, space}, tabs: [{title,
  split}]}` makes the project when `project` is given and it does not
  exist yet (its folder checked as above), and arranges panes that run
  already into the tabs given, each `split` a tree of `pane_id`s as
  `layout.Node`. It starts no shell and opens no tab of its own. A pane
  it does not know is skipped; adopting again what is adopted changes
  nothing. The migration uses it.
- `gorex.project.update {project_id, name?, color?, space?}`.
- `gorex.space.rename {from, to}` renames the space on every project of
  this host.
- Each session in a session list and a session view gains
  `project: {dir, space, used}`; the host's tabs with no project have
  `project: {}`. Rex's apps ignore the field.
- `gorex.project.set_home` of v0.38.0 is removed: a project's folder does
  not change.
- The welcome lists `gorex.projects` with `version: 2`. A client that
  sees no version talks to a host of v0.38.0 to v0.40.x and shows its
  tabs as before, with no Projects section.

## Migration

No shell stops or starts. A tab keeps its splits, and its project, as
far as one host can hold them: a tab now lives on one host, so a tab of
v0.39.0 whose panes ran on several hosts becomes one tab on each, each
with that host's part of the split.

**On each server, as it loads its layout** (also after
`gorex server upgrade`):

- A workspace with a `Dir` and `Host` "" (a project of this machine)
  stays a project, in space "Default". Two of one folder become one,
  with the tabs of both.
- A workspace with no `Dir` (an old space): its tabs of this machine go
  to the tabs with no project, and its name becomes a space name the
  client offers, so the user's old spaces come back in the picker.
- For every tab with panes of other hosts, the server keeps an
  **adoption** in the layout (`Store.Adopt`): the host's machine, the
  project when the workspace was homed there (name, folder, space), and
  for each tab the part of its split that ran on that host, by the
  panes' session IDs. It drops those panes from its own tab; a tab left
  with no pane goes.
- The old layout is kept as `layout.json.v039`.

**On the desktop, as each host connects**, the app sends that host its
adoptions (`gorex.project.adopt`) and the server forgets each once the
host answers:

- A v0.38.0 project homed there is made on that host, and its tabs of
  that host go into it with their splits.
- The other tabs' panes of that host become tabs with no project there,
  with their splits.
- A host offline keeps its adoptions until it connects, however long;
  meanwhile its panes show from the offline copy, if any, and the
  project shows greyed under the host with a note "Waiting for <host>".
- A pane whose session ended on its host meanwhile is skipped; an
  adoption with nothing left is dropped.
- A host of v0.38.0 to v0.40.x (no `gorex.projects` version 2) keeps the
  adoption until it is upgraded.

Rex Go drops its manifest and reads projects from the hosts.

### Cases

| Before (v0.39.0, this desktop) | After |
| --- | --- |
| Project gorex, folder here, tab dev split in 2 local panes | project gorex here, tab dev, same split |
| Project trainer homed on office-h800, tab train: 1 pane there | project trainer made on office-h800, tab train in it |
| Space Work, tab api split: local pane left, office-h800 pane right | tab api here (left pane), tab api on office-h800 (right pane), both with no project |
| Project trainer homed on office-h800, tab mon: 2 panes there split, 1 local | tab mon in trainer on office-h800 with its 2-pane split; tab mon here with no project |
| office-h800 offline at the upgrade | its adoption waits; done when it connects |

## Tests

- Model: every case of the Migration table, each checking that the same
  shell processes run before and after; an adoption applied twice
  changes nothing; a project round-trips with its space; a tab moves
  between projects and keeps its panes' processes.
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

1. **Server model**: `Workspace.Space`, `Used`, one project per folder,
   the `Host` field removed, the server's migration and its adoptions,
   `gorex.project.adopt`, `ShareLayout` removed, the Rex API methods and
   fields, and tests of every case in Migration.
2. **Desktop window from the hosts' layouts**, sending adoptions as
   hosts connect, then the sidebar and the title bar: hosts, Projects, tabs with no project, move by drag and
   menu, the space picker, the offline copy. The new UI goes in files of
   its own (`projects_sidebar.go` and the like), not in `state.go`
   (`docs/lessons.md`).
3. **Rex Go**: the space picker, projects under each host, create and
   move, the offline copy, the manifest removed; "Window" becomes "Tab".
4. **Web client.**
5. **Docs**: README, `docs/rex-api.md`, `docs/cli.md` (Share Layout),
   `docs/ios-centralized-spaces.md` marked replaced.

## Decided in phase 1

What the design left open, and phase 1 (the server model) decided:

- **Welcome.** `features` lists `gorex.projects`, and the welcome's new
  `feature_versions` object gives its version:
  `"feature_versions": {"gorex.projects": 2}`.
- **`exists`.** `gorex.project.create` of a folder that is a project
  already fails with an error of code `exists` (status 409), whose
  `project_id` names that project.
- **Space names offered.** The layout keeps the old spaces' names in
  `Store.Spaces`, until a project carries one; `gorex.space.list` answers
  the names the host's projects carry, then those.
- **Adoptions.** Each is one host and one project (or none): `ID`,
  `Host` (its `hosts.json` ID), `Machine` when known, `Project`, `Tabs`.
  The app takes one with the local command `adopt.taken`. An adoption
  whose project folder is not on the host puts its tabs in the tabs with
  no project. An adopted tab counts as adopted when one tab of the
  project holds all its panes that run; then adopting again changes
  nothing.
- **One folder, one path.** A project's folder is kept as `dir.list`
  names it (`rex.ProjectFolder`): its symlinks resolved, on Windows in the
  case it has on disk, so `/tmp/x` and `/private/tmp/x` are one project.
  `project.create`, `project.adopt` and `space.home` (and the
  `set_home` shim) resolve it before the one-project-per-folder check.
- **Tabs with no project.** The migration's workspace of them takes the
  ID of the first old space, so Rex Go 1.1 keeps that session, and the
  name "Tabs with no project".
- **Floats of another host** join that host's part of the split.
- **`Used`** follows input at most once a minute per pane; a new tab sets
  it at once.
- **Share Layout.** `remote.shareLayoutSet` of an older app is accepted
  and changes nothing; `remote.status` says `shareLayout: true`.
- **Older clients.** A project's `project` also carries `home_machine`,
  this machine, so Rex Go 1.1 and the web client of v0.39.0 find its
  host. `gorex.project.set_home` stays as a deprecated shim onto
  `space.home`, a folder of this machine only, for their New Project
  (`session.create`, then `set_home`). `gorex.spaces_apply` stays, ignores
  `home_machine` and `dir`, and makes no workspace: a space no session is
  sends its windows to the tabs with no project, but a project's.
- **One workspace with no folder.** The migration makes one; tabs the
  server places itself (a remote client's session, a tab with no space)
  go to it. `space.create`, and Rex's `session.create`, still make a
  workspace with no folder, because the desktop before phase 2 and the
  clients before phases 3 and 4 make a project as such a workspace and
  then give it a folder (`space.home`, `set_home`). Phases 2 to 4 stop
  sending them, and then the server refuses a second one.
- **Until phase 2** the desktop still sends `space.home`: the server keeps
  it for a folder of this machine only (another host's fails with
  `remote_home`, a second project of a folder with `exists`). Between
  phase 1 and phase 2, a migrated desktop no longer shows other hosts'
  panes of its old tabs: they wait in the adoptions.
- **Phase 2 removes** `space.home`, the app's `Workspace.Host` and the
  waits for a project's home host (`projectHost`, `homeArg`,
  `projectOpen`), `layout.MachineHost`, and sends the adoptions. **Phases
  3 and 4 remove** `gorex.spaces_apply`, `featureSpacesSync` and
  `gorex.project.set_home` once Rex Go and the web client no longer send
  them; then `space.create` and `session.create` make no second workspace
  with no folder.

## Decided in phase 2

What the design left open, and phase 2 (the desktop) decided:

- **A client arranges a host's tabs.** A remote client the host lets in
  may send `tab.set` and `layout.apply` (`rex.remoteCommand`), of the
  host's own panes only: a leaf that names another host, or a place the
  client could not see, is refused, and so are `windows.set`,
  `kills.taken`, `show.taken` and `adopt.taken`, which stay the host's own
  window's. The layout a peer reads keeps browser panes; a note's text
  stays on its host, its place shows as an unavailable pane, and the app
  arranges no tab that has one (`lockedTabs`).
- **App and host forms.** In the app a host's pane names its host
  (`Node.Host`), as before; the host's own layout names none. The app
  turns one into the other as it takes and sends (`hostForm`,
  `appForm`), and sends a host the changes of its workspaces through an
  outbox of the host's, as it does this machine's.
- **The tab a window shows is the window's.** The app sends a host no
  change of a workspace's active tab, and a host's active tab moves no
  window. Which project each window shows is kept in `settings.json`
  (`shownProjects`, by the window's place); this machine's
  `Store.Windows` names only its own workspaces. A window whose project
  is a host's shows it again once the host's layout comes.
- **Offline copy.** `hosts-layout.json` beside `hosts.json` keeps each
  host's layout as the app last took it, written as it takes one.
- **Hosts of v0.38.0 to v0.40.x** show each session as a tab with no
  project there; the app arranges nothing on them, and their adoptions
  wait ("Waiting for" the host) until they are upgraded.
- **A project with no tab stays**, as its host keeps it; closing its last
  tab shows the window another workspace.
- **Space renames for a host away** wait in `settings.json`
  (`spaceRenames`) and go as it connects.
- **Teleport** (the app's) puts the session that arrives in the host's
  tabs with no project (`project.adopt`), and the window shows it.
- **Kills.** The app's own kills of hosts' sessions stay in `hosts.json`
  (`Kills`), as before. The kills of other hosts a layout of v0.38.0 to
  v0.40.x held for a host away, made by another client of this machine,
  are dropped by the migration and not sent: such a session runs on until
  it is closed there.
- **`space.home`** stays on the server, for the deprecated
  `gorex.project.set_home` of phase 1; the desktop no longer sends it, and
  `Workspace.Host`, the home-host waits and `layout.MachineHost` are gone.

## Open

- A project in more than one space: not planned; Move to Space is the
  only change.
