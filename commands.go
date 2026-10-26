package main

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// command is an action of the menus and the command palette.
type command struct {
	// Name says the title in a language: a method of the catalog's
	// messages, as locales.Messages.CmdNewTab.
	Name func(locales.Messages) string
	// Accel is the menu item's shortcut, and Keys how the palette shows it.
	Accel, Keys string
	// Other is the shortcut elsewhere than on macOS, which gives Accel and
	// Keys there: Control and a letter are the terminal's, so Shift comes
	// with it, as when copying and pasting; Alt and Control-Alt make
	// characters (AltGr), and Alt and a letter open the menus on Windows.
	Other shortcut
	Run   func(a *App)
	// Hidden keeps it out of the palette.
	Hidden bool
}

var (
	cmdNewTab      = command{Name: locales.Messages.CmdNewTab, Accel: "CmdOrCtrl+T", Keys: "⌘T", Other: ctrlShift(ui.KeyT), Run: func(a *App) { a.newTab(a.currentDir()) }}
	cmdSplitRight  = command{Name: locales.Messages.CmdSplitRight, Accel: "CmdOrCtrl+D", Keys: "⌘D", Other: ctrlShift(ui.KeyD), Run: func(a *App) { a.split(false) }}
	cmdSplitDown   = command{Name: locales.Messages.CmdSplitDown, Accel: "CmdOrCtrl+Shift+D", Keys: "⇧⌘D", Other: ctrlShift(ui.KeyE), Run: func(a *App) { a.split(true) }}
	cmdClosePane   = command{Name: locales.Messages.CmdClosePane, Accel: "CmdOrCtrl+W", Keys: "⌘W", Other: ctrlShift(ui.KeyW), Run: func(a *App) { a.closeFocused() }}
	cmdCloseTab    = command{Name: locales.Messages.CmdCloseTab, Accel: "CmdOrCtrl+Shift+W", Keys: "⇧⌘W", Run: func(a *App) { a.closeActiveTab() }}
	cmdZoom        = command{Name: locales.Messages.CmdZoomPane, Accel: "CmdOrCtrl+Shift+Enter", Keys: "⇧⌘↩", Other: ctrlShift(ui.KeyEnter), Run: func(a *App) { a.toggleZoom() }}
	cmdEqualize    = command{Name: locales.Messages.CmdEqualizePanes, Accel: "CmdOrCtrl+Ctrl+=", Keys: "⌃⌘=", Run: func(a *App) { a.equalize() }}
	cmdNextTab     = command{Name: locales.Messages.CmdNextTab, Accel: "CmdOrCtrl+Shift+]", Keys: "⇧⌘]", Other: shortcut{ui.Ctrl, ui.KeyPageDown}, Run: func(a *App) { a.cycleTab(1) }}
	cmdPrevTab     = command{Name: locales.Messages.CmdPreviousTab, Accel: "CmdOrCtrl+Shift+[", Keys: "⇧⌘[", Other: shortcut{ui.Ctrl, ui.KeyPageUp}, Run: func(a *App) { a.cycleTab(-1) }}
	cmdSidebar     = command{Name: locales.Messages.CmdToggleSidebar, Accel: "CmdOrCtrl+Ctrl+S", Keys: "⌃⌘S", Other: ctrlShift(ui.KeyS), Run: func(a *App) { a.toggleSidebar() }}
	cmdRenameTab   = command{Name: locales.Messages.CmdRenameTab, Accel: "CmdOrCtrl+Shift+R", Keys: "⇧⌘R", Other: ctrlShift(ui.KeyR), Run: func(a *App) { a.startRename() }}
	cmdPalette     = command{Name: locales.Messages.CmdCommandPalette, Accel: "CmdOrCtrl+Shift+P", Keys: "⇧⌘P", Other: ctrlShift(ui.KeyP), Run: func(a *App) { a.openPalette() }, Hidden: true}
	cmdPalette2    = command{Name: locales.Messages.CmdGoToPane, Accel: "CmdOrCtrl+P", Keys: "⌘P", Run: func(a *App) { a.openPalette() }, Hidden: true}
	cmdFocusLeft   = command{Name: locales.Messages.CmdFocusPaneLeft, Accel: "CmdOrCtrl+Alt+Left", Keys: "⌥⌘←", Other: shortcut{ui.Alt, ui.KeyLeft}, Run: func(a *App) { a.moveFocus(-1, 0) }}
	cmdFocusRight  = command{Name: locales.Messages.CmdFocusPaneRight, Accel: "CmdOrCtrl+Alt+Right", Keys: "⌥⌘→", Other: shortcut{ui.Alt, ui.KeyRight}, Run: func(a *App) { a.moveFocus(1, 0) }}
	cmdFocusUp     = command{Name: locales.Messages.CmdFocusPaneAbove, Accel: "CmdOrCtrl+Alt+Up", Keys: "⌥⌘↑", Other: shortcut{ui.Alt, ui.KeyUp}, Run: func(a *App) { a.moveFocus(0, -1) }}
	cmdFocusDown   = command{Name: locales.Messages.CmdFocusPaneBelow, Accel: "CmdOrCtrl+Alt+Down", Keys: "⌥⌘↓", Other: shortcut{ui.Alt, ui.KeyDown}, Run: func(a *App) { a.moveFocus(0, 1) }}
	cmdGrowLeft    = command{Name: locales.Messages.CmdMoveDividerLeft, Accel: "CmdOrCtrl+Ctrl+Left", Keys: "⌃⌘←", Other: shortcut{ui.Alt | ui.Shift, ui.KeyLeft}, Run: func(a *App) { a.resizeFocused(-1, 0) }}
	cmdGrowRight   = command{Name: locales.Messages.CmdMoveDividerRight, Accel: "CmdOrCtrl+Ctrl+Right", Keys: "⌃⌘→", Other: shortcut{ui.Alt | ui.Shift, ui.KeyRight}, Run: func(a *App) { a.resizeFocused(1, 0) }}
	cmdGrowUp      = command{Name: locales.Messages.CmdMoveDividerUp, Accel: "CmdOrCtrl+Ctrl+Up", Keys: "⌃⌘↑", Other: shortcut{ui.Alt | ui.Shift, ui.KeyUp}, Run: func(a *App) { a.resizeFocused(0, -1) }}
	cmdGrowDown    = command{Name: locales.Messages.CmdMoveDividerDown, Accel: "CmdOrCtrl+Ctrl+Down", Keys: "⌃⌘↓", Other: shortcut{ui.Alt | ui.Shift, ui.KeyDown}, Run: func(a *App) { a.resizeFocused(0, 1) }}
	cmdClear       = command{Name: locales.Messages.CmdClearScreen, Accel: "CmdOrCtrl+K", Keys: "⌘K", Other: ctrlShift(ui.KeyK), Run: func(a *App) { a.clearFocused() }, Hidden: true}
	cmdClearScroll = command{Name: locales.Messages.CmdClearScrollback, Run: func(a *App) { a.clearFocused() }}
	cmdRestart     = command{Name: locales.Messages.CmdRestartShell, Run: func(a *App) { a.restartFocused() }}
	cmdBigger      = command{Name: locales.Messages.CmdBiggerText, Accel: "CmdOrCtrl+=", Keys: "⌘+", Other: shortcut{ui.Ctrl, ui.KeyEqual}, Run: func(a *App) { a.setFontSize(termFont.Size + 1) }}
	cmdSmaller     = command{Name: locales.Messages.CmdSmallerText, Accel: "CmdOrCtrl+-", Keys: "⌘−", Other: shortcut{ui.Ctrl, ui.KeyMinus}, Run: func(a *App) { a.setFontSize(termFont.Size - 1) }}
	cmdActualSize  = command{Name: locales.Messages.CmdActualSize, Accel: "CmdOrCtrl+0", Keys: "⌘0", Other: shortcut{ui.Ctrl, ui.Key0}, Run: func(a *App) { a.setFontSize(0) }}
	cmdGhostty     = command{Name: locales.Messages.CmdReloadGhostty, Run: func(a *App) { a.reloadGhostty() }}
	cmdLight       = command{Name: locales.Messages.CmdAppearanceLight, Run: func(a *App) { a.setAppearance("light") }}
	cmdDark        = command{Name: locales.Messages.CmdAppearanceDark, Run: func(a *App) { a.setAppearance("dark") }}
	cmdSystem      = command{Name: locales.Messages.CmdAppearanceSystem, Run: func(a *App) { a.setAppearance("") }}
	cmdSettings    = command{Name: locales.Messages.CmdSettings, Accel: "CmdOrCtrl+,", Keys: "⌘,", Other: shortcut{ui.Ctrl, ui.KeyComma}}
	cmdUpdates     = command{Name: locales.Messages.CmdCheckForUpdates, Run: func(a *App) { updater.CheckForUpdates() }}
	cmdEndAll      = command{Name: locales.Messages.CmdQuitAndEndAll, Accel: "CmdOrCtrl+Alt+Q", Keys: "⌥⌘Q", Run: func(a *App) { a.quitAndEnd() }}
)

// Title is the command's title in the language the app shows.
func (cmd *command) Title() string { return say(cmd.Name) }

// say writes a message in the language the app shows: the generated
// accessors read it as i18n.Locale() and offer no handle on it besides In.
func say(name func(locales.Messages) string) string { return name(locales.In(i18n.Locale())) }

// English is its title in English, which the palette and the debug verbs
// match besides.
func (cmd *command) English() string { return cmd.Name(locales.In("en")) }

// paletteCommands are those the command palette lists, before the tabs.
var paletteCommands = []*command{
	&cmdNewTab, &cmdSplitRight, &cmdSplitDown, &cmdZoom, &cmdEqualize, &cmdClosePane, &cmdCloseTab,
	&cmdNextTab, &cmdPrevTab, &cmdRenameTab, &cmdSidebar,
	&cmdFocusLeft, &cmdFocusRight, &cmdFocusUp, &cmdFocusDown,
	&cmdGrowLeft, &cmdGrowRight, &cmdGrowUp, &cmdGrowDown,
	&cmdBigger, &cmdSmaller, &cmdActualSize, &cmdLight, &cmdDark, &cmdSystem,
	&cmdGhostty, &cmdClearScroll, &cmdRestart, &cmdSettings, &cmdUpdates, &cmdEndAll,
}

// commands are all of them.
var commands = append(slices.Clone(paletteCommands), &cmdPalette, &cmdPalette2, &cmdClear)

func init() {
	if runtime.GOOS == "darwin" {
		return
	}
	for _, cmd := range commands {
		cmd.Accel, cmd.Keys = "", ""
		if cmd.Other != (shortcut{}) {
			cmd.Accel = cmd.Other.String()
			cmd.Keys = cmd.Accel
		}
	}
}

// shortcut is a key with modifiers, elsewhere than on macOS.
type shortcut struct {
	Mods ui.Modifiers
	Key  ui.Key
}

func ctrlShift(k ui.Key) shortcut { return shortcut{ui.Ctrl | ui.Shift, k} }

// showTabKey is the shortcut of the nth tab, from 1 to 9.
func showTabKey(n int) shortcut { return ctrlShift(ui.Key0 + ui.Key(n)) }

// String writes the shortcut as menus take it, and as Windows shows it:
// "Ctrl+Shift+T".
func (s shortcut) String() string {
	var parts []string
	for _, m := range []struct {
		mod  ui.Modifiers
		name string
	}{{ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}} {
		if s.Mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	var key string
	switch k := s.Key; {
	case k >= ui.KeyA && k <= ui.KeyZ:
		key = string(rune('A' + k - ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		key = string(rune('0' + k - ui.Key0))
	default:
		key = map[ui.Key]string{
			ui.KeyEnter: "Enter", ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
			ui.KeyLeft: "Left", ui.KeyRight: "Right", ui.KeyUp: "Up", ui.KeyDown: "Down",
			ui.KeyEqual: "=", ui.KeyMinus: "-", ui.KeyComma: ",",
		}[k]
	}
	return strings.Join(append(parts, key), "+")
}

// menuAccel is the shortcut of a command's menu item: its own on macOS,
// and elsewhere the one with Shift, or none, as the menus' shortcuts come
// before the terminal's keys, whose are Control and a letter.
func (cmd *command) menuAccel() string {
	if runtime.GOOS == "darwin" {
		return cmd.Accel
	}
	if cmd.Other == (shortcut{}) {
		return ""
	}
	return cmd.Other.String()
}

// menu builds the menu bar, whose items run commands in the window.
func (a *App) menu() *mygo.Menu {
	item := func(cmd *command) *mygo.MenuItem {
		return &mygo.MenuItem{Label: cmd.Title(), Accelerator: cmd.menuAccel(), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.run(cmd)
		}}
	}
	tabItems := []*mygo.MenuItem{item(&cmdNextTab), item(&cmdPrevTab), mygo.Separator()}
	for i := 1; i <= 9; i++ {
		i := i
		accel := fmt.Sprintf("CmdOrCtrl+%d", i)
		if runtime.GOOS != "darwin" {
			accel = showTabKey(i).String()
		}
		tabItems = append(tabItems, &mygo.MenuItem{
			Label: locales.MenuShowTab(i), Accelerator: accel,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.do(func() { a.showTab(i) })
			},
		})
	}
	appearance := func(label, v string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Type: mygo.MenuItemRadio, Checked: prefs.Appearance == v, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.setAppearance(v)
		}}
	}
	a.sidebarMenuItem = item(&cmdSidebar)
	a.sidebarMenuItem.Disabled = prefs.TabPlacement == ""
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: locales.MenuShell(), Submenu: []*mygo.MenuItem{
			item(&cmdNewTab),
			mygo.Separator(),
			item(&cmdSplitRight),
			item(&cmdSplitDown),
			mygo.Separator(),
			item(&cmdClosePane),
			item(&cmdCloseTab),
			mygo.Separator(),
			item(&cmdRestart),
			mygo.Separator(),
			item(&cmdSettings),
			item(&cmdUpdates),
			item(&cmdEndAll),
		}},
		{Label: locales.MenuEdit(), Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RoleCopy},
			{Role: mygo.RolePaste},
			{Role: mygo.RoleSelectAll},
			mygo.Separator(),
			item(&cmdClear),
		}},
		{Label: locales.MenuView(), Submenu: []*mygo.MenuItem{
			item(&cmdPalette),
			item(&cmdPalette2),
			mygo.Separator(),
			item(&cmdZoom),
			item(&cmdEqualize),
			a.sidebarMenuItem,
			mygo.Separator(),
			{Label: locales.MenuFocus(), Submenu: []*mygo.MenuItem{item(&cmdFocusLeft), item(&cmdFocusRight), item(&cmdFocusUp), item(&cmdFocusDown)}},
			{Label: locales.MenuResize(), Submenu: []*mygo.MenuItem{item(&cmdGrowLeft), item(&cmdGrowRight), item(&cmdGrowUp), item(&cmdGrowDown)}},
			mygo.Separator(),
			item(&cmdBigger),
			item(&cmdSmaller),
			item(&cmdActualSize),
			{Label: locales.MenuAppearance(), Submenu: []*mygo.MenuItem{
				appearance(locales.AppearanceSystem(), ""),
				appearance(locales.AppearanceLight(), "light"),
				appearance(locales.AppearanceDark(), "dark"),
			}},
			item(&cmdGhostty),
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
			{Role: mygo.RoleToggleDevTools, Label: locales.MenuInspector(), Hidden: !mygo.IsDev()},
		}},
		{Label: locales.MenuTabs(), Submenu: append(tabItems, mygo.Separator(), item(&cmdRenameTab))},
		{Role: mygo.RoleWindowMenu},
	})
}

// do changes the state on the main thread, from a menu, and draws.
func (a *App) do(fn func()) {
	if a.win == nil {
		return
	}
	a.win.Update(fn)
}

// run runs a command of the menus, on the main thread. New Tab opens the
// window again when it was closed.
func (a *App) run(cmd *command) {
	if a.win == nil {
		if cmd == &cmdNewTab {
			mygo.RunOnMain(a.open)
		}
		return
	}
	a.do(func() {
		if a.win != nil {
			cmd.Run(a)
		}
	})
}

func (a *App) closeFocused() {
	if t := a.tab(); t != nil && t.Focus != nil {
		a.closePane(t.Focus)
	}
}

func (a *App) closeActiveTab() {
	if t := a.tab(); t != nil {
		a.closeTab(t)
	}
}

// showTab shows the nth tab, from 1; 9 is the last.
func (a *App) showTab(n int) {
	if n == 9 {
		a.selectTab(len(a.tabs) - 1)
	} else {
		a.selectTab(n - 1)
	}
}

func (a *App) cycleTab(d int) {
	if n := len(a.tabs); n > 1 {
		a.selectTab(((a.active+d)%n + n) % n)
	}
}

func (a *App) startRename() {
	if t := a.tab(); t != nil {
		a.renaming, a.renameText, a.renameFocus = t, t.Name, false
		a.focusReq = nil // the field takes the focus
	}
}

func (a *App) clearFocused() {
	if t := a.tab(); t != nil && t.Focus != nil && t.Focus.term != nil {
		// Clear the scrollback and the screen, and have the shell draw
		// its prompt again, as Command-K does in Terminal.
		t.Focus.term.Feed([]byte("\x1b[H\x1b[2J\x1b[3J"))
		if t.Focus.info.Idle {
			t.Focus.term.Send([]byte{0x0c})
		}
	}
}

// restartFocused ends the focused pane's session and starts a shell in
// its place, in the same directory.
func (a *App) restartFocused() {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return
	}
	old := t.Focus
	cols, rows := 80, 24
	if old.term != nil {
		cols, rows = old.term.Size()
	}
	p := a.newPane(a.currentDir(), cols, rows)
	p.Tab, p.Node = t, old.Node
	old.Node.Pane = p
	old.closed = true
	if old.term != nil {
		old.term.Close()
	}
	go a.client.Kill(old.SID)
	if t.Zoom == old {
		t.Zoom = p
	}
	t.Focus = nil
	t.setFocus(p)
	a.focusReq = p
	a.changed()
}

func (a *App) quitAndEnd() {
	a.quitting = true
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			p.closed = true
		}
	}
	c := a.client
	go func() {
		c.Shutdown()
		mygo.App.Quit()
	}()
}

func (a *App) openPalette() {
	a.paletteOpen, a.paletteQuery, a.paletteSel = true, "", 0
	a.focusReq = nil // the palette takes the focus
}

// shortcuts handles the keys the menus do not: Control-Tab between tabs,
// and on Windows those of the commands that have no menu item. The keys of
// menu items run there before the view sees them. The window's shortcuts
// come before the terminal's keys.
func (a *App) shortcuts(c *ui.Context) {
	if c.Shortcut(ui.Ctrl, ui.KeyTab) {
		a.cycleTab(1)
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
		a.cycleTab(-1)
	}
	if runtime.GOOS != "windows" {
		return
	}
	for _, cmd := range commands {
		if cmd.Other != (shortcut{}) && c.Shortcut(cmd.Other.Mods, cmd.Other.Key) {
			a.later(c, func() { cmd.Run(a) })
		}
	}
	for i := 1; i <= 9; i++ {
		if k := showTabKey(i); c.Shortcut(k.Mods, k.Key) {
			a.later(c, func() { a.showTab(i) })
		}
	}
}

// paletteItem is a row of the command palette: a command, or a pane to
// go to.
type paletteItem struct {
	title, detail, keys string
	glyph               string
	run                 func()
}

func (a *App) paletteItems() []paletteItem {
	var items []paletteItem
	q := strings.ToLower(strings.TrimSpace(a.paletteQuery))
	match := func(s string) bool { return q == "" || fuzzy(strings.ToLower(s), q) }
	for ti, t := range a.tabs {
		for _, p := range t.panes() {
			name, detail := p.label()
			if !match(fmt.Sprintf("%s %s tab %d", name, detail, ti+1)) && !match(fmt.Sprintf("%s %s %s", name, detail, locales.PaletteTabKeys(ti+1))) {
				continue
			}
			ti, t, p := ti, t, p
			items = append(items, paletteItem{
				title: name, detail: detail, glyph: paneProgram(p).Glyph,
				keys: locales.PaletteTabKeys(ti + 1),
				run: func() {
					a.selectTab(ti)
					t.setFocus(p)
					a.focusReq = p
				},
			})
		}
	}
	for _, cmd := range paletteCommands {
		if cmd.Hidden || cmd == &cmdUpdates && !mygo.Updater.Enabled() || cmd == &cmdSidebar && prefs.TabPlacement == "" || !match(cmd.Title()) && !match(cmd.English()) {
			continue
		}
		cmd := cmd
		items = append(items, paletteItem{title: cmd.Title(), keys: cmd.Keys, glyph: "command", run: func() { cmd.Run(a) }})
	}
	return items
}

// fuzzy reports whether the letters of q appear in s, in order.
func fuzzy(s, q string) bool {
	if strings.Contains(s, q) {
		return true
	}
	i, want := 0, []rune(q)
	for _, r := range s {
		if i < len(want) && r == want[i] {
			i++
		}
	}
	return i == len(want)
}

// palette draws the command palette, over the window.
func (a *App) palette(c *ui.Context, k *colors) {
	if !a.paletteOpen {
		return
	}
	items := a.paletteItems()
	a.paletteSel = min(max(a.paletteSel, 0), max(len(items)-1, 0))
	was := a.paletteOpen
	// reveal scrolls the selection into view in the frame the keys or the
	// query move it, and only then, so that the wheel scrolls the list.
	reveal := false
	ui.DialogBase(c, &a.paletteOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop).Justify(ui.Start).Padding(78, 0, 0, 0)
		panel.Width(560).MaxHeight(440).Radius(16).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.06)).Clip()
		ui.Row(c).Padding(12, 16).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.panelBorder).Children(func() {
			ui.Icon(c, icon("search")).Size(17, 17).TextColor(k.textFaint)
			in := ui.TextInputBase(c, &a.paletteQuery).Grow(1).FontSize(15).Placeholder(locales.PalettePlaceholder()).AutoFocus()
			if in.Changed() {
				a.paletteSel, reveal = 0, true
			}
			if in.Shortcut(0, ui.KeyDown) {
				a.paletteSel, reveal = min(a.paletteSel+1, len(items)-1), true
			}
			if in.Shortcut(0, ui.KeyUp) {
				a.paletteSel, reveal = max(a.paletteSel-1, 0), true
			}
			if in.Submitted() && len(items) > 0 {
				a.paletteOpen = false
				items[a.paletteSel].run()
			}
		})
		listH := float32(min(max(len(items), 1)*34+12, 384))
		ui.Scroll(c).Height(listH).Padding(6).Children(func() {
			if len(items) == 0 {
				ui.Text(c, locales.PaletteNoMatches()).FontSize(13).TextColor(k.textFaint).Padding(14)
			}
			for i, it := range items {
				row := ui.Row(c.Key(i)).Height(34).Padding(0, 10).Gap(10).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if i == a.paletteSel {
					row.Background(k.panelSel)
					if reveal {
						row.ScrollIntoView()
					}
				} else if row.Hovered() {
					row.Background(k.hover)
				}
				if row.Clicked() {
					a.paletteOpen = false
					it.run()
				}
				row.Children(func() {
					ui.Icon(c, icon(it.glyph)).Size(15, 15).TextColor(k.textMuted)
					ui.Text(c, it.title).FontSize(13.5).FontWeight(500).TextColor(k.text).SingleLine().Ellipsis("…").Shrink(0.3).MinWidth(0)
					if it.detail != "" {
						ui.Text(c, it.detail).FontSize(13).TextColor(k.textFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					}
					ui.Spacer(c)
					if it.keys != "" {
						ui.Text(c, it.keys).FontSize(12).TextColor(k.textFaint).Padding(2, 7).Radius(6).Background(k.hover).Shrink(0)
					}
				})
			}
		})
	})
	if was && !a.paletteOpen {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}
