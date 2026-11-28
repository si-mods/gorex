package main

import (
	"cmp"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/internal/theme"
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

// sidebarRowH, sidebarLead, sidebarFont and sidebarGroupPad are the
// sidebar's metrics: the user's sidebar.* settings over the theme's over
// the constants (docs/DSK-007-sidebar-style.md, docs/DSK-008-theme.md).
func sidebarRowH() float32 {
	return cmp.Or(prefs.SidebarRowHeight, themeSidebar.RowHeight, sideRowH)
}

func sidebarLead() float32 {
	return cmp.Or(prefs.SidebarIndent, themeSidebar.Indent, sideLead)
}

// sidebarTabLead is a tab-level row's start padding: sidebarLead plus the
// sidebar.tabIndent of the user or the theme, so a project's tabs read
// under it.
func sidebarTabLead() float32 {
	return sidebarLead() + cmp.Or(prefs.SidebarTabIndent, themeSidebar.TabIndent)
}

func sidebarFont(def float32) float32 {
	return cmp.Or(prefs.SidebarFontSize, themeSidebar.FontSize, def)
}

func sidebarGroupPad() float32 {
	return cmp.Or(prefs.SidebarGroupPad, themeSidebar.GroupPad, groupPad)
}

// sidebarGrouping is sidebar.groupBy, "host" when the user and the theme
// leave it.
func sidebarGrouping() string { return cmp.Or(prefs.SidebarGroupBy, themeSidebar.GroupBy, "host") }

// sidebarIconsText tells whether sidebar.icons is "text": the rows' names
// with no program tiles, the folder glyph alone for a project.
func sidebarIconsText() bool { return cmp.Or(prefs.SidebarIcons, themeSidebar.Icons) == "text" }

// sidebarGroupFaced tells whether the current project's group draws its
// face: sidebar.groupBackground "none" leaves the group's rows on the
// sidebar's face, as every other project's.
func sidebarGroupFaced() bool {
	return cmp.Or(prefs.SidebarGroupFace, themeSidebar.GroupFace, "face") != "none"
}

// sidebarFillSel tells whether sidebar.selection is "fill": a quiet face
// fill on the chosen row instead of the glass pill.
func sidebarFillSel() bool { return cmp.Or(prefs.SidebarSelection, themeSidebar.Selection) == "fill" }

// sidebarChosen paints a sidebar row's chosen state: the glass pill as
// the other surfaces' chosen control, or the fill of sidebar.selection
// "fill", the row's face at tabActive with a radiusM corner.
func sidebarChosen(e ui.Element, k *colors, h float32, lifted bool) {
	if sidebarFillSel() {
		paintFace(e, k, roleTabActiveBackground, k.tabActive).Radius(k.radius(radiusM))
		return
	}
	if lifted {
		liftedPill(e, k, h)
		return
	}
	glassPill(e, k, h)
}

func sidebarGroupEdge() float32 {
	return (sidebarGroupPad() + rowGap - groupGap) / 2
}

// sidebarChosenColors returns the palette that the content of a chosen
// sidebar row draws with: the ladder of the row's face when the row is
// painted at tabActive (sidebar.selection "fill", or the glass setting off).
// Under glass the pill is the system's material, which no setting colors.
func sidebarChosenColors(k *colors) *colors {
	if sidebarFillSel() || !glassOn() {
		return surface(k, roleTabActiveBackground).on(k)
	}
	return pillColors(k, k.pillFace, k.sideBarFace)
}

// sidebarHoverColors returns the palette that the content of a hovered
// sidebar row draws with: the ladder of tabHover, the face under glass.
func sidebarHoverColors(k *colors) *colors {
	if glassOn() {
		return surface(k, roleTabHoverBackground).on(k)
	}
	return k
}

// sidebarPalette is the palette with the user's sidebar colors applied:
// the last layer of the resolution (docs/DSK-008-theme.md). It returns a
// copy: the palettes are shared. The role names are internal/theme's
// allowlist; tokenRenames in settings.go migrates the names
// docs/DSK-007-sidebar-style.md first had.
func sidebarPalette(k *colors, dark bool) *colors {
	if len(prefs.SidebarColors) == 0 {
		return k
	}
	out := *k
	for token, raw := range prefs.SidebarColors {
		role, ok := sidebarRole(token)
		if !ok {
			continue
		}
		if col, ok := theme.ColorFor(raw, dark); ok {
			*roleField(&out, role) = col
		}
	}
	return &out
}

// sidebarRole returns the role a sidebar.colorCustomizations key names: a
// sideBar.* role of internal/theme's allowlist. The other roles are a
// theme's alone.
func sidebarRole(token string) (theme.Role, bool) {
	if !strings.HasPrefix(token, "sideBar.") {
		return 0, false
	}
	return theme.RoleByName(token)
}

// hexColor parses a hex color without ui.Hex's panic on bad input.
func hexColor(s string) (col ui.Color, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return ui.Hex(s), true
}

// checkSidebarColors drops invalid sidebar.colorCustomizations entries of
// s at load and says why, once.
func checkSidebarColors(s *settings) {
	for token, raw := range s.SidebarColors {
		if _, ok := sidebarRole(token); !ok {
			log.Printf("gorex settings: sidebar.colorCustomizations: unknown token %q", token)
			delete(s.SidebarColors, token)
			continue
		}
		if !validStyleColor(raw) {
			log.Printf("gorex settings: sidebar.colorCustomizations: bad color for %q", token)
			delete(s.SidebarColors, token)
		}
	}
}

// validStyleColor reports whether raw is a value
// sidebar.colorCustomizations may hold: a hex color string, or an object
// whose "light" and "dark", where present, are hex color strings. A side
// that is absent overrides nothing of its appearance.
func validStyleColor(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		_, ok := hexColor(s)
		return ok
	}
	var pair struct {
		Light json.RawMessage `json:"light"`
		Dark  json.RawMessage `json:"dark"`
	}
	if json.Unmarshal(raw, &pair) != nil {
		return false
	}
	for _, side := range []json.RawMessage{pair.Light, pair.Dark} {
		if len(side) == 0 {
			continue
		}
		var v string
		if json.Unmarshal(side, &v) != nil {
			return false
		}
		if _, ok := hexColor(v); !ok {
			return false
		}
	}
	return true
}

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
// the sidebar does not have them. Beside a shown left sidebar under own
// controls, it holds the title buttons too, as the sidebar's head has the
// space chip.
func (a *App) paneBar(c *ui.Context, k *colors, right, shown bool) {
	unified := shown && !right && ownControls
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
	row := ui.Row(c).Height(titleH).Shrink(0).Padding(0, end, 0, left).Gap(14).AlignItems(ui.Center).DragWindow()
	k, face, ok := themedFace(k, roleTitleBarBackground)
	if ok {
		paintFace(row, k, roleTitleBarBackground, face)
	}
	row.Children(func() {
		if !shown && !right {
			a.sidebarButton(c, k, false)
		}
		if !shown {
			a.spacePicker(c, k, false)
			a.projectPicker(c, k)
		}
		ui.Spacer(c)
		if !shown || unified {
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
	if naturalIconButton(c, k, glyph, label, 30, 18).Tooltip(tip(label, &cmdSidebar)).Clicked() {
		a.toggleSidebar()
	}
}

// sidebar draws the sidebar: under the window's controls its buttons and
// the space picker, then the hosts, their projects and tabs
// (projectsList); the settings' button, New Project and the host's at its
// foot. With own controls on the left, its head holds the space picker
// beside the button, and the pane bar has the title buttons.
func (a *App) sidebar(c *ui.Context, k *colors, right bool) {
	k = sidebarPalette(k, c.Theme().Dark)
	bar := c.TitleBar()
	picker := a.st != nil
	unified := !right && ownControls
	winW, _ := c.Size()
	// A theme's sideBar.background is the column's face under its rows,
	// and titleBar.background the face of its head, each with the text
	// of its lightness (docs/DSK-012-theme-shaders.md, New roles).
	col := ui.Column(c).Width(sidebarWidthIn(winW)).Shrink(0).MinHeight(0)
	k, face, ok := themedFace(k, roleSideBarBackground)
	if ok {
		paintFace(col, k, roleSideBarBackground, face)
	}
	col.Children(func() {
		head := ui.Row(c).Height(titleH).Shrink(0).Gap(2).AlignItems(ui.Center).DragWindow()
		hk, face, ok := themedFace(k, roleTitleBarBackground)
		if ok {
			paintFace(head, hk, roleTitleBarBackground, face)
		}
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
			k := hk
			a.sidebarButton(c, k, right)
			if unified {
				if picker {
					a.spacePicker(c, k, true)
				}
				return
			}
			ui.Spacer(c)
			a.titleButtons(c, k, nil)
			if right {
				a.windowControls(c, k)
			}
		})
		padR, padL := sideInsets(right)
		if picker && !unified {
			ui.Row(c).Shrink(0).Padding(0, padR, 4, padL).Children(func() { a.spacePicker(c, k, true) })
		}
		reachR, reachL := listReach(right)
		list := ui.Scroll(c).Grow(1).MinHeight(0).Margin(0, -reachR, 0, -reachL).Padding(2, padR+reachR, 6, padL+reachL)
		a.sideList = list.Bounds()
		list.Children(func() {
			ui.Column(c).Gap(2).Role(ui.RoleTabList).Label(locales.TabListLabel()).Children(func() {
				a.projectsList(c, k)
			})
		})
		ui.Row(c).Shrink(0).Padding(4, padR, 8, padL).Gap(2).AlignItems(ui.Center).Children(func() {
			if iconButton(c, k, "settings-2", locales.SidebarSettings(), 30, 16).Tooltip(tip(locales.SidebarSettings(), &cmdSettings)).Clicked() {
				a.openSettings()
			}
			// New Project is an icon as the settings' is, its name in its
			// tooltip: a host's header has its + too, and ⇧⌘N.
			if iconButton(c, k, "folder-plus", cmdNewProject.Title(), 30, 16).Tooltip(tip(cmdNewProject.Title(), &cmdNewProject)).Clicked() {
				a.later(c, a.openNewProject)
			}
			ui.Spacer(c)
			a.hostButton(c, k)
		})
	})
}

// sideInsets are the sidebar list's paddings on its right and its left:
// the side the panes are on, inner, has the gap of the sidebar's edge
// beyond it, so the list's rows, and the current project's face, keep as
// far from the panes as from the window's edge.
func sideInsets(right bool) (r, l float32) {
	const window, panes = 10, 10 - gap
	if right {
		return window, panes
	}
	return panes, window
}

// listReach is how far the sidebar list's box reaches past its insets on its
// right and its left, so that the scroll's clip holds the current project's
// face (the group pad past the rows) on the panes' side, where the inset
// is short of it. The list's padding grows by the same amount, so no row
// moves; the reach lies over the sidebar's edge, which has no content.
func listReach(right bool) (r, l float32) {
	pr, pl := sideInsets(right)
	return max(sidebarGroupPad()-pr, 0), max(sidebarGroupPad()-pl, 0)
}

// sidebarEdge is the sidebar's inner edge, which drags its width; a double
// click gives it its default width back.
func (a *App) sidebarEdge(c *ui.Context, k *colors, right bool) {
	e := ui.Column(c.Key("sidebar-edge")).Width(gap).Shrink(0).Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label(locales.SidebarEdge()).
		// Over the list, whose box reaches over the edge (listReach), so the
		// edge keeps its whole width for the pointer.
		ZIndex(1)
	if prefs.SidebarHidden || right || ownControls || c.TitleBar().Right > 0 {
		if tk, face, ok := themedFace(k, roleTitleBarBackground); ok {
			e.Children(func() {
				top := ui.Box(c).Height(titleH).Shrink(0)
				paintFace(top, tk, roleTitleBarBackground, face)
			})
		}
	}
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
// lifted draws the active tab's pill over its project's group face.
func (a *App) sidebarTab(c *ui.Context, k *colors, i int, t *Tab, reserve float32, lifted bool) {
	active := i == a.active
	// Keyed by the tab's lasting ID, as spaceTab keys it: as the window
	// goes to show another space, the rows of both stay, not close and
	// open again.
	e := ui.Row(c.Key("tab-"+t.UID)).Height(sidebarRowH()).Shrink(0).Padding(0, 6, 0, sidebarTabLead()).Gap(sideGap).AlignItems(ui.Center).Radius(pillRadius(k, sidebarRowH(), k.radius(radiusM))).
		Role(ui.RoleTab).Selected(active)
	name, detail := t.label()
	e.Label(name + " " + detail)
	ck := k // the palette of the row's content, paired with its face
	if active {
		sidebarChosen(e, k, sidebarRowH(), lifted)
		ck = sidebarChosenColors(k)
		// A tab chosen otherwise than in the list, as by its keys, scrolls
		// into view, once: the list scrolls freely after.
		if a.revealed != t {
			a.revealed = t
			e.ScrollIntoView()
		}
	} else if e.Hovered() {
		paintFace(e, k, roleTabHoverBackground, hoverFace(k))
		ck = sidebarHoverColors(k)
	}
	a.tabEvents(c, e, k, i, t)
	hovered := e.Hovered()
	e.Children(func() {
		// The tiles take the room of the widest tab's, so that the names
		// line up; sidebar.icons "text" leaves the room bare.
		if !sidebarIconsText() {
			ui.Box(c).Width(reserve).Shrink(0).Children(func() { a.tiles(c, ck, t) })
		} else {
			ui.Box(c).Width(reserve).Shrink(0)
		}
		a.tabRest(c, ck, t, active, hovered)
	})
}

// tilesReserve is the room the tiles of a sidebar row take: that of the
// tab with the most panes, each tile past the first peeking out by
// tileStep, so that the names of every row line up. sidebar.icons "text"
// draws no tiles, so the room shrinks to a tile's, under the projects'
// folder glyphs.
func tilesReserve(tabs []*Tab) float32 {
	if sidebarIconsText() {
		return tileSlot
	}
	most := 1
	for _, t := range tabs {
		most = max(most, min(len(t.panes()), 3))
	}
	return float32(tileSlot + tileStep*(most-1))
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
			ui.Box(c).Absolute().Right(-2).Bottom(-2).Size(7, 7).Radius(k.capsule(7)).Background(hostDot(k, h))
		}
	})
	if h != nil {
		label, _, _ := hostStatus(h)
		box.Tooltip(label)
	}
}

// spaceTab is a tab of a session the window does not show, in the host
// view: its tiles and its name, as the window's tabs show, but neither
// chosen nor dragged; s is its panes' most urgent record, whose dot
// ends the row as a shown tab's does. It reports whether it was
// clicked.
func spaceTab(c *ui.Context, k *colors, key, name, detail string, s *rex.ProgramStatus, reserve float32, tiles func()) bool {
	return spaceTabWithAgent(c, k, key, name, detail, "", 0, 0, s, reserve, tiles)
}

func spaceTabWithAgent(c *ui.Context, k *colors, key, name, detail, agent string, agentFade float32, agentRem time.Duration, s *rex.ProgramStatus, reserve float32, tiles func()) bool {
	e := ui.Row(c.Key("tab-"+key)).Height(sidebarRowH()).Shrink(0).Padding(0, 6, 0, sidebarTabLead()).Gap(sideGap).AlignItems(ui.Center).Radius(pillRadius(k, sidebarRowH(), k.radius(radiusM))).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(name + " " + detail)
	if anim := motion(c); anim != nil {
		e.Transition(exitIn(ui.ElementTransition{Animation: anim, Position: true, Enter: tabMotion, Exit: tabMotion}, durExitMove))
	}
	if e.Hovered() {
		paintFace(e, k, roleTabHoverBackground, hoverFace(k))
		k = sidebarHoverColors(k)
	}
	e.Children(func() {
		if !sidebarIconsText() {
			ui.Box(c).Width(reserve).Shrink(0).Children(tiles)
		} else {
			ui.Box(c).Width(reserve).Shrink(0)
		}
		ui.Box(c).Grow(1).MinWidth(0).Height(18).Draw(func(p *ui.Painter, r ui.Rect) {
			fadeText(p, r, tabLabel(name, detail, k.textMuted, k.textFaint))
		})
		if agent != "" {
			agentBadge(c, k, agent, agentFade, agentRem)
		}
		if needsUser(s) || s != nil && s.State == "working" {
			statusDot(c, k, s, 5.5).Label(statusTip(s))
		}
	})
	return e.Clicked()
}

// savedTabLabel is how a tab of a layout names itself, and the program
// of its tile, from what the layout keeps: its name, else its focused
// pane's, else the program that ran there, else the pane's folder.
func savedTabLabel(t savedTab) (string, program) {
	n := focusedLeaf(t)
	prog := shellTile
	if pr, ok := extProgram(n); ok {
		prog = pr
	} else if n.Prog != "" {
		prog = programOf(n.Prog)
	}
	return cmp.Or(t.Name, n.Name, prog.Name, folderName(n.Dir), "Shell"), prog
}

// savedTabFace is how a tab of a layout no window shows names itself, and
// the program of its tile, as the window's tab would: from what its
// machine's last session list (infos) says of its focused pane's session,
// the title its program set, the program and its folder; else from what
// the layout keeps (savedTabLabel). A tab its user named keeps the name
// but still takes its icon from the session. No pane attaches for it.
func savedTabFace(t savedTab, infos map[string]rex.SessionInfo) (name, detail string, prog program) {
	name, prog = savedTabLabel(t)
	n := focusedLeaf(t)
	in, ok := infos[n.SID]
	if _, isExt := extProgram(n); n.SID == "" || isExt || !ok || in.Exited {
		return name, "", prog
	}
	p := &Pane{SID: n.SID, Name: n.Name, info: in, startDir: n.Dir}
	if t.Name != "" {
		return t.Name, "", p.program()
	}
	name, detail = p.label()
	return name, detail, p.program()
}

// focusedLeaf is the pane of tab t that has the focus, as the layout
// keeps it: its focused float, else its focused tiled pane, else its
// first.
func focusedLeaf(t savedTab) savedNode {
	if f := t.FocusFloat - 1; f >= 0 && f < len(t.Floats) && t.Floats[f].Pane != nil {
		return *layout.FirstLeaf(t.Floats[f].Pane)
	}
	if t.Root != nil {
		if l := layout.Leaves(t.Root); t.Focus >= 0 && t.Focus < len(l) {
			return *l[t.Focus]
		}
	}
	if r := t.Roots(); len(r) > 0 {
		return *layout.FirstLeaf(r[0])
	}
	return savedNode{}
}
