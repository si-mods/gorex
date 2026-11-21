package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// The title bar's `host › project ▾` (docs/MOD-002-projects.md, "Tabs on top"):
// the host and the project the window shows, and a menu of the space's
// hosts, their projects with their tabs' counts, their tabs with no
// project, and New Project….

// pickerWorkspaces are the workspaces the picker and the switcher list,
// in the sidebar's order: each host shown, its projects of the space by
// last use, then its tabs with no project.
func (a *App) pickerWorkspaces() []*Workspace {
	if a.st == nil {
		return nil
	}
	space := shownSpace()
	var out []*Workspace
	for _, h := range a.allHosts() {
		if !a.hostShown(h, space) {
			continue
		}
		recent, older := a.projectsOf(h, space)
		out = append(append(out, recent...), older...)
		if w := a.st.noProjectOf(h); w != nil {
			out = append(out, w)
		}
	}
	return out
}

// showProjectN shows the nth project the picker lists, from 1; 9 is the
// last. A number none has does nothing.
func (a *App) showProjectN(n int) {
	ws := a.pickerWorkspaces()
	if n == 9 || n > len(ws) {
		n = len(ws)
	}
	if n < 1 {
		return
	}
	a.showWorkspace(ws[n-1])
}

// projectPicker is the title bar's `host › project ▾`.
func (a *App) projectPicker(c *ui.Context, k *colors) {
	w := a.ws
	if w == nil || a.st == nil {
		return
	}
	name := w.Name
	if w.Dir == "" {
		name = locales.ProjectsNoProject()
	}
	host := a.hostName(w.host)
	label := host + " › " + name
	chip := ui.Row(c.Key("project-picker")).Height(28).Gap(6).Padding(0, 8).Radius(pillRadius(28, radiusM)).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(label).Tooltip(label).MaxWidth(320).Shrink(1).MinWidth(60)
	if chip.Hovered() || a.pickerOpen {
		chip.Background(hoverFace(k))
	}
	if chip.Clicked() {
		a.pickerOpen = !a.pickerOpen
	}
	chip.Children(func() {
		ui.Text(c, host).FontSize(fontBody).TextColor(k.textMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		glyphIcon(c, "chevron-right", 12, k.textFaint).Shrink(0)
		ui.Text(c, name).FontSize(fontBody).FontWeight(600).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if s := a.st.status(w); needsUser(s) {
			statusDot(c, k, s, 7).Label(statusTip(s))
		}
		glyphIcon(c, "chevron-down", 13, k.textFaint).Shrink(0)
	})
	ui.PopoverBase(c, chip, &a.pickerOpen, func(panel ui.Element) {
		popoverMotion(c, chip, panel)
		_, winH := c.Size()
		menuStyleOf(k, c.Theme().Dark).Panel(panel.Margin(6, 0, 0, 0).Width(340).MaxHeight(winH-24-titleH), 0)
		ui.Scroll(c.Key("picker-list")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Children(func() { a.pickerList(c, k) })
		})
		ui.Box(c).Height(1).Margin(4, 6).Background(k.panelBorder).Shrink(0)
		a.menuRow(c, k, "picker-new", false, cmdNewProject.Title(), cmdNewProject.Keys, nil, a.openNewProject).Shrink(0)
	})
}

// pickerList is the picker's hosts and their workspaces.
func (a *App) pickerList(c *ui.Context, k *colors) {
	space := shownSpace()
	// A host's header lines up with its rows.
	pad := menuStyleOf(k, c.Theme().Dark).RowPadding
	for _, h := range a.allHosts() {
		if !a.hostShown(h, space) {
			continue
		}
		ui.Row(c.Key("picker-host-"+hostKey(h))).Height(26).Padding(4, pad, 0, pad).Gap(6).AlignItems(ui.Center).Children(func() {
			a.hostSectionIcon(c, k, h)
			ui.Text(c, a.hostName(h)).FontSize(fontSubhead).FontWeight(600).TextColor(k.textMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		})
		recent, older := a.projectsOf(h, space)
		ws := append(recent, older...)
		if w := a.st.noProjectOf(h); w != nil {
			ws = append(ws, w)
		}
		for _, w := range ws {
			name, glyph := w.Name, "folder"
			if w.Dir == "" {
				name, glyph = locales.ProjectsNoProject(), "layers"
			}
			// A host's copy, while it is away, is listed but cannot be
			// shown: its row is inert.
			offline := w.offline()
			var run func()
			if !offline {
				run = func() { a.showWorkspace(w) }
			}
			row := a.menuRow(c, k, "picker-"+openKey(h, w.ID), w == a.ws, name, fmt.Sprint(w.tabCount()), func(ink ui.Color, _ bool) {
				glyphIcon(c, glyph, 14, ink).Shrink(0)
			}, run)
			if offline {
				dimmed(row, 0.5)
			}
		}
	}
}
