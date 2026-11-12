//go:build darwin || linux || windows

package rex

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
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

	"gorex/internal/pairaddr"

	"tailscale.com/types/key"
)

// Pairing by address (docs/host-links.md, "1. Identity"): its wire and
// client half are internal/pairaddr's, which Rex Go's engine shares; the
// server half is here.
const (
	pairPath    = pairaddr.Path
	pairUpgrade = pairaddr.Upgrade
)

var (
	pairCommit = pairaddr.Commit
	pairCode   = pairaddr.Code
)

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
	c.SetDeadline(time.Now().Add(pairaddr.MsgWait))
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
	c.SetDeadline(time.Now().Add(pairaddr.MsgWait))
	var head [66]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		return
	}
	n := binary.BigEndian.Uint16(head[64:])
	if n > pairaddr.NameMax {
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
	x, err := req.TLS.ExportKeyingMaterial(pairaddr.ExporterLabel, nil, 32)
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
		pairaddr.WriteAnswer(c, pairaddr.Answer{Error: refused})
		return
	}
	log.Print("rex: a pairing request by address")
	c.SetDeadline(time.Time{})
	c.SetReadDeadline(time.Now().Add(pairaddr.AddrWait))
	gone := make(chan struct{})
	go func() {
		br.ReadByte() // the client sends nothing more: this ends with the connection, or the deadline
		close(gone)
	}()
	select {
	case addrs := <-p.result:
		if granted = addrs != nil; granted {
			c.SetWriteDeadline(time.Now().Add(pairaddr.MsgWait))
			delivered = pairaddr.WriteAnswer(c, pairaddr.Answer{Addresses: addrs}) == nil
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

// AddressPairing is a pairing by address that waits for this machine's
// user: Code is to compare with the one the machine shows. Nothing is
// kept before Confirm.
type AddressPairing = pairaddr.Pairing

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
	return pairaddr.Ask(ctx, d, addr, RemotePort, name, kc)
}
