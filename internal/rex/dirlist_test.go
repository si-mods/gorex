//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
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

// TestDirListLoop answers a path through a loop of symlinks with
// not_found.
func TestDirListLoop(t *testing.T) {
	_, c, home := dirListFixture(t)
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(home, "self")
	if err := os.Symlink(self, self); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{a, filepath.Join(a, "x"), self} {
		if r := dirList(t, c, path, false); r.Code != DirNotFound || r.Path != path {
			t.Errorf("%s: %+v, want not_found", path, r)
		}
	}
	if r := dirList(t, c, "", false); len(r.Dirs) != 0 {
		t.Errorf("home lists loops as directories: %v", r.Dirs)
	}
}

// TestDirListHangs: a listing that hangs, as on a mount gone dead, holds
// no other request of its connection, which stays alive; it answers an
// error at its deadline, and a connection runs a few at once, refusing
// more.
func TestDirListHangs(t *testing.T) {
	s, _, _ := dirListFixture(t)
	release, entered := make(chan struct{}), make(chan struct{}, maxDirListsAside)
	old := listDirFunc
	listDirFunc = func(home, path string, hidden bool) (DirListResult, error) {
		if path == "hang" {
			entered <- struct{}{}
			<-release
		}
		return old(home, path, hidden)
	}
	keep := keepaliveTimeout.set(600 * time.Millisecond)
	deadline := dirListTimeout.set(time.Second)
	t.Cleanup(func() {
		close(release)
		listDirFunc = old
		keepaliveTimeout.set(keep)
		dirListTimeout.set(deadline)
	})
	c := pipe(s, "")
	defer c.Close()
	c.keepalive = true
	if h, err := c.Hello(); err != nil || h.Keepalive == 0 {
		t.Fatalf("hello %+v %v", h, err)
	}

	errs := make(chan error, maxDirListsAside)
	for range maxDirListsAside {
		go func() {
			_, err := c.DirList("hang", false)
			errs <- err
		}()
	}
	for range maxDirListsAside {
		<-entered
	}
	if _, err := c.DirList("", false); err == nil || !strings.HasPrefix(err.Error(), DirListBusy) || !strings.Contains(err.Error(), "on this connection") {
		t.Errorf("a listing over the connection's slots: %v, want refused", err)
	}
	if _, err := c.List(); err != nil {
		t.Errorf("list while listings hang: %v", err)
	}
	for range maxDirListsAside {
		if err := <-errs; err == nil || !strings.Contains(err.Error(), "took over") {
			t.Errorf("a hung listing: %v, want its deadline", err)
		}
	}
	// The slots are free again, though the hung listings are not over.
	dirList(t, c, "", false)
	// Pings went on through it all: the connection outlives the
	// server's keepalive wait.
	time.Sleep(2 * keepaliveTimeout.get())
	select {
	case <-c.Closed():
		t.Fatal("the connection closed while listings hung")
	default:
	}
	if _, err := c.List(); err != nil {
		t.Errorf("list after: %v", err)
	}
}

// TestDirListBusy refuses a listing while too many run in the server,
// stuck ones among them.
func TestDirListBusy(t *testing.T) {
	_, c, _ := dirListFixture(t)
	dirListsRunning.Add(maxDirListsRunning)
	defer dirListsRunning.Add(-maxDirListsRunning)
	if _, err := c.DirList("", false); err == nil || err.Error() != errDirListBusy.Error() || !strings.HasPrefix(err.Error(), DirListBusy) {
		t.Errorf("%v, want %v", err, errDirListBusy)
	}
}

// fakeDir is a directory of names for firstDirs, read n at a time; a
// name that ends in ".txt" is a file. err comes with the last names, or
// alone when there are none.
type fakeDir struct {
	names []string
	err   error
	reads []int
}

func (d *fakeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	d.reads = append(d.reads, n)
	if len(d.names) == 0 {
		if d.err != nil {
			return nil, d.err
		}
		return nil, io.EOF
	}
	k := min(n, len(d.names))
	var out []fs.DirEntry
	for _, name := range d.names[:k] {
		mode := fs.ModeDir
		if strings.HasSuffix(name, ".txt") {
			mode = 0
		}
		out = append(out, fs.FileInfoToDirEntry(fakeInfo{name, mode}))
	}
	d.names = d.names[k:]
	if len(d.names) == 0 && d.err != nil {
		err := d.err
		d.err = nil
		return out, err
	}
	return out, nil
}

type fakeInfo struct {
	name string
	mode fs.FileMode
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return 0 }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

// TestFirstDirs reads a directory in batches, never whole, holds no more
// than twice the names it answers, and keeps the first directories in
// order however the system returns them, files and all (#726).
func TestFirstDirs(t *testing.T) {
	var names, want []string
	for i := range 3000 {
		names = append(names, fmt.Sprintf("d%04d", i), fmt.Sprintf("f%04d.txt", i))
		want = append(want, fmt.Sprintf("d%04d", i))
	}
	rand.New(rand.NewPCG(1, 2)).Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	later := time.Now().Add(time.Hour)
	isDir := func(e fs.DirEntry) bool { return e.IsDir() }
	for _, limit := range []int{5, 2999, 3000, 3001} {
		d := &fakeDir{names: slices.Clone(names)}
		kept := 0
		got, more, err := firstDirs(d, limit, later, func(e fs.DirEntry) bool {
			kept++
			return isDir(e)
		})
		w := want[:min(limit, len(want))]
		if err != nil || more != (limit < len(want)) || !slices.Equal(got, w) {
			t.Errorf("limit %d: %d dirs %v more %v err %v; want %d more %v", limit, len(got), got[:min(3, len(got))], more, err, len(w), limit < len(want))
		}
		if kept != len(names) {
			t.Errorf("limit %d: saw %d entries, want %d", limit, kept, len(names))
		}
		for _, n := range d.reads {
			if n != dirListBatch {
				t.Fatalf("limit %d: ReadDir(%d), want batches of %d", limit, n, dirListBatch)
			}
		}
	}

	boom := errors.New("boom")
	if _, _, err := firstDirs(&fakeDir{err: boom}, 5, later, isDir); err != boom {
		t.Errorf("error at first read: %v, want %v", err, boom)
	}
	got, more, err := firstDirs(&fakeDir{names: []string{"b", "a"}, err: boom}, 5, later, isDir)
	if err != nil || more || !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("error with the first entries: %v %v %v, want [a b] and no error", got, more, err)
	}
}

// TestFirstNamesHoldsFew holds no more than 2*(limit+1) names, however
// many are added, and keeps the first in order (#726).
func TestFirstNamesHoldsFew(t *testing.T) {
	f := firstNames{limit: 10}
	for i := range 5000 {
		f.add(fmt.Sprintf("d%04d", 4999-i))
		if len(f.names) > 2*(f.limit+1) {
			t.Fatalf("holds %d names after %d, want at most %d", len(f.names), i+1, 2*(f.limit+1))
		}
	}
	got, more := f.first()
	if !more || len(got) != 10 || got[0] != "d0000" || got[9] != "d0009" {
		t.Errorf("first %v more %v, want d0000..d0009 and more", got, more)
	}
	if got, more := (&firstNames{limit: 10}).first(); got == nil || more {
		t.Errorf("none: %v more %v, want [] and no more", got, more)
	}
}

// TestFirstDirsDeadline stops reading between batches once the deadline
// has passed, so a listing the client gave up on frees its place in
// dirListsRunning.
func TestFirstDirsDeadline(t *testing.T) {
	var names []string
	for i := range 10 * dirListBatch {
		names = append(names, fmt.Sprintf("d%05d", i))
	}
	d := &fakeDir{names: names}
	_, _, err := firstDirs(d, DirListMax, time.Now().Add(-time.Second), func(fs.DirEntry) bool { return true })
	if err != errDirListLate || len(d.reads) != 1 {
		t.Errorf("err %v after %d reads, want %v after 1", err, len(d.reads), errDirListLate)
	}
}
