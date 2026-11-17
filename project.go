package main

import (
	"log"
	"os"
	"runtime"
	"slices"
	"strings"

	"gorex/internal/layout"
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
	// action names the button, run is what it does.
	action func(locales.Messages) string
	run    func()
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
// its folder dir ("" for the project's) gone, with a button that changes
// the folder.
func (a *App) folderMissing(dir string) {
	w := a.ws
	if w == nil {
		return
	}
	if dir == "" {
		dir = w.Dir
	}
	name, id, shown := w.Name, w.ID, a.tildeOn(a.projectHost(w), dir)
	a.setNote(func(m locales.Messages) string { return m.ProjectFolderMissing(shown, name) }, bannerNote{
		action: locales.Messages.ProjectChangeFolder,
		run:    func() { a.openChangeFolder(id) },
	})
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

// projectDetail is what the sidebar's project header says under the name:
// the folder, after the home host's name when that is not this machine,
// or that the host is unknown when it is none of the user's; "" for a
// project of no folder.
func (a *App) projectDetail(w *Workspace) string {
	if w == nil || w.Dir == "" {
		return ""
	}
	if w.Host == "" {
		return a.tildeOn(nil, w.Dir)
	}
	h := a.projectHost(w)
	if h == nil {
		return locales.ProjectHostUnknown() + " · " + w.Dir
	}
	return h.label() + " · " + a.tildeOn(h, w.Dir)
}

// homeArg is how a space.home command names host h, nil for this
// machine: by its machine once known, which outlives the host's removal.
func homeArg(h *hostConn) string {
	switch {
	case h == nil:
		return ""
	case h.Machine != "":
		return layout.MachineHost(h.Machine)
	}
	return h.ID
}

// setProjectHome gives project w the folder dir on host h, nil for this
// machine; dir "" takes its folder away. The server keeps it; done runs
// once it did, with whether it did.
func (a *App) setProjectHome(w *Workspace, h *hostConn, dir string, done func(bool)) {
	host := homeArg(h)
	if dir == "" {
		host = ""
	}
	a.ask(rex.LayoutCommand{Op: "space.home", Space: w.ID, Host: host, Dir: dir}, func(res rex.LayoutCommand) {
		ok := res.Code == ""
		if ok {
			w.Host, w.Dir = host, dir
		} else {
			log.Printf("project %s: no home %q %q: %s", w.ID, host, dir, res.Error)
		}
		if done != nil {
			done(ok)
		}
	})
}

// createProject makes a project named name with the folder dir on host h,
// shows it in the window, and opens its first tab in the folder.
func (a *App) createProject(h *hostConn, dir, name string) {
	st := a.st
	if st == nil || dir == "" {
		return
	}
	w := st.addWorkspace()
	if name = strings.TrimSpace(name); name != "" {
		w.Name = name
	}
	// The home is the window's as it shows the project, so that its first
	// tab opens there (show); the server has it before the tab, as the
	// window's commands go in order.
	w.Host, w.Dir, w.fresh = homeArg(h), dir, true
	a.setProjectHome(w, h, dir, nil)
	a.showWorkspace(w)
}

// removeProjectFolder takes the folder away from the window's project:
// its tabs stay, and its new ones start as a project of no folder's.
func (a *App) removeProjectFolder() {
	if w := a.ws; w != nil && w.Dir != "" {
		a.setProjectHome(w, nil, "", nil)
	}
}

// openChangeFolder opens the sheet that changes the folder of the project
// of ID id, in its folder on its home host.
func (a *App) openChangeFolder(id string) {
	if a.st == nil {
		return
	}
	i := slices.IndexFunc(a.st.workspaces, func(w *Workspace) bool { return w.ID == id })
	if i < 0 {
		return
	}
	w := a.st.workspaces[i]
	h := a.projectHost(w)
	if w.Host != "" && h == nil {
		// Its host is none of the user's: this machine's home to start.
		a.openSheet(id, nil, "")
		return
	}
	a.openSheet(id, h, w.Dir)
}

// openNewProject opens the New Project sheet, on this machine's home.
func (a *App) openNewProject() { a.openSheet("", nil, "") }

// localWindows tells whether this machine runs Windows.
var localWindows = runtime.GOOS == "windows"
