//go:build darwin || linux

package rex

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// openToSend opens path for a get, a regular file only, reached through
// no symlink that leads out of its folder (checkLinks). It looks before
// it opens, and opens without blocking, so that a FIFO is never waited
// on and a device never acted on; then it looks at the open file again,
// and refuses it unless it is the file it looked at, so that one swapped
// in between is refused too.
func openToSend(path string) (*os.File, fs.FileInfo, error) {
	st, err := checkLinks(path)
	if err != nil {
		return nil, nil, err
	}
	if err := checkRegular(path, st); err != nil {
		return nil, nil, err
	}
	if checkedHook != nil {
		checkedHook()
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	opened, err := f.Stat()
	if err == nil {
		err = checkRegular(path, opened)
	}
	if err == nil && !os.SameFile(st, opened) {
		err = fmt.Errorf("rex: %s changed as it was opened", path)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, opened, nil
}
