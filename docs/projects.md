# Projects: tabs grouped under a host in a space

Status: proposed. Issue #519.

## Problem

A space holds tabs, and a tab holds panes (`internal/layout/layout.go`:
`Workspace`, `Tab`, `Node`). With remote hosts, the sidebar lists a
space's tabs under the host each runs on (`sidebar.go`, `sidebar`), and
the host view lists Host → Space → Tab (`hostSessions`).

One piece of work often needs several tabs on one host: an editor, a
training monitor, a service log. Today these lie flat among the host's
other tabs. The user cannot fold them, name them as one thing, or start
a new terminal of that work in its directory.

## Goal

A **project** is a named group of tabs of one space on one host. The
user's examples:

- `Work → office-h800 → windflow → dev, training monitor, service logs`
- `Life → mba-15 → blog → writing, preview`

The user can:

- create, rename, fold, unfold and remove a project;
- put existing tabs in a project, and take them out, without a restart
  of any terminal and without a change to any tab's panes;
- keep tabs outside every project, as today;
- give a project a directory, where its new tabs start.

Every client of the server that keeps the space — the desktop windows,
Rex Go on iOS and the web client — sees the same projects, with the same
names, members and order.

## Non-goals

- **Groups shared by all devices.** Today each GoRex desktop keeps its
  own spaces on its own session server. A space of the Mac that holds
  tabs of office-h800 is the Mac's, not office-h800's. Sharing spaces,
  and with them their projects, between desktops is #518. This design
  keeps projects where spaces are kept, so that #518 takes them with the
  spaces (Relation to #518).
- **Projects in Rex's own apps.** Rex's API has no such level. Rex's
  apps keep seeing the tabs, in the same order, as today.
- **Projects in the strip above the panes.** With the tabs above
  (`TabPlacement` ""), tabs show flat, in project order. Only the
  sidebar shows project rows.
- **Nested projects**, and a project over several hosts.
- **Tab names and split layout across devices.** Those are #520 and
  #521.

## Design

### Model

A project is a field of the space, and a tab names its project. The
tabs stay in one flat list, `Workspace.Tabs`:

```go
type Workspace struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Color    string    `json:"color,omitempty"`
	Projects []Project `json:"projects,omitempty"`
	Layout
}

// Project is a named group of tabs of the space on one host.
type Project struct {
	ID   string `json:"id"`             // random, lasts while the project does
	Name string `json:"name"`
	Host string `json:"host,omitempty"` // as Node.Host: "" for this machine
	Dir  string `json:"dir,omitempty"`  // where its new tabs start, on Host
}

type Tab struct {
	// ...
	Project string `json:"project,omitempty"` // Project.ID, "" for none
}
```

Why a field, and not a level that holds the tabs:

- Every walk over a space's tabs stays as it is: `Layout.Active`, Rex
  API window ids (`windowIDOf`), `tab.move`, `tab.to_space`, the
  journal, and `reximp`.
- A layout of an older GoRex reads as today: no project, no tab in one.
  An older GoRex that reads a newer layout drops the two unknown
  fields and shows the tabs flat. Nothing is lost but the grouping.
- A tab keeps its `ID` and its panes. To put it in a project changes
  one string; no session ends or starts.

`Projects` is in the order the sidebar shows them under each host.
Projects of different hosts share the list. Each host's own are in
their relative order.

### Rules the server keeps

The session server applies every layout change (`internal/rex`,
`layout_apply.go`). After each change it keeps these rules, in
`layout_apply.go`'s normalization:

1. **One host.** A tab is in a project only while its host
   (`Tab.host`: its first pane's) is the project's. When a teleport or
   `pane.move` takes the tab to another host, the tab leaves the
   project.
2. **Members together.** A project's tabs are next to each other in
   `Tabs`. When a tab joins a project, the server moves it after the
   project's last tab. So `Ctrl+Tab`, the strip and Rex's window order
   go through a project's tabs in a row, as the sidebar shows them.
3. **No dangling names.** A tab whose `Project` names no project of its
   space has `Project` "". `tab.to_space` clears it: projects do not
   move between spaces.
4. **Empty projects stay.** A project with no tab stays until the user
   removes it, so that the user can make one first and fill it next.
5. **Removing a project ungroups it.** Its tabs stay open, outside every
   project. Closing them is the user's separate action.

### Commands

New `LayoutCommand` ops (`internal/rex/proto.go`), each with `Space`:

| Op | Fields | Does |
| --- | --- | --- |
| `project.create` | `Project` (id, may be ""), `Name`, `Host`, `Dir`, `Target` (tab ids) | Makes the project after the host's last, with the tabs `Target` in it. |
| `project.rename` | `Project`, `Name` | Renames it. |
| `project.set_dir` | `Project`, `Dir` | Sets or clears its directory. |
| `project.remove` | `Project` | Removes it; its tabs stay (rule 5). |
| `project.move` | `Project`, `Index` | Moves it among its host's projects; its tabs move with it in `Tabs`. |
| `tab.set_project` | `Tab`, `Project` ("" for none), `Index` | Puts the tab in the project at `Index` among its tabs, or takes it out. |

A command that breaks rule 1 — a tab of another host than the
project's — fails with an error, and changes nothing.

`tab.create` takes `Project` too. The new tab is made in the project,
after its last tab, and starts in the project's `Dir` when the command
has no `Dir` of its own.

Fold state is not in the layout. It is each client's own, as
`prefs.FoldedHosts` is today: a desktop window folds a project without
folding it on the phone. Which tab a client shows is its own too, as
today.

### Desktop sidebar

Under a host's section, the sidebar shows the host's projects in order,
then the host's tabs that are in none:

```
▾ office-h800                              +
  ▾ [folder-open] windflow              3
      [tile] dev
      [tile] training monitor
      [tile] service logs
  ▸ [folder] old-experiments            2
    [tile] scratch
```

- **The project row**: a chevron, a Lucide `folder` (folded) or
  `folder-open` (open) icon, the name, and the tab count. It is as high
  as a host's section row. `assets/icons` has `folder-open.svg`;
  Lucide's `folder.svg` is added beside it. Its tabs are indented 12 points, as the host
  view indents a space's tabs.
- **Folded**: the row shows only, with the count. As a folded host does
  today, it keeps showing the tab the window shows, and tabs that need
  the user (`needsYou`).
- **On hover**, a `plus` button makes a new tab in the project.
- **Its context menu**: Rename, Directory…, New Tab, Remove Project.
- **A tab's context menu** gets **Move to Project**: the host's
  projects, None, and New Project…, which makes one with that tab.
- **Drag and drop**: a tab dropped on a project row joins it. A tab
  dragged among a project's tabs reorders them. A tab dragged out
  between loose tabs leaves it. A tab of another host dropped on a
  project asks to teleport (`askTeleport`), as it does on a host row
  today; if the user agrees, the tab joins the project after the
  teleport.
- **The host's `+`** keeps making a loose tab.
- **The command palette** gets `New Project`, `Rename Project`,
  `Move Tab to Project…` and `New Tab in Project…`.

The host view (Host → Space → Tab) shows the same project rows between
a space and its tabs on that host.

A project's directory is a path on the project's host. For this machine
the field offers the folder picker. For a remote host it is a text
field, as GoRex has no browser of a remote file system.

New UI follows `DESIGN.md`: the rows reuse `sidebarTab`'s faces and the
existing hover and drop styles. No new shader is needed.

### Rex API and its clients

Rex's methods stay as they are. Rex's apps see windows, in the order of
rule 2.

GoRex's own clients get projects as GoRex already gives them spaces
across hosts (`internal/rexapi/spaces.go`):

- **Read.** The welcome lists a feature `gorex.projects`. Each session
  in a session list, and each session view, carries `projects`:
  id, name, the host as a machine id (`rex.MachineID`, as
  `remoteBlock.Machine`), the directory, and the window ids of its
  tabs in order. A Rex app ignores the field it does not know.
  Unconfirmed: that Rex's iOS app ignores an unknown field of a session
  view. Check before release by connecting Rex's app to a gateway that
  sends it; if it does not, serve projects at `GET /api/projects`
  instead, as `/api/spaces`.
- **Write.** Methods `gorex.project.create`, `gorex.project.rename`,
  `gorex.project.set_dir`, `gorex.project.remove`,
  `gorex.project.move` and `gorex.window.set_project`, each mapping to
  the command of the same meaning.
- **Events.** A change of projects is a change of the session's view:
  it goes into `viewSignature`, so the gateway sends
  `session_view_changed`, as it does for a change of the windows.

### Rex Go (iOS)

The session sheet lists a space's windows (`SessionSheet.swift`). With
`gorex.projects`, it groups them under project headers, folded or open
per device. A long press on a window offers Move to Project; a project
header offers Rename and Remove. Creating a project with a directory
picker is desktop-only at first.

Cross-host spaces (`Spaces.swift`) show a remote host's tabs from that
host's own gateway. The project of such a tab is in the space's server,
not the remote host's, so the grouping comes from the space's server's
`projects` field. The window ids there are the space server's, which
the app already maps for `remote_blocks`.

### Web client

The web client's sidebar (`web/`) groups windows under project rows as
the desktop does, with the same context-menu actions. It reads and
writes through the same methods.

## Relation to #518

#518 asks that all devices share one set of spaces, synced between
devices without a central server. It is not designed yet. This design
holds for it:

- A project lives in its space, and names its host by id. When #518
  syncs spaces, projects go with them, with no second sync.
- In the API, a project's host is a machine id, which is the same on
  every device; `hosts.json` ids are not.
- Project and tab ids are random and lasting, so two devices that make
  changes apart can merge them by id.

Until #518, a project made on the Mac's space shows on every client of
the Mac's server — Rex Go and the web client among them — but not in
office-h800's own desktop windows, which have spaces of their own.

## Phases

1. Model, commands and the rules in `internal/layout` and `internal/rex`,
   with tests on `layout_apply`.
2. Desktop sidebar, menus, drag and drop, palette; UI tests with
   `ui.NewTester` and screenshots by the `GOREX_DEBUG` hook.
3. Rex API read and write, with contract tests in `internal/rexapi`.
4. Rex Go and the web client.

Each phase is its own PR. Phase 1 alone changes nothing a user sees.

## Tests

- `layout`: a layout with projects round-trips; an older layout reads
  with no projects; an unknown project on a tab is dropped.
- `rex` apply: each command; rule 2 after `tab.create`, `tab.move` and
  `tab.set_project`; rule 1 after a teleport; rule 3 after
  `tab.to_space`; removing a project closes no session.
- App: a project row folds and keeps the shown tab and tabs that need
  the user; drop on a project row; a new tab in a project starts in its
  directory (local host).
- `rexapi`: `projects` in session views; the methods; a Rex client
  without the feature sees the windows in rule 2's order.

## Open questions

1. Should removing a project offer to close its tabs too, or only
   ungroup them? This design only ungroups.
2. Should the strip above the panes show project boundaries, such as a
   gap or a label? This design shows none.
3. Should a tab made by a remote client (Rex Go's new window) join the
   project of the tab it was made from? This design puts it in none,
   unless the client names a project.
