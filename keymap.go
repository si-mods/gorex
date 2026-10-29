package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// keyChord is one key press: a key and the modifiers held with it.
type keyChord struct {
	Mods ui.Modifiers
	Key  ui.Key
}

// keyBinding runs an action after keys pressed one after another. A
// binding of one key runs at once; a longer one waits for the keys after
// its prefix, up to keySequenceTimeout.
type keyBinding struct {
	Keys []keyChord
	Run  *command
	Args map[string]any
	Mode string
}

// keyMode is a named set of bindings active together.
type keyMode struct {
	exclusive bool
	once      bool
}

// keySequenceTimeout is how long a prefix waits for the rest of its
// sequence. Escape cancels at once.
const keySequenceTimeout = time.Second

// keyDispatcher matches key presses against bindings, keeping a prefix
// between presses. The zero value is ready; a nil *keyDispatcher handles
// nothing.
type keyDispatcher struct {
	bindings  []keyBinding
	modes     map[string]keyMode
	onceModes map[string]bool
	stack     []string
	pending   []keyChord
	at        time.Time
	timeout   time.Duration
}

// newKeyDispatcher returns an empty dispatcher.
func newKeyDispatcher() *keyDispatcher {
	return &keyDispatcher{timeout: keySequenceTimeout, modes: map[string]keyMode{}, onceModes: map[string]bool{}}
}

// bind adds a binding; a later binding for the same keys wins.
func (d *keyDispatcher) bind(keys []keyChord, cmd *command, args map[string]any, mode string) {
	if d == nil || len(keys) == 0 || cmd == nil {
		return
	}
	d.bindings = append(d.bindings, keyBinding{Keys: append([]keyChord(nil), keys...), Run: cmd, Args: args, Mode: mode})
}

// defineMode adds or replaces a mode.
func (d *keyDispatcher) defineMode(name string, m keyMode) {
	if d == nil || name == "" {
		return
	}
	if d.modes == nil {
		d.modes = map[string]keyMode{}
	}
	d.modes[name] = m
}

// enterMode enters a mode; once exits it after its next action.
func (d *keyDispatcher) enterMode(name string, once bool) {
	if d == nil || name == "" || len(d.stack) >= 16 {
		return
	}
	d.stack = slices.DeleteFunc(d.stack, func(s string) bool { return s == name })
	d.stack = append(d.stack, name)
	if once {
		d.onceModes[name] = true
	}
	d.reset()
}

// exitMode leaves the current mode.
func (d *keyDispatcher) exitMode() {
	if d == nil || len(d.stack) == 0 {
		return
	}
	last := d.stack[len(d.stack)-1]
	d.stack = d.stack[:len(d.stack)-1]
	delete(d.onceModes, last)
	d.reset()
}

// exitAll leaves every mode.
func (d *keyDispatcher) exitAll() {
	if d == nil {
		return
	}
	d.stack = nil
	d.onceModes = map[string]bool{}
	d.reset()
}

func (d *keyDispatcher) currentMode() string {
	if d == nil || len(d.stack) == 0 {
		return ""
	}
	return d.stack[len(d.stack)-1]
}

func (d *keyDispatcher) modeCurrent(mode string) keyMode {
	if d == nil {
		return keyMode{}
	}
	return d.modes[mode]
}

func (d *keyDispatcher) allowed(b *keyBinding, exclusive bool) bool {
	if b.Mode == "" {
		return !exclusive || b.Run.ID == "mode.exit" || b.Run.ID == "mode.exit_all"
	}
	return slices.Contains(d.stack, b.Mode)
}

// exitNamed leaves the most recent occurrence of mode.
func (d *keyDispatcher) exitNamed(mode string) {
	if d == nil {
		return
	}
	for i := len(d.stack) - 1; i >= 0; i-- {
		if d.stack[i] == mode {
			d.stack = append(d.stack[:i], d.stack[i+1:]...)
			delete(d.onceModes, mode)
			d.reset()
			return
		}
	}
}

// handle takes one key press and reports what it did: an action to run,
// with its arguments; a prefix waiting for more keys, with a nil action;
// or neither, when no binding matches and the key belongs to the terminal.
func (d *keyDispatcher) handle(mods ui.Modifiers, key ui.Key) (*command, map[string]any, bool) {
	if d == nil {
		return nil, nil, false
	}
	if len(d.pending) > 0 && !d.at.IsZero() && time.Since(d.at) > d.sequenceTimeout() {
		d.reset()
	}
	chord := keyChord{Mods: mods, Key: key}
	// Escape cancels a prefix and goes to the terminal.
	if len(d.pending) > 0 && key == ui.KeyEscape {
		d.reset()
		return nil, nil, false
	}
	candidate := append(append([]keyChord(nil), d.pending...), chord)
	mode := d.currentMode()
	exclusive := d.modeCurrent(mode).exclusive
	var exact *keyBinding
	var longer bool
	for i := range d.bindings {
		b := &d.bindings[i]
		if !d.allowed(b, exclusive) || len(b.Keys) < len(candidate) {
			continue
		}
		match := true
		for j := range candidate {
			if b.Keys[j] != candidate[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if len(b.Keys) == len(candidate) {
			exact = b // the latest binding wins
		} else {
			longer = true
		}
	}
	if exact != nil && !longer {
		d.reset()
		if exact.Run.ID != "mode.exit" && exact.Run.ID != "mode.exit_all" {
			if exact.Mode != "" && (d.modeCurrent(exact.Mode).once || d.onceModes[exact.Mode]) {
				d.exitNamed(exact.Mode)
			} else if exact.Mode == "" && mode != "" && (d.modeCurrent(mode).once || d.onceModes[mode]) {
				d.exitMode()
			}
		}
		return exact.Run, exact.Args, true
	}
	if exact != nil || longer {
		d.pending, d.at = candidate, time.Now()
		return nil, nil, true
	}
	d.reset()
	return nil, nil, false
}

// dispatchKey gives a key press to the keymap, running the action it
// names; it reports whether the map took the key.
func (a *App) dispatchKey(mods ui.Modifiers, key ui.Key) bool {
	cmd, args, ok := a.keys.handle(mods, key)
	if cmd == nil {
		return ok
	}
	if err := cmd.invoke(a, args); err != nil {
		a.err = err.Error()
	}
	return true
}

// reset forgets a prefix.
func (d *keyDispatcher) reset() {
	if d != nil {
		d.pending, d.at = nil, time.Time{}
	}
}

// pendingKeys returns the keys waiting for the rest of a sequence.
func (d *keyDispatcher) pendingKeys() []keyChord {
	if d == nil {
		return nil
	}
	return append([]keyChord(nil), d.pending...)
}

func (d *keyDispatcher) sequenceTimeout() time.Duration {
	if d.timeout > 0 {
		return d.timeout
	}
	return keySequenceTimeout
}

// parseKeySequence reads "ctrl+b>c", "cmd+shift+d" or a single key.
func parseKeySequence(s string) ([]keyChord, error) {
	parts := strings.Split(s, ">")
	out := make([]keyChord, 0, len(parts))
	for _, part := range parts {
		chord, err := parseKeyChord(part)
		if err != nil {
			return nil, err
		}
		out = append(out, chord)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty key")
	}
	return out, nil
}

func parseKeyChord(s string) (keyChord, error) {
	parts := strings.Split(strings.TrimSpace(s), "+")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return keyChord{}, fmt.Errorf("empty key in %q", s)
	}
	var mods ui.Modifiers
	for _, p := range parts[:len(parts)-1] {
		m, ok := keybindMods[strings.ToLower(p)]
		if !ok {
			return keyChord{}, fmt.Errorf("unknown modifier %q", p)
		}
		mods |= m
	}
	name := strings.ToLower(parts[len(parts)-1])
	key, ok := keybindKeys[name]
	if !ok {
		switch name {
		case "up":
			key, ok = ui.KeyUp, true
		case "down":
			key, ok = ui.KeyDown, true
		case "left":
			key, ok = ui.KeyLeft, true
		case "right":
			key, ok = ui.KeyRight, true
		case "pageup":
			key, ok = ui.KeyPageUp, true
		case "pagedown":
			key, ok = ui.KeyPageDown, true
		}
	}
	if !ok {
		return keyChord{}, fmt.Errorf("unknown key %q", parts[len(parts)-1])
	}
	return keyChord{Mods: mods, Key: key}, nil
}
