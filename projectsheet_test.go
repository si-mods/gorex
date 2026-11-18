package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

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
	if !a.sheet.open || a.sheet.host != nil {
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
	if a.sheet.open || a.ws.Name != "beta" || a.ws.host != nil || a.ws.Dir != folder {
		t.Fatalf("the project %q %v %q; sheet open %v", a.ws.Name, a.ws.host, a.ws.Dir, a.sheet.open)
	}
	if dir := localDirOf(t, a, a.tabs[0].Focus.SID); !sameDir(dir, folder) {
		t.Errorf("its first tab starts in %q, want %q", dir, folder)
	}
	caughtUp(t, a, tt)
	for _, w := range savedWorkspaces(t, a.client).Sessions {
		if w.ID == a.ws.ID && w.Dir != folder {
			t.Errorf("the server keeps the folder %q", w.Dir)
		}
	}
}

// TestNewProjectSheetHost browses a remote host's folders, on a test
// server: an offline host shows and cannot be chosen. Create makes the
// project on that host (docs/projects.md), in the space shown, and the
// window shows it, its first tab in the folder there.
func TestNewProjectSheetHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, _, _ := remotePaneOf(t, a, tt, t.TempDir())
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
	// An empty name is the folder's, as the placeholder says.
	a.sheet.name = ""

	click(t, tt, locales.ProjectSheetCreate())
	folder := filepath.Join(root, "beta")
	waitFor(t, tt, "the project on the host", func() bool {
		return a.ws != nil && a.ws.host == h && sameDir(a.ws.Dir, folder) && len(a.tabs) == 1 && a.tabs[0].Focus != nil
	})
	if a.ws.Name != "beta" || a.tabs[0].Focus.host != h {
		t.Errorf("the project %q, its pane on %v", a.ws.Name, a.tabs[0].Focus.host)
	}
}

// TestProjectSheetStaleListing cancels a sheet while a slow host lists a
// folder, and opens another on this machine: the host's late listing is
// not the new sheet's, whose folder stays this machine's.
func TestProjectSheetStaleListing(t *testing.T) {
	a, tt := newTestApp(t)
	h, proxy, _ := remotePane(t, a, tt, t.TempDir())
	root := folderTree(t, appTempDir(t, a))

	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	release := proxy.hold()
	t.Cleanup(release)
	// The host's listing is the sheet's second, as is alpha's below.
	a.pickHost(h)
	tt.Frame()
	if !a.sheet.loading || a.sheet.host != h {
		t.Fatalf("no listing of the host on its way: %+v", a.sheet)
	}
	click(t, tt, locales.ProjectSheetCancel())

	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	click(t, tt, "alpha")
	alpha := filepath.Join(root, "alpha")
	sheetShows(t, a, tt, alpha)
	release()
	// The host's listing comes back, and is dropped.
	for range 40 {
		time.Sleep(25 * time.Millisecond)
		tt.Frame()
	}
	if s := a.sheet; s.host != nil || !sameDir(s.res.Path, alpha) || !sameDir(s.chosen(), alpha) {
		t.Fatalf("the sheet of this machine shows %q of %v, would use %q", s.res.Path, s.host, s.chosen())
	}
}

// TestProjectHeader checks what the sidebar's project header says under
// the name: the folder of this machine under ~.
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
	if folderName(`C:\code\app\`) != "app" || folderName("/a/b") != "b" || folderName(`\\srv\share\`) != "share" {
		t.Error("folderName")
	}
	// A root has no name of its own: the project's is typed.
	if folderName(`C:\`) != "" || folderName("/") != "" || folderName("D:") != "" {
		t.Errorf("folderName of a root: %q %q", folderName(`C:\`), folderName("/"))
	}
}

// TestFitCrumbs checks which steps of a breadcrumb show in the room
// there is: all that fit; else the top, a menu of those left out and the
// last three, two or one; at worst the menu and the last.
func TestFitCrumbs(t *testing.T) {
	w := func(n int, each float32) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = each
		}
		return out
	}
	gap := float32(crumbGap)
	for _, c := range []struct {
		name   string
		widths []float32
		head   int
		room   float32
		want   []int
	}{
		{"all fit", w(4, 40), 0, 4*40 + 3*gap, []int{0, 1, 2, 3}},
		{"top and the last three", w(7, 40), 0, 4*40 + 26 + 4*gap, []int{0, -1, 4, 5, 6}},
		{"top and the last two", w(7, 40), 0, 3*40 + 26 + 3*gap, []int{0, -1, 5, 6}},
		{"top and the last", w(7, 40), 0, 2*40 + 26 + 2*gap, []int{0, -1, 6}},
		{"the menu and the last", w(7, 40), 0, 60, []int{-1, 6}},
		{"a long last step stays", []float32{10, 40, 40, 400}, 0, 200, []int{-1, 3}},
		// Windows: the drive is the top; the list of drives goes in the menu.
		{"windows, all fit", w(3, 40), 1, 3*40 + 2*gap, []int{0, 1, 2}},
		{"windows, the drives left out", w(5, 40), 1, 4*40 + 26 + 4*gap, []int{-1, 1, 2, 3, 4}},
		{"windows, the drive and the last two", w(6, 40), 1, 3*40 + 26 + 3*gap, []int{1, -1, 4, 5}},
	} {
		got := fitCrumbs(c.widths, c.head, c.room, 26)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if got := hiddenCrumbs([]int{1, -1, 4, 5}, 6); !slices.Equal(got, []int{0, 2, 3}) {
		t.Errorf("hiddenCrumbs %v", got)
	}
}

// deepTree makes root/<names...>, one inside the other, and returns the
// deepest.
func deepTree(t *testing.T, root string, names ...string) string {
	t.Helper()
	d := filepath.Join(append([]string{root}, names...)...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestProjectSheetBreadcrumb browses a deep folder of a long name: the
// breadcrumb keeps its name whole and its top, and leaves the steps
// between to its menu, which browses one.
func TestProjectSheetBreadcrumb(t *testing.T) {
	a, tt := newTestApp(t)
	root := appTempDir(t, a)
	long := "acme-api-service-gateway"
	deep := deepTree(t, root, "code", "clients", "acme", "services", long)
	a.openSheet(nil, deep)
	sheetShows(t, a, tt, deep)
	tt.Frame()
	if !tt.HasText(long) {
		t.Fatalf("the last step of the breadcrumb is gone: %q", tt.Texts())
	}
	steps := crumbs(a.sheet.res.Path, a.sheet.windows())
	top := steps[0].name
	if a.sheet.windows() {
		top = steps[1].name
	}
	if !tt.HasText(top) {
		t.Errorf("the breadcrumb lacks its top %q: %q", top, tt.Texts())
	}
	if _, ok := tt.Find(locales.ProjectSheetMoreFolders()); !ok {
		t.Fatalf("no menu of the steps left out: %q", tt.Texts())
	}
	// The field shows the path's end, the folder's name, while it has no
	// focus.
	if a.sheet.pathFocused || !tt.HasText(a.sheet.res.Path) {
		t.Errorf("the path over the field: %q", tt.Texts())
	}
	click(t, tt, locales.ProjectSheetMoreFolders())
	if m := tt.Menu(); !slices.Contains(m, "clients") || slices.Contains(m, long) {
		t.Fatalf("the menu of the steps left out: %q", m)
	}
	if err := tt.ChooseMenuItem("clients"); err != nil {
		t.Fatal(err)
	}
	sheetShows(t, a, tt, filepath.Join(root, "code", "clients"))
}

// TestProjectSheetKeys browses with the keyboard: Down and Up highlight
// a folder, Enter opens it, Backspace and Alt+Up go up and highlight the
// folder left; Enter in the name field creates the project.
func TestProjectSheetKeys(t *testing.T) {
	a, tt := newTestApp(t)
	root := folderTree(t, appTempDir(t, a))
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)

	key := func(mods ui.Modifiers, k ui.Key) {
		t.Helper()
		tt.Key(mods, k)
		tt.Frame()
	}
	key(0, ui.KeyDown)
	key(0, ui.KeyDown)
	if a.sheet.sel != 1 {
		t.Fatalf("Down twice highlights %d", a.sheet.sel)
	}
	key(0, ui.KeyUp)
	if a.sheet.sel != 0 {
		t.Fatalf("Up highlights %d", a.sheet.sel)
	}
	key(0, ui.KeyEnter)
	alpha := filepath.Join(root, "alpha")
	sheetShows(t, a, tt, alpha)
	if a.sheet.sel != -1 {
		t.Errorf("a folder opened highlights %d", a.sheet.sel)
	}
	key(0, ui.KeyBackspace)
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 0 {
		t.Errorf("Backspace up highlights %d, not alpha", a.sheet.sel)
	}
	key(0, ui.KeyDown)
	key(0, ui.KeyEnter)
	beta := filepath.Join(root, "beta")
	sheetShows(t, a, tt, beta)
	// beta has no folders: what the list says, and the folder to use.
	if !tt.HasText(locales.ProjectSheetEmpty()) || !tt.HasText(locales.ProjectSheetChosen(a.sheet.res.Path)) {
		t.Errorf("an empty folder: %q", tt.Texts())
	}
	key(ui.Alt, ui.KeyUp)
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 1 {
		t.Errorf("Alt+Up highlights %d, not beta", a.sheet.sel)
	}
	if !a.sheet.open {
		t.Fatal("the keys closed the sheet")
	}

	// Enter in the name field makes the project.
	key(0, ui.KeyEnter)
	sheetShows(t, a, tt, beta)
	before := a.ws
	tt.Frame()
	// The field under its label, which shows the same text.
	label, _ := tt.Find(locales.ProjectSheetName())
	tt.ClickAt(label.X+20, label.Y+label.H+8+14)
	tt.Frame()
	if !tt.Focused(locales.ProjectSheetName()) {
		t.Fatalf("the name field takes no focus; texts %q", tt.Texts())
	}
	a.sheet.name = ""
	tt.Frame()
	tt.Type("keyed")
	key(0, ui.KeyEnter)
	waitFor(t, tt, "the project made by Enter", func() bool { return a.ws != before && a.ws != nil && a.ws.Name == "keyed" })
	if a.sheet.open || !sameDir(a.ws.Dir, beta) {
		t.Errorf("Enter made %q in %q; sheet open %v", a.ws.Name, a.ws.Dir, a.sheet.open)
	}
}

// TestProjectSheetCreateState checks when Create can go: a folder named
// shows as the project's folder; the top of a disk, which has no name,
// needs one typed; the list of drives, and a folder that cannot be read,
// none.
func TestProjectSheetCreateState(t *testing.T) {
	a, tt := newTestApp(t)
	root := folderTree(t, appTempDir(t, a))
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	tt.Frame()
	if !a.sheet.ready() || !tt.HasText(locales.ProjectSheetChosen(a.sheet.res.Path)) {
		t.Fatalf("a folder: ready %v, texts %q", a.sheet.ready(), tt.Texts())
	}

	top := "/"
	if localWindows {
		top = filepath.VolumeName(root) + `\`
	}
	a.sheet.browse(top)
	sheetShows(t, a, tt, top)
	tt.Frame()
	if a.sheet.name != "" || a.sheet.ready() || !tt.HasText(locales.ProjectSheetNameNeeded()) {
		t.Fatalf("the top of the disk: name %q, ready %v, texts %q", a.sheet.name, a.sheet.ready(), tt.Texts())
	}
	click(t, tt, locales.ProjectSheetCreate())
	if !a.sheet.open {
		t.Fatal("Create made a project of no name")
	}
	a.sheet.name = "disk"
	tt.Frame()
	if !a.sheet.ready() || !tt.HasText(locales.ProjectSheetChosen(a.sheet.res.Path)) {
		t.Errorf("the top of the disk, named: ready %v, texts %q", a.sheet.ready(), tt.Texts())
	}

	if localWindows {
		a.sheet.browse(rex.DirTop)
		waitFor(t, tt, "the drives", func() bool { return !a.sheet.loading && a.sheet.asked && a.sheet.res.Path == rex.DirTop })
		tt.Frame()
		if a.sheet.ready() || !tt.HasText(locales.ProjectSheetNoChoice()) {
			t.Errorf("the drives: ready %v, texts %q", a.sheet.ready(), tt.Texts())
		}
	}
	a.sheet.browse(filepath.Join(root, "none"))
	waitFor(t, tt, "a folder gone", func() bool { return !a.sheet.loading && a.sheet.asked && a.sheet.res.Code != "" })
	tt.Frame()
	if a.sheet.ready() || !tt.HasText(locales.ProjectSheetNoChoice()) {
		t.Errorf("a folder gone: ready %v, texts %q", a.sheet.ready(), tt.Texts())
	}
}
