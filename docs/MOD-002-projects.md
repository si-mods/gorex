---
name: MOD-002
description: "Projects are folders on each host, inside a space, and hosts own them. Use when you change spaces, projects or where layouts live."
metadata:
  created: "2026-11-17"
  updated: "2026-11-24"
---

# MOD-002: Projects: folders on each host, inside a space

Status: built (#775, #776, #778, #779). Replaces the v0.38.0 design (#708).

This doc describes projects as built in v0.41.0 (issues #519 and #771). In the v0.38.0 design (#708), a project replaced a space. That design shipped in v0.38.0 and v0.39.0. This doc marks the parts that it keeps.

## Problem

In v0.38.0, a project replaced a space. Every project took one top-level group. The user made a group and then gave it a folder. In use, this splits what belongs together (#771):

- The user keeps few spaces, such as **Work** and **Life**. In Work, the user works on several hosts. On each host, the user has several projects. In each project, the user has several tabs, for example development, a server and its logs. Projects and their tabs are most of the use.
- A project is one folder on one host. The user makes it from the folder, on the host. The user does not group it by hand. It is not a top-level group.
- A space that mixes projects, hosts and tabs by hand is a rare need.

The model is the sidebar of Codex. Each host shows its **Projects**. Each project shows its tabs. The `+` button makes a project on that host.

## Design

### Levels and words

GoRex has five levels. Each level has one word in every client, in the docs and in the own protocol of GoRex (`gorex.*` methods, the session server):

| Level | Word (zh-Hans) | What it is | GoRex code | Rex API |
| --- | --- | --- | --- | --- |
| 1 | Space (空间) | Work, Life: the picker at the top of the sidebar | a name on each project | none (GoRex only) |
| 2 | Host (主机) | this machine, alice-tower | a `hosts.json` entry | a gateway |
| 3 | Project (项目) | a folder on the host | a `Workspace` with a `Dir` | a session |
| 4 | Tab (标签页) | dev, logs | `Tab` | a window |
| 5 | Pane (分屏) | one shell of the splits of a tab | `Pane` | a block |

- "Session" names no level in the UI or the docs. It stays only where the Rex API says it (`session.*`, `session_id`). In the docs, it stays only next to the mapping in this table.
- The word "Window" in Rex Go (#760) becomes "Tab". The word "Block" in any string becomes "Pane".
- Each level has a name. The name of a space is the name on its projects. The name of a host is its alias in `hosts.json` (`internal/hosts`, `Label`). For this machine, the name of the host is the name set in Settings. The name of a project is `Workspace.Name`, which is the name of its folder by default. The name of a tab is its title, as today.
- The user renames each name in place. The user renames a space in Edit Spaces… of the space picker. The user renames a host in the menu of the host. The user renames a project or a tab with Rename…. A rename changes nothing else.
- The own methods and fields of GoRex use `project`, `tab` and `pane`: `project_id`, `tab_id`, `pane_id`. Their values are the Rex IDs of the same things (`session:…`, `window:…`, `block:…`). A client can pass an ID from a Rex method to a GoRex method without a change.

### Decisions

The user made these decisions on 2026-11-18 (#771):

1. **Space → Host → Project → Tab → Pane.** The space is the group at the top of the sidebar, as before v0.38.0.
2. **A project lives on its host.** The server of the host keeps it, as a Rex session with a folder. The desktop, Rex Go and the web client that connect to the host see the same projects. No sync between clients exists.
3. **A project belongs to one space.** This is the space where the user made it. The user can move it to another space. Work shows only the projects of Work.
4. **A project is made from a folder.** The `+` of the Projects of a host browses the folders of that host and makes the project. The folder and the host of the project do not change later. Another folder is another project. The name can change.
5. **The tab bar shows the tabs of the current project** when the tabs are on top. The title bar picks the host and the project.
6. **The projects of v0.38.0 and v0.39.0 become projects** of this machine, in one space (but see 12).
7. A tab with no project stays possible, as a secondary entry.

The user made these decisions on 2026-11-18, after a review of this design:

8. **No space list to sync.** A space is a name on its projects. The `gorex.spaces_sync` manifest and its iCloud copy go.
9. **No `ShareLayout` flag.** The layout of a host is where its projects and tabs live. Every client that the host lets in reads it.
10. **One project per folder on a host**, in any space.
11. **One "No Project" group for each host.** Every space shows it.
12. **Projects that were homed on another host in v0.38.0** are made on that host when it connects, with the tabs that they held there. They start no new shell. The user chose this after the review of #773 (see Migration).
13. **Clients cache the last projects of each host**, to show an offline host. The client only shows the cache. The client never changes it.
14. **Projects are in order of last use**, as Codex lists them.

The author of #771 made these decisions in the review of #773:

15. **Every level has a name that the user can change.** The user can rename a space, a host (its alias), a project and a tab. The name of a project is the name of its folder when the user makes it. Rename changes only the name. It never changes the folder or the tabs.
16. **A project row has its own `+`.** It opens a tab in the folder of the project, as ⌘T does while the project is chosen.
17. **The migration keeps what runs.** No shell stops or starts. Tabs keep their splits and their project as far as one host can hold them.

The design keeps these parts from v0.38.0:

- the folder browser, `dir.list` and `dir.mkdir`
- the rule that a new tab of a project starts in its folder
- the folder check
- the folder sheets of Rex Go and the web client

### Model on each host

The layout of a host (`internal/layout`) keeps its projects as workspaces, as it kept spaces:

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

- The `Host` field is gone from `layout.Workspace` and from the `Workspace` of the app. The folder of a project is always on the host whose layout keeps it. The server reads a layout of v0.38.0 once, as Migration describes.
- A workspace with a `Dir` is a **project**. The workspace with no `Dir` holds the **tabs with no project** of the host. Every space shows these tabs.
- The server refuses a second project for a folder. The caller gets the ID of the first project (`code: "exists"`).
- The Rex API shows each workspace as a Rex session, a tab as a window and a pane as a block, as today. The Rex apps see a project as a session, unchanged.
- A tab moves between the workspaces of one host with its panes and their processes. The actions are Move to Project and Move out of Project.

### Spaces

A space is only a name on projects. No list of spaces is kept or synced.

- The space picker of a client lists the names that the projects of its hosts carry. It also lists "Default" (a project with no `Space`). The client keeps the color and the order of each space in its own settings.
- **New Space…** adds a name to the picker on this client. The space stays there, empty, until a project moves into it.
- **Rename** a space: the client asks each connected host to rename it (`gorex.space.rename`). A host that is offline keeps the old name. The client keeps the rename and sends it when the host connects.
- **Delete Space…** is available for every space but Default. The desktop has a trash button on each row but Default in Edit Spaces…. Rex Go has a swipe or the minus button in Edit Spaces…. The web picker has the command in its menu, for the space shown. The command asks first: "Delete space <name>? Its N projects move to Default. No tab closes." The client counts N across the connected hosts. When a host is offline, the text adds that the projects of that host move when it connects. After the user agrees, these steps run:
  - Each connected host gets `gorex.space.delete {name}` (the layout command `spaces.delete` of the desktop). The host moves every project of the space to Default (`""`) and drops the name from `Store.Spaces`. It answers `{projects: n}`.
  - A host that is offline gets the command when it connects. The client keeps the command as a waiting rename to `""`, in order with the renames.
  - The client drops the name from its own made spaces, colors and order. It shows Default if the deleted space was the space shown.

  No tab closes, no shell stops and nothing else changes. A host of v0.41.0 has no such method. The desktop sees no `spaces.delete` capability. The web client gets `method_not_found`. In this case, the client sends `gorex.space.rename {from: name, to: ""}`. This moves the projects to Default and drops the name as well.
- A space that no project carries drops from the picker, unless the user made it on this client and it is still empty.
- The design drops the `gorex.spaces_sync` manifest and its iCloud copy (`docs/IOS-004-centralized-spaces.md`).

### The desktop window

The window of the desktop shows the layouts of the hosts. It no longer keeps a layout of the panes of other hosts.

- The layout of the host of a project keeps the tabs of the project. This includes their splits, sizes, floating panes and browser panes, as it keeps the windows and blocks of Rex. Every client of the host sees the same tabs.
- The layout of this machine keeps the projects of this machine. It keeps no pane of another host. The migration drops `Node.Host`, `Kills` and `Teleports` of the panes of other hosts.
- Each client keeps these items in its own settings. They are the space shown, and the project and the tab shown in each window. They are also the size of the window and the width of the sidebar. The last items are the projects that are expanded, and the colors and order of the spaces.
- `ShareLayout` goes. A client that the host lets in (the Remote Access list, a paired device) reads the layout and follows its changes. It already attaches to the sessions in the same way.
- **Offline hosts.** Each client keeps a copy of the projects of each host and the names and pane programs of their tabs, as it last saw them. It shows them greyed while the host is away, each tab with the tile of the program it ran. The user cannot change or open anything in the copy. The client replaces the copy when the host connects.

### Listing a directory

A new server operation lists one directory, so that a client can browse the folders of a host:

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

- The operation lists directories only. It lists no files and no sizes. It is a folder picker, not a file manager.
- On Windows, `Path` "" is the home directory. A root lists the drives.
- The app calls the operation on a remote host through the client of that host, as it sends other commands (`hostConn`).
- The Rex API serves the operation as `gorex.dir.list` (a GoRex method). The welcome lists `gorex.dir_list`. Rex Go and the web client use it. The same callers can use it as `session.create`. A caller that can start a shell can already read the directory tree.

A sibling operation makes a directory — New Project's "Create & Add" is the caller, which makes the folder its field names and then creates the project in it:

```go
// "dir.mkdir": make Path, with its parents, on this machine.
type DirMkdir struct {
	Op   string `json:"op"`             // "dir.mkdir"
	Path string `json:"path"`           // absolute, or ~ of the home directory
}
```

- `Path` reads as `dir.list`'s does: absolute, `~` or `~/…` of the home directory, or relative to it; symlinks on it are resolved first. The drives' top is refused, as is a device or pipe path (`rex.VolumePath`, the same rule as `dir.list`).
- The folder and any parents are made with the session user's rights, mode `0755`; a folder already there is a success, as `MkdirAll` says.
- The welcome lists `gorex.dir_mkdir` (`CapabilityDirMkdir`). A client that talks to a server without it falls back to its own Add flow, which refuses a folder that is not there. It is allowed to the same callers as `dir.list`.

### Desktop sidebar

This sketch shows the sidebar on the left, with space Work and three hosts:

```
┌───────────────────────────────┬──────────────────────────────────────────┐
│ ● ● ●   [■ Work ▾]            │ gorex › dev                          ⋯   │
├───────────────────────────────┼──────────────────────────────────────────┤
│ THIS MAC  ●                 + │ ~/code/gorex $ go test ./...             │
│ [▤] gorex                 ⌄ + │ ok   gorex   12.3s                       │
│  ▣  dev                       │ ◀ dev is the tab shown here              │
│  ▣  logs                      │                                          │
│  ▣  review                    │                                          │
│ [▤] notes                   › │                                          │
│ [▤] No Project              › │                                          │
│  ▣  scratch                   │                                          │
│  ▣  build                     │                                          │
│                               │                                          │
│ ALICE-TOWER  ●              + │                                          │
│ [▤] trainer                 › │                                          │
│                               │                                          │
│ OLD-LAPTOP  ○ offline         │                                          │
│ [▤] site (grey)             › │                                          │
├───────────────────────────────┤                                          │
│ ⚙  [+F]                   [H] │                                          │
└───────────────────────────────┴──────────────────────────────────────────┘
```

The sketches use text marks for icons. In the UI, each mark is a lucide SVG from `assets/icons/` (`DESIGN.md`). The UI never shows the character:

| Mark | Icon |
| --- | --- |
| `[H]` | `monitor` |
| `[F]` | `folder` (tinted by the color of the project if set) |
| `[▤]` | the folder tile of a project: a tile with the size and corners of a tab tile, in a neutral face, with a `folder` in the color of the project |
| `[T]` | the program tile of a tab, in the sketch of Rex Go |
| `[+F]` | `folder-plus`: New Project… at the foot of the sidebar |
| `[S]` | `search` |
| `+` | `plus` |
| `▾` `▸` `⌄` `›` | `chevron-down`, `chevron-right` |
| `✓` | `check` |
| `⌂` `↑` | `house`, `arrow-up` |
| `⚙` | `settings` |
| `☰` | `menu` |
| `●` `○` | the state dot of a host, drawn, not icons |
| `▣` | the program icon of a tab, its tile |

The marks `◀` and `←` are notes on a sketch. The UI does not draw them.

This sketch shows the space picker:

```
┌──────────────────────┐
│ ✓ ■ Work             │
│   ■ Life             │
│ ──────────────────── │
│   New Space…         │
│   Edit Spaces…       │
└──────────────────────┘
```

This sketch shows the context menu of a project:

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

- The hosts are the hosts that the user added (`hosts.json`, read through `internal/hosts`). A machine that the tailnet finds shows only in the Add Remote Host sheet (#761). A host shows in a space when it is connected or keeps a project of the space.
- A host is a section header. It is not indented. It is small and in capitals. It has its state dot and the `+` button that makes a project there. The context menu of the host has Rename… (its alias) and New Tab with No Project. The header of an offline host says "offline". Its projects show greyed, with their last tabs.
- A project row is flush with the header. It is as high as a tab row (`sideRowH`). It shows the folder tile of the project, its name and a chevron; it shows no tab count. The folder path is in no row or tooltip.
- The folder tile is in the room of the tab tiles. It is centred where the front tile of a tab is. It has the size and corners of a tab tile (`tileW`×`tileH`) and a neutral face. The folder in it has the color of the project, or the muted color when the project has none. So a project and its tabs have marks of one kind, and the folder and the colors of the programs tell them apart (#896). #832 drew a bare folder there, and it read as a tab tile.
- The chevron is at the end of the row, before the `+`. It shows at rest, not only under the pointer. It points down when the row is open and right when it is closed. A project with no tab has no chevron. The Older row has the same chevron.
- A click on a project row shows the project and opens it. On the project shown, or on the chevron of any project, a click opens or closes the tabs of that project alone. The tabs come under the project, flush with it, with no guide line. The tile of a tab is where the folder tile of the project is. The title of a tab starts where the name of the project starts. Opening or closing moves no name and no mark. The chevron turns, and the tab list folds open and shut, with the motion of the sidebar (none with Animation off or reduced motion).
- The project that the window shows, while its tabs are open, has a **face** of its own behind its row and its tabs (`groupFace`). No other project has one. The face tells where the window is and which tabs belong to the project. The face reaches 4 px past the rows. Its corner radius is the pill radius plus those 4 px, so both corners share one centre. Every project keeps the room of the face, so choosing another project moves nothing: only the color of the face fades in and out (at once with Animation off or reduced motion: no fade). The face is not clipped by the sidebar list: on the panes' side, where the list's inset is short of the 4 px, the list's box reaches over the sidebar's edge by the difference and its padding grows by the same amount, so no row moves. A 2 px gap stays between the face and the rows of the projects next to it. The pill of the active tab on the face is lifted (`liftedPill`): it has a lighter face where the system draws no glass, a soft shadow and a hairline.
- The list, the space picker and the foot of the sidebar keep 10 px from the edge of the window and from the panes (`sideInsets`). The side next to the panes has the 8 px edge of the sidebar beyond it.
- Each project keeps its own open state, per client (`sidebarOpen`). Choosing another project, in any way, closes none.
- The `+` of a project row opens a tab in the folder. It shows on the row under the pointer and on the chosen project. ⌘T does the same while the project is chosen.
- The context menu of a project has these items: New Tab, Rename, Color, Move to Space, Reveal Folder (this host only) and Close Project.
- The projects of a host are in order of last use (`Used`), the latest first. The projects that the user did not use for 30 days go under a collapsed **Older** row, at the level of the projects.
- **No Project** (the tabs with no project) is a group shaped like a project (projectGroup), the same as on Rex Go. Its header row has the folder tile in the muted tint (no project color), the label (one line, cut with an ellipsis, never wrapped), the chevron, and a `+` after it under the pointer or while it is shown. Its tabs come under it in the columns of the tabs of a project, and fold open and shut with the same motion. It is open at first, unlike a project that the window does not show, and keeps its open state per client as a project does (no new setting). It is the last group of the host and shows in every space. With no tab, it does not show, unless the window shows it. While the window shows it, with its tabs open, it has the face and its active tab is lifted, as a project's. A click on its row shows it, and on the one shown opens or closes its tabs. A click on a tab shows its workspace on that tab. The row is a drop target for a tab of the host, which then leaves its project. It has New Tab in its context menu. No Project that an adoption waits for (Migration) shows with the same folder tile and name as a waiting project, greyed, with "Waiting for <host>".
- New Project… at the foot of the sidebar is an icon, as Settings is. Its name is in its tooltip. The header of each host has its `+` too, and ⇧⌘N works.
- The web client has the same rows (below, Web client).
- A tab of a project that the window does not show names itself as a shown tab does. It uses the title that its program set, the icon of its program and its folder. These come from the session list of the host. No pane is attached. A shell at its prompt names itself by the shell and its folder, shown or not. It does not use the title or the done or error status that a program left when it ended there.
- The user drags a tab onto a project of its host to move it there, with its panes and their processes.

### Tabs on top

```
┌───────────────────────────────────────────────────────────────────────┐
│ ● ● ●  [■ Work ▾]  This Mac › gorex ▾ │ dev │ logs │ review │  +      │
├───────────────────────────────────────────────────────────────────────┤
│ ~/code/gorex $                                                        │
└───────────────────────────────────────────────────────────────────────┘
```

The control `This Mac › gorex ▾` opens this menu:

```
┌───────────────────────────────────────┐
│ This Mac                              │
│   ✓ gorex        ~/code/gorex     3   │
│     notes        ~/notes          1   │
│     No Project                    2   │
│ alice-tower  ●                        │
│     trainer      ~/work/trainer   2   │
│ old-laptop   ○                        │
│     site         ~/site           0   │
│ ───────────────────────────────────── │
│ New Project…                    ⇧⌘N   │
└───────────────────────────────────────┘
```

- The title bar shows `host › project ▾`. Its menu lists the hosts of the space and their projects, with tab counts, No Project, and New Project….
- The tab bar shows the tabs of the chosen project. ⌘T opens a tab in the folder of the project.

### New Project

The `+` of the header of a host opens the New Project sheet with the host chosen; New Project… in the palette and in the File menu opens it on this machine. The sheet wears the ⌘P command palette's shell: a glass panel that floats at the top of the window, its field in title size, and it copies T3 Code's add-project input (`apps/web/src/components/CommandPalette.tsx` in `pingdotgg/t3code`): the path field is the browser. The text to its last separator is the directory the "Directories" group lists; the leaf after it filters the folders shown, names the folder a row opens, and creates a folder no row names.

```
        ┌─────────────────────────────────────────────┐
        │ [F+] /home/alice/cod▎           [Add    ↩] │
        ├─────────────────────────────────────────────┤
        │ Directories                                   │
        │ ↖  ..                                         │
        │ [F] code                                      │
        │ [F] codepen                                   │
        ├─────────────────────────────────────────────┤
        │ (This Mac) (alice-tower) (old-laptop) Choose… │
        └─────────────────────────────────────────────┘
```

- `~/` is the home directory, where the field starts; `C:\` a drive's root; a bare name filters the home directory's folders. A pasted path replaces the text, quoted and escaped forms unwrapping (`resolvePastedPath`). A leaf that starts with `.` lists the hidden folders too.
- The rows rank as T3 Code's `scoreQueryMatch` does: an exact name, then a prefix, one at a boundary, one inside the name, then a fuzzy one. ↓ and ↑ highlight a row.
- A click or Enter opens the highlighted row — `..` lists the parent, a folder goes into it. With no row highlighted, Enter takes the field's path: the directory listed, or the folder the leaf names exactly. ⌘/Ctrl+Enter takes the path even over a highlighted row. Tab opens a highlighted row, enters the only folder the leaf starts, or extends the leaf to the start its folders share.
- Add — the button the field holds — makes the project named for the folder and opens its first tab in it. A leaf that names no folder says "Create & Add" and "Press Enter to create this folder and add it as a project."; submitting makes the folder with `dir.mkdir` first, and a server without `gorex.dir_mkdir` answers with its error. On Windows' list of drives there is no folder to take.
- Esc closes the sheet; it has no Create or Cancel buttons. On this machine Choose… opens the system's folder panel; a host's folders are browsed in the field alone.
- A folder can be a project of this host already. Then Add shows that project and makes no second project. When that project is in another space, it asks whether to move it here.

### New tabs and panes

| Action | Host | Folder |
| --- | --- | --- |
| ⌘T, `+` of the tab bar, New Tab of a project | the host of the project | the folder of the project |
| Split, floating pane | the host of the pane | the start directory of the pane, as today |
| New Tab with No Project | the chosen host | the home directory |
| CLI `--dir`, Rex API `cwd` | as given | as given |

The folder check of v0.38.0 stays. A missing folder fails with a note and offers Reveal or Close Project. An offline host fails with a note that names the host.

### Rex Go (iOS)

```
┌─────────────────────────────────┐
│ ☰        Work ▾             [S] │   ← space picker
├─────────────────────────────────┤
│ [H] This Mac                  + │   ← + makes a project here
│   [▤] gorex                   ⌄ │
│       [T] dev              ›    │
│       [T] logs             ›    │
│       [T] review           ›    │
│       + New Tab                 │
│                                 │   ← a card for each project
│   [▤] notes                   › │
│                                 │
│   [▤] No Project              ⌄ │   ← a group like a project
│       [T] zsh              ›    │
│       + New Tab                 │
│                                 │
│ [H] alice-tower  ●            + │
│   [▤] trainer                 › │
│                                 │
│ [H] old-laptop   ○ offline      │
│   [▤] site                      │   ← no tab, no chevron
└─────────────────────────────────┘
```

- The top shows the space picker, not a strip of projects.
- A tap on the picker opens a panel that grows from it, 8 pt under it and with no arrow, over the page; a tap beside the panel closes it. In a wide window's bar the picker opens it as a popover. The panel lists each space with its color and its project count, and marks the one shown. Edit is in its header and New Space comes last. A tap on a space shows it and closes the panel.
- Each host lists its projects of the space, in order of last use. Then it lists its tabs with no project, in a group of their own, which is a card like a project's. Each tab has a row. A New Tab row comes after them. A project expands to its tabs and a New Tab row.
- The row of a project shows its folder tile, its name, and a chevron when it has tabs; it shows no tab count. The list does not show the folder itself, as on the desktop and the web client. The rows follow the desktop sidebar (#907), adapted to touch and to the system's list:
  - **Folder tile.** The tile has the size and corners of a program tile (21×16 DIP at the scale of the tab rows) and a neutral face: the text color at 9%, as on the desktop (`projectFolderTile`). It has no white plate and no shadow. The folder in it has the color of the project, or the muted color when the project has none. It stands in the column of the program tiles. The name of the project starts where the titles of the tabs start. One column width serves every row of a host, taken from the tab with the most tiles behind its front tile, so opening a project moves no name. In the project list, the program tiles of the tabs lose the white ring and the shadow that a light page gives them, so a tile looks as it does on a dark page. The tab sheet, the pane bar, the inbox, the widgets and the Live Activity keep them.
  - **A card for each project.** Each project has a card of its own with its row, its tabs and its New Tab row. A closed project is a card with its row only. A gap of 12 pt, as between the other cards of the host, separates the cards. The projects under Older share the card of Older. The tabs with no project have a card of their own, No Project (below). The card of a host away is the same.
  - **Chevron.** The chevron comes after the name, at the end of the row, and always shows. It points right when the project is closed and down when it is open, and it turns with the opening. A project with no tab has no chevron. The Older row has the same chevron before its count.
  - **One height.** A project row and a tab row are 50 pt high at least, and grow with Dynamic Type. Both keep a touch target of 44 pt.
  - **Current project.** On a wide window (iPad, the sidebar beside a terminal) the project that holds the open terminal, while its tabs are open, has a **face**. In a card of its own, the card is the face: its fill is a little stronger, and it fades in and out by opacity, so nothing moves. Under Older, where projects share a card, the face is inside the card: 4 pt in, with a radius of 18 pt, a row's 14 pt pill plus those 4 pt. The tab that is open is lifted: a lighter face, a soft shadow and a hairline. It keeps 4 pt from its own card (radius 18 pt, the 22 pt of the card less those 4 pt), or 8 pt under Older (radius 14 pt). The inner radius is always the outer radius less the padding. On a narrow window (iPhone) the list is the whole page and the terminal covers it, so no project is current and no face shows. The projects there keep the mark, the chevron and the height.
  - **Opening.** The tabs of a project and its New Tab row come and go as one block, with the opening of the chevron: they fade and shrink to the row above. With Reduce Motion they change at once.
  - **No Project.** The tabs with no project are a group like a project, in a card of the same shape. Its name is "No Project" (a Rex Go label; the desktop keeps "Tabs with no project"). A name in a row is one line, cut at its end, for a project's too. Its first row is built like a project row: a folder tile with a muted folder, as a project with no color has, and the chevron. Its tabs and a New Tab row come under it. The chevron opens and closes it with the same motion as a project's. It is open at first. With no tab it has no chevron and does not fold, as a project with no tab. Its open state lives in the in-memory `collapsed` state of the list, as the closed hosts do, and resets when the app restarts. No saved file and no protocol changes. The row's context menu has New Tab. When the shown tab has no project, on a wide window, this card is the face and the tab is lifted. The card is 12 pt from the cards next to it. The tabs use the same tile column as the projects.
- An offline host shows its projects greyed, from the copy that the app kept when it last saw them.
- The `+ ` of the host opens the folder browser of v0.38.0 on that host, with the name and a Space row that picks the space the project is made in (the space shown, at first), and makes the project (`gorex.project.create`). The list shows the space the project went to.
- New Tab in a project opens on its host with `cwd` set to the folder.
- The menu of a tab has Move to Project, with `gorex.project.move`.

### Web client

The web client is the same as Rex Go, for the host that the page is connected to and its hosts. Its sidebar has the rows of the desktop (#896; #832 and #775 drew its own):

- A project, Older and No Project are groups built by one function (`projectGroup`, `groupHead` in `web/src/projectsidebar.ts`). The head row is as high as a tab row (32 px). It has a neutral tile of a tab's program tile (21x16 px, radius 4.5 px) with the folder in the project's color (muted with none), the name on one line cut with an ellipsis and the chevron. No project row shows a tab count; only Older shows its count of projects. The tile column is as wide as the widest tab tile column of the list, so every name and every tab title start at one place.
- The chevron is at the row's end, before Older's count: right when shut, down when open, with `aria-expanded` on the row's button. A group with no tabs has none. It turns, and the tabs fold open and shut, in 200 ms. With `prefers-reduced-motion` they change at once. The sidebar is made again at each change, so the motion plays on the new elements (`folds`).
- The shown project, while open with tabs, has a face (`groupLook`, `.pgroup.face`): 4 px past its rows, radius 20 px (the pill's 16 px and the 4 px). Its active tab is lifted (`--pill-lift`, a soft shadow, a hairline). Every group keeps the room of the face, so showing another project moves nothing, and only the color fades. Rows of two groups are 6 px apart (2 px between a face and the next group's rows). The list keeps 10 px from the sidebar's edges.
- **No Project** is the group of the host's tabs with no project: the catalog name `projectNoProjectName` ("No Project", zh-Hans 「无项目」), a muted folder tile, the chevron, a `+` and the menu with New Tab, and a drop target that moves a tab out of its project (`gorex.project.move`, as before). It is open at first and keeps its open state in `gorex.web.expanded` as a project does. It is not renamable. It has the face while the shown tab has no project.
- New Project… at the sidebar's foot is the `folder-plus` icon alone; the name is its title and `aria-label`.
- No wire protocol or saved file changes: `gorex.web.expanded` keeps its format.

### Rex API

The Rex methods keep the Rex words (`session`, `window`, `block`). The own methods of GoRex use the words above:

- `gorex.project.create {name, dir, space}` → `{project_id, tab_id}`. The `dir` goes through `rex.VolumePath` first, so the server refuses a device or pipe path (`docs/lessons.md`, "device paths"). Then the server checks the folder as `dir.list` does, and the first tab of the project starts in it. A folder that is a project already answers `code: "exists"` with the ID of that project.
- `gorex.project.move {tab_id, project_id}` moves a tab, with its panes, between workspaces of this host. `project_id` "" moves it to the tabs with no project of the space.
- `gorex.project.adopt {project?: {name, dir, space}, tabs: [{title, split}]}` makes the project when `project` is given and the project does not exist yet. The server checks its folder as above. The method also arranges panes that already run into the tabs given. Each `split` is a tree of `pane_id`s as `layout.Node`. The method starts no shell and opens no tab of its own. It skips a pane that it does not know. To adopt again what is adopted changes nothing. The migration uses the method.
- `gorex.project.update {project_id, name?, color?, space?}`.
- `gorex.space.rename {from, to}` renames the space on every project of this host.
- `gorex.space.delete {name}` → `{projects}` moves every project of the space to Default and drops the name from `Store.Spaces`. `name` `""` fails with `bad_request`.
- Each session in a session list and a session view gains `project: {dir, space, used}`. The tabs with no project of the host have `project: {}`. The Rex apps ignore the field.
- The design drops `gorex.project.set_home` of v0.38.0. The folder of a project does not change.
- The welcome lists `gorex.projects` with `version: 2`. A client that sees no version talks to a host of v0.38.0 to v0.40.x. It shows the tabs as before, with no Projects section.

### Decided in phase 1

The design left these points open. Phase 1 (the server model) decided them:

- **Welcome.** `features` lists `gorex.projects`. The new `feature_versions` object of the welcome gives its version: `"feature_versions": {"gorex.projects": 2}`.
- **`exists`.** `gorex.project.create` fails with an error of code `exists` (status 409) when the folder is a project already. The `project_id` of the error names that project.
- **Space names offered.** The layout keeps the names of the old spaces in `Store.Spaces`, until a project carries one. `gorex.space.list` answers the names that the projects of the host carry, then those names.
- **Adoptions.** Each adoption is one host and one project (or none). It has `ID`, `Host` (its `hosts.json` ID), `Machine` when known, `Project` and `Tabs`. The app takes one with the local command `adopt.taken`. An adoption whose project folder is not on the host puts its tabs in the tabs with no project. An adopted tab counts as adopted when one tab of the project holds all its panes that run. Then adopting again changes nothing.
- **One folder, one path.** The server keeps the folder of a project as `dir.list` names it (`rex.ProjectFolder`). Its symlinks are resolved. On Windows, it has the case that it has on disk. So `/tmp/x` and `/private/tmp/x` are one project. `project.create`, `project.adopt` and `space.home` (and the `set_home` shim) resolve the folder before the check for one project per folder.
- **Tabs with no project.** The workspace of these tabs in the migration takes the ID of the first old space, so Rex Go 1.1 keeps that session. It takes the name "Tabs with no project".
- **Floats of another host** join the part of the split of that host.
- **`Used`** follows input at most once a minute per pane. A new tab sets it at once.
- **Share Layout.** The server accepts `remote.shareLayoutSet` of an older app and changes nothing. `remote.status` says `shareLayout: true`.
- **Older clients.** The `project` field of a project also carries `home_machine`, which is this machine. So Rex Go 1.1 and the web client of v0.39.0 find its host. `gorex.project.set_home` stays as a deprecated shim onto `space.home`. It accepts a folder of this machine only. It serves their New Project (`session.create`, then `set_home`). `gorex.spaces_apply` stays. It ignores `home_machine` and `dir`, and makes no workspace. A space that is no session sends its windows to the tabs with no project, but a project's windows do not go there.
- **One workspace with no folder.** The migration makes one. Tabs that the server places itself go to it (a session of a remote client, a tab with no space). `space.create` and the `session.create` of Rex still make a workspace with no folder. The desktop before phase 2 and the clients before phases 3 and 4 make a project as such a workspace. Then they give it a folder (`space.home`, `set_home`). Phases 2 to 4 stop sending these commands. Then the server refuses a second workspace with no folder.
- **Until phase 2,** the desktop still sends `space.home`. The server keeps it for a folder of this machine only. Another host's folder fails with `remote_home`. A second project of a folder fails with `exists`. Between phase 1 and phase 2, a migrated desktop no longer shows the panes of other hosts in its old tabs. They wait in the adoptions.
- **Phase 2 drops** `space.home`, the `Workspace.Host` of the app, and the waits for the home host of a project. It sends the adoptions. **Phases 3 and 4 drop** `gorex.spaces_apply`, `featureSpacesSync` and `gorex.project.set_home` once Rex Go and the web client no longer send them. Then `space.create` and `session.create` make no second workspace with no folder. As built, the code still has `space.home`, `gorex.spaces_apply`, `featureSpacesSync` and `gorex.project.set_home`. The code has no `Workspace.Host`, `projectHost`, `homeArg`, `projectOpen` or `layout.MachineHost` function. Only comments in `internal/layout/layout.go` and `internal/layout/migrate.go` still name `MachineHost`.

### Decided in phase 2

The design left these points open. Phase 2 (the desktop) decided them:

- **A client arranges the tabs of a host.** A remote client that the host lets in may send `tab.set` and `layout.apply` (`rex.remoteCommand`), for the own panes of the host only. The host refuses a leaf that names another host. It also refuses a place that the client could not see. It refuses `windows.set`, `kills.taken`, `show.taken` and `adopt.taken`, which stay with the own window of the host. The layout that a peer reads keeps browser panes. The text of a note stays on its host. The place of a note shows as an unavailable pane. The app arranges no tab that has such a pane (`lockedTabs`).
- **App and host forms.** In the app, a pane of a host names its host (`Node.Host`), as before. The own layout of the host names none. The app turns one form into the other as it takes and sends layouts (`hostForm`, `appForm`). It sends a host the changes of its workspaces through an outbox of that host, as it does for this machine.
- **The window owns the tab that it shows.** The app sends a host no change of the active tab of a workspace. The active tab of a host moves no window. The app keeps the project that each window shows in `settings.json` (`shownProjects`, by the place of the window). `Store.Windows` of this machine names only its own workspaces. A window whose project is a project of a host shows it again when the layout of the host comes.
- **Offline copy.** `hosts-layout.json` beside `hosts.json` keeps the layout of each host as the app last took it. The app writes it as it takes a layout.
- **Hosts of v0.38.0 to v0.40.x** show each session as a tab with no project there. The app arranges nothing on them. Their adoptions wait ("Waiting for" the host) until the user upgrades them.
- **A project with no tab stays,** as its host keeps it. When the user closes its last tab, the window shows another workspace. When the user chooses it again, the window shows it with no tab. New Tab opens a tab in its folder.
- **Space renames for a host that is away** wait in `settings.json` (`spaceRenames`) and go when the host connects.
- **Teleport** (the app's) puts the session that arrives in the tabs with no project of the host (`project.adopt`). The window shows it.
- **Kills.** The own kills of the app for the sessions of hosts stay in `hosts.json` (`Kills`), as before. A layout of v0.38.0 to v0.40.x could hold kills of other hosts for a host that is away. Another client of this machine made them. The migration drops them and does not send them. Such a session runs on until the user closes it on its host.
- **A tab named as its project loses that name, once.** `project.create` of v0.41.0 and v0.41.1 gave the first tab of a project the name of the project. This hid the title of the tab. It no longer does this. When the layout loads, the server clears the name of each tab of a project that equals the name of its project exactly. It does this once (`unnameProjectTabs`, noted in `layout.tabnames` beside the layout). Then the tab shows its title. The tabs with no project keep their names. A user who wants the name back renames the tab.
- **`space.home`** stays on the server, for the deprecated `gorex.project.set_home` of phase 1. The desktop no longer sends it. `Workspace.Host`, the waits for the home host and the `layout.MachineHost` function are gone.

## Migration

No shell stops or starts. A tab keeps its splits and its project as far as one host can hold them. A tab now lives on one host. So a tab of v0.39.0 whose panes ran on several hosts becomes one tab on each host. Each tab has the part of the split of that host.

**On each server, as it loads its layout** (also after `gorex server upgrade`):

- A workspace with a `Dir` and `Host` "" (a project of this machine) stays a project, in space "Default". Two workspaces of one folder become one, with the tabs of both.
- A workspace with no `Dir` (an old space): its tabs of this machine go to the tabs with no project. Its name becomes a space name that the client offers. So the old spaces of the user come back in the picker.
- For every tab with panes of other hosts, the server keeps an **adoption** in the layout (`Store.Adopt`). The adoption holds the machine of the host. It holds the project when the workspace was homed there (name, folder, space). For each tab, it holds the part of the split that ran on that host, by the session IDs of the panes. The server drops those panes from its own tab. A tab that has no pane left goes.
- The server keeps the old layout as `layout.json.v039`.
- A layout of projects can keep a pane of another host. A client from before projects puts such a pane back (`tab.set` of its own model). The server repairs this layout as it loads, with the same adoptions (`layout.RepairHostPanes`). The server logs the `tab.set` that named the pane. A space that has no tab that was no project becomes a name that the picker offers, as an old space does.

**On the desktop, as each host connects,** the app sends that host its adoptions (`gorex.project.adopt`). The server forgets each adoption once the host answers:

- A v0.38.0 project that was homed there is made on that host. Its tabs of that host go into it with their splits.
- The panes of the other tabs on that host become tabs with no project there, with their splits.
- A host that is offline keeps its adoptions until it connects, however long. Meanwhile its panes show from the offline copy, if any. The project shows greyed under the host with a note "Waiting for <host>".
- The migration skips a pane whose session ended on its host meanwhile. It drops an adoption that has nothing left.
- A host of v0.38.0 to v0.40.x (no `gorex.projects` version 2) keeps the adoption until the user upgrades it.

Rex Go drops its manifest and reads projects from the hosts.

### Cases

| Before (v0.39.0, this desktop) | After |
| --- | --- |
| Project gorex, folder here, tab dev split in 2 local panes | project gorex here, tab dev, same split |
| Project trainer homed on alice-tower, tab train: 1 pane there | project trainer made on alice-tower, tab train in it |
| Space Work, tab api split: local pane left, alice-tower pane right | tab api here (left pane), tab api on alice-tower (right pane), both with no project |
| Project trainer homed on alice-tower, tab mon: 2 panes there split, 1 local | tab mon in trainer on alice-tower with its 2-pane split. Tab mon here with no project |
| alice-tower offline at the upgrade | its adoption waits and completes when it connects |

## Tests

- Model: every case of the Migration table, each checking that the same shell processes run before and after. An adoption applied twice changes nothing. A project round-trips with its space. A tab moves between projects and keeps the processes of its panes.
- Rex API: `gorex.project.create` checks the folder and opens a tab in it. `gorex.project.move` keeps the panes of the tab. Old clients see their sessions as before.
- Desktop: the sidebar shows only the hosts, projects and tabs with no project of the current space. `+` makes a project on that host. ⌘T opens in the folder of the project. The checks are UI tests with `ui.NewTester` and screenshots by the `GOREX_DEBUG` hook.
- Rex Go: projects under each host, create and move, on the simulator against a real gateway.

## Phases

Each phase is its own PR. The team runs, reviews and merges each PR before the next phase.

1. **Server model**: `Workspace.Space`, `Used`, one project per folder and the `Host` field dropped. Also the migration of the server and its adoptions, `gorex.project.adopt` and `ShareLayout` dropped. Last, the Rex API methods and fields, and tests of every case in Migration.
2. **Desktop window from the layouts of the hosts**, sending adoptions as hosts connect. Then the sidebar and the title bar: hosts, Projects, tabs with no project, move by drag and menu, the space picker and the offline copy. The new UI goes in files of its own (`projects_sidebar.go` and the like), not in `state.go` (`docs/lessons.md`).
3. **Rex Go**: the space picker, projects under each host, create and move, the offline copy and the manifest dropped. "Window" becomes "Tab".
4. **Web client.**
5. **Docs**: the README, `docs/REF-010-desktop-app.md`, `docs/REF-005-rex-api.md`, `docs/REF-001-cli.md` (Share Layout), and `docs/IOS-004-centralized-spaces.md` marked replaced.

## Open questions

1. A project in more than one space: not planned. Move to Space is the only change.
