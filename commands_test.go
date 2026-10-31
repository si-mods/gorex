package main

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestMenuAccelsLeaveTerminalKeys checks that, elsewhere than on macOS, no
// menu item takes Control and a letter, which are the terminal's, or
// Control and Alt, which type characters (AltGr): the menus' shortcuts run
// before the terminal gets the key.
func TestMenuAccelsLeaveTerminalKeys(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS's menus take Command")
	}
	for _, cmd := range commands {
		a := cmd.menuAccel()
		if a == "" {
			continue
		}
		parts := strings.Split(a, "+")
		key := parts[len(parts)-1]
		if strings.Contains(a, "Ctrl") && strings.Contains(a, "Alt") {
			t.Errorf("%s: %q takes Control and Alt", cmd.English(), a)
		}
		if a == "Ctrl+"+key && len(key) == 1 && key >= "A" && key <= "Z" {
			t.Errorf("%s: %q takes Control and a letter", cmd.English(), a)
		}
		if strings.Contains(a, "CmdOrCtrl") {
			t.Errorf("%s: %q is macOS's shortcut", cmd.English(), a)
		}
	}
}

// TestAccelsUnique checks that no two commands take the same shortcut.
func TestAccelsUnique(t *testing.T) {
	seen := map[string]string{}
	for _, cmd := range commands {
		a := cmd.menuAccel()
		if a == "" {
			continue
		}
		if o, ok := seen[a]; ok {
			t.Errorf("%s and %s both take %q", o, cmd.English(), a)
		}
		seen[a] = cmd.English()
	}
}

// TestActionRegistry checks that every action has a unique, stable ID and
// that the lookup finds it: menus, the palette, the CLI and the keymap all
// refer to actions by it.
func TestActionRegistry(t *testing.T) {
	if len(commandsByID) != len(commands) {
		t.Fatalf("%d actions by ID, %d registered", len(commandsByID), len(commands))
	}
	seen := map[string]*command{}
	for _, cmd := range commands {
		if cmd.ID == "" {
			t.Errorf("%s has no ID", cmd.English())
			continue
		}
		if cmd.ID != strings.ToLower(cmd.ID) || strings.Contains(cmd.ID, " ") {
			t.Errorf("ID %q is not lower-case and without spaces", cmd.ID)
		}
		if old := seen[cmd.ID]; old != nil {
			t.Errorf("%s and %s share ID %q", old.English(), cmd.English(), cmd.ID)
		}
		seen[cmd.ID] = cmd
		if got := lookupCommand(cmd.ID); got != cmd {
			t.Errorf("lookup %q = %p, want %p", cmd.ID, got, cmd)
		}
	}
	if lookupCommand("no.such.action") != nil {
		t.Error("lookup found an action that does not exist")
	}
}

// TestActionArgs checks that the arguments of the actions are valid: each
// name once, a known type, and a value from Values when one is set.
func TestActionArgs(t *testing.T) {
	types := map[string]bool{"": true, "any": true, "string": true, "number": true, "integer": true, "boolean": true, "object": true, "array": true}
	for _, cmd := range commands {
		names := map[string]bool{}
		for _, a := range cmd.Args {
			if a.Name == "" || names[a.Name] {
				t.Errorf("%s: argument %q is empty or repeated", cmd.ID, a.Name)
			}
			names[a.Name] = true
			if !types[a.Type] {
				t.Errorf("%s: argument %s has type %q", cmd.ID, a.Name, a.Type)
			}
		}
	}
}

// TestActionValidateArgs checks the types and required arguments.
func TestActionValidateArgs(t *testing.T) {
	cmd := &command{ID: "test", Args: []actionArg{
		{Name: "name", Type: "string"},
		{Name: "count", Type: "integer", Optional: true},
		{Name: "mode", Type: "string", Optional: true, Values: []string{"on", "off"}},
	}}
	for _, tc := range []struct {
		args map[string]any
		ok   bool
	}{
		{map[string]any{"name": "x"}, true},
		{map[string]any{"name": "x", "count": 2.0}, true},
		{map[string]any{"name": "x", "mode": "on"}, true},
		{map[string]any{"name": "x", "mode": "toggle"}, false},
		{map[string]any{"name": 1}, false},
		{map[string]any{"name": "x", "count": 1.5}, false},
		{map[string]any{"name": "x", "other": true}, false},
		{map[string]any{}, false},
	} {
		if err := cmd.validateArgs(tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: error %v, want ok %v", tc.args, err, tc.ok)
		}
	}
}

// TestActionInvoke checks that invoke passes arguments and refuses one the
// action cannot take now.
func TestActionInvoke(t *testing.T) {
	var got map[string]any
	cmd := &command{ID: "test", Args: []actionArg{{Name: "dir", Type: "string", Optional: true}}, RunArgs: func(_ *App, args map[string]any) error {
		got = args
		return nil
	}}
	if err := cmd.invoke(nil, map[string]any{"dir": "/tmp"}); err != nil || got["dir"] != "/tmp" {
		t.Fatalf("invoke: %v, %v", err, got)
	}
	cmd.Available = func(*App) bool { return false }
	if err := cmd.invoke(nil, nil); err == nil {
		t.Fatal("an unavailable action ran")
	}
}

// TestImportRexCommand checks that importing a Rex server's sessions is an
// action of the palette and the Space menu.
func TestImportRexCommand(t *testing.T) {
	cmd := lookupCommand("rex.import")
	if cmd == nil {
		t.Fatal("rex.import is not an action")
	}
	if got := cmd.English(); got != "Import from Rex" {
		t.Errorf("rex.import is named %q", got)
	}
	if !slices.Contains(paletteCommands, cmd) || !slices.Contains(sessionCommands, cmd) {
		t.Error("rex.import is not in the palette")
	}
	if cmd.Run == nil {
		t.Error("rex.import does nothing")
	}
}
