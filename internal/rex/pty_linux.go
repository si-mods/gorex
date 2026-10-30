package rex

import (
	"bytes"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const defaultShell = "/bin/sh"

func openMaster() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("pty: %w", err)
	}
	n := 0
	err = control(master, func(fd int) (err error) {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
			return err
		}
		n, err = unix.IoctlGetInt(fd, unix.TIOCGPTN)
		return err
	})
	if err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty: %w", err)
	}
	return master, fmt.Sprintf("/dev/pts/%d", n), nil
}

// zombie reports whether pid exited and its parent has yet to wait for it.
func zombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// The state follows the command name, in parentheses, and a space.
	if i := bytes.LastIndexByte(b, ')'); i >= 0 {
		return len(b) > i+2 && b[i+2] == 'Z'
	}
	return false
}
