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
	// apAsks counts the requests begun: one that ends after a newer one
	// began is given up, so that the pairing kept is the newest.
	apAsks int
)

// PairByAddress asks the machine at address, "IP", "[IPv6]" or
// "IP:PORT", to allow this phone's key as name, and returns the six-digit
// code to compare with the one the machine shows. A pairing under way
// ends, and so does a request begun before this one.
func PairByAddress(address, name string) (string, error) {
	tcMu.Lock()
	k := tcKey
	tcMu.Unlock()
	if k.IsZero() {
		return "", errors.New("no tailcat key")
	}
	apMu.Lock()
	apAsks++
	n := apAsks
	apMu.Unlock()
	p, err := pairaddr.Ask(context.Background(), &net.Dialer{}, strings.TrimSpace(address), remotePort, name, k.Public())
	if err != nil {
		return "", plain(err)
	}
	apMu.Lock()
	if n != apAsks {
		apMu.Unlock()
		p.Close()
		return "", errors.New("a newer pairing request replaced this one")
	}
	old := ap
	ap = p
	apMu.Unlock()
	if old != nil {
		old.Close()
	}
	return p.Code, nil
}

// take returns the pairing under way when its code is code, and takes it
// out when out.
func take(code string, out bool) *pairaddr.Pairing {
	apMu.Lock()
	defer apMu.Unlock()
	p := ap
	if p == nil || p.Code != code {
		return nil
	}
	if out {
		ap = nil
	}
	return p
}

// ConfirmAddressPairing is the user's word that the codes match, for the
// pairing of code: it waits for the machine's approval, two minutes at
// most, and returns the tailcat address to keep, the one through the
// machine's own relay. An answer not the machine's fails with
// AddressPairingWarning.
func ConfirmAddressPairing(code string) (string, error) {
	p := take(code, false)
	if p == nil {
		return "", errors.New("no pairing under way with this code")
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

// MismatchAddressPairing is the user's word that the codes differ, for
// the pairing of code: it ends it and returns AddressPairingWarning.
func MismatchAddressPairing(code string) string {
	CancelAddressPairing(code)
	return AddressPairingWarning()
}

// CancelAddressPairing gives the pairing of code up: a confirmation
// waiting fails. Another pairing stays.
func CancelAddressPairing(code string) {
	if p := take(code, true); p != nil {
		p.Close()
	}
}

// AddressPairingWarning is the message of a pairing that did not match,
// the codes or the machine's answer: the app tells it from other errors
// by it.
func AddressPairingWarning() string { return plain(pairaddr.ErrUnknownDevice).Error() }

// plain is err without the session server's "rex: ", as the app shows it.
func plain(err error) error { return errors.New(strings.TrimPrefix(err.Error(), "rex: ")) }
