package main

import (
	"context"
	"sync"
	"time"
)

// actionClock stops a config action when its running time passes, and
// pauses while a question is open, which does not count
// (docs/MOD-009-client-prompts.md).
type actionClock struct {
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	// remaining is what the action has left while a question is open.
	remaining time.Duration
	paused    bool
	stopped   bool
	expired   bool
	cancel    context.CancelFunc
}

// newActionClock starts an action's time, which ends its context when it
// passes.
func newActionClock(limit time.Duration) (*actionClock, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &actionClock{cancel: cancel, deadline: time.Now().Add(limit)}
	c.mu.Lock()
	c.armLocked(time.Until(c.deadline))
	c.mu.Unlock()
	return c, ctx
}

// armLocked starts the timer for d from now. Its mu is held.
func (c *actionClock) armLocked(d time.Duration) {
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

// pause stops the action's time while a question is open.
func (c *actionClock) pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused || c.stopped {
		return
	}
	c.paused = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.remaining = time.Until(c.deadline)
}

// resume gives the action the time it had left.
func (c *actionClock) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.paused || c.stopped {
		return
	}
	c.paused = false
	c.deadline = time.Now().Add(c.remaining)
	c.armLocked(c.remaining)
}

// stop ends the action's time.
func (c *actionClock) stop() {
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

// expired tells whether the time passed before the action ended.
func (c *actionClock) expiredTime() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}
