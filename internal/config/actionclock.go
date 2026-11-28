package config

import (
	"context"
	"sync"
	"time"
)

// ActionClock stops a config action when its running time passes, and pauses
// while a prompt/question is open, which does not count toward the limit
// (docs/MOD-009-client-prompts.md).
type ActionClock struct {
	mu        sync.Mutex
	timer     *time.Timer
	deadline  time.Time
	remaining time.Duration
	paused    bool
	stopped   bool
	expired   bool
	cancel    context.CancelFunc
}

// NewActionClock starts an action's running-time clock and returns it along
// with a context cancelled when the limit expires or Stop is called.
func NewActionClock(limit time.Duration) (*ActionClock, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &ActionClock{cancel: cancel, deadline: time.Now().Add(limit)}
	c.mu.Lock()
	c.armLocked(time.Until(c.deadline))
	c.mu.Unlock()
	return c, ctx
}

func (c *ActionClock) armLocked(d time.Duration) {
	if c.stopped {
		return
	}
	if d <= 0 {
		c.expired = true
		c.cancel()
		return
	}
	c.timer = time.AfterFunc(d, func() {
		c.mu.Lock()
		if c.paused || c.stopped {
			c.mu.Unlock()
			return
		}
		c.expired = true
		c.mu.Unlock()
		c.cancel()
	})
}

// Pause freezes the remaining time while a client prompt is open.
func (c *ActionClock) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused || c.stopped || c.expired {
		return
	}
	c.paused = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.remaining = time.Until(c.deadline)
}

// Resume restarts the countdown with whatever time remained at Pause.
func (c *ActionClock) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.paused || c.stopped || c.expired {
		return
	}
	c.paused = false
	c.deadline = time.Now().Add(c.remaining)
	c.armLocked(c.remaining)
}

// Stop ends the clock and cancels its context without marking it expired.
func (c *ActionClock) Stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	c.cancel()
}

// Expired reports whether the time limit elapsed before Stop was called.
func (c *ActionClock) Expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}
