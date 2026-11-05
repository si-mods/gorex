//go:build darwin || linux

package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// Spawn and Upgrade run this test binary as the app runs its server.
func TestMain(m *testing.M) {
	if server, err := rex.ServerMain(os.Args[1:]); server {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestUpgradeServerKeepsSessions(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gxup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOREX_DIR", dir)
	t.Cleanup(func() {
		if c, err := rex.ConnectExisting(); err == nil {
			c.Shutdown()
			c.Close()
		}
		os.RemoveAll(dir)
	})
	c, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hello, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	session, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "gorex-next")
	if err := copyUpgradeBuild(hello.Exe, exe); err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	at := hello.ExeTime.Add(time.Second)
	if err := os.Chtimes(exe, at, at); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := upgradeServer(c, hello, exe, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "terminal sessions kept") {
		t.Fatalf("output: %q", out.String())
	}
	next, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	got, err := next.Hello()
	if err != nil || got.PID != hello.PID || got.Exe != exe || !got.ExeTime.Equal(at) {
		t.Fatalf("server after upgrade: %+v, %v; before %+v", got, err, hello)
	}
	sessions, err := next.List()
	if err != nil || len(sessions) != 1 || sessions[0].ID != session.ID || sessions[0].PID != session.PID || sessions[0].Exited {
		t.Fatalf("sessions after upgrade: %+v, %v; before %+v", sessions, err, session)
	}
}

func copyUpgradeBuild(from, to string) error {
	r, err := os.Open(from)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}
