//go:build !darwin && !linux && !windows

package rex

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("rex: the session server runs on macOS, Linux and Windows")

// Serve runs the server; not on this platform.
func Serve() error { return errUnsupported }

const handoffVersion = 0

// Resume serves the sessions of an upgrading server; not on this
// platform.
func Resume(string) error { return errUnsupported }

// Spawn starts a server; not on this platform.
func Spawn() error { return errUnsupported }

func processAlive(int) bool { return false }

// LockFile locks a file; not on this platform.
func LockFile(*os.File) error { return errUnsupported }

// OnServe runs beside a server; there is none on this platform.
var OnServe func(stop <-chan struct{})
