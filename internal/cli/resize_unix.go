//go:build darwin || linux

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

// onResize calls fn when the terminal is resized, until stop is called.
func onResize(fn func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ch:
				fn()
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
