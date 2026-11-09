package rexapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const transportPublic = "public"
const deviceCookie = "__Host-gorex-device"

// PublicConfig enables device-authenticated access, independent of a tailnet.
// Without a certificate the listener must be loopback, behind an HTTPS proxy.
type PublicConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Listen  string `json:"listen"`
	Cert    string `json:"cert,omitempty"`
	Key     string `json:"key,omitempty"`
}

func publicConfPath(dir string) string { return filepath.Join(dir, "public.json") }

func LoadPublic(dir string) (PublicConfig, error) {
	var c PublicConfig
	b, err := os.ReadFile(publicConfPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

func (c PublicConfig) check() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("public URL must be an https:// address without a path, user, query or fragment")
	}
	addr, err := netip.ParseAddrPort(c.Listen)
	if err != nil {
		return errors.New("listen must be an IP address and port")
	}
	if (c.Cert == "") != (c.Key == "") {
		return errors.New("both --cert and --key are required for TLS")
	}
	if c.Cert == "" && !addr.Addr().IsLoopback() {
		return errors.New("a listener without TLS must use a loopback address behind an HTTPS reverse proxy")
	}
	return nil
}

func SavePublic(dir string, c PublicConfig) error {
	if err := c.check(); err != nil {
		return err
	}
	c.URL = strings.TrimSuffix(c.URL, "/")
	u, _ := url.Parse(c.URL)
	u.Host = strings.ToLower(u.Host)
	if u.Port() == "443" {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	c.URL = u.String()
	return writeJSON(publicConfPath(dir), c)
}

type publicDevice struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Hash    string    `json:"hash"`
}

type publicPairing struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}

type publicState struct {
	Devices  []publicDevice  `json:"devices"`
	Pairings []publicPairing `json:"pairings"`
}

// publicAccess owns credentials in one gateway process. Administrative writes
// go through the local socket; configuration alone may be changed offline.
type publicAccess struct {
	g        *Gateway
	dir      string
	mu       sync.Mutex
	state    publicState
	config   PublicConfig
	conns    map[*trackedConn]string
	applyMu  sync.Mutex
	listener *trackedListener
	status   LANStatus
}

func newPublicAccess(g *Gateway, dir string) (*publicAccess, error) {
	p := &publicAccess{g: g, dir: dir, conns: map[*trackedConn]string{}}
	b, err := os.ReadFile(filepath.Join(dir, "devices.json"))
	if err == nil {
		err = json.Unmarshal(b, &p.state)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("device credentials: %w", err)
	}
	return p, nil
}

func credentialHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (p *publicAccess) save(next publicState) error {
	if err := writeJSON(filepath.Join(p.dir, "devices.json"), next); err != nil {
		return err
	}
	p.state = next
	return nil
}

func (p *publicAccess) pairCode() (string, time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := publicState{Devices: p.state.Devices}
	for _, code := range p.state.Pairings {
		if time.Now().Before(code.Expires) {
			next.Pairings = append(next.Pairings, code)
		}
	}
	if len(next.Pairings) >= 16 {
		return "", time.Time{}, errors.New("too many pending pairing codes; wait for them to expire")
	}
	code, expires := base64url(randomBytes(32)), time.Now().Add(5*time.Minute)
	next.Pairings = append(next.Pairings, publicPairing{Hash: credentialHash(code), Expires: expires})
	return code, expires, p.save(next)
}

func (p *publicAccess) redeem(code, name string) (publicDevice, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(name) == 0 || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return publicDevice{}, "", errors.New("a device name of 1 to 128 bytes is required")
	}
	if len(p.state.Devices) >= 256 {
		return publicDevice{}, "", errors.New("device limit reached")
	}
	hash := credentialHash(code)
	next, found := publicState{Devices: append([]publicDevice(nil), p.state.Devices...)}, false
	for _, pairing := range p.state.Pairings {
		if time.Now().Before(pairing.Expires) {
			if subtle.ConstantTimeCompare([]byte(pairing.Hash), []byte(hash)) == 1 {
				found = true
			} else {
				next.Pairings = append(next.Pairings, pairing)
			}
		}
	}
	if !found {
		return publicDevice{}, "", errors.New("pairing code is invalid or expired")
	}
	token := base64url(randomBytes(32))
	d := publicDevice{ID: randomUUID(), Name: name, Created: time.Now(), Hash: credentialHash(token)}
	next.Devices = append(next.Devices, d)
	if err := p.save(next); err != nil {
		return publicDevice{}, "", err
	}
	return d, token, nil
}

func (p *publicAccess) revoke(id string) error {
	return p.revokeExcept(id, nil)
}

// A logout response must clear its cookie before its own HTTP connection
// closes. Other connections of the device are closed immediately.
func (p *publicAccess) revokeExcept(id string, keep *trackedConn) error {
	p.mu.Lock()
	next, found := publicState{Pairings: p.state.Pairings}, false
	for _, d := range p.state.Devices {
		if d.ID == id {
			found = true
		} else {
			next.Devices = append(next.Devices, d)
		}
	}
	if !found {
		p.mu.Unlock()
		return errors.New("unknown device")
	}
	if err := p.save(next); err != nil {
		p.mu.Unlock()
		return err
	}
	var conns []*trackedConn
	for c, device := range p.conns {
		if device == id && c != keep {
			conns = append(conns, c)
		}
	}
	p.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	return nil
}

// admit checks every HTTP request, including both WebSocket handshakes. Native
// clients use a bearer token; browser clients use an HttpOnly host-only cookie.
func (p *publicAccess) admit(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	p.mu.Lock()
	conf := p.config
	p.mu.Unlock()
	if !conf.Enabled {
		writeRefusal(w, "public access is off")
		return nil, false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != conf.URL {
		writeRefusal(w, "origin is not this gateway")
		return nil, false
	}
	if r.URL.Path == "/api/access/pair" || isWebAsset(r.URL.Path) && (r.Method == "GET" || r.Method == "HEAD") {
		// Pairing needs an invitation rather than an existing credential.
		// Dispatch through Handler so the gateway's Access rule still applies.
		return map[string]any{"kind": "anonymous", "transport": transportPublic, "display": "pairing"}, true
	}
	token := ""
	if auth := r.Header.Get("Authorization"); auth != "" {
		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, 401, "device credential required")
			return nil, false
		}
		token = strings.TrimPrefix(auth, "Bearer ")
	} else if cookie, err := r.Cookie(deviceCookie); err == nil {
		token = cookie.Value
	}
	hash := credentialHash(token)
	p.mu.Lock()
	var device *publicDevice
	for _, d := range p.state.Devices {
		if token != "" && subtle.ConstantTimeCompare([]byte(d.Hash), []byte(hash)) == 1 {
			copy := d
			device = &copy
			break
		}
	}
	if device == nil {
		p.mu.Unlock()
		writeError(w, 401, "device credential required or revoked")
		return nil, false
	}
	// The tracked connection remains registered after HTTP upgrades, until it
	// closes. Revoking a device therefore also ends its live data streams.
	c, _ := r.Context().Value(connKey{}).(*trackedConn)
	added := c != nil && p.conns[c] == ""
	if c != nil {
		p.conns[c] = device.ID
	}
	p.mu.Unlock()
	if added {
		c.onClose(func() { p.mu.Lock(); delete(p.conns, c); p.mu.Unlock() })
	}
	return map[string]any{"kind": "device", "transport": transportPublic, "device_id": device.ID, "display": device.Name}, true
}

func (p *publicAccess) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "pairing is a POST")
		return
	}
	var in struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		writeError(w, 400, "invalid pairing request")
		return
	}
	d, token, err := p.redeem(in.Code, in.Name)
	if err != nil {
		writeError(w, 403, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 365 * 24 * 60 * 60})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"device_id": d.ID, "token": token})
}

func (g *Gateway) handlePublicPair(w http.ResponseWriter, r *http.Request) {
	if transportOf(r) != transportPublic || g.public == nil {
		writeRefusal(w, "pair through the public HTTPS entry point")
		return
	}
	g.public.handlePair(w, r)
}

// handlePublicAdmin is deliberately outside Rex's protocol. Only the local
// user socket can issue codes, list devices, or revoke them.
func (g *Gateway) handlePublicAdmin(w http.ResponseWriter, r *http.Request) {
	if transportOf(r) != transportUnix || g.public == nil {
		writeRefusal(w, "device administration is local only")
		return
	}
	p := g.public
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.URL.Path == "/api/access/pairing" && r.Method == http.MethodPost:
		p.mu.Lock()
		enabled := p.config.Enabled
		p.mu.Unlock()
		if !enabled {
			writeError(w, 409, "enable public access before pairing")
			return
		}
		code, expires, err := p.pairCode()
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		p.mu.Lock()
		base := p.config.URL
		p.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"code": code, "expires": expires, "url": base})
	case r.URL.Path == "/api/access/devices" && r.Method == http.MethodGet:
		p.mu.Lock()
		devices := make([]map[string]any, 0, len(p.state.Devices))
		for _, d := range p.state.Devices {
			devices = append(devices, map[string]any{"id": d.ID, "name": d.Name, "created": d.Created})
		}
		p.mu.Unlock()
		json.NewEncoder(w).Encode(devices)
	case r.URL.Path == "/api/access/revoke" && r.Method == http.MethodPost:
		var in struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
			writeError(w, 400, "invalid device ID")
			return
		}
		if err := p.revoke(in.ID); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		json.NewEncoder(w).Encode(struct{}{})
	default:
		writeError(w, 405, "unsupported device administration request")
	}
}

// sameDevice binds reconnect tokens and data tickets to the authenticated
// device. Other transports keep Rex's existing behavior.
func sameDevice(a map[string]any, deviceID string, b map[string]any) bool {
	if a["transport"] != transportPublic && b["transport"] != transportPublic {
		return true
	}
	return a["transport"] == transportPublic && b["transport"] == transportPublic && deviceID != "" && deviceID == b["device_id"]
}

func (g *Gateway) recordAllows(record string, principal map[string]any) bool {
	g.mu.Lock()
	rec := g.clients[record]
	g.mu.Unlock()
	if rec == nil {
		return principal["transport"] != transportPublic
	}
	return sameDevice(rec.principal, rec.deviceID, principal)
}

func (p *publicAccess) validDevice(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.state.Devices {
		if d.ID == id {
			return true
		}
	}
	return false
}

func (p *publicAccess) apply(c PublicConfig) {
	p.applyMu.Lock()
	defer p.applyMu.Unlock()
	p.mu.Lock()
	old := p.config
	p.config = c
	p.mu.Unlock()
	if old == c && p.listener != nil {
		return
	}
	if p.listener != nil {
		p.listener.Close()
		p.listener = nil
	}
	s := LANStatus{Running: true, Updated: time.Now()}
	defer func() { p.status = s; writeJSON(filepath.Join(p.dir, "public-status.json"), s) }()
	if !c.Enabled {
		return
	}
	if err := c.check(); err != nil {
		s.Error = err.Error()
		return
	}
	var tlsConfig *tls.Config
	if c.Cert != "" {
		cert, err := tls.LoadX509KeyPair(c.Cert, c.Key)
		if err != nil {
			s.Error = "cannot load public TLS certificate and key"
			return
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		s.Error = err.Error()
		return
	}
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
	}
	p.listener = newTrackedListener(ln)
	s.Listening = ln.Addr().String()
	listener := p.listener
	go func() {
		if err := p.g.serveOn(listener, transportPublic); err != nil {
			logf("public listener: %v", err)
		}
	}()
}

func (p *publicAccess) close() {
	p.applyMu.Lock()
	defer p.applyMu.Unlock()
	if p.listener != nil {
		p.listener.Close()
		p.listener = nil
	}
	writeJSON(filepath.Join(p.dir, "public-status.json"), LANStatus{})
}
