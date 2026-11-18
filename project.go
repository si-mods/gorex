package main

import (
	"cmp"
	"log"
	"os"
	"runtime"
	"strings"

	"gorex/internal/rex"
	"gorex/locales"
)

// bannerNote is how the error banner shows the message it was set with:
// as a wait, which is not an error, and with a button that acts on it.
// It holds for that message only (noteShows): another message the banner
// shows has neither.
type bannerNote struct {
	// text is the message it came with, as it was said.
	text string
	wait bool
	// action names the button, run is what it does; alt and altRun are a
	// second button, after it.
	action func(locales.Messages) string
	run    func()
	alt    func(locales.Messages) string
	altRun func()
	// info is a note that tells what was done, not an error.
	info bool
	// space is the ID of the project the note is about, "" for none: the
	// window showing another project drops it (show).
	space string
}

// clearNote takes the banner's message away.
func (a *App) clearNote() { a.err, a.errMsg, a.errNote = "", nil, nil }

// dropProjectNote takes away the banner's note when it is about a project
// other than the one of ID id.
func (a *App) dropProjectNote(id string) {
	if n := a.errNote; n != nil && n.space != "" && n.space != id && a.noteShows() {
		a.clearNote()
	}
}

// setNote has the banner say msg, as n says.
func (a *App) setNote(msg func(locales.Messages) string, n bannerNote) {
	n.text = say(msg)
	a.err, a.errMsg, a.errNote = "", msg, &n
}

// note is how the banner shows its message: nil for an error of no button.
func (a *App) note() *bannerNote {
	if a.noteShows() {
		return a.errNote
	}
	return nil
}

// noteShows tells whether the banner shows the message errNote came with.
func (a *App) noteShows() bool {
	n := a.errNote
	return n != nil && (a.err != "" || a.errMsg != nil) && a.errText() == n.text
}

// folderMissing says that New Tab of the window's project opened no tab,
// its folder dir ("" for the project's) gone, with buttons that reveal
// the folder's place (this machine's) or close the project.
func (a *App) folderMissing(dir string) {
	w := a.ws
	if w == nil {
		return
	}
	if dir == "" {
		dir = w.Dir
	}
	name, id, shown := w.Name, w.ID, a.tildeOn(w.host, dir)
	n := bannerNote{space: id, action: locales.Messages.CmdCloseProject, run: func() {
		if a.ws != nil && a.ws.ID == id {
			a.askCloseWorkspace()
		}
	}}
	a.setNote(func(m locales.Messages) string { return m.ProjectFolderMissing(shown, name) }, n)
}

// homeOf is the home directory of host h, nil for this machine, as its
// server last said; "" when it never did.
func (a *App) homeOf(h *hostConn) string {
	if h == nil {
		if a.hello.Host.Home != "" {
			return a.hello.Host.Home
		}
		home, _ := os.UserHomeDir()
		return home
	}
	return h.hello.Host.Home
}

// tildeOn is dir, a folder of host h, as the user reads it: under the
// host's home directory as ~.
func (a *App) tildeOn(h *hostConn, dir string) string { return tildePath(dir, a.homeOf(h)) }

// tildePath writes dir under home as ~, with dir's own separators.
func tildePath(dir, home string) string {
	home = strings.TrimRight(home, `/\`)
	if home == "" || dir == "" {
		return dir
	}
	same := func(x, y string) bool {
		if winPath(x) {
			return strings.EqualFold(x, y)
		}
		return x == y
	}
	if same(dir, home) {
		return "~"
	}
	if len(dir) > len(home) && same(dir[:len(home)], home) && (dir[len(home)] == '/' || dir[len(home)] == '\\') {
		return "~" + dir[len(home):]
	}
	return dir
}

// winPath tells a path of Windows: one with a drive, or of a share.
func winPath(p string) bool {
	return len(p) >= 2 && p[1] == ':' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') || strings.HasPrefix(p, `\\`)
}

// projectDetail is what a project's row says beside its name: its
// folder, under its host's home as ~; "" for the tabs with no project.
func (a *App) projectDetail(w *Workspace) string {
	if w == nil || w.Dir == "" {
		return ""
	}
	return a.tildeOn(w.host, w.Dir)
}

// createProject makes a project named name of the folder dir on host h,
// nil for this machine, in the space shown (docs/projects.md, "New
// Project"): the host makes it and its first tab, in the folder, which
// the window shows. A folder that is a project of the host already shows
// that one, and, when it is in another space, asks to move it here.
func (a *App) createProject(h *hostConn, dir, name string) {
	if a.st == nil || dir == "" {
		return
	}
	space := shownSpace()
	name = cmp.Or(strings.TrimSpace(name), folderName(dir))
	c := rex.LayoutCommand{Op: "project.create", Dir: dir, Name: name, SpaceName: &space}
	a.askOn(h, c, func(res rex.LayoutCommand) {
		switch res.Code {
		case "":
			if w := a.st.byID(res.Space); w != nil {
				a.showWorkspace(w)
			} else if w := a.st.workspaceOfTab(h, res.Tab); w != nil {
				a.showWorkspace(w)
			}
		case "exists":
			a.clearNote()
			w := a.st.byID(res.Space)
			if w == nil {
				log.Printf("project.create of %s: exists, as %s, which the app does not have", dir, res.Space)
				return
			}
			a.showWorkspace(w)
			if w.Space != space {
				id, from := w.ID, spaceLabel(w.Space)
				a.setNote(func(m locales.Messages) string { return m.ProjectInOtherSpace(w.Name, from) }, bannerNote{
					space: id, info: true,
					action: locales.Messages.ProjectMoveHere,
					run: func() {
						if w := a.st.byID(id); w != nil {
							a.moveProjectToSpace(w, space)
						}
					},
				})
			}
		}
	})
}

// openNewProject opens the New Project sheet, on this machine first.
func (a *App) openNewProject() { a.openSheet(nil, "") }

// openNewProjectOn opens the New Project sheet on host h.
func (a *App) openNewProjectOn(h *hostConn) { a.openSheet(h, "") }

// localWindows tells whether this machine runs Windows.
var localWindows = runtime.GOOS == "windows"
