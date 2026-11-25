package tailnet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeSSHD is an in-process sshd: the device key gets in by publickey
// auth, a direct-streamlocal channel is proxied to a real Unix socket,
// and an exec answers socket discovery, as rex server status and the
// fallback probe do.
type fakeSSHD struct {
	ln      net.Listener
	hostFP  string // the host key's SHA256 fingerprint
	config  *ssh.ServerConfig
	mu      sync.Mutex
	keys    map[string]bool   // authorized keys, by their marshal
	execs   map[string]string // exec command -> output; absent exits 127
	socket  string            // the unix socket streamlocal reaches
	execRan []string          // the commands asked
	conns   []*ssh.ServerConn
}

func newFakeSSHD(t *testing.T, authorized ...ssh.PublicKey) *fakeSSHD {
	t.Helper()
	s := &fakeSSHD{keys: map[string]bool{}, execs: map[string]string{}}
	for _, k := range authorized {
		s.keys[string(k.Marshal())] = true
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s.hostFP = ssh.FingerprintSHA256(hostKey.PublicKey())
	s.config = &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.keys[string(key.Marshal())] {
				return nil, nil
			}
			return nil, errors.New("the key is not authorized")
		},
	}
	s.config.AddHostKey(hostKey)
	s.ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.socket = echoSocket(t, "dir with space", "server.sock")
	go s.serve()
	t.Cleanup(func() { s.ln.Close(); s.kill() })
	return s
}

func (s *fakeSSHD) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// kill closes every connection the sshd holds, as a restart does.
func (s *fakeSSHD) kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

// execsAsked is the commands the sshd ran, as a test asserts whether
// discovery ran.
func (s *fakeSSHD) execsAsked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.execRan...)
}

func (s *fakeSSHD) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *fakeSSHD) handle(c net.Conn) {
	conn, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		c.Close()
		return
	}
	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	defer conn.Close()
	for newCh := range chans {
		switch newCh.ChannelType() {
		case "direct-streamlocal@openssh.com":
			go s.streamLocal(newCh)
		case "session":
			go s.session(newCh)
		default:
			newCh.Reject(ssh.UnknownChannelType, "no")
		}
	}
}

// streamLocal takes a direct-streamlocal open to the echo socket, as
// sshd does for ssh -L.
func (s *fakeSSHD) streamLocal(newCh ssh.NewChannel) {
	var m struct {
		SocketPath string
		Reserved0  string
		Reserved1  uint32
	}
	if err := ssh.Unmarshal(newCh.ExtraData(), &m); err != nil {
		newCh.Reject(ssh.ConnectionFailed, "bad open")
		return
	}
	s.mu.Lock()
	socket := s.socket
	s.mu.Unlock()
	u, err := net.Dial("unix", socket)
	if err != nil {
		newCh.Reject(ssh.ConnectionFailed, "no socket")
		return
	}
	ch, reqs, err := newCh.Accept()
	if err != nil {
		u.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() { defer ch.Close(); io.Copy(ch, u) }()
	go func() { defer u.Close(); io.Copy(u, ch) }()
}

// session answers one exec from its list and closes, as a shell that
// ran the command does.
func (s *fakeSSHD) session(newCh ssh.NewChannel) {
	ch, reqs, err := newCh.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var m struct{ Command string }
		ssh.Unmarshal(req.Payload, &m)
		s.mu.Lock()
		s.execRan = append(s.execRan, m.Command)
		out, ok := s.execs[m.Command]
		s.mu.Unlock()
		req.Reply(true, nil)
		status := uint32(127)
		if ok {
			status = 0
			io.WriteString(ch, out)
		}
		ch.SendRequest("exit-status", false, ssh.Marshal(&struct{ Status uint32 }{status}))
		return
	}
}

// echoSocket is a unix socket that answers with what it gets, standing
// in for the host's Rex socket. Its root is /tmp: a socket path is
// ~104 bytes at most, and a test's temp dir doesn't fit that.
func echoSocket(t *testing.T, names ...string) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ssht")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(append([]string{root}, names...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
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
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return path
}

// testSSHDelegate answers each host-key question with trust, keeping
// what it was asked.
type testSSHDelegate struct {
	mu    sync.Mutex
	trust bool
	asks  []sshAsk
}

type sshAsk struct {
	fingerprint, previous, user, host string
	port                              int
}

func (d *testSSHDelegate) OnSSHHostKey(qid int64, user, host string, port int, fingerprint, previous string) {
	d.mu.Lock()
	d.asks = append(d.asks, sshAsk{fingerprint, previous, user, host, port})
	trust := d.trust
	d.mu.Unlock()
	SSHAnswer(qid, trust)
}

func (d *testSSHDelegate) asked() []sshAsk {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]sshAsk(nil), d.asks...)
}

// sshEnv is a device key of its own, and no hosts or delegate after.
func sshEnv(t *testing.T) ssh.Signer {
	t.Helper()
	key := NewSSHKey()
	if err := SetSSHKey(key); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		SetSSHDelegate(nil)
		SetSSHHosts("[]")
	})
	return signer
}

func setSSHHosts(t *testing.T, hosts ...sshHost) {
	t.Helper()
	b, _ := json.Marshal(hosts)
	if err := SetSSHHosts(string(b)); err != nil {
		t.Fatal(err)
	}
}

// roundTrip checks c echoes what it is written.
func roundTrip(t *testing.T, c net.Conn, s string) {
	t.Helper()
	if _, err := c.Write([]byte(s)); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := make([]byte, len(s))
	if _, err := io.ReadFull(c, b); err != nil || string(b) != s {
		t.Fatalf("echo %q = %q, %v", s, b, err)
	}
}

// An ssh host is reached through the proxy by its id, over a
// direct-streamlocal channel to its socket; any other name is refused.
func TestSSHProxy(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: srv.socket, HostKey: srv.hostFP})
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("dialSSH: %v", err)
	}
	roundTrip(t, c, "direct")
	c.Close()
	if _, err := dial(context.Background(), "alice-laptop.ssh.invalid:80"); err != nil {
		t.Fatalf("dial: %v", err)
	}
	pr, err := startProxy(dial)
	if err != nil {
		t.Fatal(err)
	}
	defer pr.close()
	c, err = socksConnect(pr.ln.Addr().String(), "tsnet", pr.password, "alice-laptop.ssh.invalid", 80)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	for _, host := range []string{"bob-laptop.ssh.invalid", "alice-laptop.ssh.invalid.example.com"} {
		if c, err := socksConnect(pr.ln.Addr().String(), "tsnet", pr.password, host, 80); err == nil {
			c.Close()
			t.Errorf("connect to %s worked; want refused", host)
		}
	}
	// The socket of the record was used: no discovery ran.
	if got := srv.execsAsked(); len(got) != 0 {
		t.Errorf("an explicit socket still ran discovery: %v", got)
	}
}

func TestSSHTarget(t *testing.T) {
	for addr, want := range map[string]string{"alice-laptop.ssh.invalid:80": "alice-laptop", "Alice-Laptop.SSH.Invalid.:1": "alice-laptop"} {
		id, ok := sshTarget(addr)
		if !ok || id != want {
			t.Errorf("sshTarget(%s) = %s, %v; want %s", addr, id, ok, want)
		}
	}
	for _, addr := range []string{"ssh.invalid:1", ".ssh.invalid:1", "a.b.ssh.invalid:1", "alice-laptop.ssh.invalid",
		"alice-laptop.example.com:1", "-a.ssh.invalid:1", "alice-laptop.tailcat.invalid:1"} {
		if id, ok := sshTarget(addr); ok {
			t.Errorf("sshTarget(%s) = %s; want none", addr, id)
		}
	}
}

func TestSetSSHHosts(t *testing.T) {
	sshEnv(t)
	good := func() map[string]any {
		return map[string]any{"id": "alice-laptop", "user": "alice", "host": "127.0.0.1", "port": 22}
	}
	bad := []map[string]any{
		{"id": "a.b", "user": "alice", "host": "x", "port": 22},
		{"id": "alice-laptop", "user": "", "host": "x", "port": 22},
		{"id": "alice-laptop", "user": "alice", "host": "", "port": 22},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 0},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 65536},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "socket": "rex/server.sock"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "hostKey": "SHA256:!!"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "hostKey": "MD5:aa"},
	}
	for _, h := range bad {
		b, _ := json.Marshal([]map[string]any{h})
		if SetSSHHosts(string(b)) == nil {
			t.Errorf("SetSSHHosts(%s) took it", b)
		}
	}
	if SetSSHHosts("not json") == nil {
		t.Error("SetSSHHosts(not json) took it")
	}
	for _, h := range []map[string]any{
		good(),
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 65535,
			"socket": "/run/rex/server.sock", "hostKey": testFP},
	} {
		b, _ := json.Marshal([]map[string]any{h})
		if err := SetSSHHosts(string(b)); err != nil {
			t.Errorf("SetSSHHosts(%s) = %v", b, err)
		}
	}
}

// testFP is a valid fingerprint string that matches no key.
var testFP = "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))

// The device key round-trips: NewSSHKey makes a PEM SetSSHKey takes,
// and SSHPublicKey is its authorized_keys line.
func TestSSHKey(t *testing.T) {
	k := NewSSHKey()
	signer, err := ssh.ParsePrivateKey([]byte(k))
	if err != nil {
		t.Fatalf("ParsePrivateKey of NewSSHKey: %v", err)
	}
	if err := SetSSHKey("not a key"); err == nil {
		t.Error("SetSSHKey took a bad key")
	}
	if err := SetSSHKey(k); err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if SSHPublicKey() != want {
		t.Errorf("SSHPublicKey() = %s, want %s", SSHPublicKey(), want)
	}
}

// A host key the store doesn't name asks the delegate; trust keeps it
// for the run, refusal fails the dial, and a changed record asks anew
// with the trusted fingerprint.
func TestSSHHostKey(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	host := func(hostKey string) sshHost {
		return sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
			Socket: srv.socket, HostKey: hostKey}
	}
	// With no delegate the dial fails, carrying the fingerprint.
	setSSHHosts(t, host(""))
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil || !strings.Contains(err.Error(), srv.hostFP) {
		t.Fatalf("an untrusted key and no delegate: %v", err)
	}
	// The question carries the host's parts and no previous; a refused
	// answer fails the dial.
	d := &testSSHDelegate{trust: false}
	SetSSHDelegate(d)
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil {
		t.Fatal("a refused key connected")
	}
	asks := d.asked()
	if len(asks) != 1 || asks[0].fingerprint != srv.hostFP || asks[0].previous != "" ||
		asks[0].user != "alice" || asks[0].host != "127.0.0.1" || asks[0].port != srv.port() {
		t.Errorf("the question asked %+v", asks)
	}
	// A trusted answer connects, and holds for the run: a new client of
	// the same record asks nothing.
	d.trust = true
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("a trusted key: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("a session-trusted key reconnected: %v", err)
	}
	c.Close()
	if n := len(d.asked()); n != 2 {
		t.Errorf("a session-trusted key asked %d times, want 2", n)
	}
	// The same record again keeps the run's trust; a changed one clears
	// it and asks anew.
	setSSHHosts(t, host(""))
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("the same record's dial: %v", err)
	}
	c.Close()
	if n := len(d.asked()); n != 2 {
		t.Errorf("the same record asked %d times in all, want 2", n)
	}
	changed := host("")
	changed.User = "bob"
	setSSHHosts(t, changed)
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("a new record's dial: %v", err)
	}
	c.Close()
	if n := len(d.asked()); n != 3 {
		t.Errorf("a changed record asked %d times in all, want 3", n)
	}
	// A changed key asks with the trusted fingerprint as previous.
	setSSHHosts(t, host(testFP))
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("a changed key the delegate trusted: %v", err)
	}
	c.Close()
	asks = d.asked()
	if last := asks[len(asks)-1]; last.fingerprint != srv.hostFP || last.previous != testFP {
		t.Errorf("the changed-key question asked %+v; want %s replacing %s", last, srv.hostFP, testFP)
	}
}

// The socket the record doesn't name is discovered: the primary unix
// listener of rex server status, unix:// or a bare path, else the first
// of the usual paths that exists.
func TestSSHDiscovery(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	host := func() sshHost {
		return sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(), HostKey: srv.hostFP}
	}
	dial := func() {
		t.Helper()
		c, err := dialSSH(context.Background(), "alice-laptop")
		if err != nil {
			t.Fatal(err)
		}
		roundTrip(t, c, "hi")
		c.Close()
	}
	// A unix:// endpoint, escaped as the server writes it.
	srv.mu.Lock()
	srv.execs[rexStatus] = `{"listeners":[{"endpoint":"` + (&url.URL{Scheme: "unix", Path: srv.socket}).String() +
		`","transport":"unix","admission":"peer_credentials","primary":true}]}`
	srv.mu.Unlock()
	setSSHHosts(t, host())
	dial()
	// A bare path, with a tcp listener alongside.
	srv.mu.Lock()
	srv.execs = map[string]string{rexStatus: `{"listeners":[{"endpoint":"127.0.0.1:7297","transport":"tcp"},` +
		`{"endpoint":"` + srv.socket + `","transport":"unix","primary":true}]}`}
	srv.mu.Unlock()
	setSSHHosts(t, host())
	dial()
	// Status names no unix listener: the fallback probe's first line is
	// the socket.
	srv.mu.Lock()
	srv.execs = map[string]string{
		rexStatus: `{"listeners":[{"endpoint":"127.0.0.1:7297","transport":"tcp","primary":true}]}`,
		rexProbe:  srv.socket + "\n/other/path\n",
	}
	srv.mu.Unlock()
	setSSHHosts(t, host())
	dial()
	// Neither finds one: the dial says so. The kept client holds the
	// socket discovery found, so a new one must look again.
	srv.mu.Lock()
	srv.execs = map[string]string{rexProbe: "\n"}
	srv.mu.Unlock()
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil || !strings.Contains(err.Error(), "no rex socket") {
		t.Errorf("a host with no socket: %v", err)
	}
}

// A dead client's channel fails: the dial drops it, connects anew and
// retries once, as a suspension's cost is a reconnect.
func TestSSHReconnect(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: srv.socket, HostKey: srv.hostFP})
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	srv.kill()
	c, err = dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("a dial after the server died: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	sshMu.Lock()
	same := sshClients["alice-laptop"] == p
	sshMu.Unlock()
	if same {
		t.Error("the dead client stayed in the pool")
	}
}

// A host taken out, or a key replaced, drops the pooled clients.
func TestSSHDrops(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: srv.socket, HostKey: srv.hostFP})
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	pooled := func() int {
		sshMu.Lock()
		defer sshMu.Unlock()
		return len(sshClients)
	}
	if pooled() != 1 {
		t.Fatalf("%d clients pooled, want 1", pooled())
	}
	setSSHHosts(t)
	if pooled() != 0 {
		t.Errorf("%d clients left with no hosts", pooled())
	}
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil || !strings.Contains(err.Error(), "not an ssh host") {
		t.Errorf("an unlisted host: %v", err)
	}
	// A new key drops the clients of the old one.
	srv.mu.Lock()
	srv.keys = map[string]bool{}
	srv.mu.Unlock()
	key := NewSSHKey()
	newSigner, _ := ssh.ParsePrivateKey([]byte(key))
	srv.mu.Lock()
	srv.keys[string(newSigner.PublicKey().Marshal())] = true
	srv.mu.Unlock()
	if err := SetSSHKey(key); err != nil {
		t.Fatal(err)
	}
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: srv.socket, HostKey: srv.hostFP})
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("a dial of the new key: %v", err)
	}
	c.Close()
}
