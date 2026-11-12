//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMain runs the test binary as a server when Spawn or an upgrade
// starts it so, as main does the app's.
func TestMain(m *testing.M) {
	// A copy named so fails to take over, as a build that cannot would.
	if len(os.Args) > 2 && os.Args[2] == "-resume" && strings.Contains(filepath.Base(os.Args[0]), "no-resume") {
		os.Exit(1)
	}
	if server, err := ServerMain(os.Args[1:]); server {
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// readUntil reads s until what it read matches re, for a few seconds.
func readUntil(t *testing.T, s *Stream, re string) string {
	t.Helper()
	var out bytes.Buffer
	buf := make([]byte, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !regexp.MustCompile(re).Match(out.Bytes()) {
			n, err := s.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.Close()
		<-done
	}
	if !regexp.MustCompile(re).Match(out.Bytes()) {
		t.Fatalf("no %q in %q", re, out.String())
	}
	return out.String()
}

func copyFile(from, to string) error {
	r, err := os.Open(from)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func TestServer(t *testing.T) {
	// A short path, for the socket: macOS's temporary directory is long.
	base, shell, work := "/tmp", "/bin/sh", "/tmp"
	// The command prints "hello" without the line typed showing it, and
	// runs a program for a while.
	typed, sleeper := "echo he''llo; sleep 1\n", "sleep"
	if runtime.GOOS == "windows" {
		base, shell, work = "", "cmd.exe", os.TempDir()
		typed, sleeper = "echo he^llo & ping -n 3 127.0.0.1 >nul\r", "ping"
	}
	dir, err := os.MkdirTemp(base, "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	go Serve()
	var c *Client
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(dir, "server.sock")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err = Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	h, err := c.Hello()
	if err != nil || h.Version != ProtocolVersion {
		t.Fatalf("hello %+v %v", h, err)
	}
	t.Logf("host %+v", h.Host)
	info, err := c.Create(CreateOptions{Command: []string{shell}, Dir: work, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Stream(info.ID, 80, 24)
	s.Resize(100, 30)
	s.Write([]byte(typed))
	var out bytes.Buffer
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "hello") && time.Now().Before(deadline) {
		n, err := s.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("output %q", out.String())
	}
	time.Sleep(200 * time.Millisecond)
	list, err := c.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	in := list[0]
	t.Logf("info %+v", in)
	if !strings.EqualFold(in.Program, sleeper) || in.Idle || in.Cols != 100 {
		t.Errorf("info %+v", in)
	}
	if !strings.HasSuffix(strings.ToLower(in.Dir), strings.ToLower(work)) {
		t.Errorf("dir %q", in.Dir)
	}
	s.Close()
	// Attaching again replays what it printed.
	s2 := c.Stream(info.ID, 100, 30)
	s2.Resize(100, 30)
	n, _ := s2.Read(buf)
	if !strings.Contains(string(buf[:n]), "hello") {
		t.Errorf("replay %q", buf[:n])
	}
	if err := c.Kill(info.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.List(); len(list) != 0 {
		t.Errorf("sessions left %+v", list)
	}
}
