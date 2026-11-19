package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/locales"
)

// The sidebar of projects (docs/projects.md, "Sidebar"): the space picker
// atop, then each host the user added, with its PROJECTS of the space
// shown, in order of last use, those older than olderAfter under a folded
// Older, and its tabs with no project, which every space shows. A project
// expands to its tabs; the window's project lists the window's own tabs,
// which are chosen, dragged and renamed as tabs are.

// olderAfter is how long a project goes unused before it goes under
// Older.
const olderAfter = 30 * 24 * time.Hour

// projectRowH is the height of a project's row and of a host's.
const projectRowH = 28

// openKey names a row of the sidebar that opens and closes, in
// settings.Open.
func openKey(h *hostConn, id string) string { return hostKey(h) + "/" + id }

// rowOpen tells whether the row of key is open: as the user left it, else
// as def says.
func rowOpen(key string, def bool) bool {
	if open, ok := prefs.Open[key]; ok {
		return open
	}
	return def
}

// hasRowOpen tells whether the user, or a project left, set key's row.
func hasRowOpen(key string) bool { _, ok := prefs.Open[key]; return ok }

// setRowOpen opens or closes the row of key, for this client.
func setRowOpen(key string, open bool) {
	if prefs.Open == nil {
		prefs.Open = map[string]bool{}
	}
	prefs.Open[key] = open
	saveSettings()
}

// hostShown tells whether host h, nil for this machine, shows in the
// sidebar for space: this machine always, another host while it is
// connected or keeps a project of the space, or waits for an adoption.
func (a *App) hostShown(h *hostConn, space string) bool {
	if h == nil || h.current() != nil || a.st == nil {
		return true
	}
	for _, w := range a.st.hostWorkspaces(h) {
		if w.Dir != "" && w.Space == space {
			return true
		}
	}
	return len(a.st.waitingOn(h)) > 0
}

// projectsOf are host h's projects of space, in order of last use, and
// apart those not used for olderAfter.
func (a *App) projectsOf(h *hostConn, space string) (recent, older []*Workspace) {
	if a.st == nil {
		return nil, nil
	}
	for _, w := range sortedWorkspaces(a.st.hostWorkspaces(h)) {
		if w.Dir == "" || w.Space != space {
			continue
		}
		if !w.Used.IsZero() && time.Since(w.Used) > olderAfter && w != a.ws {
			older = append(older, w)
		} else {
			recent = append(recent, w)
		}
	}
	return recent, older
}

// projectsList is the sidebar's list (docs/projects.md, "Sidebar"): each
// host shown, as a section header, then its projects of the space, flush
// with it, those not used for olderAfter under Older, the adoptions that
// wait for it, and its tabs with no project.
func (a *App) projectsList(c *ui.Context, k *colors) {
	if a.st == nil {
		tabs := slices.Clone(a.tabs)
		reserve := tilesReserve(tabs)
		for i, t := range tabs {
			a.sidebarTab(c, k, i, t, reserve)
		}
		return
	}
	space := shownSpace()
	reserve := tilesReserve(a.tabs)
	for _, h := range a.allHosts() {
		if !a.hostShown(h, space) {
			continue
		}
		ui.Column(c.Key("host-body-"+hostKey(h))).Gap(rowGap).Padding(0, 0, 10, 0).Children(func() {
			a.hostHeader(c, k, h)
			recent, older := a.projectsOf(h, space)
			for _, w := range recent {
				a.projectRow(c, k, w, reserve)
			}
			if len(older) > 0 {
				key := openKey(h, "older")
				open := rowOpen(key, false)
				if a.foldRow(c, k, key, locales.ProjectsOlder(), fmt.Sprint(len(older)), open, reserve) {
					setRowOpen(key, !open)
				}
				if open {
					for _, w := range older {
						a.projectRow(c, k, w, reserve)
					}
				}
			}
			for _, ad := range a.st.waitingOn(h) {
				a.waitingRow(c, k, h, ad, reserve)
			}
			// Tabs with no project shows while it has a tab, or the
			// window shows it.
			if w := a.st.noProjectOf(h); w != nil && (w.tabCount() > 0 || w == a.ws) {
				a.projectRow(c, k, w, reserve)
			}
		})
	}
}

// hostHeaderH is the height of a host's section header.
const hostHeaderH = 24

// hostHeader is a host's section header over its projects, as the
// sidebar's other section labels: its name, small and in capitals, its
// state's dot, "offline" while it is away, and the + that makes a project
// there (the folder sheet on that host). Its menu renames it (its alias)
// and opens a tab with no project there. A host away is greyed.
func (a *App) hostHeader(c *ui.Context, k *colors, h *hostConn) {
	name, key := a.hostName(h), hostKey(h)
	away := h != nil && h.current() == nil
	row := ui.Row(c.Key("host-"+key)).Height(hostHeaderH).Shrink(0).Padding(4, 4, 0, 8).Gap(6).AlignItems(ui.Center).
		Role(ui.RoleHeading).Label(name)
	if anim := motion(c); anim != nil {
		row.Transition(ui.ElementTransition{Animation: anim, Position: true})
	}
	// A tab of another host's dropped on it asks to teleport there.
	if dragged, ok := ui.Drop[*Tab](row); ok && teleportsTo(dragged, h) {
		a.askTeleport(dragged, h)
	}
	if dragged, over := ui.DragOver[*Tab](row); over && teleportsTo(dragged, h) {
		row.Radius(pillRadius(hostHeaderH, radiusM)).Border(1.5, k.busy.Alpha(0.6))
	}
	row.ContextMenu(func(m *ui.Menu) { a.hostMenu(m, h) })
	row.Children(func() {
		ui.Text(c, strings.ToUpper(name)).FontSize(fontCaption).FontWeight(700).TextColor(k.textFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		dot := ui.Box(c).Size(6, 6).Radius(capsule(6)).Shrink(0)
		switch {
		case !away:
			dot.Background(k.textFaint)
		case h.state == hostConnecting:
			dot.Border(1, k.textFaint)
		default:
			dot.Border(1, hostDot(k, h))
		}
		if h != nil {
			label, _, _ := hostStatus(h)
			dot.Role(ui.RoleImage).Label(label).Tooltip(label)
		}
		if away {
			ui.Text(c, locales.ProjectsOfflineLabel()).FontSize(fontCaption).TextColor(k.textFaint).SingleLine().Shrink(0)
		}
		ui.Spacer(c)
		if away {
			ui.Box(c).Size(20, 20).Shrink(0)
			return
		}
		label := locales.ProjectsNewOn(name)
		if iconButton(c, k, "plus", label, 20, 13).Tooltip(label).Clicked() {
			a.later(c, func() { a.openNewProjectOn(h) })
		}
	})
	if away {
		dimmed(row, 0.55)
	}
}

// hostMenu is a host's menu: Rename… (its alias, a remote host's) and New
// Tab with No Project.
func (a *App) hostMenu(m *ui.Menu, h *hostConn) {
	if h != nil && m.Item(locales.HostRename()).Chosen() {
		a.laterService(func() { a.renameHostAlias(h) })
	}
	if m.Item(cmdNewSession.Title()).Disabled(h != nil && h.current() == nil).Chosen() {
		a.laterService(func() { a.newNoProjectTab(h) })
	}
	if h != nil {
		m.Submenu(locales.HostsIcon(), func(m *ui.Menu) { a.hostIconMenu(m, h) })
	}
}

// renameHostAlias opens the field that renames host h's alias, on the
// Hosts page of Settings, which keeps it.
func (a *App) renameHostAlias(h *hostConn) {
	a.openSettingsAt("hosts")
	hostsUI.renaming, hostsUI.alias, hostsUI.aliasFocus = h.ID, h.label(), false
}

// newNoProjectTab opens a tab with no project on host h, at its home.
func (a *App) newNoProjectTab(h *hostConn) {
	dir := ""
	if h == nil {
		dir = a.homeOf(nil)
	}
	a.newTabOn(h, dir)
}

// projectRow is a project's row, or the host's tabs with no project's,
// flush with the host's header: a folder icon for a directory project
// (none for the tabs with no project), its name and its tabs' count; under
// the pointer and while it is shown, the + that opens a tab in it in place
// of the count. Its folder shows in its tooltip and, shown, in the title
// bar. A click shows it, and on the project shown opens or closes its
// tabs. A host's offline copy shows greyed, and opens nothing.
func (a *App) projectRow(c *ui.Context, k *colors, w *Workspace, reserve float32) {
	key := openKey(w.host, w.ID)
	shown := w == a.ws
	open := rowOpen(key, shown)
	name := w.Name
	if w.Dir == "" {
		name = locales.ProjectsNoProject()
	}
	n := w.tabCount()
	offline := w.offline()
	row := ui.Row(c.Key("project-"+key)).Height(projectRowH).Shrink(0).Padding(0, 4, 0, sideLead).Gap(sideGap).AlignItems(ui.Center).
		Radius(pillRadius(projectRowH, radiusM)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Expanded(open).Label(name + spacedComma(workspaceDetail(w, a, n)))
	if anim := motion(c); anim != nil {
		row.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion})
	}
	// One row is filled, as chosen: the active tab, under the project that
	// holds it, which shows as current by its weight and dot alone; the
	// project's own row while its tabs are closed. Hover is the lighter
	// face (DESIGN.md).
	switch {
	case shown && (!open || n == 0):
		glassPill(row, k, projectRowH)
	case row.Hovered() && !offline:
		row.Background(hoverFace(k))
	}
	// A tab of the window's dropped on a project of its host moves there,
	// with its panes and their processes.
	if dragged, ok := ui.Drop[*Tab](row); ok && a.canMoveTab(dragged, w) {
		a.later(c, func() { a.moveTabToSpace(dragged, w) })
	}
	if dragged, over := ui.DragOver[*Tab](row); over && a.canMoveTab(dragged, w) {
		row.Border(1.5, k.busy.Alpha(0.6))
	}
	if !offline {
		row.ContextMenu(func(m *ui.Menu) { a.projectMenu(m, w) })
	}
	hovered := row.Hovered()
	toggled := false
	row.Children(func() {
		// The icon's room opens and closes the tabs of any project, as a
		// click on the row of the one shown does. It is as wide as the
		// tabs' tiles: the folder starts where their tiles do, and the
		// name where their titles do.
		fold := leadRoom(c.Key("fold"), reserve, projectRowH).
			Role(ui.RoleDisclosure).Expanded(open).Label(locales.ProjectsTabsOf(name))
		toggled = fold.Clicked()
		if w.Dir != "" {
			fold.Children(func() { projectFolderIcon(c, k, w.Color) })
		}
		if shown && a.wsRenaming {
			a.workspaceRenameField(c, k, w.Name)
			return
		}
		weight := 500
		if shown {
			weight = 600
		}
		ui.Text(c, name).FontSize(fontBody).FontWeight(weight).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if s := a.st.status(w); statusShown(s) {
			statusDot(c, k, s, 7).Label(statusTip(s))
		}
		if !offline && (hovered || shown) {
			label := locales.ProjectsNewTabIn(name)
			if iconButton(c, k, "plus", label, 20, 13).Tooltip(tip(label, &cmdNewTab)).Clicked() {
				a.later(c, func() { a.newTabIn(w) })
			}
			return
		}
		ui.Text(c, fmt.Sprint(n)).FontSize(fontFootnote).TextColor(k.textFaint).Shrink(0).Padding(0, 6, 0, 0)
	})
	if offline {
		dimmed(row, 0.5)
		row.Tooltip(locales.ProjectsOffline(a.hostName(w.host)))
	}
	switch {
	case toggled:
		setRowOpen(key, !open)
	case row.Clicked() && !offline && !shown:
		// The click opens the project's tabs, and no other project's
		// closes.
		setRowOpen(key, true)
		a.later(c, func() { a.showWorkspace(w) })
	case row.Clicked() && shown:
		setRowOpen(key, !open)
	}
	if !open || n == 0 {
		return
	}
	// The tabs come under their project flush with it, not one step in.
	list := ui.Column(c.Key("project-tabs-" + key)).Gap(rowGap)
	if offline {
		dimmed(list, 0.5)
	}
	list.Children(func() {
		if shown {
			for i, t := range slices.Clone(a.tabs) {
				a.sidebarTab(c, k, i, t, reserve)
			}
			return
		}
		a.workspaceTabs(c, k, w, reserve, offline)
	})
}

// rowGap is the room between the sidebar's rows, so that two filled
// ones, a hovered and the chosen, never touch.
const rowGap = 4

// The rows of the sidebar under a host's header, its projects', Older's
// and their tabs', start at one leading edge: sideLead in, then the room
// of the tabs' tiles (tilesReserve), where a project's folder icon shows,
// then sideGap, then the name or title. So a project's folder is where its
// tabs' tiles start, and its name where their titles do.
const (
	sideLead = 9
	sideGap  = 6
)

// projectFolderIcon draws a project row's folder icon, tinted by color
// when set, matching iOS and web.
func projectFolderIcon(c *ui.Context, k *colors, color string) ui.Element {
	col := k.textMuted
	if hex, ok := spaceColorHex(color); ok {
		col = ui.Hex(hex)
	}
	return glyphIcon(c, "folder", 14, col).Shrink(0)
}

// leadRoom is the room before a row's name: as wide as the tabs' tiles,
// what it holds at its start.
func leadRoom(c *ui.Context, reserve, h float32) ui.Element {
	return ui.Row(c).Width(reserve).Height(h).AlignItems(ui.Center).Shrink(0)
}

// workspaceTabs lists the tabs of workspace w no window here shows, from
// the window that shows it, else its layout: a click shows w on that tab;
// none of an offline host's copy opens.
func (a *App) workspaceTabs(c *ui.Context, k *colors, w *Workspace, reserve float32, offline bool) {
	pick := func(i int) {
		if offline {
			return
		}
		a.later(c, func() { a.showWorkspaceTab(w, i) })
	}
	if o := w.app; o != nil {
		for i, t := range o.tabs {
			name, detail := t.label()
			if spaceTab(c, k, t.UID, name, detail, reserve, func() { a.tiles(c, k, t) }) {
				pick(i)
			}
		}
		return
	}
	// Each tab names itself from what its host's last session list says
	// of its session, as a window's tab does; no pane attaches for it.
	infos, _, _ := a.st.listOf(w.host)
	for i, t := range w.layout.Tabs {
		name, detail, prog := savedTabFace(t, infos)
		if spaceTab(c, k, cmp.Or(t.ID, fmt.Sprint(i)), name, detail, reserve, func() {
			ui.Box(c).Size(tileW+2, tileH+4).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
				drawTile(p, k, ui.Rect{X: r.X + 1, Y: r.Y + 2, W: tileW, H: tileH}, prog, true)
			})
		}) {
			pick(i)
		}
	}
}

// showWorkspaceTab shows w, here or in the window that shows it, on its
// ith tab.
func (a *App) showWorkspaceTab(w *Workspace, i int) {
	a.showWorkspace(w)
	o := w.app
	if o == nil {
		return
	}
	pick := func() {
		if i >= 0 && i < len(o.tabs) {
			o.selectTab(i)
		}
	}
	if o == a {
		pick()
	} else {
		o.do(pick)
	}
}

// foldRow is a row at the projects' level that opens and closes what is
// under it, as Older: its label and a count, and a chevron under the
// pointer. It reports whether it was clicked.
func (a *App) foldRow(c *ui.Context, k *colors, key, label, count string, open bool, reserve float32) bool {
	row := ui.Row(c.Key("fold-"+key)).Height(26).Shrink(0).Padding(0, 10, 0, sideLead).Gap(sideGap).AlignItems(ui.Center).
		Radius(pillRadius(26, radiusM)).Cursor(ui.CursorPointer).Role(ui.RoleDisclosure).Expanded(open).Label(label)
	hovered := row.Hovered()
	if hovered {
		row.Background(hoverFace(k))
	}
	row.Children(func() {
		leadRoom(c, reserve, 26).Children(func() {
			if !hovered {
				return
			}
			chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
			if !open {
				chevron.Rotate(-90)
			}
		})
		ui.Text(c, label).FontSize(fontCallout).TextColor(k.textMuted).SingleLine().Grow(1).MinWidth(0)
		ui.Text(c, count).FontSize(fontFootnote).TextColor(k.textFaint).Shrink(0)
	})
	return row.Clicked()
}

// waitingRow is a project, or tabs with no project, that an adoption
// keeps for host h until it connects (docs/projects.md, Migration): at the
// projects' level, greyed, with "Waiting for <host>".
func (a *App) waitingRow(c *ui.Context, k *colors, h *hostConn, ad layout.Adoption, reserve float32) {
	name, detail := locales.ProjectsNoProject(), ""
	if p := ad.Project; p != nil {
		name, detail = p.Name, a.tildeOn(h, p.Dir)
	}
	note := locales.ProjectsWaitingFor(a.hostName(h))
	tipText := note
	if detail != "" {
		tipText = detail + " · " + note
	}
	row := ui.Row(c.Key("waiting-"+ad.ID)).Height(projectRowH).Shrink(0).Padding(0, 10, 0, sideLead).Gap(sideGap).AlignItems(ui.Center).
		Role(ui.RoleText).Label(name + ", " + note).Tooltip(tipText)
	row.Children(func() {
		leadRoom(c, reserve, projectRowH).Children(func() {
			if ad.Project != nil {
				projectFolderIcon(c, k, "")
			}
		})
		ui.Text(c, name).FontSize(fontBody).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		ui.Text(c, note).FontSize(fontCaption).TextColor(k.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Padding(0, 0, 0, 6)
	})
	dimmed(row, 0.5)
}

// projectMenu is a project's menu (docs/projects.md): New Tab, Rename…,
// Color, Move to Space, Reveal Folder (this machine's), Close Project….
func (a *App) projectMenu(m *ui.Menu, w *Workspace) {
	if m.Item(locales.CmdNewTab()).Chosen() {
		a.laterService(func() { a.newTabIn(w) })
	}
	if w.Dir == "" {
		return
	}
	m.Separator()
	if m.Item(cmdRenameSession.Title()).Chosen() {
		a.laterService(func() {
			a.showWorkspace(w)
			if a.ws == w {
				a.startWorkspaceRename()
			}
		})
	}
	m.Submenu(locales.SpaceColor(), func(sm *ui.Menu) {
		if sm.Item(locales.SpaceColorDefault()).Chosen() {
			a.laterService(func() { a.setProjectColor(w, "") })
		}
		for _, p := range layout.Palette {
			if sm.Item(paletteLabel(p.ID)).Chosen() {
				a.laterService(func() { a.setProjectColor(w, p.ID) })
			}
		}
	})
	m.Submenu(locales.ProjectMoveToSpace(), func(sm *ui.Menu) {
		for _, n := range a.st.spaceNames() {
			if n != w.Space && sm.Item(spaceLabel(n)).Chosen() {
				a.laterService(func() { a.moveProjectToSpace(w, n) })
			}
		}
	})
	if w.host == nil && m.Item(cmdRevealFolder.Title()).Chosen() {
		a.revealProject(w)
	}
	m.Separator()
	if m.Item(locales.ProjectCloseAsk()).Chosen() {
		a.laterService(func() {
			a.showWorkspace(w)
			if a.ws == w {
				a.askCloseWorkspace()
			}
		})
	}
}

// paletteLabel is the name of a preset color, as menus list it.
func paletteLabel(id string) string {
	if id == "" {
		return ""
	}
	return string(id[0]-'a'+'A') + id[1:]
}

// setProjectColor gives project w a color, which its host keeps.
func (a *App) setProjectColor(w *Workspace, color string) {
	if w.Color == color || w.offline() || a.st == nil {
		return
	}
	w.Color = color
	a.st.save()
}

// revealProject shows the folder of project w, this machine's, in the
// system's file manager.
func (a *App) revealProject(w *Workspace) {
	if w == nil || w.host != nil || w.Dir == "" {
		return
	}
	a.services.OpenURL(fileURL(w.Dir))
}

// newTabIn opens a tab in workspace w, shown then: in its folder for a
// project (New Tab of its row, and ⌘T while it is shown).
func (a *App) newTabIn(w *Workspace) {
	if w == nil || w.offline() {
		return
	}
	if w != a.ws {
		a.showWorkspace(w)
		if w.app != a {
			return
		}
	}
	a.newProjectTab()
}

// canMoveTab tells whether tab t of the window may move to workspace w:
// another workspace of its host (docs/projects.md: a tab moves between
// workspaces of one host, with its panes).
func (a *App) canMoveTab(t *Tab, w *Workspace) bool {
	return t != nil && w != nil && w != a.ws && a.ws != nil && w.host == a.ws.host && !w.offline() && serverMade(w.host) && slices.Contains(a.tabs, t)
}

// spacedComma is ", " and d, or "" for none.
func spacedComma(d string) string {
	if d == "" {
		return ""
	}
	return ", " + d
}
