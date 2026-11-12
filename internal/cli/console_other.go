//go:build !windows

package cli

import (
	"context"
	"io"
)

// attachConsole returns out and errOut: a command has its terminal already.
func attachConsole(out, errOut io.Writer) (io.Writer, io.Writer) { return out, errOut }

// onInterrupt returns parent: os/signal hears the terminal's Ctrl-C.
func onInterrupt(parent context.Context) context.Context { return parent }
