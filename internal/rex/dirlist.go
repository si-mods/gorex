//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
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
// (DirList), of the home directory of the user the sessions run as. It
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
		r, err := listDirFunc(home, o.Path, o.Hidden)
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

// listDir lists the directories in path, of home when it is not absolute.
func listDir(home, path string, hidden bool) (DirListResult, error) {
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
	// filepath.EvalSymlinks resolves symlinks, but not Windows' junctions
	// (mount points), which it keeps as they are, and it fails on some
	// paths through them, as a junction's within another: a directory
	// the system reaches all the same is listed as it was asked.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if st, serr := os.Stat(path); serr != nil || !st.IsDir() {
			return dirCode(res, err)
		}
		real = path
	}
	res.Path, res.Parent = real, parentDir(real)
	if st, err := os.Stat(real); err != nil {
		return dirCode(res, err)
	} else if !st.IsDir() {
		res.Code = DirNotFound
		return res, nil
	}
	entries, err := os.ReadDir(real)
	if err != nil && len(entries) == 0 {
		return dirCode(res, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !hidden && isHiddenDir(e) {
			continue
		}
		if !e.IsDir() {
			// A symlink, or a junction on Windows, counts when it leads
			// to a directory.
			if e.Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
				continue
			}
			if st, err := os.Stat(filepath.Join(real, name)); err != nil || !st.IsDir() {
				continue
			}
		}
		res.Dirs = append(res.Dirs, name)
	}
	slices.SortFunc(res.Dirs, func(a, b string) int {
		if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	if len(res.Dirs) > DirListMax {
		res.Dirs, res.More = res.Dirs[:DirListMax], true
	}
	return res, nil
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
