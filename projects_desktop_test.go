package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Tests of the desktop of docs/projects.md, phase 2: the window shows the
// hosts' layouts, the sidebar their projects, and the adoptions go to
// their hosts.

// projectsApp is a test app with its tabs in a sidebar, and a host server
// of its own, "office-h800", not connected yet.
func projectsApp(t *testing.T) (*App, *ui.Tester, string, *rex.Client) {
	t.Helper()
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off"}
	fakeHosts(t)
	sock, remote := startHostServer(t)
	t.Cleanup(a.stopHosts)
	return a, tt, sock, remote
}

// hostStore is the layout host server c keeps.
func hostStore(t *testing.T, c *rex.Client) layout.Store {
	t.Helper()
	raw, err := c.Layout()
	if err != nil {
		t.Fatal(err)
	}
	return layout.Parse(raw)
}

// hostProject is the workspace of ID id of the host's layout, nil for
// none.
func hostProject(t *testing.T, c *rex.Client, id string) *layout.Workspace {
	t.Helper()
	st := hostStore(t, c)
	return st.Space(id)
}

// wsSIDs are the sessions of a workspace's tabs, and whether one names
// a host.
func wsSIDs(w *layout.Workspace) (sids []string, hosted bool) {
	for _, tab := range w.Tabs {
		for _, r := range tab.Roots() {
			for _, n := range layout.Leaves(r) {
				if n.SID != "" {
					sids = append(sids, n.SID)
				}
				hosted = hosted || n.Host != ""
			}
		}
	}
	return sids, hosted
}

// noHostLeaves fails when this machine's layout keeps a pane of another
// host (docs/projects.md: it keeps only its own projects).
func noHostLeaves(t *testing.T, a *App) {
	t.Helper()
	for _, w := range savedWorkspaces(t, a.client).Sessions {
		if _, hosted := wsSIDs(&w); hosted {
			t.Fatalf("this machine's layout keeps another host's pane in %q", w.Name)
		}
	}
}

// TestHostProjectsInWindow: a host's project shows under the host in the
// sidebar; the window shows it, its tabs those of the host's layout; New
// Tab, a split and a tab's name go to the host's layout, with no pane of
// the host in this machine's.
func TestHostProjectsInWindow(t *testing.T) {
	a, tt, sock, remote := projectsApp(t)
	dir := testDir(t)
	res, err := remote.Command(rex.LayoutCommand{Op: "project.create", Dir: dir, Name: "acme-api"})
	if err != nil || res.Code != "" {
		t.Fatalf("project.create on the host: %+v %v", res, err)
	}
	h := a.addHost(savedHost{ID: "h1", Name: "office-h800", Address: sock})
	waitFor(t, tt, "the host's project", func() bool { w := a.st.byID(res.Space); return w != nil && w.host == h && !w.offline() })
	w := a.st.byID(res.Space)
	tt.Frame()
	for _, s := range []string{"OFFICE-H800", "acme-api"} {
		if !tt.HasText(s) {
			t.Fatalf("the sidebar has no %q: %q", s, tt.Texts())
		}
	}
	click(t, tt, "acme-api")
	waitFor(t, tt, "the window to show the host's project", func() bool { return a.ws == w && len(a.tabs) == 1 && a.tabs[0].Focus != nil })
	first := a.tabs[0].Focus
	if first.host != h || first.SID != hostProject(t, remote, w.ID).Tabs[0].Root.SID {
		t.Fatalf("the project's tab is not the host's: %+v", first)
	}

	// New Tab: in the project, on the host, in its folder.
	a.newProjectTab()
	waitFor(t, tt, "the new tab", func() bool { return len(a.tabs) == 2 && a.active == 1 && a.tab().Focus != nil })
	if p := a.tab().Focus; p.host != h {
		t.Fatalf("New Tab of the host's project opened on %v", p.host)
	}
	a.split(false)
	waitFor(t, tt, "the split", func() bool { return len(a.tab().panes()) == 2 })
	a.setTabName(a.tab(), "logs")
	a.save()
	waitFor(t, tt, "the host's layout", func() bool {
		hw := hostProject(t, remote, w.ID)
		if hw == nil || len(hw.Tabs) != 2 || hw.Tabs[1].Name != "logs" {
			return false
		}
		sids, hosted := wsSIDs(hw)
		return len(sids) == 3 && !hosted
	})
	noHostLeaves(t, a)
	// The sessions are the host's alone.
	for _, p := range a.tab().panes() {
		if !slices.Contains(sessionIDs(t, remote), p.SID) || slices.Contains(sessionIDs(t, a.client), p.SID) {
			t.Errorf("session %s is not the host's alone", p.SID)
		}
	}
}

// TestProjectsSidebarSpaces: the sidebar lists the projects of the space
// shown, in order of last use, and every space the tabs with no project;
// the picker shows another space; New Space adds one, empty; Move to
// Space moves a project, on its host.
func TestProjectsSidebarSpaces(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	work := "Work"
	mk := func(name, space string) *Workspace {
		t.Helper()
		res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: filepath.Join(appTempDir(t, a)), Name: name, SpaceName: &space})
		if res.Code != "" {
			t.Fatalf("project.create %s: %+v", name, res)
		}
		caughtUp(t, a, tt)
		return a.st.byID(res.Space)
	}
	api := mk("acme-api", work)
	site := mk("acme-site", work)
	notes := mk("notes", "")
	if api == nil || site == nil || notes == nil {
		t.Fatal("the projects did not come")
	}
	prefs.Space = work
	tt.Frame()
	if !tt.HasText("acme-api") || !tt.HasText("acme-site") || tt.HasText("notes") {
		t.Fatalf("the space Work: %q", tt.Texts())
	}
	if !tt.HasText(locales.ProjectsNoProject()) {
		t.Errorf("no tabs with no project in Work: %q", tt.Texts())
	}
	// The picker shows Default.
	click(t, tt, locales.SpacePickerLabel(work))
	click(t, tt, locales.SpaceDefault())
	waitFor(t, tt, "Default", func() bool { return shownSpace() == "" && tt.HasText("notes") && !tt.HasText("acme-api") })

	// New Space: an empty one, shown, kept while empty.
	a.newSpace("Side")
	tt.Frame()
	if shownSpace() != "Side" || !slices.Contains(a.st.spaceNames(), "Side") {
		t.Fatalf("the new space: shown %q, names %q", shownSpace(), a.st.spaceNames())
	}
	// Move to Space, on the host.
	a.moveProjectToSpace(notes, "Side")
	caughtUp(t, a, tt)
	waitFor(t, tt, "notes in Side", func() bool { return notes.Space == "Side" && tt.HasText("notes") })
	if sw := spaceOfStore(savedWorkspaces(t, a.client), notes.ID); sw == nil || sw.Space != "Side" {
		t.Errorf("the server keeps %+v", sw)
	}
	// Renamed: on the host's projects, and the space shown follows.
	a.renameSpace(work, "Office")
	caughtUp(t, a, tt)
	if sw := spaceOfStore(savedWorkspaces(t, a.client), api.ID); sw == nil || sw.Space != "Office" {
		t.Errorf("the rename of Work: %+v", sw)
	}
}

// TestProjectRowNewTabAndMove: a project row's + opens a tab in its
// folder; a tab moves to another project of its host with its pane and
// its process (Move to Project, as a drop on the row does).
func TestProjectRowNewTabAndMove(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	dirA, dirB := appTempDir(t, a), appTempDir(t, a)
	space := ""
	resA := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: dirA, Name: "acme-api", SpaceName: &space})
	resB := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: dirB, Name: "acme-web", SpaceName: &space})
	caughtUp(t, a, tt)
	wa, wb := a.st.byID(resA.Space), a.st.byID(resB.Space)
	if wa == nil || wb == nil {
		t.Fatal("the projects did not come")
	}
	a.newTabIn(wa)
	waitFor(t, tt, "the tab of the row's +", func() bool { return a.ws == wa && len(a.tabs) == 2 })
	tab := a.tab()
	if d := localDirOf(t, a, tab.Focus.SID); !sameDir(d, dirA) {
		t.Errorf("the tab of the row's + starts in %q, want %q", d, dirA)
	}
	sid := tab.Focus.SID
	if !a.canMoveTab(tab, wb) {
		t.Fatal("a tab cannot move to a project of its host")
	}
	a.moveTabToSpace(tab, wb)
	caughtUp(t, a, tt)
	sw := spaceOfStore(savedWorkspaces(t, a.client), wb.ID)
	if sw == nil || !slices.ContainsFunc(sw.Tabs, func(x layout.Tab) bool { return x.Root != nil && x.Root.SID == sid }) {
		t.Fatalf("the tab is not in acme-web: %+v", sw)
	}
	if !slices.Contains(sessionIDs(t, a.client), sid) {
		t.Error("the move ended the tab's session")
	}
}

// TestOfflineHostGreyed: a host that goes away shows its projects as the
// app last took them, greyed: they open nothing, and the copy is kept
// for the next launch.
func TestOfflineHostGreyed(t *testing.T) {
	a, tt, sock, remote := projectsApp(t)
	res, _ := remote.Command(rex.LayoutCommand{Op: "project.create", Dir: testDir(t), Name: "trainer"})
	proxy := newHostProxy(t, sock)
	h := a.addHost(savedHost{ID: "h1", Name: "office-h800", Address: proxy.path})
	waitFor(t, tt, "the host's project", func() bool { w := a.st.byID(res.Space); return w != nil && !w.offline() })
	w := a.st.byID(res.Space)
	if c := readHostCache()["h1"]; spaceOfStore(c, w.ID) == nil {
		t.Fatalf("no copy kept of the host: %+v", c)
	}
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil && w.offline() })
	tt.Frame()
	if !tt.HasText("trainer") {
		t.Fatalf("the project of the host away: %q", tt.Texts())
	}
	before := a.ws
	click(t, tt, "trainer")
	if a.ws != before {
		t.Error("a project of a host away opened")
	}
	if a.newTabIn(w); a.ws != before {
		t.Error("New Tab of a host away opened")
	}
	// The next launch shows the copy, greyed, before the host connects.
	st := &store{hosts: []*hostConn{newHostConn(savedHost{ID: "h1", Name: "office-h800"})}}
	st.loadHostCache()
	if c := st.byID(w.ID); c == nil || !c.offline() || c.Name != "trainer" {
		t.Errorf("the copy at launch: %+v", c)
	}
}

// TestAdoptionsOnConnect: a layout migrated from v0.39 keeps the panes of
// a host as an adoption; until the host connects the sidebar says it
// waits for it, and as it connects the app sends it the adoption: the
// host makes the project, its tab with the panes' split and the same
// sessions, and this machine's server forgets it. This machine's layout
// grows no tab of the host's panes.
func TestAdoptionsOnConnect(t *testing.T) {
	a, tt, sock, remote := projectsApp(t)
	dir := testDir(t)
	s1, err1 := remote.Create(rex.CreateOptions{Dir: dir, Cols: 80, Rows: 24})
	s2, err2 := remote.Create(rex.CreateOptions{Dir: dir, Cols: 80, Rows: 24})
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	// The adoption the migration keeps, as layout.Migrate makes it.
	st := savedWorkspaces(t, a.client)
	st.Adopt = []layout.Adoption{{ID: "ad1", Host: "h1", Project: &layout.AdoptProject{Name: "trainer", Dir: dir},
		Tabs: []layout.AdoptTab{{Title: "mon", Split: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: s1.ID}, B: &layout.Node{SID: s2.ID}}}}}}
	raw, _ := json.Marshal(st)
	if err := a.client.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	proxy := newHostProxy(t, sock)
	proxy.setDown(true)
	h := a.addHost(savedHost{ID: "h1", Name: "office-h800", Address: proxy.path})
	a.st.out.follow(a.client)
	caughtUp(t, a, tt)
	waitFor(t, tt, "the waiting note", func() bool { return tt.HasText(locales.ProjectsWaitingFor("office-h800")) })

	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the adoption on the host", func() bool {
		for _, w := range hostStore(t, remote).Sessions {
			if w.Name == "trainer" && len(w.Tabs) == 1 && w.Tabs[0].Name == "mon" {
				sids, _ := wsSIDs(&w)
				return slices.Equal(sids, []string{s1.ID, s2.ID})
			}
		}
		return false
	})
	waitFor(t, tt, "the adoption to be forgotten", func() bool { return len(savedWorkspaces(t, a.client).Adopt) == 0 })
	waitFor(t, tt, "the project under the host", func() bool { return tt.HasText("trainer") && !tt.HasText(locales.ProjectsWaitingFor("office-h800")) })
	noHostLeaves(t, a)
	if n := len(sessionIDs(t, remote)); n != 2 {
		t.Errorf("the host runs %d sessions, not the 2 adopted", n)
	}
}

// spaceOfStore is the workspace of ID id of s, nil for none.
func spaceOfStore(s layout.Store, id string) *layout.Workspace { return s.Space(id) }

// TestProjectsOrder: a host's projects of the space go by last use, the
// latest first, and those not used for 30 days under Older; the window's
// own stays in sight.
func TestProjectsOrder(t *testing.T) {
	now := time.Now()
	mk := func(name string, used time.Time) *Workspace {
		return &Workspace{ID: name, Name: name, Dir: "/srv/" + name, Space: "Work", Used: used}
	}
	old, mid, last, other := mk("old", now.Add(-40*24*time.Hour)), mk("mid", now.Add(-time.Hour)), mk("last", now), mk("other", now)
	other.Space = "Life"
	a := &App{st: &store{workspaces: []*Workspace{old, mid, last, other}}}
	recent, older := a.projectsOf(nil, "Work")
	if len(recent) != 2 || recent[0] != last || recent[1] != mid || len(older) != 1 || older[0] != old {
		t.Fatalf("recent %v, older %v", names(recent), names(older))
	}
	a.ws = old
	if recent, older = a.projectsOf(nil, "Work"); len(older) != 0 || len(recent) != 3 {
		t.Errorf("the window's own project went under Older: %v, %v", names(recent), names(older))
	}
}

func names(ws []*Workspace) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Name)
	}
	return out
}

// newTestSpace makes a workspace of this machine of a new name and color,
// of no folder, last among this machine's, as a space was before projects
// (docs/projects.md): the tests of what a window does with several
// workspaces make them so. The app makes none: its projects come from
// project.create.
func (st *store) newTestSpace() *Workspace {
	taken := func(name string) bool {
		return slices.ContainsFunc(st.workspaces, func(w *Workspace) bool { return strings.EqualFold(w.Name, name) })
	}
	used := make([]string, len(st.workspaces))
	for i, w := range st.workspaces {
		used[i] = w.Color
	}
	w := &Workspace{ID: layout.NewSpaceID(), Name: layout.SpaceName(taken), Color: layout.NextColor(used)}
	i := slices.IndexFunc(st.workspaces, func(o *Workspace) bool { return o.host != nil })
	if i < 0 {
		i = len(st.workspaces)
	}
	st.workspaces = slices.Insert(st.workspaces, i, w)
	return w
}

// TestProjectPicker: with the tabs on top the title bar says `host ›
// project`, and its menu lists the hosts and their projects of the space
// with their tabs' counts, the tabs with no project, and New Project…; a
// row shows its project.
func TestProjectPicker(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	prefs.TabPlacement = ""
	space := ""
	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-api", SpaceName: &space})
	caughtUp(t, a, tt)
	w := a.st.byID(res.Space)
	if w == nil {
		t.Fatal("no project")
	}
	label := thisMachine() + " › " + locales.ProjectsNoProject()
	click(t, tt, label)
	if !a.pickerOpen {
		t.Fatalf("the picker did not open; texts %q", tt.Texts())
	}
	for _, s := range []string{"acme-api", locales.ProjectsNoProject(), cmdNewProject.Title()} {
		if !tt.HasText(s) {
			t.Errorf("the picker has no %q: %q", s, tt.Texts())
		}
	}
	click(t, tt, "acme-api")
	waitFor(t, tt, "the project", func() bool { return a.ws == w && tt.HasText(thisMachine()+" › acme-api") })
}

// TestProjectMenu: a project's menu has New Tab, Rename…, Color, Move to
// Space, Reveal Folder (of this machine's) and Close Project…; Color
// colors it on its host.
func TestProjectMenu(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	space := ""
	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-api", SpaceName: &space})
	caughtUp(t, a, tt)
	w := a.st.byID(res.Space)
	rightClick(t, tt, "acme-api")
	m := tt.Menu()
	for _, s := range []string{locales.CmdNewTab(), cmdRenameSession.Title(), locales.SpaceColor(), locales.ProjectMoveToSpace(), cmdRevealFolder.Title(), locales.ProjectCloseAsk()} {
		if !slices.Contains(m, s) {
			t.Errorf("the menu has no %q: %q", s, m)
		}
	}
	tt.CloseMenu()
	a.setProjectColor(w, "rose")
	caughtUp(t, a, tt)
	if sw := spaceOfStore(savedWorkspaces(t, a.client), w.ID); sw == nil || sw.Color != "rose" {
		t.Errorf("the color on the host: %+v", sw)
	}
	// A host's tabs with no project have New Tab alone.
	rightClick(t, tt, locales.ProjectsNoProject())
	if m := tt.Menu(); slices.Contains(m, cmdRenameSession.Title()) || !slices.Contains(m, locales.CmdNewTab()) {
		t.Errorf("the menu of the tabs with no project: %q", m)
	}
}

// TestSidebarFlatLayout: a host is a section header, in capitals, with no
// PROJECTS row; its projects are flush with it, their folder only in the
// tooltip; the tabs of the project shown come one step in; a host away
// says "offline".
func TestSidebarFlatLayout(t *testing.T) {
	a, tt, _, _ := projectsApp(t)
	space := ""
	dir := appTempDir(t, a)
	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: dir, Name: "acme-api", SpaceName: &space})
	caughtUp(t, a, tt)
	w := a.st.byID(res.Space)
	a.showWorkspace(w)
	waitFor(t, tt, "the project's tab", func() bool { return len(a.tabs) == 1 && a.tabs[0].Focus != nil })
	off := a.addHost(savedHost{ID: "h2", Name: "old-laptop", Address: filepath.Join(testDir(t), "none.sock")})
	a.st.workspaces = append(a.st.workspaces, &Workspace{ID: "site", Name: "site", Dir: "/srv/site", host: off})
	tt.Frame()
	header, ok := tt.Find(strings.ToUpper(thisMachine()))
	if !ok || tt.HasText("PROJECTS") {
		t.Fatalf("the header of this machine: %v; texts %q", ok, tt.Texts())
	}
	row, _ := tt.Find("acme-api" + spacedComma(a.projectDetail(w)))
	name, detail := a.tabs[0].label()
	tab, _ := tt.Find(name + " " + detail)
	if row.X > header.X+1 || tab.X-row.X < dotRoom-1 || tab.X-row.X > dotRoom+1 {
		t.Errorf("header at x %v, project at %v, its tab at %v; want the project flush and its tab %v in", header.X, row.X, tab.X, dotRoom)
	}
	if r, inline := tt.Find(a.projectDetail(w)); inline && r.X < sidebarWidthIn(1000) {
		t.Errorf("the folder %q shows in the row", a.projectDetail(w))
	}
	// One row is filled: the active tab, not its project, which is
	// filled once its tabs are closed.
	face := func(r ui.Rect) [4]uint8 {
		img := tt.Image()
		c := img.RGBAAt(int(r.X+r.W*0.7), int(r.Y+r.H/2))
		return [4]uint8{c.R, c.G, c.B, c.A}
	}
	// The window's gradient moves a little row to row: a fill is more.
	same := func(x, y [4]uint8) bool {
		d := 0
		for i := range 3 {
			d += max(int(x[i])-int(y[i]), int(y[i])-int(x[i]))
		}
		return d <= 6
	}
	empty := ui.Rect{X: row.X, Y: row.Y - hostHeaderH, W: row.W, H: row.H}
	if !same(face(row), face(empty)) || same(face(tab), face(empty)) {
		t.Errorf("the project's row is filled (%v) or its active tab is not (%v), the sidebar being %v", face(row), face(tab), face(empty))
	}
	setRowOpen(openKey(nil, w.ID), false)
	tt.Frame()
	if same(face(row), face(empty)) {
		t.Error("the project of the closed tabs is not filled")
	}
	if !tt.HasText("OLD-LAPTOP") || !tt.HasText(locales.ProjectsOfflineLabel()) {
		t.Errorf("the header of a host away: %q", tt.Texts())
	}
}

// TestMoveTabStaysOnItsHost: a tab moves only between workspaces of its
// host. Its menu's Move to Project lists none of another host's, and a
// move there, either way, does nothing: the tab's session runs on, where
// it was.
func TestMoveTabStaysOnItsHost(t *testing.T) {
	a, tt, sock, remote := projectsApp(t)
	space := ""
	res, err := remote.Command(rex.LayoutCommand{Op: "project.create", Dir: testDir(t), Name: "trainer", SpaceName: &space})
	if err != nil || res.Code != "" {
		t.Fatalf("project.create on the host: %+v %v", res, err)
	}
	local := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-api", SpaceName: &space})
	caughtUp(t, a, tt)
	h := a.addHost(savedHost{ID: "h1", Name: "office-h800", Address: sock})
	waitFor(t, tt, "the host's project", func() bool { w := a.st.byID(res.Space); return w != nil && !w.offline() })
	there, here := a.st.byID(res.Space), a.st.byID(local.Space)

	// This machine's tab, to the host's project.
	tab := a.tab()
	sid := tab.Focus.SID
	if a.canMoveTab(tab, there) {
		t.Fatal("a tab of this machine may move to a host's project")
	}
	name, detail := tab.label()
	rightClick(t, tt, name+" "+detail)
	if err := tt.ChooseMenuItem(locales.TabMoveSpace(), "trainer"); err == nil {
		t.Error("Move to Project lists the host's project")
	}
	tt.CloseMenu()
	a.moveTabToSpace(tab, there)
	caughtUp(t, a, tt)
	if !slices.Contains(a.tabs, tab) || !slices.Contains(sessionIDs(t, a.client), sid) {
		t.Fatalf("the move to another host's project took the tab or ended its session")
	}

	// The host's tab, to this machine's project.
	a.showWorkspace(there)
	waitFor(t, tt, "the host's tab", func() bool { return a.ws == there && len(a.tabs) == 1 && a.tab().Focus != nil })
	rt := a.tab()
	rsid := rt.Focus.SID
	if a.canMoveTab(rt, here) {
		t.Fatal("a tab of a host may move to this machine's project")
	}
	a.moveTabToSpace(rt, here)
	a.save()
	tt.Frame()
	if !slices.Contains(a.tabs, rt) || !slices.Contains(sessionIDs(t, remote), rsid) {
		t.Fatalf("the move to this machine's project took the host's tab or ended its session")
	}
	_ = h
}

// TestOldActionIDs: an action ID of before keeps working: session.new is
// New Tab with No Project, and a removed action does nothing, without an
// error.
func TestOldActionIDs(t *testing.T) {
	if c := lookupCommand("session.new"); c == nil || c.ID != "tab.new_no_project" {
		t.Errorf("session.new is %+v", c)
	}
	for _, id := range []string{"sidebar.view.toggle", "tab.move_window", "project.change_folder", "project.remove_folder"} {
		c := lookupCommand(id)
		if c == nil {
			t.Fatalf("%s is no action", id)
		}
		if err := c.invoke(&App{}, nil); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	if lookupCommand("no.such.action") != nil {
		t.Error("an ID never an action's is one")
	}
	if blockedFromClient("tab.move_window") {
		t.Error("tab.move_window is still blocked")
	}
}

// TestRefusedAdoptionWaits: an adoption its host refused goes again only
// once the host connects anew, not as its layout changes.
func TestRefusedAdoptionWaits(t *testing.T) {
	h := newHostConn(savedHost{ID: "h1", Name: "office-h800"})
	st := &store{hosts: []*hostConn{h}, adopting: map[string]bool{"ad1": true}, adoptRefused: map[string]*hostConn{"ad1": h}}
	st.base.Adopt = []layout.Adoption{{ID: "ad1", Host: "h1"}}
	st.sendAdoptions()
	if !st.adopting["ad1"] {
		t.Fatal("a refused adoption is free to go again on the same connection")
	}
	st.retryAdoptions(h)
	if st.adopting["ad1"] || len(st.adoptRefused) != 0 {
		t.Errorf("a new connection does not let it go again: %v %v", st.adopting, st.adoptRefused)
	}
}

// TestProjectsOpenIndependently: each project opens and closes on its own,
// choosing one never closes another, and the states outlive a restart.
func TestProjectsOpenIndependently(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left", Animation: "off"}
	space := ""
	ida := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "alpha", SpaceName: &space}).Space
	idb := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "bravo", SpaceName: &space}).Space
	caughtUp(t, a, tt)
	wa, wb := a.st.byID(ida), a.st.byID(idb)
	ka, kb := openKey(wa.host, ida), openKey(wb.host, idb)
	ws := map[string]*Workspace{ka: wa, kb: wb}
	open := func(k string) bool { return rowOpen(k, ws[k] == a.ws) }

	a.showWorkspace(wa)
	a.showWorkspace(wb)
	tt.Frame()
	if !open(ka) || !open(kb) {
		t.Fatalf("alpha open %v, bravo open %v after choosing both; want both", open(ka), open(kb))
	}
	setRowOpen(ka, false)
	if open(ka) || !open(kb) {
		t.Fatalf("after closing alpha: alpha %v, bravo %v", open(ka), open(kb))
	}
	// A restart reads the file back.
	b, err := os.ReadFile(settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	prefs = settings{}
	if err := json.Unmarshal(b, &prefs); err != nil {
		t.Fatal(err)
	}
	if open(ka) || !open(kb) {
		t.Fatalf("after a restart: alpha %v, bravo %v", open(ka), open(kb))
	}
	// Choosing alpha, as the picker does, leaves bravo as it was.
	a.showWorkspace(wa)
	tt.Frame()
	if !open(kb) {
		t.Error("choosing alpha closed bravo")
	}
}

// TestDeleteSpace: Edit Spaces… offers Delete Space… for every space but
// Default; it asks first, then moves the space's projects to Default on
// each host, closing no tab, drops the name from the picker, and shows
// Default. A host offline gets the delete as it connects.
func TestDeleteSpace(t *testing.T) {
	a, tt, sock, remote := projectsApp(t)
	work := "Work"
	far, _ := remote.Command(rex.LayoutCommand{Op: "project.create", Dir: testDir(t), Name: "trainer", SpaceName: &work})
	proxy := newHostProxy(t, sock)
	h := a.addHost(savedHost{ID: "h1", Name: "office-h800", Address: proxy.path})
	waitFor(t, tt, "the host's project", func() bool { w := a.st.byID(far.Space); return w != nil && !w.offline() })
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil && a.st.byID(far.Space).offline() })

	res := clientCommand(a, rex.LayoutCommand{Op: "project.create", Dir: appTempDir(t, a), Name: "acme-api", SpaceName: &work})
	caughtUp(t, a, tt)
	api := a.st.byID(res.Space)
	if api == nil {
		t.Fatal("the project did not come")
	}
	setSpaceColorOf(work, "blue")
	a.showSpaceNamed(work)
	tabs := len(spaceOfStore(savedWorkspaces(t, a.client), api.ID).Tabs)
	before := sessionIDs(t, a.client)

	click(t, tt, locales.SpacePickerLabel(work))
	click(t, tt, locales.SpaceEdit())
	if n := strings.Count(strings.Join(tt.Texts(), "\n"), locales.SpaceDelete()); n != len(a.st.spaceNames())-1 {
		t.Fatalf("%d Delete Space… for spaces %q; texts %q", n, a.st.spaceNames(), tt.Texts())
	}
	click(t, tt, locales.SpaceDelete())
	if !tt.HasText(locales.SpaceDeleteTitle(work)) || !tt.HasText(locales.SpaceDeleteBody(1)) || !tt.HasText(locales.SpaceDeleteOffline()) {
		t.Fatalf("the question: %q", tt.Texts())
	}
	if api.Space != work {
		t.Fatal("the space went before the answer")
	}
	click(t, tt, locales.SpaceDeleteConfirm())
	caughtUp(t, a, tt)
	waitFor(t, tt, "the delete", func() bool {
		sw := spaceOfStore(savedWorkspaces(t, a.client), api.ID)
		return sw != nil && sw.Space == "" && api.Space == ""
	})
	if shownSpace() != "" || slices.Contains(a.st.spaceNames(), work) || clientSpaceOf(work) != nil {
		t.Errorf("after the delete: shown %q, names %q", shownSpace(), a.st.spaceNames())
	}
	if len(spaceOfStore(savedWorkspaces(t, a.client), api.ID).Tabs) != tabs || !slices.Equal(sessionIDs(t, a.client), before) {
		t.Error("the delete closed a tab or ended a session")
	}
	// The host away: its copy shows Default, and the delete waits for it.
	if w := a.st.byID(far.Space); w.Space != "" {
		t.Errorf("the copy of the host away: %+v", w)
	}
	if q := prefs.SpaceRenames["h1"]; len(q) != 1 || q[0] != (spaceRename{From: work}) {
		t.Fatalf("the delete queued: %+v", prefs.SpaceRenames)
	}
	if hw := hostProject(t, remote, far.Space); hw.Space != work {
		t.Fatalf("the host away moved already: %+v", hw)
	}
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the delete on the host", func() bool {
		hw := hostProject(t, remote, far.Space)
		return hw != nil && hw.Space == "" && len(prefs.SpaceRenames["h1"]) == 0
	})
	// A server of v0.41.0, of no spaces.delete, gets a rename to Default.
	if c := (&store{}).spaceDeleteCommand(nil, work); c.Op != "spaces.rename" || c.From != work || c.To != "" {
		t.Errorf("the command for an old server: %+v", c)
	}
}
