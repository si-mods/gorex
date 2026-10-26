package main

import (
	"runtime"
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
			t.Errorf("%s: %q takes Control and Alt", cmd.Title, a)
		}
		if a == "Ctrl+"+key && len(key) == 1 && key >= "A" && key <= "Z" {
			t.Errorf("%s: %q takes Control and a letter", cmd.Title, a)
		}
		if strings.Contains(a, "CmdOrCtrl") {
			t.Errorf("%s: %q is macOS's shortcut", cmd.Title, a)
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
			t.Errorf("%s and %s both take %q", o, cmd.Title, a)
		}
		seen[a] = cmd.Title
	}
}
