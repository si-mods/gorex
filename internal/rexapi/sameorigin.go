package rexapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// sameOrigin reports whether a request may go on as far as its Origin
// header tells. Native clients send none, and pass as before; a web page
// passes only when it is this gateway's own (the web client at /): the
// same scheme, host and port the request came to. Without it any page a
// browser on an allowed network opens could reach the API as that
// browser, since the access rule judges the address, not the page.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	scheme := requestScheme(r)
	return strings.EqualFold(u.Scheme, scheme) &&
		strings.EqualFold(withPort(u.Host, scheme), withPort(r.Host, scheme))
}

// requestScheme is the scheme the request came by. The tailnet's TLS
// listener is wrapped to track its connections, which hides the TLS
// state from net/http, so its transport tells instead.
func requestScheme(r *http.Request) string {
	if r.TLS != nil || transportOf(r) == transportTailscale {
		return "https"
	}
	return "http"
}

// withPort returns host:port, with the scheme's default port when host
// has none.
func withPort(host, scheme string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := "80"
	if scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}
