package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"

	"gorex/internal/config"
	"gorex/internal/rex"
)

// reloadLuaConfig reads the Lua configuration again, keeping the last
// good one when it fails.
func (a *App) reloadLuaConfig() {
	var first error
	apps := []*App{a}
	if a.st != nil {
		apps = a.st.apps
	}
	for _, app := range apps {
		if err := app.loadLuaConfig(); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		a.err = first.Error()
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
	for _, w := range cfg.Warnings {
		log.Printf("gorex config: %s", w)
	}
	cmds, err := newCustomCommands(cfg)
	if err != nil {
		cfg.Close()
		return err
	}
	d := newKeyDispatcher()
	taken := map[string]bool{}
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
		if b.Mode == "" && len(keys) > 0 {
			taken[keys[0].String()] = true
		}
	}
	overrides, err := loadShortcuts()
	if err != nil {
		log.Printf("gorex shortcuts: %v", err)
		overrides = nil
	}
	for _, o := range overrides {
		keys, err := parseKeySequence(o.Key)
		if err != nil {
			log.Printf("gorex shortcuts: %s: %v", o.Key, err)
			continue
		}
		var cmd *command
		for _, c := range cmds {
			if c.ID == o.Action {
				cmd = c
				break
			}
		}
		if cmd == nil {
			cmd = lookupCommand(o.Action)
			if cmd != nil && slices.ContainsFunc(customCommands, func(c *command) bool { return c.ID == o.Action }) {
				cmd = nil // removed from the new configuration
			}
		}
		if cmd == nil {
			log.Printf("gorex shortcuts: no action %q", o.Action)
			continue
		}
		d.bind(keys, cmd, o.Args, "")
		if len(keys) > 0 {
			taken[keys[0].String()] = true
		}
	}
	old := a.luaConfig
	setCustomCommands(cmds)
	a.keys = d
	a.custom = cmds
	a.luaConfig = cfg
	if a.st != nil {
		a.st.noAccel = taken
		mygo.App.SetMenu(a.st.menu())
	}
	if old != nil {
		old.Close()
	}
	return nil
}

type shortcutBinding struct {
	Key    string         `json:"key"`
	Action string         `json:"action"`
	Args   map[string]any `json:"args,omitempty"`
}

type shortcutsFile struct {
	Bindings []shortcutBinding `json:"bindings"`
}

// loadShortcuts reads the app's own overrides, separate from the Lua file.
func loadShortcuts() ([]shortcutBinding, error) {
	b, err := os.ReadFile(rexShortcutsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f shortcutsFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("gorex shortcuts: %s: %w", filepath.Base(rexShortcutsPath()), err)
	}
	return f.Bindings, nil
}

func rexShortcutsPath() string { return filepath.Join(rex.Dir(), "shortcuts.json") }

func saveShortcut(action, key string) error {
	b, err := loadShortcuts()
	if err != nil {
		return err
	}
	found := false
	for i := range b {
		if b[i].Action == action {
			b[i].Key, found = key, true
		}
	}
	if !found {
		b = append(b, shortcutBinding{Key: key, Action: action})
	}
	return writeShortcuts(b)
}

func removeShortcut(action string) error {
	b, err := loadShortcuts()
	if err != nil {
		return err
	}
	out := b[:0]
	for _, o := range b {
		if o.Action != action {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		err := os.Remove(rexShortcutsPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return writeShortcuts(out)
}

func writeShortcuts(b []shortcutBinding) error {
	j, err := json.MarshalIndent(shortcutsFile{Bindings: b}, "", "  ")
	if err != nil {
		return err
	}
	j = append(j, '\n')
	if err := os.MkdirAll(filepath.Dir(rexShortcutsPath()), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(rexShortcutsPath()), ".shortcuts-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(j); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, rexShortcutsPath()); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
