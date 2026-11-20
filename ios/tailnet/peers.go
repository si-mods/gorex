package tailnet

import (
	"context"
	"sync"

	"tailscale.com/ipn/ipnstate"
)

// peerCache keeps the device's status, its peers with it, for the
// proxy's dials, so that a dial does not take a status of every peer
// (dialTailnet). A watch (tailnode.Node.WatchPeers) tells it each change
// that could make the status wrong: it forgets the status then, and the
// next dial asks anew. While the watch is down it keeps nothing.
type peerCache struct {
	mu       sync.Mutex
	watching bool
	gen      uint64 // counts the changes told
	st       *ipnstate.Status
}

// changed is the watch's callback.
func (c *peerCache) changed(watching bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watching = watching
	c.gen++
	c.st = nil
}

// status is the status kept, and true, or else fetch's. fetch's status is
// kept while the watch watches and told no change since the fetch began.
// A nil cache keeps nothing.
func (c *peerCache) status(ctx context.Context, fetch func(context.Context) (*ipnstate.Status, error)) (*ipnstate.Status, bool, error) {
	if c == nil {
		st, err := fetch(ctx)
		return st, false, err
	}
	c.mu.Lock()
	st, gen := c.st, c.gen
	c.mu.Unlock()
	if st != nil {
		return st, true, nil
	}
	return c.fetch(ctx, gen, fetch)
}

// fresh is fetch's status, kept as status keeps it.
func (c *peerCache) fresh(ctx context.Context, fetch func(context.Context) (*ipnstate.Status, error)) (*ipnstate.Status, error) {
	if c == nil {
		return fetch(ctx)
	}
	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()
	st, _, err := c.fetch(ctx, gen, fetch)
	return st, err
}

func (c *peerCache) fetch(ctx context.Context, gen uint64, fetch func(context.Context) (*ipnstate.Status, error)) (*ipnstate.Status, bool, error) {
	st, err := fetch(ctx)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	if c.watching && c.gen == gen {
		c.st = st
	}
	c.mu.Unlock()
	return st, false, nil
}
