//go:build darwin || linux || windows

package rex

// Benchmarks of the session server's hot path, as
// docs/REF-009-session-benchmarks.md designs them: emit under each load
// its scans see, emit with clients attached, and the pause an attach of
// escape sequences puts on emit while it renders the snapshot under the
// session's lock.

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// benchSession returns a session of cols×rows whose program prints
// nothing, and the client that made it.
func benchSession(tb testing.TB, cols, rows int) (*Server, *Client, *session, string) {
	tb.Helper()
	s, _ := testServer(tb)
	c := pipe(s, "")
	info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: cols, Rows: rows})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { c.Kill(info.ID) })
	return s, c, sessionOf(tb, s, info.ID), info.ID
}

// chunksOf returns data as the chunks a PTY read delivers, 4 KiB long so
// a sequence can end up cut anywhere.
func chunksOf(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		n := min(len(data), 4<<10)
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

// emitLoads are the streams BenchmarkEmit feeds, 256 KiB each. The osc
// load has no "?" bodies: the server answers those on the PTY, whose
// input a command that does not read never drains.
var emitLoads = []struct {
	name string
	make func() []byte
}{
	{"plain", func() []byte {
		var b strings.Builder
		for i := 0; b.Len() < 256<<10; i++ {
			fmt.Fprintf(&b, "line %07d %s\r\n", i, strings.Repeat("x", i%80))
		}
		return []byte(b.String())
	}},
	{"styles", func() []byte {
		var b strings.Builder
		for i := 0; b.Len() < 256<<10; i++ {
			fmt.Fprintf(&b, "\x1b[3%dmline %07d\x1b[0m %s\r\n", i%8, i, strings.Repeat("x", i%50))
		}
		return []byte(b.String())
	}},
	{"osc", func() []byte {
		msg := base64.StdEncoding.EncodeToString([]byte("working"))
		var b strings.Builder
		for i := 0; b.Len() < 256<<10; i++ {
			fmt.Fprintf(&b, "\x1b[3%dmline %07d\x1b[0m %s\r\n", i%8, i, strings.Repeat("x", i%50))
			switch i % 16 {
			case 0:
				b.WriteString("\x1b]133;A\x07")
			case 4:
				fmt.Fprintf(&b, "\x1b]7501;state=working:id=w%d:progress=%d:msg=%s\x1b\\", i%64, i%100, msg)
			case 8:
				fmt.Fprintf(&b, "\x1b]7501;state=done:id=w%d\x1b\\", i%64)
			case 12:
				b.WriteString("\x1b]7502;state=clear\x1b\\")
			}
		}
		return []byte(b.String())
	}},
	{"graphics", func() []byte {
		var b strings.Builder
		for i := 0; b.Len() < 256<<10; i++ {
			fmt.Fprintf(&b, "\x1b[3%dmline %07d\x1b[0m %s\r\n", i%8, i, strings.Repeat("x", i%50))
			if i%16 == 0 {
				b.WriteString("\x1b_Ga=T,f=24,t=d,s=1,v=1,c=1,r=1,q=2;QUJD\x1b\\")
			}
		}
		return []byte(b.String())
	}},
}

// BenchmarkEmit feeds each load to a session that no client watches.
func BenchmarkEmit(b *testing.B) {
	for _, l := range emitLoads {
		b.Run(l.name, func(b *testing.B) {
			_, _, ss, _ := benchSession(b, 200, 50)
			chunks := chunksOf(l.make())
			b.SetBytes(int64(len(chunks[0])))
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				d := chunks[i%len(chunks)]
				i++
				ss.emit(d, nil, len(d))
			}
		})
	}
}

// BenchmarkEmitClients feeds the styles load to a session whose clients
// drain their streams, for the cost of send and the per-client queue.
func BenchmarkEmitClients(b *testing.B) {
	_, c, ss, sid := benchSession(b, 200, 50)
	for i := 0; i < 4; i++ {
		conn, err := c.AttachConn(Attach{SID: sid, Cols: 200, Rows: 50, Mute: true})
		if err != nil {
			b.Fatal(err)
		}
		defer conn.Close()
		go io.Copy(io.Discard, conn)
	}
	chunks := chunksOf(emitLoads[1].make())
	b.SetBytes(int64(len(chunks[0])))
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		d := chunks[i%len(chunks)]
		i++
		ss.emit(d, nil, len(d))
	}
}

// BenchmarkAttachStall measures what an attach that asks for escape
// sequences costs the session's output: a goroutine emits while the
// attach renders the scrollback under the session's lock, and the
// longest emit is the stall every client sees. ns/op is the attach
// itself; the stall is the ns/stall metric.
func BenchmarkAttachStall(b *testing.B) {
	s, c, ss, sid := benchSession(b, 300, 50)
	_ = s
	var fill []byte
	for i := 0; len(fill) < 32<<20; i++ {
		for j := range 290 {
			fill = fmt.Appendf(fill, "\x1b[38;5;%dm%c", (i+j)%256, 'a'+j%26)
		}
		fill = append(fill, "\r\n"...)
	}
	for len(fill) > 0 {
		n := min(len(fill), 64<<10)
		ss.emit(fill[:n], nil, n)
		fill = fill[n:]
	}
	var stall atomic.Int64
	stop := make(chan struct{})
	go func() {
		d := []byte("\x1b[31m.\x1b[0m")
		for {
			select {
			case <-stop:
				return
			default:
			}
			at := time.Now()
			ss.emit(d, nil, len(d))
			if d := time.Since(at); d > time.Duration(stall.Load()) {
				stall.Store(int64(d))
			}
		}
	}()
	defer close(stop)
	for b.Loop() {
		conn, err := c.AttachConn(Attach{SID: sid, Framed: true, History: resyncHistory, Mute: true})
		if err != nil {
			b.Fatal(err)
		}
		conn.Close()
	}
	b.ReportMetric(float64(stall.Load()), "ns/stall")
}
