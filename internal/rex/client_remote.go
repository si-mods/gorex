package rex

import (
	"errors"
	"strings"
)

// RemoteStatus tells the server's remote access; the address it gives
// is a secret.
func (c *Client) RemoteStatus() (s RemoteStatus, err error) {
	err = c.call(Request{Op: "remote.status"}, &s)
	return
}

// TailcatStatus asks the server how its tailcat link to addr runs: a
// direct path or a relay, and the ping's round-trip (TailcatStatus). A
// server of before the op answers "unknown op". The address is a secret.
func (c *Client) TailcatStatus(addr string) (s TailcatStatus, err error) {
	err = c.call(Request{Op: "remote.tailcatStatus", TailcatAddress: addr}, &s)
	return
}

// EnableRemote turns remote access on. The server listens in the
// background: see RemoteStatus.
func (c *Client) EnableRemote() error { return c.call(Request{Op: "remote.enable"}, nil) }

// SetRelay sets the DERP relay of the server's address: a host of one's
// own, as derp.example.com, "gorex" for the team's
// (docs/NET-004-public-relay.md), or "" for tailcat's. A change makes a
// new address; the keys stay.
func (c *Client) SetRelay(relay string) error {
	return c.call(Request{Op: "remote.relaySet", Relay: relay}, nil)
}

// SetReachable sets Reachable at: an IP address of this machine where it
// is its own relay and shows a second address, or "" for off.
func (c *Client) SetReachable(ip string) error {
	return c.call(Request{Op: "remote.reachableSet", ReachableAt: ip}, nil)
}

// CheckFirewall asks the server to read Windows Firewall again for
// Reachable at, and returns once it has: RemoteStatus then tells.
func (c *Client) CheckFirewall() error { return c.call(Request{Op: "remote.firewallCheck"}, nil) }

// DisableRemote turns remote access off.
func (c *Client) DisableRemote() error { return c.call(Request{Op: "remote.disable"}, nil) }

// SetTailnet turns the Tailscale mode on or off: discovery and
// connections with the GoRex desktops on this account's tailnet. On
// Windows and macOS, turning it on also turns built-in Tailscale on.
func (c *Client) SetTailnet(on bool) error {
	return c.call(Request{Op: "remote.tailnetSet", On: on}, nil)
}

// RemoteControl lets remote clients run app actions through the window.
func (c *Client) RemoteControl(on bool) error {
	return c.call(Request{Op: "remote.controlSet", On: on}, nil)
}

// StartPairing opens the pairing window for five minutes, or opens it
// five minutes more, while remote access is on.
func (c *Client) StartPairing() error { return c.call(Request{Op: "remote.pairStart"}, nil) }

// StopPairing closes the pairing window and forgets its requests.
func (c *Client) StopPairing() error { return c.call(Request{Op: "remote.pairStop"}, nil) }

// ApprovePairing allows the client of a pairing request, by the name it
// asked with.
func (c *Client) ApprovePairing(publicKey string) error {
	return c.call(Request{Op: "remote.approve", Remote: &RemoteClient{PublicKey: publicKey}}, nil)
}

// RejectPairing drops a pairing request.
func (c *Client) RejectPairing(publicKey string) error {
	return c.call(Request{Op: "remote.reject", Remote: &RemoteClient{PublicKey: publicKey}}, nil)
}

// ApprovePairingCode allows the client of the one request by address,
// by its code; with more than one waiting it refuses.
func (c *Client) ApprovePairingCode(code string) error {
	return c.call(Request{Op: "remote.approve", Code: code}, nil)
}

// RejectPairingCode drops a request by address, by its code.
func (c *Client) RejectPairingCode(code string) error {
	return c.call(Request{Op: "remote.reject", Code: code}, nil)
}

// RequestPairing asks the server, connected to with ConnectRemote, to
// allow this machine by name; it returns the fingerprint of this
// machine's key, which the server shows next to the request.
func (c *Client) RequestPairing(name string) (string, error) {
	var res PairResult
	err := c.call(Request{Op: "remote.pair", Remote: &RemoteClient{Name: name}}, &res)
	if err != nil && strings.Contains(err.Error(), refusedMessage) {
		// A server not pairing refuses the connection.
		return "", errors.New("rex: the server is not pairing now: start pairing there first")
	}
	return res.Fingerprint, err
}

// refusedMessage is what a server says to a client it does not allow,
// while it is not pairing, before it closes the connection.
const refusedMessage = "rex: this machine is not allowed here"

// pendingMessage ends what a pairing server answers a client whose
// request to pair waits for its user.
const pendingMessage = "the request waits for approval"

// IsRefused tells an error of a server that does not allow this machine:
// it refused the connection, or it is pairing and serves requests to pair
// only. A connection lost otherwise is not a refusal.
func IsRefused(err error) bool {
	s := err.Error()
	return strings.Contains(s, refusedMessage) || strings.Contains(s, "not allowed before pairing")
}

// IsPairingPending tells the refusal of a server pairing whose user has
// not answered this machine's request yet.
func IsPairingPending(err error) bool {
	return IsRefused(err) && strings.Contains(err.Error(), pendingMessage)
}

// AllowRemote allows a client by its public key (ClientPublicKey on its
// machine).
func (c *Client) AllowRemote(name, publicKey string) error {
	return c.call(Request{Op: "remote.allow", Remote: &RemoteClient{Name: name, PublicKey: publicKey}}, nil)
}

// RevokeRemote removes a client and drops its connections.
func (c *Client) RevokeRemote(publicKey string) error {
	return c.call(Request{Op: "remote.revoke", Remote: &RemoteClient{PublicKey: publicKey}}, nil)
}

// RegenerateRemote makes new keys: the address changes, the old one is
// dead.
func (c *Client) RegenerateRemote() error { return c.call(Request{Op: "remote.regenerate"}, nil) }
