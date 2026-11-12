package rexapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// The Host check is defence in depth against DNS rebinding: a page that
// points a name of its own at the gateway's address is same-origin with
// the gateway, and its requests carry no Origin to refuse. They do carry
// that name in Host, which the gateway answers only when it is one of its
// own: localhost, an IP address (the listener's own among them), the
// names of built-in Tailscale's device, or a name web.json lists
// (gorex rexapi web host add NAME). Any other gets 421.
//
// The local socket and tailcat are not checked: no browser reaches
// either (a Unix socket has no name to rebind, and tailcat's clients are
// GoRex's own apps, by their keys), and Rex Go names a tailcat host
// <id>.tailcat.invalid.

// checksHost reports whether requests over transport have their Host
// checked.
func checksHost(transport string) bool {
	return transport != transportUnix && transport != transportTailcat
}

// hostAllowed reports whether a request's Host is one of the gateway's
// own names.
func (g *Gateway) hostAllowed(r *http.Request) bool {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")), ".")
	if h == "" {
		return false
	}
	if h == "localhost" {
		return true
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	if g.web.host(h) {
		return true
	}
	if g.tailnet != nil {
		for _, n := range g.tailnet.names(r.Context()) {
			if h == n {
				return true
			}
		}
	}
	return false
}

// names are the names built-in Tailscale's device is reached by: its
// MagicDNS name, and the first label of it, which MagicDNS resolves too.
// None while it is off.
func (t *tailnet) names(ctx context.Context) []string {
	t.mu.Lock()
	node := t.node
	t.mu.Unlock()
	if node == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	st, err := node.Status(ctx)
	if err != nil || st.Self == nil {
		return nil
	}
	full := strings.ToLower(strings.TrimSuffix(st.Self.DNSName, "."))
	if full == "" {
		return nil
	}
	short, _, _ := strings.Cut(full, ".")
	return []string{full, short}
}

// refuseHost answers a request whose Host is not the gateway's.
func refuseHost(w http.ResponseWriter, r *http.Request) {
	tracef("refused %s: host %q", r.URL.Path, r.Host)
	writeError(w, http.StatusMisdirectedRequest, "this GoRex gateway does not answer to the name "+r.Host+
		" (add it with gorex rexapi web host add NAME)")
}
