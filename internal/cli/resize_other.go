//go:build !darwin && !linux

package cli

// onResize does nothing where the terminal has no resize signal.
func onResize(func()) (stop func()) { return func() {} }
