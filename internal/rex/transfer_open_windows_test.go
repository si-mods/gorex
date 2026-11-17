//go:build windows

package rex

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// pipeForms are the ways to name the pipe name on this host.
func pipeForms(name string) []string {
	return []string{
		`\\.\pipe\` + name,
		`//./pipe/` + name,
		`\\.\PIPE\` + name,
		`\\?\pipe\` + name,
		`//?/pipe/` + name,
		`\??\pipe\` + name,
		`\\?\\pipe\` + name,
		`\\?\Global\pipe\` + name,
		`\\?\Global\GLOBALROOT\Device\NamedPipe\` + name,
		`\\?\GLOBALROOT\Device\NamedPipe\` + name,
		`\\localhost\pipe\` + name,
		`\\127.0.0.1\pipe\` + name,
		`\\localhost\\pipe\` + name,
		`\\localhost\pipe.\` + name,
		`\\?\UNC\localhost\pipe\` + name,
		`//localhost/PIPE/` + name,
		`//?/unc/localhost/pipe/` + name,
		`\\;LanmanRedirector\localhost\pipe\` + name,
		`\\localhost\IPC$\` + name,
		`\\localhost\ipc$\` + name,
		`\\localhost\ipc$.\` + name,
		`\\?\UNC\localhost\IPC$\` + name,
		`//127.0.0.1/IPC$/` + name,
		`\\[::1]\IPC$\` + name,
		`\\localhost\.\pipe\` + name,
		`\\localhost\..\pipe\` + name,
	}
}

var pipeSeq atomic.Int32

// livePipe makes a named pipe, and counts the connects to it.
func livePipe(t *testing.T) (name string, connects *atomic.Int32) {
	t.Helper()
	name = fmt.Sprintf("gorex-test-%d-%d", os.Getpid(), pipeSeq.Add(1))
	h, err := windows.CreateNamedPipe(windows.StringToUTF16Ptr(`\\.\pipe\`+name),
		windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	connects = new(atomic.Int32)
	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			// A client that connects and closes before this call gives
			// ERROR_PIPE_CONNECTED or ERROR_NO_DATA: a connect all the same.
			windows.ConnectNamedPipe(h, nil)
			if !stop.Load() {
				connects.Add(1)
			}
			windows.DisconnectNamedPipe(h)
		}
	}()
	// Closing the handle would wait on a connect in progress: set stop,
	// connect once to end the wait, wait for the goroutine, then close.
	t.Cleanup(func() {
		stop.Store(true)
		if f, err := os.Open(`\\.\pipe\` + name); err == nil {
			f.Close()
		}
		<-done
		windows.CloseHandle(h)
	})
	return name, connects
}

// TestPlainPath: drive and file share paths pass; every device, pipe,
// redirector and malformed form is refused.
func TestPlainPath(t *testing.T) {
	for _, p := range []string{
		`C:\Windows\win.ini`,
		`c:/Windows/win.ini`,
		`C:\`,
		`\\?\C:\Windows`,
		`\\localhost\c$\Windows\win.ini`,
		`//localhost/c$/Windows`,
		`\\?\UNC\localhost\c$\Windows`,
		`\\server\share\file.txt`,
		`\\server\share\`,
		`//server/share/pipe`,
		`\\server\pipes\x`,
		`C:\pipe\x`,
	} {
		if !plainPath(p) {
			t.Errorf("plainPath(%q) = false, want true", p)
		}
	}
	for _, p := range append(pipeForms("x"),
		`\\.\C:\Windows`, `\\?\Volume{1}\x`, `\\?\C:`, `\\server`, `\\server\`, `\\`, `//`,
		`\Windows`, `C:x`, `relative\x`, ``, `\\.\share\x`, `\\?\share\x`, `\\??\share\x`, `\\;x\share\x`) {
		if plainPath(p) {
			t.Errorf("plainPath(%q) = true, want false", p)
		}
	}
}

// TestOpenToSendRefusesPipePaths: a live pipe is not reached by any form of
// its name: openToSend refuses by name, before os.Stat (which connects),
// and dir.list's volumePath refuses it too, and the pipe sees no connect.
func TestOpenToSendRefusesPipePaths(t *testing.T) {
	name, connects := livePipe(t)

	// The pipe works, and an open connects: a missed refusal shows.
	f, err := os.Open(`\\.\pipe\` + name)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	time.Sleep(200 * time.Millisecond)
	if connects.Load() == 0 {
		t.Fatal("test pipe saw no connect from os.Open; the test cannot tell")
	}
	connects.Store(0)

	for _, p := range pipeForms(name) {
		done := make(chan error, 1)
		go func() {
			f, _, err := openToSend(p)
			if f != nil {
				f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if !NotRegular(err) {
				t.Errorf("openToSend(%q) = %v, want a not-regular refusal", p, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("openToSend(%q) blocked", p)
		}
		if _, ok := volumePath(p); ok {
			t.Errorf("volumePath(%q) accepted it", p)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := connects.Load(); n != 0 {
		t.Errorf("the pipe saw %d connects, want 0", n)
	}
}

// TestOpenToSendFileShareNotRefused: a file share on this host passes the
// guard; opening it fails later, not as a not-regular refusal.
func TestOpenToSendFileShareNotRefused(t *testing.T) {
	for _, p := range []string{`\\localhost\share\file.txt`, `\\localhost\c$\Windows\win.ini`, `\\?\UNC\localhost\c$\Windows\win.ini`} {
		f, _, err := openToSend(p)
		if f != nil {
			f.Close()
		}
		if NotRegular(err) {
			t.Errorf("openToSend(%q) refused as not regular: %v", p, err)
		}
	}
}
