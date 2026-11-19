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
}

// workspaceRenameField renames the workspace in place, as tabs are: Enter
// or moving the focus away keeps the name, Escape goes back.
func (a *App) workspaceRenameField(c *ui.Context, k *colors, name string) {
	in := textField(c, k, &a.wsRenameText, fieldCompact).Placeholder(name).Grow(1).MinWidth(60).AutoFocus()
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

// popR and popPad are the workspace popover's radius and padding, which
// its rows' corners are concentric with.
const popR, popPad = radiusXL, 6

// workspaceDetail says of a workspace its n tabs, and when another window
// than a's shows it.
func workspaceDetail(w *Workspace, a *App, n int) string {
	if w.app != nil && w.app != a {
		return locales.SessionTabsElsewhere(n)
	}
	return locales.SessionTabs(n)
}

// sessionItems are the rows of the palette listing the projects of the
// space shown and the hosts' tabs with no project: the window's first,
// then the others in the sidebar's order, and New Project. A host's copy,
// while it is away, is not listed.
func (a *App) sessionItems(match func(string) bool) []paletteItem {
	var items []paletteItem
	if a.st == nil {
		return nil
	}
	for _, w := range a.pickerWorkspaces() {
		name := w.Name
		if w.Dir == "" {
			name = locales.ProjectsNoProject()
		}
		if w.offline() || !match(name) {
			continue
		}
		glyph := "folder"
		switch {
		case w == a.ws:
			glyph = "check"
		case w.app != nil:
			glyph = "app-window"
		case w.Dir == "":
			glyph = "layers"
		}
		detail := a.hostName(w.host) + " · " + workspaceDetail(w, a, w.tabCount())
		it := paletteItem{title: name, detail: detail, glyph: glyph, status: a.st.status(w), run: func() { a.showWorkspace(w) }}
		if hex, ok := spaceColorHex(w.Color); ok {
			it.dot, it.hasDot = ui.Hex(hex), true
		}
		if w == a.ws {
			items = append([]paletteItem{it}, items...)
		} else {
			items = append(items, it)
		}
	}
	if title := cmdNewProject.Title(); match(title) || match(cmdNewProject.English()) {
		items = append(items, paletteItem{title: title, keys: cmdNewProject.Keys, glyph: "folder-plus", run: a.openNewProject})
	}
	if title := cmdNewSession.Title(); match(title) || match(cmdNewSession.English()) {
		items = append(items, paletteItem{title: title, keys: cmdNewSession.Keys, glyph: "plus", run: func() { a.newNoProjectTab(a.currentHost()) }})
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
		n += len(t.allPanes())
	}
	name := a.ws.Name
	was := a.wsConfirm
	ui.DialogBase(c, &a.wsConfirm, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(380).Padding(20).Radius(radiusXXL), k.panel).Border(1, k.panelBorder)).Role(ui.RoleDialog).Label(locales.SessionCloseTitle(name))
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, locales.SessionCloseTitle(name)).FontSize(fontTitle).FontWeight(700).TextColor(k.text)
			ui.Text(c, locales.SessionCloseBody(n)).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
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
	b := ui.Row(c).Height(32).Padding(0, 14).Radius(radiusM).AlignItems(ui.Center).Cursor(ui.CursorPointer).
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
		ui.Text(c, label).FontSize(fontBody).FontWeight(600).TextColor(ui.Hex("#ffffff"))
	})
	return b
}

// windowTitle is the title of the window, which the Window menu and the
// taskbar show: the workspace's name and the active tab's, or the
// workspace's alone while it has no tab.
func (a *App) windowTitle() string {
	title := ""
	if t := a.tab(); t != nil {
		name, detail := t.label()
		title = strings.TrimSpace(name + " " + detail)
	}
	if a.ws != nil {
		if title == "" {
			return a.ws.Name
		}
		title = locales.WindowTitle(a.ws.Name, title)
	}
	return title
}
