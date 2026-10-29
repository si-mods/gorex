package main

import (
	"fmt"
	"slices"

	"gorex/internal/config"
)

// reloadLuaConfig reads the Lua configuration again, keeping the last
// good one when it fails.
func (a *App) reloadLuaConfig() {
	if err := a.loadLuaConfig(); err != nil {
		a.err = err.Error()
		return
	}
	a.err = ""
	a.changed()
}

// loadLuaConfig builds the keymap and custom actions of the Lua file, and
// swaps them in only once all of it reads.
func (a *App) loadLuaConfig() error {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return err
	}
	cmds, err := newCustomCommands(cfg)
	if err != nil {
		cfg.Close()
		return err
	}
	d := newKeyDispatcher()
	for _, m := range cfg.Modes {
		d.defineMode(m.Name, keyMode{exclusive: m.Exclusive, once: m.Once})
	}
	for _, b := range cfg.Bindings {
		keys, err := parseKeySequence(b.Key)
		if err != nil {
			cfg.Close()
			return fmt.Errorf("gorex config: %s: %w", b.Key, err)
		}
		var cmd *command
		for _, c := range cmds {
			if c.ID == b.Action {
				cmd = c
				break
			}
		}
		if cmd == nil {
			cmd = lookupCommand(b.Action)
			if cmd != nil && slices.ContainsFunc(customCommands, func(c *command) bool { return c.ID == b.Action }) {
				cmd = nil // removed from the new configuration
			}
		}
		if cmd == nil {
			cfg.Close()
			return fmt.Errorf("gorex config: no action %q", b.Action)
		}
		d.bind(keys, cmd, b.Args, b.Mode)
	}
	old := a.luaConfig
	setCustomCommands(cmds)
	a.keys = d
	a.luaConfig = cfg
	if old != nil {
		old.Close()
	}
	return nil
}
