package cli

import (
	"context"
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
