//go:build darwin

package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The plist is what launchd reads: one it rejects starts no server, and
// every spawn would fall back to a child of the app's, which the app's
// end takes with it.
func TestJobPlistIsValid(t *testing.T) {
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("no plutil")
	}
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	t.Setenv("LANG", `a&b<c>"d'`)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "not-the-job's")
	exe := "/Applications/GoRex.app/Contents/MacOS/GoRex"
	path := filepath.Join(dir, "server.plist")
	if err := writePlist(path, jobPlist(jobLabel(dir), exe)); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("plutil", "-lint", path).CombinedOutput()
	if err != nil {
		t.Fatalf("launchd would reject the job: %v: %s", err, out)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the job is not the user's alone: %v %v", st, err)
	}
	// The app's directory and language, and not its secrets: the plist
	// keeps a few variables on disk, as Rex's does.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{dir, `a&amp;b&lt;c&gt;&#34;d&#39;`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the job does not carry %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "not-the-job's") {
		t.Error("the job carries the app's whole environment")
	}
}

// Two data directories must not share a job: each server is its own, and
// the label is how launchd names it.
func TestJobLabel(t *testing.T) {
	a, b := jobLabel("/Users/x/Library/Application Support/GoRex"), jobLabel("/Users/x/Library/Application Support/GoRex Dev")
	if a == b {
		t.Fatalf("two directories, one job: %q", a)
	}
	if a != jobLabel("/Users/x/Library/Application Support/GoRex") {
		t.Fatal("the label of a directory changed between calls")
	}
	if !strings.HasPrefix(a, "dev.gorex.app.server.") {
		t.Fatalf("the label is not the app's: %q", a)
	}
}

// A command that is not the app's (a test binary, `go run . -server`) is
// started as a process of its own: it must not take over the app's job,
// whose binary is the bundle's.
func TestInBundle(t *testing.T) {
	if !inBundle("/Applications/GoRex.app/Contents/MacOS/GoRex") {
		t.Fatal("the app's binary is not in a bundle")
	}
	if inBundle("/tmp/go-build123/b001/rex.test") {
		t.Fatal("a test binary counts as an app bundle")
	}
}
