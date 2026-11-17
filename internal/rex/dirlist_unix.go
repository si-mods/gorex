//go:build darwin || linux

package rex

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
)

// isTop tells the DirList.Path of the top of the machine: "/" is a
// directory like any other here.
func isTop(string) bool { return false }

// drives are Windows' only.
func drives() []string { return []string{} }

// volumePath is Windows' only: every path names a place here.
func volumePath(path string) (string, bool) { return path, true }

// parentDir is the directory above path, "" at "/".
func parentDir(path string) string {
	if d := filepath.Dir(path); d != path {
		return d
	}
	return ""
}

// isHiddenDir tells a directory whose name starts with a dot.
func isHiddenDir(e fs.DirEntry) bool { return strings.HasPrefix(e.Name(), ".") }

// notDir tells an error of a path through a file, as a/file/b.
func notDir(err error) bool { return errors.Is(err, syscall.ENOTDIR) }
