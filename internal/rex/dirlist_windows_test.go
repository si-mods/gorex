//go:build windows

package rex

import (
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

// TestDirListHiddenAttribute hides a directory of the hidden attribute
// unless asked, as Explorer does.
func TestDirListHiddenAttribute(t *testing.T) {
	_, c, home := dirListFixture(t)
	secret := filepath.Join(home, "secret")
	mkdirs(t, secret, filepath.Join(home, "shown"))
	p, _ := windows.UTF16PtrFromString(secret)
	if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_DIRECTORY); err != nil {
		t.Fatal(err)
	}
	if r := dirList(t, c, "", false); !reflect.DeepEqual(r.Dirs, []string{"shown"}) {
		t.Errorf("without hidden: %v", r.Dirs)
	}
	if r := dirList(t, c, "", true); !reflect.DeepEqual(r.Dirs, []string{"secret", "shown"}) {
		t.Errorf("with hidden: %v", r.Dirs)
	}
}

// TestDirListDenied answers a directory this user may not list with
// denied, and still says where it is.
func TestDirListDenied(t *testing.T) {
	_, c, home := dirListFixture(t)
	locked := filepath.Join(home, "locked")
	mkdirs(t, filepath.Join(locked, "inner"))
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", locked, "/deny", "*"+me.Uid+":(RD)").CombinedOutput(); err != nil {
		t.Skipf("icacls: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("icacls", locked, "/remove:d", "*"+me.Uid).Run() })
	r := dirList(t, c, locked, false)
	if r.Code != DirDenied || r.Path != locked || r.Parent != home || len(r.Dirs) != 0 {
		t.Errorf("locked: %+v, want denied", r)
	}
}
