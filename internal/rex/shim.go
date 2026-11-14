package rex

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CLIDir is where the gorex command lives: a shim to this executable, put
// at the front of each session's PATH.
func CLIDir() string { return filepath.Join(Dir(), "bin") }

// EnvAppExe names the app's executable to a server that runs from a copy
// of it (macOS, servercopy_darwin.go): the shim in CLIDir points there, so
// that it outlives the copy, which a build replaces.
const EnvAppExe = "GOREX_APP_EXE"

// EnsureCLI makes the shims in CLIDir point at this executable. A
// failure is not fatal: sessions still get GOREX_SESSION.
func EnsureCLI() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if app := os.Getenv(EnvAppExe); app != "" {
		exe = app
	}
	return linkCLI(exe)
}

// linkCLI makes the shim point at exe, its links followed: a server
// started through the shim is the shim's path, and a shim to itself runs
// nothing.
func linkCLI(exe string) error {
	exe, _ = executable(exe)
	dir := CLIDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "gorex.cmd")
		body := fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", strings.ReplaceAll(exe, "%", "%%"))
		if old, err := os.ReadFile(path); err == nil && string(old) == body {
			return nil
		}
		return replaceFile(path, []byte(body))
	}
	if err := linkTo(filepath.Join(dir, "gorex"), exe); err != nil {
		return err
	}
	// GoRex 0.22 linked rec and sox here too, as SoX's stand-in for
	// Claude Code's /voice; a link of those to exe, which now runs the
	// app, goes. This can go once no install has them.
	for _, name := range []string{"rec", "sox"} {
		path := filepath.Join(dir, name)
		if sameFile(path, exe) {
			os.Remove(path)
		}
	}
	return nil
}

// sameFile tells whether a and b, their links followed, are one file.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// linkTo makes path a link to exe, through a temporary name.
func linkTo(path, exe string) error {
	if exe == path {
		return fmt.Errorf("rex: %s leads nowhere", path)
	}
	if target, err := os.Readlink(path); err == nil && target == exe {
		return nil
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// replaceFile writes b to path through a temporary name.
func replaceFile(path string, b []byte) error {
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o700); err != nil {
		return err
	}
	os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
