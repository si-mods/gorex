package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// shortcut is a key with modifiers, elsewhere than on macOS.
type shortcut struct {
	Mods ui.Modifiers
	Key  ui.Key
}

func ctrlShift(k ui.Key) shortcut { return shortcut{ui.Ctrl | ui.Shift, k} }

// showTabKey is the shortcut of the nth tab, from 1 to 9.
func showTabKey(n int) shortcut { return ctrlShift(ui.Key0 + ui.Key(n)) }

// showSpaceKey is the shortcut of the nth space, from 1 to 9: Control and
// the digit, which leaves the terminal Command. Elsewhere Control and the
// digit are the terminal's, and Shift's are the tabs', so the spaces take
// Alt.
func showSpaceKey(n int) shortcut {
	key := ui.Key0 + ui.Key(n)
	if runtime.GOOS == "darwin" {
		return shortcut{Mods: ui.Ctrl, Key: key}
	}
	return shortcut{Mods: ui.Ctrl | ui.Alt, Key: key}
}

// showSpaceKeys names the shortcut of the nth space as the interface
// writes it, as the shortcuts of the tabs: "⌃1" on macOS, "Ctrl+Alt+1"
// elsewhere.
func showSpaceKeys(n int) string {
	if runtime.GOOS == "darwin" {
		return fmt.Sprintf("⌃%d", n)
	}
	return showSpaceKey(n).String()
}

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

// menu builds the menu bar, whose items run commands in the window they
// are chosen in, or in the one focused last.
func (st *store) menu() *mygo.Menu {
	item := func(cmd *command) *mygo.MenuItem {
		accel := cmd.menuAccel()
		if st.recording || st.overrides(cmd) {
			accel = ""
		}
		mi := &mygo.MenuItem{Label: cmd.Title(), Accelerator: accel, Click: func(_ *mygo.MenuItem, w *mygo.Window) {
			st.run(cmd, w)
		}}
		if cmd.Host != nil {
			mi.Disabled = hostItemsOff[cmd]
			hostItems[cmd] = mi
		}
		return mi
	}
	tabItems := []*mygo.MenuItem{item(&cmdNextTab), item(&cmdPrevTab), mygo.Separator()}
	for i := 1; i <= 9; i++ {
		accel := fmt.Sprintf("CmdOrCtrl+%d", i)
		if runtime.GOOS != "darwin" {
			accel = showTabKey(i).String()
		}
		if st.recording {
			accel = ""
		} else if chord, ok := accelChord(accel); ok && st.noAccel[chord.String()] {
			accel = ""
		}
		tabItems = append(tabItems, &mygo.MenuItem{
			Label: locales.MenuShowTab(i), Accelerator: accel,
			Click: func(_ *mygo.MenuItem, w *mygo.Window) {
				if a := st.appOf(w); a != nil {
					a.do(func() { a.showTab(i) })
				}
			},
		})
	}
	spaceItems := make([]*mygo.MenuItem, 0, 10)
	spaceItems = append(spaceItems, mygo.Separator())
	for i := 1; i <= 9; i++ {
		accel := fmt.Sprintf("Ctrl+%d", i)
		if runtime.GOOS != "darwin" {
			accel = showSpaceKey(i).String()
		}
		if st.recording {
			accel = ""
		} else if chord, ok := accelChord(accel); ok && st.noAccel[chord.String()] {
			accel = ""
		}
		spaceItems = append(spaceItems, &mygo.MenuItem{
			Label: locales.MenuShowSpace(i), Accelerator: accel,
			Click: func(_ *mygo.MenuItem, w *mygo.Window) {
				if a := st.appOf(w); a != nil {
					a.do(func() { a.showSpace(i) })
				}
			},
		})
	}
	appearance := func(label, v string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Type: mygo.MenuItemRadio, Checked: prefs.Appearance == v, Click: func(*mygo.MenuItem, *mygo.Window) {
			new(App).setAppearance(v) // it changes no window of its own
		}}
	}
	sidebarMenuItem = item(&cmdSidebar)
	sidebarMenuItem.Disabled = prefs.TabPlacement == ""
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: locales.MenuShell(), Submenu: func() []*mygo.MenuItem {
			items := []*mygo.MenuItem{
				item(&cmdNewTab),
			}
			// The packs' pane commands, after the built-ins' (docs/EXT-001).
			for _, c := range extPacks.tabCmds {
				items = append(items, item(c))
			}
			items = append(items,
				item(&cmdNewWindow),
				mygo.Separator(),
				item(&cmdSplitRight),
				item(&cmdSplitDown),
			)
			for _, c := range extPacks.splitCmds {
				items = append(items, item(c))
			}
			return append(items,
				mygo.Separator(),
				item(&cmdClosePane),
				item(&cmdCloseTab),
				mygo.Separator(),
				item(&cmdRestart),
				mygo.Separator(),
				item(&cmdSettings),
				item(&cmdUpdates),
				item(&cmdEndAll),
			)
		}()},
		{Label: locales.MenuEdit(), Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RoleCopy},
			{Role: mygo.RolePaste},
			{Role: mygo.RoleSelectAll},
			mygo.Separator(),
			item(&cmdFind),
			item(&cmdFindNext),
			item(&cmdFindPrev),
			mygo.Separator(),
			item(&cmdClear),
		}},
		{Label: locales.MenuView(), Submenu: []*mygo.MenuItem{
			item(&cmdPalette),
			item(&cmdPalette2),
			mygo.Separator(),
			item(&cmdZoom),
			item(&cmdFloatPane),
			item(&cmdToggleFloat),
			item(&cmdEqualize),
			sidebarMenuItem,
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
		{Label: locales.MenuTabs(), Submenu: append(tabItems, item(&cmdJumpNeedy), item(&cmdNeeds), mygo.Separator(), item(&cmdRenameTab))},
		{Label: locales.MenuSession(), Submenu: append([]*mygo.MenuItem{
			item(&cmdNewProject),
			item(&cmdNewSession),
			item(&cmdImportRex),
			mygo.Separator(),
			item(&cmdChangeSession),
			mygo.Separator(),
			item(&cmdNextSession),
			item(&cmdPrevSession),
			mygo.Separator(),
			item(&cmdRenameSession),
			item(&cmdRevealFolder),
			item(&cmdCloseSession),
		}, spaceItems...)},
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

// run runs a command of the menus in the window w, or the one focused
// last, on the main thread. Without windows, New Tab, New Window and New
// Session open one.
func (st *store) run(cmd *command, w *mygo.Window) {
	switch a := st.appOf(w); {
	case a != nil:
		a.run(cmd)
	case cmd == &cmdNewTab:
		mygo.RunOnMain(st.reopen)
	case cmd == &cmdNewWindow || cmd == &cmdNewSession || cmd == &cmdNewProject:
		mygo.RunOnMain(st.openNew)
	}
}

// hostItems are the menu bar's items of the actions that need a host
// (command.Host), and hostItemsOff those of them disabled, as the host of
// the window focused last is not connected.
var (
	hostItems    = map[*command]*mygo.MenuItem{}
	hostItemsOff = map[*command]bool{}
)

// syncHostItems disables the menu bar's items of the actions whose host
// is not connected, in the window with the focus.
func (a *App) syncHostItems() {
	for cmd, mi := range hostItems {
		if off := cmd.hostAway(a) != nil; off != hostItemsOff[cmd] {
			hostItemsOff[cmd] = off
			mi.SetEnabled(!off)
		}
	}
}

// clearHostItems enables the menu bar's items of the actions that need a
// host again, as the last window closes: with no window, New Tab opens
// one.
func clearHostItems() {
	for cmd, mi := range hostItems {
		if hostItemsOff[cmd] {
			hostItemsOff[cmd] = false
			mi.SetEnabled(true)
		}
	}
}
