//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPOnlyLocal checks that only a local connection reaches the HTTP
// handler Rex's API is served by: a remote peer must not.
func TestHTTPOnlyLocal(t *testing.T) {
	var calls atomic.Int32
	var peers atomic.Value
	SetHTTPServe(func(conn net.Conn, r *bufio.Reader, peer string) {
		calls.Add(1)
		peers.Store(peer)
		r.ReadString('\n')
		conn.Write([]byte("HTTP/1.1 204 No Content\r\n\r\n"))
		conn.Close()
	})
	t.Cleanup(func() { SetHTTPServe(nil) })
	s := newServer(nil, nil)

	local, client := net.Pipe()
	go s.handle(local, "")
	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("GET /api/control HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	io.ReadAll(client)
	client.Close()
	if calls.Load() != 1 || peers.Load() != "" {
		t.Fatalf("a local connection: %d calls, peer %q", calls.Load(), peers.Load())
	}

	remote, rc := net.Pipe()
	go s.handle(remote, "nodekey:remote")
	rc.SetDeadline(time.Now().Add(2 * time.Second))
	rc.Write([]byte("GET /api/control HTTP/1.1\r\nHost: x\r\n\r\n"))
	io.ReadAll(rc)
	rc.Close()
	if calls.Load() != 1 {
		t.Fatalf("a remote connection reached the handler: %d calls", calls.Load())
	}
}

// TestHTTPServesOnSocket checks that a connection to the server's socket
// that asks for HTTP is served, while a JSON client still works.
func TestHTTPServesOnSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rexhttp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	SetHTTPServe(func(conn net.Conn, r *bufio.Reader, peer string) {
		r.ReadString('\n')
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
		conn.Close()
	})
	t.Cleanup(func() { SetHTTPServe(nil) })
	go Serve()
	for range 100 {
		if _, err := os.Stat(SocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("GET /api/help HTTP/1.1\r\nHost: x\r\n\r\n"))
	body, _ := io.ReadAll(conn)
	conn.Close()
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("the HTTP answer: %q", body)
	}
	// A client of the server's own protocol is unaffected.
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Hello(); err != nil {
		t.Fatalf("hello: %v", err)
	}
	c.Shutdown()
	time.Sleep(100 * time.Millisecond)
}
