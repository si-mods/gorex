//go:build linux

package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// sessionReadCmd is the helper that reads as sp.user (sessionReadFlag):
// op ("open" or "list") of r, which it reads on its input. With no user,
// as in tests, it runs as the server's.
func sessionReadCmd(ctx context.Context, sp spawner, op string, r sessionRead) *exec.Cmd {
	cmd := exec.CommandContext(ctx, sp.exe, sessionReadFlag, op)
	in, _ := json.Marshal(r)
	cmd.Stdin = bytes.NewReader(in)
	// The server's environment is not the user's to read.
	cmd.Dir, cmd.Env = "/", []string{"PATH=/usr/bin:/bin", "HOME=" + sp.home()}
	if u := sp.user; u != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: u.uid, Gid: u.gid, Groups: u.groups}}
	}
	return cmd
}

// helperError is the error of a helper that answered nothing: how it
// ended, and what it wrote to its standard error.
func helperError(err error, stderr *bytes.Buffer) error {
	msg := strings.TrimSpace(stderr.String())
	if err == nil {
		err = errors.New("no answer")
	}
	if msg != "" {
		return fmt.Errorf("rex: reading as the session user: %v: %s", err, msg)
	}
	return fmt.Errorf("rex: reading as the session user: %w", err)
}

// openAsUserHelper opens path, as openToSend does, in the helper running
// as sp.user, which hands the server the descriptor it opened.
func openAsUserHelper(sp spawner, path string) (*os.File, fs.FileInfo, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	defer unix.Close(fds[0])
	theirs := os.NewFile(uintptr(fds[1]), "session-read")
	cmd := sessionReadCmd(context.Background(), sp, "open", sessionRead{Path: path})
	cmd.ExtraFiles = []*os.File{theirs} // descriptor 3
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	closeOnExecAll()
	err = cmd.Start()
	// The helper holds its own end: once it exits, a read here ends.
	theirs.Close()
	if err != nil {
		return nil, nil, helperError(err, &stderr)
	}
	f, msg, rerr := receiveOpened(fds[0], path)
	werr := cmd.Wait()
	switch {
	case f != nil:
		st, err := f.Stat()
		if err == nil {
			err = checkRegular(path, st)
		}
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		return f, st, nil
	case msg != "":
		return nil, nil, errors.New(msg)
	case rerr != nil:
		return nil, nil, rerr
	}
	return nil, nil, helperError(werr, &stderr)
}

// receiveOpened reads the helper's answer on fd: the file it opened, or
// the text of its error; neither when it ended without one.
func receiveOpened(fd int, path string) (*os.File, string, error) {
	// An error names the path, which may be as long as the system allows.
	buf, oob := make([]byte, 64<<10), make([]byte, unix.CmsgSpace(4))
	var n, oobn, flags int
	var err error
	for {
		n, oobn, flags, _, err = unix.Recvmsg(fd, buf, oob, unix.MSG_CMSG_CLOEXEC)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		return nil, "", err
	}
	var files []int
	if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil {
		for _, m := range msgs {
			if rights, err := unix.ParseUnixRights(&m); err == nil {
				files = append(files, rights...)
			}
		}
	}
	if len(files) == 0 {
		// The system drops a descriptor it cannot give, as one past the
		// server's limit, and says so.
		if flags&unix.MSG_CTRUNC != 0 || string(buf[:n]) == "ok" {
			return nil, "", fmt.Errorf("rex: %s: the descriptor opened as the session user was lost", path)
		}
		return nil, string(buf[:n]), nil
	}
	for _, extra := range files[1:] {
		unix.Close(extra)
	}
	return os.NewFile(uintptr(files[0]), path), "", nil
}

// sendOpened opens path as openToSend does and hands it to the server on
// descriptor 3, or the text of the error.
func sendOpened(path string) error {
	const sock = 3
	f, _, err := openToSend(path)
	if err != nil {
		return unix.Sendmsg(sock, []byte(err.Error()), nil, nil, 0)
	}
	defer f.Close()
	return control(f, func(fd int) error {
		return unix.Sendmsg(sock, []byte("ok"), unix.UnixRights(fd), nil, 0)
	})
}

// listDirAsUserHelper lists path, as listDir does, in the helper running
// as sp.user. A helper that hangs past the listing's deadline is killed.
func listDirAsUserHelper(sp spawner, home, path string, hidden bool) (DirListResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dirListTimeout.get()+5*time.Second)
	defer cancel()
	cmd := sessionReadCmd(ctx, sp, "list", sessionRead{Path: path, Home: home, Hidden: hidden})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	closeOnExecAll()
	err := cmd.Run()
	var a listAnswer
	if jerr := json.Unmarshal(stdout.Bytes(), &a); jerr != nil {
		return DirListResult{}, helperError(err, &stderr)
	}
	if a.Error != "" {
		return DirListResult{}, errors.New(a.Error)
	}
	return a.Result, nil
}

// mkdirAsUserHelper makes the directory path as sp.user, in the helper.
func mkdirAsUserHelper(sp spawner, home, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dirListTimeout.get()+5*time.Second)
	defer cancel()
	cmd := sessionReadCmd(ctx, sp, "mkdir", sessionRead{Path: path, Home: home})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	closeOnExecAll()
	err := cmd.Run()
	var a listAnswer
	if jerr := json.Unmarshal(stdout.Bytes(), &a); jerr != nil {
		return helperError(err, &stderr)
	}
	if a.Error != "" {
		return errors.New(a.Error)
	}
	return nil
}
