package rexapi

import (
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// TestRelayForwardsLoopback: a relay answers on the host address and
// forwards to the loopback port, and closes when asked.
func TestRelayForwardsLoopback(t *testing.T) {
	f := startFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello relay")
	}))
	port := ln.Addr().(*net.TCPAddr).Port
	r, err := f.g.openRelay("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(r.url())
	if err != nil {
		t.Fatalf("get %s: %v", r.url(), err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello relay" {
		t.Fatalf("body = %q", body)
	}
	f.g.closeRelayTo("127.0.0.1", port)
	if resp, err := http.Get(r.url()); err == nil {
		resp.Body.Close()
		t.Fatal("the relay still answers")
	}
}

// TestRelayReusesAndExpires: the same port gets the same relay, and an
// idle relay closes by itself.
func TestRelayReusesAndExpires(t *testing.T) {
	f := startFixture(t)
	f.g.relayIdle = 60 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	first, err := f.g.openRelay("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.g.openRelay("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	if first != again || first.url() != again.url() {
		t.Fatalf("two relays: %s and %s", first.url(), again.url())
	}
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(first.url())
		if err != nil {
			return
		}
		resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the idle relay did not close")
}

// TestPortOpenRelaysAListedLoopbackPort: the method checks the pane's
// ports and opens a relay for one of them.
func TestPortOpenRelaysAListedLoopbackPort(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 for the test's listener")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{
		"python3", "-m", "http.server", "0", "--bind", "127.0.0.1",
	}})
	var port int
	deadline := time.Now().Add(15 * time.Second)
	for port == 0 && time.Now().Before(deadline) {
		r := result(t, c.request("p", "com.superlogical.terminal.ports", map[string]any{
			"session_id": sid, "block_id": block, "args": map[string]any{},
		}))
		if list, _ := r["ports"].([]any); len(list) > 0 {
			port = int(list[0].(map[string]any)["port"].(float64))
		} else {
			time.Sleep(300 * time.Millisecond)
		}
	}
	if port == 0 {
		t.Fatal("the pane did not listen")
	}
	r := result(t, c.request("o", "gorex.port.open", map[string]any{
		"session_id": sid, "block_id": block, "port": port,
	}))
	url, _ := r["url"].(string)
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("url = %q", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Directory listing") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	callChecked(t, c, "gorex.port.close", map[string]any{"session_id": sid, "block_id": block, "port": port})
	if resp, err := http.Get(url); err == nil {
		resp.Body.Close()
		t.Fatal("the relay still answers after close")
	}
	if e := failure(t, c.request("bad", "gorex.port.open", map[string]any{
		"session_id": sid, "block_id": block, "port": 1,
	})); e["code"] != "not_found" {
		t.Fatalf("a port that is not listed: %v", e)
	}
}
