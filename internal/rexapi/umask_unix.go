//go:build !windows

package rexapi

import "syscall"

// privateUmask makes the files made until restore is called this user's
// only, as the gateway's socket must be from the start.
func privateUmask() (restore func()) {
	old := syscall.Umask(0o077)
	return func() { syscall.Umask(old) }
}
