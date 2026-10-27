//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"tailscale.com/envknob"
)

// blackholeProxy forwards the connections of a socket of its own to a
// server's, as a network between them. blackhole makes it a network that
// went away without a word, as one that dropped or a NAT that forgot the
// flows: what either side sends is lost, nothing closes, and the flows of
// then stay dead once it heals. While it is out, new connections are
// accepted and dead too.
type blackholeProxy struct {
	path string
	mu   sync.Mutex
	out  bool
	// flows are the live flows, each closed (made dead) by blackhole.
	flows map[*flow]struct{}
}

type flow struct {
	mu   sync.Mutex
	dead bool
}

func (f *flow) isDead() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dead
}

func newBlackholeProxy(t *testing.T, to string) *blackholeProxy {
	t.Helper()
	dir := shortDir(t)
	p := &blackholeProxy{path: filepath.Join(dir, "p.sock"), flows: map[*flow]struct{}{}}
	ln, err := net.Listen("unix", p.path)
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	var cmu sync.Mutex
	keep := func(c ...net.Conn) {
		cmu.Lock()
		conns = append(conns, c...)
		cmu.Unlock()
	}
	t.Cleanup(func() {
		ln.Close()
		cmu.Lock()
		for _, c := range conns {
			c.Close()
		}
		cmu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			keep(c)
			f := &flow{}
			p.mu.Lock()
			if p.out {
				f.dead = true
			} else {
				p.flows[f] = struct{}{}
			}
			p.mu.Unlock()
			if f.isDead() {
				go swallow(c) // as the network does, unanswered
				continue
			}
			s, err := net.Dial("unix", to)
			if err != nil {
				c.Close()
				continue
			}
			keep(s)
			go pump(f, s, c)
			go pump(f, c, s)
		}
	}()
	return p
}

// pump copies src to dst while the flow lives: it closes dst as src
// closes, as a network passes a FIN on, but once dead it passes nothing.
func pump(f *flow, dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if f.isDead() {
			if err != nil {
				return
			}
			continue
		}
		if n > 0 {
			dst.Write(buf[:n])
		}
		if err != nil {
			if !f.isDead() {
				dst.Close()
			}
			return
		}
	}
}

func swallow(c net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
}

// blackhole makes the network go away; heal brings a new one, on which
// the flows of before stay dead.
func (p *blackholeProxy) blackhole() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out = true
	for f := range p.flows {
		f.mu.Lock()
		f.dead = true
		f.mu.Unlock()
	}
	clear(p.flows)
}

func (p *blackholeProxy) heal() {
	p.mu.Lock()
	p.out = false
	p.mu.Unlock()
}

// shortDir is a temporary directory short enough for a socket's path.
func shortDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "rex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// serveRemote serves s on a socket, its connections marked remote, as its
// tailcat listener does.
func serveRemote(t *testing.T, s *Server) string {
	t.Helper()
	// The remote client attaches to the local test sessions: the host shares them.
	s.rem.mu.Lock()
	s.rem.cfg.ShareSessions = true
	s.rem.mu.Unlock()
	sock := filepath.Join(shortDir(t), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				s.handle(c, "nodekey:stale-test")
				c.Close()
			}()
		}
	}()
	return sock
}

// attachedTo counts the connections attached to session sid.
func attachedTo(s *Server, sid string) int {
	s.mu.Lock()
	ss := s.sessions[sid]
	s.mu.Unlock()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.clients)
}

// controlsOf counts the control connections the server serves.
func controlsOf(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controls
}

// waitUntil polls cond until it holds or d passes, and tells which.
func waitUntil(d time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(d); ; time.Sleep(10 * time.Millisecond) {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// settledGoroutines waits for the goroutines to come down to at most
// want, and returns how many there are.
func settledGoroutines(want int, d time.Duration) int {
	n := runtime.NumGoroutine()
	waitUntil(d, func() bool {
		n = runtime.NumGoroutine()
		return n <= want
	})
	return n
}

// TestBlackholedAttachesAreDropped is a client whose network goes away
// without a word, again and again, as the Mac's to a Windows host while
// Wi-Fi roams: each time it connects anew and attaches again. The server
// must drop the connections of before, which nothing closes: the session
// counts one window attached, not one more per drop, and no goroutine,
// connection nor queue of them stays, on either side.
func TestBlackholedAttachesAreDropped(t *testing.T) {
	defer SetTimeoutsForTest(600*time.Millisecond, 2*time.Second)()
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	cmd, dir := testShell()
	info, err := local.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Kill(info.ID)
	proxy := newBlackholeProxy(t, serveRemote(t, s))

	const cycles = 5
	var after []int // attached once each drop settled
	base := 0
	buf := make([]byte, 64<<10)
	for i := range cycles {
		c, err := ConnectSocketKeepalive(proxy.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Hello(); err != nil {
			t.Fatal(err)
		}
		st := c.Stream(info.ID, 80, 24)
		st.Resize(80, 24)
		read := make(chan error, 1)
		go func() {
			for {
				if _, err := st.Read(buf); err != nil {
					read <- err
					return
				}
			}
		}()
		if !waitUntil(5*time.Second, func() bool { return attachedTo(s, info.ID) >= 1 }) {
			t.Fatalf("cycle %d: the stream did not attach", i)
		}
		if i == 0 {
			// The goroutines of one client attached, settled.
			time.Sleep(200 * time.Millisecond)
			base = runtime.NumGoroutine()
		}

		proxy.blackhole()
		// The client notices: its pings go unanswered. One that does not
		// is closed as the app closes it once a request times out.
		select {
		case <-c.Closed():
		case <-time.After(5 * time.Second):
			t.Errorf("cycle %d: the client did not notice the network gone", i)
			c.Close()
		}
		select {
		case <-read:
		case <-time.After(5 * time.Second):
			t.Errorf("cycle %d: the stream reads on, from a network gone", i)
		}
		c.Close()
		st.Close()
		// The server notices too, in a bounded time.
		waitUntil(5*time.Second, func() bool { return attachedTo(s, info.ID) == 0 && controlsOf(s) == 1 })
		after = append(after, attachedTo(s, info.ID))
		proxy.heal()
	}
	for _, n := range after {
		if n != 0 {
			t.Fatalf("the session counts %v attached after each drop, want 0 each time", after)
		}
	}
	if n := controlsOf(s); n != 1 {
		t.Errorf("the server serves %d control connections, want 1 (this test's own)", n)
	}
	s.mu.Lock()
	kept := len(s.kept)
	s.mu.Unlock()
	if kept != 0 {
		t.Errorf("the server keeps %d connections gone", kept)
	}
	// Each cycle's client, stream, and the server's side of them, gone:
	// what is left is the dead flows of the proxy, two a cycle (one a
	// connection), and the attached client of the first cycle, counted
	// in base.
	want := base + 4*cycles
	if n := settledGoroutines(want, 5*time.Second); n > want {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		t.Errorf("%d goroutines after %d drops, want at most %d (%d with one client attached)\n%s", n, cycles, want, base, buf)
	}
}

// TestStalledClientIsDropped is a remote client that stops reading what
// a session prints, as one whose network went away while it printed: the
// server must drop it, not hold it, its goroutines and what it would have
// written, for as long as the session prints.
func TestStalledClientIsDropped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("prints with sh")
	}
	defer SetTimeoutsForTest(500*time.Millisecond, 2*time.Second)()
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	// A session that prints 16 KiB every tenth of a second: more than the
	// socket holds soon, but far fewer chunks than a queue holds, so that
	// only the client's silence can drop it.
	info, err := local.Create(CreateOptions{
		Command: []string{"/bin/sh", "-c", `while :; do head -c 16384 /dev/zero | tr '\0' x; sleep 0.1; done`},
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Kill(info.ID)
	sock := serveRemote(t, s)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, `{"op":"attach","sid":%q,"cols":80,"rows":24}`+"\n", info.ID)
	// It reads the answer, then nothing.
	if err := readOK(conn); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(2*time.Second, func() bool { return attachedTo(s, info.ID) == 1 }) {
		t.Fatal("not attached")
	}
	if !waitUntil(10*time.Second, func() bool { return attachedTo(s, info.ID) == 0 }) {
		t.Fatalf("a client that reads nothing stays attached, %d", attachedTo(s, info.ID))
	}
	// Its connection is closed: past what the socket holds, it reads the
	// end.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	b := make([]byte, 64<<10)
	for {
		if _, err := conn.Read(b); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("the server kept the connection of the client it dropped")
			}
			break
		}
	}
}

// TestQueueIsBounded checks that what waits for a client that is behind
// is bounded in bytes, not only in chunks: 2048 chunks of a megabyte would
// be two gigabytes.
func TestQueueIsBounded(t *testing.T) {
	a := &attached{out: make(chan []byte, 2048), remote: true}
	chunk := make([]byte, 1<<20)
	queued := 0
	for range 2048 {
		if !a.send(chunk) {
			break
		}
		queued += len(chunk)
	}
	if queued > 8<<20 {
		t.Errorf("%d MiB wait for a client that reads nothing, want at most 8", queued>>20)
	}
}

// TestNetstackKeepalive checks that the server shortens the TCP keepalive
// of the connections tailcat accepts, which tailscale's netstack reads
// from its knobs, for clients that do not ping, and keeps the knobs out
// of the sessions' environment.
func TestNetstackKeepalive(t *testing.T) {
	tuneKeepalive()
	for k, want := range map[string]time.Duration{
		"TS_NETSTACK_KEEPALIVE_IDLE":     30 * time.Second,
		"TS_NETSTACK_KEEPALIVE_INTERVAL": 10 * time.Second,
	} {
		if got := envknob.RegisterDuration(k)(); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
		if v, set := os.LookupEnv(k); set {
			t.Errorf("%s=%s is in the environment", k, v)
		}
	}
}

// TestWriterClosesOnFailedWrite is a remote client that takes nothing of
// its snapshot, of a session that prints nothing after: once the write
// times out the server closes the connection at once, not when the
// session prints again, which an idle one may never do.
func TestWriterClosesOnFailedWrite(t *testing.T) {
	defer SetTimeoutsForTest(200*time.Millisecond, 2*time.Second)()
	client, server := net.Pipe()
	defer client.Close()
	a := &attached{conn: server, first: []byte("snapshot"), out: make(chan []byte, maxQueue), remote: true}
	done := make(chan struct{})
	go func() {
		a.writer()
		close(done)
	}()
	time.Sleep(500 * time.Millisecond) // past the write's deadline
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := client.Read(make([]byte, 64))
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the connection stays open after its write failed")
	}
	a.finish() // as the attach does, its read failing
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the writer does not return")
	}
}

// TestLocalQueueNotCappedInBytes checks that a local client, whose
// dropped stream would close its pane, keeps the queue bounded in chunks
// only, as before; a remote one is capped in bytes.
func TestLocalQueueNotCappedInBytes(t *testing.T) {
	chunk := make([]byte, 1<<20)
	for _, remote := range []bool{false, true} {
		a := &attached{out: make(chan []byte, maxQueue), remote: remote}
		n := 0
		for range 16 {
			if !a.send(chunk) {
				break
			}
			n++
		}
		if want := map[bool]int{false: 16, true: 8}[remote]; n != want {
			t.Errorf("remote %v: %d MiB queued, want %d", remote, n, want)
		}
	}
}

// TestKillBesideKeepaliveHello sends a kill and a hello that keeps the
// connection alive at once: the kill's answer, aside, must not race the
// hello (go test -race).
func TestKillBesideKeepaliveHello(t *testing.T) {
	s, _ := testServer(t)
	for range 20 {
		a, b := net.Pipe()
		go s.handle(b, "nodekey:stale-test")
		// One write: the server reads the hello as the kill is answered.
		go a.Write([]byte(`{"id":1,"op":"kill","sid":"nosuch"}` + "\n" + `{"id":2,"op":"hello","keepalive":true}` + "\n"))
		a.SetReadDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(a)
		for range 2 {
			if _, err := r.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
		}
		a.Close()
	}
}
