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
		if target, err := os.Readlink(path); err != nil || target != exe {
			t.Fatalf("shim points at %q, want %q: %v", target, exe, err)
		}
	}
	if err := EnsureCLI(); err != nil {
		t.Fatalf("second EnsureCLI: %v", err)
	}
}
