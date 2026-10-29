package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestKeyDispatcher(t *testing.T) {
	cmd := &command{ID: "test.action", Run: func(*App) {}}
	d := newKeyDispatcher()
	d.bind([]keyChord{{Mods: ui.Ctrl, Key: ui.KeyB}, {Key: ui.KeyC}}, cmd, nil, "")

	if got, _, ok := d.handle(ui.Ctrl, ui.KeyB); got != nil || !ok {
		t.Fatalf("prefix: command %v, taken %v", got, ok)
	}
	if n := len(d.pendingKeys()); n != 1 {
		t.Fatalf("%d keys pending, want 1", n)
	}
	got, args, ok := d.handle(0, ui.KeyC)
	if got != cmd || !ok || args != nil {
		t.Fatalf("sequence: command %v, args %v, taken %v", got, args, ok)
	}
	if n := len(d.pendingKeys()); n != 0 {
		t.Fatalf("%d keys pending after the sequence, want 0", n)
	}

	// A key that starts no binding goes to the terminal, and forgets a
	// prefix.
	d.handle(ui.Ctrl, ui.KeyB)
	if got, _, ok := d.handle(0, ui.KeyX); got != nil || ok {
		t.Fatalf("other key: command %v, taken %v", got, ok)
	}
	if n := len(d.pendingKeys()); n != 0 {
		t.Fatalf("%d keys pending after another key, want 0", n)
	}

	// Escape cancels a prefix and goes to the terminal.
	d.handle(ui.Ctrl, ui.KeyB)
	if got, _, ok := d.handle(0, ui.KeyEscape); got != nil || ok {
		t.Fatalf("escape: command %v, taken %v", got, ok)
	}
	if n := len(d.pendingKeys()); n != 0 {
		t.Fatalf("%d keys pending after escape, want 0", n)
	}
}

func TestKeyDispatcherTimeout(t *testing.T) {
	cmd := &command{ID: "test.action", Run: func(*App) {}}
	d := newKeyDispatcher()
	d.timeout = 10 * time.Millisecond
	d.bind([]keyChord{{Mods: ui.Ctrl, Key: ui.KeyB}, {Key: ui.KeyC}}, cmd, nil, "")
	d.handle(ui.Ctrl, ui.KeyB)
	d.at = time.Now().Add(-time.Second)
	if got, _, ok := d.handle(0, ui.KeyC); got != nil || ok {
		t.Fatalf("after the timeout: command %v, taken %v", got, ok)
	}
	if n := len(d.pendingKeys()); n != 0 {
		t.Fatalf("%d keys pending after the timeout, want 0", n)
	}
}

func TestKeyDispatcherArgs(t *testing.T) {
	cmd := &command{ID: "test.action", RunArgs: func(*App, map[string]any) error { return nil }}
	d := newKeyDispatcher()
	want := map[string]any{"dir": "/tmp"}
	d.bind([]keyChord{{Mods: ui.Ctrl, Key: ui.KeyB}, {Key: ui.KeyC}}, cmd, want, "")
	d.handle(ui.Ctrl, ui.KeyB)
	got, args, ok := d.handle(0, ui.KeyC)
	if got != cmd || !ok || args["dir"] != want["dir"] {
		t.Fatalf("sequence: command %v, args %v, taken %v", got, args, ok)
	}
}

func TestKeyDispatcherNil(t *testing.T) {
	var d *keyDispatcher
	if cmd, _, ok := d.handle(0, ui.KeyA); cmd != nil || ok {
		t.Fatalf("nil dispatcher: command %v, taken %v", cmd, ok)
	}
	d.bind(nil, &command{ID: "test.action"}, nil, "") // no panic
	d.reset()
	if d.pendingKeys() != nil {
		t.Error("nil dispatcher has pending keys")
	}
}

// TestKeyDispatcherInTerminal checks that the dispatcher is wired to a
// pane's terminal: a prefix is taken from the program, and the sequence
// runs its action.
func TestKeyDispatcherInTerminal(t *testing.T) {
	a, tt := newTestApp(t)
	a.keys = newKeyDispatcher()
	ran := 0
	a.keys.bind([]keyChord{{Mods: ui.Ctrl, Key: ui.KeyB}, {Key: ui.KeyC}}, &command{
		ID: "test.action", Run: func(*App) { ran++ },
	}, nil, "")
	a.focusReq = a.tab().Focus
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyB)
	tt.Frame()
	tt.Key(0, ui.KeyC)
	tt.Frame()
	if ran != 1 {
		t.Fatalf("action ran %d times, want 1", ran)
	}
}
