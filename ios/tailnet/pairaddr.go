package tailnet

// Pairing by address (docs/host-links.md, "Pairing by address"): Rex Go
// asks a GoRex machine at an IP address, through its relay of Reachable
// at, to allow this phone's tailcat key; the user compares the code both
// show, and the machine's user approves. The address it then gets is a
// tailcat address, as a Rex Go code's. One pairing at a time.

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"

	"gorex/internal/pairaddr"
)

var (
	apMu sync.Mutex
	ap   *pairaddr.Pairing
)

// PairByAddress asks the machine at address, "IP" or "IP:PORT", to allow
// this phone's key as name, and returns the six-digit code to compare
// with the one the machine shows. A pairing under way ends.
func PairByAddress(address, name string) (string, error) {
	tcMu.Lock()
	k := tcKey
	tcMu.Unlock()
	if k.IsZero() {
		return "", errors.New("no tailcat key")
	}
	p, err := pairaddr.Ask(context.Background(), &net.Dialer{}, strings.TrimSpace(address), remotePort, name, k.Public())
	if err != nil {
		return "", plain(err)
	}
	apMu.Lock()
	old := ap
	ap = p
	apMu.Unlock()
	if old != nil {
		old.Close()
	}
	return p.Code, nil
}

// ConfirmAddressPairing is the user's word that the codes match: it waits
// for the machine's approval, two minutes at most, and returns the
// tailcat address to keep, the one through the machine's own relay.
func ConfirmAddressPairing() (string, error) {
	apMu.Lock()
	p := ap
	apMu.Unlock()
	if p == nil {
		return "", errors.New("no pairing under way")
	}
	addrs, err := p.Confirm(context.Background())
	apMu.Lock()
	if ap == p {
		ap = nil
	}
	apMu.Unlock()
	if err != nil {
		return "", plain(err)
	}
	return addrs[0], nil
}

// MismatchAddressPairing is the user's word that the codes differ: it
// ends the pairing and returns the warning to show.
func MismatchAddressPairing() string {
	CancelAddressPairing()
	return plain(pairaddr.ErrUnknownDevice).Error()
}

// CancelAddressPairing gives the pairing under way up: a confirmation
// waiting fails.
func CancelAddressPairing() {
	apMu.Lock()
	p := ap
	ap = nil
	apMu.Unlock()
	if p != nil {
		p.Close()
	}
}

// plain is err without the session server's "rex: ", as the app shows it.
func plain(err error) error { return errors.New(strings.TrimPrefix(err.Error(), "rex: ")) }
