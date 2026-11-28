package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// The sidebar of projects (docs/MOD-002-projects.md, "Sidebar"): the space picker
// atop, then each host the user added, with its PROJECTS of the space
// shown, in order of last use, those older than olderAfter under a folded
// Older, and its tabs with no project, which every space shows. A project
// expands to its tabs; the window's project lists the window's own tabs,
// which are chosen, dragged and renamed as tabs are.

// olderAfter is how long a project goes unused before it goes under
// Older.
const olderAfter = 30 * 24 * time.Hour

// projectRowH() is the height of a project's row: a tab's, so that a
// project and its tabs are rows of one rhythm, and their pills, and the
// current project's face, share one radius.
func projectRowH() float32 { return sidebarRowH() }

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

// projectsList is the sidebar's list (docs/MOD-002-projects.md, "Sidebar"): each
// host shown, as a section header, then its projects of the space, flush
// with it, those not used for olderAfter under Older, the adoptions that
// wait for it, and its tabs with no project, as a group like a project.
func (a *App) projectsList(c *ui.Context, k *colors) {
	if a.st == nil {
		tabs := slices.Clone(a.tabs)
		reserve := tilesReserve(tabs)
		for i, t := range tabs {
			a.sidebarTab(c, k, i, t, reserve, false)
		}
		return
	}
	space := shownSpace()
	reserve := tilesReserve(a.tabs)
	for _, h := range a.allHosts() {
		if !a.hostShown(h, space) {
			continue
		}
		items := func() {
			recent, older := a.projectsOf(h, space)
			np := a.st.noProjectOf(h)
			npShown := np != nil && (np.tabCount() > 0 || np == a.ws)
			for _, w := range recent {
				a.projectGroup(c, k, w, reserve)
			}
			if len(older) > 0 {
				key := openKey(h, "older")
				open := rowOpen(key, false)
				if a.foldRow(c, k, key, locales.ProjectsOlder(), fmt.Sprint(len(older)), open, reserve) {
					setRowOpen(key, !open)
				}
				if open {
					for _, w := range older {
						a.projectGroup(c, k, w, reserve)
					}
				}
			}
			for _, ad := range a.st.waitingOn(h) {
				a.waitingRow(c, k, h, ad, reserve)
			}
			// Tabs with no project shows while it has a tab, or the
			// window shows it.
			if npShown {
				a.projectGroup(c, k, np, reserve)
			}
		}
		// sidebar.groupBy "flat" lists every host's items without the
		// host's section header and body (docs/DSK-007-sidebar-style.md).
		if sidebarGrouping() == "flat" {
			items()
			continue
		}
		ui.Column(c.Key("host-body-"+hostKey(h))).Gap(rowGap).Padding(0, 0, 10, 0).Children(func() {
			a.hostHeader(c, k, h)
			items()
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
		row.Radius(pillRadius(k, hostHeaderH, k.radius(radiusM))).Border(1.5, k.busy.Alpha(0.6))
	}
	row.ContextMenu(func(m *ui.Menu) { a.hostMenu(m, h) })
	row.Children(func() {
		ui.Text(c, strings.ToUpper(name)).FontSize(sidebarFont(fontCaption)).FontWeight(700).TextColor(k.textFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		dot := ui.Box(c).Size(6, 6).Radius(k.capsule(6)).Shrink(0)
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
			ui.Text(c, locales.ProjectsOfflineLabel()).FontSize(sidebarFont(fontCaption)).TextColor(k.textFaint).SingleLine().Shrink(0)
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

// projectRow is a project's row, and the header of a host's No Project
// group (named so, open at first), flush with the host's header: its folder
// tile, its name and the chevron of its state (none with no tab); under the
// pointer and while it is shown, the + that opens a tab in it, after the
// chevron. Its folder shows in the title bar while it is shown. A click
// shows it, and on the project shown opens or closes its tabs. A host's
// offline copy shows greyed, and opens nothing.
func (a *App) projectRow(c *ui.Context, k *colors, w *Workspace, reserve float32) {
	key := openKey(w.host, w.ID)
	shown := w == a.ws
	open := rowOpen(key, shown || w.Dir == "")
	name := w.Name
	if w.Dir == "" {
		name = locales.ProjectsNoProject()
	}
	n := w.tabCount()
	offline := w.offline()
	row := ui.Row(c.Key("project-"+key)).Height(projectRowH()).Shrink(0).Padding(0, 4, 0, sidebarLead()).Gap(sideGap).AlignItems(ui.Center).
		Radius(pillRadius(k, projectRowH(), k.radius(radiusM))).Cursor(ui.CursorPointer).Role(ui.RoleButton).Expanded(open).Label(name + spacedComma(workspaceDetail(w, a, n)))
	if anim := motion(c); anim != nil {
		row.Transition(exitIn(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion}, durExitMove))
	}
	// One row is filled, as chosen: the active tab, under the project that
	// holds it, which shows as current by its weight and dot alone; the
	// project's own row while its tabs are closed. Hover is the lighter
	// face (DESIGN.md).
	ck := k // the palette of the row's content, paired with its face
	switch {
	case shown && (!open || n == 0):
		sidebarChosen(row, k, projectRowH(), false)
		ck = sidebarChosenColors(k)
	case row.Hovered() && !offline:
		paintFace(row, k, roleTabHoverBackground, hoverFace(k))
		ck = sidebarHoverColors(k)
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
		// The project's folder tile stands over its tabs' tiles, and its name
		// over their titles.
		leadRoom(c, reserve, projectRowH()).Children(func() { projectFolderTile(c, ck, w.Color) })
		renaming := shown && a.wsRenaming && w.Dir != ""
		if renaming {
			a.workspaceRenameField(c, ck, w.Name)
		} else {
			weight := 500
			if shown {
				weight = 600
			}
			ui.Text(c, name).FontSize(sidebarFont(fontBody)).FontWeight(weight).TextColor(ck.text.Alpha(0.85)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			if s := a.st.status(w); needsUser(s) {
				statusDot(c, ck, s, 7).Label(statusTip(s))
			}
		}
		// The chevron, at the end of the row before the +, opens and closes
		// the tabs of any project, as a click on the row of the one shown
		// does. A project with no tab has none: there is nothing to open.
		if n > 0 {
			fold := disclosureChevron(c.Key("fold"), ck, open, projectRowH()).
				Cursor(ui.CursorPointer).Role(ui.RoleDisclosure).Expanded(open).Label(locales.ProjectsTabsOf(name))
			toggled = fold.Clicked()
		}
		if renaming {
			return
		}
		if !offline && (hovered || shown) {
			label := locales.ProjectsNewTabIn(name)
			if iconButton(c, ck, "plus", label, 20, 13).Tooltip(tip(label, &cmdNewTab)).Clicked() {
				a.later(c, func() { a.newTabIn(w) })
			}
		}
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
	// The tabs come under their project with their tiles where its folder
	// is, not one step in. The list folds open and shut as a whole, as a
	// tab does: its rows' own motion runs only in a list that stays.
	list := ui.Column(c.Key("project-tabs-" + key)).Gap(rowGap)
	if anim := motion(c); anim != nil {
		list.Transition(exitIn(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion}, durExitMove))
	}
	if offline {
		dimmed(list, 0.5)
	}
	list.Children(func() {
		if shown {
			for i, t := range slices.Clone(a.tabs) {
				a.sidebarTab(c, k, i, t, reserve, true)
			}
		} else {
			a.workspaceTabs(c, k, w, reserve, offline)
		}
		// The New Tab row only under the shown project's tabs; the others'
		// new tabs come from the + of their rows.
		if shown && !offline && w.Dir != "" {
			if agents := a.agentsOf(w.host); len(agents) > 0 {
				a.projectNewTabRow(c, k, w, agents, reserve)
			}
		}
	})
}

// projectGroup draws project w's row and its tabs. The project shown with
// its tabs open has a face of its own behind them, its active tab's pill
// lifted over it (liftedPill): it tells where the window is, and which
// tabs are that project's. The face reaches groupPad past the rows on every
// side, so nothing in it moves as it comes and goes, and its corners are the pills' plus that
// pad, so that both share one centre. With Animation off the face comes and
// goes at once.
func (a *App) projectGroup(c *ui.Context, k *colors, w *Workspace, reserve float32) {
	// Every project has the room of the face, so that choosing another moves
	// nothing: only the face's color comes and goes, fading.
	g := ui.Column(c.Key("group-"+openKey(w.host, w.ID))).Gap(rowGap).Radius(pillRadius(k, sidebarRowH(), k.radius(radiusM))+sidebarGroupPad()).Padding(sidebarGroupPad()).Margin(-sidebarGroupEdge(), -sidebarGroupPad(), -sidebarGroupEdge(), -sidebarGroupPad())
	if w == a.ws && rowOpen(openKey(w.host, w.ID), true) && w.tabCount() > 0 && sidebarGroupFaced() {
		paintFace(g, k, roleActiveGroupBackground, k.groupFace)
		k = surface(k, roleActiveGroupBackground).on(k) // what the group holds reads on its face
	} else {
		g.Background(k.groupFace.Alpha(0))
	}
	// With Animation off the face changes at once.
	if anim := motion(c); anim != nil {
		g.Transition(ui.ElementTransition{Animation: anim, Colors: true})
	}
	g.Children(func() { a.projectRow(c, k, w, reserve) })
}

// groupPad is the room between the current project's face and its rows,
// the user's sidebar.groupPadding over it (sidebarGroupPad); groupEdge the
// face's reach above and below them in the layout, short of the pad by
// enough that groupGap stays between the face and the rows of the projects
// next to it (sidebarGroupEdge).
const (
	groupPad = 4
	groupGap = 2
)

// rowGap is the room between the sidebar's rows, so that two filled
// ones, a hovered and the chosen, never touch.
const rowGap = 4

// The rows of the sidebar under a host's header, its projects', Older's
// and their tabs', start at one leading edge: sideLead in, then the room
// of the tabs' tiles (tilesReserve), where a project's folder tile shows,
// then sideGap, then the name or title. So a project's folder tile is over
// its tabs' tiles, and its name over their titles, open or closed. The
// user's sidebar.tabIndent shifts the tab-level rows (sidebarTabLead)
// right of the edge, so a project's tabs read under it.
const (
	sideLead = 9
	sideGap  = 6
)

// folderTileAlpha is the strength of the neutral face of a project's folder
// tile; chevronRoom the width of a disclosure chevron's room; foldRowH the
// height of a fold row, as Older.
const (
	folderTileAlpha = 0.09
	chevronRoom     = 16
	foldRowH        = 26
)

// projectFolderTile is a project's mark: a tile of a tab's size and corners
// (drawTile), in a neutral face, with a folder in it in the project's color
// (muted with none), so that a project and its tabs are marks of one kind,
// told apart by the folder and the colors of the programs.
func projectFolderTile(c *ui.Context, k *colors, color string) {
	col := k.textMuted
	if hex, ok := spaceColorHex(color); ok {
		col = ui.Hex(hex)
	}
	ui.Row(c).Width(tileSlot).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
		// sidebar.icons "text" draws the glyph alone, no tile face.
		if sidebarIconsText() {
			glyphIcon(c, "folder", 11, col).Shrink(0)
			return
		}
		ui.Row(c).Size(tileW, tileH).Radius(k.radius(radiusTile)).Background(k.text.Alpha(folderTileAlpha)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			glyphIcon(c, "folder", 11, col).Shrink(0)
		})
	})
}

// disclosureChevron is the chevron of a row that opens and closes what is
// under it, near the row's end: down while open, right while closed,
// turning with the sidebar's motion. h is the row's height.
func disclosureChevron(c *ui.Context, k *colors, open bool, h float32) ui.Element {
	room := ui.Row(c).Width(chevronRoom).Height(h).AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	room.Children(func() {
		chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
		turn := float32(-90)
		if open {
			turn = 0
		}
		chevron.Rotate(animated(c, chevron, "fold", turn))
	})
	return room
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
		now := c.Now()
		for i, t := range o.tabs {
			name, detail := t.label()
			ag, fade, rem, _ := t.agentBadgeAt(now)
			if spaceTabWithAgent(c, k, t.UID, name, detail, ag, fade, rem, t.status(), reserve, func() { a.tiles(c, k, t) }) {
				pick(i)
			}
		}
		return
	}
	// Each tab names itself from what its host's last session list says
	// of its session, as a window's tab does; no pane attaches for it.
	infos, id, _ := a.st.listOf(w.host)
	now := c.Now()
	for i, t := range w.layout.Tabs {
		name, detail, prog := savedTabFace(t, infos)
		s := a.st.savedTabStatus(t, id, infos)
		ag, fade, rem, _ := savedTabAgentBadgeAt(now, t, infos)
		if spaceTabWithAgent(c, k, cmp.Or(t.ID, fmt.Sprint(i)), name, detail, ag, fade, rem, s, reserve, func() {
			ui.Box(c).Size(tileSlot, tileH+4).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
				fr := ui.Rect{X: r.X + 1, Y: r.Y + 2, W: tileW, H: tileH}
				drawTile(p, k, fr, prog, true)
				if s != nil && s.State == "done" {
					drawDoneDot(p, fr, k)
				}
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
// under it, as Older: its chevron, its label and a count. It reports
// whether it was clicked.
func (a *App) foldRow(c *ui.Context, k *colors, key, label, count string, open bool, reserve float32) bool {
	row := ui.Row(c.Key("fold-"+key)).Height(foldRowH).Shrink(0).Padding(0, 10, 0, sidebarLead()).Gap(sideGap).AlignItems(ui.Center).
		Radius(pillRadius(k, foldRowH, k.radius(radiusM))).Cursor(ui.CursorPointer).Role(ui.RoleDisclosure).Expanded(open).Label(label)
	hovered := row.Hovered()
	if hovered {
		paintFace(row, k, roleTabHoverBackground, hoverFace(k))
		k = sidebarHoverColors(k)
	}
	row.Children(func() {
		leadRoom(c, reserve, foldRowH)
		ui.Text(c, label).FontSize(sidebarFont(fontCallout)).TextColor(k.textMuted).SingleLine().Grow(1).MinWidth(0)
		disclosureChevron(c, k, open, foldRowH)
		ui.Text(c, count).FontSize(sidebarFont(fontFootnote)).TextColor(k.textFaint).Shrink(0)
	})
	return row.Clicked()
}

// waitingRow is a project, or No Project, that an adoption keeps for host h
// until it connects (docs/MOD-002-projects.md, Migration): at the projects'
// level, greyed, with a project's folder tile and name, and "Waiting for
// <host>".
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
	row := ui.Row(c.Key("waiting-"+ad.ID)).Height(projectRowH()).Shrink(0).Padding(0, 10, 0, sidebarLead()).Gap(sideGap).AlignItems(ui.Center).
		Role(ui.RoleText).Label(name + ", " + note).Tooltip(tipText)
	row.Children(func() {
		// A project or No Project, the folder tile and name of a project's row.
		leadRoom(c, reserve, projectRowH()).Children(func() { projectFolderTile(c, k, "") })
		ui.Text(c, name).FontSize(sidebarFont(fontBody)).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		ui.Text(c, note).FontSize(sidebarFont(fontCaption)).TextColor(k.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Padding(0, 0, 0, 6)
	})
	dimmed(row, 0.5)
}

// projectMenu is a project's menu (docs/MOD-002-projects.md): New Tab, Rename…,
// Color, Move to Space, Reveal Folder (this machine's), Close Project….
func (a *App) projectMenu(m *ui.Menu, w *Workspace) {
	if m.Item(locales.CmdNewTab()).Disabled(w.offline()).Chosen() {
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
		for _, ch := range colorChoices(w.Color) {
			if sm.Item(ch.label).Icon(colorDot(ch.id, ch.checked)).Chosen() {
				a.laterService(func() { a.setProjectColor(w, ch.id) })
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
	if agents := a.agentsOf(w.host); len(agents) > 0 {
		m.Submenu(locales.ProjectQuickLaunchers(), func(sm *ui.Menu) { a.projectLaunchersMenu(sm, w, agents) })
	}
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

// cloneStringSlicePtr returns a deep copy of a *[]string (preserving nil vs empty).
func cloneStringSlicePtr(p *[]string) *[]string {
	if p == nil {
		return nil
	}
	cp := slices.Clone(*p)
	if cp == nil {
		cp = []string{}
	}
	return &cp
}

// agentsOf returns the installed agent CLIs detected on host h (nil for
// this machine).
func (a *App) agentsOf(h *hostConn) []rex.AgentInfo {
	if h != nil {
		return h.agents
	}
	if a.st != nil {
		return a.st.agents
	}
	return nil
}

// pinnedAgents returns the host's installed agent CLIs filtered by project
// w's pinned launchers (nil means all detected; empty means none).
func (a *App) pinnedAgents(w *Workspace) []rex.AgentInfo {
	all := a.agentsOf(w.host)
	if w.Launchers == nil {
		return all
	}
	out := make([]rex.AgentInfo, 0, len(all))
	for _, ag := range all {
		if slices.Contains(*w.Launchers, ag.ID) {
			out = append(out, ag)
		}
	}
	return out
}

// setProjectLaunchers updates project w's pinned quick launchers and saves.
func (a *App) setProjectLaunchers(w *Workspace, launchers *[]string) {
	if w == nil || w.offline() || a.st == nil {
		return
	}
	w.Launchers = cloneStringSlicePtr(launchers)
	a.st.save()
}

// toggleProjectLauncher toggles agent id in project w's pinned quick launchers.
func (a *App) toggleProjectLauncher(w *Workspace, id string) {
	all := a.agentsOf(w.host)
	var ids []string
	if w.Launchers == nil {
		for _, ag := range all {
			if ag.ID != id {
				ids = append(ids, ag.ID)
			}
		}
	} else {
		cur := *w.Launchers
		if slices.Contains(cur, id) {
			for _, x := range cur {
				if x != id {
					ids = append(ids, x)
				}
			}
		} else {
			for _, ag := range all {
				if ag.ID == id || slices.Contains(cur, ag.ID) {
					ids = append(ids, ag.ID)
				}
			}
		}
	}
	allIDs := make([]string, len(all))
	for i, ag := range all {
		allIDs[i] = ag.ID
	}
	if slices.Equal(ids, allIDs) {
		a.setProjectLaunchers(w, nil)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	a.setProjectLaunchers(w, &ids)
}

// projectLaunchersMenu fills m with a project's pin toggles for its agent
// launchers (docs/MOD-010-agent-launchers.md): each installed agent CLI
// checked while the project pins it, and Show All Installed while a pick
// is kept. The project menu's Quick Launchers submenu and the New Tab
// row's menu share it.
func (a *App) projectLaunchersMenu(m *ui.Menu, w *Workspace, agents []rex.AgentInfo) {
	for _, ag := range agents {
		agent := ag
		pinned := w.Launchers == nil || slices.Contains(*w.Launchers, agent.ID)
		if m.Item(agent.Name).Checked(pinned).Chosen() {
			a.laterService(func() { a.toggleProjectLauncher(w, agent.ID) })
		}
	}
	if w.Launchers != nil {
		m.Separator()
		if m.Item(locales.ProjectLaunchersShowAll()).Chosen() {
			a.laterService(func() { a.setProjectLaunchers(w, nil) })
		}
	}
}

// projectAgentMenu is the menu of a New Tab row's chevron (docs/MOD-010):
// every installed agent CLI with its brand mark, checked while the project
// pins it — chosen, it opens a tab that runs it — then the project's pin
// toggles, the project menu's own.
func (a *App) projectAgentMenu(c *ui.Context, m *ui.Menu, w *Workspace, agents []rex.AgentInfo) {
	for _, ag := range agents {
		agent := ag
		pinned := w.Launchers == nil || slices.Contains(*w.Launchers, agent.ID)
		if m.Item(agent.Name).Icon(menuIcon(programOf(agent.ID).Glyph)).Checked(pinned).Chosen() {
			a.later(c, func() { a.newTabInWithCommand(w, agent.Command) })
		}
	}
	m.Separator()
	m.Submenu(locales.ProjectQuickLaunchers(), func(sm *ui.Menu) { a.projectLaunchersMenu(sm, w, agents) })
}

// projectNewTabRow is the New Tab row under an open project's tabs, on a
// host with agent CLIs (docs/MOD-010-agent-launchers.md): the + where the
// tabs' tiles stand and the words where their titles are open a plain
// shell tab in the project's folder. At the row's end a cluster holds the
// first three of the project's pinned agents as monochrome brand glyphs at
// the +'s and chevron's weight — the tiles' colors stay the tabs' — each
// opening a tab that runs it, and the chevron of the menu of every
// installed one. With none pinned the cluster is the chevron alone.
func (a *App) projectNewTabRow(c *ui.Context, k *colors, w *Workspace, agents []rex.AgentInfo, reserve float32) {
	row := ui.Row(c.Key("new-tab-"+openKey(w.host, w.ID))).Height(sidebarRowH()).Shrink(0).Padding(0, 4, 0, 0).Gap(sideGap).AlignItems(ui.Center)
	row.Children(func() {
		// The shell's part starts at the row's leading edge so its hover
		// capsule lines up with the tabs above and stays concentric with
		// the project group, while the cluster's buttons keep their own
		// hovers at its end.
		label := locales.ProjectsNewTabIn(w.Name)
		add := ui.Row(c.Key("add")).Grow(1).MinWidth(0).Height(sidebarRowH()).Padding(0, 6, 0, sidebarTabLead()).Gap(sideGap).AlignItems(ui.Center).
			Radius(pillRadius(k, sidebarRowH(), k.radius(radiusM))).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label)
		hovered := add.Hovered()
		ck := k
		if hovered {
			paintFace(add, k, roleTabHoverBackground, hoverFace(k))
			ck = sidebarHoverColors(k)
		}
		if fadeColorsEnabled() {
			add.Transition(hoverFade)
		}
		col := ck.textMuted
		if hovered {
			col = ck.text
		}
		add.Children(func() {
			leadRoom(c, reserve, sidebarRowH()).Children(func() {
				ui.Row(c).Width(tileSlot).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					glyphIcon(c, "plus", 13, col).Shrink(0)
				})
			})
			ui.Text(c, locales.CmdNewTab()).FontSize(sidebarFont(fontBody)).TextColor(col).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		})
		if add.Clicked() {
			a.later(c, func() { a.newTabIn(w) })
		}
		ui.Row(c.Key("cluster")).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
			pinned := a.pinnedAgents(w)
			for _, ag := range pinned[:min(3, len(pinned))] {
				agent := ag
				b := ui.Box(c.Key("agent-"+agent.ID)).Size(20, 20).Radius(k.capsule(20)).Center().Shrink(0).
					Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(agent.Name).Tooltip(agent.Name)
				col := k.textMuted
				if b.Pressed() {
					b.Background(k.pressed)
					col = k.text
				} else if b.Hovered() {
					b.Background(k.hover)
					col = k.text
				}
				if fadeColorsEnabled() {
					b.Transition(hoverFade)
				}
				b.Children(func() {
					ui.Icon(c, icon(programOf(agent.ID).Glyph)).Size(13, 13).TextColor(col)
				})
				if b.Clicked() {
					a.later(c, func() { a.newTabInWithCommand(w, agent.Command) })
				}
			}
			more := iconButton(c, k, "chevron-down", locales.ProjectQuickLaunchers(), 20, 11).Tooltip(locales.ProjectQuickLaunchers())
			more.Menu(func(m *ui.Menu) { a.projectAgentMenu(c, m, w, agents) })
		})
	})
}

// newTabIn opens a tab in workspace w, shown then: in its folder for a
// project (New Tab of its row, and ⌘T while it is shown).
func (a *App) newTabIn(w *Workspace) {
	a.newTabInWithCommand(w, nil)
}

// newTabInWithCommand opens a tab in workspace w running command (nil for
// the login shell).
func (a *App) newTabInWithCommand(w *Workspace, command []string) {
	if w == nil || w.offline() {
		return
	}
	if w != a.ws {
		a.showWorkspace(w)
		if w.app != a {
			return
		}
	}
	a.newProjectTabWithCommand(command)
}

// canMoveTab tells whether tab t of the window may move to workspace w:
// another workspace of its host (docs/MOD-002-projects.md: a tab moves between
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
