package rex

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "gorex.cmd")
		body := fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", exe)
		if old, err := os.ReadFile(path); err == nil && string(old) == body {
			return nil
		}
		return os.WriteFile(path, []byte(body), 0o700)
	}
	path := filepath.Join(dir, "gorex")
	if target, err := os.Readlink(path); err == nil && target == exe {
		return nil
	}
	os.Remove(path)
	return os.Symlink(exe, path)
}
