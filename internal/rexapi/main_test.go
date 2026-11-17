package rexapi

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gorex/internal/rex"
)

// defaultDir is rex.Dir with GOREX_DIR unset: in the temporary home, never
// the user's. Every test server has a directory of its own, so this one
// stays empty; a server that writes here resolved the default directory,
// as #719's did once its test had ended.
var defaultDir string

// TestMain points the home and configuration directories at a temporary
// one, so that no test reaches the user's GoRex directory, and fails the
// run when a server wrote in the default directory.
func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) int {
	// A flooding session's program (TestFloodHelper) starts no server, and
	// ends with os.Exit, which would leave a temporary home behind.
	if os.Getenv("GOREX_TEST_FLOOD") != "" {
		return m.Run()
	}
	root, err := os.MkdirTemp("", "rexapi-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(root)
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		d := filepath.Join(root, strings.ToLower(k))
		if err := os.MkdirAll(d, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.Setenv(k, d)
	}
	os.Unsetenv("GOREX_DIR")
	defaultDir = rex.Dir()
	if !strings.HasPrefix(defaultDir, root) {
		fmt.Fprintf(os.Stderr, "the default directory %s is not in the temporary home\n", defaultDir)
		return 1
	}
	code := m.Run()
	if err := checkDefaultDir(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		code = 1
	}
	return code
}

// checkDefaultDir fails when anything is in the default directory.
func checkDefaultDir() error {
	entries, err := os.ReadDir(defaultDir)
	if len(entries) == 0 || err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return fmt.Errorf("a server resolved the default directory %s: it holds %v", defaultDir, names)
}

// TestDefaultDirIsChecked starts a server on the default directory, as one
// that outlives its test does: checkDefaultDir sees it.
func TestDefaultDirIsChecked(t *testing.T) {
	t.Setenv("GOREX_DIR", "")
	if got := rex.Dir(); got != defaultDir {
		t.Fatalf("Dir %s, want %s", got, defaultDir)
	}
	sock, done := serveHere(t)
	stopServer(t, sock, done)
	err := checkDefaultDir()
	if err := os.RemoveAll(defaultDir); err != nil {
		t.Fatal(err)
	}
	if err == nil {
		t.Fatal("checkDefaultDir passes a server in the default directory")
	}
}

// failingTB is a test that fails: Fatal ends the goroutine that calls it,
// and run runs the cleanups after, last first, as the testing package does.
type failingTB struct {
	testing.TB
	cleanups []func()
}

func (f *failingTB) Helper()               {}
func (f *failingTB) Cleanup(fn func())     { f.cleanups = append(f.cleanups, fn) }
func (f *failingTB) Fatal(...any)          { runtime.Goexit() }
func (f *failingTB) Fatalf(string, ...any) { runtime.Goexit() }
func (f *failingTB) Setenv(key, value string) {
	old, had := os.LookupEnv(key)
	os.Setenv(key, value)
	f.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// run runs body as a test, then the cleanups.
func (f *failingTB) run(body func()) {
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		body()
	}()
	<-ran
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

// TestServerStopsWithFailedTest fails a test once its server runs, as
// #719's failed at its first dial: the server has stopped when the test's
// cleanups end, so it cannot resolve Dir once GOREX_DIR is given back.
func TestServerStopsWithFailedTest(t *testing.T) {
	ft := &failingTB{TB: t}
	var done <-chan struct{}
	ft.run(func() {
		_, done = startServer(ft)
		ft.Fatal("the test fails")
	})
	if done == nil {
		t.Fatal("the server did not start")
	}
	select {
	case <-done:
	default:
		t.Fatal("the server outlived its failed test")
	}
}
