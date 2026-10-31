// Package rexapi serves Rex's HTTP and WebSocket control API, as Rex.app
// and the rex command line use it, over GoRex's session server: a Rex
// session is a workspace, a window is a tab, and a block is a pane, so a
// Rex client sees and drives GoRex as it would Rex.
//
// The package lives on the side of GoRex's own protocol: it talks to the
// session server with internal/rex's client, as the app does, and keeps
// the state Rex's API needs that GoRex keeps in its layout, its commands
// and its connections.
package rexapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"sync"
	"time"

	"gorex/internal/rex"
)

// APIRevision is the revision of Rex's control API this package speaks.
const APIRevision = 7

// maxMessage is the largest message this server accepts, in bytes.
const maxMessage = 1 << 20

// Options configure a Server.
type Options struct {
	// Socket is the session server's Unix socket, or a client to use in
	// its place (Client).
	Socket string
	Client *rex.Client
	// Version is what server.version reports as rex_version, with Commit
	// and BuiltAt when the build knows them.
	Version string
	Commit  string
	BuiltAt string
}

// Server serves Rex's API over GoRex.
type Server struct {
	opt    Options
	client *rex.Client

	hello  rex.Hello
	hostID string
	instID string
	user   string
	uid    int
	gid    int

	mu          sync.Mutex
	model       *model
	infos       map[string]rex.SessionInfo
	conns       map[*control]struct{}
	clients     map[string]*clientState
	tickets     map[string]*ticket
	refreshMu   sync.Mutex
	lastRefresh time.Time

	done chan struct{}
	once sync.Once
}

// clientState is a control connection, as client.list reports it.
type clientState struct {
	ID        string
	Kind      string
	Name      string
	Version   string
	Platform  string
	Label     string
	Principal map[string]any
	Connected time.Time
	Sessions  map[string]bool
}

// ticket is a one-time pass for a data connection.
type ticket struct {
	session     string
	block       string
	subprotocol string
	expires     time.Time
}

// New connects to the session server and starts serving Rex's API.
func New(opt Options) (*Server, error) {
	c := opt.Client
	if c == nil {
		if opt.Socket == "" {
			return nil, errors.New("rexapi: no socket")
		}
		var err error
		if c, err = rex.ConnectSocket(opt.Socket); err != nil {
			return nil, err
		}
	}
	hello, err := c.Hello()
	if err != nil {
		c.Close()
		return nil, err
	}
	s := &Server{
		opt:     opt,
		client:  c,
		hello:   hello,
		conns:   map[*control]struct{}{},
		infos:   map[string]rex.SessionInfo{},
		clients: map[string]*clientState{},
		tickets: map[string]*ticket{},
		done:    make(chan struct{}),
		uid:     os.Getuid(),
		gid:     os.Getgid(),
	}
	if u, err := user.Current(); err == nil {
		s.user = u.Username
	}
	s.hostID = hostIDOf(hello)
	s.instID = instanceIDOf(hello)
	if err := c.Subscribe(); err == nil {
		go s.pump()
	}
	if err := s.refresh(); err != nil {
		log.Printf("rexapi: layout: %v", err)
	}
	return s, nil
}

// Close stops serving and closes the connection to the server.
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.done)
		s.client.Close()
	})
}

// pump reads the server's changes and tells the Rex clients of them.
func (s *Server) pump() {
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	pending := false
	for {
		select {
		case <-s.done:
			return
		case _, ok := <-s.client.Events():
			if !ok {
				return
			}
			if pending {
				continue
			}
			pending = true
			debounce.Reset(20 * time.Millisecond)
		case <-debounce.C:
			pending = false
			if err := s.refresh(); err != nil {
				log.Printf("rexapi: refresh: %v", err)
			}
		}
	}
}

// refresh reads the layout and the sessions and tells every connection
// what changed.
func (s *Server) refresh() error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	st, err := s.client.LayoutState()
	if err != nil {
		return err
	}
	infos, err := s.client.List()
	if err != nil {
		return err
	}
	m, err := buildModel(st.Revision, st.Layout, infos)
	if err != nil {
		return err
	}
	s.mu.Lock()
	old := s.model
	s.model = m
	s.infos = map[string]rex.SessionInfo{}
	for _, in := range infos {
		s.infos[in.ID] = in
	}
	s.lastRefresh = time.Now()
	conns := make([]*control, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	events := m.diff(old)
	if len(events) > 0 {
		for _, c := range conns {
			c.sendEvents(events)
		}
	}
	return nil
}

// current returns the model, read from the layout as it is now: a client
// that connects just after the app changed it sees the change.
func (s *Server) current() *model {
	if err := s.refresh(); err != nil {
		log.Printf("rexapi: refresh: %v", err)
	}
	s.mu.Lock()
	m := s.model
	s.mu.Unlock()
	return m
}

// changed refreshes the model after an operation that changed it.
func (s *Server) changed() { _ = s.refresh() }

// add and remove track a connection.
func (s *Server) add(c *control) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.clients[c.id] = c.state
	s.mu.Unlock()
}

func (s *Server) remove(c *control) {
	s.mu.Lock()
	delete(s.conns, c)
	delete(s.clients, c.id)
	s.mu.Unlock()
	for _, sid := range c.attachedList() {
		s.notifyClient(c, sid, "client_disconnected")
	}
}

// notifyClient tells a session's connections that a client came or went.
func (s *Server) notifyClient(c *control, sid, kind string) {
	ev := sessionEvent{sid, event{kind, clientEvent{
		ClientID:    c.id,
		Principal:   c.principal(),
		ConnectedAt: c.connectedAt,
	}}}
	s.mu.Lock()
	conns := make([]*control, 0, len(s.conns))
	for o := range s.conns {
		conns = append(conns, o)
	}
	s.mu.Unlock()
	for _, o := range conns {
		if o == c {
			continue
		}
		if o.wants(sid) {
			o.sendEvents([]sessionEvent{ev})
		}
	}
}

// hostID names this machine and account in block and host IDs.
func hostIDOf(h rex.Hello) string {
	name := h.Host.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	return uuidLike("host", name+"/"+h.Host.User)
}

// instanceIDOf names this run of the server.
func instanceIDOf(h rex.Hello) string {
	return uuidLike("instance", h.Started.String()+"/"+h.Host.Name)
}

// uuidLike hashes a seed into the shape of a UUID.
func uuidLike(kind, seed string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + seed))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = b[6]&0x0f | 0x80
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newID returns a random id with a prefix, shaped as Rex's are.
func newID(prefix string) string { return prefix + ":" + uuidLike(prefix, randomHex(16)) }

// randomToken returns a reconnect token.
func randomToken() string { return randomHex(32) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (8 * (i % 8)))
		}
	}
	return hex.EncodeToString(b)
}

// jsonOf marshals v, dropping errors that cannot happen for our shapes.
func jsonOf(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
