//go:build darwin

package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	app := "/Applications/GoRex.app/Contents/MacOS/GoRex"
	program := filepath.Join(dir, "server", "Versions", "Current", "GoRex.app", "Contents", "MacOS", "GoRex")
	path := filepath.Join(dir, "server.plist")
	if err := writePlist(path, jobPlist(jobLabel(dir), program, app)); err != nil {
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
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The copy runs the server, and the shim keeps naming the app; the
	// environment is a few variables, whose specials are escaped, and not
	// the app's whole environment.
	for _, want := range []string{program, "<string>-server</string>", dir, `a&amp;b&lt;c&gt;&#34;d&#39;`, app} {
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

// The server of a build runs from a copy of the app: the build's own, with
// Current naming it, and the copies of the builds before it gone. The copy
// is the same build — its times, so that a newer build is told from it.
func TestServerCopy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOREX_DIR", filepath.Join(root, "data"))
	bundle := filepath.Join(root, "GoRex.app")
	src := filepath.Join(bundle, "Contents", "MacOS", "GoRex")
	writeTestFile(t, src, "one", 0o755)
	writeTestFile(t, filepath.Join(bundle, "Contents", "Info.plist"), "plist", 0o644)
	writeTestFile(t, filepath.Join(bundle, "Contents", "Resources", "libghostty-vt.dylib"), "dylib", 0o644)
	first := time.Unix(1_700_000_000, 0)
	setTestTimes(t, src, first)

	copied, at, err := serverCopy(src)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(first) || !strings.HasPrefix(copied, filepath.Join(Dir(), "server", "Versions")) {
		t.Fatalf("the copy is %s at %v", copied, at)
	}
	if got := readTestFile(t, copied); got != "one" {
		t.Fatalf("the copy holds %q", got)
	}
	if st, err := os.Stat(copied); err != nil || !st.ModTime().Equal(first) {
		t.Fatalf("the copy is not the same build: %v %v", st, err)
	}
	if got := readTestFile(t, filepath.Join(Dir(), "server", "Versions", "Current", "GoRex.app", "Contents", "MacOS", "GoRex")); got != "one" {
		t.Fatalf("Current holds %q", got)
	}
	if n := len(versionDirs(t)); n != 1 {
		t.Fatalf("%d copies, want 1", n)
	}

	// A newer build: its copy is another, Current names it, and the build
	// before it is gone.
	writeTestFile(t, src, "two!", 0o755)
	second := first.Add(time.Hour)
	setTestTimes(t, src, second)
	copied2, at2, err := serverCopy(src)
	if err != nil {
		t.Fatal(err)
	}
	if copied2 != copied {
		t.Fatalf("the job runs the copy through Current, not %s", copied2)
	}
	if !at2.Equal(second) {
		t.Fatalf("the new build is at %v", at2)
	}
	if got := readTestFile(t, copied2); got != "two!" {
		t.Fatalf("the new copy holds %q", got)
	}
	if got := readTestFile(t, filepath.Join(Dir(), "server", "Versions", "Current", "GoRex.app", "Contents", "MacOS", "GoRex")); got != "two!" {
		t.Fatalf("Current holds %q", got)
	}
	if n := len(versionDirs(t)); n != 1 {
		t.Fatalf("%d copies after the new build, want 1", n)
	}
}

func TestServerCopyOfNoBundle(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	exe := filepath.Join(t.TempDir(), "rex.test")
	writeTestFile(t, exe, "", 0o755)
	if _, _, err := serverCopy(exe); err != errNoCopy {
		t.Fatalf("a test binary made a copy: %v", err)
	}
}

// versionDirs are the copies under Versions, Current aside.
func versionDirs(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(Dir(), "server", "Versions"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && e.Name() != "Current" && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func writeTestFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func setTestTimes(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
