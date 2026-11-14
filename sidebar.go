package main

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/locales"
)

// The sidebar of vertical tabs, as Rex's: at the left or the right of the
// window, as wide as the user drags it, and collapsible.
const (
	sidebarMinW     = 180
	sidebarMaxW     = 400
	sidebarDefaultW = 236
	sideRowH        = 32
	// panesMinW is the room the panes keep beside the sidebar.
	panesMinW = 320
)

// sidebarViews are the values of SidebarView, in the order of the
// settings' control: the window's session, every session by host.
var sidebarViews = []string{"", "host"}

// hostView tells whether the sidebar lists every session under the hosts
// it has tabs on: as the user chose, where it can (hostViewable).
func (a *App) hostView() bool { return prefs.SidebarView == "host" && a.hostViewable() }

// hostViewable tells whether the sidebar can list the sessions by host: in
// a sidebar, with hosts besides this machine. Without them the sessions
// would only repeat the strip's dots.
func (a *App) hostViewable() bool {
	return prefs.TabPlacement != "" && len(a.hosts) > 0 && a.st != nil
}

// toggleSidebarView switches the sidebar between its views.
func (a *App) toggleSidebarView() {
	a.setWindowPref(func(s *settings) {
		if s.SidebarView == "" {
			s.SidebarView = "host"
		} else {
			s.SidebarView = ""
		}
	})
}

// placements are the values of TabPlacement, in the order of the settings'
// control: above, the left side, the right side.
var placements = []string{"", "left", "right"}

func sidebarWidth() float32 {
	if w := prefs.SidebarWidth; w != 0 {
		return w
	}
	return sidebarDefaultW
}

// sidebarMax is how wide the sidebar may be in a window winW wide.
func sidebarMax(winW float32) float32 {
	return max(min(sidebarMaxW, winW-panesMinW), sidebarMinW)
}

// sidebarWidthIn is the sidebar's width in a window winW wide: the user's,
// unless the panes would lack room.
func sidebarWidthIn(winW float32) float32 {
	return min(sidebarWidth(), sidebarMax(winW))
}

// setTabPlacement places the tabs. A sidebar the user hid shows again when
// the tabs come back to it from above, lest the choice seem to do nothing.
func setTabPlacement(s *settings, place string) {
	if s.TabPlacement == "" && place != "" {
		s.SidebarHidden = false
	}
	s.TabPlacement = place
}

// paneHeaders tells whether the panes show their headers: always, but
// when the user hid them and a sidebar at the left names the tabs.
func paneHeaders() bool {
	return !prefs.HidePaneHeaders || prefs.TabPlacement != "left" || prefs.SidebarHidden
}

func (a *App) toggleSidebar() {
	if prefs.TabPlacement == "" {
		return
	}
	prefs.SidebarHidden = !prefs.SidebarHidden
	saveSettings()
	for _, o := range a.peers() {
		o.do(func() {})
	}
}

// sidebarMenuItem is the menu's Toggle Sidebar.
var sidebarMenuItem *mygo.MenuItem

// syncSidebarItem enables the menu's Toggle Sidebar while the tabs are in
// a sidebar.
func (a *App) syncSidebarItem() {
	if sidebarMenuItem != nil {
		sidebarMenuItem.SetEnabled(prefs.TabPlacement != "")
	}
}

// sidebarLayout lays out the window with its tabs in a sidebar: the
// sidebar, the edge that resizes it, and the panes, under a title bar
// when the sidebar does not hold the window's controls: not when they are
// on its side, as the traffic lights on macOS are.
func (a *App) sidebarLayout(c *ui.Context, k *colors, right bool) {
	shown := !prefs.SidebarHidden
	row := ui.Row(c.Key("sidebar-layout")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	// How open the sidebar is, from 0 to 1, as it shows and hides; and how
	// tall the bar over the panes, which the sidebar hides when it has the
	// window's controls; the controls on the right, as on Linux with a left
	// sidebar, keep it, as the panes' buttons would lie under them.
	var open float32
	if shown {
		open = 1
	}
	open = max(animated(c, row, "sidebar", open), 0)
	bar := !shown || right || ownControls || c.TitleBar().Right > 0
	barH := float32(gap)
	if bar {
		barH = titleH
	}
	barH = animated(c, row, "pane-bar", barH)
	// The panes' room changes as the sidebar and the bar move: their
	// terminals hold their size until both rest.
	a.sidebarMoving = !row.Settled("sidebar") || !row.Settled("pane-bar")
	row.Children(func() {
		if !right {
			a.sidebarDrawer(c, k, false, open)
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Height(max(barH, 0)).Shrink(0).Clip().Children(func() {
				if bar {
					a.paneBar(c, k, right, shown)
				} else {
					// The panes come up to the top, as the window's controls
					// are the sidebar's; the gap over them drags the window.
					ui.Box(c).Height(gap).Shrink(0).DragWindow()
				}
			})
			// The panes' gap on the sidebar's side is its edge's.
			side := gap * (1 - min(open, 1))
			left, end := float32(gap), float32(gap)
			if right {
				end = side
			} else {
				left = side
			}
			a.panes(c, k, left, end)
		})
		if right {
			a.sidebarDrawer(c, k, true, open)
		}
	})
}

// sidebarDrawer holds the sidebar and its edge, as open as open says: the
// sidebar slides out of sight under the window's side as it closes.
func (a *App) sidebarDrawer(c *ui.Context, k *colors, right bool, open float32) {
	if open <= 0.001 {
		return
	}
	winW, _ := c.Size()
	full := sidebarWidthIn(winW) + gap
	drawer := ui.Row(c.Key("sidebar-drawer")).Width(full * min(open, 1)).Shrink(0)
	if open < 1 {
		drawer.Clip()
	}
	drawer.Children(func() {
		in := ui.Row(c).Absolute().Top(0).Bottom(0).Width(full).AlignItems(ui.Stretch)
		if right {
			in.Left(0)
		} else {
			in.Right(0)
		}
		in.Children(func() { a.sidebarParts(c, k, right) })
	})
}

// sidebarParts are the sidebar and its edge, the edge inside.
func (a *App) sidebarParts(c *ui.Context, k *colors, right bool) {
	if right {
		a.sidebarEdge(c, k, true)
		a.sidebar(c, k, true)
		return
	}
	a.sidebar(c, k, false)
	a.sidebarEdge(c, k, false)
}

// paneBar is the title bar over the panes beside a sidebar: the sidebar's
// buttons while it is hidden, and the room for the window's controls when
// the sidebar does not have them.
func (a *App) paneBar(c *ui.Context, k *colors, right, shown bool) {
	bar := c.TitleBar()
	left := float32(gap)
	if right || !shown {
		left = bar.Left
		if left == 0 {
			left = 12 // in full screen, or beside no controls
		} else {
			left += 14
		}
	}
	end := max(bar.Right, 10)
	if right && shown {
		end = 0
	}
	ui.Row(c).Height(titleH).Shrink(0).Padding(0, end, 0, left).Gap(14).AlignItems(ui.Center).DragWindow().Children(func() {
		if !shown && !right {
			a.sidebarButton(c, k, false)
		}
		if !shown {
			a.workspaceChip(c, k, false)
		}
		ui.Spacer(c)
		if !shown {
			a.titleButtons(c, k, func() {
				if right {
					a.sidebarButton(c, k, true)
				}
			})
		}
		if !right || !shown {
			a.windowControls(c, k)
		}
	})
}

// sidebarButton shows and hides the sidebar.
func (a *App) sidebarButton(c *ui.Context, k *colors, right bool) {
	glyph := "panel-left"
	if right {
		glyph = "panel-right"
	}
	label := locales.SidebarHide()
	if prefs.SidebarHidden {
		label = locales.SidebarShow()
	}
	if iconButton(c, k, glyph, label, 30, 17).Tooltip(tip(label, &cmdSidebar)).Clicked() {
		a.toggleSidebar()
	}
}

// sidebar draws the sidebar: under the window's controls its buttons and
// the tabs, one a row; the settings' button and the host's at its foot.
func (a *App) sidebar(c *ui.Context, k *colors, right bool) {
	bar := c.TitleBar()
	winW, _ := c.Size()
	ui.Column(c).Width(sidebarWidthIn(winW)).Shrink(0).MinHeight(0).Children(func() {
		head := ui.Row(c).Height(titleH).Shrink(0).Gap(2).AlignItems(ui.Center).DragWindow()
		if right {
			head.Padding(0, max(bar.Right, 6), 0, 6)
		} else {
			left := bar.Left
			if left == 0 {
				left = 6
			} else {
				left += 14 // where the title bar has it when the sidebar hides
			}
			head.Padding(0, 6, 0, left)
		}
		head.Children(func() {
			a.sidebarButton(c, k, right)
			ui.Spacer(c)
			a.titleButtons(c, k, nil)
			if right {
				a.windowControls(c, k)
			}
		})
		// The host view lists the spaces under their hosts: the chip would
		// say it twice.
		if a.ws != nil && !a.hostView() {
			ui.Row(c).Shrink(0).Padding(0, 6, 4, 6).Children(func() { a.workspaceChip(c, k, true) })
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(2, 6, 6, 6).Children(func() {
			tabs := slices.Clone(a.tabs)
			reserve := tilesReserve(tabs)
			ui.Column(c).Gap(2).Role(ui.RoleTabList).Label(locales.TabListLabel()).Children(func() {
				if len(a.hosts) == 0 {
					for i, t := range tabs {
						a.sidebarTab(c, k, i, t, reserve)
					}
					return
				}
				// With remote hosts, the tabs go under their host's name,
				// this machine's first. A folded host shows the tab last
				// chosen on it, and those that need the user.
				if t := a.tab(); t != nil {
					if a.chosenOn == nil {
						a.chosenOn = map[string]*Tab{}
					}
					a.chosenOn[hostKey(t.host())] = t
				}
				// The host view's first row of the window's space holds
				// its popover and the field that renames it.
				anchored := false
				for _, h := range a.allHosts() {
					folded := a.hostSection(c, k, h)
					on := a.tabsOn(h)
					var shown []*Tab
					for _, t := range on {
						if !folded || t == a.chosenOn[hostKey(h)] || slices.ContainsFunc(t.allPanes(), func(p *Pane) bool { return p.needsYou() > 0 }) {
							shown = append(shown, t)
						}
					}
					if a.hostView() {
						a.hostSessions(c, k, h, folded, len(on), shown, tabs, reserve, &anchored)
						continue
					}
					for _, t := range shown {
						a.sidebarTab(c, k, slices.Index(tabs, t), t, reserve)
					}
				}
				// Without a row to hold them, the popover and the field
				// close rather than stay open unseen.
				if a.hostView() && !anchored {
					a.wsOpen, a.wsRenaming = false, false
				}
			})
		})
		ui.Row(c).Shrink(0).Padding(4, 6, 8, 6).AlignItems(ui.Center).Children(func() {
			if iconButton(c, k, "settings-2", locales.SidebarSettings(), 30, 16).Tooltip(tip(locales.SidebarSettings(), &cmdSettings)).Clicked() {
				a.openSettings()
			}
			// The host view lists the sessions: their strip would say it
			// twice.
			if a.hostView() {
				ui.Spacer(c)
			} else {
				a.spaceStrip(c, k)
			}
			a.hostButton(c, k)
		})
	})
}

// sidebarEdge is the sidebar's inner edge, which drags its width; a double
// click gives it its default width back.
func (a *App) sidebarEdge(c *ui.Context, k *colors, right bool) {
	e := ui.Box(c.Key("sidebar-edge")).Width(gap).Shrink(0).Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label(locales.SidebarEdge())
	// The width follows the pointer from where the drag began, so that the
	// edge comes back under it after a bound stopped it.
	winW, _ := c.Size()
	if dx, _, ok := e.Dragged(); ok {
		if !a.edgeDrag {
			a.edgeDrag, a.edgeFrom, a.edgeMoved = true, sidebarWidthIn(winW), 0
		}
		if right {
			dx = -dx
		}
		a.edgeMoved += dx
		if a.edgeMoved != 0 {
			prefs.SidebarWidth = min(max(a.edgeFrom+a.edgeMoved, sidebarMinW), sidebarMax(winW))
		}
	} else {
		a.edgeDrag = false
	}
	if _, _, ok := e.DragEnded(); ok {
		saveSettings()
	}
	if e.DoubleClicked() {
		prefs.SidebarWidth = 0
		saveSettings()
	}
	if e.Hovered() || e.Dragging() || e.Pressed() {
		e.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, k.iconMuted.Alpha(0.6), 1.5)
		})
	}
}

// sidebarTab draws a tab in the sidebar: the tiles of its panes' programs,
// its name and detail, and its close button or activity.
func (a *App) sidebarTab(c *ui.Context, k *colors, i int, t *Tab, reserve float32) {
	active := i == a.active
	// Keyed by the tab's lasting ID, as spaceTab keys it: as the window
	// goes to show another space, the rows of both stay, not close and
	// open again.
	e := ui.Row(c.Key("tab-"+t.UID)).Height(sideRowH).Shrink(0).Padding(0, 6, 0, 9).Gap(6).AlignItems(ui.Center).Radius(pillRadius(sideRowH, 9)).
		Role(ui.RoleTab).Selected(active)
	name, detail := t.label()
	e.Label(name + " " + detail)
	if active {
		glassPill(e, k, sideRowH)
		// A tab chosen otherwise than in the list, as by its keys, scrolls
		// into view, once: the list scrolls freely after.
		if a.revealed != t {
			a.revealed = t
			e.ScrollIntoView()
		}
	} else if e.Hovered() {
		e.Background(hoverFace(k))
	}
	a.tabEvents(c, e, k, i, t)
	hovered := e.Hovered()
	e.Children(func() {
		// The tiles take the room of the widest tab's, so that the names
		// line up.
		ui.Box(c).Width(reserve).Shrink(0).Children(func() { a.tiles(c, k, t) })
		a.tabRest(c, k, t, active, hovered)
	})
}

// tilesReserve is the room the tiles of a sidebar row take: that of the
// tab with the most panes, each tile past the first peeking out by
// tileStep, so that the names of every row line up.
func tilesReserve(tabs []*Tab) float32 {
	most := 1
	for _, t := range tabs {
		most = max(most, min(len(t.panes()), 3))
	}
	return float32(tileW + tileStep*(most-1) + 2)
}

// hostSection is the label over a host's tabs in the sidebar: the icon of
// its machine, its name and a chevron, which folds the host's tabs away
// or back, and under the pointer a button that opens a tab there; a
// remote host's right click chooses its icon. It reports whether the tabs
// are folded.
func (a *App) hostSection(c *ui.Context, k *colors, h *hostConn) bool {
	name, key := a.hostName(h), hostKey(h)
	folded := slices.Contains(prefs.FoldedHosts, key)
	row := ui.Row(c.Key("host-"+key)).Height(26).Shrink(0).Padding(6, 2, 0, 9).Gap(6).AlignItems(ui.Center)
	// It slides with the tabs as those above it open and close.
	if anim := motion(c); anim != nil {
		row.Transition(ui.ElementTransition{Animation: anim, Position: true})
	}
	// A tab of another host's dropped on it asks to teleport there.
	if dragged, ok := ui.Drop[*Tab](row); ok && teleportsTo(dragged, h) {
		a.askTeleport(dragged, h)
	}
	if dragged, over := ui.DragOver[*Tab](row); over && teleportsTo(dragged, h) {
		row.Radius(pillRadius(26, 9)).Border(1.5, k.busy.Alpha(0.6))
	}
	hovered := row.Hovered()
	row.Children(func() {
		fold := ui.Row(c).Grow(1).MinWidth(0).Height(26).Gap(8).AlignItems(ui.Center).Role(ui.RoleDisclosure).Label(name)
		if h != nil {
			fold.ContextMenu(func(m *ui.Menu) {
				m.Submenu(locales.HostsIcon(), func(m *ui.Menu) { a.hostIconMenu(m, h) })
			})
		}
		if fold.Clicked() {
			if folded {
				prefs.FoldedHosts = slices.DeleteFunc(prefs.FoldedHosts, func(s string) bool { return s == key })
			} else {
				prefs.FoldedHosts = append(prefs.FoldedHosts, key)
			}
			folded = !folded
			saveSettings()
		}
		fold.Expanded(!folded).Children(func() {
			a.hostSectionIcon(c, k, h)
			ui.Text(c, name).FontSize(12).FontWeight(600).TextColor(k.textMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
			turn := float32(0)
			if folded {
				turn = -90
			}
			chevron.Rotate(animated(c, chevron, "fold", turn))
		})
		// The button's room is kept, so that the chevron stays in place.
		if !hovered {
			ui.Box(c).Size(20, 20).Shrink(0)
			return
		}
		label := locales.CmdNewTabOn(name)
		if iconButton(c, k, "plus", label, 20, 13).Tooltip(label).Clicked() {
			a.newTabOn(h, a.dirOn(h))
		}
	})
	return folded
}

// hostSectionIcon is the icon of a host's machine before its name in the
// sidebar, as the iOS app's spaces show it. A remote host that is not
// connected has a dot of its state on it, as on the host's button: not
// while it makes its first attempt, as every launch does.
func (a *App) hostSectionIcon(c *ui.Context, k *colors, h *hostConn) {
	glyph := ""
	if h == nil {
		_, _, glyph = a.hostIdentity()
	} else {
		glyph = h.remoteGlyph()
	}
	box := ui.Box(c).Size(15, 15).Shrink(0).Children(func() {
		glyphIcon(c, glyph, 15, k.textMuted)
		if h != nil && h.state != hostConnected && h.state != hostConnecting {
			ui.Box(c).Absolute().Right(-2).Bottom(-2).Size(7, 7).Radius(4).Background(hostDot(k, h))
		}
	})
	if h != nil {
		label, _, _ := hostStatus(h)
		box.Tooltip(label)
	}
}

// hostSessions lists under host h the sessions with tabs on it, in their
// order. Each opens on its tabs there and closes apart from the others
// (spaceIsOpen): the window's own on its tabs, shown as they are of the
// host, another on those it has there (spaceTabs); a click on another
// shows it. A folded host keeps, as of its tabs, the window's session when
// it has tabs to show, and, closed, the sessions that need the user there.
// own is how many tabs the window's session has on the host.
func (a *App) hostSessions(c *ui.Context, k *colors, h *hostConn, folded bool, own int, shown []*Tab, tabs []*Tab, reserve float32, anchored *bool) {
	for _, w := range a.st.workspaces {
		if w != a.ws {
			n := spaceTabsOn(w, h)
			if n == 0 || folded && !a.st.needsOn(w, h) {
				continue
			}
			open := !folded && a.spaceIsOpen(h, w)
			clicked, toggled := a.sessionRow(c, k, h, w, n, open, false)
			// Under a folded host its tabs cannot show: the chevron shows
			// the space, as the row does.
			switch {
			case toggled && !folded:
				a.setSpaceOpen(h, w, !open)
			case clicked || toggled:
				a.setSpaceOpen(h, w, true)
				a.later(c, func() { a.showSessionOn(w, h, -1) })
			}
			if open {
				a.spaceTabs(c, k, h, w, reserve)
			}
			continue
		}
		if len(shown) == 0 {
			continue
		}
		open := a.spaceIsOpen(h, w)
		anchor := !*anchored
		*anchored = true
		if clicked, toggled := a.sessionRow(c, k, h, w, own, open, anchor); clicked || toggled {
			a.setSpaceOpen(h, w, !open)
		}
		if !open {
			continue
		}
		ui.Column(c.Key("space-tabs-"+spaceKey(h, w))).Gap(2).Padding(0, 0, 0, 12).Children(func() {
			for _, t := range shown {
				a.sidebarTab(c, k, slices.Index(tabs, t), t, reserve)
			}
		})
	}
}

// spaceKey names session w under host h, in spaceOpen.
func spaceKey(h *hostConn, w *Workspace) string { return hostKey(h) + "/" + w.ID }

// spaceIsOpen tells whether the host view opens session w on its tabs of
// host h: as it was last opened or closed there, else when the window
// shows it.
func (a *App) spaceIsOpen(h *hostConn, w *Workspace) bool {
	if open, ok := a.spaceOpen[spaceKey(h, w)]; ok {
		return open
	}
	return w == a.ws
}

// setSpaceOpen opens or closes session w under host h in the host view.
func (a *App) setSpaceOpen(h *hostConn, w *Workspace, open bool) {
	if a.spaceOpen == nil {
		a.spaceOpen = map[string]bool{}
	}
	a.spaceOpen[spaceKey(h, w)] = open
}

// keepSpacesOpen, as the window goes to show session w, keeps the session
// it leaves open or closed under each host as it was, and opens w where
// it was never closed: where the user closed it, it stays closed.
func (a *App) keepSpacesOpen(w *Workspace) {
	if w == a.ws {
		return
	}
	for _, h := range a.allHosts() {
		if a.ws != nil {
			a.setSpaceOpen(h, a.ws, a.spaceIsOpen(h, a.ws))
		}
		if _, ok := a.spaceOpen[spaceKey(h, w)]; !ok {
			a.setSpaceOpen(h, w, true)
		}
	}
}

// spaceTabs lists, under the row of a session the window does not show,
// its tabs on host h, as the window's own show: the live ones of the
// window that shows it, else those this window keeps of it, else those of
// its layout. None is chosen here; a click on one shows the session on it.
func (a *App) spaceTabs(c *ui.Context, k *colors, h *hostConn, w *Workspace, reserve float32) {
	var live []*Tab
	switch {
	case w.app != nil:
		live = w.app.tabsOn(h)
	case w.kept != nil && w.kept.app == a:
		for _, t := range w.kept.tabs {
			if t.host() == h {
				live = append(live, t)
			}
		}
	}
	list := ui.Column(c.Key("space-tabs-"+spaceKey(h, w))).Gap(2).Padding(0, 0, 0, 12)
	if w.app != nil {
		list.Opacity(0.5)
	}
	list.Children(func() {
		i := 0
		row := func(key, name, detail string, tiles func()) {
			if spaceTab(c, k, key, name, detail, reserve, tiles) {
				n := i
				a.later(c, func() { a.showSessionOn(w, h, n) })
			}
			i++
		}
		if live != nil {
			reserve = max(reserve, tilesReserve(live))
			for _, t := range live {
				name, detail := t.label()
				row(t.UID, name, detail, func() { a.tiles(c, k, t) })
			}
			return
		}
		id := ""
		if h != nil {
			id = h.ID
		}
		for j, t := range w.layout.Tabs {
			if savedTabHost(t) != id {
				continue
			}
			name, prog := savedTabLabel(t)
			row(cmp.Or(t.ID, fmt.Sprint(j)), name, "", func() {
				ui.Box(c).Size(tileW+2, tileH+4).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
					drawTile(p, k, ui.Rect{X: r.X + 1, Y: r.Y + 2, W: tileW, H: tileH}, prog, true)
				})
			})
		}
	})
}

// spaceTab is a tab of a session the window does not show, in the host
// view: its tiles and its name, as the window's tabs show, but neither
// chosen nor dragged. It reports whether it was clicked.
func spaceTab(c *ui.Context, k *colors, key, name, detail string, reserve float32, tiles func()) bool {
	e := ui.Row(c.Key("tab-"+key)).Height(sideRowH).Shrink(0).Padding(0, 6, 0, 9).Gap(6).AlignItems(ui.Center).Radius(pillRadius(sideRowH, 9)).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(name + " " + detail)
	if anim := motion(c); anim != nil {
		e.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion})
	}
	if e.Hovered() {
		e.Background(hoverFace(k))
	}
	e.Children(func() {
		ui.Box(c).Width(reserve).Shrink(0).Children(tiles)
		ui.Box(c).Grow(1).MinWidth(0).Height(18).Draw(func(p *ui.Painter, r ui.Rect) {
			fadeText(p, r, tabLabel(name, detail, k.textMuted, k.textFaint))
		})
	})
	return e.Clicked()
}

// savedTabLabel is how a tab of a layout names itself, and the program
// of its tile, from what the layout keeps: its name, else its first
// pane's, else the program that ran there.
func savedTabLabel(t savedTab) (string, program) {
	var n savedNode
	if r := t.Roots(); len(r) > 0 {
		n = *layout.FirstLeaf(r[0])
	}
	prog := shellTile
	switch {
	case n.URL != "":
		prog = browserProgram
	case n.Kind == "note":
		prog = noteProgram
	case n.Prog != "":
		prog = programOf(n.Prog)
	}
	return cmp.Or(t.Name, n.Name, prog.Name, "Shell"), prog
}

// sessionRow is a session under a host in the host view: a chevron, its
// color, its name and how many tabs it has on the host, and what needs
// the user there. The chevron opens and closes it on its tabs, as a click
// on the window's own does; a right click on that renames it, colors it
// or closes it, and as the anchor, the first, it holds the space's
// popover and the field that renames it. Another is a button that shows
// it, faded when another window shows it, as its dot in the strip. It
// reports whether the row was clicked, and whether the chevron was.
func (a *App) sessionRow(c *ui.Context, k *colors, h *hostConn, w *Workspace, n int, open, anchor bool) (clicked, toggled bool) {
	row := ui.Row(c.Key("space-"+hostKey(h)+"-"+w.ID)).Height(28).Shrink(0).Padding(0, 8, 0, 0).Gap(6).AlignItems(ui.Center).
		Radius(pillRadius(28, 9)).Cursor(ui.CursorPointer).Label(w.Name + ", " + workspaceDetail(w, a, n))
	if w == a.ws {
		row.Role(ui.RoleDisclosure).Expanded(open)
		row.ContextMenu(func(m *ui.Menu) { a.spaceMenu(m, true) })
	} else {
		row.Role(ui.RoleButton)
	}
	if anim := motion(c); anim != nil {
		row.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion})
	}
	if row.Hovered() {
		row.Background(hoverFace(k))
	}
	if w.app != nil && w != a.ws {
		row.Opacity(0.5)
	}
	row.Children(func() {
		// The chevron takes the row's padding before it, for a larger
		// target.
		fold := ui.Row(c.Key("fold")).Height(28).Padding(0, 0, 0, 8).AlignItems(ui.Center).Shrink(0)
		if w != a.ws {
			fold.Role(ui.RoleDisclosure).Expanded(open).Label(w.Name)
		}
		toggled = fold.Clicked()
		fold.Children(func() {
			chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.textFaint).Shrink(0)
			turn := float32(0)
			if !open {
				turn = -90
			}
			chevron.Rotate(animated(c, chevron, "fold", turn))
		})
		spaceDot(c, k, w)
		if anchor && a.wsRenaming {
			a.workspaceRenameField(c, k, w.Name)
			return
		}
		ui.Text(c, w.Name).FontSize(13).FontWeight(500).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if s := a.st.statusOn(w, h); s != nil && s.State != "idle" {
			statusDot(c, k, s, 7).Label(statusTip(s))
		}
		ui.Text(c, fmt.Sprint(n)).FontSize(11.5).TextColor(k.textFaint).Shrink(0)
	})
	if anchor {
		a.workspacePopover(c, k, row)
		if a.wsRenaming {
			return false, false
		}
	}
	return row.Clicked(), toggled
}

// showSessionOn shows session w, here or in the window that shows it, on
// its ith tab of host h; for i < 0, on its tab there last chosen, else its
// first there, unless the tab shown is there. The window that shows it
// chooses the tab, in its own update; selectTab saves the choice
// (changed).
func (a *App) showSessionOn(w *Workspace, h *hostConn, i int) {
	a.showWorkspace(w)
	o := w.app
	if o == nil {
		return
	}
	pick := func() {
		on := o.tabsOn(h)
		if i >= 0 && i < len(on) {
			o.selectTab(slices.Index(o.tabs, on[i]))
			return
		}
		if t := o.tab(); t != nil && t.host() == h {
			return
		}
		if len(on) == 0 {
			return
		}
		t := on[0]
		if chosen := o.chosenOn[hostKey(h)]; slices.Contains(on, chosen) {
			t = chosen
		}
		o.selectTab(slices.Index(o.tabs, t))
	}
	if o == a {
		pick()
	} else {
		o.do(pick)
	}
}

// spaceTabsOn counts the tabs session w has on host h: those of the window
// that shows it, else those of its layout.
func spaceTabsOn(w *Workspace, h *hostConn) int {
	if w.app != nil {
		return len(w.app.tabsOn(h))
	}
	id := ""
	if h != nil {
		id = h.ID
	}
	n := 0
	for _, t := range w.layout.Tabs {
		if savedTabHost(t) == id {
			n++
		}
	}
	return n
}

// savedTabHost is the host id of a saved tab's first pane, as Tab.host
// takes its first pane's: "" for this machine.
func savedTabHost(t savedTab) string {
	if r := t.Roots(); len(r) > 0 {
		return layout.FirstLeaf(r[0]).Host
	}
	return ""
}
