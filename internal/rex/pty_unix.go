//go:build darwin || linux

package rex

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// pty is a pseudo-terminal and the process it runs.
type pty struct {
	master *os.File
	// cmd started the process, proc; it is nil for a process the
	// executable before this one started (resumePTY).
	cmd  *exec.Cmd
	proc int
}

// tty is the name of the terminal's slave, as /dev/ttys008, "" when it
// cannot tell.
func (p *pty) tty() string {
	name := ""
	control(p.master, func(fd int) error {
		name = slaveName(fd)
		return nil
	})
	return name
}

// sessionArgs is the program and arguments startPTY starts a session of sp
// with: the helper that drops to sp.user, which runs sp.path once it has
// cleared the capabilities, or the program itself.
func sessionArgs(sp sessionSpawn) (path string, args []string) {
	if sp.user == nil {
		return sp.path, sp.argv
	}
	return sp.exe, append([]string{sp.exe, sessionExecFlag, sp.path}, sp.argv...)
}

// startPTY runs sp.argv (argv[0] as the program sees it, sp.path the file)
// in a new pseudo-terminal of sp.cols×sp.rows.
func startPTY(sp sessionSpawn) (*pty, error) {
	master, slaveName, err := openMaster()
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: opening %s: %w", slaveName, err)
	}
	defer slave.Close()
	if err := setSize(master, sp.cols, sp.rows); err != nil {
		master.Close()
		return nil, err
	}
	cmd := &exec.Cmd{Path: sp.path, Args: sp.argv, Dir: sp.dir, Env: sp.env, Stdin: slave, Stdout: slave, Stderr: slave}
	// A session of its own whose controlling terminal is the slave, so that
	// the terminal's signals reach the process group in the foreground.
	attr := &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if sp.user != nil {
		// The terminal is the session user's: a program that opens it by
		// name, as a pager or tmux does, finds it theirs.
		if err := unix.Fchown(int(slave.Fd()), int(sp.user.uid), int(sp.user.gid)); err != nil {
			master.Close()
			return nil, fmt.Errorf("pty: %s: %w", slaveName, err)
		}
		// The program runs as sp.user: the helper starts as that user,
		// clears the capabilities the server's ambient set leaves on it,
		// then runs the program, which keeps the pid.
		cmd.Path, cmd.Args = sessionArgs(sp)
		attr.Credential = &syscall.Credential{Uid: sp.user.uid, Gid: sp.user.gid, Groups: sp.user.groups}
	}
	cmd.SysProcAttr = attr
	closeOnExecAll()
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return &pty{master: master, cmd: cmd, proc: cmd.Process.Pid}, nil
}

// resumePTY takes over the terminal of st.Master, a descriptor the
// executable before this one left open, and its process st.PID.
func resumePTY(st sessionState) (*pty, error) {
	master, pid := st.Master, st.PID
	// Non-blocking, the descriptor goes to the poller, where reads take a
	// deadline (pause).
	if err := unix.SetNonblock(master, true); err != nil {
		return nil, fmt.Errorf("pty: %w", err)
	}
	unix.CloseOnExec(master)
	return &pty{master: os.NewFile(uintptr(master), "/dev/ptmx"), proc: pid}, nil
}

// exitedPTY is the terminal of a session that exited before a handoff.
func exitedPTY(pid int) *pty { return &pty{proc: pid} }

func (p *pty) read(b []byte) (int, error)  { return p.master.Read(b) }
func (p *pty) write(b []byte) (int, error) { return p.master.Write(b) }

// close closes the terminal's master: read ends, once it read what the
// terminal had.
func (p *pty) close() error {
	if p.master == nil {
		return nil
	}
	return p.master.Close()
}

func (p *pty) resize(cols, rows int) error { return setSize(p.master, cols, rows) }

func (p *pty) pid() int { return p.proc }

// conpty tells that the console is a ConPTY, which drops the OSC that a
// terminal's paste event is; a pty is not.
func (p *pty) conpty() bool { return false }

// kill kills the process group of the session's process.
func (p *pty) kill() {
	if pid := p.pid(); pid > 0 {
		syscall.Kill(-pid, syscall.SIGKILL)
	}
}

func setSize(f *os.File, cols, rows int) error {
	ws := &unix.Winsize{Col: uint16(clamp(cols)), Row: uint16(clamp(rows))}
	return control(f, func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws) })
}

// control runs fn with the descriptor of f, which Fd would put in
// blocking mode, where reads take no deadline.
func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

func clamp(v int) int { return max(1, min(v, 0xFFFF)) }

// foreground returns the process group in the foreground of the terminal.
func (p *pty) foreground() int {
	if p.master == nil {
		return 0
	}
	pg := 0
	err := control(p.master, func(fd int) (err error) {
		pg, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err
	})
	if err != nil {
		return 0
	}
	return pg
}

// wait waits for the process and returns its exit code, 128 plus the
// signal's number for a process a signal ended.
func (p *pty) wait() int {
	if p.cmd == nil {
		// Still this process's child, which it waits for by its pid.
		var ws unix.WaitStatus
		_, err := unix.Wait4(p.proc, &ws, 0, nil)
		for err == unix.EINTR {
			_, err = unix.Wait4(p.proc, &ws, 0, nil)
		}
		switch {
		case err != nil:
			return -1
		case ws.Signaled():
			return 128 + int(ws.Signal())
		}
		return ws.ExitStatus()
	}
	err := p.cmd.Wait()
	if e, ok := errors.AsType[*exec.ExitError](err); ok {
		if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return e.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// hangup ends the session: its processes get SIGHUP, as when a terminal
// window closes.
func (p *pty) hangup() {
	if p.proc > 0 {
		syscall.Kill(-p.proc, syscall.SIGHUP)
		if pg := p.foreground(); pg > 0 {
			syscall.Kill(-pg, syscall.SIGHUP)
		}
	}
	p.close()
}

// shellCommand returns the program, its arguments and the shell's name
// for a session: the user's login shell when cmd is empty, shell when the
// session runs as another user, else $SHELL.
func shellCommand(cmd []string, shell string) (path string, argv []string, name string, err error) {
	if len(cmd) == 0 {
		sh := shell
		if sh == "" {
			sh = os.Getenv("SHELL")
		}
		if sh == "" {
			sh = defaultShell
		}
		// A login shell, as terminal apps start it: "-zsh".
		return sh, []string{"-" + filepath.Base(sh)}, filepath.Base(sh), nil
	}
	path, err = exec.LookPath(cmd[0])
	if err != nil {
		return "", nil, "", err
	}
	return path, cmd, filepath.Base(cmd[0]), nil
}

// processAlive reports whether a process runs: one that exited and is kept
// as a zombie until it is waited for does not, though killing it still
// works.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil && !zombie(pid)
}
