//go:build windows

package rexapi

import "syscall"

// extraNoFileErrors are the system's other errors of a path that names
// no file; deniedErrors those of a file this user may not read.
var (
	extraNoFileErrors = []error{syscall.ERROR_FILE_NOT_FOUND, syscall.ERROR_PATH_NOT_FOUND}
	deniedErrors      = []error{syscall.EACCES, syscall.ERROR_ACCESS_DENIED}
)
