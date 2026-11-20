//go:build darwin || linux || windows

package rex

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestPortScanFindsListener: a listener of this process is in the scan.
func TestPortScanFindsListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	want := ln.Addr().(*net.TCPAddr).Port
	socks, ok := portListeners([]int{os.Getpid()})
	if !ok {
		t.Fatalf("portListeners not ok: %v", socks)
	}
	for _, sk := range socks {
		if sk.Port == want && sk.Address == "127.0.0.1" && sk.PID == os.Getpid() {
			return
		}
	}
	t.Fatalf("listener %d not in %v", want, socks)
}

// TestPortScanFindsChild: the scan walks the process tree.
func TestPortScanFindsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's child program is a Unix one")
	}
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	pids, ok := portPIDs(os.Getpid())
	if !ok {
		t.Fatalf("portPIDs not ok: %v", pids)
	}
	for _, pid := range pids {
		if pid == cmd.Process.Pid {
			return
		}
	}
	t.Fatalf("child %d not in %v", cmd.Process.Pid, pids)
}

// TestDedupeSockets: one socket held through two descriptors is one
// entry.
func TestDedupeSockets(t *testing.T) {
	in := []portSocket{{PID: 1, Port: 2, Address: "b"}, {PID: 1, Port: 2, Address: "a"}, {PID: 1, Port: 2, Address: "a"}}
	out := dedupeSockets(in)
	if len(out) != 2 || out[0].Address != "a" || out[1].Address != "b" {
		t.Fatalf("dedupe = %v", out)
	}
}

// TestPortProcessNames keeps the process fields Rex's clients read.
func TestPortProcessNames(t *testing.T) {
	p, ok := portProcess(os.Getpid(), "test")
	if !ok {
		t.Fatal("this process is not inspectable")
	}
	if p.PID != os.Getpid() || p.Name == "" || p.User != "test" {
		t.Fatalf("process = %+v", p)
	}
	if p.Path == "" && p.Cwd == "" {
		t.Fatalf("no path and no directory: %+v", p)
	}
}

// TestPortsEvent: a terminal that starts a listener sends a ports event
// with the port, and the client's subscription receives the whole list.
func TestPortsEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's listener program is a Unix one")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 for the test's listener")
	}
	s, _ := testServer(t)
	s.portsClock = &portsTiming{
		every: 20 * time.Millisecond, min: 10 * time.Millisecond,
		active: 50 * time.Millisecond, idle: 50 * time.Millisecond,
	}
	go s.portsLoop()
	t.Cleanup(func() { s.quitOnce.Do(func() { close(s.quit) }) })
	c := pipe(s, "")
	defer c.Close()
	if _, err := c.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(); err != nil {
		t.Fatal(err)
	}
	in, err := c.Create(CreateOptions{
		Command: []string{"python3", "-m", "http.server", "0", "--bind", "127.0.0.1"},
		Dir:     "/tmp", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind != "ports" {
				continue
			}
			for _, pe := range ev.Ports {
				if pe.SessionID == in.ID && len(pe.Ports.Ports) > 0 {
					return
				}
			}
		case <-deadline:
			t.Fatal("no ports event")
		}
	}
}
