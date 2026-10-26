package main

import (
	"slices"

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
}

// syncSidebarItem enables the menu's Toggle Sidebar while the tabs are in
// a sidebar.
func (a *App) syncSidebarItem() {
	if a.sidebarMenuItem != nil {
		a.sidebarMenuItem.SetEnabled(prefs.TabPlacement != "")
	}
}

// sidebarLayout lays out the window with its tabs in a sidebar: the
// sidebar, the edge that resizes it, and the panes, under a title bar
// when the sidebar does not hold the window's controls.
func (a *App) sidebarLayout(c *ui.Context, k *colors, right bool) {
	shown := !prefs.SidebarHidden
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if shown && !right {
			a.sidebar(c, k, false)
			a.sidebarEdge(c, k, false)
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if !shown || right || ownControls {
				a.paneBar(c, k, right, shown)
			} else {
				// The panes come up to the top, as the window's controls are
				// the sidebar's; the gap over them drags the window.
				ui.Box(c).Height(gap).Shrink(0).DragWindow()
			}
			left, end := float32(gap), float32(gap)
			if shown && right {
				end = 0
			} else if shown {
				left = 0
			}
			a.panes(c, k, left, end)
		})
		if shown && right {
			a.sidebarEdge(c, k, true)
			a.sidebar(c, k, true)
		}
	})
}

// paneBar is the title bar over the panes beside a sidebar: the host and
// the sidebar's buttons while it is hidden, and the window's controls
// when the sidebar does not have them.
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
			a.hostChip(c, k)
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

// sidebar draws the sidebar: under the window's controls its buttons, the
// host, and the tabs, one a row; the settings' button at its foot.
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
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(2, 6, 6, 6).Children(func() {
			ui.Row(c).Padding(0, 0, 4, 0).Children(func() { a.hostChip(c, k) })
			tabs := slices.Clone(a.tabs)
			ui.Column(c).Gap(2).Role(ui.RoleTabList).Label(locales.TabListLabel()).Children(func() {
				if len(a.hosts) == 0 {
					for i, t := range tabs {
						a.sidebarTab(c, k, i, t)
					}
					return
				}
				// With remote hosts, the tabs go under their host's name,
				// this machine's first.
				for _, h := range a.allHosts() {
					a.hostSection(c, k, h)
					for _, t := range a.tabsOn(h) {
						a.sidebarTab(c, k, slices.Index(tabs, t), t)
					}
				}
			})
		})
		ui.Row(c).Shrink(0).Padding(4, 6, 8, 6).AlignItems(ui.Center).Children(func() {
			if iconButton(c, k, "settings-2", locales.SidebarSettings(), 30, 16).Tooltip(tip(locales.SidebarSettings(), &cmdSettings)).Clicked() {
				a.openSettings()
			}
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
func (a *App) sidebarTab(c *ui.Context, k *colors, i int, t *Tab) {
	active := i == a.active
	e := ui.Row(c.Key(t.ID)).Height(sideRowH).Shrink(0).Padding(0, 6, 0, 6).Gap(8).AlignItems(ui.Center).Radius(9).
		Role(ui.RoleTab).Selected(active)
	name, detail := t.label()
	e.Label(name + " " + detail)
	if active {
		e.Background(k.tabActive).Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 2, 8, 0, k.shadow)
		// A tab chosen otherwise than in the list, as by its keys, scrolls
		// into view, once: the list scrolls freely after.
		if a.revealed != t {
			a.revealed = t
			e.ScrollIntoView()
		}
	} else if e.Hovered() {
		e.Background(k.hover)
	}
	a.tabEvents(e, k, i, t)
	hovered := e.Hovered()
	e.Children(func() {
		// The tiles take the room of three, so that the names line up.
		ui.Box(c).Width(tileW + tileStep*2 + 2).Shrink(0).Children(func() { a.tiles(c, k, t) })
		a.tabRest(c, k, t, active, hovered)
	})
}

// hostSection is the label over a host's tabs in the sidebar: its name,
// its state, and under the pointer a button that opens a tab there.
func (a *App) hostSection(c *ui.Context, k *colors, h *hostConn) {
	name, key := a.hostName(h), "host"
	if h != nil {
		key += "-" + h.ID
	}
	row := ui.Row(c.Key(key)).Height(26).Shrink(0).Padding(6, 2, 0, 8).Gap(6).AlignItems(ui.Center)
	hovered := row.Hovered()
	row.Children(func() {
		if h != nil {
			label, _, _ := hostStatus(h)
			ui.Box(c).Size(6, 6).Radius(3).Background(hostDot(k, h)).Shrink(0).Tooltip(label)
		}
		ui.Text(c, name).FontSize(11.5).FontWeight(600).TextColor(k.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if hovered {
			label := locales.CmdNewTabOn(name)
			if iconButton(c, k, "plus", label, 20, 13).Tooltip(label).Clicked() {
				a.newTabOn(h, a.dirOn(h))
			}
		}
	})
}
