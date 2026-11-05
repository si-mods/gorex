package rexapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
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
	if err != nil || !sameLAN(got, want) {
		t.Fatalf("LoadLAN = %+v, %v; want %+v", got, err, want)
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
		{"Tailscale", "100.64.1.2:7297", "100.64.1.2:7297", "", true, true, true, "http://100.64.1.2:7297"},
		{"LAN", "192.168.1.2:7297", "192.168.1.2:7297", "", true, true, true, "http://192.168.1.2:7297"},
		{"IPv6", "[fd00:0::2]:7297", "[fd00::2]:7297", "", true, true, true, "http://[fd00::2]:7297"},
		{"disabled", "100.64.1.2:7297", "100.64.1.2:7297", "", false, true, true, ""},
		{"stopped", "100.64.1.2:7297", "100.64.1.2:7297", "", true, false, true, ""},
		{"no allowed peers", "100.64.1.2:7297", "100.64.1.2:7297", "", true, true, false, ""},
		{"bind error", "100.64.1.2:7297", "100.64.1.2:7297", "bind failed", true, true, true, ""},
		{"stale status", "100.64.1.2:7297", "100.64.1.3:7297", "", true, true, true, ""},
		{"wildcard", "[::]:7297", "[::]:7297", "", true, true, true, ""},
		{"loopback", "127.0.0.1:7297", "127.0.0.1:7297", "", true, true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := LANConfig{Enabled: tc.enabled, Listen: tc.listen}
			if tc.allowed {
				config.Allow = []string{"100.64.1.3/32"}
			}
			status := LANStatus{Running: tc.running, Listening: tc.bound, Error: tc.bindError}
			if got := config.PairURL(status); got != tc.want {
				t.Fatalf("PairURL = %q, want %q", got, tc.want)
			}
		})
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
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	url := "http://" + s.Listening

	resp, err := http.Get(url + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("allowed: status %d", resp.StatusCode)
	}

	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"10.0.0.0/8"}})
	if now := LoadLANStatus(dir).Listening; now != s.Listening {
		t.Fatalf("changing the networks moved the listener from %s to %s", s.Listening, now)
	}
	resp, err = http.Get(url + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var refusal map[string]string
	if resp.StatusCode != 403 || json.Unmarshal(body, &refusal) != nil || !strings.Contains(refusal["error"], "127.0.0.1") {
		t.Fatalf("refused: status %d, body %s", resp.StatusCode, body)
	}

	g.lan.apply(LANConfig{Enabled: false, Listen: "127.0.0.1:0", Allow: []string{"10.0.0.0/8"}})
	if s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening == "" }); s.Error != "" {
		t.Fatalf("off: %+v", s)
	}
	if _, err := http.Get(url + "/api/help"); err == nil {
		t.Error("the listener still answers once off")
	}
}

// TestLANRefusesAdministering keeps server.tailscale.* to the local
// socket: a caller on the local network gets 403.
func TestLANRefusesAdministering(t *testing.T) {
	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.0/8"}})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+s.Listening+"/api/control", &websocket.DialOptions{Subprotocols: []string{controlProtocol}})
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
	f, err := os.CreateTemp("/tmp", "gxapi-*.sock")
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
		resp, err := http.Get("http://" + s.Listening + "/api/help")
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
	// Off, the server runs no gateway.
	if err := SaveLAN(state, LANConfig{Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- rex.Serve() }()
	waitFor(t, "the server", func() bool { _, err := os.Stat(rex.SocketPath()); return err == nil })
	time.Sleep(2 * time.Second)
	if gatewayRuns() {
		t.Error("the server runs a gateway with nothing to serve")
	}
	c, _ := rex.ConnectSocket(rex.SocketPath())
	c.Shutdown()
	<-served
}
