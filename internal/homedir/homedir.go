// Package homedir resolves GoRex's home directory: ~/.gorex on macOS and
// Linux, %USERPROFILE%\.gorex on Windows, the one directory for the
// configuration and the data of the app, the CLI and the session server
// (docs/MOD-013-home-dir.md). It also moves the two directories a build
// before MOD-013 kept: the data directory of the platform's configuration
// directory, and init.lua's directory of the XDG path.
package homedir

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UserHomeDir, UserConfigDir and Dial are variables for tests and for
// the platform: the tests of packages that use homedir's resolution set
// them, as theme.TerminalThemeKnown is set.
var (
	UserHomeDir   = os.UserHomeDir
	UserConfigDir = os.UserConfigDir
	Dial          = func(path string) bool {
		c, err := net.DialTimeout("unix", path, 300*time.Millisecond)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
)

const (
	// dirName and devDirName are the home directories of the installed
	// app and of a development build, under the user's home.
	dirName    = ".gorex"
	devDirName = ".gorex-dev"
	// legacyDirName and legacyConfigName are the directories a build
	// before MOD-013 kept: the data directory under the platform's
	// configuration directory, and init.lua's under the XDG path.
	legacyDirName    = "GoRex"
	legacyConfigName = "gorex"
)

// Root returns the home directory: ~/.gorex, or ~/.gorex-dev for a
// development build. It returns "" when the user has no home.
func Root(dev bool) string {
	home, err := UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if dev {
		return filepath.Join(home, devDirName)
	}
	return filepath.Join(home, dirName)
}

// LegacyDataDir returns the data directory a build before MOD-013 used:
// the platform configuration directory's GoRex, GoRex Dev for a
// development build.
func LegacyDataDir(dev bool) string {
	base, err := UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	name := legacyDirName
	if dev {
		name += " Dev"
	}
	return filepath.Join(base, name)
}

// legacyConfigDir returns the directory a build before MOD-013 read
// init.lua from: the XDG configuration directory's gorex. It is the
// resolution of config.Path, without the GOREX_CONFIG override.
func legacyConfigDir() string {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
			base = filepath.Join(home, ".config")
		} else if dir, err := UserConfigDir(); err == nil {
			base = dir
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, legacyConfigName)
}

// DataDir resolves the data directory: GOREX_DIR, then the home when it
// exists, then the legacy data directory when it exists, then the home.
func DataDir(dev bool) string {
	if d := strings.TrimSpace(os.Getenv("GOREX_DIR")); d != "" {
		return d
	}
	if r := Root(dev); r != "" && isDir(r) {
		return r
	}
	if l := LegacyDataDir(dev); l != "" && isDir(l) {
		return l
	}
	return Root(dev)
}

// ConfigPath resolves the configuration file: GOREX_CONFIG, then the
// home's init.lua when it exists, then the legacy one, then init.lua in
// the data directory DataDir resolves. The last follows the data, so
// that a theme or icon pack installed before the move goes to the
// legacy data directory and never makes the home, which would take the
// resolution of every process away from the legacy data.
func ConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("GOREX_CONFIG")); p != "" {
		return p
	}
	if root := Root(false); root != "" && isFile(filepath.Join(root, "init.lua")) {
		return filepath.Join(root, "init.lua")
	}
	if l := legacyConfigDir(); l != "" && isFile(filepath.Join(l, "init.lua")) {
		return filepath.Join(l, "init.lua")
	}
	d := DataDir(false)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "init.lua")
}

// Migrate moves the legacy directories to Root(dev), in the code path
// that starts the session server (MOD-013). It does nothing when
// GOREX_DIR or GOREX_CONFIG names a directory, when the home exists
// already, when no legacy directory exists, or when a live server answers
// at the legacy socket. afterMove runs between the move and the legacy
// links, for the platform job of the server; it may be nil. It returns
// whether the home moved.
func Migrate(dev bool, afterMove func(legacy, root string)) (bool, error) {
	if os.Getenv("GOREX_DIR") != "" || os.Getenv("GOREX_CONFIG") != "" {
		return false, nil
	}
	root := Root(dev)
	if root == "" || exists(root) {
		return false, nil
	}
	data := LegacyDataDir(dev)
	// The configuration directory is the installed app's: a development
	// build's move takes only its own data directory.
	conf := ""
	if !dev {
		conf = legacyConfigDir()
	}
	hasData, hasConf := isDir(data), conf != "" && isDir(conf)
	if !hasData && !hasConf {
		return false, nil
	}
	// A running server owns the directory: its socket would go with the
	// move. The next start migrates instead.
	if hasData && Dial(filepath.Join(data, "server.sock")) {
		return false, nil
	}
	if hasData {
		if err := moveTree(data, root); err != nil {
			return false, err
		}
	} else if err := os.MkdirAll(root, 0o700); err != nil {
		return false, err
	}
	if hasConf {
		mergeInto(conf, root)
		// A directory whose entries all moved goes away, and the legacy
		// link below can take its place.
		os.Remove(conf)
	}
	if afterMove != nil {
		afterMove(data, root)
	}
	// The legacy paths link to the home, so a build, a script or a user
	// that still looks there finds the files. A link that fails is not
	// an error: Windows may refuse one.
	linkIfFree(data, root)
	if conf != "" && !exists(conf) {
		linkIfFree(conf, root)
	}
	return true, nil
}

// mergeInto moves each entry of src into dst. An entry whose name dst
// already has stays, and the move reports it.
func mergeInto(src, dst string) {
	des, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, de := range des {
		from, to := filepath.Join(src, de.Name()), filepath.Join(dst, de.Name())
		if exists(to) {
			log.Printf("homedir: %s exists already; %s stays", to, from)
			continue
		}
		if err := moveTree(from, to); err != nil {
			log.Printf("homedir: %s did not move: %v", from, err)
		}
	}
}

// moveTree puts src at dst: a rename, or a copy that keeps links and
// modes with the source removed, across devices.
func moveTree(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// copyTree copies src to dst as a tree. A symbolic link is copied as a
// link, so a move never follows a link out of the tree.
func copyTree(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case st.IsDir():
		if err := os.MkdirAll(dst, st.Mode().Perm()); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyTree(filepath.Join(src, de.Name()), filepath.Join(dst, de.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
}

// linkIfFree links name to target when name is free and the platform
// takes a symbolic link.
func linkIfFree(name, target string) {
	if exists(name) {
		return
	}
	if err := os.Symlink(target, name); err != nil {
		log.Printf("homedir: %s could not link to %s: %v", name, target, err)
	}
}

// isDir reports whether path is a directory, following a link.
func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// isFile reports whether path is a regular file, following a link.
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// exists reports whether path exists at all, link or not.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist) && err == nil
}
