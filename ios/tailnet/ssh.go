package tailnet

// Rex Go reaches a host over SSH (docs/IOS-023-ssh-hosts.md): the proxy
// dials <id>.ssh.invalid as a direct-streamlocal channel to the host's
// Rex socket — or, when the host's endpoint is TCP, a direct-tcpip
// channel to its listener, for Windows hosts and rexapi LAN gateways
// streamlocal cannot reach (docs/IOS-024-ssh-tcp.md) — pooled under one
// ssh.Client per host as upstream's shared connection is. The device
// key stays on the phone: the app keeps it in its Keychain and passes
// it in, as the tailcat key's. Phase 1 is key auth only; a host-key the
// store does not name asks the app through SSHDelegate, as rootshell's
// HostKeyValidationRequest does.

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
	"net/netip"
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
	// sshTokens is each host's rexapi LAN pairing token, by its id, as
	// a tcp endpoint's exec learned it ("" cached for a host that
	// answered none): it holds until the host's record changes, as
	// sshTrusted does.
	sshTokens = map[string]string{}
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

// SSHHostToken is the rexapi LAN pairing token of the host id's tcp
// endpoint, learned over the SSH connection (docs/IOS-024): the app
// sends it to the gateway as a LAN host's, from the Keychain. "" before
// a tcp dial fetched it, and for a host that has none.
func SSHHostToken(id string) string {
	sshMu.Lock()
	defer sshMu.Unlock()
	return sshTokens[strings.ToLower(id)]
}

// sshHost is an SSH host's record, as the app's Add Host form writes it
// (docs/IOS-023): id is the proxy's <id>.ssh.invalid name, socket the
// remote Rex socket, tcp a host:port to forward to instead
// (docs/IOS-024; both "" for discovery), hostKey the trusted
// SHA256 fingerprint ("" for trust on first use).
type sshHost struct {
	ID      string `json:"id"`
	User    string `json:"user"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Socket  string `json:"socket"`
	TCP     string `json:"tcp"`
	HostKey string `json:"hostKey"`
}

// SetSSHHosts sets the hosts the proxy reaches over SSH, as a JSON list
// of {"id", "user", "host", "port", "socket", "tcp", "hostKey"}. The
// clients of hosts no longer listed, or whose record changed, close.
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
		if h.Socket != "" && h.TCP != "" {
			return errors.New("an ssh host takes a socket or a tcp endpoint, not both")
		}
		if h.Socket != "" && !strings.HasPrefix(h.Socket, "/") {
			return errors.New("ssh socket is not an absolute path")
		}
		if h.TCP != "" {
			if _, _, err := tcpAddr(h.TCP); err != nil {
				return errors.New("invalid ssh tcp endpoint")
			}
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
	for id := range sshTokens {
		if gone(id) {
			delete(sshTokens, id)
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

// tcpAddr reads a direct-tcpip target: host and port of host:port, the
// port a number that fits the channel's uint32.
func tcpAddr(s string) (string, uint32, error) {
	host, ps, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.ParseUint(ps, 10, 16)
	if host == "" || err != nil || port == 0 {
		return "", 0, errors.New("not host:port: " + s)
	}
	return host, uint32(port), nil
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
// when c or err is set. endpoint is what discovery found, kept for the
// client's life as a host's endpoint doesn't move.
type pooledSSH struct {
	done     chan struct{}
	c        *ssh.Client
	err      error
	endpoint sshEndpoint
}

// sshEndpoint is where the host's Rex API listens: a unix socket for a
// direct-streamlocal channel, or a host:port for direct-tcpip. One of
// the two is set.
type sshEndpoint struct {
	socket string
	tcp    string
}

// dialSSH connects to the Rex endpoint of the ssh host id, within
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

// dialSSHHost opens a channel to h's endpoint with the pooled client.
// A channel that won't open drops the client and retries once
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
		if ch, err = openEndpoint(ctx, id, p, h); err == nil {
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

// openEndpoint opens the data channel to the host's Rex endpoint: the
// one the record names, else the one discovery found for this client —
// direct-tcpip to a host:port, direct-streamlocal to a socket. A tcp
// endpoint also learns the gateway's pairing token, once per record.
func openEndpoint(ctx context.Context, id string, p *pooledSSH, h sshHost) (ssh.Channel, error) {
	e := sshEndpoint{socket: h.Socket, tcp: h.TCP}
	if e == (sshEndpoint{}) {
		found, err := p.rexEndpoint(ctx)
		if err != nil {
			return nil, err
		}
		e = found
	}
	if e.tcp != "" {
		lanToken(ctx, id, p.c)
		return openTCPIP(ctx, p.c, e.tcp)
	}
	return openStreamLocal(ctx, p.c, e.socket)
}

// rexEndpoint is the endpoint discovery found for this client, run once
// and kept in p.
func (p *pooledSSH) rexEndpoint(ctx context.Context) (sshEndpoint, error) {
	sshMu.Lock()
	e := p.endpoint
	sshMu.Unlock()
	if e != (sshEndpoint{}) {
		return e, nil
	}
	e, err := discoverEndpoint(ctx, p.c)
	if err != nil {
		return sshEndpoint{}, err
	}
	sshMu.Lock()
	if p.endpoint == (sshEndpoint{}) {
		p.endpoint = e
	}
	e = p.endpoint
	sshMu.Unlock()
	return e, nil
}

// rexStatus asks the host's Rex server where it listens, GoRex's CLI
// name first, then upstream's; rexProbe finds the socket among the
// usual paths when status names none, the same list the design's
// fallback walks; lanTokens is the pairing token of the host's rexapi
// LAN gateway (docs/IOS-024).
const (
	// rexStatus and lanTokens wrap in no sh: cmd and the unix login
	// shells all read `a || b`, and a Windows sshd has no sh.
	rexStatus = `gorex server status --json || rex server status --json`
	rexProbe  = `sh -c 'for p in "$XDG_RUNTIME_DIR/rex/server.sock" "$HOME/.local/share/rex/server.sock" /run/rex/server.sock /var/run/rex/server.sock "$HOME/Library/Application Support/rex/server.sock"; do [ -S "$p" ] && { echo "$p"; break; }; done'`
	lanStatus = `gorex rexapi lan status || rex rexapi lan status`
	lanTokens = `gorex rexapi lan token show || rex rexapi lan token show`
)

// lanProbe is the endpoint discovery falls to when the host reports
// neither a unix nor a tcp listener, no usual socket path exists and
// the LAN CLI names no gateway — GoRex's rexapi LAN port on loopback
// (internal/rexapi's LANPort), where a named-pipe host's gateway is. A
// var, for the test's listener.
var lanProbe = "127.0.0.1:7297"

// discoverEndpoint is the host's Rex endpoint: the primary unix
// listener of server status, else a tcp listener's host:port, else the
// first of the usual socket paths that exists, else the LAN probe, as
// upstream's discovery does (docs/IOS-023, docs/IOS-024).
func discoverEndpoint(ctx context.Context, c *ssh.Client) (sshEndpoint, error) {
	if out, err := sshExec(ctx, c, rexStatus); err == nil {
		if e := statusEndpoint(out); e != (sshEndpoint{}) {
			return e, nil
		}
	}
	if out, err := sshExec(ctx, c, rexProbe); err == nil {
		line, _, _ := strings.Cut(out, "\n")
		if line = strings.TrimSpace(line); line != "" {
			return sshEndpoint{socket: line}, nil
		}
	}
	if addr := lanListen(ctx, c); addr != "" {
		return sshEndpoint{tcp: addr}, nil
	}
	return sshEndpoint{tcp: lanProbe}, nil
}

// lanListen is the rexapi LAN gateway's address as the host's CLI
// reports it: "lan: listening on http://addr", else the configured
// "listen: addr". It answers where status has no tcp listener — a
// gateway bound to the LAN address, which the loopback probe cannot
// reach — and "" where no gateway is on.
func lanListen(ctx context.Context, c *ssh.Client) string {
	out, err := sshExec(ctx, c, lanStatus)
	if err != nil {
		return ""
	}
	var configured string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if a, ok := strings.CutPrefix(line, "lan: listening on "); ok {
			if t := tcpTarget(strings.TrimSpace(a)); t != "" {
				return t
			}
		}
		if a, ok := strings.CutPrefix(line, "listen: "); ok {
			configured = a
		}
	}
	return tcpTarget(strings.TrimSpace(configured))
}

// sshStatusResult is the listeners of server status --json
// (internal/rexapi's statusResult), as far as the endpoint needs them.
type sshStatusResult struct {
	Listeners []struct {
		Endpoint  string `json:"endpoint"`
		Transport string `json:"transport"`
		Primary   bool   `json:"primary"`
	} `json:"listeners"`
}

// statusEndpoint is the listener of a server status answer the proxy
// forwards to: the primary unix one's socket first, as today, else a
// tcp one's host:port — a loopback among several, which the forwarded
// channel reaches as the host's own (docs/IOS-024).
func statusEndpoint(out string) sshEndpoint {
	var st sshStatusResult
	if json.Unmarshal([]byte(out), &st) != nil {
		return sshEndpoint{}
	}
	for _, l := range st.Listeners {
		if l.Primary && strings.HasPrefix(l.Transport, "unix") {
			return sshEndpoint{socket: endpointPath(l.Endpoint)}
		}
	}
	var first string
	for _, l := range st.Listeners {
		if l.Transport != "tcp" {
			continue
		}
		t := tcpTarget(l.Endpoint)
		if t == "" {
			continue
		}
		if first == "" {
			first = t
		}
		if host, _, err := net.SplitHostPort(t); err == nil {
			if a, err := netip.ParseAddr(host); err == nil && a.IsLoopback() {
				return sshEndpoint{tcp: t}
			}
		}
	}
	return sshEndpoint{tcp: first}
}

// tcpTarget is the direct-tcpip target a tcp listener's endpoint gives:
// host:port, as written or after a scheme, a wildcard host read as
// loopback; "" when the endpoint is not one.
func tcpTarget(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		endpoint = u.Host
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || port == "" {
		return ""
	}
	if host == "" {
		return "127.0.0.1:" + port
	}
	if a, err := netip.ParseAddr(host); err == nil && a.IsUnspecified() {
		return "127.0.0.1:" + port
	}
	return endpoint
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

// lanToken learns the host's rexapi LAN pairing token over the client,
// once per record, kept under id for SSHHostToken. A host that cannot
// answer — no gorex or rex CLI — caches "", and a channel error retries
// on a later dial.
func lanToken(ctx context.Context, id string, c *ssh.Client) {
	sshMu.Lock()
	_, done := sshTokens[id]
	sshMu.Unlock()
	if done {
		return
	}
	out, err := sshExec(ctx, c, lanTokens)
	if err != nil {
		return
	}
	var token string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			token = line
			break
		}
	}
	sshMu.Lock()
	sshTokens[id] = token
	sshMu.Unlock()
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

// directTCPIPMsg is the payload of a direct-tcpip channel open (RFC
// 4254 §7.2): the target to forward to, then the originator the server
// is told, loopback for a channel the proxy asked for.
type directTCPIPMsg struct {
	TargetAddr     string
	TargetPort     uint32
	OriginatorAddr string
	OriginatorPort uint32
}

// openStreamLocal opens the channel to the remote Unix socket, as
// ssh -L does for upstream's CLI.
func openStreamLocal(ctx context.Context, c *ssh.Client, socket string) (ssh.Channel, error) {
	return openChannel(ctx, c, "direct-streamlocal@openssh.com", ssh.Marshal(&streamLocalMsg{SocketPath: socket}))
}

// openTCPIP opens the channel to the remote host:port: plain port
// forwarding, which every sshd answers, Win32-OpenSSH's included
// (docs/IOS-024).
func openTCPIP(ctx context.Context, c *ssh.Client, addr string) (ssh.Channel, error) {
	host, port, err := tcpAddr(addr)
	if err != nil {
		return nil, err
	}
	return openChannel(ctx, c, "direct-tcpip", ssh.Marshal(&directTCPIPMsg{
		TargetAddr: host, TargetPort: port, OriginatorAddr: "127.0.0.1"}))
}

// openChannel opens a channel of typ carrying payload, bounded by ctx.
// An open that finishes after the dial gave up is closed: nothing else
// holds it.
func openChannel(ctx context.Context, c *ssh.Client, typ string, payload []byte) (ssh.Channel, error) {
	type result struct {
		ch  ssh.Channel
		err error
	}
	done := make(chan result, 1)
	go func() {
		ch, in, err := c.OpenChannel(typ, payload)
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
		go func() {
			if r := <-done; r.ch != nil {
				r.ch.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// sshChanConn is a data channel as the net.Conn the SOCKS server
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
