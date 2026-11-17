//go:build darwin || linux || windows

package rex

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// doDirList lists the directories of a directory of this machine
// (DirList), of the home directory of the user the sessions run as.
func (s *Server) doDirList(req Request, _ string, _ *keptConn) (any, error) {
	var o DirList
	if req.DirList != nil {
		o = *req.DirList
	}
	home, err := s.homeDir()
	if err != nil {
		return nil, err
	}
	return listDir(home, o.Path, o.Hidden)
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
	path = volumeRoot(path)
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		path = filepath.Join(home, rest)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	path = filepath.Clean(path)
	res.Path, res.Parent = path, parentDir(path)
	real, err := filepath.EvalSymlinks(path)
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
// not read, with its code; another error as it is.
func dirCode(res DirListResult, err error) (DirListResult, error) {
	switch {
	case errors.Is(err, fs.ErrNotExist) || notDir(err):
		res.Code = DirNotFound
	case errors.Is(err, fs.ErrPermission):
		res.Code = DirDenied
	default:
		return DirListResult{}, err
	}
	return res, nil
}
