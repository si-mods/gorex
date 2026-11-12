package rex

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureCLI(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	if err := EnsureCLI(); err != nil {
		t.Fatal(err)
	}
	name := "gorex"
	if runtime.GOOS == "windows" {
		name = "gorex.cmd"
	}
	path := filepath.Join(CLIDir(), name)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		exe, _ := os.Executable()
		exe, _ = executable(exe)
		if target, err := os.Readlink(path); err != nil || target != exe {
			t.Fatalf("gorex points at %q, want %q: %v", target, exe, err)
		}
	}
	if err := EnsureCLI(); err != nil {
		t.Fatalf("second EnsureCLI: %v", err)
	}
}

// A server started through the shim gets the shim's path as its
// executable: the shim must keep pointing at the binary, not at itself.
func TestLinkCLIThroughShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shim is a script on Windows")
	}
	t.Setenv("GOREX_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "GoRex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin, _ = executable(bin)
	if err := linkCLI(bin); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(CLIDir(), "gorex")
	if err := linkCLI(shim); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(shim); err != nil || target != bin {
		t.Fatalf("shim points at %q, want %q: %v", target, bin, err)
	}
}

// GoRex 0.22's rec and sox links go; a rec or sox of the user's stays.
func TestLinkCLIDropsRec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there were no links on Windows")
	}
	t.Setenv("GOREX_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "GoRex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin, _ = executable(bin)
	if err := os.MkdirAll(CLIDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	rec, sox := filepath.Join(CLIDir(), "rec"), filepath.Join(CLIDir(), "sox")
	if err := os.Symlink(bin, rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sox, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := linkCLI(bin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(rec); !os.IsNotExist(err) {
		t.Errorf("GoRex's rec stayed: %v", err)
	}
	if _, err := os.Lstat(sox); err != nil {
		t.Errorf("the user's sox went: %v", err)
	}
}
