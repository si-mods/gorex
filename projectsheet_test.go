package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// folderTree makes root/alpha/inner and root/beta, and returns root.
func folderTree(t *testing.T, root string) string {
	t.Helper()
	for _, d := range []string{"alpha/inner", "beta"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// sheetShows waits for the sheet to list dir.
func sheetShows(t *testing.T, a *App, tt *ui.Tester, dir string) {
	t.Helper()
	waitFor(t, tt, "the sheet to list "+dir, func() bool {
		s := a.sheet
		return s.open && !s.loading && s.asked && s.failed == "" && sameDir(s.res.Path, dir)
	})
}

// click clicks what shows label, and draws.
func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	tt.Frame()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts %q", label, err, tt.Texts())
	}
	tt.Frame()
}

// rightClick right-clicks what shows label, and draws.
func rightClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	tt.Frame()
	if err := tt.RightClick(label); err != nil {
		t.Fatalf("right click %q: %v; texts %q", label, err, tt.Texts())
	}
	tt.Frame()
}

// TestNewProjectSheetLocal makes a project with the sheet, from the
// sidebar's +: it browses this machine's folders, a click going into one;
// the name follows the folder until typed; Create shows the project, its
// home kept by the server, with its first tab in the folder.
func TestNewProjectSheetLocal(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	root := folderTree(t, appTempDir(t, a))
	before := a.ws

	click(t, tt, cmdNewProject.Title())
	if !a.sheet.open || a.sheet.space != "" || a.sheet.host != nil {
		t.Fatalf("the sidebar's + opened %+v", a.sheet)
	}
	sheetShows(t, a, tt, a.homeOf(nil))
	if a.sheet.name == "" || a.sheet.name != folderName(a.sheet.res.Path) {
		t.Errorf("the name of the home folder is %q", a.sheet.name)
	}

	// A path typed, then Enter.
	a.sheet.path = ""
	click(t, tt, locales.ProjectSheetPath())
	tt.Type(root)
	tt.Key(0, ui.KeyEnter)
	sheetShows(t, a, tt, root)
	if !tt.HasText("alpha") || !tt.HasText("beta") || tt.HasText("notes.txt") {
		t.Fatalf("the folders of the tree: %q", tt.Texts())
	}
	click(t, tt, "alpha")
	sheetShows(t, a, tt, filepath.Join(root, "alpha"))
	if a.sheet.name != "alpha" || !tt.HasText("inner") {
		t.Fatalf("in alpha: name %q, texts %q", a.sheet.name, tt.Texts())
	}
	// Up goes back; the name follows the folder.
	click(t, tt, locales.ProjectSheetUp())
	sheetShows(t, a, tt, root)
	click(t, tt, "beta")
	sheetShows(t, a, tt, filepath.Join(root, "beta"))
	folder := a.sheet.res.Path
	if a.sheet.name != "beta" {
		t.Errorf("the name follows the folder: %q", a.sheet.name)
	}

	click(t, tt, locales.ProjectSheetCreate())
	waitFor(t, tt, "the new project", func() bool { return a.ws != before && a.ws != nil && len(a.tabs) == 1 && a.tabs[0].Focus != nil })
	if a.sheet.open || a.ws.Name != "beta" || a.ws.Host != "" || a.ws.Dir != folder {
		t.Fatalf("the project %q %q %q; sheet open %v", a.ws.Name, a.ws.Host, a.ws.Dir, a.sheet.open)
	}
	if dir := localDirOf(t, a, a.tabs[0].Focus.SID); !sameDir(dir, folder) {
		t.Errorf("its first tab starts in %q, want %q", dir, folder)
	}
	caughtUp(t, a, tt)
	for _, w := range savedWorkspaces(t, a.client).Sessions {
		if w.ID == a.ws.ID && (w.Dir != folder || w.Host != "") {
			t.Errorf("the server keeps the home %q %q", w.Host, w.Dir)
		}
	}
}

// TestNewProjectSheetHost browses a remote host's folders, on a test
// server: an offline host shows and cannot be chosen; the project made
// opens its first tab on the host in the folder.
func TestNewProjectSheetHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, _, remote := remotePaneOf(t, a, tt, t.TempDir())
	off := a.addHost(savedHost{ID: "h-off", Name: "far-box", Address: filepath.Join(t.TempDir(), "none.sock")})
	root := folderTree(t, t.TempDir())
	focusLocal(t, a)

	a.openNewProject()
	sheetShows(t, a, tt, a.homeOf(nil))
	click(t, tt, "far-box")
	if a.sheet.host != nil || pickable(off) {
		t.Fatalf("an offline host was chosen: %v", a.sheet.host)
	}
	click(t, tt, "office-mac")
	if a.sheet.host != h {
		t.Fatalf("the host chosen is %v", a.sheet.host)
	}
	sheetShows(t, a, tt, a.homeOf(h))
	a.sheet.browse(root)
	sheetShows(t, a, tt, root)
	click(t, tt, "alpha")
	sheetShows(t, a, tt, filepath.Join(root, "alpha"))
	// The breadcrumb goes back to root.
	click(t, tt, a.sheet.res.Parent)
	sheetShows(t, a, tt, root)
	click(t, tt, "beta")
	sheetShows(t, a, tt, filepath.Join(root, "beta"))
	folder := a.sheet.res.Path

	before := a.ws
	click(t, tt, locales.ProjectSheetCreate())
	waitFor(t, tt, "the project's tab on the host", func() bool { return a.ws != before && len(a.tabsOn(h)) == 1 })
	if a.ws.Host != layout.MachineHost(h.Machine) || a.ws.Dir != folder {
		t.Fatalf("the project's home is %q %q", a.ws.Host, a.ws.Dir)
	}
	p := a.tabsOn(h)[0].Focus
	waitFor(t, tt, "the shell in the folder", func() bool {
		for _, in := range sessionInfos(t, remote) {
			if in.ID == p.SID {
				return sameDir(in.Dir, folder)
			}
		}
		return false
	})
}

// TestProjectSettings changes the window's project from its context menu:
// Change Folder… opens the sheet in its folder and gives it another;
// Remove Folder takes it away; the menu offers Remove Folder only to a
// project with a folder.
func TestProjectSettings(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	root := folderTree(t, appTempDir(t, a))
	setProjectHome(t, a, tt, "", filepath.Join(root, "alpha"))
	w := a.ws

	chip := locales.SessionLabel(w.Name) + " · " + a.projectDetail(w)
	tt.Frame()
	rightClick(t, tt, chip)
	if err := tt.ChooseMenuItem(locales.ProjectChangeFolder()); err != nil {
		t.Fatalf("Change Folder…: %v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if !a.sheet.open || a.sheet.space != w.ID || a.sheet.host != nil {
		t.Fatalf("the sheet of the project: %+v", a.sheet)
	}
	sheetShows(t, a, tt, filepath.Join(root, "alpha"))
	if tt.HasText(locales.ProjectSheetName()) || !tt.HasText(locales.ProjectSheetChangeTitle(w.Name)) {
		t.Errorf("the sheet of Change Folder: %q", tt.Texts())
	}
	click(t, tt, locales.ProjectSheetUp())
	sheetShows(t, a, tt, root)
	click(t, tt, "beta")
	sheetShows(t, a, tt, filepath.Join(root, "beta"))
	folder := a.sheet.res.Path
	click(t, tt, locales.ProjectSheetUse())
	waitFor(t, tt, "the new folder", func() bool { return a.ws == w && w.Dir == folder })

	chip = locales.SessionLabel(w.Name) + " · " + a.projectDetail(w)
	rightClick(t, tt, chip)
	if err := tt.ChooseMenuItem(locales.ProjectRemoveFolder()); err != nil {
		t.Fatalf("Remove Folder: %v", err)
	}
	waitFor(t, tt, "no folder", func() bool { return w.Dir == "" && w.Host == "" })
	caughtUp(t, a, tt)
	if w.Dir != "" {
		t.Errorf("the server gave the folder back: %q", w.Dir)
	}
	rightClick(t, tt, locales.SessionLabel(w.Name))
	if m := tt.Menu(); slices.Contains(m, locales.ProjectRemoveFolder()) || !slices.Contains(m, locales.ProjectChangeFolder()) {
		t.Errorf("the menu of a project of no folder: %q", m)
	}
}

// TestProjectHeader checks what the sidebar's project header says under
// the name: the folder of this machine under ~; a host's name before its
// folder; an unknown host as such.
func TestProjectHeader(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	tt.Frame()
	if d := a.projectDetail(a.ws); d != "" {
		t.Errorf("a project of no folder says %q", d)
	}
	home := a.homeOf(nil)
	folder := filepath.Join(home, "code", "windflow")
	setProjectHome(t, a, tt, "", folder)
	want := "~" + string(filepath.Separator) + filepath.Join("code", "windflow")
	if d := a.projectDetail(a.ws); d != want || !tt.HasText(want) {
		t.Errorf("the header says %q, want %q; texts %q", d, want, tt.Texts())
	}

	h, _, _ := remotePane(t, a, tt, t.TempDir())
	setProjectHomeAs(t, a, tt, h.ID, folder, layout.MachineHost(h.Machine))
	want = "office-mac · " + tildePath(folder, a.homeOf(h))
	tt.Frame()
	if d := a.projectDetail(a.ws); d != want || !tt.HasText(want) {
		t.Errorf("the header of a host's project says %q, want %q", d, want)
	}
	if _, ok := tt.Find(locales.SessionLabel(a.ws.Name) + " · " + want); !ok {
		t.Errorf("the chip's label lacks the folder; texts %q", tt.Texts())
	}

	far := "/srv/far"
	setProjectHome(t, a, tt, layout.MachineHost("machine-far"), far)
	want = locales.ProjectHostUnknown() + " · " + far
	tt.Frame()
	if d := a.projectDetail(a.ws); d != want || !tt.HasText(want) {
		t.Errorf("the header of an unknown host says %q, want %q", d, want)
	}
}

// TestProjectNotes checks the notes of New Tab in a project: a folder
// gone says so with Change Folder…, which opens the sheet of the project;
// a home host unknown says so with no button; a wait is not an error,
// and goes once the tab opens.
func TestProjectNotes(t *testing.T) {
	a, tt := newTestApp(t)
	folder := appTempDir(t, a)
	gone := filepath.Join(folder, "gone")
	setProjectHome(t, a, tt, "", gone)
	a.newProjectTab()
	waitFor(t, tt, "the note", func() bool { return a.note() != nil })
	tt.Frame()
	if n := a.note(); n.wait || n.action == nil || !strings.Contains(a.errText(), "gone") || !tt.HasText(a.errText()) {
		t.Fatalf("the note %q: %+v; texts %q", a.errText(), n, tt.Texts())
	}
	click(t, tt, locales.ProjectChangeFolder())
	if !a.sheet.open || a.sheet.space != a.ws.ID || a.errText() != "" {
		t.Fatalf("Change Folder… of the note: sheet %+v, note %q", a.sheet, a.errText())
	}
	// Another message has no button, though the note was not cleared.
	a.sheet.open = false
	a.folderMissing("")
	a.err, a.errMsg = "something else", nil
	if a.note() != nil {
		t.Errorf("the note's button stays with another message")
	}

	setProjectHome(t, a, tt, layout.MachineHost("machine-far"), folder)
	a.err, a.errMsg = "", nil
	a.newProjectTab()
	tt.Frame()
	if n := a.note(); n == nil || n.action != nil || n.wait || !tt.HasText(locales.ProjectHostNotYours(a.ws.Name, folder)) {
		t.Errorf("the note of an unknown host: %q", a.errText())
	}

	a.setNote(func(m locales.Messages) string { return m.ProjectHostWaiting("p", "/x") }, bannerNote{wait: true})
	if n := a.note(); n == nil || !n.wait {
		t.Errorf("the wait: %+v", n)
	}
}

// TestCrumbs checks the breadcrumb's steps and the paths below a folder,
// of macOS and Linux paths and of Windows'.
func TestCrumbs(t *testing.T) {
	names := func(cs []crumb) (n, p []string) {
		for _, c := range cs {
			n, p = append(n, c.name), append(p, c.path)
		}
		return
	}
	n, p := names(crumbs("/home/me/code", false))
	if !slices.Equal(n, []string{"/", "home", "me", "code"}) || !slices.Equal(p, []string{"/", "/home", "/home/me", "/home/me/code"}) {
		t.Errorf("unix crumbs %q %q", n, p)
	}
	n, p = names(crumbs(`C:\Users\me`, true))
	if !slices.Equal(n, []string{locales.ProjectSheetDrives(), "C:", "Users", "me"}) || !slices.Equal(p, []string{rex.DirTop, `C:\`, `C:\Users`, `C:\Users\me`}) {
		t.Errorf("windows crumbs %q %q", n, p)
	}
	if n, p = names(crumbs(`C:\`, true)); len(n) != 2 || p[1] != `C:\` {
		t.Errorf("a drive's crumbs %q %q", n, p)
	}
	if n, p = names(crumbs(`\\srv\share\x`, true)); !slices.Equal(p, []string{rex.DirTop, `\\srv\share\`, `\\srv\share\x`}) {
		t.Errorf("a share's crumbs %q %q", n, p)
	}
	for _, c := range []struct {
		dir, name string
		win       bool
		want      string
	}{
		{"/", "etc", false, "/etc"},
		{"/home", "me", false, "/home/me"},
		{rex.DirTop, `D:\`, true, `D:\`},
		{`D:\`, "x", true, `D:\x`},
		{`D:\x`, "y", true, `D:\x\y`},
	} {
		if got := childPath(c.dir, c.name, c.win); got != c.want {
			t.Errorf("childPath(%q, %q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
	for _, c := range [][3]string{
		{"/home/me/code", "/home/me", "~/code"},
		{"/home/me", "/home/me/", "~"},
		{"/home/meow", "/home/me", "/home/meow"},
		{`C:\Users\Me\x`, `c:\users\me`, `~\x`},
		{"/srv", "", "/srv"},
	} {
		if got := tildePath(c[0], c[1]); got != c[2] {
			t.Errorf("tildePath(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	if folderName(`C:\code\app\`) != "app" || folderName("/a/b") != "b" {
		t.Error("folderName")
	}
}
