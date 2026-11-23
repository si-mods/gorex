package rexapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

func TestParseNetwork(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.88.0/24":  "192.168.88.0/24",
		"192.168.88.17/24": "192.168.88.0/24",
		"10.6.0.2":         "10.6.0.2/32",
		" 10.6.0.2 ":       "10.6.0.2/32",
		"fd00::1":          "fd00::1/128",
		"fd00::/8":         "fd00::/8",
		"::ffff:10.0.0.1":  "10.0.0.1/32",
	} {
		p, err := ParseNetwork(in)
		if err != nil || p.String() != want {
			t.Errorf("ParseNetwork(%q) = %v, %v; want %s", in, p, err, want)
		}
	}
	for _, bad := range []string{"", "192.168.88", "10.0.0.0/33", "phone", "10.0.0.1:80"} {
		if _, err := ParseNetwork(bad); err == nil {
			t.Errorf("ParseNetwork(%q) took it", bad)
		}
	}
}

func TestAllowedPeer(t *testing.T) {
	nets, err := ParseNetworks([]string{"192.168.88.0/24", "10.6.0.2", "fd00::/8"})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{
		"192.168.88.17:5000":      true,
		"192.168.89.17:5000":      false,
		"10.6.0.2:1":              true,
		"10.6.0.3:1":              false,
		"[fd00::5]:1":             true,
		"[fe80::1]:1":             false,
		"[::ffff:192.168.88.9]:1": true,
		"nonsense":                false,
	} {
		if _, got := allowedPeer(nets, addr); got != want {
			t.Errorf("allowedPeer(%s) = %v, want %v", addr, got, want)
		}
	}
	if _, ok := allowedPeer(nil, "127.0.0.1:1"); ok {
		t.Error("no networks let a caller in")
	}
}

func TestLANConfigCheck(t *testing.T) {
	for _, c := range []LANConfig{
		{Listen: "192.168.88.119"},
		{Listen: "studio:7297"},
		{Listen: "1.2.3.4:7297", Allow: []string{"everyone"}},
	} {
		if err := SaveLAN(t.TempDir(), c); err == nil {
			t.Errorf("SaveLAN(%+v) took it", c)
		}
	}
	dir := t.TempDir()
	want := LANConfig{Enabled: true, Listen: "0.0.0.0:7297", Allow: []string{"10.6.0.0/24"}}
	if err := SaveLAN(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLAN(dir)
	if err != nil || got.Token == "" {
		t.Fatalf("LoadLAN = %+v, %v; want a pairing token made", got, err)
	}
	want.Token = got.Token
	if !sameLAN(got, want) {
		t.Fatalf("LoadLAN = %+v; want %+v", got, want)
	}
	// Saved again with no token, it keeps the one it has.
	want.Token = ""
	if err := SaveLAN(dir, want); err != nil {
		t.Fatal(err)
	}
	if again, _ := LoadLAN(dir); again.Token != got.Token {
		t.Errorf("saving with no token changed it")
	}
	if c, err := LoadLAN(t.TempDir()); err != nil || c.Enabled {
		t.Errorf("a missing file: %+v, %v; want off", c, err)
	}
}

func TestLANPairURL(t *testing.T) {
	for _, tc := range []struct {
		name, listen, bound, bindError string
		enabled, running, allowed      bool
		want                           string
	}{
		{"LAN", "192.168.1.2:7297", "192.168.1.2:7297", "", true, true, true, "http://192.168.1.2:7297/#k=" + testLANToken},
		{"IPv6", "[fd00:0::2]:7297", "[fd00::2]:7297", "", true, true, true, "http://[fd00::2]:7297/#k=" + testLANToken},
		{"disabled", "192.168.1.2:7297", "192.168.1.2:7297", "", false, true, true, ""},
		{"stopped", "192.168.1.2:7297", "192.168.1.2:7297", "", true, false, true, ""},
		{"no allowed peers", "192.168.1.2:7297", "192.168.1.2:7297", "", true, true, false, ""},
		{"bind error", "192.168.1.2:7297", "192.168.1.2:7297", "bind failed", true, true, true, ""},
		{"stale status", "192.168.1.2:7297", "192.168.1.3:7297", "", true, true, true, ""},
		{"wildcard", "[::]:7297", "[::]:7297", "", true, true, true, ""},
		{"loopback", "127.0.0.1:7297", "127.0.0.1:7297", "", true, true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := LANConfig{Enabled: tc.enabled, Listen: tc.listen, Token: testLANToken}
			if tc.allowed {
				config.Allow = []string{"192.168.1.3/32"}
			}
			status := LANStatus{Running: tc.running, Listening: tc.bound, Error: tc.bindError}
			if got := config.PairURL(status); got != tc.want {
				t.Fatalf("PairURL = %q, want %q", got, tc.want)
			}
		})
	}
	// No token, no code: one without it would only be refused.
	c := LANConfig{Enabled: true, Listen: "192.168.1.2:7297", Allow: []string{"192.168.1.0/24"}}
	if got := c.PairURL(LANStatus{Running: true, Listening: "192.168.1.2:7297"}); got != "" {
		t.Errorf("PairURL with no token = %q", got)
	}
}

// TestDefaultLANNeverEveryone keeps the default allowlist to one network,
// never every address.
func TestDefaultLANNeverEveryone(t *testing.T) {
	listen, allow := DefaultLAN()
	if listen == "" {
		t.Skip("this machine has no private IPv4 address")
	}
	if !strings.HasSuffix(listen, fmt.Sprintf(":%d", LANPort)) || len(allow) != 1 {
		t.Fatalf("DefaultLAN = %s, %v", listen, allow)
	}
	p, err := ParseNetwork(allow[0])
	if err != nil || p.Bits() < 8 {
		t.Fatalf("the default network %s is too wide (%v)", allow[0], err)
	}
	host, _, _ := strings.Cut(listen, ":")
	if !p.Contains(netip.MustParseAddr(host)) {
		t.Errorf("the default network %s leaves out the address listened on, %s", p, host)
	}
}

// startStateGateway is startFixture's gateway, with a state directory.
func startStateGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	f := startFixture(t)
	c, err := rex.ConnectSocketKeepalive(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	g, err := New(Options{Client: c, TailnetDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	_ = f
	return g, dir
}

func waitLAN(t *testing.T, dir string, cond func(LANStatus) bool) LANStatus {
	t.Helper()
	var s LANStatus
	waitFor(t, "the local network listener", func() bool {
		s = LoadLANStatus(dir)
		return cond(s)
	})
	return s
}

// TestLANAllowsOnlyItsNetworks serves the local network listener to an
// allowed caller and refuses one in no network allowed, with 403 and a
// JSON error as Rex refuses, changing the networks without closing the
// listener.
func TestLANAllowsOnlyItsNetworks(t *testing.T) {
	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	url := "http://" + s.Listening

	resp, err := http.Get(url + "/api/help?k=" + testLANToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("allowed: status %d", resp.StatusCode)
	}

	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"10.0.0.0/8"}, Token: testLANToken})
	if now := LoadLANStatus(dir).Listening; now != s.Listening {
		t.Fatalf("changing the networks moved the listener from %s to %s", s.Listening, now)
	}
	resp, err = http.Get(url + "/api/help?k=" + testLANToken)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var refusal map[string]string
	if resp.StatusCode != 403 || json.Unmarshal(body, &refusal) != nil || !strings.Contains(refusal["error"], "127.0.0.1") {
		t.Fatalf("refused: status %d, body %s", resp.StatusCode, body)
	}

	// A caller connected as the listener goes off, its connection kept
	// alive after an answer.
	kept, err := net.Dial("tcp", s.Listening)
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	fmt.Fprintf(kept, "GET /api/help HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", s.Listening, testLANToken)
	r := bufio.NewReader(kept)
	resp, err = http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.Close {
		t.Fatal("the server closes the connection after its answer: nothing is kept alive")
	}
	g.lan.mu.Lock()
	ln := g.lan.ln
	g.lan.mu.Unlock()

	g.lan.apply(LANConfig{Enabled: false, Listen: "127.0.0.1:0", Allow: []string{"10.0.0.0/8"}, Token: testLANToken})
	if s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening == "" }); s.Error != "" {
		t.Fatalf("off: %+v", s)
	}
	// The listener itself is asked, not the port: another program
	// listening on the port on every address answers on 127.0.0.1 once
	// the listener lets it go.
	ln.Listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
	if c, err := ln.Accept(); err == nil || os.IsTimeout(err) {
		if c != nil {
			c.Close()
		}
		t.Error("the listener still accepts once off")
	}
	kept.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.ReadByte(); err == nil || os.IsTimeout(err) {
		t.Errorf("a connection kept alive stays once off: %v", err)
	}
}

// TestLANRefusesAdministering keeps server.tailscale.* to the local
// socket: a caller on the local network gets 403.
func TestLANRefusesAdministering(t *testing.T) {
	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.0/8"}, Token: testLANToken})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws://"+s.Listening+"/api/control?k="+testLANToken, &websocket.DialOptions{Subprotocols: []string{controlProtocol}})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	c := newTestConn(t, ws)
	c.hello(7)
	who := result(t, c.request("w", "server.whoami", nil))
	if p, _ := who["principal"].(map[string]any); p["kind"] != "anonymous" || p["remote_addr"] != "127.0.0.1" {
		t.Errorf("whoami principal = %v", who["principal"])
	}
	e := failure(t, c.request("t", "server.tailscale.enable", map[string]any{"allow_tailnet_members": false}))
	if e["status"] != float64(403) {
		t.Errorf("server.tailscale.enable over the local network: %v", e)
	}
}

// shortSocket gives the gateway a socket path short enough for a Unix
// socket, which a test's temporary directory may not be.
func shortSocket(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "gxapi-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	os.Remove(f.Name())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

// TestRunFollowsLANFile changes lan.json under a running gateway, which
// listens and stops within a second or so, and keeps a second gateway
// off the same state.
func TestRunFollowsLANFile(t *testing.T) {
	startFixture(t)
	dir := t.TempDir()
	c, err := rex.ConnectSocketKeepalive(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- run(c, runOptions{socket: shortSocket(t), dir: dir, stop: stop}) }()
	defer func() {
		close(stop)
		<-done
	}()
	if err := SaveLAN(dir, LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })

	c2, err := rex.ConnectSocketKeepalive(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := run(c2, runOptions{socket: shortSocket(t), dir: dir, stop: make(chan struct{})}); err != errGatewayRuns {
		t.Errorf("a second gateway on the same state: %v, want errGatewayRuns", err)
	}

	time.Sleep(20 * time.Millisecond) // a new modification time
	if err := SaveLAN(dir, LANConfig{Enabled: false, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	waitLAN(t, dir, func(s LANStatus) bool { return s.Running && s.Listening == "" })
}

// TestServerRunsGateway runs the gateway in the GoRex server (OnServe)
// while the local network is on: it comes with the server, and again
// with the server that replaces it.
func TestServerRunsGateway(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	t.Setenv("GOREX_REXAPI_SOCKET", shortSocket(t))
	state := TailnetDir()
	if err := SaveLAN(state, LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	old := rex.OnServe
	rex.OnServe = Supervise
	t.Cleanup(func() { rex.OnServe = old })
	for run := 1; run <= 2; run++ {
		os.Remove(lanStatusPath(state))
		served := make(chan error, 1)
		go func() { served <- rex.Serve() }()
		s := waitLAN(t, state, func(s LANStatus) bool { return s.Listening != "" })
		if !gatewayRuns() {
			t.Fatalf("run %d: no gateway on the local socket", run)
		}
		conf, _ := LoadLAN(state)
		resp, err := http.Get("http://" + s.Listening + "/api/help?k=" + conf.Token)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("run %d: %v %v", run, resp, err)
		}
		resp.Body.Close()
		c, err := rex.ConnectSocket(rex.SocketPath())
		if err != nil {
			t.Fatal(err)
		}
		c.Shutdown()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Fatalf("run %d: the server did not stop", run)
		}
		waitFor(t, "the gateway to stop with the server", func() bool { return !gatewayRuns() })
	}
	// Off, the server runs no gateway. Supervise looks again every tick
	// and has none to start: wait for many ticks, not for the real ones.
	superviseEveryNS.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() { superviseEveryNS.Store(0) })
	if err := SaveLAN(state, LANConfig{Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- rex.Serve() }()
	waitFor(t, "the server", func() bool { _, err := os.Stat(rex.SocketPath()); return err == nil })
	time.Sleep(200 * time.Millisecond) // about twenty ticks of Supervise
	if gatewayRuns() {
		t.Error("the server runs a gateway with nothing to serve")
	}
	c, _ := rex.ConnectSocket(rex.SocketPath())
	c.Shutdown()
	<-served
}

// TestStatusListsListeners checks revision 12's listeners: the local
// socket first and alone primary, then --listen's port with the port it
// got, then the local network's listener while it listens; a revision 11
// client sees no list in server.status or session.bootstrap.
func TestStatusListsListeners(t *testing.T) {
	startFixture(t)
	dir := t.TempDir()
	c, err := rex.ConnectSocketKeepalive(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	sock := shortSocket(t)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- run(c, runOptions{socket: sock, dir: dir, listen: "127.0.0.1:0", stop: stop})
	}()
	defer func() {
		close(stop)
		<-done
	}()
	waitFor(t, "the local socket", func() bool {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			conn.Close()
		}
		return err == nil
	})
	dial := func(rev int) *testConn {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpc := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}}
		ws, resp, err := websocket.Dial(ctx, "ws://localhost/api/control", &websocket.DialOptions{HTTPClient: httpc, Subprotocols: []string{controlProtocol}})
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ws.CloseNow() })
		conn := newTestConn(t, ws)
		conn.hello(rev)
		return conn
	}
	listeners := func(conn *testConn) []map[string]any {
		t.Helper()
		var out []map[string]any
		st := result(t, conn.request("s", "server.status", nil))
		checkResult(t, "server.status", st)
		for _, l := range st["listeners"].([]any) {
			out = append(out, l.(map[string]any))
		}
		return out
	}

	// The socket serves before --listen's port is registered (serve.go):
	// a client that reaches the first asks again until both are listed.
	cur := dial(apiRevision)
	var got []map[string]any
	waitFor(t, "the socket and --listen's listeners", func() bool {
		got = listeners(cur)
		return len(got) == 2
	})
	if got[0]["endpoint"] != unixEndpoint(sock) || got[0]["transport"] != "unix" || got[0]["primary"] != true ||
		got[0]["addresses"].([]any)[0] != sock || got[0]["admission"] != nilIfEmpty(unixAdmission) {
		t.Errorf("primary = %v", got[0])
	}
	tcpAddr, _ := got[1]["addresses"].([]any)[0].(string)
	if got[1]["endpoint"] != "127.0.0.1:0" || got[1]["transport"] != "tcp" || got[1]["admission"] != "anonymous" ||
		got[1]["primary"] != nil || tcpAddr == "127.0.0.1:0" || !strings.HasPrefix(tcpAddr, "127.0.0.1:") {
		t.Errorf("--listen = %v", got[1])
	}
	t.Setenv("GOREX_REXAPI_SOCKET", sock)
	if info, err := GatewayStatus(); err != nil || len(info.Listeners) != 2 || !info.Listeners[0].Primary ||
		info.Listeners[1].Addresses[0] != tcpAddr || info.APIRevision != apiRevision || info.RexVersion == "" {
		t.Errorf("GatewayStatus = %+v, %v", info, err)
	}

	if err := SaveLAN(dir, LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	got = listeners(cur)
	if len(got) != 3 || got[2]["endpoint"] != "127.0.0.1:0" || got[2]["addresses"].([]any)[0] != s.Listening ||
		got[2]["transport"] != "tcp" || got[2]["admission"] != "anonymous" || got[2]["primary"] != nil {
		t.Fatalf("listeners with the local network = %v, want it third at %s", got, s.Listening)
	}
	primaries := 0
	for _, l := range got {
		if l["primary"] == true {
			primaries++
		}
	}
	if primaries != 1 {
		t.Errorf("%d primaries in %v", primaries, got)
	}
	boot := result(t, cur.request("b", "session.bootstrap", map[string]any{"event_scope": "all"}))
	checkResult(t, "session.bootstrap", boot)
	if l, _ := boot["server_status"].(map[string]any)["listeners"].([]any); len(l) != 3 {
		t.Errorf("bootstrap's listeners = %v", l)
	}

	old := dial(11)
	if st := result(t, old.request("s", "server.status", nil)); st["listeners"] != nil {
		t.Errorf("revision 11 sees server.status's listeners: %v", st["listeners"])
	}
	boot = result(t, old.request("b", "session.bootstrap", map[string]any{"event_scope": "all"}))
	if ss := boot["server_status"].(map[string]any); ss["listeners"] != nil || ss["pid"] == nil {
		t.Errorf("revision 11 bootstrap's server_status = %v", ss)
	}

	time.Sleep(20 * time.Millisecond) // a new modification time
	if err := SaveLAN(dir, LANConfig{Enabled: false, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	waitLAN(t, dir, func(s LANStatus) bool { return s.Running && s.Listening == "" })
	if got := listeners(cur); len(got) != 2 {
		t.Errorf("listeners once the local network is off = %v", got)
	}
}

// nilIfEmpty is how JSON decodes a member omitempty left out.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TestUnixEndpointEscapes checks that the primary endpoint is written as
// Rex writes its own, with a space in the path escaped.
func TestUnixEndpointEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Unix path")
	}
	got := unixEndpoint("/Users/me/Library/Application Support/GoRex/rexapi.sock")
	if want := "unix:///Users/me/Library/Application%20Support/GoRex/rexapi.sock"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// testLANToken is a pairing token for tests; a real one is 43 random
// characters (NewLANToken).
const testLANToken = "test-token-0123456789abcdefghijklmnopqrstuv"

// lanGet asks the listener for url with the headers given, as name and
// value pairs, and returns the status and the body.
func lanGet(t *testing.T, url string, header ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// dialLANControl opens a control connection on the listener at addr,
// with query added to its path, and says hello.
func dialLANControl(t *testing.T, addr, query string) (*testConn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws://"+addr+"/api/control"+query, &websocket.DialOptions{Subprotocols: []string{controlProtocol}})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { ws.CloseNow() })
	c := newTestConn(t, ws)
	c.hello(apiRevision)
	return c, nil
}

// TestLANNeedsToken lets in a caller from a network allowed only with
// the pairing token, as a Bearer header or as k; any other gets 403 and
// is told how to get in.
func TestLANNeedsToken(t *testing.T) {
	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	help := "http://" + s.Listening + "/api/help"

	for _, tc := range []struct {
		name   string
		url    string
		header []string
		want   int
	}{
		{"no token", help, nil, 403},
		{"wrong Bearer", help, []string{"Authorization", "Bearer wrong"}, 403},
		{"wrong k", help + "?k=wrong", nil, 403},
		{"a prefix of the token", help + "?k=" + testLANToken[:10], nil, 403},
		{"Basic", help, []string{"Authorization", "Basic " + testLANToken}, 403},
		{"Bearer", help, []string{"Authorization", "Bearer " + testLANToken}, 200},
		{"bearer", help, []string{"Authorization", "bearer " + testLANToken}, 200},
		{"k", help + "?k=" + testLANToken, nil, 200},
	} {
		code, body := lanGet(t, tc.url, tc.header...)
		if code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, code, tc.want, body)
		}
		if code == 403 && (!strings.Contains(body, "re-scan the pairing QR code") || !strings.Contains(body, "gorex rexapi lan legacy add 127.0.0.1")) {
			t.Errorf("%s: the refusal does not say how to get in: %s", tc.name, body)
		}
	}

	if _, err := dialLANControl(t, s.Listening, ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("a WebSocket with no token: %v, want 403", err)
	}
	c, err := dialLANControl(t, s.Listening, "?k="+testLANToken)
	if err != nil {
		t.Fatalf("a WebSocket with k: %v", err)
	}
	result(t, c.request("v", "server.version", nil))
}

// TestLANTokenNotLogged keeps the token in k out of the log, the wire
// trace among it.
func TestLANTokenNotLogged(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	old, oldTracing := log.Writer(), tracing.Load()
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	tracing.Store(true)
	t.Cleanup(func() { log.SetOutput(old); tracing.Store(oldTracing) })

	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	const wrong = "wrong-token-that-must-not-be-logged"
	if code, _ := lanGet(t, "http://"+s.Listening+"/api/help?k="+testLANToken+"&x=1"); code != 200 {
		t.Fatalf("with k: %d", code)
	}
	if code, _ := lanGet(t, "http://"+s.Listening+"/api/help?k="+wrong); code != 403 {
		t.Fatalf("with a wrong k: %d", code)
	}
	mu.Lock()
	logged := buf.String()
	mu.Unlock()
	if !strings.Contains(logged, "/api/help?x=1") {
		t.Errorf("the trace does not show the request without its token:\n%s", logged)
	}
	if strings.Contains(logged, testLANToken) || strings.Contains(logged, wrong) {
		t.Errorf("a token reached the log:\n%s", logged)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// TestLANLegacyOpen lets an address on the legacy list in without the
// token, while it stays in a network allowed; a network is not taken on
// the list.
func TestLANLegacyOpen(t *testing.T) {
	g, dir := startStateGateway(t)
	conf := LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken, LegacyOpen: []string{"127.0.0.1"}}
	g.lan.apply(conf)
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	help := "http://" + s.Listening + "/api/help"
	if code, body := lanGet(t, help); code != 200 {
		t.Fatalf("legacy, no token: %d %s", code, body)
	}
	// Out of the networks allowed, the legacy list lets no one in.
	conf.Allow = []string{"10.0.0.0/8"}
	g.lan.apply(conf)
	if code, _ := lanGet(t, help); code != 403 {
		t.Errorf("legacy out of the networks allowed: %d", code)
	}
	conf.Allow, conf.LegacyOpen = []string{"127.0.0.1"}, nil
	g.lan.apply(conf)
	if code, _ := lanGet(t, help); code != 403 {
		t.Errorf("off the legacy list, no token: %d", code)
	}

	for _, ok := range [][]string{{"10.0.0.5"}, {"10.0.0.5/32"}, {"fd00::5"}, {"fd00::5/128"}} {
		if err := SaveLAN(t.TempDir(), LANConfig{LegacyOpen: ok}); err != nil {
			t.Errorf("legacy %v: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"10.0.0.0/24"}, {"0.0.0.0/0"}, {"fd00::/64"}, {"phone"}} {
		if err := SaveLAN(t.TempDir(), LANConfig{LegacyOpen: bad}); err == nil {
			t.Errorf("legacy %v was taken", bad)
		}
	}
}

// TestLANMigratesToToken gives an enabled lan.json from before the token
// one on the gateway's first look, saved for this user only, and refuses
// a caller without it from then on.
func TestLANMigratesToToken(t *testing.T) {
	g, dir := startStateGateway(t)
	old := []byte(`{"enabled": true, "listen": "127.0.0.1:0", "allow": ["127.0.0.1/32"]}`)
	if err := os.WriteFile(lanConfPath(dir), old, 0o644); err != nil {
		t.Fatal(err)
	}
	conf, err := LoadLAN(dir)
	if err != nil || conf.Token != "" {
		t.Fatalf("the old file: %+v, %v", conf, err)
	}
	g.lan.apply(conf)
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	saved, err := LoadLAN(dir)
	if err != nil || len(saved.Token) != 43 {
		t.Fatalf("after the gateway's look: %+v, %v; want a token", saved, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(lanConfPath(dir)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("lan.json: %v, %v; want mode 0600", fi, err)
		}
	}
	help := "http://" + s.Listening + "/api/help"
	if code, body := lanGet(t, help); code != 403 || !strings.Contains(body, "legacy add") {
		t.Errorf("no token after the upgrade: %d %s", code, body)
	}
	if code, _ := lanGet(t, help, "Authorization", "Bearer "+saved.Token); code != 200 {
		t.Errorf("the new token: %d", code)
	}
	// The file read again, as the gateway does when it changes, keeps
	// the listener and its token.
	g.lan.apply(saved)
	if code, _ := lanGet(t, help+"?k="+saved.Token); code != 200 {
		t.Errorf("the token after the file is read again: %d", code)
	}
}

// TestLANRotateClosesConnections gives the listener a new token: every
// connection made with the old one closes at once, the old token is
// refused and the new one let in, on the same address.
func TestLANRotateClosesConnections(t *testing.T) {
	g, dir := startStateGateway(t)
	if err := SaveLAN(dir, LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := LoadLAN(dir)
	g.lan.apply(before)
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })

	ws, err := dialLANControl(t, s.Listening, "?k="+before.Token)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := net.Dial("tcp", s.Listening)
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	fmt.Fprintf(kept, "GET /api/help HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", s.Listening, before.Token)
	r := bufio.NewReader(kept)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("kept alive: %v %v", resp, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	after, err := RotateLANToken(dir)
	if err != nil || after.Token == before.Token || after.Token == "" {
		t.Fatalf("RotateLANToken = %+v, %v", after, err)
	}
	if saved, _ := LoadLAN(dir); saved.Token != after.Token {
		t.Fatalf("the new token was not saved")
	}
	g.lan.apply(after)

	closed := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-ws.msgs:
		case <-closed:
			t.Fatal("the WebSocket made with the old token stays open")
		}
	}
	kept.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.ReadByte(); err == nil || os.IsTimeout(err) {
		t.Errorf("the connection kept alive stays open: %v", err)
	}
	if now := LoadLANStatus(dir).Listening; now != s.Listening {
		t.Errorf("the listener moved from %s to %s", s.Listening, now)
	}
	help := "http://" + s.Listening + "/api/help"
	if code, _ := lanGet(t, help+"?k="+before.Token); code != 403 {
		t.Errorf("the old token: %d", code)
	}
	if code, _ := lanGet(t, help+"?k="+after.Token); code != 200 {
		t.Errorf("the new token: %d", code)
	}
}

// TestWriteJSONWhileRead writes a file that a reader has open, as the
// app's poll has lan-status.json open: on Windows the rename fails until
// the reader closes it, and the write must not be lost.
func TestWriteJSONWhileRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lan-status.json")
	if err := writeJSON(path, LANStatus{Running: true}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.Close()
	}()
	if err := writeJSON(path, LANStatus{Running: true, Listening: "192.0.2.10:7297"}); err != nil {
		t.Fatalf("a write while the file is read: %v", err)
	}
	if s := LoadLANStatus(filepath.Dir(path)); s.Listening != "192.0.2.10:7297" {
		t.Errorf("the write was lost: %+v", s)
	}
}

// TestLANLegacyMappedAddress takes an IPv4 address written as IPv6, as
// ::ffff:a.b.c.d/128, for the IPv4 address a caller comes from.
func TestLANLegacyMappedAddress(t *testing.T) {
	for in, want := range map[string]string{
		"::ffff:192.0.2.5/128": "192.0.2.5",
		"::ffff:192.0.2.5":     "192.0.2.5",
		"192.0.2.5/32":         "192.0.2.5",
		"fd00::5/128":          "fd00::5",
	} {
		if a, err := ParseLegacyAddr(in); err != nil || a.String() != want {
			t.Errorf("ParseLegacyAddr(%q) = %v, %v; want %s", in, a, err, want)
		}
	}
	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken, LegacyOpen: []string{"::ffff:127.0.0.1/128"}})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	if code, body := lanGet(t, "http://"+s.Listening+"/api/help"); code != 200 {
		t.Errorf("a mapped legacy address, no token: %d %s", code, body)
	}
}

// TestLANMigrationKeepsConcurrentToken: a token another writer saved in
// lan.json while the gateway applied a configuration without one is the
// token the gateway uses, and the file keeps what that writer saved.
func TestLANMigrationKeepsConcurrentToken(t *testing.T) {
	g, dir := startStateGateway(t)
	saved := LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken, LegacyOpen: []string{"192.0.2.9"}}
	if err := SaveLAN(dir, saved); err != nil {
		t.Fatal(err)
	}
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	if code, _ := lanGet(t, "http://"+s.Listening+"/api/help?k="+testLANToken); code != 200 {
		t.Errorf("the token saved meanwhile: %d", code)
	}
	if got, _ := LoadLAN(dir); got.Token != testLANToken || len(got.LegacyOpen) != 1 {
		t.Errorf("lan.json after the gateway's look: %+v", got)
	}
}

// TestLANMigrationUnsavedTokenStaysOff: a token the gateway cannot save
// is one no device can learn; the listener stays off and says why,
// instead of serving with it.
func TestLANMigrationUnsavedTokenStaysOff(t *testing.T) {
	g, dir := startStateGateway(t)
	// A directory where the file's temporary copy goes makes the write fail.
	if err := os.MkdirAll(lanConfPath(dir)+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Running })
	if s.Listening != "" || !strings.Contains(s.Error, "pairing token") {
		t.Errorf("status = %+v; want off, with the token's error", s)
	}
	g.lan.mu.Lock()
	token := g.lan.conf.Token
	g.lan.mu.Unlock()
	if token != "" {
		t.Errorf("the listener holds a token it could not save")
	}
}

// TestWebPairURL: the page's link carries the gateway's address, its
// certificate's hash and the pairing token in the fragment, and nothing
// without a listener that can be reached there.
func TestWebPairURL(t *testing.T) {
	c := LANConfig{Token: "t0ken"}
	s := LANStatus{Running: true, WTListening: "192.168.1.20:7298", WTCertHash: "abc"}
	want := "https://web.gorex.uk/#host=https%3A%2F%2F192.168.1.20%3A7298&wt=sha256-abc&k=t0ken"
	if got := c.WebPairURL(s, WebOrigin); got != want {
		t.Fatalf("WebPairURL = %q, want %q", got, want)
	}
	if got := c.WebPairURL(s, "https://web.gorex.uk/"); got != want {
		t.Errorf("a trailing slash changed the link: %q", got)
	}
	for name, tc := range map[string]struct {
		c LANConfig
		s LANStatus
	}{
		"no token":     {LANConfig{}, s},
		"listener off": {c, LANStatus{}},
		"no hash":      {c, LANStatus{Running: true, WTListening: "192.168.1.20:7298"}},
		"no address":   {c, LANStatus{Running: true, WTCertHash: "abc"}},
		"an error":     {c, LANStatus{Running: true, WTCertHash: "abc", WTListening: "192.168.1.20:7298", Error: "address already in use"}},
	} {
		if got := tc.c.WebPairURL(tc.s, WebOrigin); got != "" {
			t.Errorf("%s: WebPairURL = %q, want none", name, got)
		}
	}
}
