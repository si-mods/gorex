package main

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"

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
	// Host, when set, is the host the action makes a session on, nil for
	// this machine: while that host is not connected, the menus and the
	// palette show the action disabled (hostAway).
	Host func(a *App) *hostConn
	// Passthrough marks an action whose key is not taken while it is
	// unavailable, and which then fails without a word.
	Passthrough bool
	// Hidden keeps it out of the palette.
	Hidden bool
	// Custom marks an action defined in the Lua configuration.
	Custom bool
	// extPlacement is the menu an extension pack's command goes in, "tab"
	// or "split", "" for a command that takes no menu slot.
	extPlacement string
}

// clientBlockedActions are actions a client may not run, even when it may
// reach the action channel.
var clientBlockedActions = map[string]bool{
	"app.quit": true, "app.updates": true, "server.restart": true, "config.reload": true,
	"session.close": true, "tab.close": true, "pane.close": true, "pane.restart": true,
	"screen.clear": true, "scrollback.clear": true,
	"mode.enter": true, "mode.exit": true, "mode.exit_all": true,
	"rex.import": true,
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

var appCommandsOnce sync.Once

// bindAppCommands wires the window/settings/server actions whose targets would
// otherwise form a static initialization cycle through the command table.
func bindAppCommands() {
	appCommandsOnce.Do(func() {
		cmdSettings.Run = func(a *App) { a.openSettings() }
		cmdRestartServer.Run = func(a *App) { a.st.restartServer() }
		cmdNewWindow.Run = func(a *App) { a.newWindowCmd() }
	})
}

// hostAway is the host the action needs that is not connected, nil when
// it can run.
func (cmd *command) hostAway(a *App) *hostConn {
	if cmd.Host == nil {
		return nil
	}
	if h := cmd.Host(a); h != nil && h.current() == nil {
		return h
	}
	return nil
}

// focusedHost is the host of the active tab's focused terminal, which a
// split or a restart makes a session on; nil for this machine and for a
// page or a note.
func focusedHost(a *App) *hostConn {
	if t := a.tab(); t != nil && t.Focus != nil && t.Focus.terminal() {
		return t.Focus.host
	}
	return nil
}

// tabHostOnline tells whether the active tab's host is connected, and
// there is a tab.
func tabHostOnline(a *App) bool {
	t := a.tab()
	if t == nil {
		return false
	}
	h := t.host()
	return h == nil || h.current() != nil
}

// tabHost is the host of the active tab, which a new float makes a
// session on.
func tabHost(a *App) *hostConn {
	if t := a.tab(); t != nil {
		return t.host()
	}
	return nil
}

// commandsByID is every built-in action by its ID, made from commands at start.
var commandsByID = initCommandsByID()

// newCustomCommands builds the actions of cfg without registering them.
func newCustomCommands(cfg *config.Config) ([]*command, error) {
	out := make([]*command, 0, len(cfg.Actions))
	for _, ca := range cfg.Actions {
		if commandsByID[ca.Name] != nil {
			return nil, fmt.Errorf("custom action %q collides with a built-in", ca.Name)
		}
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
			// Custom actions run off the main thread: one that asks a
			// question waits for its answer while the window shows it
			// (docs/MOD-009-client-prompts.md).
			Custom: true,
			RunArgs: func(app *App, args map[string]any) error {
				clock, ctx := config.NewActionClock(actionLimit)
				defer clock.Stop()
				cfg.SetInvoke(func(id string, args map[string]any) error {
					if blockedFromClient(id) {
						return fmt.Errorf("%s is not allowed from an action", id)
					}
					cmd := app.lookupCommand(id)
					if cmd == nil {
						return fmt.Errorf("no action %q", id)
					}
					// The action runs off the main thread of the app; a
					// built-in action changes the window, so it runs on it.
					// A headless view test has no loop to run it on.
					if app.win == nil {
						return cmd.invoke(app, args)
					}
					var err error
					mygo.RunOnMain(func() { err = cmd.invoke(app, args) })
					return err
				})
				cfg.SetUI(func(opts config.UIOptions) (config.UIAnswer, error) {
					// The question's time does not count.
					clock.Pause()
					defer clock.Resume()
					return app.askUI(ca.Name, opts)
				})
				err := cfg.InvokeWithin(ctx, ca.Name, map[string]any{"origin": "action"}, args)
				if err != nil && clock.Expired() {
					return fmt.Errorf("%s did not finish in %s", ca.Name, actionLimit)
				}
				return err
			},
		})
	}
	return out, nil
}

// lookupCommand returns the built-in action ID, or nil when there is none.
func lookupCommand(id string) *command {
	return commandsByID[id]
}

// lookupCommand returns the action by ID, checking custom actions on a
// before built-in actions.
func (a *App) lookupCommand(id string) *command {
	if a != nil {
		for _, c := range a.custom {
			if c.ID == id {
				return c
			}
		}
	}
	return lookupCommand(id)
}

var (
	cmdNewTab = command{
		ID: "tab.new", Name: locales.Messages.CmdNewTab, Accel: "CmdOrCtrl+T", Keys: "⌘T", Other: ctrlShift(ui.KeyT),
		Args: []actionArg{{Name: "dir", Type: "string", Optional: true}},
		Run:  func(a *App) { a.newProjectTab() },
		Host: func(a *App) *hostConn { return a.currentHost() },
		RunArgs: func(a *App, args map[string]any) error {
			// A directory the caller gives wins over the project's folder.
			dir, _ := args["dir"].(string)
			if strings.TrimSpace(dir) == "" {
				a.newProjectTab()
				return nil
			}
			a.newTab(dir)
			return nil
		},
	}
	cmdSplitRight = command{
		ID: "pane.split.right", Name: locales.Messages.CmdSplitRight, Accel: "CmdOrCtrl+D", Keys: "⌘D", Other: ctrlShift(ui.KeyD),
		Args:    []actionArg{{Name: "dir", Type: "string", Optional: true}},
		Run:     func(a *App) { a.split(false) },
		Host:    focusedHost,
		RunArgs: func(a *App, args map[string]any) error { a.splitAt(false, argString(args, "dir")); return nil },
	}
	cmdSplitDown = command{
		ID: "pane.split.down", Name: locales.Messages.CmdSplitDown, Accel: "CmdOrCtrl+Shift+D", Keys: "⇧⌘D", Other: ctrlShift(ui.KeyE),
		Args:    []actionArg{{Name: "dir", Type: "string", Optional: true}},
		Run:     func(a *App) { a.split(true) },
		Host:    focusedHost,
		RunArgs: func(a *App, args map[string]any) error { a.splitAt(true, argString(args, "dir")); return nil },
	}
	cmdClosePane    = command{ID: "pane.close", Name: locales.Messages.CmdClosePane, Accel: "CmdOrCtrl+W", Keys: "⌘W", Other: ctrlShift(ui.KeyW), Run: func(a *App) { a.closeFocused() }}
	cmdPaneToNewTab = command{ID: "pane.to_new_tab", Name: locales.Messages.CmdPaneToNewTab, Run: func(a *App) { a.paneToNewTab() }}
	cmdCloseTab     = command{ID: "tab.close", Name: locales.Messages.CmdCloseTab, Accel: "CmdOrCtrl+Shift+W", Keys: "⇧⌘W", Run: func(a *App) { a.closeActiveTab() }}
	cmdZoom         = command{
		ID: "pane.zoom", Name: locales.Messages.CmdZoomPane, Accel: "CmdOrCtrl+Shift+Enter", Keys: "⇧⌘↩", Other: ctrlShift(ui.KeyEnter),
		Args:    []actionArg{{Name: "mode", Type: "string", Optional: true, Values: []string{"on", "off", "toggle"}}},
		Run:     func(a *App) { _ = a.zoomFocused("toggle") },
		RunArgs: func(a *App, args map[string]any) error { return a.zoomFocused(argString(args, "mode")) },
	}
	cmdEqualize    = command{ID: "pane.equalize", Name: locales.Messages.CmdEqualizePanes, Accel: "CmdOrCtrl+Ctrl+=", Keys: "⌃⌘=", Run: func(a *App) { a.equalize() }}
	cmdResetCanvas = command{
		ID: "canvas.reset_view", Name: locales.Messages.CmdResetCanvasView,
		Run:       func(a *App) { a.resetCanvasView() },
		Available: func(a *App) bool { t := a.tab(); return t != nil && t.canvas != nil },
	}
	cmdFloatPane = command{
		ID: "pane.float", Name: locales.Messages.CmdFloatPane,
		Args:    []actionArg{{Name: "mode", Type: "string", Optional: true, Values: []string{"on", "off", "toggle"}}},
		Run:     func(a *App) { _ = a.floatFocused("toggle") },
		RunArgs: func(a *App, args map[string]any) error { return a.floatFocused(argString(args, "mode")) },
		Available: func(a *App) bool {
			t := a.tab()
			return t != nil && t.Focus != nil && (t.Focus.float != nil || len(t.panes()) > 1)
		},
	}
	cmdNewFloat = command{
		ID: "float.new", Name: locales.Messages.CmdNewFloat,
		Args: []actionArg{
			{Name: "x", Type: "number", Optional: true}, {Name: "y", Type: "number", Optional: true},
			{Name: "w", Type: "number", Optional: true}, {Name: "h", Type: "number", Optional: true},
		},
		Run: func(a *App) { a.newFloat(nil) },
		RunArgs: func(a *App, args map[string]any) error {
			for _, k := range []string{"x", "y", "w", "h"} {
				if _, ok := args[k]; !ok {
					a.newFloat(nil)
					return nil
				}
			}
			// The box, once the float shows, as the server makes it.
			a.newFloatThen(nil, func(p *Pane) {
				if p.float != nil {
					a.setFloatBox(p.float, argFloat(args, "x"), argFloat(args, "y"), argFloat(args, "w"), argFloat(args, "h"))
				}
			})
			return nil
		},
		Available: tabHostOnline,
		Host:      tabHost,
	}
	cmdToggleFloat = command{
		ID: "float.toggle", Name: locales.Messages.CmdToggleFloat,
		Accel: "CmdOrCtrl+Alt+F", Keys: "⌥⌘F",
		Run:       func(a *App) { a.toggleFloat() },
		Available: func(a *App) bool { return a.tab() != nil },
	}
	cmdHideFloats = command{
		ID: "float.hide_all", Name: locales.Messages.CmdHideFloats,
		Run:       func(a *App) { a.toggleAllFloats() },
		Available: func(a *App) bool { t := a.tab(); return t != nil && len(t.Floats) > 0 },
	}
	cmdFloatState = command{
		ID: "float.set_state", Name: func(locales.Messages) string { return "Set Floating Pane State" }, Hidden: true,
		Args: []actionArg{
			{Name: "sid", Type: "string"},
			{Name: "state", Type: "string", Values: []string{"normal", "minimized"}},
			{Name: "focus", Type: "boolean", Optional: true},
		},
		RunArgs: func(a *App, args map[string]any) error {
			sid, _ := args["sid"].(string)
			state, _ := args["state"].(string)
			focus, _ := args["focus"].(bool)
			return a.setFloatState(sid, state, focus)
		},
	}
	cmdRaiseFloat = command{
		ID: "float.raise", Name: locales.Messages.CmdRaiseFloat,
		Run:       func(a *App) { a.raiseFloat(a.focusedFloat()) },
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdLowerFloat = command{
		ID: "float.lower", Name: locales.Messages.CmdLowerFloat,
		Run:       func(a *App) { a.lowerFloat(a.focusedFloat()) },
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdCycleFloat = command{
		ID: "float.cycle", Name: locales.Messages.CmdCycleFloat,
		Run: func(a *App) { a.cycleFloat() },
		Available: func(a *App) bool {
			t := a.tab()
			return t != nil && slices.ContainsFunc(t.Floats, func(f *Float) bool { return !f.Hidden })
		},
	}
	cmdFloatMove = command{
		ID: "float.move", Name: func(locales.Messages) string { return "Move Floating Pane" }, Hidden: true,
		Args: []actionArg{
			{Name: "x", Type: "number", Optional: true}, {Name: "y", Type: "number", Optional: true},
			{Name: "dx", Type: "number", Optional: true}, {Name: "dy", Type: "number", Optional: true},
		},
		RunArgs: func(a *App, args map[string]any) error {
			f := a.focusedFloat()
			if f == nil {
				return nil
			}
			if _, ok := args["x"]; ok {
				f.X = argFloat(args, "x")
			}
			if _, ok := args["y"]; ok {
				f.Y = argFloat(args, "y")
			}
			f.clamp()
			a.moveFloat(f, argFloat(args, "dx"), argFloat(args, "dy"))
			return nil
		},
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdFloatResize = command{
		ID: "float.resize", Name: func(locales.Messages) string { return "Resize Floating Pane" }, Hidden: true,
		Args: []actionArg{
			{Name: "w", Type: "number", Optional: true}, {Name: "h", Type: "number", Optional: true},
			{Name: "dw", Type: "number", Optional: true}, {Name: "dh", Type: "number", Optional: true},
		},
		RunArgs: func(a *App, args map[string]any) error {
			f := a.focusedFloat()
			if f == nil {
				return nil
			}
			if _, ok := args["w"]; ok {
				f.W = argFloat(args, "w")
			}
			if _, ok := args["h"]; ok {
				f.H = argFloat(args, "h")
			}
			f.clamp()
			a.resizeFloat(f, argFloat(args, "dw"), argFloat(args, "dh"))
			return nil
		},
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdMoveFloatLeft = command{
		ID: "float.move.left", Name: locales.Messages.CmdMoveFloatLeft,
		Run:       func(a *App) { a.nudgeFloat(-0.05, 0) },
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdMoveFloatRight = command{
		ID: "float.move.right", Name: locales.Messages.CmdMoveFloatRight,
		Run:       func(a *App) { a.nudgeFloat(0.05, 0) },
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdMoveFloatUp = command{
		ID: "float.move.up", Name: locales.Messages.CmdMoveFloatUp,
		Run:       func(a *App) { a.nudgeFloat(0, -0.05) },
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}
	cmdMoveFloatDown = command{
		ID: "float.move.down", Name: locales.Messages.CmdMoveFloatDown,
		Run:       func(a *App) { a.nudgeFloat(0, 0.05) },
		Available: func(a *App) bool { return a.focusedFloat() != nil },
	}

	cmdNextTab   = command{ID: "tab.next", Name: locales.Messages.CmdNextTab, Accel: "CmdOrCtrl+Shift+]", Keys: "⇧⌘]", Other: shortcut{ui.Ctrl, ui.KeyPageDown}, Run: func(a *App) { a.cycleTab(1) }}
	cmdPrevTab   = command{ID: "tab.previous", Name: locales.Messages.CmdPreviousTab, Accel: "CmdOrCtrl+Shift+[", Keys: "⇧⌘[", Other: shortcut{ui.Ctrl, ui.KeyPageUp}, Run: func(a *App) { a.cycleTab(-1) }}
	cmdJumpNeedy = command{ID: "pane.jump_needy", Name: locales.Messages.CmdJumpToNeedy, Accel: "CmdOrCtrl+J", Keys: "⌘J", Other: ctrlShift(ui.KeyJ), Run: func(a *App) { a.jumpToNeedy() }}
	cmdNeeds     = command{ID: "pane.needs", Name: locales.Messages.CmdNeedsYou, Accel: "CmdOrCtrl+Shift+J", Keys: "⇧⌘J", Run: func(a *App) { a.openNeedsPalette() }}
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
				a.setTabName(t, name)
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
	cmdFind        = command{ID: "find.open", Name: locales.Messages.CmdFind, Accel: "CmdOrCtrl+F", Keys: "⌘F", Other: ctrlShift(ui.KeyF), Run: func(a *App) { a.openFind() }, Available: func(a *App) bool { return a.findable() }}
	cmdFindNext    = command{ID: "find.next", Name: locales.Messages.FindNext, Accel: "CmdOrCtrl+G", Keys: "⌘G", Other: ctrlShift(ui.KeyG), Run: func(a *App) { a.findNext() }, Available: func(a *App) bool { return a.findable() }}
	cmdFindPrev    = command{ID: "find.previous", Name: locales.Messages.FindPrevious, Accel: "CmdOrCtrl+Shift+G", Keys: "⇧⌘G", Run: func(a *App) { a.findPrevious() }, Available: func(a *App) bool { return a.findable() }}
	cmdRestart     = command{ID: "pane.restart", Name: locales.Messages.CmdRestartShell, Run: func(a *App) { a.restartFocused() }, Host: focusedHost}
	cmdBigger      = command{ID: "font.bigger", Name: locales.Messages.CmdBiggerText, Accel: "CmdOrCtrl+=", Keys: "⌘+", Other: shortcut{ui.Ctrl, ui.KeyEqual}, Run: func(a *App) {
		if !a.zoomPaneExt(noteSizeStep) {
			a.setFontSize(termFont.Size + 1)
		}
	}}
	cmdSmaller = command{ID: "font.smaller", Name: locales.Messages.CmdSmallerText, Accel: "CmdOrCtrl+-", Keys: "⌘−", Other: shortcut{ui.Ctrl, ui.KeyMinus}, Run: func(a *App) {
		if !a.zoomPaneExt(-noteSizeStep) {
			a.setFontSize(termFont.Size - 1)
		}
	}}
	cmdActualSize = command{ID: "font.reset", Name: locales.Messages.CmdActualSize, Accel: "CmdOrCtrl+0", Keys: "⌘0", Other: shortcut{ui.Ctrl, ui.Key0}, Run: func(a *App) {
		if !a.zoomPaneExt(0) {
			a.setFontSize(0)
		}
	}}
	cmdGhostty  = command{ID: "config.reload", Name: locales.Messages.CmdReloadGhostty, Run: func(a *App) { a.reloadGhostty() }}
	cmdLight    = command{ID: "appearance.light", Name: locales.Messages.CmdAppearanceLight, Run: func(a *App) { a.setAppearance("light") }}
	cmdDark     = command{ID: "appearance.dark", Name: locales.Messages.CmdAppearanceDark, Run: func(a *App) { a.setAppearance("dark") }}
	cmdSystem   = command{ID: "appearance.system", Name: locales.Messages.CmdAppearanceSystem, Run: func(a *App) { a.setAppearance("") }}
	cmdSettings = command{ID: "app.settings", Name: locales.Messages.CmdSettings, Accel: "CmdOrCtrl+,", Keys: "⌘,", Other: shortcut{ui.Ctrl, ui.KeyComma}}
	cmdUpdates  = command{ID: "app.updates", Name: locales.Messages.CmdCheckForUpdates, Run: func(a *App) { updater.CheckForUpdates() }}
	// cmdAmbience turns the theme's ambience off and on
	// (docs/DSK-010-theme-ambience.md); the palette lists it while the
	// theme has some.
	cmdAmbience = command{ID: "appearance.ambience.toggle", Name: locales.Messages.CmdToggleAmbience,
		Available: func(a *App) bool { return themeAmbient() }, Run: func(a *App) { a.toggleAmbience() }}
	// cmdRestartServer restarts a server an update left behind on Windows,
	// when the user chose Later (store.restartServer).
	cmdRestartServer = command{ID: "server.restart", Name: locales.Messages.CmdRestartServer, Hidden: runtime.GOOS != "windows",
		Available: func(a *App) bool { return a.st != nil && a.st.serverBehind() }}
	cmdEndAll = command{ID: "app.quit", Name: locales.Messages.CmdQuitAndEndAll, Accel: "CmdOrCtrl+Alt+Q", Keys: "⌥⌘Q", Run: func(a *App) { a.askQuitAndEnd() }}

	cmdNewWindow     = command{ID: "window.new", Name: locales.Messages.CmdNewWindow, Accel: "CmdOrCtrl+N", Keys: "⌘N", Other: ctrlShift(ui.KeyN)}
	cmdNewProject    = command{ID: "project.new", Name: locales.Messages.CmdNewProject, Accel: "CmdOrCtrl+Shift+N", Keys: "⇧⌘N", Run: func(a *App) { a.openNewProject() }}
	cmdNewSession    = command{ID: "tab.new_no_project", Name: locales.Messages.CmdNewSession, Run: func(a *App) { a.newNoProjectTab(a.currentHost()) }}
	cmdImportRex     = command{ID: "rex.import", Name: locales.Messages.CmdImportRex, Run: func(a *App) { a.importRex() }, Available: func(a *App) bool { return !rexImporting.Load() }}
	cmdChangeSession = command{ID: "session.switch", Name: locales.Messages.CmdChangeSession, Accel: "CmdOrCtrl+Shift+O", Keys: "⇧⌘O", Other: ctrlShift(ui.KeyO), Run: func(a *App) { a.openSessionPalette() }}
	cmdNextSession   = command{ID: "session.next", Name: locales.Messages.CmdNextSession, Accel: "CmdOrCtrl+Ctrl+]", Keys: "⌃⌘]", Other: shortcut{ui.Ctrl | ui.Shift, ui.KeyPageDown}, Run: func(a *App) { a.cycleWorkspace(1) }}
	cmdPrevSession   = command{ID: "session.previous", Name: locales.Messages.CmdPreviousSession, Accel: "CmdOrCtrl+Ctrl+[", Keys: "⌃⌘[", Other: shortcut{ui.Ctrl | ui.Shift, ui.KeyPageUp}, Run: func(a *App) { a.cycleWorkspace(-1) }}
	cmdRenameSession = command{ID: "session.rename", Name: locales.Messages.CmdRenameSession, Run: func(a *App) { a.startWorkspaceRename() },
		Available: func(a *App) bool { return a.ws != nil && a.ws.Dir != "" }}
	cmdCloseSession = command{ID: "session.close", Name: locales.Messages.CmdCloseSession, Run: func(a *App) { a.askCloseWorkspace() }}
	// cmdRevealFolder shows the window's project's folder in the system's
	// file manager: of this machine's projects only.
	cmdRevealFolder = command{ID: "project.reveal", Name: locales.Messages.CmdRevealProjectFolder, Run: func(a *App) { a.revealProject(a.ws) },
		Available: func(a *App) bool { return a.ws != nil && a.ws.host == nil && a.ws.Dir != "" }}

	cmdModeEnter = command{
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

// focusedFloat returns the float of the active tab's focused pane, nil when
// that pane is tiled.
func (a *App) focusedFloat() *Float {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return nil
	}
	return t.Focus.float
}

// floatFocused floats the focused tiled pane, docks the focused float, or
// does nothing, mode "on", "off" or else toggle.
func (a *App) floatFocused(mode string) error {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return nil
	}
	if t.Focus.float != nil {
		if mode != "on" {
			a.dockPane(t.Focus)
		}
		return nil
	}
	if mode != "off" {
		a.floatPane(t.Focus)
	}
	return nil
}

// nudgeFloat moves the focused float by fractions of the tab's room.
func (a *App) nudgeFloat(dx, dy float32) {
	if f := a.focusedFloat(); f != nil {
		a.moveFloat(f, dx, dy)
	}
}

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
	&cmdNewTab, &cmdSplitRight, &cmdSplitDown, &cmdZoom, &cmdEqualize, &cmdResetCanvas, &cmdPaneToNewTab, &cmdClosePane, &cmdCloseTab,
	&cmdFloatPane, &cmdNewFloat, &cmdToggleFloat, &cmdHideFloats, &cmdFloatState, &cmdRaiseFloat, &cmdLowerFloat, &cmdCycleFloat,
	&cmdFloatMove, &cmdFloatResize, &cmdMoveFloatLeft, &cmdMoveFloatRight, &cmdMoveFloatUp, &cmdMoveFloatDown,
	&cmdNextTab, &cmdPrevTab, &cmdJumpNeedy, &cmdNeeds, &cmdRenameTab, &cmdSidebar,
	&cmdFocusLeft, &cmdFocusRight, &cmdFocusUp, &cmdFocusDown,
	&cmdGrowLeft, &cmdGrowRight, &cmdGrowUp, &cmdGrowDown,
	&cmdBigger, &cmdSmaller, &cmdActualSize, &cmdLight, &cmdDark, &cmdSystem, &cmdAmbience,
	&cmdGhostty, &cmdClearScroll, &cmdFind, &cmdFindNext, &cmdFindPrev, &cmdRestart, &cmdSettings, &cmdUpdates, &cmdRestartServer, &cmdEndAll,
	&cmdNewWindow, &cmdNewProject, &cmdNewSession, &cmdImportRex, &cmdChangeSession, &cmdNextSession, &cmdPrevSession,
	&cmdRenameSession, &cmdRevealFolder, &cmdCloseSession,
}

// sessionCommands are those of the workspaces and the windows, which an
// app without a store, as in tests, does not list.
var sessionCommands = []*command{
	&cmdNewWindow, &cmdNewProject, &cmdNewSession, &cmdImportRex, &cmdChangeSession, &cmdNextSession, &cmdPrevSession,
	&cmdRenameSession, &cmdRevealFolder, &cmdCloseSession,
}

// commands are all of them, with non-macOS shortcuts applied at initialization.
var commands = func() []*command {
	list := append(slices.Clone(paletteCommands), &cmdPalette, &cmdPalette2, &cmdClear, &cmdModeEnter, &cmdModeExit, &cmdModeExitAll, &cmdPaneRatio)
	if runtime.GOOS != "darwin" {
		for _, cmd := range list {
			cmd.Accel, cmd.Keys = "", ""
			if cmd.Other != (shortcut{}) {
				cmd.Accel = cmd.Other.String()
				cmd.Keys = cmd.Accel
			}
		}
	}
	return list
}()

func initCommandsByID() map[string]*command {
	m := make(map[string]*command, len(commands))
	for _, cmd := range commands {
		m[cmd.ID] = cmd
	}
	return m
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
	if cmd.Passthrough && cmd.Available != nil && !cmd.Available(a) {
		return
	}
	// An action whose host is offline says so, rather than that it is not
	// available.
	if h := cmd.hostAway(a); h != nil {
		a.offline(h)
		return
	}
	if a.runCustom(cmd, args, func(err error) {
		if err != nil {
			a.commandFailed(cmd, err)
		}
	}) {
		return
	}
	if err := cmd.invoke(a, args); err != nil {
		a.commandFailed(cmd, err)
	}
}

// runCustom runs a custom action off the main thread, as a question it
// asks waits while the window shows it, and reports its end on the main
// thread. It returns false for a built-in action, which runs now.
func (a *App) runCustom(cmd *command, args map[string]any, report func(error)) bool {
	if !cmd.Custom {
		return false
	}
	// A headless view test has no window and no event loop to draw the
	// question: it runs the action on its own thread, as the app did
	// before the question's panel, and sees the effect at once.
	if a.win == nil {
		err := cmd.invoke(a, args)
		if report != nil {
			report(err)
		}
		return true
	}
	go func() {
		err := cmd.invoke(a, args)
		if report != nil {
			mygo.RunOnMain(func() { report(err) })
		}
	}()
	return true
}

func (a *App) closeFocused() {
	if t := a.tab(); t != nil && t.Focus != nil {
		a.askClosePane(t.Focus)
	}
}

func (a *App) closeActiveTab() {
	if t := a.tab(); t != nil {
		a.askCloseTab(t)
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
// its place, in the same directory: the server's (pane.restart), or the
// host's for a pane of a host, once the host made it.
func (a *App) restartFocused() {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return
	}
	old := t.Focus
	if old.ext != nil {
		old.ext.restart(a, old)
		return
	}
	if old.host == nil {
		if old.SID != "" && a.ws != nil {
			a.ask(rex.LayoutCommand{Op: "pane.restart", Space: a.ws.ID, SID: old.SID}, func(res rex.LayoutCommand) {
				if p := a.localPane(res.New); p != nil && a.tab() == p.Tab {
					a.focusReq = p
				}
			})
		}
		return
	}
	if a.offline(old.host) {
		return
	}
	cols, rows := old.paneSize()
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	a.newPaneRun(old.host, a.currentDir(), cols, rows, nil, func(p *Pane) bool {
		// The pane's tab once the host answers: it may have moved.
		if old.closed {
			return false
		}
		t := old.Tab
		p.Tab, p.Node = t, old.Node
		old.Node.Pane = p
		if f := old.float; f != nil {
			f.Pane = p
			p.float = f
		}
		if t.Zoom == old {
			t.Zoom = p
		}
		// The old session ends once the host has the tab with the new
		// pane: its exit would change the tab first, and the host would
		// refuse the app's change, built on the tab as it was, and show
		// its own, with no new pane.
		a.disposePane(old, false)
		a.st.sendOn(old.host, rex.LayoutCommand{Op: "layout.apply"}, func(rex.LayoutCommand) { killOn(old.host, old.SID) })
		// The focus moved meanwhile stays.
		if t.Focus == old {
			t.Focus = nil
			t.setFocus(p)
			if a.tab() == t {
				a.focusReq = p
			}
		}
		a.changed()
		return true
	})
}

func (a *App) quitAndEnd() {
	a.quitting = true
	for _, t := range a.tabs {
		for _, p := range t.allPanes() {
			p.closed = true
		}
	}
	c := a.client
	go endAndQuit(c)
}

// shortcuts handles the keys the menus do not: Control-Tab between tabs,
// and on Windows those of the commands that have no menu item. The keys of
// menu items run there before the view sees them. The window's shortcuts
// come before the terminal's keys.
func (a *App) shortcuts(c *ui.Context) {
	a.canvasSpace(c)
	if c.Shortcut(ui.Ctrl, ui.KeyTab) {
		a.cycleTab(1)
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
		a.cycleTab(-1)
	}
	for i := 1; i <= 9; i++ {
		k := showSpaceKey(i)
		if a.st != nil && a.st.noAccel[keyChord(k).String()] {
			continue
		}
		if c.Shortcut(k.Mods, k.Key) {
			a.later(c, func() { a.showSpace(i) })
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	for _, cmd := range commands {
		if a.st != nil && a.st.overrides(cmd) {
			continue
		}
		if cmd.Passthrough && cmd.Available != nil && !cmd.Available(a) {
			continue
		}
		if cmd.Other != (shortcut{}) && c.Shortcut(cmd.Other.Mods, cmd.Other.Key) {
			a.later(c, func() { a.invokeCommand(cmd, nil) })
		}
	}
	for i := 1; i <= 9; i++ {
		k := showTabKey(i)
		if a.st != nil && a.st.noAccel[keyChord(k).String()] {
			continue
		}
		if c.Shortcut(k.Mods, k.Key) {
			a.later(c, func() { a.showTab(i) })
		}
	}
}
