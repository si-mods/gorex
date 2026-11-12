//go:build darwin || linux

package rex

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// A server upgrades by running the new executable in its own process,
// which keeps its children, the sessions' shells, and the descriptors it
// leaves open, the listener's, the lock's and the terminals'.

func canUpgrade() bool { return true }

// handOver runs exe in this process with the handoff h. It returns only
// when that fails.
func (s *Server) handOver(exe string, h *handoff) error {
	lf, err := s.ln.(*net.UnixListener).File()
	if err != nil {
		return err
	}
	defer lf.Close()
	h.Listener, h.Lock = descriptor(lf), descriptor(s.lock)
	fds := []int{h.Listener, h.Lock}
	for _, st := range h.Sessions {
		if st.Master >= 0 {
			fds = append(fds, st.Master)
		}
	}
	path := handoffPath()
	if err := writeJSON(path, h); err != nil {
		return err
	}
	defer os.Remove(path)
	s.stopForUpgrade(exe)
	// No process starts meanwhile, which would get the descriptors.
	syscall.ForkLock.Lock()
	closeOnExecAll()
	for _, fd := range fds {
		unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0)
	}
	err = syscall.Exec(exe, []string{exe, "-server", "-resume", path}, serverEnv())
	for _, fd := range fds {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.Unlock()
	s.goOn()
	return fmt.Errorf("rex: running %s: %w", exe, err)
}

// resumeListener takes over the listener and the lock of a handoff.
func resumeListener(h handoff) (net.Listener, *os.File, error) {
	unix.CloseOnExec(h.Lock)
	lock := os.NewFile(uintptr(h.Lock), "server.lock")
	lf := os.NewFile(uintptr(h.Listener), "server.sock")
	ln, err := net.FileListener(lf)
	lf.Close()
	if err != nil {
		lock.Close()
		return nil, nil, fmt.Errorf("rex: resuming the listener: %w", err)
	}
	return ln, lock, nil
}

// tookOver tells the server that handed over that this one serves: an
// exec needs no telling.
func tookOver(handoff) {}

// closeHandedOver closes what a session lost in a handoff kept open.
func closeHandedOver(st sessionState) {
	if st.Master >= 0 {
		unix.Close(st.Master)
	}
}

// descriptor returns the descriptor of f, leaving it non-blocking, or -1.
func descriptor(f *os.File) int {
	n := -1
	control(f, func(fd int) error {
		n = fd
		return nil
	})
	return n
}

// stopRead stops read, leaving what the terminal has in the kernel, and
// returns once reading, of the read goroutine, closes.
func (p *pty) stopRead(reading <-chan struct{}) error {
	if p.master != nil {
		if err := p.master.SetReadDeadline(time.Unix(1, 0)); err != nil && !errors.Is(err, os.ErrClosed) {
			return err
		}
	}
	<-reading
	return nil
}

// readAgain undoes stopRead.
func (p *pty) readAgain() { p.master.SetReadDeadline(time.Time{}) }

// handoff puts the terminal's descriptor in st, false when it has none.
func (p *pty) handoff(st *sessionState) bool {
	st.Master = descriptor(p.master)
	return st.Master >= 0
}
