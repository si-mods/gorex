package main

import (
	"os"
	"path/filepath"
	"testing"

	"gorex/internal/omarchy"
)

// watchPaths makes an Omarchy desktop in temporary folders, with a dark
// palette.
func watchPaths(t *testing.T) omarchy.Paths {
	t.Helper()
	root := t.TempDir()
	writeWatchFile(t, filepath.Join(root, "version"), "3.0.0\n")
	writeWatchFile(t, filepath.Join(root, "bin", "omarchy-theme-current"), "#!/bin/sh\n")
	state := t.TempDir()
	writeWatchFile(t, filepath.Join(state, "current", "theme", "colors.toml"), `mode = "dark"
background = "#1a1b26"
foreground = "#c0caf5"
`)
	writeWatchFile(t, filepath.Join(state, "current", "theme.name"), "Tokyo Night\n")
	return omarchy.Paths{OmarchyRoot: root, StateDir: state}
}

func writeWatchFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOmarchyEventAppliesOnce is the watch's decision: a swap of the
// theme folder changes the resolved palette once, and a write of the same
// content changes nothing.
func TestOmarchyEventAppliesOnce(t *testing.T) {
	p := watchPaths(t)
	pal, ok, changed := omarchyEvent(p, omarchy.Palette{}, false)
	if !ok || !changed || pal.Name != "Tokyo Night" || pal.Mode != "dark" {
		t.Fatalf("the first read: %+v %v %v", pal, ok, changed)
	}

	// Omarchy renders the next theme in a sibling directory and renames it
	// into place, with the theme's name too.
	next := filepath.Join(p.CurrentDir(), "theme.next")
	writeWatchFile(t, filepath.Join(next, "colors.toml"), `mode = "light"
background = "#f5f6f8"
foreground = "#1a1d21"
`)
	if err := os.Rename(filepath.Join(p.CurrentDir(), "theme"), filepath.Join(p.CurrentDir(), "theme.old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, filepath.Join(p.CurrentDir(), "theme")); err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, filepath.Join(p.CurrentDir(), "theme.name"), "Paper\n")

	swapped, ok, changed := omarchyEvent(p, pal, ok)
	if !ok || !changed || swapped.Name != "Paper" || swapped.Mode != "light" {
		t.Fatalf("the swap: %+v %v %v", swapped, ok, changed)
	}

	// An event that leaves the content as it is changes nothing.
	writeWatchFile(t, filepath.Join(p.CurrentDir(), "theme", "colors.toml"), `mode = "light"
background = "#f5f6f8"
foreground = "#1a1d21"
`)
	if _, _, changed := omarchyEvent(p, swapped, ok); changed {
		t.Error("the same content changed the palette")
	}

	// A palette that no longer reads is a change to the built-in palettes,
	// once.
	if err := os.Remove(filepath.Join(p.CurrentDir(), "theme", "colors.toml")); err != nil {
		t.Fatal(err)
	}
	if _, ok, changed := omarchyEvent(p, swapped, true); ok || !changed {
		t.Errorf("a palette that went away: %v %v", ok, changed)
	}
}
