//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// dirListTimeout is how long a listing may take before "dir.list" answers
// an error: a directory on a mount that hangs, as an SMB or NFS share
// whose server is gone, holds the call that reads it till the system
// gives up. It is shorter than a client's call timeout, so the client
// hears why.
var dirListTimeout = newDuration(10 * time.Second)

// maxDirListsAside is how many listings a connection runs aside at once
// (control): a listing that hangs holds no other request of its
// connection. maxDirListsRunning is how many run in the whole server, those
// that outlived their deadline, still stuck in the system, among them.
const (
	maxDirListsAside   = 2
	maxDirListsRunning = 16
)

// dirListsRunning counts the listings under way, stuck ones among them.
var dirListsRunning atomic.Int32

// errDirListBusy answers a listing over maxDirListsRunning, and
// errDirListsAside one over maxDirListsAside of its connection.
var (
	errDirListBusy   = errors.New(DirListBusy + " under way; try again later")
	errDirListsAside = errors.New(DirListBusy + " under way on this connection; try again later")
)

// DirListBusy starts the error of a "dir.list" refused because too many
// run, on its connection or in the server: one to try again later, unlike
// a listing that failed.
const DirListBusy = "rex: too many directory listings"

// VolumePath is volumePath for callers outside this package: the path
// itself when it names a place a directory can be read from, false for a
// device, a pipe, or a path rooted with no volume. On Windows reading such
// a path may wait on it or contact another host, so no path a client sends
// reaches a file call unchecked.
func VolumePath(path string) (string, bool) { return volumePath(path) }

// FillDirListsForTest makes the servers of this process refuse listings
// as too many run, till restore: for the tests of other packages.
func FillDirListsForTest() (restore func()) {
	dirListsRunning.Add(maxDirListsRunning)
	return func() { dirListsRunning.Add(-maxDirListsRunning) }
}

// listDirFunc lists a directory: listDir, which tests replace with one
// that hangs.
var listDirFunc = listDir

// doDirList lists the directories of a directory of this machine
// (DirList), of the home directory of the user the sessions run as, and
// as that user (listAsSession). It
// answers within dirListTimeout: a listing that takes longer goes on
// alone, counted in dirListsRunning till the system lets it go.
func (s *Server) doDirList(req Request, _ string, _ *keptConn) (any, error) {
	var o DirList
	if req.DirList != nil {
		o = *req.DirList
	}
	if dirListsRunning.Add(1) > maxDirListsRunning {
		dirListsRunning.Add(-1)
		return nil, errDirListBusy
	}
	type answer struct {
		res DirListResult
		err error
	}
	done := make(chan answer, 1)
	go func() {
		defer dirListsRunning.Add(-1)
		home, err := s.homeDir()
		if err != nil {
			done <- answer{err: err}
			return
		}
		r, err := s.listAsSession(home, o.Path, o.Hidden)
		done <- answer{r, err}
	}()
	timeout := dirListTimeout.get()
	select {
	case a := <-done:
		return a.res, a.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("rex: listing %q took over %v", o.Path, timeout)
	}
}

// doDirMkdir makes the directory a "dir.mkdir" asks for (DirMkdir), as
// the user the sessions run as.
func (s *Server) doDirMkdir(req Request, _ string, _ *keptConn) (any, error) {
	var o DirMkdir
	if req.DirMkdir != nil {
		o = *req.DirMkdir
	}
	home, err := s.homeDir()
	if err != nil {
		return nil, err
	}
	return nil, s.mkdirAsSession(home, o.Path)
}

// mkdirDir makes path and the folders under it that are missing, as the
// user making it. Path is read as listDir reads it: "~" and a relative
// path are of home; the top of the machine is no answer.
func mkdirDir(home, path string) error {
	if isTop(path) {
		return fmt.Errorf("rex: %q is the top of the machine", path)
	}
	if _, ok := volumePath(path); !ok {
		return fmt.Errorf("rex: %q is not a folder path", path)
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		path = filepath.Join(home, rest)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	return os.MkdirAll(filepath.Clean(path), 0o755)
}

// listDir lists the directories in path, of home when it is not absolute.
// It stops reading once dirListTimeout has passed, when doDirList has
// answered without it.
func listDir(home, path string, hidden bool) (DirListResult, error) {
	deadline := time.Now().Add(dirListTimeout.get())
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	res := DirListResult{Home: home, Dirs: []string{}}
	if isTop(path) {
		res.Path = DirTop
		res.Dirs = drives()
		return res, nil
	}
	path, ok := volumePath(path)
	if !ok {
		res.Path, res.Code = path, DirNotFound
		return res, nil
	}
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		path = filepath.Join(home, rest)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	path = filepath.Clean(path)
	res.Path, res.Parent = path, parentDir(path)
	real, err := resolveDir(path)
	if err != nil {
		return dirCode(res, err)
	}
	res.Path, res.Parent = real, parentDir(real)
	if st, err := os.Stat(real); err != nil {
		return dirCode(res, err)
	} else if !st.IsDir() {
		res.Code = DirNotFound
		return res, nil
	}
	f, err := os.Open(real)
	if err != nil {
		return dirCode(res, err)
	}
	defer f.Close()
	dirs, more, err := firstDirs(f, DirListMax, deadline, func(e fs.DirEntry) bool {
		if !hidden && isHiddenDir(e) {
			return false
		}
		if e.IsDir() {
			return true
		}
		// A symlink, or a junction on Windows, counts when it leads to a
		// directory.
		if e.Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
			return false
		}
		st, err := os.Stat(filepath.Join(real, e.Name()))
		return err == nil && st.IsDir()
	})
	if err != nil {
		return dirCode(res, err)
	}
	res.Dirs, res.More = dirs, more
	return res, nil
}

// dirListBatch is how many entries firstDirs reads at a time.
const dirListBatch = 256

// errDirListLate ends a listing past its deadline: doDirList has answered
// the client already, and the listing frees its place in dirListsRunning.
var errDirListLate = errors.New("rex: listing past its deadline")

// dirReader reads a directory n entries at a time, as *os.File does.
type dirReader interface {
	ReadDir(n int) ([]fs.DirEntry, error)
}

// firstNames keeps the first limit names in order (compareDirs) of those
// added, and whether there are more, in at most 2*(limit+1) names however
// many are added: when they fill up, it sorts them and drops all but the
// first limit+1.
type firstNames struct {
	limit int
	names []string
}

func (f *firstNames) add(name string) {
	f.names = append(f.names, name)
	if len(f.names) >= 2*(f.limit+1) {
		f.trim()
	}
}

func (f *firstNames) trim() {
	slices.SortFunc(f.names, compareDirs)
	if len(f.names) > f.limit+1 {
		f.names = f.names[:f.limit+1]
	}
}

// first is the first limit names, never nil, and whether there are more.
func (f *firstNames) first() ([]string, bool) {
	f.trim()
	if f.names == nil {
		return []string{}, false
	}
	if len(f.names) > f.limit {
		return f.names[:f.limit], true
	}
	return f.names, false
}

// firstDirs reads the entries of d in batches and keeps the names of those
// keep accepts (firstNames): memory grows with limit, not with d. An error
// after some entries were read ends the listing with what it has; an
// error before any is returned. Past deadline it stops between batches
// with errDirListLate.
func firstDirs(d dirReader, limit int, deadline time.Time, keep func(fs.DirEntry) bool) ([]string, bool, error) {
	f := firstNames{limit: limit}
	for first := true; ; first = false {
		entries, err := d.ReadDir(dirListBatch)
		for _, e := range entries {
			if keep(e) {
				f.add(e.Name())
			}
		}
		if err != nil {
			if err != io.EOF && first && len(entries) == 0 {
				return nil, false, err
			}
			break
		}
		if time.Now().After(deadline) {
			return nil, false, errDirListLate
		}
	}
	names, more := f.first()
	return names, more, nil
}

// compareDirs orders directory names regardless of case, then by case.
func compareDirs(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// resolveDir is path, absolute and clean, as dir.list lists it: its
// symlinks resolved, and on Windows each name in the case it has on disk
// (filepath.EvalSymlinks). EvalSymlinks keeps Windows' junctions (mount
// points) as they are, and fails on some paths through them, as a
// junction's within another: a directory the system reaches all the same
// is kept as it was asked.
func resolveDir(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if st, serr := os.Stat(path); serr != nil || !st.IsDir() {
			return path, err
		}
		real = path
	}
	return real, nil
}

// ProjectFolder is the folder dir names, as dir.list names it
// (resolveDir), and whether it is a directory here: dir must be absolute
// and not a device or pipe (VolumePath), which is checked before anything
// reads it. Two paths of one folder, as /tmp/x and /private/tmp/x on
// macOS, are one project's (docs/MOD-002-projects.md).
func ProjectFolder(dir string) (string, bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return dir, false
	}
	dir = filepath.Clean(dir)
	if _, ok := VolumePath(dir); !ok {
		return dir, false
	}
	real, err := resolveDir(dir)
	if err != nil {
		return dir, false
	}
	st, err := os.Stat(real)
	return real, err == nil && st.IsDir()
}

// dirCode answers a directory that is not there, or that the server may
// not read, with its code; another error as it is. A path through a loop
// of symlinks leads to no directory: filepath.EvalSymlinks says so in
// words of its own, os.Stat with ELOOP.
func dirCode(res DirListResult, err error) (DirListResult, error) {
	switch {
	case errors.Is(err, fs.ErrNotExist) || notDir(err) || errors.Is(err, syscall.ELOOP) || strings.Contains(err.Error(), "too many links"):
		res.Code = DirNotFound
	case errors.Is(err, fs.ErrPermission):
		res.Code = DirDenied
	default:
		return DirListResult{}, err
	}
	return res, nil
}
