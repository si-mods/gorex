package main

import (
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
}

// keySequenceTimeout is how long a prefix waits for the rest of its
// sequence. Escape cancels at once.
const keySequenceTimeout = time.Second

// keyDispatcher matches key presses against bindings, keeping a prefix
// between presses. The zero value is ready; a nil *keyDispatcher handles
// nothing.
type keyDispatcher struct {
	bindings []keyBinding
	pending  []keyChord
	at       time.Time
	timeout  time.Duration
}

// newKeyDispatcher returns an empty dispatcher.
func newKeyDispatcher() *keyDispatcher {
	return &keyDispatcher{timeout: keySequenceTimeout}
}

// bind adds a binding; a later binding for the same keys wins.
func (d *keyDispatcher) bind(keys []keyChord, cmd *command, args map[string]any) {
	if d == nil || len(keys) == 0 || cmd == nil {
		return
	}
	d.bindings = append(d.bindings, keyBinding{Keys: append([]keyChord(nil), keys...), Run: cmd, Args: args})
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
	var exact *keyBinding
	var longer bool
	for i := range d.bindings {
		b := &d.bindings[i]
		if len(b.Keys) < len(candidate) {
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
