//go:build windows

package rex

import (
	"os"
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

// junction makes a junction at link to target, as mklink /J does.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

// TestDirListJunction lists a junction to a directory as one, and goes
// through a junction as it is, unresolved, as filepath.EvalSymlinks does:
// a junction to itself nests as deep as it is asked.
func TestDirListJunction(t *testing.T) {
	_, c, home := dirListFixture(t)
	target := filepath.Join(home, "target")
	mkdirs(t, filepath.Join(target, "inner"))
	os.WriteFile(filepath.Join(home, "file.txt"), nil, 0o644)
	junction(t, filepath.Join(home, "jump"), target)
	if r := dirList(t, c, "", false); !reflect.DeepEqual(r.Dirs, []string{"jump", "target"}) {
		t.Errorf("home: %v, want the junction among the directories", r.Dirs)
	}
	jump := filepath.Join(home, "jump")
	if r := dirList(t, c, jump, false); r.Path != jump || r.Parent != home || !reflect.DeepEqual(r.Dirs, []string{"inner"}) {
		t.Errorf("through the junction: %+v", r)
	}
	self := filepath.Join(home, "target", "self")
	junction(t, self, target)
	deep := filepath.Join(self, "self", "self")
	if r := dirList(t, c, deep, false); r.Path != deep || r.Code != "" || !reflect.DeepEqual(r.Dirs, []string{"inner", "self"}) {
		t.Errorf(`self\self\self: %+v`, r)
	}
}

// TestDirListWindowsPaths: a drive with no root after it is of the
// drive's root; a path rooted with no drive, or an incomplete share, is
// not found, with no parent.
func TestDirListWindowsPaths(t *testing.T) {
	_, c, _ := dirListFixture(t)
	drive := os.Getenv("SystemDrive")
	win, err := filepath.EvalSymlinks(drive + `\Windows`)
	if err != nil {
		t.Fatal(err)
	}
	if r := dirList(t, c, drive+"Windows", false); r.Path != win || r.Parent != drive+`\` || r.Code != "" {
		t.Errorf("%sWindows: %+v, want %s", drive, r, win)
	}
	for _, path := range []string{"/Windows", `\Windows`, "//", `\\`, `\\server`, "//server", `/\`,
		`\\.\pipe\`, `\\.\PIPE\x`, "//./pipe/x", `\\.\C:\Windows`, `\\?\pipe\x`, `\\?\GLOBALROOT\Device`,
		`\\localhost\pipe\`, `\\127.0.0.1\pipe\x`, `\\?\UNC\localhost\pipe\x`, "//localhost/PIPE/x"} {
		if r := dirList(t, c, path, false); r.Code != DirNotFound || r.Path != path || r.Parent != "" || len(r.Dirs) != 0 {
			t.Errorf("%q: %+v, want not_found with no parent", path, r)
		}
	}
}
