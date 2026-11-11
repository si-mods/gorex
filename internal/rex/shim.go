package rex

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CLIDir is where the gorex command lives: a shim to this executable, put
// at the front of each session's PATH, beside rec and sox on Unix.
func CLIDir() string { return filepath.Join(Dir(), "bin") }

// EnsureCLI makes the shims in CLIDir point at this executable. A
// failure is not fatal: sessions still get GOREX_SESSION.
func EnsureCLI() error {
	exe, err := os.Executable()
	if err != nil {
		return err
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
	// rec and sox stand in for SoX's, which Claude Code's /voice records
	// with on a machine with no microphone of its own: rec reads the
	// session's voice (Voice).
	for _, name := range []string{"gorex", "rec", "sox"} {
		if err := linkTo(filepath.Join(dir, name), exe); err != nil {
			return err
		}
	}
	return nil
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
