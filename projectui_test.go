package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gorex/locales"
)

// manyProjects adds n projects to the window's store, named p01, p02….
func manyProjects(a *App, n int) []*Workspace {
	var ws []*Workspace
	for i := range n {
		w := a.st.addWorkspace()
		w.Name = fmt.Sprintf("p%02d", i+1)
		w.Color = "blue"
		ws = append(ws, w)
	}
	return ws
}

// TestProjectPopoverMany opens the project popover of a window with 16
// projects: the list scrolls, and the actions stay in the window and
// work.
func TestProjectPopoverMany(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	a.ws.Name = "first"
	manyProjects(a, 16)
	click(t, tt, locales.SessionLabel("first"))
	tt.Frame()
	if !a.wsOpen {
		t.Fatal("the popover did not open")
	}
	const winH = 620
	for _, label := range []string{cmdNewProject.Title(), cmdNewSession.Title(), cmdRenameSession.Title(), locales.ProjectSetFolder(), cmdCloseSession.Title()} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q in the popover; texts %q", label, tt.Texts())
		}
		if r.Y < 0 || r.Y+r.H > winH {
			t.Errorf("%q is out of the window: %+v", label, r)
		}
	}
	// A project of no folder offers Set Folder…, not Remove Folder.
	if tt.HasText(locales.ProjectRemoveFolder()) || tt.HasText(locales.ProjectChangeFolder()) {
		t.Errorf("the actions of a project of no folder: %q", tt.Texts())
	}
	if !tt.HasText(locales.ProjectNoFolder() + " · " + locales.SessionTabs(1)) {
		t.Errorf("a project of no folder is not marked; texts %q", tt.Texts())
	}
	click(t, tt, cmdCloseSession.Title())
	tt.Frame()
	if !a.wsConfirm {
		t.Error("Close Project of the popover did not ask")
	}
}

// TestProjectPopoverFolder checks the popover's rows and actions of a
// project with a folder: its folder under its name, Change Folder… and
// Remove Folder, and the context menu's labels.
func TestProjectPopoverFolder(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	rightClick(t, tt, locales.SessionLabel(a.ws.Name))
	if m := tt.Menu(); !slices.Contains(m, locales.ProjectSetFolder()) || slices.Contains(m, locales.ProjectRemoveFolder()) {
		t.Errorf("the menu of a project of no folder: %q", m)
	}
	tt.CloseMenu()
	folder := filepath.Join(a.homeOf(nil), "code", "windflow")
	setProjectHome(t, a, tt, "", folder)
	want := a.projectDetail(a.ws)
	click(t, tt, locales.SessionLabel(a.ws.Name)+" · "+want)
	if !tt.HasText(want) || !tt.HasText(locales.ProjectChangeFolder()) || !tt.HasText(locales.ProjectRemoveFolder()) {
		t.Fatalf("the popover of a project with a folder; texts %q", tt.Texts())
	}
	if !strings.HasPrefix(want, "~") {
		t.Errorf("the folder of this machine is not under ~: %q", want)
	}
}

// TestProjectStripMany checks the sidebar's strip with 16 projects: the
// plus stays clear of the host button and opens New Project; the window's
// project, the 9th, out of sight at first, has its dot in sight.
func TestProjectStripMany(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	ws := manyProjects(a, 16)
	tt.Frame()
	ninth := ws[7]
	a.showWorkspace(ninth)
	tt.Frame()
	tt.Frame()
	plus, ok := tt.Find(cmdNewProject.Title())
	if !ok {
		t.Fatalf("no plus; texts %q", tt.Texts())
	}
	name, _, _ := a.hostIdentity()
	host, ok := tt.Find(locales.HostLabel(name))
	if !ok {
		t.Fatalf("no host button; texts %q", tt.Texts())
	}
	if plus.X+plus.W > host.X {
		t.Errorf("the plus %+v is under the host button %+v", plus, host)
	}
	// The window's dot shows, left of the plus.
	dot, ok := tt.Find(spaceDotLabel(8, ninth))
	if !ok {
		t.Fatalf("no dot of %s; texts %q", ninth.Name, tt.Texts())
	}
	if dot.X < 0 || dot.X+dot.W > plus.X+1 {
		t.Errorf("the window's dot %+v is out of sight, the plus at %+v", dot, plus)
	}
	click(t, tt, cmdNewProject.Title())
	tt.Frame()
	if !a.sheet.open || a.sheet.space != "" {
		t.Errorf("the plus did not open New Project: %+v", a.sheet)
	}
}

// TestProjectNoteDropped shows another project: a note about the one the
// window left goes; a note of no project stays.
func TestProjectNoteDropped(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.ws
	setProjectHome(t, a, tt, "", filepath.Join(appTempDir(t, a), "gone"))
	a.folderMissing("")
	if a.note() == nil {
		t.Fatal("no note")
	}
	other := a.st.addWorkspace()
	a.showWorkspace(other)
	tt.Frame()
	if a.errText() != "" {
		t.Errorf("the note of %s stays in %s: %q", first.Name, other.Name, a.errText())
	}
	a.err = "something else"
	a.showWorkspace(first)
	tt.Frame()
	if a.errText() != "something else" {
		t.Errorf("an error of no project went: %q", a.errText())
	}
}

// TestRemoveFolderUndo removes the folder of the window's project from the
// popover: a note says so, and its Undo gives the folder back.
func TestRemoveFolderUndo(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	folder := appTempDir(t, a)
	setProjectHome(t, a, tt, "", folder)
	w := a.ws
	click(t, tt, locales.SessionLabel(w.Name)+" · "+a.projectDetail(w))
	click(t, tt, locales.ProjectRemoveFolder())
	waitFor(t, tt, "no folder", func() bool { return w.Dir == "" && a.note() != nil })
	if n := a.note(); !n.info || n.action == nil || !strings.Contains(a.errText(), w.Name) {
		t.Fatalf("the note of Remove Folder: %q %+v", a.errText(), n)
	}
	click(t, tt, locales.ProjectUndo())
	waitFor(t, tt, "the folder back", func() bool { return w.Dir == folder })
	caughtUp(t, a, tt)
	if w.Dir != folder || w.Host != "" {
		t.Errorf("Undo gave %q %q", w.Host, w.Dir)
	}
}

// TestProjectFolderCommands checks the commands of a project's folder: ⇧⌘N
// is New Project…; Change Project Folder… runs from the palette, and the
// palette lists Remove Project Folder only for a project with a folder.
func TestProjectFolderCommands(t *testing.T) {
	a, tt := newTestApp(t)
	if cmdNewProject.Accel != "CmdOrCtrl+Shift+N" && cmdNewProject.Keys != "" || cmdNewSession.Accel != "" {
		t.Errorf("New Project %q, New Project with No Folder %q", cmdNewProject.Accel, cmdNewSession.Accel)
	}
	for _, id := range []string{"project.change_folder", "project.remove_folder"} {
		if commandsByID[id] == nil {
			t.Errorf("no command %s", id)
		}
	}
	listed := func(cmd *command) bool {
		a.openPalette()
		tt.Frame()
		defer func() { a.paletteOpen = false; tt.Frame() }()
		return tt.HasText(cmd.Title())
	}
	if !listed(&cmdChangeFolder) || listed(&cmdRemoveFolder) {
		t.Errorf("the palette of a project of no folder; texts %q", tt.Texts())
	}
	folder := appTempDir(t, a)
	setProjectHome(t, a, tt, "", folder)
	if !listed(&cmdRemoveFolder) {
		t.Errorf("no Remove Project Folder for a project with a folder")
	}
	a.invokeCommand(&cmdChangeFolder, nil)
	tt.Frame()
	if !a.sheet.open || a.sheet.space != a.ws.ID {
		t.Errorf("Change Project Folder… opened %+v", a.sheet)
	}
	a.sheet.open = false
	a.invokeCommand(&cmdRemoveFolder, nil)
	waitFor(t, tt, "no folder", func() bool { return a.ws.Dir == "" })
}
