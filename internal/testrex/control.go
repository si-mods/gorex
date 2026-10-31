package testrex

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// clientHello is what a control connection sends first.
type clientHello struct {
	Type           string `json:"type"`
	ReconnectToken string `json:"reconnect_token,omitempty"`
	Client         *struct {
		Kind     string `json:"kind,omitempty"`
		Platform string `json:"platform,omitempty"`
		Name     string `json:"name,omitempty"`
		Version  string `json:"version,omitempty"`
		Label    string `json:"label,omitempty"`
	} `json:"client,omitempty"`
	APIRevision    int `json:"api_revision,omitempty"`
	APIMinRevision int `json:"api_min_revision,omitempty"`
}

// envelope is one message from a client.
type envelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// welcome is the server's first message.
type welcome struct {
	Type         string    `json:"type"`
	Protocol     string    `json:"protocol"`
	Limits       limits    `json:"limits"`
	Client       *clientID `json:"client"`
	APIRevision  int       `json:"api_revision"`
	APIServerRev int       `json:"api_server_revision"`
}

type limits struct {
	MaxMessageBytes int `json:"max_message_bytes"`
}

type clientID struct {
	ID             string `json:"id"`
	ReconnectToken string `json:"reconnect_token"`
	Resumed        bool   `json:"resumed,omitempty"`
}

// response is an answer to a request.
type response struct {
	Type    string    `json:"type"`
	ID      string    `json:"id"`
	Seq     uint64    `json:"seq"`
	Payload any       `json:"payload,omitempty"`
	Error   *apiError `json:"error,omitempty"`
}

// apiError is a failed request, in Rex's shape.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

// errorf makes an apiError.
func errorf(code string, status int, format string, args ...any) *apiError {
	return &apiError{Code: code, Message: fmt.Sprintf(format, args...), Status: status}
}

var (
	errBadRequest = func(format string, args ...any) *apiError {
		return errorf("bad_request", 400, format, args...)
	}
	errNotFound = func(format string, args ...any) *apiError {
		return errorf("not_found", 404, format, args...)
	}
	errSessionNotFound = func(format string, args ...any) *apiError {
		return errorf("session_not_found", 404, format, args...)
	}
	errConflict = func(format string, args ...any) *apiError {
		return errorf("conflict", 409, format, args...)
	}
	errNotSupported = func(format string, args ...any) *apiError {
		return errorf("not_supported", 400, format, args...)
	}
	errInternal = func(format string, args ...any) *apiError {
		return errorf("internal", 500, format, args...)
	}
	errMethodNotFound = func(format string, args ...any) *apiError {
		return errorf("method_not_found", 404, format, args...)
	}
)

// control is one control connection.
type control struct {
	s           *Server
	ws          *websocket.Conn
	id          string
	state       *clientState
	principalID map[string]any
	connectedAt time.Time

	mu       sync.Mutex
	attached map[string]bool
	all      bool
	scope    string
	seq      uint64
	pending  map[string]bool
	wmu      sync.Mutex
	ordered  sync.Mutex
}

// serveControl runs a control connection until it closes.
func (s *Server) serveControl(ws *websocket.Conn) {
	ws.SetReadLimit(maxMessage)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, data, err := ws.Read(ctx)
	cancel()
	if err != nil {
		ws.Close(websocket.StatusPolicyViolation, "no hello")
		return
	}
	var h clientHello
	if json.Unmarshal(data, &h) != nil || h.Type != "hello" {
		ws.Close(websocket.StatusPolicyViolation, "hello expected")
		return
	}
	if h.APIMinRevision > APIRevision {
		ws.Close(websocket.StatusPolicyViolation, "the client needs a newer API than this server speaks")
		return
	}
	c := &control{
		s:           s,
		ws:          ws,
		id:          newID("client"),
		connectedAt: time.Now(),
		attached:    map[string]bool{},
		pending:     map[string]bool{},
	}
	kind, name, version, platform, label := "other", "", "", "", ""
	if h.Client != nil {
		if h.Client.Kind != "" {
			kind = h.Client.Kind
		}
		name, version, platform, label = h.Client.Name, h.Client.Version, h.Client.Platform, h.Client.Label
	}
	c.principalID = c.principal()
	c.state = &clientState{
		ID: c.id, Kind: kind, Name: name, Version: version, Platform: platform,
		Label: label, Principal: c.principalID, Connected: c.connectedAt,
	}
	w := welcome{
		Type:         "welcome",
		Protocol:     "rex.control.v1",
		Limits:       limits{MaxMessageBytes: maxMessage},
		Client:       &clientID{ID: c.id, ReconnectToken: randomToken()},
		APIRevision:  APIRevision,
		APIServerRev: APIRevision,
	}
	if err := c.write(w); err != nil {
		ws.Close(websocket.StatusInternalError, "welcome")
		return
	}
	if _, err := s.ensure(); err != nil {
		ws.Close(websocket.StatusInternalError, "no server")
		return
	}
	defer s.release()
	s.add(c)
	defer func() {
		s.remove(c)
		ws.Close(websocket.StatusNormalClosure, "")
	}()
	for {
		_, data, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var msg envelope
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Type != "request" {
			continue // unknown types are ignored, as older servers do
		}
		if msg.ID == "" {
			ws.Close(websocket.StatusPolicyViolation, "request without id")
			return
		}
		c.mu.Lock()
		if c.pending[msg.ID] {
			c.mu.Unlock()
			ws.Close(websocket.StatusPolicyViolation, "duplicate request id")
			return
		}
		c.pending[msg.ID] = true
		c.mu.Unlock()
		go c.handle(msg)
	}
}

// handle answers one request. A handler that panics answers an internal
// error: the session server must outlive it.
func (c *control) handle(msg envelope) {
	defer func() {
		c.mu.Lock()
		delete(c.pending, msg.ID)
		c.mu.Unlock()
	}()
	defer func() {
		if p := recover(); p != nil {
			log.Printf("testrex: %s: %v", msg.Method, p)
			c.respond(msg.ID, nil, errInternal("%s failed", msg.Method))
		}
	}()
	m := methods[msg.Method]
	if m == nil {
		c.respond(msg.ID, nil, errMethodNotFound("%s is not a method this server has", msg.Method))
		return
	}
	if m.ordered {
		c.ordered.Lock()
		defer c.ordered.Unlock()
	}
	payload, apiErr := m.handler(c, msg.Payload)
	c.respond(msg.ID, payload, apiErr)
}

// respond writes a request's answer, with the next sequence number.
func (c *control) respond(id string, payload any, apiErr *apiError) {
	seq := c.nextSeq()
	if apiErr != nil {
		c.write(response{Type: "response", ID: id, Seq: seq, Error: apiErr})
		return
	}
	c.write(response{Type: "response", ID: id, Seq: seq, Payload: payload})
}

// nextSeq returns the sequence number of the next message.
func (c *control) nextSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}

// write sends one JSON message.
func (c *control) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

// wants tells whether this connection hears of a session's changes. A
// connection told to hear the server's own events with event_scope
// "session" hears only those of the sessions it attached to.
func (c *control) wants(sid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attached[sid] || c.all && c.scope != "session"
}

// attachedList returns the sessions the connection is attached to.
func (c *control) attachedList() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.attached))
	for sid := range c.attached {
		out = append(out, sid)
	}
	return out
}

// sendEvents sends the events this connection wants, in order.
func (c *control) sendEvents(events []sessionEvent) {
	for _, ev := range events {
		if !c.wants(ev.Session) {
			continue
		}
		seq := c.nextSeq()
		inner := map[string]any{
			"type":    "session_event",
			"payload": map[string]any{"session_id": ev.Session, "event": ev.Event},
		}
		if err := c.write(map[string]any{"type": "event", "seq": seq, "payload": inner}); err != nil {
			return
		}
	}
}

// principal reports who the caller is, as Rex identifies a local caller.
func (c *control) principal() map[string]any {
	p := map[string]any{
		"kind":      "peer",
		"transport": "unix",
		"display":   fmt.Sprintf("uid %d", c.s.uid),
		"uid":       c.s.uid,
		"gid":       c.s.gid,
	}
	return p
}

// clientEvent is a client joining or leaving a session.
type clientEvent struct {
	ClientID    string         `json:"client_id"`
	Principal   map[string]any `json:"principal"`
	ConnectedAt time.Time      `json:"connected_at"`
}
