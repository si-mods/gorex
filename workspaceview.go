package main

import (
	"strings"

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
	chip.Children(func() {
		ui.Icon(c, icon("layers")).Size(15, 15).TextColor(k.textMuted).Shrink(0)
		if a.wsRenaming {
			a.workspaceRenameField(c, w.Name)
			return
		}
		name := ui.Text(c, w.Name).FontSize(13).FontWeight(600).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").MinWidth(0)
		if wide {
			name.Grow(1)
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
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, w.Name).FontSize(13.5).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
				ui.Text(c, detail).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
			})
			if w.app != nil && !current {
				ui.Icon(c, icon("app-window")).Size(14, 14).TextColor(k.textFaint).Shrink(0).Tooltip(locales.SessionElsewhere())
			}
		})
	}
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

// workspaceDetail says of a workspace how many tabs it has, and when
// another window than a's shows it.
func workspaceDetail(w *Workspace, a *App) string {
	d := locales.SessionTabs(w.tabCount())
	if w.app != nil && w.app != a {
		d += " · " + locales.SessionElsewhere()
	}
	return d
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
		it := paletteItem{title: w.Name, detail: workspaceDetail(w, a), glyph: glyph, run: func() { a.showWorkspace(w) }}
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

// workspaceConfirm asks before Close Session ends the workspace's
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
				if ui.Button(c, cmdCloseSession.Title()).Clicked() {
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

// windowTitle is the title of the window, which the Window menu and the
// taskbar show: the workspace's name and the active tab's.
func (a *App) windowTitle() string {
	title := ""
	if t := a.tab(); t != nil {
		name, detail := t.label()
		title = strings.TrimSpace(name + " " + detail)
	}
	if a.ws != nil {
		title = a.ws.Name + " — " + title
	}
	return title
}
