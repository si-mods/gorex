//go:build darwin

package rex

import "golang.org/x/sys/unix"

// inputMode reports how the terminal handles typed input, from the line
// discipline of its pty.
func (p *pty) inputMode() (InputMode, error) {
	var m InputMode
	err := control(p.master, func(fd int) error {
		t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
		if err != nil {
			return err
		}
		m = InputMode{
			Echo:      t.Lflag&unix.ECHO != 0,
			Canonical: t.Lflag&unix.ICANON != 0,
			Signals:   t.Lflag&unix.ISIG != 0,
		}
		return nil
	})
	return m, err
}
