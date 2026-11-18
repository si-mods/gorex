package main

import (
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Tests that a tab no window shows names itself as a shown one does: by
// the title its program set and its program's icon, from what its
// machine's session list says, with no pane attached for it.

const (
	claudeTitle = "✳ Fix the login bug"
	ompTitle    = "π > Tidy the release notes"
)

// titledInfo is a session that runs prog, which set the title title.
func titledInfo(sid, prog, title, dir string) rex.SessionInfo {
	return rex.SessionInfo{ID: sid, Shell: "zsh", Program: prog, Title: title, Dir: dir}
}

// TestLocalTabsKeepTheirTitles: a project of this machine with two tabs,
// whose sessions run Claude Code and omp, each with its own title. The
// tab the window does not show names itself by its title, and once
// another project is shown, both tabs of the project do in the sidebar,
// each with its program.
func TestLocalTabsKeepTheirTitles(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off"}
	space := ""
	dir := appTempDir(t, a)
	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: dir, Name: "acme-api", SpaceName: &space})
	caughtUp(t, a, tt)
	w := a.st.byID(res.Space)
	a.showWorkspace(w)
	waitFor(t, tt, "the project's tab", func() bool { return a.ws == w && len(a.tabs) == 1 && a.tabs[0].Focus != nil })
	a.newTab(dir)
	waitFor(t, tt, "the second tab", func() bool { return len(a.tabs) == 2 && a.active == 1 && a.tab().Focus != nil })
	p1, p2 := a.tabs[0].Focus, a.tabs[1].Focus
	a.st.infos = map[string]rex.SessionInfo{
		p1.SID: titledInfo(p1.SID, "claude", claudeTitle, dir),
		p2.SID: titledInfo(p2.SID, "omp", ompTitle, dir),
	}
	a.apply(nil, a.st.infos)
	tt.Frame()

	// The tab not shown: its terminal has read no title. (The shown one's
	// terminal reads the test shell's own.)
	if name, _ := a.tabs[0].label(); name != claudeTitle {
		t.Errorf("the tab not shown is %q, want %q", name, claudeTitle)
	}
	if !tt.HasText(claudeTitle) {
		t.Errorf("the sidebar has no %q: %q", claudeTitle, tt.Texts())
	}

	// Another project shown: the sidebar lists this one's tabs from its
	// layout.
	a.showWorkspace(a.st.noProjectOf(nil))
	waitFor(t, tt, "the other workspace", func() bool { return a.ws != w && w.app == nil })
	setRowOpen(openKey(nil, w.ID), true)
	tt.Frame()
	want := map[string]string{claudeTitle: "Claude Code", ompTitle: "omp"}
	for i, tab := range w.layout.Tabs {
		name, _, prog := savedTabFace(tab, a.st.infos)
		if prog.Name != want[name] {
			t.Errorf("tab %d of the project not shown is %q with %q's icon", i, name, prog.Name)
		}
	}
	for _, s := range []string{claudeTitle, ompTitle} {
		if !tt.HasText(s) {
			t.Errorf("the sidebar has no %q for the project not shown: %q", s, tt.Texts())
		}
	}
}

// TestHostShellTabDropsAnEndedProgramsTitle: a host's project whose tab's
// session is a shell at its prompt, after Claude Code ended there. The
// server still keeps the title Claude Code set, and the status it
// reported last (done, which stays until seen). No window shows the
// project: the sidebar names the tab by its shell and folder, as a shown
// pane does, not by the ended program's title.
func TestHostShellTabDropsAnEndedProgramsTitle(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off"}
	const stale = "Projects V2"
	done := &rex.ProgramStatus{State: "done", App: "claude", Progress: -1}
	h := newHostConn(savedHost{ID: "h9", Name: "lab-box"})
	h.state = hostConnected
	h.infos = map[string]rex.SessionInfo{
		// The status went with the program.
		"r1": {ID: "r1", Shell: "zsh", Program: "zsh", Idle: true, Title: stale, Dir: "/srv/app"},
		// The done status stays till seen.
		"r2": {ID: "r2", Shell: "zsh", Program: "zsh", Idle: true, Title: stale, Dir: "/srv/app",
			Status: done, Statuses: []rex.ProgramStatus{*done}},
	}
	a.hosts = append(a.hosts, h)
	tabs := []savedTab{
		{ID: "t1", Root: &layout.Node{SID: "r1", Host: h.ID, Dir: "/srv/app"}},
		{ID: "t2", Root: &layout.Node{SID: "r2", Host: h.ID, Dir: "/srv/app"}},
	}
	w := &Workspace{ID: "hp", Name: "site", Dir: "/srv/app", Space: shownSpace(), host: h}
	w.layout.Tabs = tabs
	a.st.workspaces = append(a.st.workspaces, w)
	setRowOpen(openKey(h, w.ID), true)
	tt.Frame()
	for i, tab := range tabs {
		if name, _, _ := savedTabFace(tab, h.infos); name != "zsh" {
			t.Errorf("tab %d, a shell at its prompt, is %q, want %q", i, name, "zsh")
		}
		// A shown pane of it, its terminal having read the same title.
		p := &Pane{SID: tab.Root.SID, host: h, info: h.infos[tab.Root.SID], title: stale}
		if name, _ := p.label(); name != "zsh" {
			t.Errorf("pane %d, a shell at its prompt, is %q, want %q", i, name, "zsh")
		}
	}
	if tt.HasText(stale) {
		t.Errorf("the sidebar shows the ended program's title %q: %q", stale, tt.Texts())
	}
}

// TestHostTabsKeepTheirTitles: a host's project with two tabs, whose
// sessions run Claude Code and omp with titles of their own. No window
// shows it: the sidebar names each tab by its title, with its program's
// icon, from the host's session list. A host's pane whose terminal read no
// title names itself the same. The host's tabs with no project, none,
// show no row.
func TestHostTabsKeepTheirTitles(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off"}
	h := newHostConn(savedHost{ID: "h9", Name: "lab-box"})
	h.state = hostConnected
	h.infos = map[string]rex.SessionInfo{
		"r1": titledInfo("r1", "claude", claudeTitle, "/srv/app"),
		"r2": titledInfo("r2", "omp", ompTitle, "/srv/app"),
	}
	a.hosts = append(a.hosts, h)
	tabs := []savedTab{
		{ID: "t1", Root: &layout.Node{SID: "r1", Host: h.ID, Dir: "/srv/app"}},
		{ID: "t2", Root: &layout.Node{SID: "r2", Host: h.ID, Dir: "/srv/app"}},
	}
	w := &Workspace{ID: "hp", Name: "site", Dir: "/srv/app", Space: shownSpace(), host: h}
	w.layout.Tabs = tabs
	loose := &Workspace{ID: "hl", Name: layout.NoProjectName, host: h}
	a.st.workspaces = append(a.st.workspaces, w, loose)
	setRowOpen(openKey(h, w.ID), true)
	tt.Frame()
	for _, s := range []string{claudeTitle, ompTitle} {
		if !tt.HasText(s) {
			t.Errorf("the sidebar has no %q for the host's project: %q", s, tt.Texts())
		}
	}
	for i, want := range []string{"Claude Code", "omp"} {
		if _, _, prog := savedTabFace(tabs[i], h.infos); prog.Name != want {
			t.Errorf("tab %d of the host's project has %q's icon, want %q's", i, prog.Name, want)
		}
	}
	// With nothing listed of a session, the tab says its folder.
	if name, _, _ := savedTabFace(tabs[0], nil); name != "app" {
		t.Errorf("a tab with nothing listed is %q, want its folder", name)
	}
	// A host's pane of a tab not shown, its terminal having read nothing.
	p := &Pane{SID: "r2", host: h, info: h.infos["r2"]}
	if name, _ := (&Tab{Focus: p}).label(); name != ompTitle {
		t.Errorf("a host's pane not shown is %q, want %q", name, ompTitle)
	}
	n := 0
	for _, s := range tt.Texts() {
		if s == locales.ProjectsTabsOf(locales.ProjectsNoProject()) { // one a row
			n++
		}
	}
	if n != 1 { // this machine's, which has the window's tab
		t.Errorf("%d rows of tabs with no project, want this machine's alone: %q", n, tt.Texts())
	}
}
