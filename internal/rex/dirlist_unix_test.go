//go:build darwin || linux

package rex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDirListDenied answers a directory this user may not list with
// denied, and still says where it is.
func TestDirListDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists every directory")
	}
	_, c, home := dirListFixture(t)
	locked := filepath.Join(home, "locked")
	mkdirs(t, filepath.Join(locked, "inner"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	r := dirList(t, c, locked, false)
	if r.Code != DirDenied || r.Path != locked || r.Parent != home || len(r.Dirs) != 0 {
		t.Errorf("locked: %+v, want denied", r)
	}
	// Behind a directory that may not be searched, the path is denied too.
	if err := os.Chmod(locked, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := dirList(t, c, filepath.Join(locked, "inner"), false); r.Code != DirDenied {
		t.Errorf("locked/inner: %+v, want denied", r)
	}
}
