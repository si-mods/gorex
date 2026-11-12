//go:build darwin || linux

package rex

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// openToSend opens path for a get, a regular file only. It looks before
// it opens, and opens without blocking, so that a FIFO is never waited
// on and a device never acted on; then it looks at the open file again,
// so that one swapped in between is refused too.
func openToSend(path string) (*os.File, fs.FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if err := checkRegular(path, st); err != nil {
		return nil, nil, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	if st, err = f.Stat(); err == nil {
		err = checkRegular(path, st)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}
