//go:build darwin || linux || windows

package rex

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

// dirListFixture is a test server whose home directory is a temporary
// one, its symlinks resolved, and a client of this machine's to it.
func dirListFixture(t *testing.T) (*Server, *Client, string) {
	t.Helper()
	s, _ := testServer(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	c := pipe(s, "")
	t.Cleanup(func() { c.Close() })
	return s, c, home
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func dirList(t *testing.T, c *Client, path string, hidden bool) DirListResult {
	t.Helper()
	r, err := c.DirList(path, hidden)
	if err != nil {
		t.Fatalf("dir.list %q: %v", path, err)
	}
	return r
}

// TestDirList lists directories through a client: the home directory by
// default and by "~", a nested path absolute or of home, with ".." gone
// and symlinks resolved, directories only, sorted without regard to case,
// hidden ones only when asked.
func TestDirList(t *testing.T) {
	_, c, home := dirListFixture(t)
	h, err := c.Hello()
	if err != nil || !h.Can(CapabilityDirList) {
		t.Fatalf("hello %v %v: no %s", h.Capabilities, err, CapabilityDirList)
	}
	mkdirs(t, filepath.Join(home, "work", "beta"), filepath.Join(home, "work", "Alpha"), filepath.Join(home, "work", "gamma", "deep"),
		filepath.Join(home, ".hidden"), filepath.Join(home, "work", ".git"))
	if err := os.WriteFile(filepath.Join(home, "work", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"", "~"} {
		r := dirList(t, c, path, false)
		if r.Path != home || r.Home != home || r.Code != "" || r.More || !reflect.DeepEqual(r.Dirs, []string{"work"}) {
			t.Errorf("%q: %+v, want home %s with work alone", path, r, home)
		}
		if r.Parent != filepath.Dir(home) {
			t.Errorf("%q: parent %q, want %q", path, r.Parent, filepath.Dir(home))
		}
	}
	if r := dirList(t, c, "", true); !reflect.DeepEqual(r.Dirs, []string{".hidden", "work"}) {
		t.Errorf("home with hidden: %v", r.Dirs)
	}

	work := filepath.Join(home, "work")
	for _, path := range []string{work, "work", "~/work", filepath.Join(work, "gamma", ".."), work + string(filepath.Separator)} {
		r := dirList(t, c, path, false)
		if r.Path != work || r.Parent != home || !reflect.DeepEqual(r.Dirs, []string{"Alpha", "beta", "gamma"}) {
			t.Errorf("%q: %+v, want %s: Alpha beta gamma", path, r, work)
		}
	}
	if r := dirList(t, c, work, true); !reflect.DeepEqual(r.Dirs, []string{".git", "Alpha", "beta", "gamma"}) {
		t.Errorf("work with hidden: %v", r.Dirs)
	}
	if r := dirList(t, c, filepath.Join(work, "gamma"), false); !reflect.DeepEqual(r.Dirs, []string{"deep"}) || r.Parent != work {
		t.Errorf("gamma: %+v", r)
	}

	// A symlink to a directory lists as one, and lists the directory it
	// leads to; one to a file does not.
	link := filepath.Join(home, "link")
	if err := os.Symlink(filepath.Join(work, "gamma"), link); err != nil {
		t.Logf("no symlinks here: %v", err)
	} else {
		os.Symlink(filepath.Join(work, "notes.txt"), filepath.Join(home, "filelink"))
		if r := dirList(t, c, "", false); !reflect.DeepEqual(r.Dirs, []string{"link", "work"}) {
			t.Errorf("home with a link: %v", r.Dirs)
		}
		r := dirList(t, c, link, false)
		if r.Path != filepath.Join(work, "gamma") || r.Parent != work || !reflect.DeepEqual(r.Dirs, []string{"deep"}) {
			t.Errorf("through the link: %+v", r)
		}
		// ".." goes up a name of the path asked, before its links.
		if r := dirList(t, c, filepath.Join(link, "deep", ".."), false); r.Path != filepath.Join(work, "gamma") {
			t.Errorf("link/deep/..: %+v", r)
		}
	}
}

// TestDirListMissing answers a path that is not there, or not a
// directory, with not_found, and still says where it is.
func TestDirListMissing(t *testing.T) {
	_, c, home := dirListFixture(t)
	file := filepath.Join(home, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "missing", "deeper")
	for path, want := range map[string]string{
		missing:                       missing,
		file:                          file,
		filepath.Join(file, "inside"): filepath.Join(file, "inside"),
		filepath.Join("~", "missing"): filepath.Join(home, "missing"),
	} {
		r := dirList(t, c, path, false)
		if r.Code != DirNotFound || r.Path != want || r.Parent != filepath.Dir(want) || r.Home != home || len(r.Dirs) != 0 || r.Dirs == nil {
			t.Errorf("%q: %+v, want not_found at %s", path, r, want)
		}
	}
}

// TestDirListLimit cuts a directory of more than DirListMax directories
// at the first DirListMax in order, and says so; files do not count.
func TestDirListLimit(t *testing.T) {
	_, c, home := dirListFixture(t)
	big := filepath.Join(home, "big")
	var want []string
	for i := range DirListMax + 1 {
		name := fmt.Sprintf("d%05d", i)
		want = append(want, name)
		mkdirs(t, filepath.Join(big, name))
	}
	for i := range 5 {
		os.WriteFile(filepath.Join(big, fmt.Sprintf("a-file-%d", i)), nil, 0o644)
	}
	r := dirList(t, c, big, false)
	if !r.More || len(r.Dirs) != DirListMax || !slices.Equal(r.Dirs, want[:DirListMax]) {
		t.Errorf("%d dirs, more %v, first %v last %v; want the first %d and more", len(r.Dirs), r.More, r.Dirs[:1], r.Dirs[len(r.Dirs)-1:], DirListMax)
	}
	if err := os.Remove(filepath.Join(big, want[DirListMax])); err != nil {
		t.Fatal(err)
	}
	if r := dirList(t, c, big, false); r.More || len(r.Dirs) != DirListMax {
		t.Errorf("exactly %d: %d dirs, more %v", DirListMax, len(r.Dirs), r.More)
	}
}

// TestDirListRemote lets a remote client list directories, as it may
// start a shell.
func TestDirListRemote(t *testing.T) {
	s, _, home := dirListFixture(t)
	mkdirs(t, filepath.Join(home, "proj"))
	c := pipe(s, "peer")
	defer c.Close()
	if r := dirList(t, c, "", false); r.Path != home || !reflect.DeepEqual(r.Dirs, []string{"proj"}) {
		t.Errorf("remote: %+v", r)
	}
}

// TestDirListTop lists the top of the machine: "/" on macOS and Linux,
// the drives on Windows, above whose roots it is.
func TestDirListTop(t *testing.T) {
	_, c, _ := dirListFixture(t)
	r := dirList(t, c, DirTop, false)
	if r.Path != DirTop || r.Parent != "" || r.Code != "" || len(r.Dirs) == 0 {
		t.Fatalf("top: %+v", r)
	}
	if runtime.GOOS != "windows" {
		return
	}
	sys := os.Getenv("SystemDrive") + `\`
	if !slices.Contains(r.Dirs, sys) {
		t.Errorf("drives %v lack %s", r.Dirs, sys)
	}
	if r2 := dirList(t, c, `\`, false); !reflect.DeepEqual(r2.Dirs, r.Dirs) || r2.Path != DirTop {
		t.Errorf(`\: %+v`, r2)
	}
	for _, path := range []string{sys, os.Getenv("SystemDrive")} {
		r := dirList(t, c, path, false)
		if r.Path != sys || r.Parent != DirTop || r.Code != "" || !slices.ContainsFunc(r.Dirs, func(d string) bool { return d == "Windows" }) {
			t.Errorf("%q: %+v, want %s under the drives, with Windows", path, r, sys)
		}
	}
}
