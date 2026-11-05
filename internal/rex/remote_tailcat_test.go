package rex

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stun/stuntest"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/nettype"
)

// The tests here are of #212: two tailcat clients of one key take each
// other's packets, so the local server dials for every process of the
// machine, with one client per address. They run tailcat against a DERP
// relay and a STUN server on the loopback.

// runDERP runs a DERP relay and a STUN server on the loopback, as
// tailscale.com/tstest/integration.RunDERPAndSTUN does, without the
// dependencies of that package.
func runDERP(t *testing.T) *tailcfg.DERPRegion {
	d := derpserver.New(key.NewNode(), func(string, ...any) {})
	hs := httptest.NewUnstartedServer(derpserver.Handler(d))
	hs.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	hs.StartTLS()
	stunAddr, stunClose := stuntest.ServeWithPacketListener(t, nettype.Std{})
	t.Cleanup(func() {
		hs.CloseClientConnections()
		hs.Close()
		d.Close()
		stunClose()
	})
	return &tailcfg.DERPRegion{
		RegionID: 1, RegionCode: "test",
		Nodes: []*tailcfg.DERPNode{{
			Name: "t1", RegionID: 1,
			HostName: "127.0.0.1", IPv4: "127.0.0.1", IPv6: "none",
			STUNPort: stunAddr.Port, STUNTestIP: "127.0.0.1",
			DERPPort:         hs.Listener.Addr().(*net.TCPAddr).Port,
			InsecureForTests: true,
		}},
	}
}

// echoServer is a tailcat server that answers each line on RemotePort
// with the same line.
func echoServer(t *testing.T, reg *tailcfg.DERPRegion, k key.NodePrivate, psk tailcat.PresharedKey) *tailcat.Server {
	t.Helper()
	ts := &tailcat.Server{Key: k, PresharedKey: psk, Region: reg, Logf: func(string, ...any) {}}
	ln, err := ts.Listen(context.Background(), "tcp", fmt.Sprintf(":%d", RemotePort))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil || func() error { _, err := c.Write([]byte(line)); return err }() != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ts.Close() })
	return ts
}

// tailcatEnv is a directory of its own, so a client key of its own, a
// pool of tailcat clients emptied after, and no local server to start.
func tailcatEnv(t *testing.T) *tailcfg.DERPRegion {
	t.Setenv("GOREX_DIR", t.TempDir())
	old := spawnServer
	spawnServer = func() error { return errors.New("no server in tests") }
	t.Cleanup(func() { spawnServer = old })
	t.Cleanup(func() {
		tailcatMu.Lock()
		pool := tailcatClients
		tailcatClients = map[string]*pooledTailcat{}
		tailcatMu.Unlock()
		for _, p := range pool {
			p.tc.Close()
		}
	})
	return runDERP(t)
}

// localRelay serves the local socket as the server does the first line
// of a connection; s may be nil for a server that answers that line with
// reply instead.
func localRelay(t *testing.T, s *Server, reply string) {
	t.Helper()
	ln, err := net.Listen("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	serveRelay(ln, s, reply)
}

func serveRelay(ln net.Listener, s *Server, reply string) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if s != nil {
				go s.handle(c, "")
				continue
			}
			go func() {
				defer c.Close()
				bufio.NewReader(c).ReadString('\n')
				c.Write([]byte(reply))
				c.Read(make([]byte, 1))
			}()
		}
	}()
}

func lineTrip(c net.Conn, d time.Duration) error {
	c.SetDeadline(time.Now().Add(d))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write([]byte("ping\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if line != "ping\n" {
		return fmt.Errorf("read %q", line)
	}
	return nil
}

// dialTrip connects as ConnectRemote does and gets a line back.
func dialTrip(t *testing.T, addr tailcat.Addr) (net.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), tailcatRedial)
	defer cancel()
	t0 := time.Now()
	c, err := dialRemote(ctx, string(addr))
	if err != nil {
		return nil, err
	}
	if err := lineTrip(c, 5*time.Second); err != nil {
		c.Close()
		return nil, err
	}
	t.Logf("connected in %v", time.Since(t0).Round(time.Millisecond))
	return c, nil
}

func poolSize() int {
	tailcatMu.Lock()
	defer tailcatMu.Unlock()
	return len(tailcatClients)
}

// The window's connection stays up, asked every 200 ms, while the CLI of
// the same machine connects again and again: both work, through one
// tailcat client of the local server.
func TestTailcatSameKeyRelayed(t *testing.T) {
	reg := tailcatEnv(t)
	ts := echoServer(t, reg, key.NewNode(), tailcat.NewPresharedKey())
	s, _ := testServer(t)
	localRelay(t, s, "")

	win, err := dialTrip(t, ts.TailcatAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer win.Close()
	if _, ok := win.(*net.UnixConn); !ok {
		t.Fatalf("the window's connection is a %T, not the relay's", win)
	}
	var mu sync.Mutex
	var winErrs []error
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
			if err := lineTrip(win, 5*time.Second); err != nil {
				mu.Lock()
				winErrs = append(winErrs, err)
				mu.Unlock()
			}
		}
	})
	for i := range 4 {
		c, err := dialTrip(t, ts.TailcatAddr())
		if err != nil {
			t.Errorf("the CLI's connection %d: %v", i, err)
			continue
		}
		c.Close()
	}
	close(stop)
	wg.Wait()
	if len(winErrs) > 0 {
		t.Errorf("the window's connection failed %d times: %v", len(winErrs), winErrs[0])
	}
	if n := poolSize(); n != 1 {
		t.Errorf("%d tailcat clients, want 1", n)
	}
}

// Concurrent first dials share a client, which closes once it had no
// connection for tailcatIdle.
func TestTailcatPool(t *testing.T) {
	reg := tailcatEnv(t)
	ts := echoServer(t, reg, key.NewNode(), tailcat.NewPresharedKey())
	old := tailcatIdle
	tailcatIdle = 300 * time.Millisecond
	t.Cleanup(func() { tailcatIdle = old })

	var conns [3]net.Conn
	var errs [3]error
	var wg sync.WaitGroup
	for i := range conns {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), tailcatDial)
			defer cancel()
			if conns[i], errs[i] = dialTailcat(ctx, string(ts.TailcatAddr())); errs[i] == nil {
				errs[i] = lineTrip(conns[i], 5*time.Second)
			}
		})
	}
	wg.Wait()
	tailcatMu.Lock()
	p := tailcatClients[string(ts.TailcatAddr())]
	n := len(tailcatClients)
	tailcatMu.Unlock()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		if conns[i].(*tailcatConn).p != p {
			t.Errorf("dial %d has a client of its own", i)
		}
	}
	if n != 1 {
		t.Fatalf("%d tailcat clients, want 1", n)
	}
	conns[0].Close()
	conns[1].Close()
	time.Sleep(2 * tailcatIdle)
	if poolSize() != 1 {
		t.Fatal("closed with a connection open")
	}
	conns[2].Close()
	conns[2].Close()
	for deadline := time.Now().Add(5 * time.Second); poolSize() != 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("still open, idle")
		}
	}
}

// The remote server restarts, as an upgrade does, with the same address:
// the kept client registers again and connects.
func TestTailcatServerRestart(t *testing.T) {
	reg := tailcatEnv(t)
	sk, psk := key.NewNode(), tailcat.NewPresharedKey()
	ts := echoServer(t, reg, sk, psk)
	s, _ := testServer(t)
	localRelay(t, s, "")
	oldTry := tailcatTry
	tailcatTry = time.Second
	t.Cleanup(func() { tailcatTry = oldTry })

	c, err := dialTrip(t, ts.TailcatAddr())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	ts.Close()
	ts = echoServer(t, reg, sk, psk)
	c, err = dialTrip(t, ts.TailcatAddr())
	if err != nil {
		t.Fatalf("after the restart: %v", err)
	}
	c.Close()
}

// With no local server it cannot start, or one that does not relay, the
// process dials itself; a relay that failed is the error, not dialed
// again here.
func TestTailcatRelayFallback(t *testing.T) {
	reg := tailcatEnv(t)
	ts := echoServer(t, reg, key.NewNode(), tailcat.NewPresharedKey())
	addr := ts.TailcatAddr()

	c, err := dialTrip(t, addr)
	if err != nil {
		t.Fatalf("without a local server: %v", err)
	}
	if _, ok := c.(*tailcatConn); !ok {
		t.Errorf("without a local server, a %T", c)
	}
	if poolSize() != 1 {
		t.Errorf("%d tailcat clients, want 1", poolSize())
	}
	c.Close()

	localRelay(t, nil, `{"id":0,"error":"unknown op \"remote.tailcatDial\""}`+"\n")
	if c, err = dialTrip(t, addr); err != nil {
		t.Fatalf("with an older local server: %v", err)
	}
	if _, ok := c.(*tailcatConn); !ok {
		t.Errorf("with an older local server, a %T", c)
	}
	c.Close()
}

func TestTailcatRelayError(t *testing.T) {
	tailcatEnv(t)
	localRelay(t, nil, `{"error":"context deadline exceeded"}`+"\n")
	addr := key.NewNode().Public().String() // not dialed: any string
	_, err := dialRemote(context.Background(), addr)
	if err == nil || err.Error() != "rex: connecting to the remote server: context deadline exceeded" {
		t.Fatalf("err = %v", err)
	}
	if poolSize() != 0 {
		t.Error("dialed here as well")
	}
	if errors.Is(err, errNoRelay) {
		t.Error("taken for no relay")
	}
}

func TestTailcatRelayRefusesBadAddress(t *testing.T) {
	tailcatEnv(t)
	s, _ := testServer(t)
	localRelay(t, s, "")
	secret := "tc" + strings.Repeat("Zq9_-x", 12) + "!!"
	_, err := dialRemote(context.Background(), secret)
	if err == nil || strings.Contains(err.Error(), "Zq9_") || !strings.Contains(err.Error(), "not a tailcat address") {
		t.Fatalf("err = %v", err)
	}
}

// With no local server, one starts, and the connection goes through it.
func TestTailcatRelaySpawns(t *testing.T) {
	reg := tailcatEnv(t)
	ts := echoServer(t, reg, key.NewNode(), tailcat.NewPresharedKey())
	s, _ := testServer(t)
	spawned := 0
	spawnServer = func() error {
		spawned++
		ln, err := net.Listen("unix", SocketPath())
		if err != nil {
			return err
		}
		t.Cleanup(func() { ln.Close() })
		serveRelay(ln, s, "")
		return nil
	}
	c, err := dialTrip(t, ts.TailcatAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.(*net.UnixConn); !ok || spawned != 1 {
		t.Errorf("a %T, %d servers started; want the relay's, 1", c, spawned)
	}
}

// A relayed connection keeps the local server, as a control connection
// does; it may stop once the connection closed.
func TestTailcatRelayKeepsServer(t *testing.T) {
	reg := tailcatEnv(t)
	ts := echoServer(t, reg, key.NewNode(), tailcat.NewPresharedKey())
	s, _ := testServer(t)
	localRelay(t, s, "")
	controls := func() (int, time.Time) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.controls, s.idleFrom
	}
	c, err := dialTrip(t, ts.TailcatAddr())
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := controls(); n != 1 {
		t.Errorf("%d controls with a relayed connection, want 1", n)
	}
	t0 := time.Now()
	c.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		n, idle := controls()
		if n == 0 && !idle.Before(t0) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d controls, idle from %v, after the close", n, idle)
		}
	}
}

// A dial that fails, as one too slow does, closes no client another
// connection uses: that one goes on working.
func TestTailcatFailedDialKeepsClient(t *testing.T) {
	reg := tailcatEnv(t)
	ts := echoServer(t, reg, key.NewNode(), tailcat.NewPresharedKey())
	addr := string(ts.TailcatAddr())
	ctx, cancel := context.WithTimeout(context.Background(), tailcatDial)
	win, err := dialTailcat(ctx, addr)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer win.Close()
	late, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if c, err := dialTailcat(late, addr); err == nil {
		c.Close()
		t.Fatal("dialed past the deadline")
	}
	if err := lineTrip(win, 5*time.Second); err != nil {
		t.Errorf("the live connection after a failed dial: %v", err)
	}
	tailcatMu.Lock()
	same := tailcatClients[addr] == win.(*tailcatConn).p
	tailcatMu.Unlock()
	if !same {
		t.Error("the client was replaced")
	}
}
