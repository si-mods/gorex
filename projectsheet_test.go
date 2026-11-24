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
// sidebar's +: the path field is the browser — a path typed lists its
// folder, a click goes into one, ".." goes up; the name follows the
// folder until typed; Create shows the project, its home kept by the
// server, with its first tab in the folder.
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

	// A path typed, then Enter on the row it names.
	click(t, tt, locales.ProjectSheetPath())
	tt.Type(root)
	waitFor(t, tt, "the folder "+filepath.Dir(root)+" listed", func() bool {
		return !a.sheet.loading && a.sheet.asked && sameDir(a.sheet.res.Path, filepath.Dir(root))
	})
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
	// ".." goes back; the name follows the folder.
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
// project on that host (docs/MOD-002-projects.md), in the space shown, and
// the window shows it, its first tab in the folder there.
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
	// ".." goes back to root.
	click(t, tt, locales.ProjectSheetUp())
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

// TestProjectHeader checks that the sidebar's project row says its name,
// and not its folder: the folder, under ~ or not, is in no label or
// tooltip.
func TestProjectHeader(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{TabPlacement: "left"}
	tt.Frame()
	home := a.homeOf(nil)
	folder := filepath.Join(home, "code", "windflow")
	setProjectHome(t, a, tt, "", folder)
	want := "~" + string(filepath.Separator) + filepath.Join("code", "windflow")
	if tt.HasText(want) {
		t.Errorf("the project's row says its folder %q: %q", want, tt.Texts())
	}
	if !tt.HasText(a.ws.Name) {
		t.Errorf("the project's row does not say its name %q: %q", a.ws.Name, tt.Texts())
	}
}

// TestPaths checks the sheet's path reading: split's directory and leaf,
// the paths below a folder, of macOS and Linux paths and of Windows'.
func TestPaths(t *testing.T) {
	s := &projectSheet{}
	for _, c := range []struct {
		path, dir, leaf string
	}{
		{"", "", ""},
		{"/home/me/code", "/home/me/", "code"},
		{"/home/me/", "/home/me/", ""},
		{"/", "/", ""},
		{"~", "~", ""},
		{"~/code", "~/", "code"},
		{"code", "", "code"},
	} {
		if d, l := s.splitFor(c.path); d != c.dir || l != c.leaf {
			t.Errorf("split %q = %q, %q, want %q, %q", c.path, d, l, c.dir, c.leaf)
		}
	}
	// Windows paths read as Windows' before a listing says so.
	ws := &projectSheet{}
	ws.res.Home = `C:\Users\me`
	for _, c := range []struct {
		path, dir, leaf string
	}{
		{`C:`, `C:\`, ""},
		{`C:\`, `C:\`, ""},
		{`C:\Users\me`, `C:\Users\`, "me"},
		{`\\srv\share\x`, `\\srv\share\`, "x"},
	} {
		if d, l := ws.splitFor(c.path); d != c.dir || l != c.leaf {
			t.Errorf("windows split %q = %q, %q, want %q, %q", c.path, d, l, c.dir, c.leaf)
		}
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
	if got := commonStart("acme-api", "ACME-app"); got != "acme-ap" {
		t.Errorf("commonStart = %q", got)
	}
}

// splitFor reads path as split does on the sheet: dir, leaf.
func (s *projectSheet) splitFor(path string) (dir, leaf string) {
	s.path = path
	return s.split()
}

// TestSheetEntries checks the folder rows a path field shows: ".." for
// the parent first, then the folders the leaf keeps, the ones it starts
// first; and that the leaf that names a folder chooses it.
func TestSheetEntries(t *testing.T) {
	s := &projectSheet{asked: true}
	s.res = rex.DirListResult{Path: "/x", Parent: "/", Home: "/home/me", Dirs: []string{"alpha", "ab", "ba", "inner"}}
	names := func() (out []string) {
		for _, e := range s.entries() {
			if e.up {
				out = append(out, "..")
			} else {
				out = append(out, e.name)
			}
		}
		return
	}
	if got := names(); !slices.Equal(got, []string{"..", "alpha", "ab", "ba", "inner"}) {
		t.Fatalf("all rows %q", got)
	}
	s.path = "/x/a"
	if got := names(); !slices.Equal(got, []string{"..", "alpha", "ab", "ba"}) {
		t.Errorf("the leaf keeps and orders %q", got)
	}
	s.path = "/x/zz"
	if got := names(); !slices.Equal(got, []string{".."}) {
		t.Errorf("nothing matches %q", got)
	}
	if s.chosen() != "" {
		t.Errorf("a leaf that matches nothing chooses %q", s.chosen())
	}
	s.path = "/x/ALPHA"
	if got := s.chosen(); got != "/x/alpha" {
		t.Errorf("an exact leaf chooses %q, want /x/alpha", got)
	}
	s.path = "/x/"
	if got := s.chosen(); got != "/x" {
		t.Errorf("no leaf chooses %q, want /x", got)
	}
	// No parent, no ".." row.
	s.res.Parent = ""
	s.path = "/x/"
	if got := names(); !slices.Equal(got, []string{"alpha", "ab", "ba", "inner"}) {
		t.Errorf("a folder at the top %q", got)
	}
}

// TestProjectSheetFilter browses by typing into the path field: the leaf
// filters the rows, a dot asks for the hidden folders, and Enter opens
// the leaf's match.
func TestProjectSheetFilter(t *testing.T) {
	a, tt := newTestApp(t)
	root := t.TempDir()
	for _, d := range []string{"gorex", "golinux", "plain", ".gitx"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	if tt.HasText(".gitx") {
		t.Fatal("a hidden folder shows with no dot typed")
	}

	// "go" keeps gorex and golinux; ".g" asks for the hidden folders.
	a.sheet.path = filepath.Join(root, "go")
	tt.Frame()
	waitFor(t, tt, "the leaf to filter", func() bool {
		return tt.HasText("gorex") && tt.HasText("golinux") && !tt.HasText("plain")
	})
	a.sheet.path = filepath.Join(root, ".g")
	tt.Frame()
	waitFor(t, tt, "the hidden folders", func() bool {
		return tt.HasText(".gitx") && !tt.HasText("gorex")
	})
	if a.sheet.hidden != true {
		t.Error("a leaf of a dot does not ask for the hidden folders")
	}

	// Enter in the field opens the leaf's exact match.
	a.sheet.path = filepath.Join(root, "gorex")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	sheetShows(t, a, tt, filepath.Join(root, "gorex"))
	if !sameDir(a.sheet.chosen(), filepath.Join(root, "gorex")) {
		t.Errorf("gorex opened, chosen %q", a.sheet.chosen())
	}
}

// TestProjectSheetKeys browses with the keyboard: Down and Up highlight
// a row — ".." first — Enter opens it, Backspace and Alt+Up go up and
// highlight the folder left, Tab completes; Enter in the name field
// creates the project.
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
	// ".." is the first row, alpha and beta after it.
	key(0, ui.KeyDown)
	key(0, ui.KeyDown)
	if a.sheet.sel != 1 {
		t.Fatalf("Down twice highlights %d", a.sheet.sel)
	}
	key(0, ui.KeyUp)
	if a.sheet.sel != 0 {
		t.Fatalf("Up highlights %d, not \"..\"", a.sheet.sel)
	}
	key(0, ui.KeyDown)
	key(0, ui.KeyEnter)
	alpha := filepath.Join(root, "alpha")
	sheetShows(t, a, tt, alpha)
	if a.sheet.sel != -1 {
		t.Errorf("a folder opened highlights %d", a.sheet.sel)
	}
	key(0, ui.KeyBackspace)
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 1 {
		t.Errorf("Backspace up highlights %d, not alpha", a.sheet.sel)
	}
	// Tab completes "a" to alpha, in the path field — Shift+Tab back
	// from the list puts the focus on it.
	a.sheet.path = filepath.Join(root) + string(filepath.Separator) + "a"
	key(ui.Shift, ui.KeyTab)
	if !tt.Focused(locales.ProjectSheetPath()) {
		t.Fatal("Shift+Tab did not reach the path field")
	}
	key(0, ui.KeyTab)
	sheetShows(t, a, tt, alpha)
	// Down hands the list the focus; a field's own Backspace edits.
	key(0, ui.KeyDown)
	key(0, ui.KeyBackspace)
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 1 {
		t.Errorf("Backspace up highlights %d, not alpha", a.sheet.sel)
	}
	key(0, ui.KeyDown)
	key(0, ui.KeyEnter)
	beta := filepath.Join(root, "beta")
	sheetShows(t, a, tt, beta)
	// beta has no folders: ".." alone says so, and the folder is usable.
	if !tt.HasText(locales.ProjectSheetEmpty()) || !tt.HasText(locales.ProjectSheetChosen(a.sheet.res.Path)) {
		t.Errorf("an empty folder: %q", tt.Texts())
	}
	key(ui.Alt, ui.KeyUp)
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 2 {
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
