//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// linksOutside ends the error of a get through a symlink that leads out
// of the folder the link is in.
const linksOutside = "links outside its folder"

// LinksOutside reports whether err is the server refusing a get through a
// symlink that leads out of its folder (the error arrives as text).
func LinksOutside(err error) bool {
	return err != nil && strings.HasSuffix(err.Error(), linksOutside)
}

// maxLinks is how many symlinks a path may go through, as the systems
// allow about as many.
const maxLinks = 255

// checkedHook, when set, runs in openToSend between its checks and its
// open: tests swap the file there.
var checkedHook func()

// errLinksOutside is resolveLinks' error of a link that leads out of its
// folder, which checkLinks words with the path asked for.
var errLinksOutside = errors.New(linksOutside)

// checkLinks refuses path, absolute, when a symlink on it leads out of the
// folder the link is in, or a folder below it: the last name
// (chart.png -> ~/.ssh/id_ed25519) or a folder on the way (docs -> ~/.ssh,
// asked as docs/id_ed25519). A link's target counts with its own links
// resolved, so a chain that leaves in a later link is refused too; a link
// that stays in (a.png -> img/a.png, latest -> v2, b -> x/../b2) is
// followed. A program's output names a file by text that a cloned repo's
// links may point anywhere, and a get of it sends without asking.
//
// It returns the file path names, which openToSend compares with the file
// it opens: one swapped in after the check is refused. On Windows only
// symlinks are read, not junctions, which git does not make. Both
// openToSend call it, after any check of their own on the path.
func checkLinks(path string) (fs.FileInfo, error) {
	hops := 0
	real, err := resolveLinks(path, &hops)
	if err == nil {
		var st fs.FileInfo
		if st, err = os.Lstat(real); err == nil {
			// Windows reads a file's id when it is first compared: now,
			// not after the open.
			os.SameFile(st, st)
			return st, nil
		}
	}
	if errors.Is(err, errLinksOutside) {
		return nil, fmt.Errorf("rex: %s %s", path, linksOutside)
	}
	// The system's error, with the path asked for, not one a link led to.
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: pe.Err}
	}
	return nil, err
}

// resolveLinks is path, absolute, with its symlinks resolved one name at a
// time as the system does, or errLinksOutside for a link that leads out of
// its folder. hops counts the links followed, which a loop runs past
// maxLinks.
func resolveLinks(path string, hops *int) (string, error) {
	path = filepath.FromSlash(path)
	vol := filepath.VolumeName(path)
	rest := path[len(vol):]
	if rest == "" || !os.IsPathSeparator(rest[0]) {
		return "", fmt.Errorf("rex: %s is not absolute", path)
	}
	sep := string(filepath.Separator)
	cur := vol + sep
	for name := range strings.SplitSeq(rest, sep) {
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur) // cur has no links: its parent is the real one
			continue
		}
		next := filepath.Join(cur, name)
		st, err := os.Lstat(next)
		if err != nil {
			return "", err
		}
		if st.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if *hops++; *hops > maxLinks {
			return "", &fs.PathError{Op: "stat", Path: next, Err: syscall.ELOOP}
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		switch {
		case filepath.IsAbs(target):
		case filepath.VolumeName(target) != "":
			// C:x, of a drive's own folder of the moment: not this one.
			return "", errLinksOutside
		case target != "" && os.IsPathSeparator(target[0]):
			target = vol + target // Windows' \x, of the link's drive
		default:
			target = cur + sep + target // not cleaned: a/.. is a's parent
		}
		to, err := resolveLinks(target, hops)
		if err != nil {
			return "", err
		}
		if !within(cur, to) {
			return "", errLinksOutside
		}
		cur = to
	}
	return cur, nil
}

// within reports whether path is dir or under it; both are clean and have
// no links. Windows' names match without regard to case.
func within(dir, path string) bool {
	eq := func(a, b string) bool { return a == b }
	if runtime.GOOS == "windows" {
		eq = strings.EqualFold
	}
	if eq(path, dir) {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return len(path) > len(dir) && eq(path[:len(dir)], dir)
}
