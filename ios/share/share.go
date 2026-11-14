// Package share serves GoRex's web client on the iPhone's Personal Hotspot
// and forwards requests under /h/<id>/... to the phone's configured hosts.
package share

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultPort is the single port the hotspot share server listens on.
const DefaultPort = 7300

var hotspotPrefix = netip.MustParsePrefix("172.20.10.0/28")

// DialFunc dials a host:port for a given transport ("system", "tailnet", "tailcat").
type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

// Host describes one GoRex host shared by the phone.
type Host struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Icon      string `json:"icon,omitempty"`
	URL       string `json:"url"`
	Transport string `json:"transport,omitempty"` // "system", "tailnet", "tailcat"
}

// Device represents a hotspot client that has requested or been granted access.
type Device struct {
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	State     string `json:"state"` // "pending", "admitted", "denied"
	AddedAt   int64  `json:"addedAt"`
}

// HotspotIP returns the phone's Personal Hotspot IPv4 address (in 172.20.10.0/28),
// or "" if Personal Hotspot is not active.
func HotspotIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipNet.IP.To4())
			if !ok {
				continue
			}
			if hotspotPrefix.Contains(addr) {
				if addr.String() == "172.20.10.1" {
					return addr.String()
				}
				if fallback == "" {
					fallback = addr.String()
				}
			}
		}
	}
	return fallback
}

// Server is the hotspot HTTP + WebSocket reverse-proxy server.
type Server struct {
	mu         sync.RWMutex
	ln         net.Listener
	srv        *http.Server
	done       chan struct{}
	webDir     string
	phoneName  string
	hosts      map[string]Host
	hostOrder  []string
	devices    map[string]*Device
	conns      map[string]map[net.Conn]struct{}
	dialProxy  DialFunc
	dialSystem DialFunc
	onPending  func(Device)
	onChange   func()

	proxyTransport  *http.Transport
	systemTransport *http.Transport
}

// Config configures a new share Server.
type Config struct {
	Addr       string
	WebDir     string
	PhoneName  string
	DialProxy  DialFunc
	DialSystem DialFunc
	OnPending  func(Device)
	OnChange   func()
}

// Start creates and starts the share server on cfg.Addr.
func Start(cfg Config) (*Server, error) {
	if cfg.Addr == "" {
		return nil, errors.New("share: empty listen address")
	}
	if cfg.DialSystem == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		cfg.DialSystem = func(ctx context.Context, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", addr)
		}
	}
	if cfg.DialProxy == nil {
		cfg.DialProxy = cfg.DialSystem
	}
	if cfg.PhoneName == "" {
		cfg.PhoneName = "iPhone"
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}

	s := &Server{
		ln:         ln,
		done:       make(chan struct{}),
		webDir:     cfg.WebDir,
		phoneName:  cfg.PhoneName,
		hosts:      make(map[string]Host),
		devices:    make(map[string]*Device),
		conns:      make(map[string]map[net.Conn]struct{}),
		dialProxy:  cfg.DialProxy,
		dialSystem: cfg.DialSystem,
		onPending:  cfg.OnPending,
		onChange:   cfg.OnChange,
		proxyTransport: &http.Transport{
			DialContext:         func(ctx context.Context, _, addr string) (net.Conn, error) { return cfg.DialProxy(ctx, addr) },
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		systemTransport: &http.Transport{
			DialContext:         func(ctx context.Context, _, addr string) (net.Conn, error) { return cfg.DialSystem(ctx, addr) },
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	s.srv = &http.Server{
		Handler:   s,
		ConnState: s.trackConn,
	}
	go func() {
		_ = s.srv.Serve(ln)
		close(s.done)
	}()
	return s, nil
}

// Addr returns the listener's bound address.
func (s *Server) Addr() string {
	return s.ln.Addr().String()
}

// Stop closes the listener and all active client connections.
func (s *Server) Stop() {
	_ = s.srv.Close()
	s.proxyTransport.CloseIdleConnections()
	s.systemTransport.CloseIdleConnections()
	s.mu.Lock()
	for _, set := range s.conns {
		for c := range set {
			_ = c.Close()
		}
	}
	s.conns = make(map[string]map[net.Conn]struct{})
	s.mu.Unlock()
}

// Alive reports whether the listener is still accepting connections.
func (s *Server) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
	}
	c, err := net.DialTimeout("tcp", s.ln.Addr().String(), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// SetHosts updates the list of shared hosts from JSON.
func (s *Server) SetHosts(hostsJSON string) error {
	var list []Host
	if strings.TrimSpace(hostsJSON) != "" {
		if err := json.Unmarshal([]byte(hostsJSON), &list); err != nil {
			return err
		}
	}
	m := make(map[string]Host, len(list))
	order := make([]string, 0, len(list))
	for _, h := range list {
		if h.ID == "" || h.URL == "" {
			continue
		}
		if _, dup := m[h.ID]; !dup {
			order = append(order, h.ID)
		}
		m[h.ID] = h
	}
	s.mu.Lock()
	s.hosts = m
	s.hostOrder = order
	s.mu.Unlock()
	return nil
}

// SetPhoneName updates the displayed iPhone name.
func (s *Server) SetPhoneName(name string) {
	if name == "" {
		name = "iPhone"
	}
	s.mu.Lock()
	s.phoneName = name
	s.mu.Unlock()
}

// Admit marks ip as admitted.
func (s *Server) Admit(ip string) {
	s.setDeviceState(ip, "admitted", false)
}

// Deny marks ip as denied and closes its open connections.
func (s *Server) Deny(ip string) {
	s.setDeviceState(ip, "denied", true)
}

// Revoke removes ip from the device list and closes its open connections.
func (s *Server) Revoke(ip string) {
	s.mu.Lock()
	delete(s.devices, ip)
	toClose := s.takeConnsLocked(ip)
	cb := s.onChange
	s.mu.Unlock()
	for _, c := range toClose {
		_ = c.Close()
	}
	if cb != nil {
		cb()
	}
}

// Devices returns a snapshot of known devices.
func (s *Server) Devices() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, *d)
	}
	return out
}

func (s *Server) setDeviceState(ip, state string, closeConns bool) {
	s.mu.Lock()
	d, ok := s.devices[ip]
	if !ok {
		d = &Device{IP: ip, AddedAt: time.Now().Unix()}
		s.devices[ip] = d
	}
	d.State = state
	var toClose []net.Conn
	if closeConns {
		toClose = s.takeConnsLocked(ip)
	}
	cb := s.onChange
	s.mu.Unlock()
	for _, c := range toClose {
		_ = c.Close()
	}
	if cb != nil {
		cb()
	}
}

func (s *Server) takeConnsLocked(ip string) []net.Conn {
	set := s.conns[ip]
	if len(set) == 0 {
		return nil
	}
	out := make([]net.Conn, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	delete(s.conns, ip)
	return out
}

func (s *Server) trackConn(c net.Conn, st http.ConnState) {
	ip := remoteIP(c.RemoteAddr().String())
	if ip == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch st {
	case http.StateNew:
		set := s.conns[ip]
		if set == nil {
			set = make(map[net.Conn]struct{})
			s.conns[ip] = set
		}
		set[c] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		if set := s.conns[ip]; set != nil {
			delete(set, c)
			if len(set) == 0 {
				delete(s.conns, ip)
			}
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r.RemoteAddr)
	if r.URL.Path == "/share/state" {
		s.handleState(w, r, ip)
		return
	}

	state, isNew := s.checkOrRequest(ip, r.Header.Get("User-Agent"))
	if isNew && s.onPending != nil {
		s.mu.RLock()
		d := *s.devices[ip]
		s.mu.RUnlock()
		s.onPending(d)
	}

	if state != "admitted" {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			s.serveAdmissionPage(w, state)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if r.URL.Path == "/share/hosts" {
		s.handleHosts(w)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/h/") {
		s.handleProxy(w, r)
		return
	}

	s.serveStatic(w, r)
}

func (s *Server) checkOrRequest(ip, ua string) (state string, isNew bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devices[ip]; ok {
		if ua != "" && d.UserAgent == "" {
			d.UserAgent = SummarizeUA(ua)
		}
		return d.State, false
	}
	s.devices[ip] = &Device{
		IP:        ip,
		UserAgent: SummarizeUA(ua),
		State:     "pending",
		AddedAt:   time.Now().Unix(),
	}
	return "pending", true
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request, ip string) {
	state, isNew := s.checkOrRequest(ip, r.Header.Get("User-Agent"))
	if isNew && s.onPending != nil {
		s.mu.RLock()
		d := *s.devices[ip]
		s.mu.RUnlock()
		s.onPending(d)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"state": state})
}

func (s *Server) handleHosts(w http.ResponseWriter) {
	type publicHost struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon,omitempty"`
	}
	s.mu.RLock()
	phone := s.phoneName
	out := make([]publicHost, 0, len(s.hostOrder))
	for _, id := range s.hostOrder {
		if h, ok := s.hosts[id]; ok {
			out = append(out, publicHost{ID: h.ID, Name: h.Name, Icon: h.Icon})
		}
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"phone": phone,
		"hosts": out,
	})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Only same-origin requests (or requests with no Origin, e.g. hidden-iframe downloads) are allowed.
	if origin := r.Header.Get("Origin"); origin != "" {
		want := "http://" + r.Host
		if !strings.EqualFold(origin, want) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
	}

	rest := strings.TrimPrefix(r.URL.Path, "/h/")
	rawID, subPath, _ := strings.Cut(rest, "/")
	id, err := url.PathUnescape(rawID)
	if err != nil || id == "" {
		http.NotFound(w, r)
		return
	}

	s.mu.RLock()
	h, ok := s.hosts[id]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	target, err := url.Parse(h.URL)
	if err != nil {
		http.Error(w, "bad host url", http.StatusBadGateway)
		return
	}

	tr := s.systemTransport
	if h.Transport == "tailnet" || h.Transport == "tailcat" {
		tr = s.proxyTransport
	}

	rp := &httputil.ReverseProxy{
		Transport: tr,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/" + subPath
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			// Drop Origin so the gateway treats the request as native (the phone's).
			pr.Out.Header.Del("Origin")
		},
	}
	rp.ServeHTTP(w, r)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.webDir == "" {
		http.NotFound(w, r)
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	if clean == "/" {
		clean = "/index.html"
	}
	full := filepath.Join(s.webDir, filepath.FromSlash(clean))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if clean == "/index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else if strings.HasPrefix(clean, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFile(w, r, full)
}

func (s *Server) serveAdmissionPage(w http.ResponseWriter, state string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	msg := "Allow this computer on your iPhone"
	if state == "denied" {
		msg = "Access denied on iPhone"
	}
	_, _ = fmt.Fprintf(w, `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Rex Go</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; background: #111; color: #eee;
         display: grid; place-items: center; min-height: 100vh; margin: 0; text-align: center; padding: 24px; }
  h1 { font-size: 20px; font-weight: 600; margin: 0 0 8px; }
  p { color: #999; font-size: 14px; margin: 0; }
</style>
<div>
  <h1 id="msg">%s</h1>
  <p id="sub">%s</p>
</div>
<script>
const poll = async () => {
  try {
    const r = await fetch("/share/state", { cache: "no-store" });
    const d = await r.json();
    if (d.state === "admitted") { location.reload(); return; }
    if (d.state === "denied") {
      document.getElementById("msg").textContent = "Access denied on iPhone";
      document.getElementById("sub").textContent = "";
      return;
    }
  } catch {}
  setTimeout(poll, 1500);
};
if (%q !== "denied") setTimeout(poll, 1500);
</script>`, html.EscapeString(msg), html.EscapeString(admissionSub(state)), state)
}

func admissionSub(state string) string {
	if state == "denied" {
		return ""
	}
	return "Tap Allow in Rex Go to open the web client."
}

func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// SummarizeUA turns a raw User-Agent header into a short label such as "Chrome on macOS".
func SummarizeUA(ua string) string {
	if ua == "" {
		return "Browser"
	}
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	osName := ""
	switch {
	case strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "iPad"):
		osName = "iPad"
	case strings.Contains(ua, "iPhone"):
		osName = "iPhone"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	if osName == "" {
		return browser
	}
	return browser + " on " + osName
}
