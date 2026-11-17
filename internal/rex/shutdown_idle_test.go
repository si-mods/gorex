package rex

import (
	"strings"
	"testing"
	"time"
)

// TestShutdownIfIdle: a server that runs a shell refuses an if-idle stop
// with BusyPrefix and keeps running; once the shell ended it stops.
func TestShutdownIfIdle(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Serve()
	}()
	var c *Client
	var err error
	for i := 0; i < 500; i++ {
		if c, err = ConnectSocket(SocketPath()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	ss, err := c.Create(CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = c.ShutdownIfIdle()
	if err == nil || !strings.Contains(err.Error(), BusyPrefix) {
		t.Fatalf("a server with a shell: %v", err)
	}
	select {
	case <-done:
		t.Fatal("a busy server stopped")
	case <-time.After(300 * time.Millisecond):
	}
	if err := c.Kill(ss.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.ShutdownIfIdle(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("an idle server did not stop")
	}
}
