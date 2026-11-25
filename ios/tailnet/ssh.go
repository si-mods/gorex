package tailnet

// Rex Go reaches a host over SSH (docs/IOS-023-ssh-hosts.md): the proxy
// dials <id>.ssh.invalid as a direct-streamlocal channel to the host's
// Rex socket, pooled under one ssh.Client per host as upstream's shared
// connection is. The device key stays on the phone: the app keeps it in
// its Keychain and passes it in, as the tailcat key's. Phase 1 is key
// auth only; a host-key the store does not name asks the app through
// SSHDelegate, as rootshell's HostKeyValidationRequest does.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorex/internal/tailnode"

	"golang.org/x/crypto/ssh"
)

// sshDomain ends the proxy's name of an ssh host. .invalid never
// resolves, so the name can't reach anything else, as tailcatDomain's.
const sshDomain = ".ssh.invalid"

var (
	sshMu     sync.Mutex
	sshSigner ssh.Signer
	// sshHosts is each ssh host's record, by its id.
	sshHosts = map[string]sshHost{}
	// sshClients is the ssh client of each host: one per host, as
	// upstream's ControlMaster carries every channel.
	sshClients = map[string]*pooledSSH{}
	// sshScopes ends the dials to each host: SetSSHHosts cancels the
	// scope of a host no longer listed or whose record changed,
	// SetSSHKey all.
	sshScopes = map[string]sshScope{}
	// sshTrusted is each host's fingerprint trusted for this run, by
	// its id: the answer to a host-key question holds until the host's
	// record changes.
	sshTrusted = map[string]string{}
	// sshDelegate answers the host-key questions; sshQuestions is each
	// question waiting for SSHAnswer, by its qid.
	sshDelegate  SSHDelegate
	sshQuestions = map[int64]chan bool{}
	sshQID       atomic.Int64
)

// sshScope is the context the dials to a host run in, as tcScope is.
type sshScope struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// sshScopeLocked is the scope of the dials to the host id. sshMu must
// be held.
func sshScopeLocked(id string) context.Context {
	s, ok := sshScopes[id]
	if !ok {
		s.ctx, s.cancel = context.WithCancel(context.Background())
		sshScopes[id] = s
	}
	return s.ctx
}

// NewSSHKey makes the device key, an ed25519 private key in OpenSSH PEM,
// for the app to keep in its Keychain and pass to SetSSHKey.
func NewSSHKey() string {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ""
	}
	b, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(b))
}

// SetSSHKey sets the private key the ssh hosts know this phone by, an
// OpenSSH PEM as NewSSHKey makes. A new key drops the clients of the
// old one; they reconnect on the next dial.
func SetSSHKey(pemKey string) error {
	s, err := ssh.ParsePrivateKey([]byte(pemKey))
	if err != nil {
		return err
	}
	sshMu.Lock()
	if sshSigner != nil && bytes.Equal(sshSigner.PublicKey().Marshal(), s.PublicKey().Marshal()) {
		sshMu.Unlock()
		return nil
	}
	sshSigner = s
	old := sshClients
	sshClients = map[string]*pooledSSH{}
	for _, sc := range sshScopes {
		sc.cancel() // dials of the old key
	}
	sshScopes = map[string]sshScope{}
	sshMu.Unlock()
	for _, p := range old {
		if p.c != nil {
			go p.c.Close() // a stuck close would block the caller
		}
	}
	return nil
}

// SSHPublicKey is the device key's public half, as authorized_keys
// takes it; "" before SetSSHKey.
func SSHPublicKey() string {
	sshMu.Lock()
	s := sshSigner
	sshMu.Unlock()
	if s == nil {
		return ""
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey())))
}

// SSHDelegate answers the questions a dial can't, as ShareDelegate does
// for hotspot share. gomobile binds this interface to a Swift protocol.
// The host, port and fingerprint of OnSSHHostKey are for the card it
// shows; previous is the fingerprint the host was trusted by, "" on a
// first sighting. The answer comes back through SSHAnswer.
type SSHDelegate interface {
	OnSSHHostKey(qid int64, user, host string, port int, fingerprint, previous string)
}

// SetSSHDelegate sets the delegate the host-key check asks, or nil to
// refuse any key the store does not name.
func SetSSHDelegate(d SSHDelegate) {
	sshMu.Lock()
	sshDelegate = d
	sshMu.Unlock()
}

// SSHAnswer gives the answer to a pending host-key question; a qid no
// question waits on is ignored.
func SSHAnswer(qid int64, trust bool) {
	sshMu.Lock()
	ch := sshQuestions[qid]
	delete(sshQuestions, qid)
	sshMu.Unlock()
	if ch != nil {
		ch <- trust
	}
}

// sshHost is an SSH host's record, as the app's Add Host form writes it
// (docs/IOS-023): id is the proxy's <id>.ssh.invalid name, socket the
// remote Rex socket ("" for discovery), hostKey the trusted
// SHA256 fingerprint ("" for trust on first use).
type sshHost struct {
	ID      string `json:"id"`
	User    string `json:"user"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Socket  string `json:"socket"`
	HostKey string `json:"hostKey"`
}

// SetSSHHosts sets the hosts the proxy reaches over SSH, as a JSON list
// of {"id", "user", "host", "port", "socket", "hostKey"}. The clients
// of hosts no longer listed, or whose record changed, close.
func SetSSHHosts(hostsJSON string) error {
	var hosts []sshHost
	if err := json.Unmarshal([]byte(hostsJSON), &hosts); err != nil {
		return err
	}
	m := map[string]sshHost{}
	for _, h := range hosts {
		if !tailnode.ValidHostname(h.ID) {
			return errors.New("invalid ssh host id")
		}
		if h.User == "" || h.Host == "" {
			return errors.New("an ssh host needs a user and a host")
		}
		if h.Port < 1 || h.Port > 65535 {
			return errors.New("invalid ssh port")
		}
		if h.Socket != "" && !strings.HasPrefix(h.Socket, "/") {
			return errors.New("ssh socket is not an absolute path")
		}
		if h.HostKey != "" && !validFingerprint(h.HostKey) {
			return errors.New("invalid ssh host key fingerprint")
		}
		m[strings.ToLower(h.ID)] = h
	}
	sshMu.Lock()
	// gone is a host no longer listed or whose record changed: its
	// client, scope and run's trust are of the old record.
	gone := func(id string) bool {
		h, ok := m[id]
		return !ok || h != sshHosts[id]
	}
	var dead []*pooledSSH
	for id, p := range sshClients {
		if gone(id) {
			delete(sshClients, id)
			dead = append(dead, p)
		}
	}
	for id, s := range sshScopes {
		if gone(id) {
			delete(sshScopes, id)
			s.cancel()
		}
	}
	for id := range sshTrusted {
		if gone(id) {
			delete(sshTrusted, id)
		}
	}
	sshHosts = m
	sshMu.Unlock()
	for _, p := range dead {
		if p.c != nil {
			go p.c.Close()
		}
	}
	return nil
}

// validFingerprint tells an ssh.FingerprintSHA256 string: "SHA256:" and
// the base64 of a SHA-256 sum.
func validFingerprint(s string) bool {
	b64, ok := strings.CutPrefix(s, "SHA256:")
	if !ok {
		return false
	}
	sum, err := base64.RawStdEncoding.DecodeString(b64)
	return err == nil && len(sum) == sha256.Size
}

// sshTarget is the host id of a proxy target under sshDomain, as
// tailcatTarget is under tailcatDomain; the port is the socket's
// protocol's, not sshd's, and goes unread.
func sshTarget(addr string) (id string, ok bool) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	id, ok = strings.CutSuffix(strings.ToLower(strings.TrimSuffix(host, ".")), sshDomain)
	if !ok || !tailnode.ValidHostname(id) {
		return "", false
	}
	return id, true
}

// sshDial bounds a dial of an ssh host, as tailcatDial does a tailcat
// one: long, as a first connect may wait on the host-key question.
var sshDial = 90 * time.Second

// pooledSSH is a host's client, or the connect making it: done closes
// when c or err is set. socket is the socket discovery found, kept for
// the client's life as a host's socket doesn't move.
type pooledSSH struct {
	done   chan struct{}
	c      *ssh.Client
	err    error
	socket string
}

// dialSSH connects to the Rex socket of the ssh host id, within
// sshDial. A cancel of ctx ends the dial; ctx's deadline does not, as
// dialTailcat's: the proxy's SOCKS server gives each dial five seconds,
// shorter than a first connect's host-key question.
func dialSSH(ctx context.Context, id string) (net.Conn, error) {
	sshMu.Lock()
	h, ok := sshHosts[id]
	var scope context.Context
	if ok {
		scope = sshScopeLocked(id)
	}
	sshMu.Unlock()
	if !ok {
		return nil, errors.New("not an ssh host: " + id)
	}
	dctx, cancel := context.WithTimeout(scope, sshDial)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
	defer stop()
	return dialSSHHost(dctx, id, h)
}

// dialSSHHost opens a streamlocal channel to h's socket with the pooled
// client. A channel that won't open drops the client and retries once
// with a new one, as a dead client drops its channels and a reconnect
// is all a suspension or a network change costs.
func dialSSHHost(ctx context.Context, id string, h sshHost) (net.Conn, error) {
	var err error
	for try := 0; try < 2; try++ {
		var p *pooledSSH
		p, err = sshClientFor(ctx, id, h)
		if err != nil {
			return nil, err
		}
		var ch ssh.Channel
		if ch, err = openSocket(ctx, p, h); err == nil {
			return &sshChanConn{Channel: ch, laddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
				raddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: h.Port}}, nil
		}
		dropSSHClient(id, p)
	}
	return nil, err
}

// sshClientFor is the pooled client of the host id, waiting on the
// connect that makes it when another dial got there first.
func sshClientFor(ctx context.Context, id string, h sshHost) (*pooledSSH, error) {
	sshMu.Lock()
	p := sshClients[id]
	if p == nil {
		if sshSigner == nil {
			sshMu.Unlock()
			return nil, errors.New("no ssh key")
		}
		signer := sshSigner
		p = &pooledSSH{done: make(chan struct{})}
		sshClients[id] = p
		go func() {
			c, err := connectSSH(ctx, id, h, signer)
			sshMu.Lock()
			p.c, p.err = c, err
			// A client the pool dropped while it connected is
			// closed here: nothing else holds it.
			orphan := sshClients[id] != p
			sshMu.Unlock()
			close(p.done)
			if orphan && c != nil {
				go c.Close()
			}
		}()
	}
	sshMu.Unlock()
	select {
	case <-p.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.err != nil {
		dropSSHClient(id, p)
		return nil, p.err
	}
	return p, nil
}

// dropSSHClient takes p out of the pool and closes its client, as a
// dead client makes its channels fail.
func dropSSHClient(id string, p *pooledSSH) {
	sshMu.Lock()
	held := sshClients[id] == p
	if held {
		delete(sshClients, id)
	}
	c := p.c
	sshMu.Unlock()
	if held && c != nil {
		go c.Close()
	}
}

// connectSSH makes the client: TCP to sshd, then the handshake, which
// the host-key check may hold for the question. A cancel of ctx closes
// the connection, as the handshake takes none.
func connectSSH(ctx context.Context, id string, h sshHost, signer ssh.Signer) (*ssh.Client, error) {
	addr := net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
	c, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	config := &ssh.ClientConfig{
		User:            h.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:         15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error { return sshHostKey(ctx, id, h, key) },
	}
	conn, chans, reqs, err := ssh.NewClientConn(c, addr, config)
	if err != nil {
		c.Close()
		return nil, err
	}
	return ssh.NewClient(conn, chans, reqs), nil
}

// sshHostKey is the client's host-key check: the fingerprint the host's
// record names or one trusted for this run connects; another asks the
// delegate, and a trusted answer holds for the run. With no delegate
// the error carries the fingerprint for the app log.
func sshHostKey(ctx context.Context, id string, h sshHost, key ssh.PublicKey) error {
	fingerprint := ssh.FingerprintSHA256(key)
	sshMu.Lock()
	if fingerprint == h.HostKey || sshTrusted[id] != "" && sshTrusted[id] == fingerprint {
		sshMu.Unlock()
		return nil
	}
	d := sshDelegate
	sshMu.Unlock()
	if d == nil {
		return errors.New("untrusted ssh host key: " + fingerprint)
	}
	answer := make(chan bool, 1)
	qid := sshQID.Add(1)
	sshMu.Lock()
	sshQuestions[qid] = answer
	sshMu.Unlock()
	defer func() {
		sshMu.Lock()
		delete(sshQuestions, qid)
		sshMu.Unlock()
	}()
	d.OnSSHHostKey(qid, h.User, h.Host, h.Port, fingerprint, h.HostKey)
	select {
	case trust := <-answer:
		if !trust {
			return errors.New("ssh host key refused: " + fingerprint)
		}
		sshMu.Lock()
		sshTrusted[id] = fingerprint
		sshMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(sshDial):
		return errors.New("ssh host key question unanswered: " + fingerprint)
	}
}

// openSocket opens the data channel to the host's Rex socket: the one
// the record names, else the one discovery found for this client.
func openSocket(ctx context.Context, p *pooledSSH, h sshHost) (ssh.Channel, error) {
	socket := h.Socket
	if socket == "" {
		s, err := p.rexSocket(ctx, h)
		if err != nil {
			return nil, err
		}
		socket = s
	}
	return openStreamLocal(ctx, p.c, socket)
}

// rexSocket is the socket discovery found for this client, run once and
// kept in p.
func (p *pooledSSH) rexSocket(ctx context.Context, h sshHost) (string, error) {
	sshMu.Lock()
	s := p.socket
	sshMu.Unlock()
	if s != "" {
		return s, nil
	}
	s, err := discoverSocket(ctx, p.c, h)
	if err != nil {
		return "", err
	}
	sshMu.Lock()
	if p.socket == "" {
		p.socket = s
	}
	s = p.socket
	sshMu.Unlock()
	return s, nil
}

// rexStatus asks the host's Rex server where it listens; rexProbe finds
// the socket among the usual paths when status names none, the same
// list the design's fallback walks.
const (
	rexStatus = `sh -c 'rex server status --json'`
	rexProbe  = `sh -c 'for p in "$XDG_RUNTIME_DIR/rex/server.sock" "$HOME/.local/share/rex/server.sock" /run/rex/server.sock /var/run/rex/server.sock "$HOME/Library/Application Support/rex/server.sock"; do [ -S "$p" ] && { echo "$p"; break; }; done'`
)

// discoverSocket is the host's Rex socket: the primary unix listener of
// rex server status, else the first of the usual paths that exists, as
// upstream's discovery does (docs/IOS-023).
func discoverSocket(ctx context.Context, c *ssh.Client, h sshHost) (string, error) {
	if out, err := sshExec(ctx, c, rexStatus); err == nil {
		if s := primarySocket(out); s != "" {
			return s, nil
		}
	}
	out, err := sshExec(ctx, c, rexProbe)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(out, "\n")
	if line = strings.TrimSpace(line); line != "" {
		return line, nil
	}
	return "", errors.New("no rex socket on " + h.Host)
}

// sshStatusResult is the listeners of rex server status --json
// (internal/rexapi's statusResult), as far as the socket needs them.
type sshStatusResult struct {
	Listeners []struct {
		Endpoint  string `json:"endpoint"`
		Transport string `json:"transport"`
		Primary   bool   `json:"primary"`
	} `json:"listeners"`
}

// primarySocket is the endpoint path of the primary unix listener of a
// server status answer, "" when it reports none.
func primarySocket(out string) string {
	var st sshStatusResult
	if json.Unmarshal([]byte(out), &st) != nil {
		return ""
	}
	for _, l := range st.Listeners {
		if l.Primary && strings.HasPrefix(l.Transport, "unix") {
			return endpointPath(l.Endpoint)
		}
	}
	return ""
}

// endpointPath is the socket path of a unix listener's endpoint:
// unix:///path, escaped as internal/rexapi's unixEndpoint writes it, or
// a bare path.
func endpointPath(endpoint string) string {
	if strings.HasPrefix(endpoint, "unix://") {
		if u, err := url.Parse(endpoint); err == nil {
			return u.Path
		}
		return ""
	}
	if strings.Contains(endpoint, "://") {
		return ""
	}
	return endpoint
}

// sshExec runs command in a session channel and returns its output, as
// discovery's execs do.
func sshExec(ctx context.Context, c *ssh.Client, command string) (string, error) {
	ch, in, err := c.OpenChannel("session", nil)
	if err != nil {
		return "", err
	}
	go ssh.DiscardRequests(in)
	ok, err := ch.SendRequest("exec", true, ssh.Marshal(&struct{ Command string }{command}))
	if err != nil {
		ch.Close()
		return "", err
	}
	if !ok {
		ch.Close()
		return "", errors.New("exec refused")
	}
	go io.Copy(io.Discard, ch.Stderr())
	stop := context.AfterFunc(ctx, func() { ch.Close() })
	defer stop()
	defer ch.Close()
	b, err := io.ReadAll(ch)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// streamLocalMsg is the payload of a direct-streamlocal channel open:
// the socket path and two reserved fields, in the order OpenSSH's
// PROTOCOL and x/crypto's streamLocalChannelOpenDirectMsg marshal them.
// (x/crypto's Client.dialStreamLocal is not exported.)
type streamLocalMsg struct {
	SocketPath string
	Reserved0  string
	Reserved1  uint32
}

// openStreamLocal opens the channel to the remote Unix socket, as
// ssh -L does for upstream's CLI.
func openStreamLocal(ctx context.Context, c *ssh.Client, socket string) (ssh.Channel, error) {
	type result struct {
		ch  ssh.Channel
		err error
	}
	done := make(chan result, 1)
	go func() {
		ch, in, err := c.OpenChannel("direct-streamlocal@openssh.com", ssh.Marshal(&streamLocalMsg{SocketPath: socket}))
		if err == nil {
			go ssh.DiscardRequests(in)
			go io.Copy(io.Discard, ch.Stderr())
		}
		done <- result{ch, err}
	}()
	select {
	case r := <-done:
		return r.ch, r.err
	case <-ctx.Done():
		// An open that finishes after the dial gave up is closed:
		// nothing else holds it.
		go func() {
			if r := <-done; r.ch != nil {
				r.ch.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// sshChanConn is a streamlocal channel as the net.Conn the SOCKS server
// takes: deadlines do nothing, as a channel has none. Its ends are
// loopback TCP addresses: the SOCKS server reads LocalAddr back as
// host:port for its reply, as it does a dialed conn's.
type sshChanConn struct {
	ssh.Channel
	laddr, raddr net.Addr
}

func (c *sshChanConn) LocalAddr() net.Addr              { return c.laddr }
func (c *sshChanConn) RemoteAddr() net.Addr             { return c.raddr }
func (c *sshChanConn) SetDeadline(time.Time) error      { return nil }
func (c *sshChanConn) SetReadDeadline(time.Time) error  { return nil }
func (c *sshChanConn) SetWriteDeadline(time.Time) error { return nil }

// CloseRead is here for the callers that check nettype.HalfCloser, as
// tailcatConn's is; a channel can't close a read.
func (c *sshChanConn) CloseRead() error { return errors.ErrUnsupported }
