package tailnet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/socks5"
)

// proxy is the app's way into the tailnet: a SOCKS5 server on loopback,
// user name "tsnet" and a random password per run, that dials only the
// tailnet's devices. tsnet's own (Server.Loopback) dials any address that
// isn't a known peer through the phone's network, as every tailnet address
// is before the network map arrives, and serves the LocalAPI on its port.
type proxy struct {
	ln       net.Listener
	password string
}

// startProxy serves the proxy; status gives the node's peers and dial
// dials through the node.
func startProxy(status func(context.Context) (*ipnstate.Status, error),
	dial func(ctx context.Context, network, addr string) (net.Conn, error)) (*proxy, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{ln: ln, password: hex.EncodeToString(b[:])}
	s := &socks5.Server{
		Logf:     func(string, ...any) {},
		Username: "tsnet",
		Password: p.password,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network != "tcp" {
				return nil, errors.New("tcp only")
			}
			st, err := status(ctx)
			if err != nil {
				return nil, err
			}
			to, err := peerAddr(st, addr)
			if err != nil {
				return nil, err
			}
			return dial(ctx, "tcp", to.String())
		},
	}
	go s.Serve(ln)
	return p, nil
}

func (p *proxy) close() { p.ln.Close() }

// peerAddr is the Tailscale address of the device addr names, as
// host:port: a peer's IP address, its MagicDNS name, or its name short of
// the tailnet's suffix. Anything else is an error, as is any address
// before the node runs with its network map.
func peerAddr(st *ipnstate.Status, addr string) (netip.AddrPort, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return netip.AddrPort{}, err
	}
	pn, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if st == nil || st.BackendState != ipn.Running.String() || len(st.Peer) == 0 {
		return netip.AddrPort{}, errors.New("tailnet not ready")
	}
	ip, ipErr := netip.ParseAddr(host)
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, k := range st.Peers() {
		ps := st.Peer[k]
		if len(ps.TailscaleIPs) == 0 {
			continue
		}
		if ipErr == nil {
			for _, a := range ps.TailscaleIPs {
				if a == ip.Unmap() {
					return netip.AddrPortFrom(a, uint16(pn)), nil
				}
			}
			continue
		}
		full := strings.ToLower(strings.TrimSuffix(ps.DNSName, "."))
		short, _, _ := strings.Cut(full, ".")
		if name == full || name == short {
			return netip.AddrPortFrom(ps.TailscaleIPs[0], uint16(pn)), nil
		}
	}
	return netip.AddrPort{}, errors.New("not a device on the tailnet: " + host)
}
