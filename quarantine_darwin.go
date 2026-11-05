package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// quarantine marks the file at path as downloaded, as a browser marks its
// downloads: the system asks before it opens an app, a script or a
// package of it.
func quarantine(path string) error {
	v := fmt.Sprintf("0081;%08x;GoRex;", time.Now().Unix())
	return unix.Setxattr(path, "com.apple.quarantine", []byte(v), 0)
}
