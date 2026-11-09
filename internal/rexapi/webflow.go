package rexapi

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// renderWindow bounds bytes received but not yet rendered by a browser. It
// controls this viewer's attach only: a slow viewer never pauses the shell.
const renderWindow = 256 << 10

type renderFlow struct {
	mu             sync.Mutex
	sent, rendered uint64
	wake, done     chan struct{}
	once           sync.Once
}

func newRenderFlow() *renderFlow {
	return &renderFlow{wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (f *renderFlow) close() { f.once.Do(func() { close(f.done) }) }

func (f *renderFlow) ack(n uint64) bool {
	f.mu.Lock()
	if n < f.rendered || n > f.sent {
		f.mu.Unlock()
		return false
	}
	f.rendered = n
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
	return true
}

func (f *renderFlow) reserve(ctx context.Context, n int) error {
	for {
		select {
		case <-f.done:
			return net.ErrClosed
		default:
		}
		f.mu.Lock()
		if f.sent-f.rendered+uint64(n) <= renderWindow {
			f.sent += uint64(n)
			f.mu.Unlock()
			return nil
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.done:
			return net.ErrClosed
		case <-f.wake:
		}
	}
}

func (s *stream) writeOutput(ws *websocket.Conn, p []byte) error {
	s.mu.Lock()
	flow := s.flow
	current := s.ws == ws
	s.mu.Unlock()
	if !current {
		return net.ErrClosed
	}
	if flow == nil {
		return writeStream(ws, p)
	}
	for len(p) > 0 {
		n := min(len(p), maxChunk)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := flow.reserve(ctx, n)
		if err == nil {
			err = ws.Write(ctx, websocket.MessageBinary, p[:n])
		}
		cancel()
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}
