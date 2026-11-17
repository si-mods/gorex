//go:build windows

package rex

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// isTop tells the DirList.Path of the top of the machine, above the
// drives: DirTop, written with either slash.
func isTop(path string) bool { return path == DirTop || path == `\` }

// drives are the roots of the machine's drives, as `C:\`, in order.
func drives() []string {
	out := []string{}
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return out
	}
	for i := range 26 {
		if mask&(1<<i) != 0 {
			out = append(out, string(rune('A'+i))+`:\`)
		}
	}
	return out
}

// volumeRoot makes a drive alone, as "C:", its root: else it would name
// the drive's current directory.
func volumeRoot(path string) string {
	if len(path) == 2 && path[1] == ':' {
		return path + `\`
	}
	return path
}

// parentDir is the directory above path: DirTop above a drive's root, ""
// above a share's (\\server\share), which no drive lists.
func parentDir(path string) string {
	if d := filepath.Dir(path); d != path {
		return d
	}
	if v := filepath.VolumeName(path); len(v) == 2 && v[1] == ':' {
		return DirTop
	}
	return ""
}

// isHiddenDir tells a directory whose name starts with a dot, or that has
// the hidden attribute, as Explorer hides it.
func isHiddenDir(e fs.DirEntry) bool {
	if strings.HasPrefix(e.Name(), ".") {
		return true
	}
	info, err := e.Info()
	if err != nil {
		return false
	}
	a, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && a.FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0
}

// notDir tells an error of a path through a file, or of a name Windows
// does not take.
func notDir(err error) bool {
	return errors.Is(err, windows.ERROR_DIRECTORY) || errors.Is(err, windows.ERROR_INVALID_NAME) || errors.Is(err, windows.ERROR_BAD_NETPATH)
}
