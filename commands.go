package main

import (
	"cmp"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/rex"
	"gorex/locales"
)

// command is an action of the menus, the command palette, the keymap, the
// CLI and Lua: one registry, one ID, one set of arguments. Menus and the
// palette are built from it; commands without an ID are not actions.
type command struct {
	// ID names the action across the app, such as "pane.split.right". It
	// is stable: scripts and user configuration refer to it.
	ID string
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
	// Args are the arguments an action takes. A command with none takes
	// none; invocation rejects an unknown or wrongly typed one.
	Args []actionArg
	// Run makes the action when it takes no arguments. RunArgs, when set,
	// makes it with arguments, which Run does not see.
	Run     func(a *App)
	RunArgs func(a *App, args map[string]any) error
	// Available, when set, tells whether the action can run now.
	Available func(a *App) bool
	// Hidden keeps it out of the palette.
	Hidden bool
	// Custom marks an action defined in the Lua configuration.
	Custom bool
}

// clientBlockedActions are actions a client may not run, even when it may
// reach the action channel.
var clientBlockedActions = map[string]bool{
	"app.quit": true, "app.updates": true, "config.reload": true,
	"session.close": true, "tab.close": true, "pane.close": true, "pane.restart": true,
	"screen.clear": true, "scrollback.clear": true, "tab.move_window": true,
	"mode.enter": true, "mode.exit": true, "mode.exit_all": true,
}

func blockedFromClient(id string) bool { return clientBlockedActions[id] }

// actionArg is one argument of an action. Type is "string", "number",
// "integer", "boolean", "object", "array" or "any"; Values, when set,
// limits a string to one of them.
type actionArg struct {
	Name     string
	Type     string
	Optional bool
	Values   []string
}

// validateArgs checks args against the action's arguments.
func (cmd *command) validateArgs(args map[string]any) error {
	specs := make(map[string]actionArg, len(cmd.Args))
	for _, a := range cmd.Args {
		specs[a.Name] = a
	}
	for name := range args {
		if _, ok := specs[name]; !ok {
			return fmt.Errorf("%s: no argument %q", cmd.ID, name)
		}
	}
	for _, a := range cmd.Args {
		v, ok := args[a.Name]
		if !ok {
			if a.Optional {
				continue
			}
			return fmt.Errorf("%s: %s is required", cmd.ID, a.Name)
		}
		if err := a.check(v); err != nil {
			return fmt.Errorf("%s: %s: %w", cmd.ID, a.Name, err)
		}
	}
	return nil
}

// check reports whether v is of the argument's type.
func (a actionArg) check(v any) error {
	ok := false
	switch a.Type {
	case "", "any":
		ok = true
	case "string":
		s, is := v.(string)
		ok = is
		if ok && len(a.Values) > 0 && !slices.Contains(a.Values, s) {
			return fmt.Errorf("want one of %s", strings.Join(a.Values, ", "))
		}
	case "number":
		switch n := v.(type) {
		case float64:
			ok = !math.IsNaN(n) && !math.IsInf(n, 0)
		case float32:
			ok = !float32IsBad(n)
		case int, int64:
			ok = true
		}
	case "integer":
		switch n := v.(type) {
		case int, int64:
			ok = true
		case float64:
			ok = n == float64(int64(n))
		}
	case "boolean":
		_, ok = v.(bool)
	case "object":
		_, ok = v.(map[string]any)
	case "array":
		_, ok = v.([]any)
	default:
		return fmt.Errorf("unknown type %q", a.Type)
	}
	if !ok {
		return fmt.Errorf("want %s", a.Type)
	}
	return nil
}

// invoke runs the action with args, after checking them and whether it can
// run now.
func (cmd *command) invoke(a *App, args map[string]any) error {
	if cmd.Available != nil && !cmd.Available(a) {
		return fmt.Errorf("%s is not available now", cmd.ID)
	}
	if err := cmd.validateArgs(args); err != nil {
		return err
	}
	if cmd.RunArgs != nil {
		return cmd.RunArgs(a, args)
	}
	if cmd.Run != nil {
		cmd.Run(a)
	}
	return nil
}

// commandsByID is every action by its ID, made from commands at start,
// plus the custom actions of the Lua configuration.
var commandsByID = map[string]*command{}

// customCommands are the Lua configuration's actions, which the palette
// and the CLI list beside the built-ins.
var customCommands []*command

// newCustomCommands builds the actions of cfg without registering them.
func newCustomCommands(cfg *config.Config) ([]*command, error) {
	out := make([]*command, 0, len(cfg.Actions))
	for _, ca := range cfg.Actions {
		if lookupCommand(ca.Name) != nil && !slices.ContainsFunc(customCommands, func(c *command) bool { return c.ID == ca.Name }) {
			return nil, fmt.Errorf("custom action %q collides with a built-in", ca.Name)
		}
		ca := ca
		title := ca.Title
		if title == "" {
			title = ca.Name
		}
		args := make([]actionArg, 0, len(ca.Args))
		for _, a := range ca.Args {
			args = append(args, actionArg{Name: a.Name, Type: a.Type, Optional: a.Optional})
		}
		out = append(out, &command{
			ID:   ca.Name,
			Name: func(locales.Messages) string { return title },
			Args: args,
			RunArgs: func(app *App, args map[string]any) error {
				cfg.SetInvoke(func(id string, args map[string]any) error {
					if blockedFromClient(id) {
						return fmt.Errorf("%s is not allowed from an action", id)
					}
					cmd := lookupCommand(id)
					if cmd == nil {
						return fmt.Errorf("no action %q", id)
					}
					return cmd.invoke(app, args)
				})
				return cfg.Invoke(ca.Name, map[string]any{"origin": "action"}, args)
			},
		})
	}
	return out, nil
}

// setCustomCommands replaces the registered custom actions.
func setCustomCommands(list []*command) {
	for _, c := range customCommands {
		delete(commandsByID, c.ID)
	}
	customCommands = list
	for _, c := range list {
		commandsByID[c.ID] = c
	}
}

// lookupCommand returns the action ID, nil when there is none.
func lookupCommand(id string) *command { return commandsByID[id] }

var (
	cmdNewTab = command{
		ID: "tab.new", Name: locales.Messages.CmdNewTab, Accel: "CmdOrCtrl+T", Keys: "⌘T", Other: ctrlShift(ui.KeyT),
		Args: []actionArg{{Name: "dir", Type: "string", Optional: true}},
		Run:  func(a *App) { a.newTab(a.currentDir()) },
		RunArgs: func(a *App, args map[string]any) error {
			dir, _ := args["dir"].(string)
			if strings.TrimSpace(dir) == "" {
				dir = a.currentDir()
			}
			a.newTab(dir)
			return nil
		},
	}
	cmdSplitRight = command{
		ID: "pane.split.right", Name: locales.Messages.CmdSplitRight, Accel: "CmdOrCtrl+D", Keys: "⌘D", Other: ctrlShift(ui.KeyD),
		Args:    []actionArg{{Name: "dir", Type: "string", Optional: true}},
		Run:     func(a *App) { a.split(false) },
		RunArgs: func(a *App, args map[string]any) error { a.splitAt(false, argString(args, "dir")); return nil },
	}
	cmdSplitDown = command{
		ID: "pane.split.down", Name: locales.Messages.CmdSplitDown, Accel: "CmdOrCtrl+Shift+D", Keys: "⇧⌘D", Other: ctrlShift(ui.KeyE),
		Args:    []actionArg{{Name: "dir", Type: "string", Optional: true}},
		Run:     func(a *App) { a.split(true) },
		RunArgs: func(a *App, args map[string]any) error { a.splitAt(true, argString(args, "dir")); return nil },
	}
	cmdClosePane = command{ID: "pane.close", Name: locales.Messages.CmdClosePane, Accel: "CmdOrCtrl+W", Keys: "⌘W", Other: ctrlShift(ui.KeyW), Run: func(a *App) { a.closeFocused() }}
	cmdCloseTab  = command{ID: "tab.close", Name: locales.Messages.CmdCloseTab, Accel: "CmdOrCtrl+Shift+W", Keys: "⇧⌘W", Run: func(a *App) { a.closeActiveTab() }}
	cmdZoom      = command{
		ID: "pane.zoom", Name: locales.Messages.CmdZoomPane, Accel: "CmdOrCtrl+Shift+Enter", Keys: "⇧⌘↩", Other: ctrlShift(ui.KeyEnter),
		Args:    []actionArg{{Name: "mode", Type: "string", Optional: true, Values: []string{"on", "off", "toggle"}}},
		Run:     func(a *App) { _ = a.zoomFocused("toggle") },
		RunArgs: func(a *App, args map[string]any) error { return a.zoomFocused(argString(args, "mode")) },
	}
	cmdEqualize  = command{ID: "pane.equalize", Name: locales.Messages.CmdEqualizePanes, Accel: "CmdOrCtrl+Ctrl+=", Keys: "⌃⌘=", Run: func(a *App) { a.equalize() }}
	cmdNextTab   = command{ID: "tab.next", Name: locales.Messages.CmdNextTab, Accel: "CmdOrCtrl+Shift+]", Keys: "⇧⌘]", Other: shortcut{ui.Ctrl, ui.KeyPageDown}, Run: func(a *App) { a.cycleTab(1) }}
	cmdPrevTab   = command{ID: "tab.previous", Name: locales.Messages.CmdPreviousTab, Accel: "CmdOrCtrl+Shift+[", Keys: "⇧⌘[", Other: shortcut{ui.Ctrl, ui.KeyPageUp}, Run: func(a *App) { a.cycleTab(-1) }}
	cmdSidebar   = command{ID: "sidebar.toggle", Name: locales.Messages.CmdToggleSidebar, Accel: "CmdOrCtrl+Ctrl+S", Keys: "⌃⌘S", Other: ctrlShift(ui.KeyS), Run: func(a *App) { a.toggleSidebar() }}
	cmdRenameTab = command{
		ID: "tab.rename", Name: locales.Messages.CmdRenameTab, Accel: "CmdOrCtrl+Shift+R", Keys: "⇧⌘R", Other: ctrlShift(ui.KeyR),
		Args: []actionArg{{Name: "name", Type: "string", Optional: true}},
		Run:  func(a *App) { a.startRename() },
		RunArgs: func(a *App, args map[string]any) error {
			name := strings.TrimSpace(argString(args, "name"))
			if name == "" {
				a.startRename()
				return nil
			}
			if t := a.tab(); t != nil {
				t.Name = name
				a.changed()
			}
			return nil
		},
	}
	cmdPalette    = command{ID: "palette.open", Name: locales.Messages.CmdCommandPalette, Accel: "CmdOrCtrl+Shift+P", Keys: "⇧⌘P", Other: ctrlShift(ui.KeyP), Run: func(a *App) { a.openPalette() }, Hidden: true}
	cmdPalette2   = command{ID: "pane.goto", Name: locales.Messages.CmdGoToPane, Accel: "CmdOrCtrl+P", Keys: "⌘P", Run: func(a *App) { a.openPalette() }, Hidden: true}
	cmdFocusLeft  = command{ID: "pane.focus.left", Name: locales.Messages.CmdFocusPaneLeft, Accel: "CmdOrCtrl+Alt+Left", Keys: "⌥⌘←", Other: shortcut{ui.Alt, ui.KeyLeft}, Run: func(a *App) { a.moveFocus(-1, 0) }}
	cmdFocusRight = command{ID: "pane.focus.right", Name: locales.Messages.CmdFocusPaneRight, Accel: "CmdOrCtrl+Alt+Right", Keys: "⌥⌘→", Other: shortcut{ui.Alt, ui.KeyRight}, Run: func(a *App) { a.moveFocus(1, 0) }}
	cmdFocusUp    = command{ID: "pane.focus.up", Name: locales.Messages.CmdFocusPaneAbove, Accel: "CmdOrCtrl+Alt+Up", Keys: "⌥⌘↑", Other: shortcut{ui.Alt, ui.KeyUp}, Run: func(a *App) { a.moveFocus(0, -1) }}
	cmdFocusDown  = command{ID: "pane.focus.down", Name: locales.Messages.CmdFocusPaneBelow, Accel: "CmdOrCtrl+Alt+Down", Keys: "⌥⌘↓", Other: shortcut{ui.Alt, ui.KeyDown}, Run: func(a *App) { a.moveFocus(0, 1) }}
	cmdGrowLeft   = command{
		ID: "pane.resize.left", Name: locales.Messages.CmdMoveDividerLeft, Accel: "CmdOrCtrl+Ctrl+Left", Keys: "⌃⌘←", Other: shortcut{ui.Alt | ui.Shift, ui.KeyLeft},
		Args: []actionArg{{Name: "amount", Type: "number", Optional: true}},
		Run:  func(a *App) { a.resizeFocused(-1, 0) },
		RunArgs: func(a *App, args map[string]any) error {
			a.resizeFocusedBy(-1, 0, argFloat(args, "amount"))
			return nil
		},
	}
	cmdGrowRight = command{
		ID: "pane.resize.right", Name: locales.Messages.CmdMoveDividerRight, Accel: "CmdOrCtrl+Ctrl+Right", Keys: "⌃⌘→", Other: shortcut{ui.Alt | ui.Shift, ui.KeyRight},
		Args:    []actionArg{{Name: "amount", Type: "number", Optional: true}},
		Run:     func(a *App) { a.resizeFocused(1, 0) },
		RunArgs: func(a *App, args map[string]any) error { a.resizeFocusedBy(1, 0, argFloat(args, "amount")); return nil },
	}
	cmdGrowUp = command{
		ID: "pane.resize.up", Name: locales.Messages.CmdMoveDividerUp, Accel: "CmdOrCtrl+Ctrl+Up", Keys: "⌃⌘↑", Other: shortcut{ui.Alt | ui.Shift, ui.KeyUp},
		Args: []actionArg{{Name: "amount", Type: "number", Optional: true}},
		Run:  func(a *App) { a.resizeFocused(0, -1) },
		RunArgs: func(a *App, args map[string]any) error {
			a.resizeFocusedBy(0, -1, argFloat(args, "amount"))
			return nil
		},
	}
	cmdGrowDown = command{
		ID: "pane.resize.down", Name: locales.Messages.CmdMoveDividerDown, Accel: "CmdOrCtrl+Ctrl+Down", Keys: "⌃⌘↓", Other: shortcut{ui.Alt | ui.Shift, ui.KeyDown},
		Args:    []actionArg{{Name: "amount", Type: "number", Optional: true}},
		Run:     func(a *App) { a.resizeFocused(0, 1) },
		RunArgs: func(a *App, args map[string]any) error { a.resizeFocusedBy(0, 1, argFloat(args, "amount")); return nil },
	}
	cmdPaneRatio = command{
		ID: "pane.ratio", Name: func(locales.Messages) string { return "Set Split Ratio" }, Hidden: true,
		Args:    []actionArg{{Name: "ratio", Type: "number"}},
		RunArgs: func(a *App, args map[string]any) error { a.setSplitRatio(argFloat(args, "ratio")); return nil },
	}
	cmdClear       = command{ID: "screen.clear", Name: locales.Messages.CmdClearScreen, Accel: "CmdOrCtrl+K", Keys: "⌘K", Other: ctrlShift(ui.KeyK), Run: func(a *App) { a.clearFocused() }, Hidden: true}
	cmdClearScroll = command{ID: "scrollback.clear", Name: locales.Messages.CmdClearScrollback, Run: func(a *App) { a.clearFocused() }}
	cmdRestart     = command{ID: "pane.restart", Name: locales.Messages.CmdRestartShell, Run: func(a *App) { a.restartFocused() }}
	cmdBigger      = command{ID: "font.bigger", Name: locales.Messages.CmdBiggerText, Accel: "CmdOrCtrl+=", Keys: "⌘+", Other: shortcut{ui.Ctrl, ui.KeyEqual}, Run: func(a *App) { a.setFontSize(termFont.Size + 1) }}
	cmdSmaller     = command{ID: "font.smaller", Name: locales.Messages.CmdSmallerText, Accel: "CmdOrCtrl+-", Keys: "⌘−", Other: shortcut{ui.Ctrl, ui.KeyMinus}, Run: func(a *App) { a.setFontSize(termFont.Size - 1) }}
	cmdActualSize  = command{ID: "font.reset", Name: locales.Messages.CmdActualSize, Accel: "CmdOrCtrl+0", Keys: "⌘0", Other: shortcut{ui.Ctrl, ui.Key0}, Run: func(a *App) { a.setFontSize(0) }}
	cmdGhostty     = command{ID: "config.reload", Name: locales.Messages.CmdReloadGhostty, Run: func(a *App) { a.reloadGhostty() }}
	cmdLight       = command{ID: "appearance.light", Name: locales.Messages.CmdAppearanceLight, Run: func(a *App) { a.setAppearance("light") }}
	cmdDark        = command{ID: "appearance.dark", Name: locales.Messages.CmdAppearanceDark, Run: func(a *App) { a.setAppearance("dark") }}
	cmdSystem      = command{ID: "appearance.system", Name: locales.Messages.CmdAppearanceSystem, Run: func(a *App) { a.setAppearance("") }}
	cmdSettings    = command{ID: "app.settings", Name: locales.Messages.CmdSettings, Accel: "CmdOrCtrl+,", Keys: "⌘,", Other: shortcut{ui.Ctrl, ui.KeyComma}}
	cmdUpdates     = command{ID: "app.updates", Name: locales.Messages.CmdCheckForUpdates, Run: func(a *App) { updater.CheckForUpdates() }}
	cmdEndAll      = command{ID: "app.quit", Name: locales.Messages.CmdQuitAndEndAll, Accel: "CmdOrCtrl+Alt+Q", Keys: "⌥⌘Q", Run: func(a *App) { a.quitAndEnd() }}

	cmdNewWindow     = command{ID: "window.new", Name: locales.Messages.CmdNewWindow, Accel: "CmdOrCtrl+N", Keys: "⌘N", Other: ctrlShift(ui.KeyN)}
	cmdNewSession    = command{ID: "session.new", Name: locales.Messages.CmdNewSession, Accel: "CmdOrCtrl+Shift+N", Keys: "⇧⌘N", Run: func(a *App) { a.newWorkspace() }}
	cmdChangeSession = command{ID: "session.switch", Name: locales.Messages.CmdChangeSession, Accel: "CmdOrCtrl+Shift+O", Keys: "⇧⌘O", Other: ctrlShift(ui.KeyO), Run: func(a *App) { a.openSessionPalette() }}
	cmdNextSession   = command{ID: "session.next", Name: locales.Messages.CmdNextSession, Accel: "CmdOrCtrl+Ctrl+]", Keys: "⌃⌘]", Other: shortcut{ui.Ctrl | ui.Shift, ui.KeyPageDown}, Run: func(a *App) { a.cycleWorkspace(1) }}
	cmdPrevSession   = command{ID: "session.previous", Name: locales.Messages.CmdPreviousSession, Accel: "CmdOrCtrl+Ctrl+[", Keys: "⌃⌘[", Other: shortcut{ui.Ctrl | ui.Shift, ui.KeyPageUp}, Run: func(a *App) { a.cycleWorkspace(-1) }}
	cmdRenameSession = command{ID: "session.rename", Name: locales.Messages.CmdRenameSession, Run: func(a *App) { a.startWorkspaceRename() }}
	cmdCloseSession  = command{ID: "session.close", Name: locales.Messages.CmdCloseSession, Run: func(a *App) { a.askCloseWorkspace() }}
	cmdMoveTabWindow = command{ID: "tab.move_window", Name: locales.Messages.CmdMoveTabToNewWindow}
	cmdModeEnter     = command{
		ID: "mode.enter", Name: func(locales.Messages) string { return "Enter Mode" }, Hidden: true,
		Args: []actionArg{{Name: "name", Type: "string"}, {Name: "once", Type: "boolean", Optional: true}},
		RunArgs: func(a *App, args map[string]any) error {
			name, _ := args["name"].(string)
			once, _ := args["once"].(bool)
			a.keys.enterMode(name, once)
			return nil
		},
	}
	cmdModeExit = command{
		ID: "mode.exit", Name: func(locales.Messages) string { return "Leave Mode" }, Hidden: true,
		Run: func(a *App) { a.keys.exitMode() },
	}
	cmdModeExitAll = command{
		ID: "mode.exit_all", Name: func(locales.Messages) string { return "Leave Every Mode" }, Hidden: true,
		Run: func(a *App) { a.keys.exitAll() },
	}
)

// argString returns the string argument name, "" when absent.
func float32IsBad(n float32) bool { return math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) }

func argFloat(args map[string]any, name string) float32 {
	switch n := args[name].(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return float32(n)
	case float32:
		if float32IsBad(n) {
			return 0
		}
		return n
	case int:
		return float32(n)
	case int64:
		return float32(n)
	}
	return 0
}

func argString(args map[string]any, name string) string {
	s, _ := args[name].(string)
	return s
}

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
	&cmdNewWindow, &cmdNewSession, &cmdChangeSession, &cmdNextSession, &cmdPrevSession,
	&cmdRenameSession, &cmdCloseSession, &cmdMoveTabWindow,
}

// sessionCommands are those of the workspaces and the windows, which an
// app without a store, as in tests, does not list.
var sessionCommands = []*command{
	&cmdNewWindow, &cmdNewSession, &cmdChangeSession, &cmdNextSession, &cmdPrevSession,
	&cmdRenameSession, &cmdCloseSession, &cmdMoveTabWindow,
}

// commands are all of them.
var commands = append(slices.Clone(paletteCommands), &cmdPalette, &cmdPalette2, &cmdClear, &cmdModeEnter, &cmdModeExit, &cmdModeExitAll, &cmdPaneRatio)

func init() {
	for _, cmd := range commands {
		if cmd.ID == "" {
			panic("gorex: command without an ID: " + cmd.English())
		}
		if _, ok := commandsByID[cmd.ID]; ok {
			panic("gorex: duplicate command ID: " + cmd.ID)
		}
		commandsByID[cmd.ID] = cmd
	}
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

// overrides reports that a keymap binding took this command's menu
// shortcut over.
func (st *store) overrides(cmd *command) bool {
	if st == nil || st.noAccel == nil {
		return false
	}
	chord, ok := accelChord(cmd.menuAccel())
	return ok && st.noAccel[chord.String()]
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

// menu builds the menu bar, whose items run commands in the window they
// are chosen in, or in the one focused last.
func (st *store) menu() *mygo.Menu {
	item := func(cmd *command) *mygo.MenuItem {
		accel := cmd.menuAccel()
		if st.recording || st.overrides(cmd) {
			accel = ""
		}
		return &mygo.MenuItem{Label: cmd.Title(), Accelerator: accel, Click: func(_ *mygo.MenuItem, w *mygo.Window) {
			st.run(cmd, w)
		}}
	}
	tabItems := []*mygo.MenuItem{item(&cmdNextTab), item(&cmdPrevTab), mygo.Separator()}
	for i := 1; i <= 9; i++ {
		i := i
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
	appearance := func(label, v string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Type: mygo.MenuItemRadio, Checked: prefs.Appearance == v, Click: func(*mygo.MenuItem, *mygo.Window) {
			new(App).setAppearance(v) // it changes no window of its own
		}}
	}
	sidebarMenuItem = item(&cmdSidebar)
	sidebarMenuItem.Disabled = prefs.TabPlacement == ""
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: locales.MenuShell(), Submenu: []*mygo.MenuItem{
			item(&cmdNewTab),
			item(&cmdNewWindow),
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
		{Label: locales.MenuTabs(), Submenu: append(tabItems, mygo.Separator(), item(&cmdRenameTab), item(&cmdMoveTabWindow))},
		{Label: locales.MenuSession(), Submenu: []*mygo.MenuItem{
			item(&cmdNewSession),
			item(&cmdChangeSession),
			mygo.Separator(),
			item(&cmdNextSession),
			item(&cmdPrevSession),
			mygo.Separator(),
			item(&cmdRenameSession),
			item(&cmdCloseSession),
		}},
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
	case cmd == &cmdNewWindow || cmd == &cmdNewSession:
		mygo.RunOnMain(st.openNew)
	}
}

// run runs a command in the window, on the main thread.
func (a *App) run(cmd *command) {
	if a.win == nil {
		return
	}
	a.do(func() {
		if a.win != nil {
			a.invokeCommand(cmd, nil)
		}
	})
}

// invokeCommand runs an action on the main thread, keeping what went
// wrong for the view to say.
func (a *App) invokeCommand(cmd *command, args map[string]any) {
	if err := cmd.invoke(a, args); err != nil {
		a.err = err.Error()
	}
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
	if a.offline(old.host) {
		return
	}
	cols, rows := 80, 24
	if old.term != nil {
		cols, rows = old.term.Size()
	}
	p := a.newPane(old.host, a.currentDir(), cols, rows)
	p.Tab, p.Node = t, old.Node
	old.Node.Pane = p
	old.closed = true
	if old.term != nil {
		old.term.Close()
	}
	a.killOn(old.host, old.SID)
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
	a.paletteOpen, a.paletteQuery, a.paletteSel, a.paletteSessions = true, "", 0, false
	a.focusReq = nil // the palette takes the focus
}

// openSessionPalette opens the palette on the workspaces, to show one.
func (a *App) openSessionPalette() {
	if a.st != nil {
		a.openPalette()
		a.paletteSessions = true
	}
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
		if a.st != nil && a.st.overrides(cmd) {
			continue
		}
		if cmd.Other != (shortcut{}) && c.Shortcut(cmd.Other.Mods, cmd.Other.Key) {
			a.later(c, func() { a.invokeCommand(cmd, nil) })
		}
	}
	for i := 1; i <= 9; i++ {
		k := showTabKey(i)
		if a.st != nil && a.st.noAccel[keyChord{Mods: k.Mods, Key: k.Key}.String()] {
			continue
		}
		if c.Shortcut(k.Mods, k.Key) {
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
	// status is what the program of a pane reported.
	status *rex.ProgramStatus
}

func (a *App) paletteItems() []paletteItem {
	var items []paletteItem
	q := strings.ToLower(strings.TrimSpace(a.paletteQuery))
	match := func(s string) bool { return q == "" || fuzzy(strings.ToLower(s), q) }
	if a.paletteSessions {
		return a.sessionItems(match)
	}
	for ti, t := range a.tabs {
		for _, p := range t.panes() {
			name, detail := p.label()
			if !match(fmt.Sprintf("%s %s tab %d", name, detail, ti+1)) && !match(fmt.Sprintf("%s %s %s", name, detail, locales.PaletteTabKeys(ti+1))) {
				continue
			}
			ti, t, p := ti, t, p
			s := p.status()
			if s != nil && s.State != "idle" {
				detail = cmp.Or(s.Msg, s.Title, detail)
			}
			items = append(items, paletteItem{
				title: name, detail: detail, glyph: paneProgram(p).Glyph,
				keys: locales.PaletteTabKeys(ti + 1), status: s,
				run: func() {
					a.selectTab(ti)
					t.setFocus(p)
					a.focusReq = p
				},
			})
		}
	}
	// Without a query, the panes waiting for the user come first: blocked,
	// then failed, then done.
	if q == "" {
		slices.SortStableFunc(items, func(x, y paletteItem) int {
			rank := func(s *rex.ProgramStatus) int {
				if needsUser(s) {
					return rex.StatusRank(s.State)
				}
				return 0
			}
			return rank(y.status) - rank(x.status)
		})
	}
	for _, cmd := range paletteCommands {
		if cmd.Hidden || cmd == &cmdUpdates && !mygo.Updater.Enabled() || cmd == &cmdSidebar && prefs.TabPlacement == "" ||
			a.st == nil && slices.Contains(sessionCommands, cmd) || !match(cmd.Title()) && !match(cmd.English()) {
			continue
		}
		cmd := cmd
		items = append(items, paletteItem{title: cmd.Title(), keys: cmd.Keys, glyph: "command", run: func() { a.invokeCommand(cmd, nil) }})
		if cmd == &cmdNewTab {
			items = append(items, a.newTabOnItems(match)...)
		}
	}
	for _, cmd := range a.custom {
		if !match(cmd.Title()) && !match(cmd.English()) {
			continue
		}
		if slices.ContainsFunc(cmd.Args, func(a actionArg) bool { return !a.Optional }) {
			continue // the palette supplies no arguments
		}
		cmd := cmd
		items = append(items, paletteItem{title: cmd.Title(), glyph: "command", run: func() { a.invokeCommand(cmd, nil) }})
	}
	return items
}

// newTabOnItems are the palette's New Tab on a host, for this machine and
// each host, when there are hosts.
func (a *App) newTabOnItems(match func(string) bool) []paletteItem {
	if len(a.hosts) == 0 {
		return nil
	}
	var items []paletteItem
	for _, h := range a.allHosts() {
		name := a.hostName(h)
		title := locales.CmdNewTabOn(name)
		if !match(title) && !match(locales.In("en").CmdNewTabOn(name)) {
			continue
		}
		items = append(items, paletteItem{title: title, glyph: "plus", run: func() { a.newTabOn(h, a.dirOn(h)) }})
	}
	return items
}

// dirOn is the directory a new tab on host h opens in: the focused pane's
// when it is on h, else the home directory there.
func (a *App) dirOn(h *hostConn) string {
	if a.currentHost() == h {
		return a.currentDir()
	}
	if h != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	return home
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
			placeholder := locales.PalettePlaceholder()
			if a.paletteSessions {
				placeholder = locales.SessionPlaceholder()
			}
			in := ui.TextInputBase(c, &a.paletteQuery).Grow(1).FontSize(15).Placeholder(placeholder).AutoFocus()
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
					if s := it.status; s != nil && s.State != "idle" {
						statusDot(c, k, s, 6)
					}
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
