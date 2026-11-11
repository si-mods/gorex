package tailnet

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"tailscale.com/ipn/ipnstate"
)

// pingWait is how long a diagnostic ping waits for its pong.
const pingWait = 5 * time.Second

// PeerPath is how the device reaches the tailnet device host names (as
// the proxy takes it: an IP address, a MagicDNS name or its first label),
// as JSON, for the app's diagnostics: its name and address, whether it is
// online, the path, the direct endpoint or the DERP region, the seconds
// since the last WireGuard handshake, and the bytes received and sent.
// An error is in "error".
func PeerPath(host string) string {
	n, err := running()
	if err != nil {
		return encode(pathJSON{Error: err.Error()})
	}
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	st, err := n.StatusWithPeers(ctx)
	if err != nil {
		return encode(pathJSON{Error: err.Error()})
	}
	p, ip, err := findPeer(st, host)
	if err != nil {
		return encode(pathJSON{Error: err.Error()})
	}
	j := fromPeer(p, time.Now())
	j.IP = ip.String()
	return encode(j)
}

type pathJSON struct {
	Name      string  `json:"name,omitempty"`
	IP        string  `json:"ip,omitempty"`
	Online    bool    `json:"online"`
	Path      string  `json:"path,omitempty"`
	Endpoint  string  `json:"endpoint,omitempty"`
	Relay     string  `json:"relay,omitempty"`
	Handshake float64 `json:"handshake,omitempty"`
	Rx        int64   `json:"rx"`
	Tx        int64   `json:"tx"`
	Error     string  `json:"error,omitempty"`
}

func fromPeer(p *ipnstate.PeerStatus, now time.Time) pathJSON {
	j := pathJSON{Name: strings.TrimSuffix(p.DNSName, "."), Online: p.Online, Path: path(p), Endpoint: p.CurAddr, Relay: p.Relay, Rx: p.RxBytes, Tx: p.TxBytes}
	if p.PeerRelay != "" {
		j.Endpoint = p.PeerRelay
	}
	if !p.LastHandshake.IsZero() {
		j.Handshake = math.Round(now.Sub(p.LastHandshake).Seconds())
	}
	return j
}

// Ping sends a disco ping of size bytes to the tailnet device host names
// (PeerPath), as tailscale ping --size does, and tells, as JSON, how long
// its pong took and the path it came by: "direct" from an endpoint,
// "derp" through a region, or "peer-relay". A large ping that goes
// direct while the path's packets are smaller is lost; one that tries
// the path and DERP at once takes whichever answers first. An error, as
// no pong within 5 s, is in "error".
func Ping(host string, size int) string {
	n, err := running()
	if err != nil {
		return encode(pingJSON{Size: size, Error: err.Error()})
	}
	ctx, done := context.WithTimeout(context.Background(), pingWait)
	defer done()
	st, err := n.StatusWithPeers(ctx)
	if err != nil {
		return encode(pingJSON{Size: size, Error: err.Error()})
	}
	_, ip, err := findPeer(st, host)
	if err != nil {
		return encode(pingJSON{Size: size, Error: err.Error()})
	}
	r, err := n.Ping(ctx, ip, size)
	return encode(fromPing(size, r, err))
}

type pingJSON struct {
	Size     int     `json:"size"`
	Millis   float64 `json:"ms,omitempty"`
	Path     string  `json:"path,omitempty"`
	Endpoint string  `json:"endpoint,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func fromPing(size int, r *ipnstate.PingResult, err error) pingJSON {
	j := pingJSON{Size: size}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		j.Error = "no pong in " + pingWait.String()
	case err != nil:
		j.Error = err.Error()
	case r.Err != "":
		j.Error = r.Err
	default:
		j.Millis = math.Round(r.LatencySeconds*10000) / 10
		switch {
		case r.Endpoint != "":
			j.Path, j.Endpoint = "direct", r.Endpoint
		case r.PeerRelay != "":
			j.Path, j.Endpoint = "peer-relay", r.PeerRelay
		case r.DERPRegionCode != "":
			j.Path, j.Endpoint = "derp", r.DERPRegionCode
		}
	}
	return j
}
