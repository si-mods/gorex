package main

import "slices"

// What each window shows is the client's own (docs/MOD-002-projects.md, "The
// desktop window"): this machine's layout keeps the windows of its own
// projects (Store.Windows), and the settings what every window shows, a
// host's project too, by the window's place (settings.Shown). A window
// whose project is a host's shows one of this machine's as it opens, and
// the host's once the host's layout came (takeWanted).

// noteShown keeps in the settings that the window shows w.
func (a *App) noteShown(w *Workspace) {
	if a.st == nil || w == nil {
		return
	}
	key := openKey(w.host, w.ID)
	for len(prefs.Shown) <= a.slot {
		prefs.Shown = append(prefs.Shown, "")
	}
	if prefs.Shown[a.slot] == key {
		return
	}
	prefs.Shown[a.slot] = key
	saveSettings()
}

// wantShown has the window show, once its host's layout came, the host's
// project the settings say it showed last.
func (a *App) wantShown() {
	if a.slot >= len(prefs.Shown) {
		return
	}
	key := prefs.Shown[a.slot]
	if key == "" || a.ws != nil && openKey(a.ws.host, a.ws.ID) == key {
		return
	}
	a.want = key
	if a.st != nil {
		a.st.takeWanted()
	}
}

// takeWanted shows in each window that wants one the project it wants,
// once its host's layout has it and no other window shows it.
func (st *store) takeWanted() {
	for _, a := range slices.Clone(st.apps) {
		if a.want == "" {
			continue
		}
		for _, w := range st.workspaces {
			if openKey(w.host, w.ID) == a.want && !w.offline() && w.app == nil {
				a.want = ""
				a.showWorkspace(w)
				break
			}
		}
	}
}
