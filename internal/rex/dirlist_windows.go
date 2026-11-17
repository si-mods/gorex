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

// volumePath reads path as a picker means it, false for one that names
// no place. A drive with no root after it, as "C:" or "C:x", would name
// the drive's current directory, which means nothing to a picker: it is
// of the drive's root, `C:\` and `C:\x`. A path rooted with no volume, as
// `\Windows` or "/Windows", would be of the server's current drive, and an
// incomplete share, as "//" or `\\server`, is nowhere: no place. Nor is
// a device path, as `\\.\pipe\x`.
func volumePath(path string) (string, bool) {
	// A device or pipe path is no directory, and reading it may wait on
	// it: refused, as openToSend refuses it to file.get.
	if isDevicePath(path) {
		return path, false
	}
	vol := filepath.VolumeName(path)
	switch {
	case len(vol) == 2 && vol[1] == ':':
		if rest := path[2:]; rest == "" || (rest[0] != '\\' && rest[0] != '/') {
			return vol + `\` + rest, true
		}
	case vol == "" && path != "" && (path[0] == '\\' || path[0] == '/'):
		return path, false
	case (strings.HasPrefix(vol, `\\`) || strings.HasPrefix(vol, "//")) && !strings.ContainsAny(strings.Trim(vol, `\/`), `\/`):
		return path, false
	}
	return path, true
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
