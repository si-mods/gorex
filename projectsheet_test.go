package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// TestNewProjectSheetLocal makes a project with the sheet, as T3 Code's
// add-project does: the path field lists the folder its directory part
// names, a row's click goes into it, ".." goes up, and the Add the field
// holds — or Enter — makes the project, named for the folder.
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
	if a.sheet.path != "~/" {
		t.Errorf("the field starts at home, is %q", a.sheet.path)
	}

	// A path typed — into a field cleared first — lists its folder; a
	// folder clicked goes into it.
	a.sheet.path = ""
	tt.Frame()
	tt.Type(root + string(filepath.Separator))
	sheetShows(t, a, tt, root)
	if !tt.HasText("alpha") || !tt.HasText("beta") || tt.HasText("notes.txt") {
		t.Fatalf("the folders of the tree: %q", tt.Texts())
	}
	click(t, tt, "alpha")
	sheetShows(t, a, tt, filepath.Join(root, "alpha"))
	if !tt.HasText("inner") {
		t.Fatalf("in alpha: %q", tt.Texts())
	}
	// ".." goes back, and highlights the folder left.
	click(t, tt, locales.ProjectSheetUp())
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 1 {
		t.Errorf("back at the parent, row %d is highlighted, not alpha", a.sheet.sel)
	}
	click(t, tt, "beta")
	sheetShows(t, a, tt, filepath.Join(root, "beta"))
	folder := a.sheet.res.Path

	click(t, tt, locales.ProjectSheetAdd())
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
// server: an offline host shows and cannot be chosen. Add makes the
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
	folder := filepath.Join(root, "beta")
	sheetShows(t, a, tt, folder)

	click(t, tt, locales.ProjectSheetAdd())
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
	a.sheet.open = false
	tt.Frame()

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
	dir, _ := a.sheet.resolved()
	if s := a.sheet; s.host != nil || !sameDir(s.res.Path, alpha) || !sameDir(dir, alpha) {
		t.Fatalf("the sheet of this machine shows %q of %v, would use %q", s.res.Path, s.host, dir)
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
		{"~/", "~/", ""},
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
// the parent first, then the folders the leaf keeps, scored as T3 Code's
// browse scores them — an exact name first, a prefix next, then one
// inside the name; and that the leaf that names a folder takes it while
// one that names none asks to create it.
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
	// "ab" and "alpha" start with it — the shorter first — "ba" holds
	// it inside, "inner" does not.
	if got := names(); !slices.Equal(got, []string{"..", "ab", "alpha", "ba"}) {
		t.Errorf("the leaf keeps and orders %q", got)
	}
	s.path = "/x/alpha"
	if dir, create := s.resolved(); dir != "/x/alpha" || create {
		t.Errorf("an exact leaf takes %q create %v, want /x/alpha", dir, create)
	}
	s.path = "/x/zz"
	if got := names(); !slices.Equal(got, []string{".."}) {
		t.Errorf("nothing matches %q", got)
	}
	if dir, create := s.resolved(); dir != "/x/zz" || !create {
		t.Errorf("a new leaf creates %q create %v, want /x/zz", dir, create)
	}
	s.path = "/x/"
	if dir, create := s.resolved(); dir != "/x" || create {
		t.Errorf("no leaf takes %q create %v, want /x", dir, create)
	}
	// No parent, no ".." row.
	s.res.Parent = ""
	if got := names(); !slices.Equal(got, []string{"alpha", "ab", "ba", "inner"}) {
		t.Errorf("a folder at the top %q", got)
	}
}

// TestLeafScore checks the ranking T3 Code's scoreQueryMatch gives a
// leaf: exact, prefix, boundary, inside, fuzzy, in tiers a thousand
// apart; a name that holds none of it is out.
func TestLeafScore(t *testing.T) {
	for _, c := range []struct {
		name, leaf string
	}{
		{"alpha", "alpha"},
		{"alphabet", "alpha"},
		{"my alpha", "alpha"},
		{"xalpha", "alpha"},
		{"axbxc", "abc"},
	} {
		if leafScore(c.name, c.leaf) < 0 {
			t.Errorf("leafScore(%q, %q) does not match", c.name, c.leaf)
		}
	}
	if leafScore("zzz", "abc") >= 0 || leafScore("", "a") >= 0 {
		t.Error("a name without the leaf matches")
	}
	// The tiers, in order.
	tiers := []int{
		leafScore("alpha", "alpha"),
		leafScore("alpha-zz", "alpha"),
		leafScore("my alpha", "alpha"),
		leafScore("xalpha", "alpha"),
		leafScore("alnpha", "alpa"),
	}
	if !slices.IsSorted(tiers) || tiers[0] != 0 || tiers[1]/1000 != 1 || tiers[2]/1000 != 2 || tiers[3]/1000 != 3 || tiers[4]/1000 != 4 {
		t.Errorf("the tiers: %v", tiers)
	}
	// Inside a tier, the sooner and the shorter the better.
	if leafScore("a-alpha", "alpha") >= leafScore("zz-alpha", "alpha") {
		t.Error("a boundary match does not rank by place")
	}
	if leafScore("ab", "a") >= leafScore("abcdef", "a") {
		t.Error("a prefix match does not rank by length")
	}
}

// TestResolvePastedPath checks what the field does with pasted text, as
// T3 Code's resolvePastedPath says: a path pasted replaces the text,
// quoted and escaped forms unwrapping; typed text, and pasted text that
// names no path, stays.
func TestResolvePastedPath(t *testing.T) {
	for _, c := range []struct {
		prev, next, want string
	}{
		// Typing and deleting keep the text.
		{"~/cod", "~/code", "~/code"},
		{"~/code", "~/cod", "~/cod"},
		// A path pasted after a folder replaces; one under ~ stays,
		// being a folder path itself.
		{"/x/", "/x//a/b", "/a/b"},
		{"~/x", "~/x/a/b", "~/x/a/b"},
		{"", "/a/b c", "/a/b c"},
		{"/a/", "/a/b/c", "/a/b/c"},
		// Quoted and escaped paths unwrap.
		{"", `'/a b'`, "/a b"},
		{"", `"/a b"`, "/a b"},
		{"", `/a\ b/c\ d`, "/a b/c d"},
		{"", `C:\Users\me`, `C:\Users\me`},
		// Pasted text that names no path stays.
		{"~/x", "~/x hello", "~/x hello"},
		{"", "hello", "hello"},
	} {
		if got := resolvePastedPath(c.prev, c.next); got != c.want {
			t.Errorf("resolvePastedPath(%q, %q) = %q, want %q", c.prev, c.next, got, c.want)
		}
	}
}

// TestProjectSheetFilter browses by typing into the path field: the leaf
// filters the rows, a dot asks for the hidden folders, and Enter takes
// the folder the leaf names.
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

	// Enter takes the folder the leaf names: gorex's project.
	a.sheet.path = filepath.Join(root, "gorex")
	tt.Frame()
	before := a.ws
	tt.Key(0, ui.KeyEnter)
	waitFor(t, tt, "the gorex project", func() bool {
		return a.ws != before && a.ws != nil && a.ws.Name == "gorex"
	})
	if a.sheet.open || !sameDir(a.ws.Dir, filepath.Join(root, "gorex")) {
		t.Fatalf("Enter made %q of %q; sheet open %v", a.ws.Name, a.ws.Dir, a.sheet.open)
	}
}

// TestProjectSheetCreateFolder adds a folder that is not there: a leaf
// that names no folder says "Create & Add" and the hint under it, and
// Enter makes the folder — dir.mkdir — and its project.
func TestProjectSheetCreateFolder(t *testing.T) {
	a, tt := newTestApp(t)
	root := folderTree(t, appTempDir(t, a))
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)

	a.sheet.path = filepath.Join(root, "gamma")
	tt.Frame()
	if !a.sheet.willCreate() || !tt.HasText(locales.ProjectSheetCreateAdd()) || !tt.HasText(locales.ProjectSheetCreateHint()) {
		t.Fatalf("a new leaf: willCreate %v, texts %q", a.sheet.willCreate(), tt.Texts())
	}
	dir := filepath.Join(root, "gamma")
	before := a.ws
	tt.Key(0, ui.KeyEnter)
	waitFor(t, tt, "the folder made", func() bool {
		return a.ws != before && a.ws != nil && a.ws.Name == "gamma"
	})
	if a.sheet.open || !sameDir(a.ws.Dir, dir) {
		t.Fatalf("Create & Add made %q of %q; sheet open %v", a.ws.Name, a.ws.Dir, a.sheet.open)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Errorf("dir.mkdir made no folder %q: %v", dir, err)
	}
}

// TestProjectSheetKeys browses with the keyboard as T3 Code's palette
// does: Down and Up highlight a row — ".." first — Enter opens it, Enter
// with none highlighted takes the field's path, ⌘/Ctrl+Enter takes it
// over a highlighted row, and Tab enters a row's folder or extends the
// leaf to the start its folders share.
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
	if !tt.Focused(locales.ProjectSheetPath()) {
		t.Fatal("the path field did not take the focus on open")
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
	// Enter on the ".." row goes up, and highlights the folder left.
	key(0, ui.KeyDown)
	key(0, ui.KeyEnter)
	sheetShows(t, a, tt, root)
	if a.sheet.sel != 1 {
		t.Errorf("back up, row %d is highlighted, not alpha", a.sheet.sel)
	}

	// ⌘/Ctrl+Enter takes the field's path even over a highlighted row.
	mod := ui.Cmd
	if runtime.GOOS != "darwin" {
		mod = ui.Ctrl
	}
	before := a.ws
	key(mod, ui.KeyEnter)
	waitFor(t, tt, "the root project over a row", func() bool {
		return a.ws != before && a.ws != nil && sameDir(a.ws.Dir, root)
	})
	if a.sheet.open {
		t.Fatal("⌘/Ctrl+Enter left the sheet open")
	}

	// Tab enters the only folder the leaf starts, and extends it to
	// the start its folders share.
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	a.sheet.path = filepath.Join(root, "a")
	tt.Frame()
	key(0, ui.KeyTab)
	sheetShows(t, a, tt, alpha)
	// alpha has inner alone: its leaf grows to it.
	a.sheet.path = filepath.Join(alpha, "i")
	tt.Frame()
	key(0, ui.KeyTab)
	inner := filepath.Join(alpha, "inner")
	sheetShows(t, a, tt, inner)
	if !a.sheet.open {
		t.Fatal("the keys closed the sheet")
	}
}

// TestProjectSheetTabPrefix checks Tab with several folders the leaf
// starts: the leaf grows to their shared start, and a second Tab goes no
// further.
func TestProjectSheetTabPrefix(t *testing.T) {
	a, tt := newTestApp(t)
	root := t.TempDir()
	for _, d := range []string{"alphax", "alphay", "beta"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)

	a.sheet.path = filepath.Join(root, "a")
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if want := filepath.Join(root, "alpha"); a.sheet.path != want {
		t.Fatalf("Tab grew the leaf to %q, want %q", a.sheet.path, want)
	}
	if sameDir(a.sheet.res.Path, filepath.Join(root, "alphax")) {
		t.Fatal("Tab entered a folder two rows share")
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if a.sheet.path != filepath.Join(root, "alpha") {
		t.Errorf("a second Tab went to %q", a.sheet.path)
	}
	// The leaf spelled out enters its folder.
	a.sheet.path = filepath.Join(root, "alphax")
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	sheetShows(t, a, tt, filepath.Join(root, "alphax"))
}

// TestProjectSheetSubmitState checks when Add can go: a folder listed,
// or a leaf that creates one, yes; the list of drives, and a folder that
// cannot be read, no.
func TestProjectSheetSubmitState(t *testing.T) {
	a, tt := newTestApp(t)
	root := folderTree(t, appTempDir(t, a))
	a.openSheet(nil, root)
	sheetShows(t, a, tt, root)
	tt.Frame()
	if !a.sheet.canSubmit() || a.sheet.willCreate() {
		t.Fatalf("a folder: canSubmit %v willCreate %v", a.sheet.canSubmit(), a.sheet.willCreate())
	}

	if localWindows {
		a.sheet.browse(rex.DirTop)
		waitFor(t, tt, "the drives", func() bool { return !a.sheet.loading && a.sheet.asked && a.sheet.res.Path == rex.DirTop })
		tt.Frame()
		if a.sheet.canSubmit() {
			t.Error("the list of drives is not a folder to add")
		}
	}
	a.sheet.browse(filepath.Join(root, "none"))
	waitFor(t, tt, "a folder gone", func() bool { return !a.sheet.loading && a.sheet.asked && a.sheet.res.Code != "" })
	tt.Frame()
	if a.sheet.canSubmit() {
		t.Error("a folder gone can be added")
	}
}
