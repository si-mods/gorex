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
	sshMu   sync.Mutex
	sshKeys []sshKeyEntry
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
	// a tcp endpoint's exec learned it ("" cached for a host whose CLI
	// answered that it has none): it holds until the host's record
	// changes, as sshTrusted does, or SSHForgetHostToken. sshTokenFailed
	// is when a host's token exec last failed, so that a host with no CLI
	// does not run it on every dial; it never caches "".
	sshTokens      = map[string]string{}
	sshTokenFailed = map[string]time.Time{}
	// sshPasswords is each host's answered password, by its id, kept in
	// memory for the run and never written (docs/IOS-023), from the
	// password method or a keyboard-interactive password prompt;
	// sshPasswordAuth marks the hosts whose client logged in by a secret
	// the user typed, for the install-key offer.
	sshPasswords    = map[string]string{}
	sshPasswordAuth = map[string]bool{}
	// sshDelegate answers the questions a dial can't; sshPending is
	// each host's question waiting for an answer, by its id. A second
	// check of the same question shares the one's answer, and a retry
	// of a dial that timed out reuses it instead of asking again, as
	// rootshell's serialised queue does.
	sshDelegate SSHDelegate
	sshPending  = map[string]*sshPendingQ{}
	sshQID      atomic.Int64
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

// sshKeyEntry is one parsed local SSH key kept in memory.
type sshKeyEntry struct {
	id        string
	signer    ssh.Signer
	isDefault bool
}

// sshKeyInput is one entry in SetSSHKeys' JSON input.
type sshKeyInput struct {
	ID        string `json:"id"`
	PEM       string `json:"pem"`
	IsDefault bool   `json:"isDefault"`
}

// parsedSSHKey is the JSON ParseSSHKey returns to the app.
type parsedSSHKey struct {
	PEM         string `json:"pem"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
}

// ParseSSHKey parses an OpenSSH or PEM private key, decrypting it with
// passphrase when given, and returns JSON with the unencrypted OpenSSH
// PEM, authorized_keys public line, SHA256 fingerprint, and algorithm.
func ParseSSHKey(pemKey, passphrase string) (string, error) {
	var (
		raw any
		err error
	)
	if passphrase != "" {
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase([]byte(pemKey), []byte(passphrase))
	} else {
		raw, err = ssh.ParseRawPrivateKey([]byte(pemKey))
	}
	if err != nil {
		return "", err
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return "", err
	}
	normalized := pemKey
	if block, err := ssh.MarshalPrivateKey(raw, ""); err == nil {
		normalized = string(pem.EncodeToMemory(block))
	}
	pub := signer.PublicKey()
	out := parsedSSHKey{
		PEM:         normalized,
		PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))),
		Fingerprint: ssh.FingerprintSHA256(pub),
		Type:        pub.Type(),
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SetSSHKeys sets the local SSH keys the app manages (docs/IOS-026),
// as a JSON list of {"id", "pem", "isDefault"}. Any change drops the
// pooled SSH clients so they reconnect with the new key set.
func SetSSHKeys(keysJSON string) error {
	var inputs []sshKeyInput
	if err := json.Unmarshal([]byte(keysJSON), &inputs); err != nil {
		return err
	}
	next := make([]sshKeyEntry, 0, len(inputs))
	for _, k := range inputs {
		s, err := ssh.ParsePrivateKey([]byte(k.PEM))
		if err != nil {
			return err
		}
		next = append(next, sshKeyEntry{
			id:        strings.ToLower(k.ID),
			signer:    s,
			isDefault: k.IsDefault,
		})
	}
	setSSHKeyEntries(next)
	return nil
}

// SetSSHKey sets the single device private key (legacy wrapper for
// SetSSHKeys). A new key drops the clients of the old one.
func SetSSHKey(pemKey string) error {
	s, err := ssh.ParsePrivateKey([]byte(pemKey))
	if err != nil {
		return err
	}
	setSSHKeyEntries([]sshKeyEntry{{id: "", signer: s, isDefault: true}})
	return nil
}

func sshKeyEntriesEqual(a, b []sshKeyEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].id != b[i].id || a[i].isDefault != b[i].isDefault {
			return false
		}
		if !bytes.Equal(a[i].signer.PublicKey().Marshal(), b[i].signer.PublicKey().Marshal()) {
			return false
		}
	}
	return true
}

func setSSHKeyEntries(next []sshKeyEntry) {
	sshMu.Lock()
	if sshKeyEntriesEqual(sshKeys, next) {
		sshMu.Unlock()
		return
	}
	sshKeys = next
	old := sshClients
	sshClients = map[string]*pooledSSH{}
	for _, sc := range sshScopes {
		sc.cancel() // dials of the old key
	}
	sshScopes = map[string]sshScope{}
	sshPasswords = map[string]string{}
	sshPasswordAuth = map[string]bool{}
	sshMu.Unlock()
	for _, p := range old {
		if p.c != nil {
			go p.c.Close() // a stuck close would block the caller
		}
	}
}

// SSHPublicKey is the primary default key's public half, as
// authorized_keys takes it; "" when no keys are loaded.
func SSHPublicKey() string {
	sshMu.Lock()
	defer sshMu.Unlock()
	return sshPublicKeyForLocked("")
}

// sshSignersForLocked returns the signers to offer for a host whose
// record names keyID ("" for automatic). sshMu must be held.
func sshSignersForLocked(keyID string) []ssh.Signer {
	if id := strings.ToLower(keyID); id != "" {
		for _, k := range sshKeys {
			if k.id == id {
				return []ssh.Signer{k.signer}
			}
		}
	}
	var out []ssh.Signer
	for _, k := range sshKeys {
		if k.isDefault {
			out = append(out, k.signer)
		}
	}
	if len(out) == 0 {
		for _, k := range sshKeys {
			out = append(out, k.signer)
		}
	}
	return out
}

// sshPublicKeyForLocked returns the authorized_keys public line for
// keyID, or the first default key (else first key). sshMu must be held.
func sshPublicKeyForLocked(keyID string) string {
	signers := sshSignersForLocked(keyID)
	if len(signers) == 0 {
		return ""
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signers[0].PublicKey())))
}

// SSHDelegate answers the questions a dial can't, as ShareDelegate does
// for hotspot share. gomobile binds this interface to a Swift protocol.
// The host, port, fingerprint and keyType of OnSSHHostKey are for the
// card it shows; previous is the fingerprint the host was trusted by,
// "" on a first sighting. OnSSHAuth asks for a secret: prompt is what
// the server asked (a password, a keyboard-interactive question), retry
// marks a second ask after a refused answer, and secret tells the card
// to hide the typing. The answers come back through SSHAnswer and
// SSHAuthAnswer.
type SSHDelegate interface {
	OnSSHHostKey(qid int64, user, host string, port int, fingerprint, previous, keyType string)
	OnSSHAuth(qid int64, user, host string, port int, prompt string, retry, secret bool)
}

// SetSSHDelegate sets the delegate the questions ask, or nil to refuse
// any key the store does not name and fail a dial that needs a secret.
func SetSSHDelegate(d SSHDelegate) {
	sshMu.Lock()
	sshDelegate = d
	sshMu.Unlock()
}

// sshPendingQ is a host's question waiting for an answer (sshPending):
// kind names it ("hostkey" or "auth") and match tells it from the
// other's of the same kind, the fingerprint or the prompt. done closes
// on the answer, which trust, or secret and refused, carries to every
// waiter.
type sshPendingQ struct {
	qid     int64
	id      string
	kind    string
	match   string
	done    chan struct{}
	trust   bool
	secret  string
	refused bool
}

// sshPendingFor attaches to or makes the host's pending question, as
// the pending map wants. sshMu must be held. A made one (mine) the
// caller posts to the delegate; an existing one of the same question
// shares its answer; a different one (other) the caller waits out and
// retries, as rootshell's serialised queue asks one card at a time.
func sshPendingFor(id, kind, match string) (q *sshPendingQ, mine bool, other *sshPendingQ) {
	q = sshPending[id]
	if q == nil {
		q = &sshPendingQ{qid: sshQID.Add(1), id: id, kind: kind, match: match, done: make(chan struct{})}
		sshPending[id] = q
		return q, true, nil
	}
	if q.kind == kind && q.match == match {
		return q, false, nil
	}
	return nil, false, q
}

// sshResolve answers the pending question of the qid and closes its
// done. sshMu must be held.
func sshResolve(qid int64, set func(q *sshPendingQ)) {
	for id, q := range sshPending {
		if q.qid != qid {
			continue
		}
		delete(sshPending, id)
		set(q)
		close(q.done)
		return
	}
}

// SSHAnswer gives the answer to a pending host-key question; a qid no
// question waits on is ignored. A trusted fingerprint holds for the
// run, as a saved one does — the card's Trust & Save writes it to the
// host's record, Connect Once answers without writing.
func SSHAnswer(qid int64, trust bool) {
	sshMu.Lock()
	defer sshMu.Unlock()
	sshResolve(qid, func(q *sshPendingQ) {
		q.trust = trust
		if trust {
			sshTrusted[q.id] = q.match
		}
	})
}

// SSHAuthAnswer gives the answer to a pending secret question: ok
// false is the cancel a refused card sends, and with ok an empty secret
// is an answer, as Return at a notice or an empty password is.
func SSHAuthAnswer(qid int64, secret string, ok bool) {
	sshMu.Lock()
	defer sshMu.Unlock()
	sshResolve(qid, func(q *sshPendingQ) {
		q.secret = secret
		q.refused = !ok
	})
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

// SSHForgetHostToken forgets the pairing token learned for the host id,
// so that its next tcp dial reads it again: the app calls it when the
// gateway refuses the token it kept, as after gorex rexapi lan token
// rotate (docs/IOS-024).
func SSHForgetHostToken(id string) {
	id = strings.ToLower(id)
	sshMu.Lock()
	defer sshMu.Unlock()
	delete(sshTokens, id)
	delete(sshTokenFailed, id)
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
	KeyID   string `json:"keyId"`
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
	for id := range sshTokenFailed {
		if gone(id) {
			delete(sshTokenFailed, id)
		}
	}
	for id := range sshPasswords {
		if gone(id) {
			delete(sshPasswords, id)
		}
	}
	for id := range sshPasswordAuth {
		if gone(id) {
			delete(sshPasswordAuth, id)
		}
	}
	// A pending question of a changed record stays until its card
	// answers while the record names the same login (user, host, port):
	// its waiters leave on the cancelled scope, and a dial of the new
	// record that asks the same question shares the card still up, as
	// after Trust & Save, which changes the record while a password card
	// shows. A question of a removed host, or of a login that moved,
	// goes: its card's answer is for another machine, and is ignored.
	for id := range sshPending {
		n, ok := m[id]
		o := sshHosts[id]
		if !ok || n.User != o.User || n.Host != o.Host || n.Port != o.Port {
			delete(sshPending, id)
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
		signers := sshSignersForLocked(h.KeyID)
		if len(signers) == 0 && sshDelegate == nil {
			sshMu.Unlock()
			return nil, errors.New("no ssh key")
		}
		p = &pooledSSH{done: make(chan struct{})}
		sshClients[id] = p
		go func() {
			c, err := connectSSH(ctx, id, h, signers)
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
func connectSSH(ctx context.Context, id string, h sshHost, signers []ssh.Signer) (*ssh.Client, error) {
	addr := net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
	c, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	var st sshAuthState
	config := &ssh.ClientConfig{
		User: h.User,
		Auth: sshAuthMethods(ctx, id, h, signers, &st),
		// No handshake Timeout: the host-key and secret questions need
		// longer than a dial takes to answer, and the dial's own ctx
		// bounds the connect by closing c.
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error { return sshHostKey(ctx, id, h, key) },
	}
	conn, chans, reqs, err := ssh.NewClientConn(c, addr, config)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		c.Close()
		// A password the host refused is no password to send again:
		// a cached one that went stale (changed on the host), or one
		// typed in this connect and cached before the host answered.
		// The next dial asks again.
		if sshAuthRefused(err) {
			sshMu.Lock()
			for _, p := range []string{st.cached, st.typedPassword} {
				if p != "" && sshPasswords[id] == p {
					delete(sshPasswords, id)
				}
			}
			sshMu.Unlock()
		}
		return nil, err
	}
	sshMu.Lock()
	if st.kiPassword != "" {
		sshPasswords[id] = st.kiPassword
	}
	if st.typed {
		sshPasswordAuth[id] = true
	}
	sshMu.Unlock()
	return ssh.NewClient(conn, chans, reqs), nil
}

// sshHostKey is the client's host-key check: the fingerprint the host's
// record names or one trusted for this run connects; another asks the
// delegate, and a trusted answer holds for the run. The question
// outlives its dial: a second check of the same fingerprint shares it,
// and a retry after a timed-out dial reattaches instead of asking
// again. With no delegate the error carries the fingerprint for the
// app log.
func sshHostKey(ctx context.Context, id string, h sshHost, key ssh.PublicKey) error {
	fingerprint := ssh.FingerprintSHA256(key)
	for {
		sshMu.Lock()
		if fingerprint == h.HostKey || sshTrusted[id] != "" && sshTrusted[id] == fingerprint {
			sshMu.Unlock()
			return nil
		}
		d := sshDelegate
		if d == nil {
			sshMu.Unlock()
			return errors.New("untrusted ssh host key: " + fingerprint)
		}
		// The fingerprint trusted before: the record's, else the one
		// Connect Once trusted for the run, so that a changed key warns
		// either way.
		previous := h.HostKey
		if previous == "" {
			previous = sshTrusted[id]
		}
		q, mine, other := sshPendingFor(id, "hostkey", fingerprint)
		sshMu.Unlock()
		if other != nil {
			if err := sshWait(ctx, other); err != nil {
				return err
			}
			continue
		}
		if mine {
			d.OnSSHHostKey(q.qid, h.User, h.Host, h.Port, fingerprint, previous, key.Type())
		}
		select {
		case <-q.done:
			if !q.trust {
				return errors.New("ssh host key refused: " + fingerprint)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// sshWait waits out a different pending question of the same host, as
// the queue asks one card at a time.
func sshWait(ctx context.Context, q *sshPendingQ) error {
	select {
	case <-q.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sshAuthRefused tells a handshake the host refused at authentication:
// every method failed, or the host hung up after too many tries.
func sshAuthRefused(err error) bool {
	s := err.Error()
	return strings.Contains(s, "unable to authenticate") || strings.Contains(s, "too many authentication failures")
}

// sshAuthState is what a connect's auth did, for connectSSH to keep
// once the handshake ends: typed marks a secret the user typed, for the
// install-key offer (a reconnect on the run's cached password offers
// nothing again); cached is the cached password a method sent, and
// typedPassword the last one the password method asked, each dropped
// from the cache when the host refuses the login; kiPassword is a keyboard-interactive
// password typed, cached only once the host takes it, as PAM's sshd
// refuses a wrong one without asking again.
type sshAuthState struct {
	mu            sync.Mutex
	typed         bool
	cached        string
	typedPassword string
	kiPassword    string
}

// sshAuthMethods is the connect's auth: the device key, then the
// delegate's password and keyboard-interactive questions, as
// docs/IOS-023 orders them, recording in st what they sent. A run that
// cannot ask keeps the keys alone, as a delegate's absence can't answer.
func sshAuthMethods(ctx context.Context, id string, h sshHost, signers []ssh.Signer, st *sshAuthState) []ssh.AuthMethod {
	var auth []ssh.AuthMethod
	if len(signers) > 0 {
		auth = append(auth, ssh.PublicKeys(signers...))
	}
	sshMu.Lock()
	delegated := sshDelegate != nil
	cached := sshPasswords[id]
	sshMu.Unlock()
	if !delegated {
		return auth
	}
	attempts := 0
	auth = append(auth, ssh.RetryableAuthMethod(ssh.PasswordCallback(func() (string, error) {
		attempts++
		if attempts == 1 && cached != "" {
			st.mu.Lock()
			st.cached = cached
			st.mu.Unlock()
			return cached, nil
		}
		s, err := sshAskAuth(ctx, id, h, "Password", attempts > 1 || cached != "", true)
		if err != nil {
			return "", err
		}
		st.mu.Lock()
		st.typed = true
		st.typedPassword = s
		st.mu.Unlock()
		sshMu.Lock()
		sshPasswords[id] = s
		sshMu.Unlock()
		return s, nil
	}), 4))
	// answered is the prompts an earlier round answered: the server
	// asking one again is a retry after a refused answer. A second
	// question of one round is not.
	answered := map[string]bool{}
	auth = append(auth, ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		prompts := make([]string, len(questions))
		for i, q := range questions {
			prompt := strings.TrimSpace(q)
			if prompt == "" {
				prompt = strings.TrimSpace(instruction)
			}
			if prompt == "" {
				prompt = strings.TrimSpace(name)
			}
			if prompt == "" {
				prompt = "Response"
			}
			prompts[i] = prompt
			secret := true
			if i < len(echos) {
				secret = !echos[i]
			}
			retry := answered[prompt]
			// A password prompt takes the run's cached password first,
			// as the password method does: PAM's sshd (macOS's default)
			// asks it here, not there.
			password := secret && strings.Contains(strings.ToLower(prompt), "password")
			if password && !retry {
				sshMu.Lock()
				p := sshPasswords[id]
				sshMu.Unlock()
				if p != "" {
					st.mu.Lock()
					st.cached = p
					st.mu.Unlock()
					answers[i] = p
					continue
				}
			}
			s, err := sshAskAuth(ctx, id, h, prompt, retry, secret)
			if err != nil {
				return nil, err
			}
			answers[i] = s
			st.mu.Lock()
			if secret {
				st.typed = true
			}
			if password {
				st.kiPassword = s
			}
			st.mu.Unlock()
		}
		for _, p := range prompts {
			answered[p] = true
		}
		return answers, nil
	}))
	return auth
}

// sshAskAuth asks the delegate for a secret, sharing one card's answer
// as the host-key check's does: a retry after a timed-out dial
// reattaches to the card still up, and a second question queues behind
// it. A refused card fails the dial as a cancel does; an empty answer
// is an answer.
func sshAskAuth(ctx context.Context, id string, h sshHost, prompt string, retry, secret bool) (string, error) {
	for {
		sshMu.Lock()
		d := sshDelegate
		if d == nil {
			sshMu.Unlock()
			return "", errors.New("ssh cannot ask questions")
		}
		q, mine, other := sshPendingFor(id, "auth", prompt)
		sshMu.Unlock()
		if other != nil {
			if err := sshWait(ctx, other); err != nil {
				return "", err
			}
			continue
		}
		if mine {
			d.OnSSHAuth(q.qid, h.User, h.Host, h.Port, prompt, retry, secret)
		}
		select {
		case <-q.done:
			if q.refused {
				return "", errors.New("ssh question refused: " + prompt)
			}
			return q.secret, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// SSHTakePasswordOffer tells, once per secret login, that a client of
// the host id logged in by a secret the user typed (password or
// keyboard-interactive): the app's offer to install the device key
// shows then, and not again until the user types one again — a
// reconnect on the run's cached password, after Not Now, offers
// nothing.
func SSHTakePasswordOffer(id string) bool {
	sshMu.Lock()
	defer sshMu.Unlock()
	if !sshPasswordAuth[strings.ToLower(id)] {
		return false
	}
	delete(sshPasswordAuth, strings.ToLower(id))
	return true
}

// SSHInstallKey installs the device key on the host id over its client,
// as ssh-copy-id does: it makes ~/.ssh with its permissions and appends
// the public key to authorized_keys under a rexgo comment, missing none
// the host has, then checks that authorized_keys holds it. A command
// that fails — a host whose shell is cmd, a read-only ~/.ssh — or a key
// the check does not find is an error (docs/IOS-023).
func SSHInstallKey(id string) error {
	id = strings.ToLower(id)
	sshMu.Lock()
	p := sshClients[id]
	h := sshHosts[id]
	pub := sshPublicKeyForLocked(h.KeyID)
	sshMu.Unlock()
	if p == nil || p.c == nil {
		return errors.New("ssh host not connected: " + id)
	}
	if pub == "" {
		return errors.New("no ssh key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := "umask 077; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && " +
		"grep -qF '" + pub + "' ~/.ssh/authorized_keys 2>/dev/null || " +
		"printf '%s\\n' '" + pub + " rexgo' >> ~/.ssh/authorized_keys"
	if _, err := sshExec(ctx, p.c, cmd); err != nil {
		return errors.New("installing the key failed: " + err.Error())
	}
	if _, err := sshExec(ctx, p.c, installCheck(pub)); err != nil {
		return errors.New("the key is not in ~/.ssh/authorized_keys after the install: " + err.Error())
	}
	return nil
}

// installCheck is the exec that tells whether authorized_keys holds the
// public key pub: exit status 0 when it does.
func installCheck(pub string) string {
	return "grep -qF '" + pub + "' ~/.ssh/authorized_keys"
}

// openEndpoint opens the data channel to the host's Rex endpoint: the
// one the record names, else the one discovery found for this client —
// direct-tcpip to a host:port, direct-streamlocal to a socket. A tcp
// endpoint also learns the gateway's pairing token, once per record.
func openEndpoint(ctx context.Context, id string, p *pooledSSH, h sshHost) (ssh.Channel, error) {
	e := sshEndpoint{socket: h.Socket, tcp: h.TCP}
	if e == (sshEndpoint{}) {
		found, err := p.rexEndpoint(ctx, h.Host)
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
// and kept in p; host names the machine in discovery's error.
func (p *pooledSSH) rexEndpoint(ctx context.Context, host string) (sshEndpoint, error) {
	sshMu.Lock()
	e := p.endpoint
	sshMu.Unlock()
	if e != (sshEndpoint{}) {
		return e, nil
	}
	e, err := discoverEndpoint(ctx, p.c, host)
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

// rexCLIs are the names discovery and the token read run the host's
// CLI by, GoRex's first, then upstream's. Each name runs in an exec of
// its own: a login shell need not read `a || b` (Windows PowerShell 5.1
// and nushell do not), and a Windows sshd has no sh to wrap it in.
var rexCLIs = []string{"gorex", "rex"}

// rexStatus asks the host's Rex server where it listens; lanStatus is
// the rexapi LAN gateway's state, which names whether it has a pairing
// token; lanTokens prints that token (docs/IOS-024). Each runs after a
// name of rexCLIs. rexProbe finds the socket among the usual paths
// when status names none, the same list the design's fallback walks;
// it exits non-zero when none exists.
const (
	rexStatus = `server status --json`
	lanStatus = `rexapi lan status`
	lanTokens = `rexapi lan token show`
	rexProbe  = `sh -c 'for p in "$XDG_RUNTIME_DIR/rex/server.sock" "$HOME/.local/share/rex/server.sock" /run/rex/server.sock /var/run/rex/server.sock "$HOME/Library/Application Support/rex/server.sock"; do [ -S "$p" ] && { echo "$p"; break; }; done'`
)

// rexExec runs the CLI command args under each name of rexCLIs in turn,
// and returns the output of the first that exits 0 and the name that
// ran it; the last error when none does.
func rexExec(ctx context.Context, c *ssh.Client, args string) (out, cli string, err error) {
	for _, name := range rexCLIs {
		if out, err = sshExec(ctx, c, name+" "+args); err == nil {
			return out, name, nil
		}
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
	}
	return "", "", err
}

// lanProbe is the endpoint discovery falls to for a host whose server
// listens on a named pipe only, when no usual socket path exists and the
// LAN CLI names no gateway — GoRex's rexapi LAN port on loopback
// (internal/rexapi's LANPort), where a named-pipe host's gateway is. A
// var, for the test's listener.
var lanProbe = "127.0.0.1:7297"

// discoverEndpoint is the host's Rex endpoint, as upstream's discovery
// finds it (docs/IOS-023, docs/IOS-024): a unix listener of server
// status, the primary first; else the first of the usual socket paths
// that exists, so a Unix host keeps its socket; else a tcp listener of
// status; else the gateway the LAN CLI names; else, for a server that
// listens on a named pipe, the LAN probe. A host where none of these
// answers has no Rex endpoint the phone can reach, and the error says
// so.
func discoverEndpoint(ctx context.Context, c *ssh.Client, host string) (sshEndpoint, error) {
	var st sshStatusResult
	if out, _, err := rexExec(ctx, c, rexStatus); err == nil {
		if json.Unmarshal([]byte(out), &st) != nil {
			st = sshStatusResult{}
		}
	}
	if e := st.unixEndpoint(); e != (sshEndpoint{}) {
		return e, nil
	}
	if out, err := sshExec(ctx, c, rexProbe); err == nil {
		line, _, _ := strings.Cut(out, "\n")
		if line = strings.TrimSpace(line); line != "" {
			return sshEndpoint{socket: line}, nil
		}
	}
	if e := st.tcpEndpoint(); e != (sshEndpoint{}) {
		return e, nil
	}
	if addr := lanListen(ctx, c); addr != "" {
		return sshEndpoint{tcp: addr}, nil
	}
	if st.namedPipe() {
		return sshEndpoint{tcp: lanProbe}, nil
	}
	return sshEndpoint{}, errors.New("no rex socket on " + host + ": is a Rex server running there, with gorex or rex on the login shell's PATH?")
}

// lanListen is the rexapi LAN gateway's address as the host's CLI
// reports it: "lan: listening on http://addr", else the configured
// "listen: addr" of a gateway that is on but has not said where it
// listens; an off gateway, or one in error, gives "". It answers where status has no tcp listener — a gateway
// bound to the LAN address, which the loopback probe cannot reach —
// and "" where no gateway is on.
func lanListen(ctx context.Context, c *ssh.Client) string {
	out, _, err := rexExec(ctx, c, lanStatus)
	if err != nil {
		return ""
	}
	var configured string
	waiting := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if a, ok := strings.CutPrefix(line, "lan: listening on "); ok {
			if t := tcpTarget(strings.TrimSpace(a)); t != "" {
				return t
			}
		}
		// "on, waiting for the GoRex server": on, with no word yet of
		// where it listens, as from a server that writes no status.
		// "off" and "error" are no gateway.
		if strings.HasPrefix(line, "lan: on") {
			waiting = true
		}
		if a, ok := strings.CutPrefix(line, "listen: "); ok {
			configured = a
		}
	}
	if !waiting {
		return ""
	}
	return tcpTarget(strings.TrimSpace(configured))
}

// sshStatusResult is the listeners of server status --json
// (internal/rexapi's statusResult), as far as the endpoint needs them.
type sshStatusResult struct {
	Listeners []struct {
		Endpoint  string   `json:"endpoint"`
		Addresses []string `json:"addresses"`
		Transport string   `json:"transport"`
		Primary   bool     `json:"primary"`
	} `json:"listeners"`
}

// unixEndpoint is the socket of the status's primary unix listener,
// else of its first other unix one; none when it lists none.
func (st sshStatusResult) unixEndpoint() sshEndpoint {
	var other string
	for _, l := range st.Listeners {
		if !strings.HasPrefix(l.Transport, "unix") {
			continue
		}
		p := endpointPath(l.Endpoint)
		if p == "" {
			continue
		}
		if l.Primary {
			return sshEndpoint{socket: p}
		}
		if other == "" {
			other = p
		}
	}
	return sshEndpoint{socket: other}
}

// tcpEndpoint is the host:port of a tcp listener of the status — a
// loopback one among several, which the forwarded channel reaches as
// the host's own — read from the address it is bound to, which shows
// the port a listen on port 0 got, else from its endpoint (docs/IOS-024).
func (st sshStatusResult) tcpEndpoint() sshEndpoint {
	var first string
	for _, l := range st.Listeners {
		if l.Transport != "tcp" {
			continue
		}
		var t string
		for _, a := range l.Addresses {
			if t = tcpTarget(a); t != "" {
				break
			}
		}
		if t == "" {
			t = tcpTarget(l.Endpoint)
		}
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

// namedPipe tells whether the status lists a named-pipe listener: a
// server that runs, on a pipe no SSH channel reaches.
func (st sshStatusResult) namedPipe() bool {
	for _, l := range st.Listeners {
		if l.Transport == "named_pipe" {
			return true
		}
	}
	return false
}

// tcpTarget is the direct-tcpip target a tcp listener's endpoint gives:
// host:port, as written or after a scheme, a wildcard host read as
// loopback; "" when the endpoint is not one, or names port 0, which
// only the bound address resolves.
func tcpTarget(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		endpoint = u.Host
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || port == "" {
		return ""
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
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
// discovery's execs do. A command that exits non-zero, dies of a signal
// or ends with no exit status is an error, as ssh.Session's Wait reads
// it: an output then is not the command's answer.
func sshExec(ctx context.Context, c *ssh.Client, command string) (string, error) {
	sess, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Run(command); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	return out.String(), nil
}

// sshTokenRetry is how long a host whose token exec failed waits before
// a dial runs it again: a host with no CLI then costs an exec now and
// then, not on every dial. A var, for the tests.
var sshTokenRetry = 30 * time.Second

// lanToken learns the host's rexapi LAN pairing token over the client,
// once per record, kept under id for SSHHostToken. Only an exec that
// succeeds caches, "" included for a host whose gateway has no token; a
// failed one leaves the cache empty, and a dial after sshTokenRetry
// tries again.
func lanToken(ctx context.Context, id string, c *ssh.Client) {
	sshMu.Lock()
	_, done := sshTokens[id]
	failed, waits := sshTokenFailed[id]
	sshMu.Unlock()
	if done || waits && time.Since(failed) < sshTokenRetry {
		return
	}
	token, err := readLANToken(ctx, c)
	sshMu.Lock()
	defer sshMu.Unlock()
	if err != nil {
		sshTokenFailed[id] = time.Now()
		return
	}
	delete(sshTokenFailed, id)
	sshTokens[id] = token
}

// readLANToken reads the gateway's pairing token and creates none: the
// CLI's lan status tells whether a token is set, and only then does
// token show print it, as token show alone would make and save one on a
// host that has none. Every GoRex with token show prints that line
// (#684), so an older host needs no new command. "" with no error is a
// host that answered that it has no token.
func readLANToken(ctx context.Context, c *ssh.Client) (string, error) {
	out, cli, err := rexExec(ctx, c, lanStatus)
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, "pairing token: set") {
		return "", nil
	}
	if out, err = sshExec(ctx, c, cli+" "+lanTokens); err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", errors.New("token show printed no token")
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
