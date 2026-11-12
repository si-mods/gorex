package cli

import (
	"context"
	"io"
	"log"
	"os"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	consoleCtrlHandler        = windows.NewCallback(consoleCtrl)
	// interrupt is what Ctrl-C cancels, once onInterrupt set it.
	interrupt atomic.Pointer[context.CancelFunc]
)

// attachConsole attaches this process, a GUI program, to the console of
// the shell that runs the command, if it has one: Ctrl-C there reaches
// only the processes attached to it. Attaching drops the handler through
// which os/signal hears Ctrl-C, which Go set as it started; consoleCtrl
// takes its place.
//
// A GUI program started from a console has no standard handles, so the
// standard streams that lead nowhere are opened on the console, and out
// and errOut, when they are those streams, are returned as the new ones.
// A redirected or piped stream keeps its handle, which attaching had
// replaced with the console's.
func attachConsole(out, errOut io.Writer) (io.Writer, io.Writer) {
	const attachParentProcess = 0xFFFFFFFF // (DWORD)-1
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return out, errOut
	}
	procSetConsoleCtrlHandler.Call(consoleCtrlHandler, 1)
	stdout, stderr := os.Stdout, os.Stderr
	reopen(&os.Stdin, windows.STD_INPUT_HANDLE, "CONIN$")
	reopen(&os.Stdout, windows.STD_OUTPUT_HANDLE, "CONOUT$")
	reopen(&os.Stderr, windows.STD_ERROR_HANDLE, "CONOUT$")
	log.SetOutput(os.Stderr)
	if out == io.Writer(stdout) {
		out = os.Stdout
	}
	if errOut == io.Writer(stderr) {
		errOut = os.Stderr
	}
	return out, errOut
}

// reopen opens the console's name in place of *f, the standard stream of
// handle std, if *f leads nowhere, and otherwise gives std back its own
// handle. An os.File of a console writes to it in UTF-16, which any code
// page shows.
func reopen(f **os.File, std uint32, name string) {
	h := windows.Handle((*f).Fd())
	if !lost(windows.GetFileType(h)) {
		windows.SetStdHandle(std, h)
		return
	}
	c, err := windows.CreateFile(windows.StringToUTF16Ptr(name),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return
	}
	windows.SetStdHandle(std, c)
	*f = os.NewFile(uintptr(c), name)
}

// lost reports whether a handle of file type typ, as GetFileType gave it
// with err, leads nowhere. An unknown type with no error, which x/sys
// gives as EINVAL, is a handle still.
func lost(typ uint32, err error) bool {
	return typ == windows.FILE_TYPE_UNKNOWN && err != nil && err != syscall.EINVAL
}

// consoleCtrl cancels what onInterrupt returned on Ctrl-C or Ctrl-Break.
// Without one, the next handler, Windows's, ends the process, as Go does
// on a signal it was not asked to relay.
func consoleCtrl(event uint32) uintptr {
	if event != windows.CTRL_C_EVENT && event != windows.CTRL_BREAK_EVENT {
		return 0
	}
	cancel := interrupt.Load()
	if cancel == nil {
		return 0
	}
	(*cancel)()
	return 1
}

// onInterrupt returns a context of parent that Ctrl-C in the console
// cancels too.
func onInterrupt(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	interrupt.Store(&cancel)
	return ctx
}
