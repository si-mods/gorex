package main

import (
	"cmp"
	"log"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Spaces (docs/projects.md, "Spaces"): a space is only a name on the
// projects of the hosts. The picker atop the sidebar lists the names the
// hosts' projects carry, Default (a project of no space), the names the
// hosts offer from their layouts of before projects (Store.Spaces), and
// the spaces made here that no project carries yet. Their colors and
// order are this client's own, in its settings.

// clientSpace is what this client keeps of a space: its color, and that
// it was made here (Made), which keeps it in the picker while no project
// carries it.
type clientSpace struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Made  bool   `json:"made,omitempty"`
}

// spaceRename is a rename of a space that waits for a host to connect.
type spaceRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// shownSpace is the space the windows show, "" for Default.
func shownSpace() string { return prefs.Space }

// spaceLabel is how the UI names space name: Default for "".
func spaceLabel(name string) string {
	if name == "" {
		return locales.SpaceDefault()
	}
	return name
}

// spaceNames are the spaces the picker lists, in this client's order:
// Default first, then those of the client's order, then the others by
// name.
func (st *store) spaceNames() []string {
	carried := map[string]bool{"": true}
	if st != nil {
		for _, w := range st.workspaces {
			if w.Dir != "" {
				carried[w.Space] = true
			}
		}
		for _, n := range st.base.Spaces {
			carried[n] = true
		}
		for _, h := range st.hosts {
			if h.lay != nil {
				for _, n := range h.lay.base.Spaces {
					carried[n] = true
				}
			}
		}
	}
	for _, s := range prefs.Spaces {
		if s.Made {
			carried[s.Name] = true
		}
	}
	names := []string{""}
	for _, s := range prefs.Spaces {
		if s.Name != "" && carried[s.Name] && !slices.Contains(names, s.Name) {
			names = append(names, s.Name)
		}
	}
	var rest []string
	for n := range carried {
		if !slices.Contains(names, n) {
			rest = append(rest, n)
		}
	}
	slices.SortFunc(rest, func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) })
	return append(names, rest...)
}

// clientSpaceOf is this client's entry of space name, nil for none.
func clientSpaceOf(name string) *clientSpace {
	for i := range prefs.Spaces {
		if prefs.Spaces[i].Name == name {
			return &prefs.Spaces[i]
		}
	}
	return nil
}

// spaceColor is the color this client gives space name, "" for none.
func spaceColor(name string) string {
	if s := clientSpaceOf(name); s != nil {
		return s.Color
	}
	return ""
}

// setSpaceColorOf gives space name a color on this client.
func setSpaceColorOf(name, color string) {
	if s := clientSpaceOf(name); s != nil {
		s.Color = color
	} else {
		prefs.Spaces = append(prefs.Spaces, clientSpace{Name: name, Color: color})
	}
	saveSettings()
}

// moveSpace moves space name by d in this client's order.
func (st *store) moveSpace(name string, d int) {
	names := st.spaceNames()
	i := slices.Index(names, name)
	j := i + d
	if i <= 0 || j <= 0 || j >= len(names) {
		return // Default stays first
	}
	names[i], names[j] = names[j], names[i]
	var next []clientSpace
	for _, n := range names {
		if s := clientSpaceOf(n); s != nil {
			next = append(next, *s)
		} else {
			next = append(next, clientSpace{Name: n})
		}
	}
	prefs.Spaces = next
	saveSettings()
}

// showSpaceNamed shows space name in every window: the sidebar and the
// title bar list its projects, and a window whose project is of another
// space shows the first of this one.
func (a *App) showSpaceNamed(name string) {
	if prefs.Space == name {
		return
	}
	prefs.Space = name
	saveSettings()
	for _, o := range a.peers() {
		if w := o.ws; w != nil && w.Dir != "" && w.Space != name {
			if next := o.firstOfSpace(name); next != nil {
				o.showWorkspace(next)
			}
		}
	}
}

// firstOfSpace is the project of space name the window shows first: the
// latest used of this machine's, else of a host's connected, else the
// window's host's tabs with no project.
func (a *App) firstOfSpace(name string) *Workspace {
	if a.st == nil {
		return nil
	}
	for _, h := range a.allHosts() {
		for _, w := range sortedWorkspaces(a.st.hostWorkspaces(h)) {
			if w.Dir != "" && w.Space == name && !w.offline() && w.app == nil {
				return w
			}
		}
	}
	if w := a.st.noProjectOf(a.currentHost()); w != nil && w.app == nil {
		return w
	}
	return nil
}

// newSpace adds a space made here, shown at once; it stays in the picker,
// empty, until a project moves into it.
func (a *App) newSpace(name string) {
	name = layout.Label(name)
	if name == "" {
		return
	}
	if s := clientSpaceOf(name); s != nil {
		s.Made = true
	} else {
		prefs.Spaces = append(prefs.Spaces, clientSpace{Name: name, Made: true})
	}
	saveSettings()
	a.showSpaceNamed(name)
}

// renameSpace renames space from to to: each connected host renames it
// on its projects (spaces.rename), and a host not connected does once it
// connects (sendSpaceRenames); this client's color, order and space shown
// follow.
func (a *App) renameSpace(from, to string) {
	to = layout.Label(to)
	if a.st == nil || to == "" || from == to || from == "" {
		return
	}
	st := a.st
	for _, h := range a.allHosts() {
		if h != nil && !serverMade(h) {
			if prefs.SpaceRenames == nil {
				prefs.SpaceRenames = map[string][]spaceRename{}
			}
			prefs.SpaceRenames[h.ID] = append(prefs.SpaceRenames[h.ID], spaceRename{From: from, To: to})
			continue
		}
		st.sendOn(h, rex.LayoutCommand{Op: "spaces.rename", From: from, To: to}, nil)
	}
	if s := clientSpaceOf(from); s != nil {
		s.Name = to
	}
	// The copies of hosts away show the new name too.
	for _, w := range st.workspaces {
		if w.Space == from {
			w.Space = to
		}
	}
	if prefs.Space == from {
		prefs.Space = to
	}
	saveSettings()
}

// sendSpaceRenames sends host h the renames of spaces that waited for it,
// in order, now that it is connected. Call it on the main thread.
func (st *store) sendSpaceRenames(h *hostConn) {
	renames := prefs.SpaceRenames[h.ID]
	if len(renames) == 0 || !serverMade(h) {
		return
	}
	delete(prefs.SpaceRenames, h.ID)
	saveSettings()
	for _, r := range renames {
		st.sendOn(h, rex.LayoutCommand{Op: "spaces.rename", From: r.From, To: r.To}, func(res rex.LayoutCommand) {
			if res.Code != "" {
				log.Printf("gorex: renaming space %q to %q on %s: %s", r.From, r.To, h.label(), res.Error)
			}
		})
	}
}

// moveProjectToSpace moves project w to space name, on its host.
func (a *App) moveProjectToSpace(w *Workspace, name string) {
	if a.st == nil || w == nil || w.Dir == "" || w.offline() {
		return
	}
	a.askOn(w.host, rex.LayoutCommand{Op: "project.update", Space: w.ID, SpaceName: &name}, func(res rex.LayoutCommand) {
		if res.Code == "" {
			w.Space = name
			if c := clientSpaceOf(name); c != nil && c.Made {
				// Carried now: it no longer needs keeping.
				c.Made = false
				saveSettings()
			}
		}
	})
}

// spaceUI is the state of the space picker and its dialogs, of a window.
type spaceUI struct {
	open bool
	// dialog is "new" or "edit" while the dialog of New Space… or Edit
	// Spaces… shows; name is the new space's name, names the edit's
	// fields by the space's name.
	dialog string
	name   string
	names  map[string]string
}

// spacePicker is the space's button atop the sidebar and in the title
// bar: its color and name, and a menu of the spaces, New Space… and Edit
// Spaces….
func (a *App) spacePicker(c *ui.Context, k *colors, wide bool) {
	name := shownSpace()
	label := locales.SpacePickerLabel(spaceLabel(name))
	chip := ui.Row(c.Key("space-picker")).Height(28).Gap(7).Padding(0, 8, 0, 8).Radius(pillRadius(28, 9)).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(label).Tooltip(label)
	if wide {
		chip.Grow(1).MinWidth(0)
	} else {
		chip.MaxWidth(180).Shrink(0)
	}
	if chip.Hovered() || a.spaces.open {
		chip.Background(hoverFace(k))
	}
	if chip.Clicked() {
		a.spaces.open = !a.spaces.open
	}
	chip.Children(func() {
		spaceSwatchMark(c, k, spaceColor(name), 10)
		t := ui.Text(c, spaceLabel(name)).FontSize(13).FontWeight(600).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").MinWidth(0)
		if wide {
			t.Grow(1)
		}
		glyphIcon(c, "chevron-down", 13, k.textFaint).Shrink(0)
	})
	ui.PopoverBase(c, chip, &a.spaces.open, func(panel ui.Element) {
		glassPanel(panel.Margin(6, 0, 0, 0).Width(240).Padding(popPad).Radius(popR), k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 32, 0, k.shadowFocused)
		ui.Column(c).Gap(2).Children(func() {
			for _, n := range a.st.spaceNames() {
				a.menuRow(c, k, "space-"+n, n == name, spaceLabel(n), "", func() {
					spaceSwatchMark(c, k, spaceColor(n), 10)
				}, func() { a.showSpaceNamed(n) })
			}
			ui.Box(c).Height(1).Margin(4, 6).Background(k.panelBorder)
			a.menuRow(c, k, "space-new", false, locales.SpaceNew(), "", nil, func() {
				a.spaces.dialog, a.spaces.name = "new", ""
			})
			a.menuRow(c, k, "space-edit", false, locales.SpaceEdit(), "", nil, func() {
				a.spaces.dialog, a.spaces.names = "edit", map[string]string{}
			})
		})
	})
}

// menuRow is a row of a popover menu of GoRex's own: a check when chosen,
// a mark, its label and a detail; a click closes the menus and runs run.
func (a *App) menuRow(c *ui.Context, k *colors, key string, chosen bool, label, detail string, mark func(), run func()) ui.Element {
	row := ui.Row(c.Key(key)).Height(30).Padding(0, 8).Gap(8).Radius(concentric(popR, 1+popPad)).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleMenuItem).Label(label)
	if row.Hovered() {
		row.Background(k.hover)
	}
	if row.Clicked() {
		a.closeMenus()
		a.later(c, run)
	}
	row.Children(func() {
		ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
			if chosen {
				glyphIcon(c, "check", 14, k.text)
			}
		})
		if mark != nil {
			mark()
		}
		ui.Text(c, label).FontSize(13).TextColor(k.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if detail != "" {
			ui.Text(c, detail).FontSize(11.5).TextColor(k.textFaint).SingleLine().Shrink(0)
		}
	})
	return row
}

// closeMenus closes the window's popovers of spaces and projects.
func (a *App) closeMenus() {
	a.spaces.open, a.pickerOpen, a.wsOpen = false, false, false
}

// spaceSwatchMark is a space's color: a dot of it, or an outline for none.
func spaceSwatchMark(c *ui.Context, k *colors, color string, size float32) ui.Element {
	dot := ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0)
	if hex, ok := spaceColorHex(color); ok {
		return dot.Background(ui.Hex(hex))
	}
	return dot.Border(1, k.textFaint)
}

// spaceDialog is the dialog of New Space… or Edit Spaces…, while one
// shows.
func (a *App) spaceDialog(c *ui.Context, k *colors) {
	s := &a.spaces
	if s.dialog == "" || a.st == nil {
		return
	}
	open := true
	title := locales.SpaceNewTitle()
	if s.dialog == "edit" {
		title = locales.SpaceEditTitle()
	}
	ui.DialogBase(c.Key("space-dialog"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		_, winH := c.Size()
		glassPanel(panel.Width(400).MaxHeight(winH-24).Padding(20).Radius(16), k.panel).Border(1, k.panelBorder).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Role(ui.RoleDialog).Label(title)
		ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(k.text).Margin(0, 0, 10, 0)
		if s.dialog == "new" {
			in := textField(c.Key("space-name"), k, &s.name, fieldRegular).Label(locales.SpaceNameLabel()).Placeholder(locales.SpaceNameHint()).AutoFocus()
			done := in.Submitted()
			ui.Row(c).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.SessionCancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.SpaceCreate()).Disabled(strings.TrimSpace(s.name) == "").Clicked() {
					done = true
				}
			})
			if done && strings.TrimSpace(s.name) != "" {
				name := s.name
				open = false
				a.later(c, func() { a.newSpace(name) })
			}
			return
		}
		ui.Scroll(c.Key("space-list")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for i, n := range a.st.spaceNames() {
					a.spaceEditRow(c, k, i, n)
				}
			})
		})
		ui.Row(c).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
			if ui.PrimaryButton(c, locales.SpaceDone()).Clicked() {
				open = false
				a.later(c, a.applySpaceNames)
			}
		})
	})
	if !open {
		if s.dialog == "edit" {
			a.later(c, a.applySpaceNames)
		}
		s.dialog = ""
	}
}

// spaceEditRow is a space's row of Edit Spaces…: its colors, its name in
// a field (Default's is not renamed), and its moves up and down.
func (a *App) spaceEditRow(c *ui.Context, k *colors, i int, name string) {
	s := &a.spaces
	ui.Row(c.Key("edit-" + name)).Gap(6).AlignItems(ui.Center).Children(func() {
		mark := ui.Box(c).Size(22, 22).Center().Radius(11).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.SpaceColor()).Tooltip(locales.SpaceColor())
		mark.Children(func() { spaceSwatchMark(c, k, spaceColor(name), 12) })
		mark.ContextMenu(func(m *ui.Menu) { spaceColorMenu(m, name) })
		if mark.Clicked() {
			// A click goes on to the next preset, as a right click lists
			// them all.
			next := layout.Palette[0].ID
			for j, p := range layout.Palette {
				if p.ID == spaceColor(name) && j+1 < len(layout.Palette) {
					next = layout.Palette[j+1].ID
				}
			}
			setSpaceColorOf(name, next)
		}
		if name == "" {
			ui.Text(c, spaceLabel(name)).FontSize(13).TextColor(k.text).Grow(1).Padding(0, 6)
		} else {
			if _, ok := s.names[name]; !ok {
				s.names[name] = name
			}
			v := s.names[name]
			in := textField(c.Key("field"), k, &v, fieldCompact).Grow(1).MinWidth(0).Label(locales.SpaceNameLabel())
			s.names[name] = v
			if in.Submitted() {
				a.later(c, a.applySpaceNames)
			}
		}
		up := iconButton(c, k, "arrow-up", locales.SpaceMoveUp(), 24, 13).Tooltip(locales.SpaceMoveUp())
		if i <= 1 {
			up.Disabled(true)
			dimmed(up, 0.4)
		} else if up.Clicked() {
			a.st.moveSpace(name, -1)
		}
		down := iconButton(c, k, "chevron-down", locales.SpaceMoveDown(), 24, 13).Tooltip(locales.SpaceMoveDown())
		if i == 0 || i >= len(a.st.spaceNames())-1 {
			down.Disabled(true)
			dimmed(down, 0.4)
		} else if down.Clicked() {
			a.st.moveSpace(name, 1)
		}
	})
}

// spaceColorMenu lists the colors space name may take on this client.
func spaceColorMenu(m *ui.Menu, name string) {
	if m.Item(locales.SpaceColorDefault()).Chosen() {
		setSpaceColorOf(name, "")
	}
	for _, p := range layout.Palette {
		if m.Item(strings.ToUpper(p.ID[:1]) + p.ID[1:]).Chosen() {
			setSpaceColorOf(name, p.ID)
		}
	}
}

// applySpaceNames renames the spaces whose fields Edit Spaces… changed.
func (a *App) applySpaceNames() {
	for from, to := range a.spaces.names {
		if to = strings.TrimSpace(to); to != "" && to != from {
			a.renameSpace(from, to)
		}
	}
	a.spaces.names = map[string]string{}
}

// spaceOf is the space of workspace w: its own for a project, the shown
// one for the tabs with no project, which every space shows.
func spaceOf(w *Workspace) string {
	if w == nil || w.Dir == "" {
		return shownSpace()
	}
	return w.Space
}

// windowColor is the color the window takes: its project's, else its
// space's on this client.
func (a *App) windowColor() string {
	if a.ws == nil {
		return spaceColor(shownSpace())
	}
	return cmp.Or(a.ws.Color, spaceColor(spaceOf(a.ws)))
}
