package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// quarantined tells whether the file at path is marked as downloaded.
func quarantined(t *testing.T, path string) bool {
	t.Helper()
	b := make([]byte, 256)
	n, err := unix.Getxattr(path, "com.apple.quarantine", b)
	return err == nil && strings.HasPrefix(string(b[:n]), "0081;")
}
