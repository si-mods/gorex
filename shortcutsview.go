package main

import (
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type shortcutsState struct {
	recording *command
	err       string
}

var shortcutsUI shortcutsState

var recordKeys = func() []ui.Key {
	keys := []ui.Key{
		ui.KeyEnter, ui.KeyTab, ui.KeySpace, ui.KeyBackspace, ui.KeyDelete,
		ui.KeyLeft, ui.KeyRight, ui.KeyUp, ui.KeyDown, ui.KeyHome, ui.KeyEnd,
		ui.KeyPageUp, ui.KeyPageDown, ui.KeyEqual, ui.KeyMinus, ui.KeyComma,
		ui.KeyPeriod, ui.KeySlash, ui.KeySemicolon, ui.KeyQuote, ui.KeyBackquote,
		ui.KeyBracketLeft, ui.KeyBracketRight, ui.KeyBackslash,
	}
	for k := ui.KeyA; k <= ui.KeyZ; k++ {
		keys = append(keys, k)
	}
	for k := ui.Key0; k <= ui.Key9; k++ {
		keys = append(keys, k)
	}
	for k := ui.KeyF1; k <= ui.KeyF12; k++ {
		keys = append(keys, k)
	}
	return keys
}()

var recordMods = func() []ui.Modifiers {
	var out []ui.Modifiers
	for i := 0; i < 16; i++ {
		var m ui.Modifiers
		if i&1 != 0 {
			m |= ui.Super
		}
		if i&2 != 0 {
			m |= ui.Ctrl
		}
		if i&4 != 0 {
			m |= ui.Alt
		}
		if i&8 != 0 {
			m |= ui.Shift
		}
		out = append(out, m)
	}
	return out
}()

// rebuildMenu is set by init: the menu must be rebuilt without
// accelerators while the keyboard records.
var rebuildMenu func(*store)
var reloadConfig func(*App)

func init() {
	rebuildMenu = func(st *store) {
		mygo.App.SetMenu(st.menu())
	}
	reloadConfig = func(a *App) {
		if err := a.loadLuaConfig(); err != nil {
			a.err = err.Error()
		}
	}
}

func (a *App) startRecording(cmd *command) {
	shortcutsUI.recording, shortcutsUI.err = cmd, ""
	if a.st != nil {
		a.st.recording = true
		rebuildMenu(a.st)
	}
}

func (a *App) stopRecording() {
	shortcutsUI.recording = nil
	if a.st != nil {
		a.st.recording = false
		rebuildMenu(a.st)
	}
}

// recordShortcut takes the next key press for the command being recorded.
func (a *App) recordShortcut(c *ui.Context) {
	if shortcutsUI.recording == nil {
		return
	}
	if c.Shortcut(0, ui.KeyEscape) {
		a.stopRecording()
		return
	}
	for _, mods := range recordMods {
		for _, key := range recordKeys {
			if !c.Shortcut(mods, key) {
				continue
			}
			chord := keyChord{Mods: mods, Key: key}.String()
			if owner, ok := conflictingShortcut(shortcutsUI.recording.ID, chord); ok {
				shortcutsUI.err = "Already bound to " + owner
				return
			}
			if err := saveShortcut(shortcutsUI.recording.ID, chord); err != nil {
				shortcutsUI.err = err.Error()
				return
			}
			a.stopRecording()
			reloadConfig(a)
			return
		}
	}
}

func conflictingShortcut(action, key string) (string, bool) {
	b, err := loadShortcuts()
	if err != nil {
		return "", false
	}
	for _, o := range b {
		if o.Action != action && sameShortcut(o.Key, key) {
			return o.Action, true
		}
	}
	return "", false
}

func sameShortcut(a, b string) bool {
	ka, err := parseKeySequence(a)
	if err != nil {
		return false
	}
	kb, err := parseKeySequence(b)
	if err != nil || len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

func (a *App) resetShortcut(cmd *command) {
	if err := removeShortcut(cmd.ID); err != nil {
		a.err = err.Error()
		return
	}
	reloadConfig(a)
}

func (a *App) resetAllShortcuts() {
	if err := os.Remove(rexShortcutsPath()); err != nil && !os.IsNotExist(err) {
		a.err = err.Error()
		return
	}
	reloadConfig(a)
}

// resetAllButton is the keyboard section's reset.
func resetAllButton(a *App, c *ui.Context) {
	if ui.Button(c, "Reset All").Clicked() {
		a.resetAllShortcuts()
	}
}
