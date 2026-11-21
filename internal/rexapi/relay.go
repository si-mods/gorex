package rexapi

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// relayTTL is how long a relay lives after it opens.
	relayTTL = 10 * time.Minute
	// relayIdle is how long a relay without a connection lives.
	relayIdle = 2 * time.Minute
	// relayLimit is how many relays one gateway keeps at once.
	relayLimit = 8
)

// relay forwards connections on a host address to a loopback port of a
// pane, for a phone whose browser cannot reach the loopback port itself.
type relay struct {
	host    string
	target  int
	ln      net.Listener
	created time.Time
	expires time.Time
	ttl     time.Duration
	idle    time.Duration

	mu     sync.Mutex
	active map[net.Conn]struct{}
	last   time.Time
	closed bool
}

// url is the address a browser opens for the relay.
func (r *relay) url() string {
	return "http://" + net.JoinHostPort(r.host, strconv.Itoa(r.ln.Addr().(*net.TCPAddr).Port)) + "/"
}

// serve accepts connections until the relay closes.
func (r *relay) serve() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			c.Close()
			return
		}
		r.last = time.Now()
		r.mu.Unlock()
		go r.pipe(c)
	}
}

// pipe connects one accepted connection to the loopback port.
func (r *relay) pipe(c net.Conn) {
	t, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.target)), 5*time.Second)
	if err != nil {
		c.Close()
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		c.Close()
		t.Close()
		return
	}
	if r.active == nil {
		r.active = map[net.Conn]struct{}{}
	}
	r.active[c], r.active[t] = struct{}{}, struct{}{}
	r.mu.Unlock()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(t, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, t); done <- struct{}{} }()
	<-done
	c.Close()
	t.Close()
	<-done
	r.mu.Lock()
	delete(r.active, c)
	delete(r.active, t)
	r.mu.Unlock()
}

// close ends the relay and every connection it carries.
func (r *relay) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	active := make([]net.Conn, 0, len(r.active))
	for c := range r.active {
		active = append(active, c)
	}
	r.mu.Unlock()
	r.ln.Close()
	for _, c := range active {
		c.Close()
	}
}

// expired tells whether the relay passed its time or its idle time.
func (r *relay) expired(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.After(r.expires) {
		return true
	}
	return len(r.active) == 0 && now.Sub(r.last) > r.idle
}

// openRelay opens a relay on host to the loopback port, or returns the
// one already open.
func (g *Gateway) openRelay(host string, target int) (*relay, error) {
	g.relaysMu.Lock()
	defer g.relaysMu.Unlock()
	key := host + "\x00" + strconv.Itoa(target)
	if r := g.relays[key]; r != nil {
		if !r.expired(time.Now()) {
			return r, nil
		}
		r.close()
		delete(g.relays, key)
	}
	if len(g.relays) >= relayLimit {
		return nil, errConflict("this host holds %d open ports already", relayLimit)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, errInternal("opening %s: %v", host, err)
	}
	ttl, idle := g.relayTTL, g.relayIdle
	if ttl <= 0 {
		ttl = relayTTL
	}
	if idle <= 0 {
		idle = relayIdle
	}
	now := time.Now()
	r := &relay{
		host: host, target: target, ln: ln, created: now,
		expires: now.Add(ttl), ttl: ttl, idle: idle, last: now,
	}
	if g.relays == nil {
		g.relays = map[string]*relay{}
	}
	g.relays[key] = r
	go r.serve()
	go func() {
		every := min(idle, 30*time.Second) / 2
		if every <= 0 {
			every = 10 * time.Millisecond
		}
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for range ticker.C {
			if r.expired(time.Now()) {
				g.closeRelay(r)
				return
			}
		}
	}()
	return r, nil
}

// closeRelay closes a relay and forgets it.
func (g *Gateway) closeRelay(r *relay) {
	g.relaysMu.Lock()
	key := r.host + "\x00" + strconv.Itoa(r.target)
	if g.relays[key] == r {
		delete(g.relays, key)
	}
	g.relaysMu.Unlock()
	r.close()
}

// closeRelayTo closes the relay on host to a loopback port, if open.
func (g *Gateway) closeRelayTo(host string, target int) {
	g.relaysMu.Lock()
	key := host + "\x00" + strconv.Itoa(target)
	r := g.relays[key]
	g.relaysMu.Unlock()
	if r != nil {
		g.closeRelay(r)
	}
}

// closeRelays ends every relay, as the gateway stops.
func (g *Gateway) closeRelays() {
	g.relaysMu.Lock()
	all := make([]*relay, 0, len(g.relays))
	for _, r := range g.relays {
		all = append(all, r)
	}
	g.relays = nil
	g.relaysMu.Unlock()
	for _, r := range all {
		r.close()
	}
}

// localHost is the host address of the connection a request arrived on,
// which a client on that network can reach. A socket has none, so the
// name the client dialed is used then. It is empty when neither says.
func localHost(r *http.Request) string {
	if c, _ := r.Context().Value(connKey{}).(net.Conn); c != nil {
		if a, ok := c.LocalAddr().(*net.TCPAddr); ok {
			return a.IP.String()
		}
	}
	if host, _, err := net.SplitHostPort(r.Host); err == nil && host != "" {
		if ip := net.ParseIP(host); ip != nil || strings.Contains(host, ".") {
			return host
		}
	}
	return ""
}

// methodPortOpen opens a relay to a panes loopback port, for the phone's
// browser, and answers its URL.
func methodPortOpen(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	port, ok, aerr := a.int("port")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || port < 1 || port > 65535 {
		return nil, errBadRequest("port must be between 1 and 65535")
	}
	if aerr := a.only("session_id", "block_id", "port"); aerr != nil {
		return nil, aerr
	}
	b := s.findBlock(blockID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	ports, err := c.g.opt.Client.Ports(b.sid)
	if err != nil {
		return nil, errInternal("listing the terminal's ports: %v", err)
	}
	loopback := false
	for _, p := range ports.Ports {
		if p.Port == port && (p.Address == "127.0.0.1" || p.Address == "::1") {
			loopback = true
			break
		}
	}
	if !loopback {
		return nil, errNotFound("the pane does not listen on %d on loopback", port)
	}
	switch c.transport {
	case "unix", "tailcat":
		return nil, errNotSupported("this connection has no network address a browser can open")
	}
	if c.local == "" {
		return nil, errNotSupported("the gateway does not know this connection's address")
	}
	r, err := c.g.openRelay(c.local, port)
	if err != nil {
		if e, ok := err.(*apiError); ok {
			return nil, e
		}
		return nil, errInternal("%v", err)
	}
	return map[string]any{"url": r.url(), "expires_at": r.expires.UTC().Format(time.RFC3339)}, nil
}

// methodPortClose closes a relay that methodPortOpen opened.
func methodPortClose(c *control, a args) (any, *apiError) {
	if _, aerr := c.attachedArg(a); aerr != nil {
		return nil, aerr
	}
	port, ok, aerr := a.int("port")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || port < 1 || port > 65535 {
		return nil, errBadRequest("port must be between 1 and 65535")
	}
	if aerr := a.only("session_id", "block_id", "port"); aerr != nil {
		return nil, aerr
	}
	if c.local != "" {
		c.g.closeRelayTo(c.local, port)
	}
	return struct{}{}, nil
}

// portOpenEntry is gorex.port.open in the help document.
var portOpenEntry = map[string]any{
	"name":        "gorex.port.open",
	"description": "Open a relay on this host's address to a pane's loopback port, for a browser on the same network. GoRex's own method.",
	"invoke":      map[string]any{"method": "gorex.port.open"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "The session that holds the block."},
			"block_id":   map[string]any{"type": "string", "description": "The block whose port to relay."},
			"port":       map[string]any{"type": "integer", "description": "The loopback port the pane listens on.", "minimum": 1, "maximum": 65535},
		},
		"required":             []any{"session_id", "block_id", "port"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":        map[string]any{"type": "string", "description": "The address a browser on this network opens."},
			"expires_at": map[string]any{"type": "string", "description": "When the relay closes by itself, as an RFC 3339 timestamp."},
		},
		"required":             []any{"url", "expires_at"},
		"additionalProperties": false,
	},
}

// portCloseEntry is gorex.port.close in the help document.
var portCloseEntry = map[string]any{
	"name":        "gorex.port.close",
	"description": "Close a relay that gorex.port.open opened. GoRex's own method.",
	"invoke":      map[string]any{"method": "gorex.port.close"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "The session that holds the block."},
			"block_id":   map[string]any{"type": "string", "description": "The block whose relay to close."},
			"port":       map[string]any{"type": "integer", "description": "The port the relay forwards to.", "minimum": 1, "maximum": 65535},
		},
		"required":             []any{"session_id", "block_id", "port"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	},
}
