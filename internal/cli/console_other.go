//go:build !windows

package cli

import "context"

// attachConsole does nothing where a command has its terminal already.
func attachConsole() {}

// onInterrupt returns parent: os/signal hears the terminal's Ctrl-C.
func onInterrupt(parent context.Context) context.Context { return parent }
