package rex

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Windows has no exec: a server upgrades by starting the new executable
// as a process that inherits the consoles' handles, and exits once that
// one serves. A console lives as long as a handle of its signal pipe does,
// not as long as the process that made it (consoleSignal).

// badConsole tells that a console's signal pipe could not be read: the
// server does not upgrade, and restarts instead.
var badConsole atomic.Bool

func canUpgrade() bool { return !badConsole.Load() }

// upgradeWait is how long the new executable may take to serve.
var upgradeWait = 10 * time.Second

// handOver starts exe with the handoff h and exits once it serves. It
// returns only when that fails, the server serving again.
func (s *Server) handOver(exe string, h *handoff) error {
	var inherit []syscall.Handle
	for _, st := range h.Sessions {
		if st.Master >= 0 {
			c := st.Console
			inherit = append(inherit, syscall.Handle(st.Master), syscall.Handle(c.Out), syscall.Handle(c.Signal))
			if c.Process != 0 {
				inherit = append(inherit, syscall.Handle(c.Process))
			}
		}
	}
	// Copies of the sessions' handles (pty.handoff), which this process
	// closes as it exits.
	defer func() {
		for _, h := range inherit {
			syscall.CloseHandle(h)
		}
	}()
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	ready := windows.Handle(w.Fd())
	if err := windows.SetHandleInformation(ready, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		w.Close()
		return err
	}
	h.Ready = uintptr(ready)
	path := handoffPath()
	if err := writeJSON(path, h); err != nil {
		w.Close()
		return err
	}
	defer os.Remove(path)
	s.stopForUpgrade(exe)
	// The socket is exe's to listen on: a client that dials meanwhile
	// tries again.
	s.ln.Close()
	cmd := exec.Command(exe, "-server", "-resume", path)
	cmd.Env = serverEnv()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = detached()
	cmd.SysProcAttr.AdditionalInheritedHandles = append(inherit, syscall.Handle(ready))
	err = cmd.Start()
	w.Close()
	if err == nil {
		if err = waitServing(r); err == nil {
			// Without closing the consoles, which go on.
			log.Printf("rex: %s serves", exe)
			os.Exit(0)
		}
		cmd.Process.Kill()
		cmd.Wait()
	}
	if ln, lerr := listenSocket(); lerr != nil {
		log.Printf("rex: listening again: %v", lerr)
	} else {
		s.ln = ln
	}
	s.goOn()
	return fmt.Errorf("rex: running %s: %w", exe, err)
}

// waitServing waits for the new executable to write to the pipe of r:
// it serves.
func waitServing(r *os.File) error {
	done := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return errors.New("it exited before it served")
		}
		return nil
	case <-time.After(upgradeWait):
		return errors.New("it did not serve in time")
	}
}

// resumeListener listens on the socket the server that handed over
// closed. That server holds the lock until it exits, once this one
// serves; this one takes it then.
func resumeListener(handoff) (net.Listener, *os.File, error) {
	lock, err := os.OpenFile(filepath.Join(Dir(), "server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	ln, err := listenSocket()
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	go func() {
		for lockFile(lock) != nil {
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return ln, lock, nil
}

// tookOver tells the server that handed over that this one serves.
func tookOver(h handoff) {
	if h.Ready != 0 {
		f := os.NewFile(h.Ready, "ready")
		f.Write([]byte{1})
		f.Close()
	}
}

// closeHandedOver closes what a session lost in a handoff kept open.
func closeHandedOver(st sessionState) {
	if st.Master < 0 {
		return
	}
	windows.CloseHandle(windows.Handle(st.Master))
	if c := st.Console; c != nil {
		for _, h := range []uintptr{c.Out, c.Signal, c.Process} {
			if h != 0 {
				windows.CloseHandle(windows.Handle(h))
			}
		}
	}
}
