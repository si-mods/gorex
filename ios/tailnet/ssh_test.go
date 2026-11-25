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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeSSHD is an in-process sshd: the device key gets in by publickey
// auth, a direct-streamlocal channel is proxied to a real Unix socket,
// a direct-tcpip one to the TCP address it names, and an exec answers
// socket discovery, as rex server status and the fallback probe do.
type fakeSSHD struct {
	ln      net.Listener
	hostFP  string // the host key's SHA256 fingerprint
	config  *ssh.ServerConfig
	mu      sync.Mutex
	keys    map[string]bool   // authorized keys, by their marshal
	pw      string            // the password PasswordCallback takes; "" refuses all
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
		PasswordCallback: func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.pw != "" && string(pass) == s.pw {
				return nil, nil
			}
			return nil, errors.New("the password is wrong")
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
		case "direct-tcpip":
			go s.directTCP(newCh)
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
	u, err := net.Dial(socketNet, socket)
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

// directTCP takes a direct-tcpip open, dialing the address it names as
// sshd does for ssh -W: the backend any TCP listener is.
func (s *fakeSSHD) directTCP(newCh ssh.NewChannel) {
	var m struct {
		TargetAddr     string
		TargetPort     uint32
		OriginatorAddr string
		OriginatorPort uint32
	}
	if err := ssh.Unmarshal(newCh.ExtraData(), &m); err != nil {
		newCh.Reject(ssh.ConnectionFailed, "bad open")
		return
	}
	u, err := net.Dial("tcp", net.JoinHostPort(m.TargetAddr, strconv.Itoa(int(m.TargetPort))))
	if err != nil {
		newCh.Reject(ssh.ConnectionFailed, "connect failed")
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
		if !ok {
			// A trailing * in the list's key matches by prefix, for a
			// command the test can't spell whole (an install's).
			for k, v := range s.execs {
				if strings.HasSuffix(k, "*") && strings.HasPrefix(m.Command, strings.TrimSuffix(k, "*")) {
					out, ok = v, true
					break
				}
			}
		}
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

// socketNet is the fake Rex socket's transport: unix where Windows
// isn't, tcp on Windows, which has no unix sockets. The sshd side dials
// either by name; the client side passes it on as the socket's path.
var socketNet = "unix"

func init() {
	if runtime.GOOS == "windows" {
		socketNet = "tcp"
	}
}

// echoSocket is a socket that answers with what it gets, standing in
// for the host's Rex socket. Its root is /tmp: a socket path is
// ~104 bytes at most, and a test's temp dir doesn't fit that. On
// Windows it's a loopback TCP listener instead, its address the path.
func echoSocket(t *testing.T, names ...string) string {
	t.Helper()
	path := "127.0.0.1:0"
	if socketNet == "unix" {
		root, err := os.MkdirTemp("/tmp", "ssht")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(root) })
		path = filepath.Join(append([]string{root}, names...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen(socketNet, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	path = ln.Addr().String()
	go echo(ln)
	return path
}

// echoTCP is an echo listener on loopback TCP, the backend a
// direct-tcpip channel reaches, as echoSocket is streamlocal's.
func echoTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go echo(ln)
	return ln.Addr().String()
}

// echo answers each conn on ln with what it gets.
func echo(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() { defer c.Close(); io.Copy(c, c) }()
	}
}

// testSSHDelegate answers each host-key question with trust, keeping
// what it was asked.
type testSSHDelegate struct {
	mu     sync.Mutex
	trust  bool
	secret *string
	asks   []sshAsk
}

type sshAsk struct {
	fingerprint, previous, user, host string
	port                              int
}

func (d *testSSHDelegate) OnSSHHostKey(qid int64, user, host string, port int, fingerprint, previous, keyType string) {
	d.mu.Lock()
	d.asks = append(d.asks, sshAsk{fingerprint, previous, user, host, port})
	trust := d.trust
	d.mu.Unlock()
	SSHAnswer(qid, trust)
}

// OnSSHAuth answers each secret question with the delegate's secret;
// a nil secret refuses the card as the app's cancel does.
func (d *testSSHDelegate) OnSSHAuth(qid int64, user, host string, port int, prompt string, retry, secret bool) {
	d.mu.Lock()
	s := d.secret
	d.mu.Unlock()
	if s == nil {
		SSHAuthAnswer(qid, "")
		return
	}
	SSHAuthAnswer(qid, *s)
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

// socketRec is the socket a record names, valid as SetSSHHosts wants:
// the socket's path on unix, "/" + its address on Windows, where the
// fake's streamlocal dials the same socket no matter the path asked.
func socketRec(s *fakeSSHD) string {
	if socketNet == "tcp" {
		return "/" + s.socket
	}
	return s.socket
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
		Socket: socketRec(srv), HostKey: srv.hostFP})
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
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "socket": "/run/rex/server.sock", "tcp": "127.0.0.1:7297"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "not an address"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "127.0.0.1"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": ":7297"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "127.0.0.1:0"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "127.0.0.1:65536"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "127.0.0.1:http"},
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
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "127.0.0.1:7297"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "localhost:7297"},
		{"id": "alice-laptop", "user": "alice", "host": "x", "port": 22, "tcp": "[::1]:7297"},
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
			Socket: socketRec(srv), HostKey: hostKey}
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

// heldDelegate keeps its questions for the test to answer, as a card
// left up does.
type heldDelegate struct {
	mu   sync.Mutex
	qids []int64
}

func (d *heldDelegate) OnSSHHostKey(qid int64, user, host string, port int, fingerprint, previous, keyType string) {
	d.mu.Lock()
	d.qids = append(d.qids, qid)
	d.mu.Unlock()
}

func (d *heldDelegate) OnSSHAuth(qid int64, user, host string, port int, prompt string, retry, secret bool) {
	d.mu.Lock()
	d.qids = append(d.qids, qid)
	d.mu.Unlock()
}

func (d *heldDelegate) asked() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int64(nil), d.qids...)
}

// Two waits on one host-key question share the card, a cancelled wait
// leaves it for a retry to reattach to, and one answer releases every
// waiter (docs/IOS-023's pending question).
func TestSSHPending(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	h := sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(), Socket: socketRec(srv)}
	setSSHHosts(t, h)
	d := &heldDelegate{}
	SetSSHDelegate(d)
	key := signer.PublicKey()

	ctx1, cancel1 := context.WithCancel(context.Background())
	err1 := make(chan error, 1)
	go func() { err1 <- sshHostKey(ctx1, "alice-laptop", h, key) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(d.asked()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(d.asked()); n != 1 {
		t.Fatalf("the first check asked %d times, want 1", n)
	}
	// A second check of the same fingerprint shares the question; a
	// cancelled one leaves it standing.
	err2 := make(chan error, 1)
	go func() { err2 <- sshHostKey(context.Background(), "alice-laptop", h, key) }()
	ctx2, cancel2 := context.WithCancel(context.Background())
	err3 := make(chan error, 1)
	go func() { err3 <- sshHostKey(ctx2, "alice-laptop", h, key) }()
	cancel2()
	if err := <-err3; err == nil {
		t.Fatal("a cancelled wait on the question returned nil")
	}
	if n := len(d.asked()); n != 1 {
		t.Fatalf("the shared and cancelled checks asked %d times in all, want 1", n)
	}
	// Cancelling the first waiter leaves the question: a retry of it
	// reattaches and still asks nothing.
	cancel1()
	if err := <-err1; err == nil {
		t.Fatal("the first waiter's cancel returned nil")
	}
	err4 := make(chan error, 1)
	go func() { err4 <- sshHostKey(context.Background(), "alice-laptop", h, key) }()
	time.Sleep(20 * time.Millisecond)
	if n := len(d.asked()); n != 1 {
		t.Fatalf("the reattached check asked %d times in all, want 1", n)
	}
	// One answer releases every waiter and trusts the fingerprint.
	SSHAnswer(d.asked()[0], true)
	for i, c := range []chan error{err2, err4} {
		if err := <-c; err != nil {
			t.Fatalf("waiter %d of the answered question: %v", i, err)
		}
	}
	sshMu.Lock()
	trusted := sshTrusted["alice-laptop"]
	sshMu.Unlock()
	if trusted != ssh.FingerprintSHA256(key) {
		t.Errorf("the answer trusted %q, want %q", trusted, ssh.FingerprintSHA256(key))
	}
}

// A host that refuses the device key asks the password, the run caches
// the answered one, and a password login marks the host for the
// install-key offer (docs/IOS-023).
func TestSSHPassword(t *testing.T) {
	Stop()
	sshEnv(t)
	srv := newFakeSSHD(t) // the device key is not authorized
	srv.pw = "swordfish"
	d := &testSSHDelegate{trust: true}
	pw := "swordfish"
	d.secret = &pw
	SetSSHDelegate(d)
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv)})
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("a password login: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	if !SSHTakePasswordOffer("alice-laptop") {
		t.Error("a password login made no install-key offer")
	}
	if SSHTakePasswordOffer("alice-laptop") {
		t.Error("the taken offer offered again")
	}
	// A reconnect takes the run's cached password: the delegate
	// refusing any new card still connects.
	d.secret = nil
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	c, err = dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("the cached password's reconnect: %v", err)
	}
	c.Close()
	// Install key execs the authorized_keys append on the client.
	srv.mu.Lock()
	srv.execs["umask 077; mkdir -p ~/.ssh*"] = ""
	srv.mu.Unlock()
	if err := SSHInstallKey("alice-laptop"); err != nil {
		t.Fatalf("install key: %v", err)
	}
	ran := srv.execsAsked()
	found := false
	for _, cmd := range ran {
		if strings.Contains(cmd, "authorized_keys") && strings.Contains(cmd, "rexgo") {
			found = true
		}
	}
	if !found {
		t.Errorf("install key ran %v, want an authorized_keys append", ran)
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
	// A unix:// endpoint, escaped as the server writes it; only a unix
	// socket has a unix:// endpoint, so on Windows the bare path covers it.
	if socketNet == "unix" {
		srv.mu.Lock()
		srv.execs[rexStatus] = `{"listeners":[{"endpoint":"` + (&url.URL{Scheme: "unix", Path: srv.socket}).String() +
			`","transport":"unix","admission":"peer_credentials","primary":true}]}`
		srv.mu.Unlock()
		setSSHHosts(t, host())
		dial()
	}
	// A bare path, with a tcp listener alongside.
	srv.mu.Lock()
	srv.execs = map[string]string{rexStatus: `{"listeners":[{"endpoint":"127.0.0.1:7297","transport":"tcp"},` +
		`{"endpoint":"` + srv.socket + `","transport":"unix","primary":true}]}`}
	srv.mu.Unlock()
	setSSHHosts(t, host())
	dial()
	// Status names no unix listener the proxy can use — a named pipe,
	// which no SSH channel reaches: the fallback probe's first line is
	// the socket. The kept client holds the endpoint discovery found,
	// so a new one must look again.
	srv.mu.Lock()
	srv.execs = map[string]string{
		rexStatus: `{"listeners":[{"endpoint":"rex-pipe","transport":"named_pipe","primary":true}]}`,
		rexProbe:  srv.socket + "\n/other/path\n",
	}
	srv.mu.Unlock()
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	dial()
	// Neither finds one: the dial tries the LAN probe, which fails where
	// nothing listens.
	defer func(prev string) { lanProbe = prev }(lanProbe)
	lanProbe = "127.0.0.1:1"
	srv.mu.Lock()
	srv.execs = map[string]string{rexProbe: "\n"}
	srv.mu.Unlock()
	sshMu.Lock()
	p = sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil {
		t.Error("a host with no listener and no socket connected")
	}
}

// A dead client's channel fails: the dial drops it, connects anew and
// retries once, as a suspension's cost is a reconnect.
func TestSSHReconnect(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv), HostKey: srv.hostFP})
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
		Socket: socketRec(srv), HostKey: srv.hostFP})
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
		Socket: socketRec(srv), HostKey: srv.hostFP})
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("a dial of the new key: %v", err)
	}
	c.Close()
}

// A tcp endpoint is a direct-tcpip channel to host:port, and the dial
// learns the gateway's pairing token by exec for SSHHostToken; a
// changed record forgets it and learns the new one's.
func TestSSHTCPEndpoint(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	srv.mu.Lock()
	srv.execs[lanTokens] = "tok-one\n"
	srv.mu.Unlock()
	host := func() sshHost {
		return sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
			TCP: echoTCP(t), HostKey: srv.hostFP}
	}
	setSSHHosts(t, host())
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("dialSSH: %v", err)
	}
	roundTrip(t, c, "direct-tcpip")
	c.Close()
	if got := SSHHostToken("alice-laptop"); got != "tok-one" {
		t.Errorf("SSHHostToken = %q, want tok-one", got)
	}
	if got := srv.execsAsked(); !slices.Equal(got, []string{lanTokens}) {
		t.Errorf("an explicit tcp endpoint ran %v, want the token exec only", got)
	}
	// A dead client reconnects with no new fetch: the token holds for
	// the record, as the trust does.
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("a reconnected dial: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	if got := srv.execsAsked(); len(got) != 1 {
		t.Errorf("a reconnect refetched the token: %v", got)
	}
	// A changed record forgets the token; its dial learns the new one.
	srv.mu.Lock()
	srv.execs[lanTokens] = "\ntok-two\n"
	srv.mu.Unlock()
	changed := host()
	setSSHHosts(t, changed)
	if got := SSHHostToken("alice-laptop"); got != "" {
		t.Errorf("a changed record kept the token %q", got)
	}
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("the new record's dial: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	if got := SSHHostToken("Alice-Laptop"); got != "tok-two" {
		t.Errorf("SSHHostToken after the change = %q, want tok-two", got)
	}
}

// Discovery feeds a status tcp listener's endpoint to direct-tcpip — a
// loopback among several — and an answer without a usable listener
// falls through the usual socket paths to the LAN probe.
func TestSSHTCPDiscovery(t *testing.T) {
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
	drop := func() {
		t.Helper()
		sshMu.Lock()
		p := sshClients["alice-laptop"]
		sshMu.Unlock()
		dropSSHClient("alice-laptop", p)
	}
	// Two tcp listeners: the loopback one is picked over a LAN one.
	srv.mu.Lock()
	srv.execs = map[string]string{rexStatus: `{"listeners":[{"endpoint":"192.0.2.1:7297","transport":"tcp"},` +
		`{"endpoint":"` + echoTCP(t) + `","transport":"tcp"}]}`}
	srv.mu.Unlock()
	setSSHHosts(t, host())
	dial()
	// A wildcard endpoint, scheme and all, reads as loopback: the
	// forward can only aim at the host itself.
	_, port, _ := net.SplitHostPort(echoTCP(t))
	srv.mu.Lock()
	srv.execs = map[string]string{rexStatus: `{"listeners":[{"endpoint":"tcp://0.0.0.0:` + port + `","transport":"tcp"}]}`}
	srv.mu.Unlock()
	drop()
	dial()
	// No listener the status names and no socket: the LAN CLI reports a
	// gateway on the LAN address, which the loopback probe would miss.
	srv.mu.Lock()
	srv.execs = map[string]string{
		rexStatus: `{"listeners":[{"endpoint":"rex-pipe","transport":"named_pipe","primary":true}]}`,
		rexProbe:  "\n",
		lanStatus: "lan: listening on http://" + echoTCP(t) + "\n  token: set\n",
	}
	srv.mu.Unlock()
	drop()
	dial()
	// The gateway away but configured: the listen line is the target.
	_, port, _ = net.SplitHostPort(echoTCP(t))
	srv.mu.Lock()
	srv.execs[lanStatus] = "lan: off\n  listen: 0.0.0.0:" + port + "\n"
	srv.mu.Unlock()
	drop()
	dial()
	// And with no listener, socket or gateway, the LAN probe: GoRex's
	// rexapi port on loopback.
	defer func(prev string) { lanProbe = prev }(lanProbe)
	lanProbe = echoTCP(t)
	srv.mu.Lock()
	srv.execs = map[string]string{
		rexStatus: `{"listeners":[{"endpoint":"rex-pipe","transport":"named_pipe","primary":true}]}`,
		rexProbe:  "\n",
	}
	srv.mu.Unlock()
	drop()
	dial()
	if got := SSHHostToken("alice-laptop"); got != "" {
		t.Errorf("a host with no token exec answered %q", got)
	}
}
