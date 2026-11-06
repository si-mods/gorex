//go:build darwin || linux

package rex

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestMain runs the test binary as a server when Spawn or an upgrade
// starts it so, as main does the app's.
func TestMain(m *testing.M) {
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

// TestUpgrade upgrades a server, of a process of its own, to another
// executable: the session, its shell and what it printed stay.
func TestUpgrade(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	// The server is this test binary, started by Spawn.
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil || !h.Upgrade {
		t.Fatalf("hello %+v %v", h, err)
	}
	defer func() {
		c.Shutdown()
		c.Close()
		time.Sleep(100 * time.Millisecond)
		if processAlive(h.PID) {
			syscall.Kill(h.PID, syscall.SIGKILL)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "server.log")); err == nil && t.Failed() {
			t.Logf("server.log:\n%s", b)
		}
	}()
	// An app runs, so the sessions stay headless: one of a pane that
	// exits would go with its pane.
	app, err := ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	go app.TakeCommand()
	time.Sleep(50 * time.Millisecond)
	app.Hello() // answered after the take, which the server reads first
	info, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("X=42; echo before-''upgrade; sleep 1000 & echo sleeper=$!\n"))
	out := readUntil(t, s, `sleeper=(\d+)`)
	sleeper, _ := strconv.Atoi(regexp.MustCompile(`sleeper=(\d+)`).FindStringSubmatch(out)[1])
	defer syscall.Kill(sleeper, syscall.SIGKILL)
	s.Close()
	// A session that exited keeps what it showed last.
	done, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", "printf '\\033]2;last-title\\007'; echo last-words; exit 5"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if l, _ := c.List(); len(l) == 2 && l[1].Exited {
			break
		}
	}

	// An executable that does not run, or does not read this server's
	// handoffs, as an older build, leaves the server as it was.
	bad := filepath.Join(dir, "not-executable")
	os.WriteFile(bad, []byte("nothing"), 0o600)
	older := filepath.Join(dir, "older")
	os.WriteFile(older, []byte("#!/bin/sh\necho 0\n"), 0o700)
	for _, exe := range []string{bad, older} {
		if _, err := c.Upgrade(exe); err == nil {
			t.Fatalf("upgraded to %s", exe)
		}
	}
	s = c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("echo still-''here\n"))
	readUntil(t, s, `still-here`)
	s.Close()

	// A copy of the test binary is another executable.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "rex-upgraded.test")
	if err := copyFile(self, exe); err != nil {
		t.Fatal(err)
	}
	c2, err := c.Upgrade(exe)
	if err != nil {
		t.Fatal(err)
	}
	c = c2
	h2, err := c.Hello()
	wantExe, wantTime := executable(exe)
	if err != nil || h2.PID != h.PID || h2.Exe != wantExe || !h2.ExeTime.Equal(wantTime) || h2.Exe == h.Exe {
		t.Fatalf("hello after upgrade %+v %v, before %+v", h2, err, h)
	}
	list, err := c.List()
	if err != nil || len(list) != 2 || list[0].ID != info.ID || list[0].PID != info.PID || list[0].Exited {
		t.Fatalf("list %+v %v, created %+v", list, err, info)
	}
	if in := list[1]; in.ID != done.ID || !in.Exited || in.ExitCode != 5 || in.Title != "last-title" {
		t.Fatalf("exited session %+v", in)
	}
	s = c.Stream(done.ID, 80, 24)
	s.Resize(80, 24)
	readUntil(t, s, `last-words`)
	s.Close()
	if !processAlive(info.PID) || !processAlive(sleeper) {
		t.Fatal("the shell or its job is gone")
	}
	s = c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	readUntil(t, s, `before-upgrade`)
	s.Write([]byte("echo x$X\n"))
	readUntil(t, s, `x42`)
	s.Write([]byte("exit 3\n"))
	var in SessionInfo
	for deadline := time.Now().Add(5 * time.Second); !in.Exited && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if list, err := c.List(); err == nil && len(list) == 2 {
			in = list[0]
		}
	}
	if !in.Exited || in.ExitCode != 3 {
		t.Fatalf("after exit %+v", in)
	}
	s.Close()
}

// TestUpgradeKeepsAppliedLayout makes a tab with no window, upgrades the
// server, and finds the tab, its session and the layout's revision as they
// were.
func TestUpgradeKeepsAppliedLayout(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c.Shutdown()
		c.Close()
		time.Sleep(100 * time.Millisecond)
		if processAlive(h.PID) {
			syscall.Kill(h.PID, syscall.SIGKILL)
		}
	}()
	res, err := c.Command(LayoutCommand{Op: "tab.create", Dir: "/tmp", Command: []string{"/bin/sh"}, Name: "kept"})
	if err != nil || res.Code != "" || res.New == "" {
		t.Fatalf("tab.create: %+v %v", res, err)
	}
	before, err := c.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "rex-upgraded.test")
	if err := copyFile(self, exe); err != nil {
		t.Fatal(err)
	}
	if c, err = c.Upgrade(exe); err != nil {
		t.Fatal(err)
	}
	after, err := c.LayoutState()
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Layout, before.Layout) {
		t.Fatalf("after the upgrade %d %s %v, before %d %s", after.Revision, after.Layout, err, before.Revision, before.Layout)
	}
	if !bytes.Contains(after.Layout, []byte(res.New)) || !bytes.Contains(after.Layout, []byte(`"kept"`)) {
		t.Errorf("the tab is gone: %s", after.Layout)
	}
	if list, err := c.List(); err != nil || len(list) != 1 || list[0].ID != res.New || list[0].Exited {
		t.Errorf("sessions %+v %v", list, err)
	}
	// The server goes on making changes after it.
	if res, err := c.Command(LayoutCommand{Op: "pane.split", SID: res.New, Command: []string{"/bin/sh"}}); err != nil || res.Code != "" {
		t.Errorf("split after the upgrade: %+v %v", res, err)
	}
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
