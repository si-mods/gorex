package main

import (
	"cmp"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Spaces (docs/MOD-002-projects.md, "Spaces"): a space is only a name on the
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

// spaceRename is a rename of a space that waits for a host to connect;
// one to "" (Default) is a delete of the space (deleteSpace).
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

// deleteSpace deletes space name: each connected host moves its projects
// to Default and offers the name no more (spaces.delete), and a host not
// connected does once it connects (sendSpaceRenames). This client drops
// its color, order and made space, and a window of it shows Default. No
// tab closes. Default ("") is not deleted.
func (a *App) deleteSpace(name string) {
	if a.st == nil || name == "" {
		return
	}
	st := a.st
	for _, h := range a.allHosts() {
		if h != nil && !serverMade(h) {
			if prefs.SpaceRenames == nil {
				prefs.SpaceRenames = map[string][]spaceRename{}
			}
			prefs.SpaceRenames[h.ID] = append(prefs.SpaceRenames[h.ID], spaceRename{From: name})
			// Its copy offers the name no more, as it will not.
			if h.lay != nil {
				h.lay.base.Spaces = slices.DeleteFunc(slices.Clone(h.lay.base.Spaces), func(n string) bool { return n == name })
			}
			continue
		}
		st.sendOn(h, st.spaceDeleteCommand(h, name), nil)
	}
	prefs.Spaces = slices.DeleteFunc(prefs.Spaces, func(s clientSpace) bool { return s.Name == name })
	// The copies of hosts away show their projects in Default too.
	for _, w := range st.workspaces {
		if w.Dir != "" && w.Space == name {
			w.Space = ""
		}
	}
	saveSettings()
	if prefs.Space == name {
		a.showSpaceNamed("")
	}
}

// spaceDeleteCommand is the command that deletes space name on host h:
// spaces.delete, or on a server of v0.41.0, which has it not, a rename to
// Default, which moves the projects as well.
func (st *store) spaceDeleteCommand(h *hostConn, name string) rex.LayoutCommand {
	hello := st.hello
	if h != nil {
		hello = h.hello
	}
	if hello.Can(rex.CapabilitySpacesDelete) {
		return rex.LayoutCommand{Op: "spaces.delete", From: name}
	}
	return rex.LayoutCommand{Op: "spaces.rename", From: name}
}

// spaceProjects counts the projects of space name of the hosts
// connected, and tells whether a host is not connected, whose projects
// are not counted.
func (a *App) spaceProjects(name string) (n int, away bool) {
	if a.st == nil {
		return 0, false
	}
	for _, w := range a.st.workspaces {
		if w.Dir != "" && w.Space == name && !w.offline() {
			n++
		}
	}
	for _, h := range a.hosts {
		if !serverMade(h) {
			away = true
		}
	}
	return n, away
}

// sendSpaceRenames sends host h the renames and deletes of spaces that
// waited for it, in order, now that it is connected. Call it on the main
// thread.
func (st *store) sendSpaceRenames(h *hostConn) {
	renames := prefs.SpaceRenames[h.ID]
	if len(renames) == 0 || !serverMade(h) {
		return
	}
	delete(prefs.SpaceRenames, h.ID)
	saveSettings()
	for _, r := range renames {
		c := rex.LayoutCommand{Op: "spaces.rename", From: r.From, To: r.To}
		if r.To == "" {
			c = st.spaceDeleteCommand(h, r.From)
		}
		st.sendOn(h, c, func(res rex.LayoutCommand) {
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
	// dialog is "new", "edit" or "delete" while the dialog of New
	// Space…, Edit Spaces… or Delete Space… shows; name is the new
	// space's name, or the one to delete; names the edit's fields by the
	// space's name.
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
	chip := ui.Row(c.Key("space-picker")).Height(28).Gap(8).Padding(0, 8, 0, 8).Radius(pillRadius(k, 28, k.radius(radiusM))).AlignItems(ui.Center).Cursor(ui.CursorPointer).
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
		t := ui.Text(c, spaceLabel(name)).FontSize(fontBody).FontWeight(600).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…").MinWidth(0)
		if wide {
			t.Grow(1)
		}
		glyphIcon(c, "chevron-down", 13, k.textFaint).Shrink(0)
	})
	popoverBase(c, chip, &a.spaces.open, func(panel ui.Element, entering bool) {
		k := k.window() // the menu lies on the window's panel, not on the chip's face
		m := menuStyleOf(k, c.Theme().Dark)
		m.Panel(panel.Margin(6, 0, 0, 0).Width(260), 0)
		// The menu's parts come in one after another, a step apart.
		i := 0
		ui.Column(c).Children(func() {
			menuReveal(c, a.spaceMenuHead(c, k), entering, i)
			i++
			for _, n := range a.st.spaceNames() {
				menuReveal(c, a.spaceMenuRow(c, k, m, n, n == name), entering, i)
				i++
			}
			menuReveal(c, ui.Box(c).Height(1).Margin(4, 6).Background(k.panelBorder), entering, i)
			i++
			menuReveal(c, a.spaceMenuNew(c, k, m), entering, i)
		})
	})
}

// spaceMenuHead is the head of the space picker's menu: its title and
// the Edit button that opens Edit Spaces…, as the iOS picker's panel
// has them.
func (a *App) spaceMenuHead(c *ui.Context, k *colors) ui.Element {
	head := ui.Row(c.Key("space-head")).Height(28).Padding(0, 10).Gap(8).AlignItems(ui.Center)
	head.Children(func() {
		ui.Text(c, locales.SpacesTitle()).FontSize(fontFootnote).FontWeight(600).TextColor(k.textMuted).SingleLine()
		ui.Box(c).Grow(1)
		edit := ui.Row(c.Key("space-edit")).Radius(k.radius(radiusS)).Padding(2, 4, 2, 4).Cursor(ui.CursorPointer).
			Role(ui.RoleButton).Label(locales.SpaceEdit())
		if edit.Hovered() {
			edit.Background(hoverFace(k))
		}
		if edit.Clicked() {
			a.closeMenus()
			a.later(c, func() { a.spaces.dialog, a.spaces.names = "edit", map[string]string{} })
		}
		edit.Children(func() {
			ui.Text(c, locales.MenuEdit()).FontSize(fontBody).FontWeight(600).TextColor(k.accent).SingleLine()
		})
	})
	return head
}

// spaceMenuRow is the menu's row of the space n: a swatch of its color,
// its name above how many projects it has, and a check in its color when
// it is the space shown, whose name is semibold and row washed in it
// then. A click shows the space.
func (a *App) spaceMenuRow(c *ui.Context, k *colors, m *ui.MenuStyle, n string, on bool) ui.Element {
	row := ui.Row(c.Key("space-"+n)).Height(44).Padding(0, m.RowPadding).Gap(8).Radius(m.RowRadius).AlignItems(ui.Center).
		Role(ui.RoleMenuItem).Label(spaceLabel(n)).AccessSelected(on).Cursor(ui.CursorPointer)
	ink, muted := k.text, k.textMuted
	tint := k.textMuted
	if hex, ok := spaceColorHex(spaceColor(n)); ok {
		tint = ui.Hex(hex)
	}
	hot := row.Hovered()
	if hot {
		row.Background(m.Highlight)
		ink, muted = m.HighlightText, m.HighlightText
	} else if on {
		row.Background(tint.Alpha(0.12))
	}
	if row.Clicked() {
		a.closeMenus()
		a.later(c, func() { a.showSpaceNamed(n) })
	}
	row.Children(func() {
		dot := spaceSwatchMark(c, k, spaceColor(n), 12)
		if hot {
			// On the highlight a ring in its ink keeps the dot, whose
			// color may be the highlight's, in sight.
			dot.Border(1, ink)
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			weight := 400
			if on {
				weight = 600
			}
			ui.Text(c, spaceLabel(n)).FontSize(fontBody).FontWeight(weight).TextColor(ink).SingleLine().Ellipsis("…")
			projs, _ := a.spaceProjects(n)
			ui.Text(c, locales.SpaceProjects(projs)).FontSize(fontFootnote).TextColor(muted).SingleLine().Ellipsis("…")
		})
		if on {
			mark := tint
			if hot {
				mark = ink
			}
			glyphIcon(c, "check", 14, mark).Shrink(0)
		}
	})
	return row
}

// spaceMenuNew is the menu's last row, New Space…, in the accent with a
// plus as the iOS picker shows it.
func (a *App) spaceMenuNew(c *ui.Context, k *colors, m *ui.MenuStyle) ui.Element {
	row := ui.Row(c.Key("space-new")).Height(m.RowHeight).Padding(0, m.RowPadding).Gap(8).Radius(m.RowRadius).AlignItems(ui.Center).
		Role(ui.RoleMenuItem).Label(locales.SpaceNew()).Cursor(ui.CursorPointer)
	ink := k.accent
	if row.Hovered() {
		row.Background(m.Highlight)
		ink = m.HighlightText
	}
	if row.Clicked() {
		a.closeMenus()
		a.later(c, func() { a.spaces.dialog, a.spaces.name = "new", "" })
	}
	row.Children(func() {
		ui.Box(c).Size(12, 12).Shrink(0).Children(func() { glyphIcon(c, "plus", 11, ink) })
		ui.Text(c, locales.SpaceNew()).FontSize(fontBody).TextColor(ink).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
	return row
}

// menuRow is a row of a popover menu of GoRex's own, the space picker's
// or the project picker's: a check when chosen, a mark drawn in ink (hot
// on the highlight), its label and a detail; a click closes the menus and
// runs run. It is a row of the drawn menus (menuStyleOf): their height,
// padding and radius, and under the pointer their highlight, everything
// on it in its text color. With run nil the row is inert, as a drawn
// menu's disabled item: no highlight, no click; the caller dims it.
func (a *App) menuRow(c *ui.Context, k *colors, key string, chosen bool, label, detail string, mark func(ink ui.Color, hot bool), run func()) ui.Element {
	m := menuStyleOf(k, c.Theme().Dark)
	row := ui.Row(c.Key(key)).Height(m.RowHeight).Padding(0, m.RowPadding).Gap(8).Radius(m.RowRadius).AlignItems(ui.Center).
		Role(ui.RoleMenuItem).Label(label)
	if run == nil {
		row.AccessDisabled(true)
	} else {
		row.Cursor(ui.CursorPointer)
	}
	ink, muted := k.text, k.textMuted
	hot := run != nil && row.Hovered()
	if hot {
		row.Background(m.Highlight)
		ink, muted = m.HighlightText, m.HighlightText
	}
	if run != nil && row.Clicked() {
		a.closeMenus()
		a.later(c, run)
	}
	row.Children(func() {
		ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
			if chosen {
				glyphIcon(c, "check", 14, ink)
			}
		})
		if mark != nil {
			mark(muted, hot)
		}
		ui.Text(c, label).FontSize(fontBody).TextColor(ink).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if detail != "" {
			ui.Text(c, detail).FontSize(fontFootnote).TextColor(muted).SingleLine().Shrink(0)
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
	return dot.Border(1, k.textMuted)
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
	switch s.dialog {
	case "edit":
		title = locales.SpaceEditTitle()
	case "delete":
		title = locales.SpaceDeleteTitle(s.name)
	}
	ui.DialogBase(c.Key("space-dialog"), &open, func(backdrop, panel ui.Element) {
		dialogEnter(c, backdrop, panel)
		backdrop.Background(k.backdrop)
		_, winH := c.Size()
		dialogShadow(glassPanel(panel.Width(400).MaxHeight(winH-24).Padding(20).Radius(k.radius(radiusXXL)), k).Border(k.borderW(), k.panelBorder), k).Role(ui.RoleDialog).Label(title)
		ui.Text(c, title).FontSize(fontTitle).FontWeight(700).TextColor(k.text).Margin(0, 0, 10, 0)
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
		if s.dialog == "delete" {
			n, away := a.spaceProjects(s.name)
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, locales.SpaceDeleteBody(n)).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
				if away {
					ui.Text(c, locales.SpaceDeleteOffline()).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
				}
			})
			ui.Row(c).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
				if cancelButton(c, locales.SessionCancel()).Clicked() {
					open = false
				}
				if dangerButton(c, k, locales.SpaceDeleteConfirm()).Clicked() {
					name := s.name
					open = false
					a.later(c, func() { a.deleteSpace(name) })
				}
			})
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
		// A click opens the colors below the dot, as a right click does. The
		// two menus are on two elements: one element cannot have both.
		ui.Box(c.Key("color")).Size(22, 22).Shrink(0).ContextMenu(func(m *ui.Menu) { spaceColorMenu(m, name) }).Children(func() {
			mark := ui.Box(c).Size(22, 22).Center().Radius(k.capsule(22)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.SpaceColor()).Tooltip(locales.SpaceColor())
			mark.Children(func() { spaceSwatchMark(c, k, spaceColor(name), 12) })
			mark.Menu(func(m *ui.Menu) { spaceColorMenu(m, name) })
		})
		if name == "" {
			ui.Text(c, spaceLabel(name)).FontSize(fontBody).TextColor(k.text).Grow(1).Padding(0, 6)
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
		if name == "" {
			// Default is not deleted: a box keeps the rows' columns.
			ui.Box(c).Size(24, 24).Shrink(0)
		} else if del := iconButton(c, k, "trash-2", locales.SpaceDelete(), 24, 13).Tooltip(locales.SpaceDelete()); del.Clicked() {
			a.later(c, func() {
				// The other fields' renames go first; this one's is dropped.
				delete(s.names, name)
				a.applySpaceNames()
				s.dialog, s.name = "delete", name
			})
		}
	})
}

// colorChoice is an item of a color menu: its label, the color it sets
// ("" for Default) and whether it is the color the menu's owner has.
type colorChoice struct {
	label, id string
	checked   bool
}

// colorChoices are the items of a color menu, Default and then the
// presets, the one that is cur ticked.
func colorChoices(cur string) []colorChoice {
	out := []colorChoice{{locales.SpaceColorDefault(), "", cur == ""}}
	for _, p := range layout.Palette {
		out = append(out, colorChoice{paletteLabel(p.ID), p.ID, cur == p.ID})
	}
	return out
}

// colorDots are the menu icons of the colors, by id and tick, each made
// once.
var (
	colorDotsMu sync.Mutex
	colorDots   = map[string]*mygo.MenuIcon{}
)

// colorDot is the icon of color id in a color menu: a dot of it, or, for
// Default (""), a ring in the color of the item's text, as spaceSwatchMark
// draws them. The current color's has a tick inside, in white or black,
// whichever stands out more on it: the menu marks it there, not with a
// mark of its own beside the dot.
func colorDot(id string, ticked bool) *mygo.MenuIcon {
	key := id
	if ticked {
		key += "/ticked"
	}
	colorDotsMu.Lock()
	defer colorDotsMu.Unlock()
	if m, ok := colorDots[key]; ok {
		return m
	}
	const tick = `<path d="M5.2 8.3 7.1 10.1 10.8 6.1" fill="none" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" stroke="`
	shape, template := `<circle cx="8" cy="8" r="5.25" fill="none" stroke="#000" stroke-width="1.5"/>`, true
	if ticked {
		shape += tick + `#000"/>`
	}
	if hex, ok := spaceColorHex(id); ok {
		shape, template = `<circle cx="8" cy="8" r="6" fill="`+hex+`"/>`, false
		if ticked {
			ink := "#fff"
			if c := ui.Hex(hex); contrast(c, ui.RGB(0, 0, 0)) > contrast(c, ui.RGB(255, 255, 255)) {
				ink = "#000"
			}
			shape += tick + ink + `"/>`
		}
	}
	m, err := mygo.NewMenuIconSVG(ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16">`+shape+`</svg>`)), template)
	if err != nil {
		// The item shows no dot: the menu still works.
		log.Printf("gorex: color dot %q: %v", key, err)
		return nil
	}
	colorDots[key] = m
	return m
}

// spaceColorMenu lists the colors space name may take on this client, each
// with its dot, the one it has ticked.
func spaceColorMenu(m *ui.Menu, name string) {
	for _, ch := range colorChoices(spaceColor(name)) {
		if m.Item(ch.label).Icon(colorDot(ch.id, ch.checked)).Chosen() {
			setSpaceColorOf(name, ch.id)
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
