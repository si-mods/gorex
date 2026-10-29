//go:build darwin || linux

package rex

import (
	"os"

	"golang.org/x/sys/unix"
)

// openImageFile opens path without blocking on a FIFO, which a program may
// name as an image.
func openImageFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
