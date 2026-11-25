package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gorex/internal/ext"
)

// writeExtPack writes a pack of manifest and page into dir/<id>.
func writeExtPack(t *testing.T, dir, id, manifest string) {
	t.Helper()
	d := filepath.Join(dir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		ext.File:     manifest,
		"index.html": "<html><body></body></html>",
	} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const demoExtManifest = `{
	"id": "demo", "name": "Demo", "engine": "webview", "api": 1,
	"panes": [{"kind": "demo.note", "title": "Demo Note",
		"fields": ["note", "noteSize", "notePreview", "id"], "files": ["{note}.txt"]}],
	"permissions": ["state", "storage", "invoke", "env"],
	"commands": [
		{"id": "new", "title": "New Demo", "kind": "demo.note", "placement": "tab"},
		{"id": "split", "title": "Split Demo", "kind": "demo.note", "placement": "split"},
		{"id": "act", "title": "Act", "action": "new-tab"}
	]
}`

// loadTestPacks serves the packs of dir during the test, and puts the
// pack state back after it.
func loadTestPacks(t *testing.T, dir string) {
	t.Helper()
	oldPacks, oldCmds := extPacks.packs, extPacks.cmds
	oldTab, oldSplit := extPacks.tabCmds, extPacks.splitCmds
	oldDisabled := prefs.DisabledExts
	oldDir := extsDirFn
	extsDirFn = func() string { return dir }
	t.Cleanup(func() {
		for _, c := range extPacks.cmds {
			delete(commandsByID, c.ID)
		}
		paletteCommands = slices.DeleteFunc(paletteCommands, func(c *command) bool {
			return slices.Contains(extPacks.cmds, c)
		})
		commands = slices.DeleteFunc(commands, func(c *command) bool {
			return slices.Contains(extPacks.cmds, c)
		})
		extsDirFn = oldDir
		prefs.DisabledExts = oldDisabled
		extPacks.packs, extPacks.cmds = oldPacks, oldCmds
		extPacks.tabCmds, extPacks.splitCmds = oldTab, oldSplit
	})
	loadExtPacks()
	for _, err := range extPacks.errs {
		t.Fatalf("the pack did not load: %v", err)
	}
}

func TestLoadExtPacks(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "demo", demoExtManifest)
	loadTestPacks(t, dir)

	def := paneKindOf(&savedNode{Kind: "demo.note"})
	if def == nil {
		t.Fatal("the pack's pane kind is not served")
	}
	for _, id := range []string{"ext.demo.new", "ext.demo.split", "ext.demo.act"} {
		if commandsByID[id] == nil {
			t.Fatalf("command %s not registered", id)
		}
		if !slices.Contains(paletteCommands, commandsByID[id]) {
			t.Fatalf("command %s not in the palette", id)
		}
	}
	if len(extPacks.tabCmds) != 1 || extPacks.tabCmds[0].ID != "ext.demo.new" {
		t.Fatalf("tabCmds: %v", extPacks.tabCmds)
	}
	if len(extPacks.splitCmds) != 1 || extPacks.splitCmds[0].ID != "ext.demo.split" {
		t.Fatalf("splitCmds: %v", extPacks.splitCmds)
	}
}

func TestLoadExtPacksDisabled(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "demo", demoExtManifest)
	loadTestPacks(t, dir)
	prefs.DisabledExts = []string{"demo"}
	loadExtPacks()

	a := &App{}
	p := a.extPaneLoad(&savedNode{Kind: "demo.note"}, func(*savedNode) bool { return false })
	if p == nil {
		t.Fatal("a disabled pack's node must load as a placeholder")
	}
	if _, ok := p.ext.(*placeholderExt); !ok {
		t.Fatalf("a disabled pack's node loaded as %T, not a placeholder", p.ext)
	}
	if commandsByID["ext.demo.new"] != nil {
		t.Fatal("a disabled pack's commands must not be served")
	}
}

func TestExtPaneSaveLoad(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "demo", demoExtManifest)
	loadTestPacks(t, dir)

	// A windowless app makes the pane without its web views; the node's
	// fields still go through save and load.
	a := &App{}
	p := a.newExtPane("demo.note")
	if p == nil || p.ext == nil {
		t.Fatal("the pack's kind did not spawn")
	}
	e := p.ext.(*extPane)
	if e.keep.Kind != "demo.note" || e.keep.ID == "" {
		t.Fatalf("keep: %+v", e.keep)
	}
	var n savedNode
	p.ext.save(p, &n)
	if n.Kind != "demo.note" || n.ID != e.keep.ID {
		t.Fatalf("save: %+v", n)
	}
	n.Note = "abc123"
	lp := paneKindOf(&n).load(a, &n, func(*savedNode) bool { return false })
	le := lp.ext.(*extPane)
	if le.keep.Note != "abc123" || le.keep.ID != n.ID {
		t.Fatalf("load: %+v", le.keep)
	}
	// A taken id spawns a fresh one.
	lp2 := paneKindOf(&n).load(a, &n, func(*savedNode) bool { return true })
	if lp2.ext.(*extPane).keep.ID == n.ID {
		t.Fatal("a taken id must not be reused")
	}
}

func TestExtPaneApply(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "demo", demoExtManifest)
	loadTestPacks(t, dir)

	a := &App{}
	p := a.newExtPane("demo.note")
	e := p.ext.(*extPane)
	e.layoutSaved(p)

	// The same node the pane wrote changes nothing.
	s := e.keep
	e.apply(a, p, &s)
	// A node another client wrote is taken.
	s.Note = "other"
	e.apply(a, p, &s)
	if e.keep.Note != "other" {
		t.Fatalf("apply: %+v", e.keep)
	}
}

func TestExtSetFields(t *testing.T) {
	fields := []string{"note", "noteSize", "notePreview", "url", "id"}
	s := &savedNode{}
	err := extSetFields(s, fields, map[string]any{
		"note": "abc", "noteSize": float64(80), "notePreview": true, "url": "https://x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Note != "abc" || s.NoteSize != 80 || !s.NotePreview || s.URL != "https://x" {
		t.Fatalf("fields: %+v", s)
	}
	if got := extNodeFields(s, fields); got["note"] != "abc" || got["noteSize"] != float64(80) ||
		got["notePreview"] != true || got["url"] != "https://x" {
		t.Fatalf("nodeFields: %v", got)
	}
	if err := extSetFields(s, fields, map[string]any{"note": 3.0}); err == nil {
		t.Fatal("a number for a string field must fail")
	}
	if err := extSetFields(s, fields, map[string]any{"bogus": 1.0}); err == nil {
		t.Fatal("an undeclared field must fail")
	}
	if err := extSetFields(s, fields, map[string]any{"id": "x"}); err == nil {
		t.Fatal("the pane's own id must not be settable")
	}
}

// A kind that declares a note field and a files pattern is the note
// pane's replacement: its file id is made at spawn, its key dedupes on
// it, its zoom is the noteSize field, and closing it deletes its file.
func TestExtPaneNotesParity(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "demo", demoExtManifest)
	loadTestPacks(t, dir)
	t.Setenv("GOREX_DIR", t.TempDir())

	a := &App{}
	p := a.newExtPane("demo.note")
	e := p.ext.(*extPane)
	if e.keep.Note == "" {
		t.Fatal("a note field is not filled at spawn")
	}

	// Two panes of the same note dedupe, as the note pane's did.
	if k := savedNodeKey(&savedNode{Kind: "demo.note", Note: "abc"}); k != "ext:demo.note:abc" {
		t.Fatalf("key: %q", k)
	}
	if savedNodeKey(&savedNode{Kind: "demo.note"}) != "" {
		t.Fatal("a pane with no note must not share the empty key")
	}

	// ⌘+ sizes the noteSize field, ⌘0 puts back the default, as the note
	// pane's text size did.
	if !e.zoom(a, p, noteSizeStep) || e.keep.NoteSize != noteTextDefault+noteSizeStep {
		t.Fatalf("zoom: %+v", e.keep)
	}
	if !e.zoom(a, p, 0) || e.keep.NoteSize != 0 {
		t.Fatalf("reset zoom: %+v", e.keep)
	}

	// Closing the pane deletes its file, as a note's <id>.txt went.
	root, err := e.storageRoot()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, e.keep.Note+".txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.destroy(true)
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatalf("the note file is still there: %v", err)
	}
}

// A kind may ask for the tiled background to show through, as the note
// pane's page did.
func TestExtPaneOpaqueTiled(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "demo", `{
		"id": "demo", "name": "Demo", "engine": "webview", "api": 1,
		"panes": [
			{"kind": "demo.solid", "title": "Solid"},
			{"kind": "demo.clear", "title": "Clear", "opaque_tiled": false}
		]
	}`)
	loadTestPacks(t, dir)

	a := &App{}
	if !a.newExtPane("demo.solid").ext.opaqueTiled() {
		t.Fatal("opaque_tiled must default to true")
	}
	if a.newExtPane("demo.clear").ext.opaqueTiled() {
		t.Fatal("opaque_tiled: false is not honored")
	}
}
