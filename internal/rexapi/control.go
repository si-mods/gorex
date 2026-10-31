package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// outMsg is one message waiting for the writer: a response or an event.
type outMsg struct {
	data  []byte
	event bool
	size  int
}

// control is one control connection.
type control struct {
	g  *Gateway
	ws *websocket.Conn
	// transport is how the client reached the gateway: the methods that
	// administer it are for its local socket only.
	transport string
	rec       *clientRecord
	out       chan outMsg
	done      chan struct{}

	mu      sync.Mutex
	seq     uint64
	queued  int
	pending map[string]bool
	ordered sync.Mutex

	ours      atomic.Bool
	closeOnce sync.Once
	// hold, when set by a test, runs before a request is handled, so
	// that a test can keep a request's id pending.
	hold func(id string)
}

// handleControl upgrades a request to a control connection and serves it
// until it closes.
func (g *Gateway) handleControl(w http.ResponseWriter, r *http.Request) {
	// Native clients send no Origin, and the access rule is the boundary
	// that decides who may connect, so the WebSocket origin check is
	// left to it.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{controlProtocol},
		InsecureSkipVerify: true,
		// Rex offers permessage-deflate without context takeover, and
		// leaves a hello under 512 bytes uncompressed.
		CompressionMode:      websocket.CompressionNoContextTakeover,
		CompressionThreshold: maxHello,
	})
	if err != nil {
		return
	}
	if ws.Subprotocol() != controlProtocol {
		ws.Close(1002, "protocol error")
		return
	}
	ws.SetReadLimit(maxMessage)
	c := &control{
		g: g, ws: ws, transport: transportOf(r),
		out:     make(chan outMsg, 1<<16),
		done:    make(chan struct{}),
		pending: map[string]bool{},
		hold:    g.hold,
	}
	hello, failure := readHello(ws, g.helloWait)
	if failure != "" {
		// A hello that never came is dropped without a close frame, which
		// clients see as an abnormal closure.
		if failure == "timeout" {
			ws.CloseNow()
			return
		}
		if failure == "binary" {
			ws.Close(1003, "protocol error")
			return
		}
		if failure == "utf8" {
			ws.Close(1007, "protocol error")
			return
		}
		ws.Close(1002, "protocol error")
		return
	}
	rev := negotiate(hello.apiRevision, hello.apiMinRevision)
	if rev < 0 {
		ws.Close(1008, "no common API revision: this server speaks revision 0 through 7; update the client")
		return
	}
	var rec *clientRecord
	resumed := false
	if hello.reconnectToken != "" {
		rec, resumed = g.resumeControl(c, hello.reconnectToken)
	}
	if rec == nil {
		rec = g.addControl(c, hello.client, principalOf(r))
	}
	c.rec = rec
	wel := welcome{
		Type:     "welcome",
		Protocol: controlProtocol,
		Limits:   limits{MaxMessageBytes: maxMessage},
		Client: &welcomeClient{
			ID:             rec.id,
			ReconnectToken: rec.token,
			Resumed:        resumed,
		},
		APIRevision:       rev,
		APIServerRevision: apiRevision,
	}
	if err := c.writeSync(wel); err != nil {
		ws.CloseNow()
		return
	}
	g.mu.Lock()
	g.conns[c] = struct{}{}
	g.mu.Unlock()
	go c.writeLoop()
	go c.keepalive()
	c.readLoop()
}

// welcome is the server's first message.
type welcome struct {
	Type              string         `json:"type"`
	Protocol          string         `json:"protocol"`
	Limits            limits         `json:"limits"`
	Client            *welcomeClient `json:"client,omitempty"`
	Features          []string       `json:"features,omitempty"`
	APIRevision       int            `json:"api_revision"`
	APIServerRevision int            `json:"api_server_revision"`
	APIMinRevision    int            `json:"api_min_revision,omitempty"`
}

type limits struct {
	MaxMessageBytes int `json:"max_message_bytes"`
}

type welcomeClient struct {
	ID             string `json:"id"`
	ReconnectToken string `json:"reconnect_token"`
	Resumed        bool   `json:"resumed,omitempty"`
}

// hello is what a client says when it connects.
type hello struct {
	reconnectToken string
	client         *clientInfo
	apiRevision    int
	apiMinRevision int
}

// readHello reads and checks a control connection's first message. A
// non-empty result names why it failed.
func readHello(ws *websocket.Conn, wait time.Duration) (*hello, string) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	typ, data, err := ws.Read(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, "timeout"
		}
		return nil, "closed"
	}
	if typ != websocket.MessageText {
		return nil, "binary"
	}
	if !utf8.Valid(data) {
		return nil, "utf8"
	}
	if len(data) > maxHello {
		return nil, "large"
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, "json"
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil || kind != "hello" {
		return nil, "type"
	}
	h := &hello{}
	if raw, ok := fields["reconnect_token"]; ok {
		if err := json.Unmarshal(raw, &h.reconnectToken); err != nil {
			return nil, "token"
		}
	}
	if raw, ok := fields["api_revision"]; ok {
		n, err := int64Of(raw)
		if err != nil {
			return nil, "revision"
		}
		h.apiRevision = int(n)
	}
	if raw, ok := fields["api_min_revision"]; ok {
		n, err := int64Of(raw)
		if err != nil {
			return nil, "revision"
		}
		h.apiMinRevision = int(n)
	}
	if raw, ok := fields["client"]; ok {
		var info struct {
			Kind     string `json:"kind"`
			Platform string `json:"platform"`
			Name     string `json:"name"`
			Version  string `json:"version"`
			Label    string `json:"label"`
		}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &info); err != nil {
				return nil, "client"
			}
			h.client = &clientInfo{
				Kind:     clientKind(info.Kind),
				Platform: clientPlatform(info.Platform),
				Name:     info.Name,
				Version:  info.Version,
				Label:    info.Label,
			}
		}
	}
	return h, ""
}

// int64Of reads a JSON integer.
func int64Of(raw json.RawMessage) (int64, error) {
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	return n, nil
}

// negotiate returns the newest API revision both sides speak, or -1 when
// they share none.
func negotiate(clientRevision, clientMin int) int {
	rev := min(clientRevision, apiRevision)
	if rev < apiMinRevision || rev < clientMin {
		return -1
	}
	return rev
}

// clientKind treats a kind this server does not know as other.
func clientKind(kind string) string {
	switch kind {
	case "", "app", "cli", "agent", "other":
		return kind
	default:
		return "other"
	}
}

// clientPlatform treats a platform this server does not know as other.
func clientPlatform(platform string) string {
	switch platform {
	case "", "macos", "ios", "linux", "windows", "freebsd", "web", "other":
		return platform
	default:
		return "other"
	}
}

// principalOf reports who the caller is, as Rex identifies a client over
// a TCP connection it did not authenticate.
func principalOf(r *http.Request) map[string]any {
	if p, ok := r.Context().Value(principalKey{}).(map[string]any); ok {
		return p
	}
	display := "anonymous"
	p := map[string]any{"kind": "anonymous", "transport": "tcp", "display": display}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		p["display"], p["remote_addr"] = host, host
	}
	return p
}

// writeSync writes one message before the connection's writer starts.
func (c *control) writeSync(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

// writeLoop writes the queued messages until the connection ends.
func (c *control) writeLoop() {
	for {
		select {
		case m := <-c.out:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := c.ws.Write(ctx, websocket.MessageText, m.data)
			cancel()
			if m.event {
				c.mu.Lock()
				c.queued -= m.size
				c.mu.Unlock()
			}
			if err != nil {
				c.closeWith(1011, "write failed")
				return
			}
		case <-c.done:
			return
		}
	}
}

// readLoop reads requests until the connection ends, and drops the
// client when it does.
func (c *control) readLoop() {
	clean := false
	defer func() { c.g.dropControl(c, clean) }()
	for {
		typ, data, err := c.ws.Read(context.Background())
		if err != nil {
			// A close frame from the client ends its client at once; any
			// other end keeps it for a reconnect.
			clean = websocket.CloseStatus(err) >= 0 && !c.ours.Load()
			return
		}
		if typ == websocket.MessageBinary {
			c.closeWith(1003, "protocol error")
			return
		}
		if !utf8.Valid(data) {
			c.closeWith(1007, "protocol error")
			return
		}
		req, ok := parseRequest(data)
		if !ok {
			c.closeWith(1002, "protocol error")
			return
		}
		if req == nil {
			continue // an unknown message type, which newer servers add
		}
		c.mu.Lock()
		if c.pending[req.ID] {
			c.mu.Unlock()
			c.closeWith(1002, "protocol error")
			return
		}
		c.pending[req.ID] = true
		req.seq = c.seq
		c.mu.Unlock()
		if orderedMethods[req.Method] {
			// Ordered methods run one at a time on the connection, in
			// the order they arrive, so this handles them inline.
			c.handle(req)
			continue
		}
		go c.handle(req)
	}
}

// parseRequest reads one message from a client. It returns nil, true for
// a message type this server does not know, which it skips, and nil,
// false for a message that is not JSON or has no type string, which
// closes the connection.
func parseRequest(data []byte) (*request, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, false
	}
	var typ string
	if raw, ok := fields["type"]; !ok || json.Unmarshal(raw, &typ) != nil {
		return nil, false
	}
	if typ != "request" {
		return nil, true
	}
	var id, method string
	raw, ok := fields["id"]
	if !ok || json.Unmarshal(raw, &id) != nil || id == "" {
		return nil, false
	}
	raw, ok = fields["method"]
	if !ok || json.Unmarshal(raw, &method) != nil || method == "" {
		return nil, false
	}
	return &request{ID: id, Method: method, Payload: fields["payload"]}, true
}

// handle answers one request. A handler that panics answers an internal
// error, because the server must outlive it.
func (c *control) handle(req *request) {
	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
	}()
	if c.hold != nil {
		c.hold(req.ID)
	}
	defer func() {
		if p := recover(); p != nil {
			logf("%s: %v", req.Method, p)
			c.respond(req, nil, errInternal("%s failed", req.Method))
		}
	}()
	fn := methods[req.Method]
	if fn == nil {
		if strings.HasPrefix(req.Method, "com.superlogical.") {
			args, err := decodeArgs(req.Payload)
			if err != nil {
				c.respond(req, nil, err)
				return
			}
			payload, err := callBlockMethod(c, req.Method, args)
			c.respond(req, payload, err)
			return
		}
		if knownMethods()[req.Method] {
			c.respond(req, nil, unsupportedMethod(req.Method))
		} else {
			c.respond(req, nil, errMethodNotFound("%s is not a method this server has", req.Method))
		}
		return
	}
	args, err := decodeArgs(req.Payload)
	if err != nil {
		c.respond(req, nil, err)
		return
	}
	payload, err := fn(c, args)
	c.respond(req, payload, err)
}

// respond queues a request's answer.
func (c *control) respond(req *request, payload any, err *apiError) {
	if err != nil {
		c.enqueue(response{Type: "response", ID: req.ID, Seq: req.seq, Error: err})
		return
	}
	if payload == nil {
		payload = struct{}{}
	}
	res := response{Type: "response", ID: req.ID, Seq: req.seq, Payload: payload}
	data, merr := json.Marshal(res)
	if merr == nil && len(data) > maxMessage {
		res = response{Type: "response", ID: req.ID, Seq: req.seq,
			Error: errTooLarge("the result is larger than %d bytes", maxMessage)}
	}
	c.enqueue(res)
}

// enqueue queues a message, and closes the connection when the writer
// cannot keep up.
func (c *control) enqueue(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if len(data) > maxMessage {
		data, _ = json.Marshal(response{Type: "response",
			Error: errTooLarge("the result is larger than %d bytes", maxMessage)})
	}
	select {
	case c.out <- outMsg{data: data, size: len(data)}:
	case <-c.done:
	}
}

// sendChanges sends the events this connection wants, with the next seq.
func (c *control) sendChanges(chs []change) {
	for _, ch := range chs {
		if !c.wants(ch) {
			continue
		}
		var payload any
		if ch.server {
			payload = innerEvent{Type: ch.typ, Payload: ch.payload}
		} else {
			payload = sessionEvent{
				Type: "session_event",
				Payload: eventBody{
					SessionID: ch.session,
					Event:     innerEvent{Type: ch.typ, Payload: ch.payload},
				},
			}
		}
		c.mu.Lock()
		c.seq++
		msg := eventMessage{Type: "event", Seq: c.seq, Payload: payload}
		data, err := json.Marshal(msg)
		if err != nil {
			c.mu.Unlock()
			continue
		}
		if len(data) > maxMessage {
			c.mu.Unlock()
			c.closeWith(1009, "event exceeds message limit")
			return
		}
		if c.queued+len(data) > eventBacklog {
			c.mu.Unlock()
			c.closeWith(1008, "event backlog exceeded")
			return
		}
		c.queued += len(data)
		c.mu.Unlock()
		select {
		case c.out <- outMsg{data: data, event: true, size: len(data)}:
		case <-c.done:
			c.mu.Lock()
			c.queued -= len(data)
			c.mu.Unlock()
			return
		default:
			c.mu.Lock()
			c.queued -= len(data)
			c.mu.Unlock()
			c.closeWith(1008, "event backlog exceeded")
			return
		}
	}
}

// wants tells whether this connection receives a change.
func (c *control) wants(ch change) bool {
	rec := c.rec
	if rec == nil {
		return false
	}
	all, scope := rec.allScope()
	switch ch.typ {
	case "client_joined", "client_left", "client_changed", "client_performed",
		"hosts_changed", "keymap_changed":
		// The events of the whole server reach the connections that asked
		// for all of them.
		return scope == "all"
	case "tailscale_status_changed":
		// Either scope of session.attach_all or session.bootstrap gets the
		// server's Tailscale status, as the doc's event_scope says.
		return all
	case "block_event":
		// A block's own events reach a session's attached connections,
		// and the connections that asked for all.
		return scope == "all" || rec.attachedTo(ch.session)
	case "session_created":
		return all
	case "session_destroyed":
		return all || rec.attachedTo(ch.session)
	default:
		return all || rec.attachedTo(ch.session)
	}
}

// keepalive pings the client on Rex's schedule, and closes the
// connection after three unanswered pings.
func (c *control) keepalive() {
	ticker := time.NewTicker(keepaliveEvery)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), keepaliveWait)
		err := c.ws.Ping(ctx)
		late := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		switch {
		case err == nil:
			misses = 0
		case late:
			misses++
			if misses >= 3 {
				c.closeWith(1001, "keepalive timeout")
				return
			}
		default:
			return
		}
	}
}

// closeWith closes the connection with a status and a reason, once.
func (c *control) closeWith(status websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.ours.Store(true)
		close(c.done)
		c.ws.Close(status, reason)
	})
}
