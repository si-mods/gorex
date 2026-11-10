package tailnet

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorex/internal/rex"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stun/stuntest"
	"tailscale.com/tailcfg"
	"tailscale.com/tsd"
	"tailscale.com/types/key"
	"tailscale.com/types/nettype"
)

// runDERP runs a DERP relay and a STUN server on the loopback, as
// internal/rex's tests do.
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

// machine is a GoRex machine's tailcat server: HTTP on 7297, as the
// gateway will be, an echo on 7465, and pairing on RemotePort, as the
// session server does it with internal/rex's wire. Pairing answers
// pairing, else refuses as a machine not pairing does.
type machine struct {
	ts      *tailcat.Server
	mu      sync.Mutex
	pairing bool
	asked   string // the name of the last request
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	m := &machine{ts: &tailcat.Server{Key: key.NewNode(), PresharedKey: tailcat.NewPresharedKey(), Region: runDERP(t),
		Logf: func(string, ...any) {}}}
	t.Cleanup(func() { m.ts.Close() })
	listen := func(port int, serve func(net.Conn)) {
		ln, err := m.ts.Listen(context.Background(), "tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { defer c.Close(); serve(c) }()
			}
		}()
	}
	gw, err := m.ts.Listen(context.Background(), "tcp", ":7297")
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(gw, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "gateway "+r.Host)
	}))
	listen(7465, func(c net.Conn) { io.Copy(c, c) })
	listen(rex.RemotePort, func(c net.Conn) {
		var k string
		for _, kv := range m.ts.PeerEnv(c.LocalAddr(), c.RemoteAddr()) {
			if v, ok := strings.CutPrefix(kv, "TAILCAT_PEER_KEY="); ok {
				k = v
			}
		}
		m.mu.Lock()
		pairing := m.pairing
		m.mu.Unlock()
		if !pairing {
			b, _ := json.Marshal(rex.Response{Error: "rex: this machine is not allowed here"})
			c.Write(append(b, '\n'))
			return
		}
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			return
		}
		var req rex.Request
		res := rex.Response{}
		if json.Unmarshal(line, &req) != nil || req.Op != "remote.pair" || req.Remote == nil {
			res.Error = "rex: not a pairing request"
		} else {
			m.mu.Lock()
			m.asked = req.Remote.Name
			m.mu.Unlock()
			res.ID = req.ID
			res.Data, _ = json.Marshal(rex.PairResult{Fingerprint: rex.Fingerprint(k)})
		}
		b, _ := json.Marshal(res)
		c.Write(append(b, '\n'))
	})
	return m
}

// tailcatEnv is a client key of its own, and no hosts after.
func tailcatEnv(t *testing.T) {
	if err := SetTailcatKey(NewTailcatKey()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetTailcatHosts("[]") })
}

func setHosts(t *testing.T, hosts ...tailcatHost) {
	t.Helper()
	b, _ := json.Marshal(hosts)
	if err := SetTailcatHosts(string(b)); err != nil {
		t.Fatal(err)
	}
}

// proxyClient is an HTTP client through the proxy, as URLSession's.
func proxyClient(t *testing.T) (*http.Client, string) {
	t.Helper()
	j, err := Proxy()
	if err != nil {
		t.Fatal(err)
	}
	var p struct{ Address, Password string }
	json.Unmarshal([]byte(j), &p)
	u := &url.URL{Scheme: "socks5", User: url.UserPassword("tsnet", p.Password), Host: p.Address}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true}}, p.Address + " " + p.Password
}

func get(c *http.Client, u string) (string, error) {
	res, err := c.Get(u)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return string(b), err
}

// A tailcat host is reached through the proxy, by its id, on any port,
// with no tailnet running; any other name is refused.
func TestTailcatProxy(t *testing.T) {
	Stop()
	tailcatEnv(t)
	m := newMachine(t)
	setHosts(t, tailcatHost{ID: "mac", Address: string(m.ts.TailcatAddr())})
	hc, _ := proxyClient(t)
	if got, err := get(hc, "http://mac.tailcat.invalid:7297/"); err != nil || got != "gateway mac.tailcat.invalid:7297" {
		t.Fatalf("GET through the proxy = %q, %v", got, err)
	}
	if got, err := get(hc, "http://MAC.tailcat.invalid.:7297/"); err != nil || !strings.HasPrefix(got, "gateway") {
		t.Errorf("GET of the name in capitals = %q, %v", got, err)
	}
	for _, u := range []string{"http://other.tailcat.invalid:7297/", "http://example.com/", "http://mac-gorex.example.ts.net/",
		"http://mac.tailcat.invalid.example.com:7297/", "http://100.64.0.8/"} {
		if got, err := get(hc, u); err == nil {
			t.Errorf("GET %s = %q; want refused", u, got)
		}
	}
	// A host taken out is refused, its client closed.
	setHosts(t)
	if got, err := get(hc, "http://mac.tailcat.invalid:7297/"); err == nil {
		t.Errorf("GET of a host taken out = %q", got)
	}
	tcMu.Lock()
	n := len(tcClients)
	tcMu.Unlock()
	if n != 0 {
		t.Errorf("%d clients left with no hosts", n)
	}
}

func TestTailcatTarget(t *testing.T) {
	for addr, want := range map[string]string{"mac.tailcat.invalid:7297": "mac 7297", "Mac.Tailcat.Invalid.:1": "mac 1"} {
		id, port, ok := tailcatTarget(addr)
		if got := fmt.Sprint(id, " ", port); !ok || got != want {
			t.Errorf("tailcatTarget(%s) = %s, %v; want %s", addr, got, ok, want)
		}
	}
	for _, addr := range []string{"tailcat.invalid:1", ".tailcat.invalid:1", "a.b.tailcat.invalid:1", "mac.tailcat.invalid",
		"mac.tailcat.invalid:70000", "mac.example.com:1", "-a.tailcat.invalid:1"} {
		if id, port, ok := tailcatTarget(addr); ok {
			t.Errorf("tailcatTarget(%s) = %s %d; want none", addr, id, port)
		}
	}
}

func TestSetTailcatHosts(t *testing.T) {
	tailcatEnv(t)
	for _, j := range []string{`not json`, `[{"id":"a.b","address":"x"}]`, `[{"id":"mac","address":"not an address"}]`} {
		if SetTailcatHosts(j) == nil {
			t.Errorf("SetTailcatHosts(%s) took it", j)
		}
	}
}

func TestTailcatKey(t *testing.T) {
	for _, k := range []string{"", "privkey:00", "nodekey:" + strings.Repeat("0", 64)} {
		if SetTailcatKey(k) == nil {
			t.Errorf("SetTailcatKey(%q) took it", k)
		}
	}
	k := NewTailcatKey()
	if err := SetTailcatKey(k); err != nil {
		t.Fatal(err)
	}
	var priv key.NodePrivate
	priv.UnmarshalText([]byte(k))
	if TailcatKey() != priv.Public().String() || !strings.HasPrefix(TailcatKey(), "nodekey:") {
		t.Errorf("TailcatKey() = %s, want %s", TailcatKey(), priv.Public())
	}
	if _, err := rex.NormalizePublicKey(TailcatKey()); err != nil {
		t.Errorf("an allowlist would not take TailcatKey(): %v", err)
	}
}

// Pairing speaks the session server's remote.pair, and returns the
// fingerprint the machine shows.
func TestTailcatPair(t *testing.T) {
	tailcatEnv(t)
	m := newMachine(t)
	addr := string(m.ts.TailcatAddr())
	if _, err := TailcatPair(addr, "rexgo-kqzt"); err == nil || !strings.Contains(err.Error(), "not pairing") {
		t.Errorf("pairing with a machine not pairing: %v", err)
	}
	m.mu.Lock()
	m.pairing = true
	m.mu.Unlock()
	fp, err := TailcatPair(addr, "rexgo-kqzt")
	if err != nil || fp == "" || fp != rex.Fingerprint(TailcatKey()) {
		t.Errorf("TailcatPair = %q, %v; want %q", fp, err, rex.Fingerprint(TailcatKey()))
	}
	if m.asked != "rexgo-kqzt" {
		t.Errorf("the machine was asked by %q", m.asked)
	}
	// The client made to pair, of no host, is gone.
	tcMu.Lock()
	n := len(tcClients)
	tcMu.Unlock()
	if n != 0 {
		t.Errorf("%d clients left after pairing", n)
	}
	if _, err := TailcatPair("not an address", "rexgo-kqzt"); err == nil {
		t.Error("paired with no address")
	}
	// A listed machine's pooled client pairs, and stays.
	setHosts(t, tailcatHost{ID: "mac", Address: addr})
	if fp, err := TailcatPair(addr, "rexgo-kqzt"); err != nil || fp != rex.Fingerprint(TailcatKey()) {
		t.Errorf("TailcatPair with a listed machine = %q, %v", fp, err)
	}
	tcMu.Lock()
	p := tcClients[addr]
	tcMu.Unlock()
	if p == nil || !p.used {
		t.Error("pairing a listed machine didn't use its client")
	}
}

// The fields tailcatSystem reaches; it gives nil for any it doesn't find.
func TestTailcatFields(t *testing.T) {
	ty := reflect.TypeFor[tailcat.Client]()
	for name, kind := range map[string]reflect.Kind{"startMu": reflect.Struct, "started": reflect.Bool, "lb": reflect.Pointer} {
		if f, ok := ty.FieldByName(name); !ok || f.Type.Kind() != kind {
			t.Errorf("tailcat.Client.%s: not a %v", name, kind)
		}
	}
	f, _ := ty.FieldByName("lb")
	if f.Type.Kind() != reflect.Pointer {
		return
	}
	if sf, ok := f.Type.Elem().FieldByName("sys"); !ok || sf.Type != reflect.TypeFor[tsd.System]() {
		t.Error("tailcat's locoBackend.sys: not a tsd.System")
	}
	if tailcatSystem(&tailcat.Client{}) != nil {
		t.Error("a client never used has a system")
	}
}

// After iOS reclaims the sockets, ResetSockets starts a tailcat client's
// receive loops again; a connection open across it goes on, and new ones
// connect.
func TestTailcatReset(t *testing.T) {
	Stop()
	tailcatEnv(t)
	m := newMachine(t)
	addr := string(m.ts.TailcatAddr())
	setHosts(t, tailcatHost{ID: "mac", Address: addr})
	hc, _ := proxyClient(t)
	if _, err := get(hc, "http://mac.tailcat.invalid:7297/"); err != nil {
		t.Fatal(err)
	}
	j, _ := Proxy()
	var p struct{ Address, Password string }
	json.Unmarshal([]byte(j), &p)
	echo, err := socksConnect(p.Address, "tsnet", p.Password, "mac.tailcat.invalid", 7465)
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	roundTrip := func(s string) {
		t.Helper()
		echo.Write([]byte(s))
		b := make([]byte, len(s))
		if _, err := io.ReadFull(echo, b); err != nil || string(b) != s {
			t.Fatalf("echo %q = %q, %v", s, b, err)
		}
	}
	roundTrip("before")

	tcMu.Lock()
	c := tcClients[addr]
	tcMu.Unlock()
	sys := tailcatSystem(c.tc)
	if sys == nil {
		t.Fatal("a started client has no system")
	}
	// The machine's server and the client: three receive loops each.
	waitLoops(t, 6)
	closeSockets(sys.MagicSock.Get())
	waitLoops(t, 4)
	if err := ResetSockets(); err != nil {
		t.Fatal(err)
	}
	waitLoops(t, 6)
	roundTrip("after")
	if _, err := get(hc, "http://mac.tailcat.invalid:7297/"); err != nil {
		t.Errorf("GET after the reset: %v", err)
	}
	tcMu.Lock()
	same := tcClients[addr] == c
	tcMu.Unlock()
	if !same {
		t.Error("the reset replaced a client that wasn't stuck")
	}
	DefaultInterfaceChanged("en0")
}

// A machine whose server restarted, as on an upgrade, is reached again at
// once, not after WireGuard's rekey.
func TestTailcatServerRestart(t *testing.T) {
	Stop()
	tailcatEnv(t)
	m := newMachine(t)
	setHosts(t, tailcatHost{ID: "mac", Address: string(m.ts.TailcatAddr())})
	hc, _ := proxyClient(t)
	if _, err := get(hc, "http://mac.tailcat.invalid:7297/"); err != nil {
		t.Fatal(err)
	}
	ci, _ := tailcat.ParseAddr(m.ts.TailcatAddr())
	m.ts.Close()
	again := &tailcat.Server{Key: m.ts.Key, PresharedKey: m.ts.PresharedKey, Region: ci.Region[0], Logf: func(string, ...any) {}}
	t.Cleanup(func() { again.Close() })
	gw, err := again.Listen(context.Background(), "tcp", ":7297")
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(gw, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "again") }))
	start := time.Now()
	if got, err := get(hc, "http://mac.tailcat.invalid:7297/"); err != nil || got != "again" {
		t.Fatalf("GET after the restart = %q, %v", got, err)
	}
	if d := time.Since(start); d > tailcatDial {
		t.Errorf("reached again in %v", d)
	}
}

// A client with no connection for tailcatIdle closes; one with an open
// connection stays, as does one whose dial to a closed port failed while
// another connection uses it.
func TestTailcatIdle(t *testing.T) {
	defer func(d time.Duration) { tailcatIdle = d }(tailcatIdle)
	tailcatIdle = 200 * time.Millisecond
	Stop()
	tailcatEnv(t)
	m := newMachine(t)
	addr := string(m.ts.TailcatAddr())
	setHosts(t, tailcatHost{ID: "mac", Address: addr})
	pooled := func() *pooledTailcat {
		tcMu.Lock()
		defer tcMu.Unlock()
		return tcClients[addr]
	}
	j, _ := Proxy()
	var p struct{ Address, Password string }
	json.Unmarshal([]byte(j), &p)
	echo, err := socksConnect(p.Address, "tsnet", p.Password, "mac.tailcat.invalid", 7465)
	if err != nil {
		t.Fatal(err)
	}
	c := pooled()
	if _, err := socksConnect(p.Address, "tsnet", p.Password, "mac.tailcat.invalid", 1); err == nil {
		t.Error("a port nothing listens on connected")
	}
	time.Sleep(3 * tailcatIdle)
	if pooled() != c {
		t.Fatal("a client with an open connection went")
	}
	echo.Close()
	for range 50 {
		if pooled() == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("an idle client stayed")
}
