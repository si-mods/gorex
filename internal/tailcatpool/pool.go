// Package tailcatpool manages a shared pool of tailcat.Client instances keyed
// by server address, one client per machine. Both the desktop/server remote
// dialer (internal/rex) and the iOS SOCKS proxy (ios/tailnet) use this pool so
// connection reuse, stale-client retirement, idle eviction, and half-close
// propagation behave identically everywhere.
package tailcatpool

import (
	"context"
	"errors"
	"log"
	"maps"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

const (
	// DefaultIdleTimeout is how long a pooled client with no open connections
	// stays alive before closing.
	DefaultIdleTimeout = 3 * time.Minute
	// DefaultTryTimeout bounds a dial attempt on a previously-used client so a
	// restarted server is detected quickly and retired.
	DefaultTryTimeout = 3 * time.Second
)

// Pool holds one *tailcat.Client per server address. Two clients with the same
// key steal each other's packets (#212), so concurrent dials and connections to
// the same address share a single client.
type Pool struct {
	// LogPrefix prefixes reconnect log lines (e.g. "rex" or "tailcat").
	LogPrefix string
	// KeyFn, when non-nil, supplies the active client key when retiring a stale
	// client. Returning ok=false stops retirement (e.g. after key cleared).
	KeyFn func() (key.NodePrivate, bool)
	// OnDialing, when non-nil, is called right after a dial registers on its
	// pooled entry (used by tests to synchronize concurrent dials).
	OnDialing func()
	// Tries counts dial attempts against previously-used clients (read by tests).
	Tries atomic.Int64

	mu          sync.Mutex
	clients     map[string]*Entry
	idleTimeout time.Duration
	tryTimeout  time.Duration
}

// Entry is a pooled tailcat.Client for one server address.
type Entry struct {
	tc    *tailcat.Client
	conns int  // open, or being dialed
	used  bool // connected at least once since creation or MarkCold
	dead  bool // removed from pool and closed (retire, drop, evict)
	idle  *time.Timer
}

// Client returns the underlying *tailcat.Client.
func (e *Entry) Client() *tailcat.Client { return e.tc }

// SetIdleTimeout overrides DefaultIdleTimeout (0 restores default).
func (p *Pool) SetIdleTimeout(d time.Duration) {
	p.mu.Lock()
	p.idleTimeout = d
	p.mu.Unlock()
}

// SetTryTimeout overrides DefaultTryTimeout (0 restores default).
func (p *Pool) SetTryTimeout(d time.Duration) {
	p.mu.Lock()
	p.tryTimeout = d
	p.mu.Unlock()
}

func (p *Pool) idleDur() time.Duration {
	if p.idleTimeout > 0 {
		return p.idleTimeout
	}
	return DefaultIdleTimeout
}

func (p *Pool) tryDur() time.Duration {
	p.mu.Lock()
	d := p.tryTimeout
	p.mu.Unlock()
	if d > 0 {
		return d
	}
	return DefaultTryTimeout
}

// Dial connects to port at addr using the pooled client for addr. A client that
// connected before and fails now may have a server that restarted and forgot
// it, so a new client replaces it (retire). A dial whose caller canceled ctx
// does not drop a healthy client.
func (p *Pool) Dial(ctx context.Context, addr string, port uint16, k key.NodePrivate) (net.Conn, error) {
	e, used := p.acquire(addr, k)
	if fn := p.OnDialing; fn != nil {
		fn()
	}

	var (
		c   net.Conn
		err error
	)
	if used {
		t0 := time.Now()
		var timedOut bool
		c, timedOut, err = p.try(ctx, e, port)
		var fresh *Entry
		if err != nil && ctx.Err() == nil {
			fresh = p.retire(addr, e, timedOut)
		}
		if fresh != nil {
			tried := time.Since(t0)
			p.release(addr, e)
			e = fresh
			c, err = e.tc.DialTCPPort(ctx, port)
			prefix := p.LogPrefix
			if prefix == "" {
				prefix = "tailcat"
			}
			status := "failed"
			if err == nil {
				status = "connected"
			}
			log.Printf("%s: the kept tailcat client's try failed after %v; a new client %s after %v more",
				prefix, tried.Round(time.Millisecond), status, (time.Since(t0) - tried).Round(time.Millisecond))
		}
	} else {
		c, err = e.tc.DialTCPPort(ctx, port)
	}
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) && p.drop(addr, e) {
			go e.tc.Close()
		}
		p.release(addr, e)
		return nil, err
	}
	p.mu.Lock()
	e.used = true
	p.mu.Unlock()
	return &Conn{Conn: c, pool: p, entry: e, addr: addr}, nil
}

// WithClient borrows the pooled client for addr (warming the pool if absent),
// runs fn, marks the entry used on success or drops it on error, and releases
// it when fn returns.
func (p *Pool) WithClient(addr string, k key.NodePrivate, fn func(*tailcat.Client) error) error {
	e, _ := p.acquire(addr, k)
	defer p.release(addr, e)
	if err := fn(e.tc); err != nil {
		if p.drop(addr, e) {
			go e.tc.Close()
		}
		return err
	}
	p.mu.Lock()
	e.used = true
	p.mu.Unlock()
	return nil
}

// Snapshot returns a shallow copy of the currently pooled entries.
func (p *Pool) Snapshot() map[string]*Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.clients)
}

// Len returns the number of currently pooled clients.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

// Get returns the current pooled entry for addr, or nil if absent.
func (p *Pool) Get(addr string) *Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clients[addr]
}

// IsUsed reports whether e has connected at least once since creation or MarkCold.
func (p *Pool) IsUsed(e *Entry) bool {
	if e == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return e.used
}

// IsDead reports whether e has been retired, dropped, or evicted from the pool.
func (p *Pool) IsDead(e *Entry) bool {
	if e == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return e.dead
}

// MarkCold clears e's used flag if it is still the active entry for addr, so
// its next dial gets the full dial timeout instead of the short try timeout.
func (p *Pool) MarkCold(addr string, e *Entry) {
	p.mu.Lock()
	if p.clients[addr] == e {
		e.used = false
	}
	p.mu.Unlock()
}

// Evict removes e from the pool if it is still the active entry for addr, marks
// it dead, and closes its tailcat.Client asynchronously.
func (p *Pool) Evict(addr string, e *Entry) {
	p.mu.Lock()
	evicted := p.clients[addr] == e
	if evicted {
		delete(p.clients, addr)
		e.dead = true
	}
	p.mu.Unlock()
	if evicted {
		go e.tc.Close()
	}
}

// CloseAll removes every client from the pool and closes them asynchronously.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	old := p.clients
	p.clients = nil
	for _, e := range old {
		e.dead = true
		if e.idle != nil {
			e.idle.Stop()
			e.idle = nil
		}
	}
	p.mu.Unlock()
	for _, e := range old {
		go e.tc.Close()
	}
}

// KeepOnly removes and asynchronously closes every pooled client whose address
// is not present in addrs.
func (p *Pool) KeepOnly(addrs map[string]bool) {
	var old []*Entry
	p.mu.Lock()
	for a, e := range p.clients {
		if !addrs[a] {
			delete(p.clients, a)
			e.dead = true
			if e.idle != nil {
				e.idle.Stop()
				e.idle = nil
			}
			old = append(old, e)
		}
	}
	p.mu.Unlock()
	for _, e := range old {
		go e.tc.Close()
	}
}

func (p *Pool) acquire(addr string, k key.NodePrivate) (*Entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.clients[addr]
	if e == nil {
		e = p.newEntryLocked(addr, k)
	}
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	e.conns++
	return e, e.used
}

func (p *Pool) newEntryLocked(addr string, k key.NodePrivate) *Entry {
	if p.clients == nil {
		p.clients = map[string]*Entry{}
	}
	// Suppress tailcat's default logger because it logs keys.
	e := &Entry{tc: &tailcat.Client{Server: tailcat.Addr(addr), Key: k, Logf: func(string, ...any) {}}}
	p.clients[addr] = e
	return e
}

func (p *Pool) try(ctx context.Context, e *Entry, port uint16) (net.Conn, bool, error) {
	p.Tries.Add(1)
	tctx, cancel := context.WithTimeout(ctx, p.tryDur())
	defer cancel()
	c, err := e.tc.DialTCPPort(tctx, port)
	return c, err != nil && tctx.Err() != nil, err
}

// retire handles a failed try on e while the caller's dial context is still
// alive. e leaves the pool and closes when its server appears to have forgotten
// it (the try timed out, or no other connection uses e). It returns the
// replacement entry (already counted), or nil when e stays.
func (p *Pool) retire(addr string, e *Entry, timedOut bool) *Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !e.dead {
		if p.clients[addr] != e || !timedOut && e.conns != 1 {
			return nil
		}
		e.dead = true
		delete(p.clients, addr)
		go e.tc.Close()
	}
	k := e.tc.Key
	if p.KeyFn != nil {
		var ok bool
		k, ok = p.KeyFn()
		if !ok {
			return nil
		}
	}
	n := p.clients[addr]
	if n == nil {
		n = p.newEntryLocked(addr, k)
	}
	if n.idle != nil {
		n.idle.Stop()
		n.idle = nil
	}
	n.conns++
	return n
}

func (p *Pool) drop(addr string, e *Entry) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.conns != 1 || p.clients[addr] != e {
		return false
	}
	e.dead = true
	delete(p.clients, addr)
	return true
}

func (p *Pool) release(addr string, e *Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.conns--
	if e.conns > 0 || p.clients[addr] != e {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(p.idleDur(), func() {
		p.mu.Lock()
		idle := e.idle == t && e.conns == 0 && p.clients[addr] == e
		if idle {
			delete(p.clients, addr)
		}
		p.mu.Unlock()
		if idle {
			e.tc.Close()
		}
	})
	e.idle = t
}

// Conn is a net.Conn backed by a pooled tailcat.Client. Closing it decrements
// the entry's reference count and starts the idle eviction timer when unused.
type Conn struct {
	net.Conn
	pool  *Pool
	entry *Entry
	addr  string
	once  sync.Once
}

// Entry returns the pooled entry backing this connection.
func (c *Conn) Entry() *Entry { return c.entry }

func (c *Conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.pool.release(c.addr, c.entry) })
	return err
}

// CloseRead forwards half-close to the underlying conn so callers checking
// nettype.HalfCloser (such as tailscale.com/net/socks5) propagate EOF.
func (c *Conn) CloseRead() error {
	if hr, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return hr.CloseRead()
	}
	return errors.ErrUnsupported
}

// CloseWrite forwards half-close to the underlying conn.
func (c *Conn) CloseWrite() error {
	if hw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return hw.CloseWrite()
	}
	return errors.ErrUnsupported
}
