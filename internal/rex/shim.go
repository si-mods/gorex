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

// EnsureCLI makes the gorex shim in CLIDir point at this executable. A
// failure is not fatal: sessions still get GOREX_SESSION.
func EnsureCLI() error {
	dir := CLIDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
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
	path := filepath.Join(dir, "gorex")
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
