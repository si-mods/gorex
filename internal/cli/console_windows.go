package cli

import (
	"context"
	"sync/atomic"

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
func attachConsole() {
	const attachParentProcess = 0xFFFFFFFF // (DWORD)-1
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r != 0 {
		procSetConsoleCtrlHandler.Call(consoleCtrlHandler, 1)
	}
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
