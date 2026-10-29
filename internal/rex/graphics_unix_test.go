//go:build darwin || linux

package rex

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadImageFileRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := readImageFile(path, "")
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("read a FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("opening a FIFO blocked")
	}
}
