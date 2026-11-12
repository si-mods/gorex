// Package pairaddr is the wire of pairing by address (docs/host-links.md,
// "1. Identity") and its client half: internal/rex serves it, and the
// desktop and Rex Go's engine (ios/tailnet) ask with it, the engine
// without the server's packages.
package pairaddr

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// On the relay of Reachable at, while the pairing window is open, a
// client that has no address yet asks to pair over TLS, and both sides
// show a code of the TLS connection and both keys. One exchange per
// connection, after an HTTP/1.1 upgrade to Upgrade:
//
//	client -> server  commit                      32 bytes, Commit
//	server -> client  K_S, N_S                    64 bytes
//	client -> server  K_C, N_C, u16be len, name   66 bytes and the name
//	server -> client  u16be len, JSON Answer      once approved, or refused
//
// The server closes the connection on a rejection, a timeout, the end of
// the window, or an opening that does not match its commitment. An
// exchange that got the server's nonce and ends without the approval
// closes the pairing window: one code per window.

// The domain separation strings of pairing by address, never to change:
// a new protocol takes new ones.
const (
	Path          = "/gorex/pair"
	Upgrade       = "gorex-pair-v1"
	commitLabel   = "gorex-pair-v1/commit\x00"
	codeLabel     = "gorex-pair-v1/code\x00"
	ExporterLabel = "EXPORTER-gorex-pair-v1"
)

const (
	MsgWait  = 10 * time.Second // for each message before approval
	AddrWait = 2 * time.Minute  // for the user's approval
	NameMax  = 256              // bytes of a name on the wire
	maxName  = 40               // characters of a name, as cleaned
)

// Name cleans the name a client asks to pair with: printable characters
// only, at most maxName of them.
func Name(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxName {
		s = strings.TrimSpace(string(r[:maxName]))
	}
	return s
}

// Commit is the client's commitment to its key, its nonce and its name.
func Commit(kc, nc [32]byte, name []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(commitLabel))
	h.Write(kc[:])
	h.Write(nc[:])
	h.Write(binary.BigEndian.AppendUint16(nil, uint16(len(name))))
	h.Write(name)
	return [32]byte(h.Sum(nil))
}

// Code is the six digits both sides show: of the TLS exporter x, the
// certificate the connection showed, both keys and both nonces.
func Code(x, leaf []byte, kc, ks, nc, ns [32]byte) string {
	cert := sha256.Sum256(leaf)
	h := sha256.New()
	h.Write([]byte(codeLabel))
	h.Write(x)
	h.Write(cert[:])
	h.Write(kc[:])
	h.Write(ks[:])
	h.Write(nc[:])
	h.Write(ns[:])
	return fmt.Sprintf("%06d", binary.BigEndian.Uint64(h.Sum(nil)[:8])%1_000_000)
}

// Answer is the server's last message: its addresses, the one of
// Reachable at first, or why it refused.
type Answer struct {
	Addresses []string `json:"addresses,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// WriteAnswer writes a, after its length.
func WriteAnswer(w io.Writer, a Answer) error {
	b, _ := json.Marshal(a)
	_, err := w.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(b))), b...))
	return err
}

func readAnswer(r io.Reader) (Answer, error) {
	var n [2]byte
	var a Answer
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return a, err
	}
	b := make([]byte, binary.BigEndian.Uint16(n[:]))
	if _, err := io.ReadFull(r, b); err != nil {
		return a, err
	}
	return a, json.Unmarshal(b, &a)
}

// ErrUnknownDevice is what a client says when the codes or the answer do
// not match: somebody in the middle may be on the allowlist now.
var ErrUnknownDevice = errors.New("rex: the pairing did not match: the machine may have let in an unknown device; remove its newest device there")

// Pairing is a pairing by address that waits for the machine's user:
// Code is to compare with the one the machine shows. Nothing is kept
// before Confirm.
type Pairing struct {
	Code string
	conn *tls.Conn
	br   *bufio.Reader
	ks   key.NodePublic
	pin  string // "sha256-raw:" and the hex SHA-256 of the certificate seen
}

// Ask asks the machine at addr, an IP address with the port port or
// IP:PORT, to allow the key kc as name, through its relay of Reachable
// at while its pairing window is open, dialing with d. It returns at once
// with the code; the user then compares it and Confirms.
func Ask(ctx context.Context, d *net.Dialer, addr string, port uint16, name string, kc key.NodePublic) (*Pairing, error) {
	ap, err := netip.ParseAddrPort(addr)
	if ip, ierr := netip.ParseAddr(addr); ierr == nil {
		ap, err = netip.AddrPortFrom(ip, port), nil
	}
	if err != nil {
		return nil, errors.New("rex: pair by an IP address, as 192.0.2.10")
	}
	if name = Name(name); name == "" || len(name) > NameMax {
		return nil, errors.New("rex: name this machine to pair it")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*MsgWait)
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
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", Path, ap.Addr(), Upgrade)
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
	commit := Commit(kcRaw, ncRaw, []byte(name))
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
	x, err := st.ExportKeyingMaterial(ExporterLabel, nil, 32)
	if err != nil || len(st.PeerCertificates) == 0 {
		return nil, errors.New("rex: the pairing's TLS connection has no exporter or certificate")
	}
	var ks key.NodePublic
	if err := ks.UnmarshalText([]byte("nodekey:" + hex.EncodeToString(ksRaw[:]))); err != nil || ks.IsZero() {
		return nil, errors.New("rex: the machine sent no key")
	}
	leaf := st.PeerCertificates[0].Raw
	sum := sha256.Sum256(leaf)
	ok = true
	return &Pairing{
		Code: Code(x, leaf, kcRaw, ksRaw, ncRaw, ns),
		conn: c, br: br, ks: ks,
		pin: "sha256-raw:" + hex.EncodeToString(sum[:]),
	}, nil
}

// Confirm is the user's word that the codes match: it waits for the
// machine's approval and returns its addresses, Reachable at's first,
// once each is the machine's key with the certificate this connection
// showed. Before or after the approval arrives, as the user answers.
func (p *Pairing) Confirm(ctx context.Context) ([]string, error) {
	defer p.conn.Close()
	stop := context.AfterFunc(ctx, func() { p.conn.Close() })
	defer stop()
	p.conn.SetReadDeadline(time.Now().Add(AddrWait + MsgWait))
	a, err := readAnswer(p.br)
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case err != nil:
		return nil, errors.New("rex: the machine rejected the request, or nobody approved it in time")
	case a.Error != "":
		return nil, errors.New(a.Error)
	}
	if err := CheckAddrs(a.Addresses, p.ks, p.pin); err != nil {
		return nil, err
	}
	return a.Addresses, nil
}

// Mismatch is the user's word that the codes differ: it ends the request
// and returns the warning to show.
func (p *Pairing) Mismatch() error {
	p.conn.Close()
	return ErrUnknownDevice
}

// Close gives the request up.
func (p *Pairing) Close() error { return p.conn.Close() }

// CheckAddrs tells whether addrs are the machine's: each a tailcat
// address of the key ks, the first through the relay of the pin seen.
func CheckAddrs(addrs []string, ks key.NodePublic, pin string) error {
	if len(addrs) == 0 {
		return ErrUnknownDevice
	}
	for i, a := range addrs {
		ci, err := tailcat.ParseAddr(tailcat.Addr(a))
		if err != nil || ci.ServerPublic.NodePublic != ks {
			return ErrUnknownDevice
		}
		if i == 0 && (len(ci.Region) != 1 || len(ci.Region[0].Nodes) != 1 || ci.Region[0].Nodes[0].CertName != pin) {
			return ErrUnknownDevice
		}
	}
	return nil
}
