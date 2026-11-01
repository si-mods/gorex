package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"rsc.io/qr"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/views"
)

// fakeNode is a Tailscale device a test drives: its state, who is
// calling, and when its HTTPS listener is ready.
type fakeNode struct {
	mu       sync.Mutex
	hostname string
	authKey  string
	tags     []string
	state    string
	authURL  string
	owner    tailcfg.UserID
	callers  map[string]*apitype.WhoIsResponse // by remote host
	changed  func()
	ready    chan struct{}
	ln       net.Listener
	closed   bool
	logins   int
	logouts  int
}

func (n *fakeNode) Start() error { return nil }

func (n *fakeNode) Status(context.Context) (*ipnstate.Status, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	self := &ipnstate.PeerStatus{DNSName: n.hostname + ".example.ts.net.", UserID: n.owner}
	if len(n.tags) > 0 {
		v := views.SliceOf(n.tags)
		self.Tags = &v
	}
	return &ipnstate.Status{
		BackendState:   n.state,
		AuthURL:        n.authURL,
		Self:           self,
		CurrentTailnet: &ipnstate.TailnetStatus{Name: "example"},
		User:           map[tailcfg.UserID]tailcfg.UserProfile{n.owner: {ID: n.owner, LoginName: "owner@example.com"}},
	}, nil
}

func (n *fakeNode) Watch(ctx context.Context, changed func()) {
	n.mu.Lock()
	n.changed = changed
	n.mu.Unlock()
	<-ctx.Done()
}

func (n *fakeNode) ListenTLS() (net.Listener, error) {
	<-n.ready
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, errors.New("closed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	n.ln = ln
	return ln, err
}

func (n *fakeNode) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	host, _, _ := net.SplitHostPort(addr)
	n.mu.Lock()
	defer n.mu.Unlock()
	if w := n.callers[host]; w != nil {
		return w, nil
	}
	return nil, errors.New("no such peer")
}

func (n *fakeNode) StartLogin(context.Context) error {
	n.mu.Lock()
	n.logins++
	n.mu.Unlock()
	return nil
}

func (n *fakeNode) Logout(context.Context) error {
	n.mu.Lock()
	n.logouts++
	n.mu.Unlock()
	return nil
}

func (n *fakeNode) SetHostname(_ context.Context, name string) error {
	n.mu.Lock()
	n.hostname = name
	n.mu.Unlock()
	return nil
}

func (n *fakeNode) Close() error {
	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()
	return nil
}

// set changes the device's state and tells the gateway, as tsnet's bus
// does.
func (n *fakeNode) set(state, authURL string) {
	n.mu.Lock()
	n.state, n.authURL = state, authURL
	changed := n.changed
	n.mu.Unlock()
	if changed != nil {
		changed()
	}
}

func (n *fakeNode) addr() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ln == nil {
		return ""
	}
	return n.ln.Addr().String()
}

// tailnetFixture is a gateway with a fake device and a local socket.
type tailnetFixture struct {
	*fixture
	node *fakeNode
	sock string
	dir  string
}

func startTailnetFixture(t *testing.T) *tailnetFixture {
	t.Helper()
	f := startFixture(t)
	node := &fakeNode{state: "NoState", owner: 7, callers: map[string]*apitype.WhoIsResponse{}, ready: make(chan struct{})}
	dir := t.TempDir()
	f.g.tailnet = newTailnet(f.g, dir, func(_, hostname, authKey string, tags []string) tailnetNode {
		node.mu.Lock()
		node.hostname, node.authKey, node.tags, node.closed = hostname, authKey, tags, false
		node.mu.Unlock()
		return node
	})
	f.g.tailnet.serve = f.g.serveOn
	// A Unix socket's path is short on macOS: the test's own directory is
	// too long for it.
	sdir, err := os.MkdirTemp("", "rx")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sdir, "g.sock")
	ln, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	go f.g.serveOn(ln, transportUnix)
	t.Cleanup(func() {
		ln.Close()
		os.RemoveAll(sdir)
	})
	return &tailnetFixture{fixture: f, node: node, sock: sock, dir: dir}
}

func (f *tailnetFixture) call(method string, payload any) (map[string]any, error) {
	f.t.Helper()
	raw, err := callGateway(f.sock, method, payload)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatal(err)
	}
	return out, nil
}

func (f *tailnetFixture) mustCall(method string, payload any) map[string]any {
	f.t.Helper()
	out, err := f.call(method, payload)
	if err != nil {
		f.t.Fatalf("%s: %v", method, err)
	}
	checkResult(f.t, method, out)
	return out
}

func tailscaleOf(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	ts, ok := res["tailscale"].(map[string]any)
	if !ok {
		t.Fatalf("no tailscale in %v", res)
	}
	return ts
}

// TestTailscaleOnlyLocal: the administering methods are refused over
// TCP with 403, as Rex refuses them over anything but its local socket.
func TestTailscaleOnlyLocal(t *testing.T) {
	f := startTailnetFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	for _, m := range []string{"server.tailscale.enable", "server.tailscale.disable", "server.tailscale.login", "server.tailscale.logout", "server.tailscale.rename"} {
		e := failure(t, c.request(m, m, map[string]any{"allow_tailnet_members": false, "hostname": "x"}))
		if e["code"] != "internal" || e["status"] != float64(403) {
			t.Errorf("%s over TCP: %v", m, e)
		}
	}
	// server.status reports built-in Tailscale to anyone.
	st := result(t, c.request("s", "server.status", nil))
	if ts, _ := st["tailscale"].(map[string]any); ts == nil || ts["state"] != "disabled" {
		t.Errorf("server.status tailscale: %v", st["tailscale"])
	}
}

// TestTailscaleWithout: a gateway with no built-in Tailscale answers
// not_supported, and server.status has no tailscale.
func TestTailscaleWithout(t *testing.T) {
	f := startTailnetFixture(t)
	f.g.tailnet = nil
	if _, err := f.call("server.tailscale.disable", nil); err == nil || !strings.HasPrefix(err.Error(), "not_supported") {
		t.Errorf("disable without Tailscale: %v", err)
	}
	st := f.mustCall("server.status", nil)
	if _, ok := st["tailscale"]; ok {
		t.Errorf("server.status has tailscale: %v", st["tailscale"])
	}
}

// TestTailscaleLifecycle turns built-in Tailscale on, signs it in,
// serves HTTPS, renames it and turns it off, over the local socket, and
// watches the events a client attached to the server gets.
func TestTailscaleLifecycle(t *testing.T) {
	f := startTailnetFixture(t)
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "session"})

	if _, err := f.call("server.tailscale.login", nil); err == nil || !strings.HasPrefix(err.Error(), "conflict") {
		t.Errorf("login while off: %v", err)
	}
	if _, err := f.call("server.tailscale.rename", map[string]any{"hostname": "studio"}); err == nil || !strings.HasPrefix(err.Error(), "conflict") {
		t.Errorf("rename while off: %v", err)
	}
	if _, err := f.call("server.tailscale.enable", map[string]any{"allow_tailnet_members": false, "advertise_tags": []string{"tag:rex"}}); err == nil || !strings.HasPrefix(err.Error(), "bad_request") {
		t.Errorf("tags without allow_tailnet_members: %v", err)
	}
	if _, err := f.call("server.tailscale.enable", map[string]any{"allow_tailnet_members": true, "advertise_tags": []string{"rex"}}); err == nil || !strings.HasPrefix(err.Error(), "bad_request") {
		t.Errorf("a tag without tag: %v", err)
	}
	if _, err := f.call("server.tailscale.enable", map[string]any{}); err == nil || !strings.HasPrefix(err.Error(), "bad_request") {
		t.Errorf("enable without allow_tailnet_members: %v", err)
	}

	f.node.state, f.node.authURL = "NeedsLogin", "https://login.example/abc"
	ts := tailscaleOf(t, f.mustCall("server.tailscale.enable", map[string]any{"allow_tailnet_members": false, "hostname": "dev", "auth_key": "tskey-secret"}))
	if ts["state"] != "needs_login" || ts["auth_url"] != "https://login.example/abc" || ts["allow_tailnet_members"] != false {
		t.Errorf("enable: %v", ts)
	}
	if f.node.hostname != "dev" || f.node.authKey != "tskey-secret" {
		t.Errorf("device made as %q with key %q", f.node.hostname, f.node.authKey)
	}
	conf, err := os.ReadFile(filepath.Join(f.dir, "tailscale.json"))
	if err != nil || strings.Contains(string(conf), "tskey") || !strings.Contains(string(conf), `"enabled": true`) {
		t.Errorf("saved settings: %s %v", conf, err)
	}
	if ev := serverEvent(t, obs, "tailscale_status_changed"); ev["state"] != "needs_login" {
		t.Errorf("first event: %v", ev)
	}

	f.mustCall("server.tailscale.login", nil)
	if f.node.logins != 1 {
		t.Errorf("%d logins", f.node.logins)
	}

	// The device signs in, then its HTTPS listener comes up.
	f.node.set("Running", "")
	close(f.node.ready)
	waitFor(t, "https", func() bool {
		ts := tailscaleOf(t, f.mustCall("server.status", nil))
		h, _ := ts["https"].(map[string]any)
		return h["state"] == "available"
	})
	ts = tailscaleOf(t, f.mustCall("server.status", nil))
	if ts["state"] != "running" || ts["dns_name"] != "dev.example.ts.net" || ts["tailnet_name"] != "example" {
		t.Errorf("running: %v", ts)
	}
	if a, _ := ts["account"].(map[string]any); a["login_name"] != "owner@example.com" {
		t.Errorf("account: %v", ts["account"])
	}
	if h, _ := ts["https"].(map[string]any); h["url"] != "https://dev.example.ts.net" {
		t.Errorf("https: %v", ts["https"])
	}
	for {
		ev := serverEvent(t, obs, "tailscale_status_changed")
		if h, _ := ev["https"].(map[string]any); h["state"] == "available" {
			break
		}
	}
	if url, err := pairURL(statusOfMap(t, ts)); err != nil || url != "https://dev.example.ts.net" {
		t.Errorf("pair URL: %q %v", url, err)
	}

	// Enabling again only applies allow_tailnet_members.
	ts = tailscaleOf(t, f.mustCall("server.tailscale.enable", map[string]any{"allow_tailnet_members": true, "hostname": "other"}))
	if ts["allow_tailnet_members"] != true || f.node.hostname != "dev" {
		t.Errorf("enable again: %v, hostname %q", ts, f.node.hostname)
	}

	if _, err := f.call("server.tailscale.rename", map[string]any{"hostname": "-bad"}); err == nil || !strings.HasPrefix(err.Error(), "bad_request") {
		t.Errorf("bad name: %v", err)
	}
	f.mustCall("server.tailscale.rename", map[string]any{"hostname": "studio"})
	if f.node.hostname != "studio" {
		t.Errorf("renamed to %q", f.node.hostname)
	}

	ts = tailscaleOf(t, f.mustCall("server.tailscale.disable", nil))
	if ts["state"] != "disabled" || !f.node.closed {
		t.Errorf("disable: %v, closed %v", ts, f.node.closed)
	}
	if _, err := pairURL(statusOfMap(t, ts)); err == nil {
		t.Error("a pair URL while off")
	}
	conf, _ = os.ReadFile(filepath.Join(f.dir, "tailscale.json"))
	if !strings.Contains(string(conf), `"enabled": false`) || !strings.Contains(string(conf), `"hostname": "studio"`) {
		t.Errorf("saved after disable: %s", conf)
	}
	// Disabling again is safe, and logging out while off changes nothing.
	f.mustCall("server.tailscale.disable", nil)
	f.mustCall("server.tailscale.logout", nil)
	if f.node.logouts != 0 {
		t.Errorf("logged out while off")
	}
}

func statusOfMap(t *testing.T, m map[string]any) *tailscaleStatus {
	t.Helper()
	b, _ := json.Marshal(m)
	var st tailscaleStatus
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

// TestTailnetAccess: over the tailnet, the user who signed the device in
// gets in and others get 403, unless allow_tailnet_members lets in every
// member; a tagged caller is a machine, and needs it too. A caller let
// in is identified by its Tailscale login.
func TestTailnetAccess(t *testing.T) {
	f := startTailnetFixture(t)
	f.node.state = "Running"
	close(f.node.ready)
	f.mustCall("server.tailscale.enable", map[string]any{"allow_tailnet_members": false})
	waitFor(t, "listener", func() bool { return f.node.addr() != "" })
	addr := f.node.addr()

	get := func() (int, string) {
		resp, err := http.Get("http://" + addr + "/api/help")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(); code != 403 || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown caller: %d %s", code, body)
	}
	set := func(w *apitype.WhoIsResponse) {
		f.node.mu.Lock()
		f.node.callers["127.0.0.1"] = w
		f.node.mu.Unlock()
	}
	set(&apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: "phone"}, UserProfile: &tailcfg.UserProfile{ID: 8, LoginName: "other@example.com"}})
	if code, _ := get(); code != 403 {
		t.Errorf("another user: %d", code)
	}
	set(&apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: "ci", Tags: []string{"tag:ci"}}, UserProfile: &tailcfg.UserProfile{ID: 7, LoginName: "tagged-devices"}})
	if code, _ := get(); code != 403 {
		t.Errorf("a tagged caller: %d", code)
	}
	set(&apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: "phone"}, UserProfile: &tailcfg.UserProfile{ID: 7, LoginName: "owner@example.com"}})
	if code, _ := get(); code != 200 {
		t.Errorf("the owner: %d", code)
	}

	// The owner's control connection is a Tailscale principal.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+addr+"/api/control", &websocket.DialOptions{Subprotocols: []string{controlProtocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	c := newTestConn(t, ws)
	c.hello(apiRevision)
	who := result(t, c.request("w", "server.whoami", nil))
	p, _ := who["principal"].(map[string]any)
	if p["kind"] != "tailscale" || p["transport"] != "tailscale" || p["login_name"] != "owner@example.com" || p["user_id"] != "7" || p["node"] != "phone" {
		t.Errorf("principal: %v", p)
	}
	checkResult(t, "server.whoami", who)
	// And may not administer the server.
	if e := failure(t, c.request("d", "server.tailscale.disable", nil)); e["status"] != float64(403) {
		t.Errorf("disable over the tailnet: %v", e)
	}

	f.mustCall("server.tailscale.enable", map[string]any{"allow_tailnet_members": true})
	set(&apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: "ci", Tags: []string{"tag:ci"}}, UserProfile: &tailcfg.UserProfile{ID: 9, LoginName: "tagged-devices"}})
	if code, _ := get(); code != 200 {
		t.Errorf("a tagged caller with allow_tailnet_members: %d", code)
	}

	// Turning it off closes the connections that came over the tailnet.
	f.mustCall("server.tailscale.disable", nil)
	if _, _, err := ws.Read(ctx); err == nil {
		t.Error("the tailnet connection stayed open")
	}
}

// TestUnixPrincipal: a client on the local socket is identified by its
// credentials, as Rex identifies a peer.
func TestUnixPrincipal(t *testing.T) {
	f := startTailnetFixture(t)
	who := f.mustCall("server.whoami", nil)
	p, _ := who["principal"].(map[string]any)
	if p["kind"] != "peer" || p["transport"] != "unix" {
		t.Fatalf("principal: %v", p)
	}
	if uid, ok := p["uid"].(float64); ok && int(uid) != os.Getuid() {
		t.Errorf("uid %v, want %d", uid, os.Getuid())
	}
}

func TestHostnames(t *testing.T) {
	for name, ok := range map[string]bool{"studio": true, "a": true, "a-b-1": true, "-a": false, "a-": false, "": false, "a_b": false, strings.Repeat("a", 64): false} {
		if validHostname(name) != ok {
			t.Errorf("validHostname(%q) = %v", name, !ok)
		}
	}
	if d := defaultHostname(); !validHostname(d) || !strings.HasSuffix(d, "-gorex") && d != "gorex" {
		t.Errorf("default hostname %q", d)
	}
}

// TestRenderQR: the code is drawn two rows a line, with a quiet zone of
// two modules all around, which is light.
func TestRenderQR(t *testing.T) {
	code, err := qr.Encode("https://dev.example.ts.net", qr.M)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(renderQR(code), "\n"), "\n")
	n := code.Size + 4
	if len(lines) != (n+1)/2 {
		t.Errorf("%d lines for %d modules", len(lines), n)
	}
	for i, l := range lines {
		if got := len([]rune(l)); got != n {
			t.Fatalf("line %d is %d wide, want %d", i, got, n)
		}
	}
	if lines[0] != strings.Repeat("█", n) {
		t.Errorf("the quiet zone is not light: %q", lines[0])
	}
}

func TestPairURLWhy(t *testing.T) {
	for _, st := range []*tailscaleStatus{
		nil,
		{State: "disabled"},
		{State: "needs_login", AuthURL: "https://login.example/x"},
		{State: "running", HTTPS: &httpsStatus{State: "pending"}},
	} {
		if _, err := pairURL(st); err == nil {
			t.Errorf("a URL for %+v", st)
		}
	}
}
