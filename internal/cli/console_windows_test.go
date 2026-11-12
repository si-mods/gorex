package cli

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestConsoleCtrl checks the handler attachConsole sets: Ctrl-C cancels
// what `gorex run` waits on, and without a run waiting it leaves Ctrl-C
// to Windows, which ends the process.
func TestConsoleCtrl(t *testing.T) {
	t.Cleanup(func() { interrupt.Store(nil) })
	if consoleCtrl(windows.CTRL_C_EVENT) != 0 {
		t.Fatal("Ctrl-C was handled with no run waiting")
	}
	ctx := onInterrupt(context.Background())
	if consoleCtrl(windows.CTRL_CLOSE_EVENT) != 0 || ctx.Err() != nil {
		t.Fatal("closing the console was taken for Ctrl-C")
	}
	if consoleCtrl(windows.CTRL_C_EVENT) != 1 || ctx.Err() == nil {
		t.Fatal("Ctrl-C did not interrupt the run")
	}
}

// TestLost checks which standard handles attachConsole opens on the
// console: none, as a GUI program started from a console has, and not a
// file or a pipe that redirection gave it.
func TestLost(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, c := range []struct {
		name string
		h    uintptr
		want bool
	}{{"none", 0, true}, {"pipe", w.Fd(), false}, {"file", f.Fd(), false}} {
		if got := lost(windows.GetFileType(windows.Handle(c.h))); got != c.want {
			t.Errorf("%s: lost = %v, want %v", c.name, got, c.want)
		}
	}
	if lost(windows.FILE_TYPE_UNKNOWN, syscall.EINVAL) {
		t.Error("a handle of an unknown type, with no error, was lost")
	}
	if lost(windows.FILE_TYPE_CHAR, nil) {
		t.Error("a console was lost")
	}
}
