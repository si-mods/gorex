//go:build windows

package rex

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// plainPath reports whether path names a place on a drive or a file share,
// and nothing else. It is an allow-list, read by name before anything
// opens or looks at the path, because even os.Stat connects to a named
// pipe. Either slash is read as a backslash. It takes only
//
//	X:\...                          a drive path,
//	\\?\X:\...                      the long form of the same,
//	\\server\share[\...]            a UNC path,
//	\\?\UNC\server\share[\...]      the long form of the same,
//
// where the server is a name (not ".", "?", "??", and none that starts
// with ".", "?" or ";" as `\\.\`, `\\?\Global` and `\\;LanmanRedirector`
// do), the share is not "pipe" or "ipc$" (the host's named pipes), nor "."
// or "..", and no segment
// is empty. Anything else is refused: `\??\`, `\\.\`, `\\?\Global`,
// `\\?\GLOBALROOT`, a redirector form, a relative path.
func plainPath(path string) bool {
	p := strings.ReplaceAll(path, "/", `\`)
	if driveRooted(p) {
		return true
	}
	if rest, ok := strings.CutPrefix(p, `\\?\`); ok {
		if driveRooted(rest) {
			return true
		}
		if len(rest) > 4 && strings.EqualFold(rest[:4], `unc\`) {
			return plainUNC(rest[4:])
		}
		return false
	}
	if rest, ok := strings.CutPrefix(p, `\\`); ok {
		return plainUNC(rest)
	}
	return false
}

// driveRooted reports whether p starts with a drive letter and `:\`.
func driveRooted(p string) bool {
	return len(p) >= 3 && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') && p[1] == ':' && p[2] == '\\'
}

// plainUNC reports whether rest, of a UNC path after its `\\`, is
// `server\share[\...]` as plainPath takes it. One trailing backslash is
// allowed.
func plainUNC(rest string) bool {
	parts := strings.Split(strings.TrimSuffix(rest, `\`), `\`)
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	if strings.ContainsRune(".?;", rune(parts[0][0])) {
		return false
	}
	// The share "pipe" and the SMB share "IPC$" are the host's pipes; "."
	// and ".." are no share. Windows drops a name's trailing dots and
	// spaces: "pipe." is "pipe".
	if parts[1] == "." || parts[1] == ".." {
		return false
	}
	share := strings.TrimRight(parts[1], ". ")
	return !strings.EqualFold(share, "pipe") && !strings.EqualFold(share, "ipc$")
}

// openToSend opens path for a get, a regular file only. It refuses a
// device or pipe path (see plainPath), which opening would wait on, looks
// before it opens, and looks at the open file again, so that one swapped
// in between is refused too.
func openToSend(path string) (*os.File, fs.FileInfo, error) {
	if !plainPath(path) {
		return nil, nil, fmt.Errorf("rex: %s %s", path, notRegular)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if err := checkRegular(path, st); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if st, err = f.Stat(); err == nil {
		err = checkRegular(path, st)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}
