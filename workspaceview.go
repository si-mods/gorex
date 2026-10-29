package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// Run is set here: they open windows, whose debug hook lists the commands.
func init() {
	cmdNewWindow.Run = func(a *App) { a.newWindowCmd() }
	cmdMoveTabWindow.Run = func(a *App) { a.moveTabToNewWindow(a.tab()) }
}

// workspaceChip shows the window's workspace, which the interface calls a
// session: a click lists the workspaces, to show another, make one or
// rename this one. In the sidebar it is wide, a row of its own.
func (a *App) workspaceChip(c *ui.Context, k *colors, wide bool) {
	w := a.ws
	if w == nil {
		return
	}
	chip := ui.Row(c.Key("session-chip")).Gap(7).Padding(3, 6, 3, 8).Radius(9).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(locales.SessionLabel(w.Name)).Tooltip(locales.SessionLabel(w.Name))
	if wide {
		chip.Height(30).Grow(1).MinWidth(0)
	} else {
		chip.MaxWidth(180).Shrink(0)
	}
	if chip.Hovered() || a.wsOpen {
		chip.Background(k.hover)
	}
	if chip.Clicked() && !a.wsRenaming {
		a.wsOpen = !a.wsOpen
	}
	glyph := k.textMuted
	if _, ok := spaceColorHex(w.Color); ok {
		glyph = k.accent
	}
	chip.Children(func() {
		ui.Icon(c, icon("layers")).Size(15, 15).TextColor(glyph).Shrink(0)
		if a.wsRenaming {
			a.workspaceRenameField(c, w.Name)
			return
		}
		name := ui.Text(c, w.Name).FontSize(13).FontWeight(600).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").MinWidth(0)
		if wide {
			name.Grow(1)
		}
		if s := a.st.status(w); s != nil && s.State != "idle" {
			statusDot(c, k, s, 7)
		}
		ui.Icon(c, icon("chevron-down")).Size(13, 13).TextColor(k.textFaint).Shrink(0)
	})
	a.workspacePopover(c, k, chip)
}

// workspaceRenameField renames the workspace in place, as tabs are: Enter
// or moving the focus away keeps the name, Escape goes back.
func (a *App) workspaceRenameField(c *ui.Context, name string) {
	in := ui.TextInput(c, &a.wsRenameText).Placeholder(name).Grow(1).MinWidth(60).Height(22).FontSize(13).AutoFocus()
	done := func(keep bool) {
		if keep {
			a.renameWorkspace(a.wsRenameText)
		}
		a.wsRenaming, a.wsRenameFocus = false, false
		if t := a.tab(); t != nil {
			a.focusReq = t.Focus
		}
	}
	switch {
	case in.Shortcut(0, ui.KeyEscape):
		done(false)
	case in.Submitted():
		done(true)
	case in.Focused():
		a.wsRenameFocus = true
	case a.wsRenameFocus:
		done(true) // the focus went elsewhere
	}
}

// startWorkspaceRename opens the field that renames the workspace.
func (a *App) startWorkspaceRename() {
	if a.ws != nil {
		a.wsOpen, a.wsRenaming, a.wsRenameText, a.wsRenameFocus = false, true, a.ws.Name, false
		a.focusReq = nil // the field takes the focus
	}
}

// askCloseWorkspace asks before closing the workspace.
func (a *App) askCloseWorkspace() {
	if a.ws != nil {
		a.wsOpen, a.wsConfirm = false, true
		a.focusReq = nil
	}
}

// workspacePopover lists the workspaces under the chip: their tabs, the
// window's checked, and those other windows show; then New Session,
// Rename and Close.
func (a *App) workspacePopover(c *ui.Context, k *colors, anchor ui.Element) {
	ui.PopoverBase(c, anchor, &a.wsOpen, func(panel ui.Element) {
		_, winH := c.Size()
		panel.Margin(8, 0, 0, 0).MaxHeight(winH-24-titleH).Width(280).Padding(6).Radius(14).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 32, 0, k.shadowFocused)
		ui.Scroll(c).MinHeight(0).Children(func() {
			ui.Column(c).Gap(2).Children(func() { a.workspaceList(c, k) })
		})
	})
}

// workspaceList is the popover's body: a row for each workspace, then
// the actions.
func (a *App) workspaceList(c *ui.Context, k *colors) {
	ui.Text(c, locales.SessionListTitle()).FontSize(12).FontWeight(600).TextColor(k.textFaint).Padding(6, 8, 4, 8)
	for _, w := range a.st.workspaces {
		current := w == a.ws
		detail := workspaceDetail(w, a)
		row := ui.Row(c.Key(w.ID)).Height(40).Padding(0, 8).Gap(9).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Role(ui.RoleButton).Label(w.Name + ", " + detail)
		if row.Hovered() {
			row.Background(k.hover)
		}
		if row.Clicked() {
			a.wsOpen = false
			a.later(c, func() { a.showWorkspace(w) })
		}
		row.Children(func() {
			ui.Box(c).Size(15, 15).Shrink(0).Children(func() {
				if current {
					ui.Icon(c, icon("check")).Size(15, 15).TextColor(k.text)
				}
			})
			spaceDot(c, k, w)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, w.Name).FontSize(13.5).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
				ui.Text(c, detail).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
			})
			if s := a.st.status(w); s != nil && s.State != "idle" {
				statusDot(c, k, s, 7)
			}
			if w.app != nil && !current {
				ui.Icon(c, icon("app-window")).Size(14, 14).TextColor(k.textFaint).Shrink(0).Tooltip(locales.SessionElsewhere())
			}
		})
	}
	a.spaceColorPicker(c, k)
	ui.Box(c).Height(1).Margin(4, 6).Background(k.panelBorder)
	action := func(glyph, label string, run func()) {
		row := ui.Row(c.Key("action-"+glyph)).Height(32).Padding(0, 8).Gap(9).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Role(ui.RoleButton).Label(label)
		if row.Hovered() {
			row.Background(k.hover)
		}
		if row.Clicked() {
			a.wsOpen = false
			a.later(c, run)
		}
		row.Children(func() {
			ui.Icon(c, icon(glyph)).Size(15, 15).TextColor(k.textMuted).Shrink(0)
			ui.Text(c, label).FontSize(13).TextColor(k.text).SingleLine().Ellipsis("…")
		})
	}
	action("plus", cmdNewSession.Title(), a.newWorkspace)
	action("pencil", cmdRenameSession.Title(), a.startWorkspaceRename)
	action("trash-2", cmdCloseSession.Title(), a.askCloseWorkspace)
}

// spaceColorPicker is the popover's colors for the window's space: the
// default look, the presets, and a field for a custom #rrggbb. A choice
// keeps the popover open, so that the window shows it at once.
func (a *App) spaceColorPicker(c *ui.Context, k *colors) {
	if a.ws == nil {
		return
	}
	ui.Text(c, locales.SpaceColor()).FontSize(12).FontWeight(600).TextColor(k.textFaint).Padding(10, 8, 4, 8)
	base := colorsOf(c)
	ui.Row(c).Gap(5).Wrap().Padding(0, 8, 4, 8).Children(func() {
		a.spaceSwatch(c, k, "", locales.SpaceColorDefault(), base.bgMid, true)
		for _, p := range spacePalette {
			a.spaceSwatch(c, k, p.ID, strings.ToUpper(p.ID[:1])+p.ID[1:], ui.Hex(p.Hex), false)
		}
	})
	in := ui.TextInput(c, &a.wsColorText).FontSize(12).Height(24).Margin(0, 8, 6, 8).Placeholder(locales.SpaceColorCustom())
	if in.Submitted() {
		a.useSpaceColorText()
	}
}

// spaceSwatch is one of the popover's colors: the space's own, ringed,
// and the one that takes it when clicked. Outlined is the default look,
// which shows the window's own gradient.
func (a *App) spaceSwatch(c *ui.Context, k *colors, id, label string, face ui.Color, outlined bool) ui.Element {
	b := ui.Box(c).Size(20, 20).Radius(10).Shrink(0).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label).Background(face)
	switch {
	case a.ws != nil && a.ws.Color == id:
		b.Border(2, k.text)
	case outlined:
		b.Border(1, k.textFaint)
	}
	if b.Clicked() {
		a.setSpaceColor(id)
	}
	return b
}

// setSpaceColor gives the window's space a color, which the layout keeps.
func (a *App) setSpaceColor(color string) {
	if a.ws != nil && a.ws.Color != color {
		a.ws.Color = color
		a.changed()
	}
}

// useSpaceColorText takes the color the picker's field holds, when it
// names one: a preset's id, or a #rrggbb.
func (a *App) useSpaceColorText() {
	v := strings.TrimSpace(a.wsColorText)
	hex, ok := spaceColorHex(v)
	if !ok {
		return
	}
	if strings.HasPrefix(v, "#") {
		v = hex
	}
	a.setSpaceColor(v)
}

// spaceDot is the mark of a space's color before its name in the
// popover: a dot of its base color, or only an outline for the default
// look.
func spaceDot(c *ui.Context, k *colors, w *Workspace) ui.Element {
	dot := ui.Box(c).Size(8, 8).Radius(4).Shrink(0)
	if hex, ok := spaceColorHex(w.Color); ok {
		return dot.Background(ui.Hex(hex))
	}
	return dot.Border(1, k.textFaint)
}

// workspaceDetail says of a workspace how many tabs it has, and when
// another window than a's shows it.
func workspaceDetail(w *Workspace, a *App) string {
	if w.app != nil && w.app != a {
		return locales.SessionTabsElsewhere(w.tabCount())
	}
	return locales.SessionTabs(w.tabCount())
}

// sessionItems are the rows of the palette listing the workspaces: the
// window's first, then the others in their order, and New Session.
func (a *App) sessionItems(match func(string) bool) []paletteItem {
	var items []paletteItem
	if a.st == nil {
		return nil
	}
	for _, w := range a.st.workspaces {
		if !match(w.Name) {
			continue
		}
		glyph := "layers"
		switch {
		case w == a.ws:
			glyph = "check"
		case w.app != nil:
			glyph = "app-window"
		}
		it := paletteItem{title: w.Name, detail: workspaceDetail(w, a), glyph: glyph, status: a.st.status(w), run: func() { a.showWorkspace(w) }}
		if hex, ok := spaceColorHex(w.Color); ok {
			it.dot, it.hasDot = ui.Hex(hex), true
		}
		if w == a.ws {
			items = append([]paletteItem{it}, items...)
		} else {
			items = append(items, it)
		}
	}
	if title := cmdNewSession.Title(); match(title) || match(cmdNewSession.English()) {
		items = append(items, paletteItem{title: title, keys: cmdNewSession.Keys, glyph: "plus", run: a.newWorkspace})
	}
	return items
}

// workspaceConfirm asks before Close Space ends the workspace's
// terminals.
func (a *App) workspaceConfirm(c *ui.Context, k *colors) {
	if !a.wsConfirm || a.ws == nil {
		a.wsConfirm = false
		return
	}
	n := 0
	for _, t := range a.tabs {
		n += len(t.panes())
	}
	name := a.ws.Name
	was := a.wsConfirm
	ui.DialogBase(c, &a.wsConfirm, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		panel.Width(380).Padding(20).Radius(16).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.06)).Role(ui.RoleDialog).Label(locales.SessionCloseTitle(name))
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, locales.SessionCloseTitle(name)).FontSize(15).FontWeight(700).TextColor(k.text)
			ui.Text(c, locales.SessionCloseBody(n)).FontSize(13).TextColor(k.textMuted).LineHeight(1.35)
			ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.SessionCancel()).Clicked() {
					a.wsConfirm = false
				}
				if dangerButton(c, k, cmdCloseSession.Title()).Clicked() {
					a.wsConfirm = false
					a.later(c, a.closeWorkspace)
				}
			})
		})
	})
	if was && !a.wsConfirm {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// dangerButton is a button that destroys: white on red, darker under the
// pointer and pressed.
func dangerButton(c *ui.Context, k *colors, label string) ui.Element {
	b := ui.Row(c).Height(32).Padding(0, 14).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(label)
	bg := k.danger
	switch {
	case b.Pressed():
		bg = bg.Mix(ui.Hex("#000000"), 0.25)
	case b.Hovered():
		bg = bg.Mix(ui.Hex("#000000"), 0.12)
	}
	b.Background(bg).Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	b.Children(func() {
		ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(ui.Hex("#ffffff"))
	})
	return b
}

// windowTitle is the title of the window, which the Window menu and the
// taskbar show: the workspace's name and the active tab's.
func (a *App) windowTitle() string {
	title := ""
	if t := a.tab(); t != nil {
		name, detail := t.label()
		title = strings.TrimSpace(name + " " + detail)
	}
	if a.ws != nil {
		title = locales.WindowTitle(a.ws.Name, title)
	}
	return title
}
