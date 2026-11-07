package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

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
// when the sidebar does not hold the window's controls.
func (a *App) sidebarLayout(c *ui.Context, k *colors, right bool) {
	shown := !prefs.SidebarHidden
	row := ui.Row(c.Key("sidebar-layout")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	// How open the sidebar is, from 0 to 1, as it shows and hides; and how
	// tall the bar over the panes, which the sidebar hides when it has the
	// window's controls.
	var open float32
	if shown {
		open = 1
	}
	open = max(animated(c, row, "sidebar", open), 0)
	bar := !shown || right || ownControls
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
// buttons while it is hidden, and the window's controls when the sidebar
// does not have them.
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
		if a.ws != nil {
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
				for _, h := range a.allHosts() {
					folded := a.hostSection(c, k, h)
					for _, t := range a.tabsOn(h) {
						if !folded || t == a.chosenOn[hostKey(h)] || slices.ContainsFunc(t.allPanes(), func(p *Pane) bool { return p.needsYou() > 0 }) {
							a.sidebarTab(c, k, slices.Index(tabs, t), t, reserve)
						}
					}
				}
			})
		})
		ui.Row(c).Shrink(0).Padding(4, 6, 8, 6).AlignItems(ui.Center).Children(func() {
			if iconButton(c, k, "settings-2", locales.SidebarSettings(), 30, 16).Tooltip(tip(locales.SidebarSettings(), &cmdSettings)).Clicked() {
				a.openSettings()
			}
			a.spaceStrip(c, k)
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
	e := ui.Row(c.Key(t.ID)).Height(sideRowH).Shrink(0).Padding(0, 6, 0, 9).Gap(6).AlignItems(ui.Center).Radius(pillRadius(sideRowH, 9)).
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

// hostSection is the label over a host's tabs in the sidebar: a button
// of its state, its name and a chevron, which folds the host's tabs away
// or back, and under the pointer a button that opens a tab there. It
// reports whether the tabs are folded.
func (a *App) hostSection(c *ui.Context, k *colors, h *hostConn) bool {
	name, key := a.hostName(h), hostKey(h)
	folded := slices.Contains(prefs.FoldedHosts, key)
	row := ui.Row(c.Key("host-"+key)).Height(26).Shrink(0).Padding(6, 2, 0, 8).Gap(6).AlignItems(ui.Center)
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
		fold := ui.Row(c).Grow(1).MinWidth(0).Height(26).Gap(6).AlignItems(ui.Center).Role(ui.RoleDisclosure).Label(name)
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
			if h != nil {
				label, _, _ := hostStatus(h)
				ui.Box(c).Size(6, 6).Radius(3).Background(hostDot(k, h)).Shrink(0).Tooltip(label)
			}
			ui.Text(c, name).FontSize(11.5).FontWeight(600).TextColor(k.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
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
