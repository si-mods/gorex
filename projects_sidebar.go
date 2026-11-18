package main

import (
	"cmp"
	"fmt"
	"slices"
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

// projectsList is the sidebar's list: each host shown, its projects and
// its tabs with no project.
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
		if a.sidebarHostRow(c, k, h) {
			continue // folded
		}
		ui.Column(c.Key("host-body-"+hostKey(h))).Gap(2).Padding(0, 0, 6, 8).Children(func() {
			a.projectsHeader(c, k, h)
			recent, older := a.projectsOf(h, space)
			for _, w := range recent {
				a.projectRow(c, k, w, reserve)
			}
			if len(older) > 0 {
				key := openKey(h, "older")
				open := rowOpen(key, false)
				if a.foldRow(c, k, key, locales.ProjectsOlder(), fmt.Sprint(len(older)), open, nil) {
					setRowOpen(key, !open)
				}
				if open {
					for _, w := range older {
						a.projectRow(c, k, w, reserve)
					}
				}
			}
			for _, ad := range a.st.waitingOn(h) {
				a.waitingRow(c, k, h, ad)
			}
			if w := a.st.noProjectOf(h); w != nil {
				a.projectRow(c, k, w, reserve)
			}
		})
	}
}

// sidebarHostRow is a host's row: its machine's icon, its name, its state, and a
// chevron that folds it; its menu renames it (its alias) and opens a tab
// with no project there. It reports whether the host is folded.
func (a *App) sidebarHostRow(c *ui.Context, k *colors, h *hostConn) bool {
	name, key := a.hostName(h), hostKey(h)
	folded := slices.Contains(prefs.FoldedHosts, key)
	row := ui.Row(c.Key("host-"+key)).Height(projectRowH).Shrink(0).Padding(6, 4, 0, 6).Gap(7).AlignItems(ui.Center).
		Radius(pillRadius(projectRowH, 9)).Cursor(ui.CursorPointer).Role(ui.RoleDisclosure).Expanded(!folded).Label(name)
	if anim := motion(c); anim != nil {
		row.Transition(ui.ElementTransition{Animation: anim, Position: true})
	}
	// A tab of another host's dropped on it asks to teleport there.
	if dragged, ok := ui.Drop[*Tab](row); ok && teleportsTo(dragged, h) {
		a.askTeleport(dragged, h)
	}
	if dragged, over := ui.DragOver[*Tab](row); over && teleportsTo(dragged, h) {
		row.Border(1.5, k.busy.Alpha(0.6))
	}
	row.ContextMenu(func(m *ui.Menu) { a.hostMenu(m, h) })
	if row.Clicked() {
		if folded {
			prefs.FoldedHosts = slices.DeleteFunc(prefs.FoldedHosts, func(s string) bool { return s == key })
		} else {
			prefs.FoldedHosts = append(prefs.FoldedHosts, key)
		}
		folded = !folded
		saveSettings()
	}
	if h != nil && h.current() == nil && h.state != hostConnecting {
		dimmed(row, 0.55)
	}
	row.Children(func() {
		a.hostSectionIcon(c, k, h)
		ui.Text(c, name).FontSize(13).FontWeight(600).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if h != nil && h.current() == nil {
			label, _, _ := hostStatus(h)
			ui.Text(c, label).FontSize(11).TextColor(k.textFaint).SingleLine().Shrink(0)
		}
		chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
		turn := float32(0)
		if folded {
			turn = -90
		}
		chevron.Rotate(animated(c, chevron, "fold", turn))
	})
	return folded
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

// projectsHeader is PROJECTS over a host's projects, with the + that makes
// one there (the folder sheet on that host).
func (a *App) projectsHeader(c *ui.Context, k *colors, h *hostConn) {
	ui.Row(c.Key("projects-"+hostKey(h))).Height(22).Shrink(0).Padding(0, 4, 0, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.ProjectsHeader()).FontSize(10.5).FontWeight(700).TextColor(k.textFaint).Grow(1).SingleLine()
		if h != nil && h.current() == nil {
			return
		}
		label := locales.ProjectsNewOn(a.hostName(h))
		if iconButton(c, k, "plus", label, 20, 13).Tooltip(label).Clicked() {
			a.later(c, func() { a.openNewProjectOn(h) })
		}
	})
}

// newNoProjectTab opens a tab with no project on host h, at its home.
func (a *App) newNoProjectTab(h *hostConn) {
	dir := ""
	if h == nil {
		dir = a.homeOf(nil)
	}
	a.newTabOn(h, dir)
}

// projectRow is a project's row, or the tabs with no project's: a
// chevron, its folder's icon and its name, its folder, its tabs' count
// and what needs the user there; under the pointer and when shown, the +
// that opens a tab in it. A click shows it, the chevron opens it on its
// tabs. A host's offline copy shows greyed, and does nothing.
func (a *App) projectRow(c *ui.Context, k *colors, w *Workspace, reserve float32) {
	key := openKey(w.host, w.ID)
	shown := w == a.ws
	open := rowOpen(key, shown)
	name, glyph := w.Name, "folder"
	if w.Dir == "" {
		name, glyph = locales.ProjectsNoProject(), "layers"
	}
	n := w.tabCount()
	offline := w.offline()
	row := ui.Row(c.Key("project-"+key)).Height(projectRowH).Shrink(0).Padding(0, 4, 0, 0).Gap(6).AlignItems(ui.Center).
		Radius(pillRadius(projectRowH, 9)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Selected(shown).Label(name + spacedComma(a.projectDetail(w)))
	if anim := motion(c); anim != nil {
		row.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion})
	}
	switch {
	case shown && prefs.TabPlacement == "":
		glassPill(row, k, projectRowH)
	case shown:
		row.Background(hoverFace(k))
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
		fold := ui.Row(c.Key("fold")).Height(projectRowH).Padding(0, 0, 0, 6).AlignItems(ui.Center).Shrink(0).
			Role(ui.RoleDisclosure).Expanded(open).Label(locales.ProjectsTabsOf(name))
		toggled = fold.Clicked()
		fold.Children(func() {
			chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
			turn := float32(0)
			if !open {
				turn = -90
			}
			chevron.Rotate(animated(c, chevron, "fold", turn))
		})
		if hex, ok := spaceColorHex(w.Color); ok {
			glyphIcon(c, glyph, 14, ui.Hex(hex)).Shrink(0)
		} else {
			glyphIcon(c, glyph, 14, k.textMuted).Shrink(0)
		}
		if shown && a.wsRenaming {
			a.workspaceRenameField(c, k, w.Name)
			return
		}
		ui.Text(c, name).FontSize(13).FontWeight(500).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Shrink(0).MaxWidth(150)
		ui.Text(c, a.projectDetail(w)).FontSize(11).TextColor(k.textFaint).SingleLine().Ellipsis("…").TruncateMiddle().Grow(1).MinWidth(0)
		if s := a.st.status(w); needsUser(s) {
			statusDot(c, k, s, 7).Label(statusTip(s))
		}
		if !offline && (hovered || shown) {
			label := locales.ProjectsNewTabIn(name)
			if iconButton(c, k, "plus", label, 20, 13).Tooltip(tip(label, &cmdNewTab)).Clicked() {
				a.later(c, func() { a.newTabIn(w) })
			}
			return
		}
		ui.Text(c, fmt.Sprint(n)).FontSize(11.5).TextColor(k.textFaint).Shrink(0)
	})
	if offline {
		dimmed(row, 0.5)
		row.Tooltip(locales.ProjectsOffline(a.hostName(w.host)))
	}
	switch {
	case toggled:
		setRowOpen(key, !open)
	case row.Clicked() && !offline && !shown:
		a.later(c, func() { a.showWorkspace(w) })
	case row.Clicked() && shown && !open:
		setRowOpen(key, true)
	}
	if !open {
		return
	}
	list := ui.Column(c.Key("project-tabs-"+key)).Gap(2).Padding(0, 0, 0, 14)
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
	for i, t := range w.layout.Tabs {
		name, prog := savedTabLabel(t)
		if spaceTab(c, k, cmp.Or(t.ID, fmt.Sprint(i)), name, "", reserve, func() {
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

// foldRow is a row that opens and closes what is under it, as Older: a
// chevron, its label and a count. It reports whether it was clicked.
func (a *App) foldRow(c *ui.Context, k *colors, key, label, count string, open bool, mark func()) bool {
	row := ui.Row(c.Key("fold-"+key)).Height(26).Shrink(0).Padding(0, 8, 0, 6).Gap(6).AlignItems(ui.Center).
		Radius(pillRadius(26, 9)).Cursor(ui.CursorPointer).Role(ui.RoleDisclosure).Expanded(open).Label(label)
	if row.Hovered() {
		row.Background(hoverFace(k))
	}
	row.Children(func() {
		chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
		turn := float32(0)
		if !open {
			turn = -90
		}
		chevron.Rotate(animated(c, chevron, "fold", turn))
		if mark != nil {
			mark()
		}
		ui.Text(c, label).FontSize(12).TextColor(k.textMuted).SingleLine().Grow(1).MinWidth(0)
		ui.Text(c, count).FontSize(11.5).TextColor(k.textFaint).Shrink(0)
	})
	return row.Clicked()
}

// waitingRow is a project, or tabs with no project, that an adoption
// keeps for host h until it connects (docs/projects.md, Migration):
// greyed, with "Waiting for <host>".
func (a *App) waitingRow(c *ui.Context, k *colors, h *hostConn, ad layout.Adoption) {
	name, detail := locales.ProjectsNoProject(), ""
	if p := ad.Project; p != nil {
		name, detail = p.Name, a.tildeOn(h, p.Dir)
	}
	note := locales.ProjectsWaitingFor(a.hostName(h))
	row := ui.Row(c.Key("waiting-"+ad.ID)).Height(projectRowH).Shrink(0).Padding(0, 8, 0, 23).Gap(6).AlignItems(ui.Center).
		Role(ui.RoleText).Label(name + ", " + note).Tooltip(note)
	row.Children(func() {
		glyphIcon(c, "folder", 14, k.textMuted).Shrink(0)
		ui.Text(c, name).FontSize(13).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Shrink(0).MaxWidth(130)
		ui.Text(c, cmp.Or(detail, note)).FontSize(11).TextColor(k.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
	dimmed(row, 0.5)
	ui.Text(c.Key("waiting-note-"+ad.ID), note).FontSize(10.5).TextColor(k.textFaint).Padding(0, 8, 2, 43).SingleLine().Ellipsis("…")
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
	return t != nil && w != nil && w != a.ws && a.ws != nil && w.host == a.ws.host && !w.offline() && slices.Contains(a.tabs, t)
}

// spacedComma is ", " and d, or "" for none.
func spacedComma(d string) string {
	if d == "" {
		return ""
	}
	return ", " + d
}
