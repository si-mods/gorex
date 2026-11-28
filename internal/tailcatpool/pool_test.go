package tailcatpool

import (
	"errors"
	"net"
	"testing"
	"time"

	"tailscale.com/types/key"
)

type halfCloseConn struct {
	net.Conn
	readClosed  bool
	writeClosed bool
}

func (c *halfCloseConn) CloseRead() error {
	c.readClosed = true
	return nil
}

func (c *halfCloseConn) CloseWrite() error {
	c.writeClosed = true
	return nil
}

func TestPoolLifecycleAndHalfClose(t *testing.T) {
	var p Pool
	p.SetIdleTimeout(20 * time.Millisecond)

	k := key.NewNode()
	e, used, _ := p.acquire("addr-1", k)
	if used {
		t.Fatal("fresh entry should not be marked used")
	}
	if p.Len() != 1 || p.Get("addr-1") != e {
		t.Fatalf("Len=%d Get=%v, want 1 and %v", p.Len(), p.Get("addr-1"), e)
	}

	// MarkCold / Evict / KeepOnly / CloseAll semantics.
	p.mu.Lock()
	e.used = true
	p.mu.Unlock()
	p.MarkCold("addr-1", e)
	p.mu.Lock()
	if e.used {
		t.Fatal("MarkCold did not clear used")
	}
	p.mu.Unlock()

	a, b := net.Pipe()
	defer b.Close()
	hc := &halfCloseConn{Conn: a}
	c := &Conn{Conn: hc, pool: &p, entry: e, addr: "addr-1"}
	if c.Entry() != e {
		t.Fatal("Entry() mismatch")
	}
	if err := c.CloseRead(); err != nil || !hc.readClosed {
		t.Fatalf("CloseRead err=%v readClosed=%v", err, hc.readClosed)
	}
	if err := c.CloseWrite(); err != nil || !hc.writeClosed {
		t.Fatalf("CloseWrite err=%v writeClosed=%v", err, hc.writeClosed)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Plain net.Pipe does not implement HalfCloser -> ErrUnsupported.
	plain := &Conn{Conn: b, pool: &p, entry: e, addr: "addr-1"}
	if !errors.Is(plain.CloseRead(), errors.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for CloseRead")
	}
	if !errors.Is(plain.CloseWrite(), errors.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for CloseWrite")
	}

	// Idle timer evicts the entry.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && p.Len() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if p.Len() != 0 {
		t.Fatalf("expected idle eviction, Len=%d", p.Len())
	}
}

func TestPoolKeepOnlyAndCloseAll(t *testing.T) {
	var p Pool
	k := key.NewNode()
	e1, _, _ := p.acquire("a", k)
	e2, _, _ := p.acquire("b", k)
	if snap := p.Snapshot(); len(snap) != 2 {
		t.Fatalf("Snapshot len=%d, want 2", len(snap))
	}
	p.KeepOnly(map[string]bool{"a": true})
	if p.Get("a") != e1 || p.Get("b") != nil {
		t.Fatalf("KeepOnly failed: a=%v b=%v", p.Get("a"), p.Get("b"))
	}
	p.Evict("a", e1)
	if p.Len() != 0 || !e1.dead || !e2.dead {
		t.Fatalf("Evict failed: len=%d e1.dead=%v e2.dead=%v", p.Len(), e1.dead, e2.dead)
	}
	_, _, _ = p.acquire("c", k)
	p.CloseAll()
	if p.Len() != 0 {
		t.Fatalf("CloseAll left %d entries", p.Len())
	}
}
