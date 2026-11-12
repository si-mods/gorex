//go:build !windows

package rexapi

import "syscall"

// extraNoFileErrors are the system's other errors of a path that names
// no file; deniedErrors those of a file this user may not read.
var (
	extraNoFileErrors []error
	deniedErrors      = []error{syscall.EACCES, syscall.EPERM}
)
