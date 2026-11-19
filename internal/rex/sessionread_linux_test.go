//go:build linux

package rex

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// readFixture makes a folder others may enter, with a file all may read
// (open.txt), one only its owner may (secret.txt), a folder all may list
// (work) and one only its owner may (private).
func readFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gorex-read")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(filepath.Join(dir, "private"), 0o700)
		os.RemoveAll(dir)
	})
	mkdirs(t, filepath.Join(dir, "work"), filepath.Join(dir, "private", "inner"))
	for name, mode := range map[string]os.FileMode{"open.txt": 0o644, "secret.txt": 0o600} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	for path, mode := range map[string]os.FileMode{dir: 0o755, filepath.Join(dir, "private"): 0o700} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// checkHelper opens and lists through the helper of sp, which may read
// what all may, and when denied is true not what only the folder's owner
// may.
func checkHelper(t *testing.T, sp spawner, dir string, denied bool) {
	t.Helper()
	f, st, err := openAsUserHelper(sp, filepath.Join(dir, "open.txt"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	b, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(b) != "open.txt" || st.Size() != int64(len("open.txt")) || st.Name() != "open.txt" {
		t.Errorf("open: %q %v, size %d name %q", b, err, st.Size(), st.Name())
	}
	if _, _, err := openAsUserHelper(sp, dir); !Folder(err) {
		t.Errorf("open of a folder: %v", err)
	}
	if _, _, err := openAsUserHelper(sp, filepath.Join(dir, "missing")); err == nil || !strings.HasSuffix(err.Error(), "no such file or directory") {
		t.Errorf("open of a missing file: %v", err)
	}
	_, _, err = openAsUserHelper(sp, filepath.Join(dir, "secret.txt"))
	if denied && (err == nil || !strings.HasSuffix(err.Error(), "permission denied")) {
		t.Errorf("open of another user's file: %v, want permission denied", err)
	} else if !denied && err != nil {
		t.Errorf("open of the user's own file: %v", err)
	}
	r, err := listDirAsUserHelper(sp, dir, "", false)
	if err != nil || r.Path != dir || !reflect.DeepEqual(r.Dirs, []string{"private", "work"}) {
		t.Errorf("list: %+v %v", r, err)
	}
	r, err = listDirAsUserHelper(sp, dir, "private", false)
	want := DirListResult{Path: filepath.Join(dir, "private"), Parent: dir, Home: dir, Dirs: []string{"inner"}}
	if denied {
		want.Dirs, want.Code = []string{}, DirDenied
	}
	if err != nil || !reflect.DeepEqual(r, want) {
		t.Errorf("list of private: %+v %v, want %+v", r, err, want)
	}
}

// TestSessionReadHelper runs the helper a server reads as its session
// user with (sessionReadFlag) as this test's user: it hands back the
// file it opened, or the system's error, and the listing it made.
func TestSessionReadHelper(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the helper refuses root: TestSessionReadAsAnotherUser runs it as nobody")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	checkHelper(t, spawner{exe: exe}, readFixture(t), false)
}

// TestSessionReadAsAnotherUser runs the helper as nobody, as a server
// that runs as root with EnvSessionUser does (#720): it reads what nobody
// may, and not root's own file and folder. It needs root, which CI does
// not run tests as.
func TestSessionReadAsAnotherUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to run the helper as another user")
	}
	u, err := lookupSessionUser("nobody")
	if err != nil {
		t.Skip(err)
	}
	dir := readFixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The test binary's own folder is root's alone.
	helper := filepath.Join(dir, "helper")
	if err := copyFile(exe, helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	checkHelper(t, spawner{user: u, exe: helper}, dir, true)
}
