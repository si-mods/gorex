//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// Pairing by address (docs/host-links.md, "1. Identity"): on the relay of
// Reachable at, while the pairing window is open, a client that has no
// address yet asks to pair over TLS, and both sides show a code of the
// TLS connection and both keys. One exchange per connection, after an
// HTTP/1.1 upgrade to pairUpgrade:
//
//	client -> server  commit                      32 bytes, pairCommit
//	server -> client  K_S, N_S                    64 bytes
//	client -> server  K_C, N_C, u16be len, name   66 bytes and the name
//	server -> client  u16be len, JSON pairAnswer  once approved, or refused
//
// The server closes the connection on a rejection, a timeout, the end of
// the window, or an opening that does not match its commitment. An
// exchange that got the server's nonce and ends without the approval
// closes the pairing window: one code per window.

// The domain separation strings of pairing by address, never to change:
// a new protocol takes new ones.
const (
	pairPath          = "/gorex/pair"
	pairUpgrade       = "gorex-pair-v1"
	pairCommitLabel   = "gorex-pair-v1/commit\x00"
	pairCodeLabel     = "gorex-pair-v1/code\x00"
	pairExporterLabel = "EXPORTER-gorex-pair-v1"
)

const (
	pairMsgWait  = 10 * time.Second // for each message before approval
	pairAddrWait = 2 * time.Minute  // for the user's approval
	pairNameMax  = 256              // bytes of a name on the wire
)

// pairCommit is the client's commitment to its key, its nonce and its name.
func pairCommit(kc, nc [32]byte, name []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(pairCommitLabel))
	h.Write(kc[:])
	h.Write(nc[:])
	h.Write(binary.BigEndian.AppendUint16(nil, uint16(len(name))))
	h.Write(name)
	return [32]byte(h.Sum(nil))
}

// pairCode is the six digits both sides show: of the TLS exporter x, the
// certificate the connection showed, both keys and both nonces.
func pairCode(x, leaf []byte, kc, ks, nc, ns [32]byte) string {
	cert := sha256.Sum256(leaf)
	h := sha256.New()
	h.Write([]byte(pairCodeLabel))
	h.Write(x)
	h.Write(cert[:])
	h.Write(kc[:])
	h.Write(ks[:])
	h.Write(nc[:])
	h.Write(ns[:])
	return fmt.Sprintf("%06d", binary.BigEndian.Uint64(h.Sum(nil)[:8])%1_000_000)
}

// pairAnswer is the server's last message: its addresses, the one of
// Reachable at first, or why it refused.
type pairAnswer struct {
	Addresses []string `json:"addresses,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func writePairAnswer(w io.Writer, a pairAnswer) error {
	b, _ := json.Marshal(a)
	_, err := w.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(b))), b...))
	return err
}

func readPairAnswer(r io.Reader) (pairAnswer, error) {
	var n [2]byte
	var a pairAnswer
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return a, err
	}
	b := make([]byte, binary.BigEndian.Uint16(n[:]))
	if _, err := io.ReadFull(r, b); err != nil {
		return a, err
	}
	return a, json.Unmarshal(b, &a)
}

// addrPending is a pairing by address on its live connection: Code is ""
// until the client opened its commitment. result takes the addresses on
// approval, nil on rejection.
type addrPending struct {
	RemoteAddrPending
	key    string
	slot   netip.Prefix // the source, an IPv6 one by its /64
	result chan []string
}

// waitingLocked returns the requests by address that wait for the user.
func (r *remote) waitingLocked() []*addrPending {
	if !r.pairOpenLocked() {
		return nil
	}
	var w []*addrPending
	for _, p := range r.byAddr {
		if p.Code != "" {
			w = append(w, p)
		}
	}
	return w
}

// pairByAddress serves one pairing by address on the relay whose
// certificate is leaf, for the server key self. While the window is
// closed it is an unknown path.
func (s *Server) pairByAddress(w http.ResponseWriter, req *http.Request, leaf []byte, self key.NodePublic) {
	r := &s.rem
	ap, _ := netip.ParseAddrPort(req.RemoteAddr)
	src := ap.Addr().Unmap()
	slot := netip.PrefixFrom(src, src.BitLen())
	if src.Is6() {
		slot, _ = src.Prefix(64)
	}
	r.mu.Lock()
	if !r.pairOpenLocked() || req.TLS == nil || req.TLS.Version < tls.VersionTLS13 || !strings.EqualFold(req.Header.Get("Upgrade"), pairUpgrade) {
		r.mu.Unlock()
		http.NotFound(w, req)
		return
	}
	if len(r.byAddr) >= maxPending || slices.ContainsFunc(r.byAddr, func(p *addrPending) bool { return p.slot == slot }) {
		r.mu.Unlock()
		http.Error(w, "rex: too many pairing requests; try again later", http.StatusServiceUnavailable)
		return
	}
	p := &addrPending{RemoteAddrPending: RemoteAddrPending{Source: src.String()}, slot: slot, result: make(chan []string, 1)}
	r.byAddr = append(r.byAddr, p)
	r.mu.Unlock()
	// shown: the client has the server's nonce, and so the code. granted:
	// the user approved. delivered: the client has the addresses.
	var shown, granted, delivered bool
	defer func() {
		r.mu.Lock()
		r.byAddr = slices.DeleteFunc(r.byAddr, func(q *addrPending) bool { return q == p })
		if !granted {
			select {
			case addrs := <-p.result: // approved as the connection ended
				granted = addrs != nil
			default:
			}
		}
		if granted && !delivered {
			// The client never got an address: without this its key would
			// be allowed, of no use, and refused the next time it asks.
			cfg := r.cfg
			cfg.Allowed = slices.DeleteFunc(slices.Clone(cfg.Allowed), func(c RemoteClient) bool { return c.PublicKey == p.key })
			if writeJSON(remotePath(), cfg) == nil {
				r.cfg = cfg
			}
		}
		// One code per window: a client that saw its code and left without
		// the user's approval could ask again until a code matched the one
		// on another screen.
		closed := shown && !delivered && r.pairOpenLocked()
		if closed {
			r.pairUntil, r.pending = time.Time{}, nil
			log.Print("rex: a pairing by address ended without approval: the pairing window is closed")
		}
		r.mu.Unlock()
		if closed {
			s.ownDropStrangers()
		}
	}()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.NotFound(w, req)
		return
	}
	c, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	// The connection is this exchange's: the relay's idle timeouts no more.
	c.SetDeadline(time.Now().Add(pairMsgWait))
	if _, err := io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: "+pairUpgrade+"\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		return
	}
	br := brw.Reader
	var commit [32]byte
	if _, err := io.ReadFull(br, commit[:]); err != nil {
		return
	}
	var ns [32]byte
	rand.Read(ns[:])
	ks := self.Raw32()
	shown = true
	if _, err := c.Write(append(ks[:], ns[:]...)); err != nil {
		return
	}
	c.SetDeadline(time.Now().Add(pairMsgWait))
	var head [66]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		return
	}
	n := binary.BigEndian.Uint16(head[64:])
	if n > pairNameMax {
		return
	}
	name := make([]byte, n)
	if _, err := io.ReadFull(br, name); err != nil {
		return
	}
	kc, nc := [32]byte(head[:32]), [32]byte(head[32:64])
	if want := pairCommit(kc, nc, name); subtle.ConstantTimeCompare(want[:], commit[:]) != 1 {
		return
	}
	k, err := parsePublicKey("nodekey:" + hex.EncodeToString(kc[:]))
	if err != nil {
		return
	}
	x, err := req.TLS.ExportKeyingMaterial(pairExporterLabel, nil, 32)
	if err != nil {
		return
	}
	refused := ""
	r.mu.Lock()
	switch {
	case !r.pairOpenLocked():
		refused = "rex: the server is not pairing"
	case pairName(string(name)) == "":
		refused = "rex: name this machine to pair it"
	case r.allowedLocked(k.String()):
		refused = "rex: this machine is allowed there already"
	case slices.ContainsFunc(r.pending, func(q RemotePending) bool { return q.PublicKey == k.String() }),
		slices.ContainsFunc(r.byAddr, func(q *addrPending) bool { return q.key == k.String() }):
		refused = "rex: this machine's request waits there already"
	default:
		p.key, p.Name, p.RequestedAt = k.String(), pairName(string(name)), time.Now()
		p.Code = pairCode(x, leaf, kc, ks, nc, ns)
	}
	r.mu.Unlock()
	if refused != "" {
		writePairAnswer(c, pairAnswer{Error: refused})
		return
	}
	log.Print("rex: a pairing request by address")
	c.SetDeadline(time.Time{})
	c.SetReadDeadline(time.Now().Add(pairAddrWait))
	gone := make(chan struct{})
	go func() {
		br.ReadByte() // the client sends nothing more: this ends with the connection, or the deadline
		close(gone)
	}()
	select {
	case addrs := <-p.result:
		if granted = addrs != nil; granted {
			c.SetWriteDeadline(time.Now().Add(pairMsgWait))
			delivered = writePairAnswer(c, pairAnswer{Addresses: addrs}) == nil
		}
	case <-gone:
	}
}

// addrDecideLocked approves or rejects the request by address of code.
// Approving takes exactly one waiting: each other could be a guess.
func (s *Server) addrDecideLocked(approve bool, code string) error {
	r := &s.rem
	waiting := r.waitingLocked()
	if approve && len(waiting) > 1 {
		return fmt.Errorf("rex: %d requests by address wait: reject them all, and ask again from the device", len(waiting))
	}
	i := slices.IndexFunc(waiting, func(p *addrPending) bool { return p.Code == code })
	if i < 0 {
		return errors.New("rex: no such request")
	}
	p := waiting[i]
	var addrs []string
	if approve {
		if r.own == nil {
			return errors.New("rex: Reachable at is not running")
		}
		if !r.allowedLocked(p.key) {
			cfg := r.cfg
			cfg.Allowed = append(slices.Clone(cfg.Allowed), RemoteClient{Name: p.Name, PublicKey: p.key, Created: time.Now()})
			if err := writeJSON(remotePath(), cfg); err != nil {
				return err
			}
			r.cfg = cfg
		}
		addrs = []string{string(r.own.addr)}
		if r.addr != "" {
			addrs = append(addrs, string(r.addr))
		}
	}
	p.result <- addrs
	r.byAddr = slices.DeleteFunc(r.byAddr, func(q *addrPending) bool { return q == p })
	return nil
}

// errUnknownDevice is what a client says when the codes or the answer do
// not match: somebody in the middle may be on the allowlist now.
var errUnknownDevice = errors.New("rex: the pairing did not match: the machine may have let in an unknown device; remove its newest device there")

// AddressPairing is a pairing by address that waits for this machine's
// user: Code is to compare with the one the machine shows. Nothing is
// kept before Confirm.
type AddressPairing struct {
	Code string
	conn *tls.Conn
	br   *bufio.Reader
	ks   key.NodePublic
	pin  string // "sha256-raw:" and the hex SHA-256 of the certificate seen
}

// PairByAddress asks the machine at addr, an IP address with the port
// RemotePort or IP:PORT, to allow this machine as name, through its relay
// of Reachable at while its pairing window is open. It returns at once
// with the code; the user then compares it and Confirms.
func PairByAddress(ctx context.Context, addr, name string) (*AddressPairing, error) {
	k, err := remoteClientKey()
	if err != nil {
		return nil, err
	}
	return pairByAddressAs(ctx, &net.Dialer{}, addr, name, k.Public())
}

// pairByAddressAs is PairByAddress for the key kc, dialing with d.
func pairByAddressAs(ctx context.Context, d *net.Dialer, addr, name string, kc key.NodePublic) (*AddressPairing, error) {
	ap, err := netip.ParseAddrPort(addr)
	if ip, ierr := netip.ParseAddr(addr); ierr == nil {
		ap, err = netip.AddrPortFrom(ip, RemotePort), nil
	}
	if err != nil {
		return nil, errors.New("rex: pair by an IP address, as 192.0.2.10")
	}
	if name = pairName(name); name == "" || len(name) > pairNameMax {
		return nil, errors.New("rex: name this machine to pair it")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*pairMsgWait)
	defer cancel()
	// A plain dial, no proxy; no chain to verify on a first contact: the
	// code is what tells it is the machine.
	td := &tls.Dialer{NetDialer: d, Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}
	nc0, err := td.DialContext(ctx, "tcp", ap.String())
	if err != nil {
		return nil, fmt.Errorf("rex: connecting to %s: %w", ap, err)
	}
	c := nc0.(*tls.Conn)
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	br := bufio.NewReader(c)
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", pairPath, ap.Addr(), pairUpgrade)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, fmt.Errorf("rex: asking %s to pair: %w", ap, err)
	}
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusSwitchingProtocols:
	case http.StatusNotFound:
		return nil, errors.New("rex: the machine is not pairing now: start pairing there first, with Reachable at on")
	default:
		return nil, fmt.Errorf("rex: the machine refused to pair: %s", res.Status)
	}
	kcRaw := kc.Raw32()
	var ncRaw [32]byte
	rand.Read(ncRaw[:])
	commit := pairCommit(kcRaw, ncRaw, []byte(name))
	if _, err := c.Write(commit[:]); err != nil {
		return nil, err
	}
	var kn [64]byte
	if _, err := io.ReadFull(br, kn[:]); err != nil {
		return nil, fmt.Errorf("rex: the machine closed the pairing: %w", err)
	}
	ksRaw, ns := [32]byte(kn[:32]), [32]byte(kn[32:])
	opening := append(append(kcRaw[:], ncRaw[:]...), binary.BigEndian.AppendUint16(nil, uint16(len(name)))...)
	if _, err := c.Write(append(opening, name...)); err != nil {
		return nil, err
	}
	st := c.ConnectionState()
	x, err := st.ExportKeyingMaterial(pairExporterLabel, nil, 32)
	if err != nil || len(st.PeerCertificates) == 0 {
		return nil, errors.New("rex: the pairing's TLS connection has no exporter or certificate")
	}
	ks, err := parsePublicKey("nodekey:" + hex.EncodeToString(ksRaw[:]))
	if err != nil {
		return nil, errors.New("rex: the machine sent no key")
	}
	leaf := st.PeerCertificates[0].Raw
	sum := sha256.Sum256(leaf)
	ok = true
	return &AddressPairing{
		Code: pairCode(x, leaf, kcRaw, ksRaw, ncRaw, ns),
		conn: c, br: br, ks: ks,
		pin: "sha256-raw:" + hex.EncodeToString(sum[:]),
	}, nil
}

// Confirm is the user's word that the codes match: it waits for the
// machine's approval and returns its addresses, Reachable at's first,
// once each is the machine's key with the certificate this connection
// showed. Before or after the approval arrives, as the user answers.
func (p *AddressPairing) Confirm(ctx context.Context) ([]string, error) {
	defer p.conn.Close()
	stop := context.AfterFunc(ctx, func() { p.conn.Close() })
	defer stop()
	p.conn.SetReadDeadline(time.Now().Add(pairAddrWait + pairMsgWait))
	a, err := readPairAnswer(p.br)
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case err != nil:
		return nil, errors.New("rex: the machine rejected the request, or nobody approved it in time")
	case a.Error != "":
		return nil, errors.New(a.Error)
	}
	if err := checkPairAddrs(a.Addresses, p.ks, p.pin); err != nil {
		return nil, err
	}
	return a.Addresses, nil
}

// Mismatch is the user's word that the codes differ: it ends the request
// and returns the warning to show.
func (p *AddressPairing) Mismatch() error {
	p.conn.Close()
	return errUnknownDevice
}

// Close gives the request up.
func (p *AddressPairing) Close() error { return p.conn.Close() }

// checkPairAddrs tells whether addrs are the machine's: each a tailcat
// address of the key ks, the first through the relay of the pin seen.
func checkPairAddrs(addrs []string, ks key.NodePublic, pin string) error {
	if len(addrs) == 0 {
		return errUnknownDevice
	}
	for i, a := range addrs {
		ci, err := tailcat.ParseAddr(tailcat.Addr(a))
		if err != nil || ci.ServerPublic.NodePublic != ks {
			return errUnknownDevice
		}
		if i == 0 && (len(ci.Region) != 1 || len(ci.Region[0].Nodes) != 1 || ci.Region[0].Nodes[0].CertName != pin) {
			return errUnknownDevice
		}
	}
	return nil
}
