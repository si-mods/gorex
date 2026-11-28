package tailnet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	// ki runs a keyboard-interactive login's rounds; nil refuses the
	// method.
	ki func(ssh.KeyboardInteractiveChallenge) error
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
		KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			s.mu.Lock()
			ki := s.ki
			s.mu.Unlock()
			if ki == nil {
				return nil, errors.New("no keyboard-interactive")
			}
			return nil, ki(ch)
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

// newHostKey gives the sshd a new host key, as a reinstalled host has,
// for the connections after.
func (s *fakeSSHD) newHostKey(t *testing.T) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.config.AddHostKey(k)
	s.mu.Unlock()
	s.hostFP = ssh.FingerprintSHA256(k.PublicKey())
}

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
	// Under mu: a test changes the config (a host key, the methods)
	// between connections.
	s.mu.Lock()
	config := s.config
	s.mu.Unlock()
	conn, chans, reqs, err := ssh.NewServerConn(c, config)
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

// The commands discovery and the token read run, as GoRex's CLI names
// them; each runs in an exec of its own.
const (
	gorexStatus = "gorex " + rexStatus
	gorexLAN    = "gorex " + lanStatus
	gorexToken  = "gorex " + lanTokens
)

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
		SSHAuthAnswer(qid, "", false)
		return
	}
	SSHAuthAnswer(qid, *s, true)
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

// ParseSSHKey parses unencrypted and passphrase-protected keys, returning
// normalized unencrypted OpenSSH PEM, public key, fingerprint, and type.
func TestSSHParseKey(t *testing.T) {
	rawPEM := NewSSHKey()
	out, err := ParseSSHKey(rawPEM, "")
	if err != nil {
		t.Fatalf("ParseSSHKey unencrypted: %v", err)
	}
	var parsed parsedSSHKey
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal parsedSSHKey: %v", err)
	}
	if parsed.Type != "ssh-ed25519" {
		t.Errorf("Type = %q, want ssh-ed25519", parsed.Type)
	}
	if !strings.HasPrefix(parsed.PublicKey, "ssh-ed25519 ") {
		t.Errorf("PublicKey = %q, want ssh-ed25519 prefix", parsed.PublicKey)
	}
	if !validFingerprint(parsed.Fingerprint) {
		t.Errorf("invalid Fingerprint = %q", parsed.Fingerprint)
	}

	// Passphrase-encrypted key: missing or wrong passphrase fails; right passphrase decrypts to unencrypted PEM.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("top-secret"))
	if err != nil {
		t.Fatal(err)
	}
	encPEM := string(pem.EncodeToMemory(encBlock))
	if _, err := ParseSSHKey(encPEM, ""); err == nil || !strings.Contains(strings.ToLower(err.Error()), "passphrase") {
		t.Errorf("encrypted key without passphrase: %v", err)
	}
	if _, err := ParseSSHKey(encPEM, "wrong"); err == nil {
		t.Error("encrypted key with wrong passphrase succeeded")
	}
	decOut, err := ParseSSHKey(encPEM, "top-secret")
	if err != nil {
		t.Fatalf("encrypted key with valid passphrase: %v", err)
	}
	var dec parsedSSHKey
	if err := json.Unmarshal([]byte(decOut), &dec); err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParsePrivateKey([]byte(dec.PEM)); err != nil {
		t.Errorf("normalized PEM should parse without passphrase: %v", err)
	}
	if _, err := ParseSSHKey("not a key", ""); err == nil {
		t.Error("ParseSSHKey accepted garbage")
	}
}

// SetSSHKeys supports multiple keys, default ordering, per-host keyId
// selection, and SSHInstallKey targeting the host's selected key.
func TestSSHMultiKey(t *testing.T) {
	Stop()
	k1 := NewSSHKey()
	k2 := NewSSHKey()
	s1, err := ssh.ParsePrivateKey([]byte(k1))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ssh.ParsePrivateKey([]byte(k2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		SetSSHDelegate(nil)
		SetSSHHosts("[]")
		_ = SetSSHKeys("[]")
	})

	// k1 is default, k2 is non-default.
	keysJSON, _ := json.Marshal([]sshKeyInput{
		{ID: "key-1", PEM: k1, IsDefault: true},
		{ID: "key-2", PEM: k2, IsDefault: false},
	})
	if err := SetSSHKeys(string(keysJSON)); err != nil {
		t.Fatalf("SetSSHKeys: %v", err)
	}
	wantPub1 := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s1.PublicKey())))
	wantPub2 := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s2.PublicKey())))
	if got := SSHPublicKey(); got != wantPub1 {
		t.Errorf("SSHPublicKey() = %q, want %q", got, wantPub1)
	}

	// Server only authorizes k2. Automatic mode (only default k1) fails without delegate.
	srv := newFakeSSHD(t, s2.PublicKey())
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv), HostKey: srv.hostFP})
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil {
		t.Fatal("automatic mode should not offer non-default key-2")
	}

	// Binding the host to key-2 succeeds.
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv), HostKey: srv.hostFP, KeyID: "key-2"})
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("dial with key-2: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()

	// SSHInstallKey installs key-2 (the host's selected key), not key-1.
	srv.mu.Lock()
	srv.execs["umask 077; mkdir -p ~/.ssh*"] = ""
	srv.execs["grep -qF*"] = "" // the check after the install (#1155)
	srv.mu.Unlock()
	if err := SSHInstallKey("alice-laptop"); err != nil {
		t.Fatalf("SSHInstallKey: %v", err)
	}
	found := false
	for _, cmd := range srv.execsAsked() {
		if strings.Contains(cmd, wantPub2) {
			found = true
		}
	}
	if !found {
		t.Errorf("SSHInstallKey did not install key-2 (%s)", wantPub2)
	}

	// Zero keys with password delegate still connects by password.
	if err := SetSSHKeys("[]"); err != nil {
		t.Fatal(err)
	}
	srv.pw = "hunter2"
	pw := "hunter2"
	d := &testSSHDelegate{trust: true, secret: &pw}
	SetSSHDelegate(d)
	c, err = dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("zero keys with password delegate: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
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
	// The cached password is no secret the user typed: after Not Now,
	// the reconnect makes no new install-key offer.
	if SSHTakePasswordOffer("alice-laptop") {
		t.Error("a reconnect on the cached password offered the key again")
	}
	// Install key execs the authorized_keys append on the client, then
	// the check that the key is there.
	srv.mu.Lock()
	srv.execs["umask 077; mkdir -p ~/.ssh*"] = ""
	srv.execs[installCheck(SSHPublicKey())] = ""
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
		srv.execs[gorexStatus] = `{"listeners":[{"endpoint":"` + (&url.URL{Scheme: "unix", Path: srv.socket}).String() +
			`","transport":"unix","admission":"peer_credentials","primary":true}]}`
		srv.mu.Unlock()
		setSSHHosts(t, host())
		dial()
	}
	// A bare path, with a tcp listener alongside.
	srv.mu.Lock()
	srv.execs = map[string]string{gorexStatus: `{"listeners":[{"endpoint":"127.0.0.1:7297","transport":"tcp"},` +
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
		gorexStatus: `{"listeners":[{"endpoint":"rex-pipe","transport":"named_pipe","primary":true}]}`,
		rexProbe:    srv.socket + "\n/other/path\n",
	}
	srv.mu.Unlock()
	sshMu.Lock()
	p := sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	dial()
	// Neither finds one, on a host whose server is down: the dial fails
	// with the error that says so, and runs no token exec, which could
	// make a token on the host.
	defer func(prev string) { lanProbe = prev }(lanProbe)
	lanProbe = echoTCP(t)
	srv.mu.Lock()
	srv.execs = map[string]string{rexProbe: "\n"}
	srv.execRan = nil
	srv.mu.Unlock()
	sshMu.Lock()
	p = sshClients["alice-laptop"]
	sshMu.Unlock()
	dropSSHClient("alice-laptop", p)
	_, err := dialSSH(context.Background(), "alice-laptop")
	if err == nil || !strings.Contains(err.Error(), "no rex socket on 127.0.0.1") {
		t.Errorf("a host with no listener and no socket: %v, want no rex socket on 127.0.0.1", err)
	}
	for _, cmd := range srv.execsAsked() {
		if strings.Contains(cmd, "token") {
			t.Errorf("a host with nothing found ran %q", cmd)
		}
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
	srv.execs[gorexLAN] = "lan: listening on http://127.0.0.1:7297\n  pairing token: set (gorex rexapi lan token show)\n"
	srv.execs[gorexToken] = "tok-one\n"
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
	if got := srv.execsAsked(); !slices.Equal(got, []string{gorexLAN, gorexToken}) {
		t.Errorf("an explicit tcp endpoint ran %v, want the token execs only", got)
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
	if got := srv.execsAsked(); len(got) != 2 {
		t.Errorf("a reconnect refetched the token: %v", got)
	}
	// A changed record forgets the token; its dial learns the new one.
	srv.mu.Lock()
	srv.execs[gorexToken] = "\ntok-two\n"
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
	srv.execs = map[string]string{gorexStatus: `{"listeners":[{"endpoint":"192.0.2.1:7297","transport":"tcp"},` +
		`{"endpoint":"` + echoTCP(t) + `","transport":"tcp"}]}`}
	srv.mu.Unlock()
	setSSHHosts(t, host())
	dial()
	// A wildcard endpoint, scheme and all, reads as loopback: the
	// forward can only aim at the host itself.
	_, port, _ := net.SplitHostPort(echoTCP(t))
	srv.mu.Lock()
	srv.execs = map[string]string{gorexStatus: `{"listeners":[{"endpoint":"tcp://0.0.0.0:` + port + `","transport":"tcp"}]}`}
	srv.mu.Unlock()
	drop()
	dial()
	// No listener the status names and no socket: the LAN CLI reports a
	// gateway on the LAN address, which the loopback probe would miss.
	srv.mu.Lock()
	srv.execs = map[string]string{
		gorexStatus: `{"listeners":[{"endpoint":"rex-pipe","transport":"named_pipe","primary":true}]}`,
		rexProbe:    "\n",
		gorexLAN:    "lan: listening on http://" + echoTCP(t) + "\n  token: set\n",
	}
	srv.mu.Unlock()
	drop()
	dial()
	// The gateway on but not yet listening, as before its server says
	// where: the listen line is the target.
	_, port, _ = net.SplitHostPort(echoTCP(t))
	srv.mu.Lock()
	srv.execs[gorexLAN] = "lan: on, waiting for the GoRex server\n  listen: 0.0.0.0:" + port + "\n"
	srv.mu.Unlock()
	drop()
	dial()
	// And with no listener, socket or gateway, the LAN probe: GoRex's
	// rexapi port on loopback.
	defer func(prev string) { lanProbe = prev }(lanProbe)
	lanProbe = echoTCP(t)
	srv.mu.Lock()
	srv.execs = map[string]string{
		gorexStatus: `{"listeners":[{"endpoint":"rex-pipe","transport":"named_pipe","primary":true}]}`,
		rexProbe:    "\n",
	}
	srv.mu.Unlock()
	drop()
	dial()
	if got := SSHHostToken("alice-laptop"); got != "" {
		t.Errorf("a host with no token exec answered %q", got)
	}
}

// authDelegate trusts every host key and answers each secret question
// by its answer func, keeping what it was asked.
type authDelegate struct {
	mu     sync.Mutex
	answer func(prompt string, retry bool) (string, bool)
	asks   []authAsk
	keys   []sshAsk
}

type authAsk struct {
	prompt        string
	retry, secret bool
}

func (d *authDelegate) OnSSHHostKey(qid int64, user, host string, port int, fingerprint, previous, keyType string) {
	d.mu.Lock()
	d.keys = append(d.keys, sshAsk{fingerprint, previous, user, host, port})
	d.mu.Unlock()
	SSHAnswer(qid, true)
}

func (d *authDelegate) OnSSHAuth(qid int64, user, host string, port int, prompt string, retry, secret bool) {
	d.mu.Lock()
	d.asks = append(d.asks, authAsk{prompt, retry, secret})
	answer := d.answer
	d.mu.Unlock()
	s, ok := answer(prompt, retry)
	SSHAuthAnswer(qid, s, ok)
}

func (d *authDelegate) asked() []authAsk {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]authAsk(nil), d.asks...)
}

// dropClient drops the pooled client of the host id, so that the next
// dial connects anew.
func dropClient(id string) {
	sshMu.Lock()
	p := sshClients[id]
	sshMu.Unlock()
	if p != nil {
		dropSSHClient(id, p)
	}
}

// Keyboard-interactive (#1155): a retry is a prompt an earlier round
// asked, not a round's second question; a password prompt's answer is
// the run's cached password, as the password method's is; an empty
// answer is an answer; and a secret login's mark goes with its record.
func TestSSHKeyboardInteractive(t *testing.T) {
	Stop()
	sshEnv(t)
	srv := newFakeSSHD(t) // the device key is not authorized
	srv.mu.Lock()
	srv.config.PasswordCallback = nil
	srv.ki = func(ch ssh.KeyboardInteractiveChallenge) error {
		for range 3 {
			a, err := ch("", "", []string{"Password: ", "Verification code: "}, []bool{false, true})
			if err != nil {
				return err
			}
			if a[0] == "swordfish" && a[1] == "123456" {
				return nil
			}
		}
		return errors.New("wrong")
	}
	srv.mu.Unlock()
	passwords := []string{"wrong", "swordfish"}
	d := &authDelegate{answer: func(prompt string, retry bool) (string, bool) {
		if prompt == "Verification code:" {
			return "123456", true
		}
		if len(passwords) == 0 {
			return "", false
		}
		p := passwords[0]
		passwords = passwords[1:]
		return p, true
	}}
	SetSSHDelegate(d)
	h := sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv), HostKey: srv.hostFP}
	setSSHHosts(t, h)
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("a keyboard-interactive login: %v", err)
	}
	c.Close()
	want := []authAsk{{"Password:", false, true}, {"Verification code:", false, false},
		{"Password:", true, true}, {"Verification code:", true, false}}
	if got := d.asked(); !slices.Equal(got, want) {
		t.Errorf("the rounds asked %+v, want %+v", got, want)
	}
	if !SSHTakePasswordOffer("alice-laptop") {
		t.Error("a typed keyboard-interactive password made no install-key offer")
	}
	// A reconnect answers the password prompt from the run's cache and
	// asks the code alone, which is no typed secret: no new offer.
	dropClient("alice-laptop")
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("the reconnect: %v", err)
	}
	c.Close()
	if got := d.asked()[len(want):]; !slices.Equal(got, []authAsk{{"Verification code:", false, false}}) {
		t.Errorf("the reconnect asked %+v, want the code alone", got)
	}
	if SSHTakePasswordOffer("alice-laptop") {
		t.Error("the cached password's reconnect offered the key again")
	}

	// An empty answer, Return at a notice, logs in; a refused card
	// does not.
	srv.mu.Lock()
	srv.ki = func(ch ssh.KeyboardInteractiveChallenge) error {
		a, err := ch("", "", []string{"Press Return to continue"}, []bool{true})
		if err != nil {
			return err
		}
		if len(a) != 1 || a[0] != "" {
			return errors.New("not Return")
		}
		return nil
	}
	srv.mu.Unlock()
	d.mu.Lock()
	d.answer = func(string, bool) (string, bool) { return "", true }
	d.mu.Unlock()
	dropClient("alice-laptop")
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatalf("an empty answer: %v", err)
	}
	c.Close()
	d.mu.Lock()
	d.answer = func(string, bool) (string, bool) { return "", false }
	d.mu.Unlock()
	dropClient("alice-laptop")
	if _, err = dialSSH(context.Background(), "alice-laptop"); err == nil {
		t.Fatal("a refused card logged in")
	}

	// A secret that is no password marks the host for the offer, and
	// the mark goes with its record although no password is cached.
	srv.mu.Lock()
	srv.ki = func(ch ssh.KeyboardInteractiveChallenge) error {
		_, err := ch("", "", []string{"Verification code: "}, []bool{false})
		return err
	}
	srv.mu.Unlock()
	d.mu.Lock()
	d.answer = func(string, bool) (string, bool) { return "123456", true }
	d.mu.Unlock()
	bob := h
	bob.ID = "bob-laptop"
	setSSHHosts(t, h, bob)
	if c, err = dialSSH(context.Background(), "bob-laptop"); err != nil {
		t.Fatalf("a code login: %v", err)
	}
	c.Close()
	setSSHHosts(t, h)
	sshMu.Lock()
	marked, cached := sshPasswordAuth["bob-laptop"], sshPasswords["bob-laptop"]
	sshMu.Unlock()
	if marked || cached != "" {
		t.Errorf("a removed host kept its offer mark %v and password %q", marked, cached)
	}

	// PAM refuses a wrong password without asking again: it is not
	// cached, and the next dial asks anew.
	srv.mu.Lock()
	srv.ki = func(ch ssh.KeyboardInteractiveChallenge) error {
		a, err := ch("", "", []string{"Password: "}, []bool{false})
		if err != nil {
			return err
		}
		if a[0] != "swordfish" {
			return errors.New("wrong")
		}
		return nil
	}
	srv.mu.Unlock()
	typed := []string{"wrong", "swordfish"}
	d.mu.Lock()
	d.asks = nil
	d.answer = func(string, bool) (string, bool) {
		if len(typed) == 0 {
			return "", false
		}
		p := typed[0]
		typed = typed[1:]
		return p, true
	}
	d.mu.Unlock()
	carol := h
	carol.ID = "carol-laptop"
	setSSHHosts(t, h, carol)
	if _, err = dialSSH(context.Background(), "carol-laptop"); err == nil {
		t.Fatal("a wrong password logged in")
	}
	if c, err = dialSSH(context.Background(), "carol-laptop"); err != nil {
		t.Fatalf("the password typed again: %v", err)
	}
	c.Close()
	if n := len(d.asked()); n != 2 {
		t.Errorf("a wrong then a right password asked %d times, want 2", n)
	}
	// A cached password the host refuses — changed on the host — is
	// dropped: the next dial asks again.
	sshMu.Lock()
	sshPasswords["carol-laptop"] = "stale"
	sshMu.Unlock()
	typed = []string{"swordfish"}
	dropClient("carol-laptop")
	if _, err = dialSSH(context.Background(), "carol-laptop"); err == nil {
		t.Fatal("a stale cached password logged in")
	}
	sshMu.Lock()
	stale := sshPasswords["carol-laptop"]
	sshMu.Unlock()
	if stale != "" {
		t.Errorf("a refused cached password stayed: %q", stale)
	}
}

// A secret question outlives its record (#1155): a record change, as
// Trust & Save makes while a password card shows, leaves the card's
// question, which its answer then resolves.
func TestSSHPendingOutlivesRecord(t *testing.T) {
	Stop()
	sshEnv(t)
	srv := newFakeSSHD(t)
	srv.mu.Lock()
	srv.pw = "swordfish"
	srv.mu.Unlock()
	d := &heldDelegate{}
	SetSSHDelegate(d)
	h := sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv)}
	h.HostKey = srv.hostFP
	setSSHHosts(t, h)
	errs := make(chan error, 1)
	go func() {
		_, err := dialSSH(context.Background(), "alice-laptop")
		errs <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(d.asked()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(d.asked()) != 1 {
		t.Fatal("the password was not asked")
	}
	// Trust & Save's kind of change: the same login, another field.
	changed := h
	changed.Socket = "/run/rex/other.sock"
	setSSHHosts(t, changed)
	if err := <-errs; err == nil {
		t.Error("the old record's dial connected")
	}
	sshMu.Lock()
	q := sshPending["alice-laptop"]
	sshMu.Unlock()
	if q == nil || q.qid != d.asked()[0] {
		t.Fatalf("the record change dropped the question on screen: %+v", q)
	}
	SSHAuthAnswer(q.qid, "swordfish", true)
	select {
	case <-q.done:
	default:
		t.Fatal("the card's answer did not resolve its question")
	}
	if q.secret != "swordfish" || q.refused {
		t.Errorf("the question got %q, refused %v", q.secret, q.refused)
	}
	// A login that moves to another machine drops the card's question:
	// a password typed for alice@127.0.0.1 must not reach bob's.
	go func() {
		_, err := dialSSH(context.Background(), "alice-laptop")
		errs <- err
	}()
	deadline = time.Now().Add(5 * time.Second)
	for len(d.asked()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(d.asked()) != 2 {
		t.Fatal("the second password was not asked")
	}
	moved := changed
	moved.User = "bob"
	setSSHHosts(t, moved)
	<-errs
	sshMu.Lock()
	left := sshPending["alice-laptop"]
	sshMu.Unlock()
	if left != nil {
		t.Errorf("a moved login kept the question %+v", left)
	}
}

// A key trusted by Connect Once is the previous fingerprint when the
// host's key changes later in the run (#1155): the card warns.
func TestSSHConnectOnceKeyChanged(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	d := &testSSHDelegate{trust: true}
	SetSSHDelegate(d)
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv)})
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	first := srv.hostFP
	srv.newHostKey(t)
	dropClient("alice-laptop")
	if c, err = dialSSH(context.Background(), "alice-laptop"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	asks := d.asked()
	if len(asks) != 2 || asks[1].fingerprint != srv.hostFP || asks[1].previous != first {
		t.Errorf("the changed key asked %+v; want %s replacing %s", asks, srv.hostFP, first)
	}
}

// Install key fails when the remote command fails or the key is not in
// authorized_keys afterwards (#1155), as on a cmd.exe host or a
// read-only ~/.ssh.
func TestSSHInstallKeyChecks(t *testing.T) {
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
	if err := SSHInstallKey("alice-laptop"); err == nil {
		t.Error("an install whose command failed reported success")
	}
	srv.mu.Lock()
	srv.execs["umask 077; mkdir -p ~/.ssh*"] = ""
	srv.mu.Unlock()
	if err := SSHInstallKey("alice-laptop"); err == nil || !strings.Contains(err.Error(), "authorized_keys") {
		t.Errorf("an install the check did not find: %v", err)
	}
	srv.mu.Lock()
	srv.execs[installCheck(SSHPublicKey())] = ""
	srv.mu.Unlock()
	if err := SSHInstallKey("alice-laptop"); err != nil {
		t.Errorf("an install the check found: %v", err)
	}
}

// The token read (#1154): a failed exec caches nothing, so a later dial
// learns the token; each CLI name runs in an exec of its own; token show
// runs only for a gateway whose status says it has a token, as it would
// make one; and SSHForgetHostToken reads it again.
func TestSSHTCPTokenRead(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	defer func(prev time.Duration) { sshTokenRetry = prev }(sshTokenRetry)
	sshTokenRetry = 0
	backend := echoTCP(t)
	h := sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		TCP: backend, HostKey: srv.hostFP}
	setSSHHosts(t, h)
	dial := func() {
		t.Helper()
		c, err := dialSSH(context.Background(), "alice-laptop")
		if err != nil {
			t.Fatal(err)
		}
		roundTrip(t, c, "hi")
		c.Close()
	}
	cached := func() (string, bool) {
		sshMu.Lock()
		defer sshMu.Unlock()
		tok, ok := sshTokens["alice-laptop"]
		return tok, ok
	}
	// No CLI answers: nothing is cached.
	dial()
	if tok, ok := cached(); ok {
		t.Fatalf("a failed token exec cached %q", tok)
	}
	// Upstream's name alone answers, in an exec of its own.
	srv.mu.Lock()
	srv.execs["rex "+lanStatus] = "lan: listening on http://127.0.0.1:7297\n  pairing token: set (gorex rexapi lan token show)\n"
	srv.execs["rex "+lanTokens] = "tok-rex\n"
	srv.mu.Unlock()
	dial()
	if got := SSHHostToken("alice-laptop"); got != "tok-rex" {
		t.Errorf("the token after a failed exec = %q, want tok-rex", got)
	}
	// A gateway with no token: token show does not run, and "" is the
	// answer cached.
	srv.mu.Lock()
	srv.execs = map[string]string{gorexLAN: "lan: off\n  allow: none\n", gorexToken: "made-a-token\n"}
	srv.execRan = nil
	srv.mu.Unlock()
	changed := h
	changed.User = "bob"
	setSSHHosts(t, changed)
	dial()
	if tok, ok := cached(); !ok || tok != "" {
		t.Errorf("a host with no token cached %q, %v; want \"\", true", tok, ok)
	}
	if got := srv.execsAsked(); !slices.Equal(got, []string{gorexLAN}) {
		t.Errorf("a host with no token ran %v, want the status alone", got)
	}
	// Forgotten, as after a refusal of a rotated token: the next dial
	// reads the new one.
	srv.mu.Lock()
	srv.execs = map[string]string{gorexLAN: "lan: listening on http://127.0.0.1:7297\n  pairing token: set (gorex rexapi lan token show)\n",
		gorexToken: "tok-rotated\n"}
	srv.mu.Unlock()
	SSHForgetHostToken("Alice-Laptop")
	dial()
	if got := SSHHostToken("alice-laptop"); got != "tok-rotated" {
		t.Errorf("the token after a forget = %q, want tok-rotated", got)
	}
}

// Discovery (#1154): a tcp listener's bound address beats its listen
// string, a Unix host's socket beats a tcp listener, and a unix listener
// that is not primary still counts.
func TestSSHDiscoveryOrder(t *testing.T) {
	Stop()
	signer := sshEnv(t)
	srv := newFakeSSHD(t, signer.PublicKey())
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		HostKey: srv.hostFP})
	dial := func(what string) {
		t.Helper()
		dropClient("alice-laptop")
		c, err := dialSSH(context.Background(), "alice-laptop")
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		roundTrip(t, c, "hi")
		c.Close()
	}
	set := func(execs map[string]string) {
		srv.mu.Lock()
		srv.execs = execs
		srv.mu.Unlock()
	}
	// A listen on port 0: the address it is bound to has the port.
	set(map[string]string{gorexStatus: `{"listeners":[{"endpoint":"127.0.0.1:0","addresses":["` + echoTCP(t) +
		`"],"transport":"tcp","admission":"anonymous"}]}`})
	dial("a port-0 listener")
	// A tcp listener nothing answers on, and no unix one: the usual
	// socket path wins, as before tcp listeners were read.
	set(map[string]string{
		gorexStatus: `{"listeners":[{"endpoint":"127.0.0.1:1","addresses":["127.0.0.1:1"],"transport":"tcp"}]}`,
		rexProbe:    srv.socket + "\n",
	})
	dial("a Unix host with a tcp listener")
	// A gateway in error names no target, though it has a listen
	// address: nothing found, and the error says so.
	_, port, _ := net.SplitHostPort(echoTCP(t))
	set(map[string]string{gorexLAN: "lan: error\n  listen: 127.0.0.1:" + port + "\n  error: address in use\n"})
	dropClient("alice-laptop")
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil || !strings.Contains(err.Error(), "no rex socket") {
		t.Errorf("a gateway in error: %v, want no rex socket", err)
	}
	// A unix listener that is not the primary one beats the tcp one.
	if socketNet == "unix" {
		set(map[string]string{gorexStatus: `{"listeners":[{"endpoint":"127.0.0.1:1","transport":"tcp"},` +
			`{"endpoint":"` + srv.socket + `","transport":"unix"}]}`})
		dial("a unix listener not primary")
	}
}

// A password typed in the password method is cached before the host
// answers; when the whole login then fails, it is dropped, so the next
// dial asks afresh instead of sending the wrong one first (#1159
// review).
func TestSSHTypedPasswordRefused(t *testing.T) {
	Stop()
	sshEnv(t)
	srv := newFakeSSHD(t) // the device key is not authorized
	srv.mu.Lock()
	srv.pw = "swordfish"
	srv.mu.Unlock()
	answer := "wrong"
	d := &authDelegate{answer: func(string, bool) (string, bool) { return answer, true }}
	SetSSHDelegate(d)
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv), HostKey: srv.hostFP})
	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil {
		t.Fatal("a wrong password logged in")
	} else {
		t.Logf("the refused login: %v", err)
	}
	sshMu.Lock()
	kept := sshPasswords["alice-laptop"]
	sshMu.Unlock()
	if kept != "" {
		t.Fatalf("a refused typed password stayed cached: %q", kept)
	}
	// The next dial asks first, as a first ask (no retry), and the
	// right password logs in.
	d.mu.Lock()
	d.asks = nil
	d.mu.Unlock()
	answer = "swordfish"
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("the right password: %v", err)
	}
	c.Close()
	if asks := d.asked(); len(asks) == 0 || asks[0].retry {
		t.Errorf("the next dial asked %+v; want a first ask, not the cached wrong password", asks)
	}
}

// A dial before SetSSHKeys/SetSSHKey has ever initialized the key store must
// fail fast instead of falling back to password prompts during a cold start.
func TestSSHDialBeforeKeysReady(t *testing.T) {
	Stop()
	k := NewSSHKey()
	signer, err := ssh.ParsePrivateKey([]byte(k))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		SetSSHDelegate(nil)
		_ = SetSSHHosts("[]")
		_ = SetSSHKeys("[]")
	})

	// Simulate a fresh process before SetSSHKeys has succeeded.
	sshMu.Lock()
	sshKeys = nil
	sshKeysReady = false
	sshMu.Unlock()

	srv := newFakeSSHD(t, signer.PublicKey())
	srv.mu.Lock()
	srv.pw = "swordfish"
	srv.mu.Unlock()
	d := &authDelegate{answer: func(string, bool) (string, bool) { return "swordfish", true }}
	SetSSHDelegate(d)
	setSSHHosts(t, sshHost{ID: "alice-laptop", User: "alice", Host: "127.0.0.1", Port: srv.port(),
		Socket: socketRec(srv), HostKey: srv.hostFP})

	if _, err := dialSSH(context.Background(), "alice-laptop"); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("dial before keys ready: err = %v, want ssh keys not ready", err)
	}
	if asks := d.asked(); len(asks) != 0 {
		t.Fatalf("dial before keys ready prompted for auth: %+v", asks)
	}

	// Once keys are synced, public-key auth succeeds without prompting.
	keysJSON, _ := json.Marshal([]sshKeyInput{{ID: "k1", PEM: k, IsDefault: true}})
	if err := SetSSHKeys(string(keysJSON)); err != nil {
		t.Fatalf("SetSSHKeys: %v", err)
	}
	c, err := dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("dial after SetSSHKeys: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	if asks := d.asked(); len(asks) != 0 {
		t.Fatalf("public-key dial prompted for auth: %+v", asks)
	}

	// Explicitly syncing an empty key set still allows password auth.
	if err := SetSSHKeys("[]"); err != nil {
		t.Fatalf("SetSSHKeys empty: %v", err)
	}
	c, err = dialSSH(context.Background(), "alice-laptop")
	if err != nil {
		t.Fatalf("dial after explicit empty SetSSHKeys: %v", err)
	}
	roundTrip(t, c, "hi")
	c.Close()
	if asks := d.asked(); len(asks) == 0 {
		t.Fatalf("explicit empty key set did not prompt for password")
	}
}
