package rexapi

import (
	"log"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"sync"
	"time"

	"gorex/internal/rex"
)

// Intervals of the gateway. They are variables so that tests can shorten
// them.
var (
	// pollEvery is how often the gateway reads the layout from a server
	// that does not push its changes.
	pollEvery = 500 * time.Millisecond
	// safetyEvery is how often it reads the layout from a server that
	// does, for what the server does not tell.
	safetyEvery = 30 * time.Second
	// settle is how long a burst of changes waits before one read.
	settle = 20 * time.Millisecond
	// helloWait is how long a control or data connection may take to
	// send its hello.
	helloWait = 5 * time.Second
	// keepaliveEvery is how often the server pings a connection, and
	// keepaliveWait how long it waits for each answer.
	keepaliveEvery = 120 * time.Second
	keepaliveWait  = 60 * time.Second
	// reconnectWait is how long a client's state is kept for a
	// reconnect, and the same wait for a data stream's resume.
	reconnectWait = 60 * time.Second
	// renewEvery is how often the gateway renews the size locks it
	// holds, as internal/control renews the tabs rootshell holds.
	renewEvery = 5 * time.Second
	// commandWait is the longest the gateway waits to see a layout
	// change, a second more than the server waits for the app: the
	// app's answer comes once it saved the layout.
	commandWait = rex.CommandTimeout + time.Second
	// ticketWait is how long a data connection ticket works.
	ticketWait = 60 * time.Second
	// resumeBytes is how many stream bytes a waiting data stream keeps
	// for a resume.
	resumeBytes = 8 << 20
)

// Access decides whether a request may use the API. Rex answers requests
// it refuses with status 403 and a JSON body, such as {"error": "..."}.
type Access interface {
	// Allow returns nil to let the request in, or an error whose text
	// answers it with status 403.
	Allow(r *http.Request) error
}

// AccessFunc is an Access made from a function.
type AccessFunc func(r *http.Request) error

func (f AccessFunc) Allow(r *http.Request) error { return f(r) }

// Options configure a Gateway.
type Options struct {
	// Client is the connection to the GoRex server, which the gateway
	// keeps for as long as it serves. Required.
	Client *rex.Client
	// Access decides who may use the API. A nil Access allows everyone.
	Access Access
	// TailnetDir, when set, is where built-in Tailscale keeps its device
	// and settings: the gateway can then serve the API on the tailnet,
	// and turns it on again if it was on when it last stopped. Without
	// it, the server.tailscale methods fail with not_supported.
	TailnetDir string
}

// Gateway serves Rex's API over one GoRex server.
type Gateway struct {
	opt        Options
	started    time.Time
	hello      rex.Hello
	host       string
	user       string
	instanceID string
	// helloWait is how long a connection may take to send its hello;
	// tests shorten it on a gateway of their own.
	helloWait time.Duration
	// tailnet is built-in Tailscale, nil when the gateway cannot run it.
	tailnet *tailnet
	// reconnectWait is how long a dropped client, or a data stream, waits
	// to be resumed; tests shorten it before making a gateway of their own.
	reconnectWait time.Duration

	// refreshMu serializes reading the layout and installing the model,
	// so that changes reach connections in the order they were read.
	refreshMu sync.Mutex
	mapper    *mapper

	mu      sync.Mutex
	model   *model
	conns   map[*control]struct{}
	clients map[string]*clientRecord
	tokens  map[string]*clientRecord
	nextSeq uint64
	tickets map[string]*ticket
	streams map[string]*stream
	stokens map[string]*stream

	// ownMu guards the ownership of blocks: who owns each, who is in
	// line, the size each client asked for, and the size locks the
	// gateway holds (locks, by GoRex session). The server calls a lock
	// makes run under it, one at a time.
	ownMu  sync.Mutex
	owners map[string]*blockOwner
	locks  map[string]string

	// blobs are the results too large for one reply, which blob_read
	// reads, and blobsNext their count.
	blobs     map[int]blob
	blobsNext int

	// themes are the default colors a client set for a block.
	themes map[string]theme

	// generation counts the loads the configuration file had, which
	// server.config_reload makes; keymapHash names the file's contents.
	generation int
	keymapHash string
	// canLock tells whether the server keeps the gateway's connection
	// alive, which the size locks need.
	canLock bool

	done      chan struct{}
	closing   bool
	closeOnce sync.Once
	wg        sync.WaitGroup
	// hold, when set by a test, runs before a control request is
	// handled, so that a test can keep an id pending.
	hold func(id string)
}

// New makes a gateway over the server of opt.Client.
func New(opt Options) (*Gateway, error) {
	if opt.Client == nil {
		return nil, errNoClient
	}
	hello, err := opt.Client.Hello()
	if err != nil {
		return nil, err
	}
	name := hello.Host.Name
	if name == "" {
		name, _ = os.Hostname()
	}
	g := &Gateway{
		opt:           opt,
		started:       time.Now(),
		hello:         hello,
		host:          hostID(name, hello.Host.User),
		user:          currentUser(),
		instanceID:    randomUUID(),
		helloWait:     helloWait,
		reconnectWait: reconnectWait,
		mapper:        newMapper(),
		conns:         map[*control]struct{}{},
		clients:       map[string]*clientRecord{},
		tokens:        map[string]*clientRecord{},
		tickets:       map[string]*ticket{},
		streams:       map[string]*stream{},
		stokens:       map[string]*stream{},
		owners:        map[string]*blockOwner{},
		locks:         map[string]string{},
		blobs:         map[int]blob{},
		themes:        map[string]theme{},
		generation:    1,
		canLock:       hello.Keepalive > 0 && hello.Conn != "",
		done:          make(chan struct{}),
	}
	if opt.TailnetDir != "" {
		g.tailnet = newTailnet(g, opt.TailnetDir, newTsnetNode)
		g.tailnet.serve = g.serveOn
	}
	g.wg.Add(1)
	go g.pump()
	g.wg.Add(1)
	go g.renewLocks()
	g.wg.Add(1)
	go g.watchBlocks()
	return g, nil
}

var errNoClient = &optionsError{"rexapi: no connection to the GoRex server"}

type optionsError struct{ text string }

func (e *optionsError) Error() string { return e.text }

// currentUser returns the account the gateway runs as, or "".
func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// Close stops the gateway: it closes every connection and its own to the
// GoRex server.
func (g *Gateway) Close() error {
	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.closing = true
		g.mu.Unlock()
		close(g.done)
		g.mu.Lock()
		conns := make([]*control, 0, len(g.conns))
		for c := range g.conns {
			conns = append(conns, c)
		}
		streams := make([]*stream, 0, len(g.streams))
		for _, s := range g.streams {
			streams = append(streams, s)
		}
		g.mu.Unlock()
		if g.tailnet != nil {
			g.tailnet.close()
		}
		for _, c := range conns {
			c.closeWith(1001, "data provider canceled")
		}
		for _, s := range streams {
			s.closeWith(1001, "data provider canceled")
		}
		g.opt.Client.Close()
		g.wg.Wait()
	})
	return nil
}

// pump reads the layout as the server tells of changes, or polls it from
// a server that tells of none.
func (g *Gateway) pump() {
	defer g.wg.Done()
	subscribed := g.opt.Client.Subscribe() == nil
	every := pollEvery
	if subscribed {
		every = safetyEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	var burst *time.Timer
	var due <-chan time.Time
	for {
		select {
		case <-g.done:
			return
		case _, ok := <-g.opt.Client.Events():
			if !ok {
				return
			}
			if subscribed && burst == nil {
				burst = time.NewTimer(settle)
				due = burst.C
			}
		case <-due:
			burst = nil
			g.refresh()
		case <-ticker.C:
			g.refresh()
		case <-g.opt.Client.Closed():
			return
		}
	}
}

// current reads the layout and returns the model as it is now.
func (g *Gateway) current() (*model, error) {
	return g.refresh()
}

// refresh reads the layout, builds the model, tells the connections what
// changed, and returns the new model. A read that fails leaves the last
// model in place.
func (g *Gateway) refresh() (*model, error) {
	g.refreshMu.Lock()
	defer g.refreshMu.Unlock()
	raw, err := g.opt.Client.Layout()
	if err != nil {
		return g.modelNow(), err
	}
	m, err := g.mapper.build(raw, g.host, g.ownersNow())
	if err != nil {
		return g.modelNow(), err
	}
	g.pruneOwners(m)
	g.pruneAttachments(m)
	g.mu.Lock()
	old := g.model
	g.model = m
	conns := make([]*control, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	if old != nil {
		events := diffModels(old, m)
		for _, c := range conns {
			c.sendChanges(events)
		}
	}
	return m, nil
}

// pruneAttachments forgets the sessions a client attached to that the
// layout no longer has.
func (g *Gateway) pruneAttachments(m *model) {
	g.mu.Lock()
	recs := make([]*clientRecord, 0, len(g.clients))
	for _, r := range g.clients {
		recs = append(recs, r)
	}
	g.mu.Unlock()
	for _, r := range recs {
		for _, sid := range r.attachedList() {
			if m.byID[sid] == nil {
				r.detach(sid)
			}
		}
	}
}

// renewLocks renews the size locks the gateway holds, every renewEvery,
// for as long as it serves.
func (g *Gateway) renewLocks() {
	defer g.wg.Done()
	t := time.NewTicker(renewEvery)
	defer t.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-t.C:
			g.renewOwned()
		}
	}
}

// modelNow returns the last model, or an empty one.
func (g *Gateway) modelNow() *model {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.model != nil {
		return g.model
	}
	return &model{byID: map[string]*mSession{}, blocks: map[string]*mBlock{}}
}

// clientRecord is one Rex client across its connections: it keeps its ID
// and attachments when a connection drops, for a reconnect.
type clientRecord struct {
	id          string
	info        *clientInfo
	principal   map[string]any
	connectedAt time.Time
	order       uint64

	mu       sync.Mutex
	token    string
	conn     *control
	attached map[string]bool
	all      bool
	scope    string
	lost     bool
	lostAt   time.Time
	timer    *time.Timer
	gone     bool
	// owned are the blocks the client owned when its connection dropped,
	// which it gets back when it reconnects, if no one took them since.
	owned map[string]bool
}

// rememberOwned notes a block the client owned as its connection went.
func (r *clientRecord) rememberOwned(block string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owned == nil {
		r.owned = map[string]bool{}
	}
	r.owned[block] = true
}

// ownedList returns the blocks the client owned before it dropped.
func (r *clientRecord) ownedList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.owned))
	for id := range r.owned {
		out = append(out, id)
	}
	return out
}

// clearOwned forgets the blocks the client owned before it dropped.
func (r *clientRecord) clearOwned() {
	r.mu.Lock()
	r.owned = nil
	r.mu.Unlock()
}

// clientInfo is what a client says about itself.
type clientInfo struct {
	Kind     string
	Platform string
	Name     string
	Version  string
	Label    string
}

// json returns the client's info, leaving out what it did not say.
func (i *clientInfo) json() map[string]any {
	if i == nil {
		return nil
	}
	m := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	set("kind", i.Kind)
	set("platform", i.Platform)
	set("name", i.Name)
	set("version", i.Version)
	set("label", i.Label)
	return m
}

// connectedAtString writes a time as Rex does.
func connectedAtString(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// addControl registers a connection's client. A welcome's token is made
// here, and the record is what a reconnect continues.
func (g *Gateway) addControl(c *control, info *clientInfo, principal map[string]any) *clientRecord {
	rec := &clientRecord{
		id:          newID("client"),
		info:        info,
		principal:   principal,
		connectedAt: time.Now(),
		attached:    map[string]bool{},
		conn:        c,
	}
	g.mu.Lock()
	g.nextSeq++
	rec.order = g.nextSeq
	g.clients[rec.id] = rec
	g.mu.Unlock()
	g.rotateToken(rec)
	g.emitClientEvent(rec, "", "client_joined")
	return rec
}

// resumeControl continues the client of token, if it is waiting to
// reconnect, and reports whether it did.
func (g *Gateway) resumeControl(c *control, token string) (*clientRecord, bool) {
	g.mu.Lock()
	rec := g.tokens[token]
	g.mu.Unlock()
	if rec == nil {
		return nil, false
	}
	rec.mu.Lock()
	if rec.gone || rec.conn != nil {
		// A connected client's token is not a way to take its client
		// over, and a token whose client is gone is unknown.
		rec.mu.Unlock()
		return nil, false
	}
	if rec.timer != nil {
		rec.timer.Stop()
		rec.timer = nil
	}
	rec.conn = c
	rec.lost = false
	rec.lostAt = time.Time{}
	rec.mu.Unlock()
	g.mu.Lock()
	if g.tokens[token] == rec {
		delete(g.tokens, token)
	}
	g.mu.Unlock()
	g.rotateToken(rec)
	g.emitOwnerChanges(g.restoreBlocks(rec))
	g.emitConnectionState(rec, rec.attachedList(), "connected", time.Time{})
	g.refresh()
	return rec, true
}

// rotateToken gives the client a new reconnect token, and makes the old
// one stop working.
func (g *Gateway) rotateToken(rec *clientRecord) string {
	token := randomToken()
	rec.mu.Lock()
	old := rec.token
	rec.token = token
	rec.mu.Unlock()
	g.mu.Lock()
	if old != "" {
		delete(g.tokens, old)
	}
	g.tokens[token] = rec
	g.mu.Unlock()
	return token
}

// dropControl ends a connection. A connection that closed with a close
// frame ends its client at once; one that dropped keeps it for a
// reconnect when it had something to keep.
func (g *Gateway) dropControl(c *control, clean bool) {
	g.mu.Lock()
	delete(g.conns, c)
	g.mu.Unlock()
	rec := c.rec
	if rec == nil {
		return
	}
	rec.mu.Lock()
	if rec.conn != c {
		rec.mu.Unlock()
		return
	}
	rec.conn = nil
	attached := rec.attachedListLocked()
	g.mu.Lock()
	closing := g.closing
	g.mu.Unlock()
	keep := !clean && !closing && (len(attached) > 0 || rec.all)
	if keep {
		lostAt := time.Now()
		rec.lost = true
		rec.lostAt = lostAt
		rec.timer = time.AfterFunc(g.reconnectWait, func() { g.expireClient(rec) })
		rec.mu.Unlock()
		g.emitOwnerChanges(g.releaseBlocks(rec, "lost"))
		g.emitConnectionState(rec, attached, "lost", lostAt)
		g.refresh()
		return
	}
	rec.gone = true
	rec.mu.Unlock()
	g.mu.Lock()
	delete(g.clients, rec.id)
	if rec.token != "" {
		delete(g.tokens, rec.token)
	}
	g.mu.Unlock()
	g.emitOwnerChanges(g.releaseBlocks(rec, "disconnect"))
	for _, sid := range attached {
		g.emitClientEvent(rec, sid, "client_disconnected")
	}
	g.emitClientEvent(rec, "", "client_left")
	g.cancelStreams(rec.id)
	g.refresh()
}

// expireClient drops a client that no connection came back for.
func (g *Gateway) expireClient(rec *clientRecord) {
	rec.mu.Lock()
	if rec.gone || rec.conn != nil {
		rec.mu.Unlock()
		return
	}
	rec.gone = true
	attached := rec.attachedListLocked()
	rec.mu.Unlock()
	g.mu.Lock()
	delete(g.clients, rec.id)
	if rec.token != "" {
		delete(g.tokens, rec.token)
	}
	g.mu.Unlock()
	g.emitOwnerChanges(g.releaseBlocks(rec, "disconnect"))
	for _, sid := range attached {
		g.emitClientEvent(rec, sid, "client_disconnected")
	}
	g.emitClientEvent(rec, "", "client_left")
	g.cancelStreams(rec.id)
	g.refresh()
}

// attachedListLocked returns the sessions a client is attached to. Its
// mu is held.
func (r *clientRecord) attachedListLocked() []string {
	out := make([]string, 0, len(r.attached))
	for sid := range r.attached {
		out = append(out, sid)
	}
	return out
}

// attachedList returns the sessions a client is attached to.
func (r *clientRecord) attachedList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attachedListLocked()
}

// attachedTo tells whether the client attached to the session with
// session.attach or session.bootstrap.
func (r *clientRecord) attachedTo(sid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attached[sid]
}

// attach adds a session and reports whether it was new.
func (r *clientRecord) attach(sid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attached[sid] {
		return false
	}
	r.attached[sid] = true
	return true
}

// detach removes a session and reports whether it was there.
func (r *clientRecord) detach(sid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.attached[sid] {
		return false
	}
	delete(r.attached, sid)
	return true
}

// allScope returns whether the client asked for every session's events,
// and what its event_scope is.
func (r *clientRecord) allScope() (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.all, r.scope
}

// setAll makes the client hear of every session's events.
func (r *clientRecord) setAll(scope string) {
	r.mu.Lock()
	r.all, r.scope = true, scope
	r.mu.Unlock()
}

// state returns how client.list reports the client, and whether it is
// still connected.
func (r *clientRecord) state() (lost bool, lostAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lost, r.lostAt
}

// emitClientEvent tells a session's connections that a client came or
// went.
func (g *Gateway) emitClientEvent(rec *clientRecord, sid, typ string) {
	payload := any(clientIDPayload{ClientID: rec.id})
	switch typ {
	case "client_connected":
		payload = map[string]any{
			"client_id":    rec.id,
			"principal":    rec.principal,
			"connected_at": connectedAtString(rec.connectedAt),
		}
	case "client_joined":
		g.emitServerEvent(typ, map[string]any{"client": rec.view()})
		return
	case "client_left":
		g.emitServerEvent(typ, map[string]any{"client_id": rec.id})
		return
	}
	g.emitSession(sid, typ, payload)
}

// emitServerEvent sends a server event to the connections that asked for
// every event.
func (g *Gateway) emitServerEvent(typ string, payload any) {
	g.mu.Lock()
	conns := make([]*control, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		c.sendChanges([]change{{server: true, typ: typ, payload: payload}})
	}
}

// emitConnectionState tells the sessions a client is attached to that
// its connection dropped, or came back.
func (g *Gateway) emitConnectionState(rec *clientRecord, sids []string, state string, at time.Time) {
	for _, sid := range sids {
		payload := map[string]any{
			"client_id":        rec.id,
			"principal":        rec.principal,
			"connection_state": state,
		}
		if state == "lost" {
			payload["connection_lost_at"] = connectedAtString(at)
		}
		g.emitSession(sid, "client_connection_changed", payload)
	}
}

// emitOwnerChanges tells the sessions that a block's owner changed.
func (g *Gateway) emitOwnerChanges(changes []ownerChange) {
	if len(changes) == 0 {
		return
	}
	m := g.modelNow()
	for _, ch := range changes {
		b := m.blockByID(ch.block)
		if b == nil {
			continue
		}
		payload := map[string]any{"block_id": ch.block}
		if ch.owner != "" {
			payload["owner"] = ch.owner
		}
		if ch.previous != "" {
			payload["previous"] = ch.previous
		}
		if ch.reason != "" {
			payload["reason"] = ch.reason
		}
		g.emitSession(b.session, "block_owner_changed", payload)
	}
}

// emitSession sends a session event to the connections that receive the
// session's events.
func (g *Gateway) emitSession(sid, typ string, payload any) {
	g.mu.Lock()
	conns := make([]*control, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		c.sendChanges([]change{{session: sid, typ: typ, payload: payload}})
	}
}

// ticket is a one-time pass for a data connection.
type ticket struct {
	record      string
	session     string
	block       string
	sid         string
	dataRef     string
	subprotocol string
	expires     time.Time
}

// issueTicket makes a ticket for a data connection.
func (g *Gateway) issueTicket(t *ticket) string {
	id := base64url(randomBytes(16))
	g.mu.Lock()
	now := time.Now()
	for k, old := range g.tickets {
		if now.After(old.expires) {
			delete(g.tickets, k)
		}
	}
	g.tickets[id] = t
	g.mu.Unlock()
	return id
}

// takeTicket spends a ticket, whether or not the connection then uses
// it: a HELLO uses up its ticket even when the server refuses it.
func (g *Gateway) takeTicket(id, subprotocol string) (*ticket, byte) {
	g.mu.Lock()
	t := g.tickets[id]
	if t != nil {
		delete(g.tickets, id)
	}
	g.mu.Unlock()
	if t == nil || time.Now().After(t.expires) {
		return nil, 0x01
	}
	if t.subprotocol != "" && t.subprotocol != subprotocol {
		return nil, 0x07
	}
	if t.dataRef != dataTerminal {
		return t, 0x03
	}
	return t, 0
}

// cancelStreams ends the data streams a client opened.
func (g *Gateway) cancelStreams(record string) {
	g.mu.Lock()
	var streams []*stream
	for _, s := range g.streams {
		if s.record == record {
			streams = append(streams, s)
		}
	}
	g.mu.Unlock()
	for _, s := range streams {
		s.closeWith(1001, "data provider canceled")
	}
}

// removeStream forgets a stream.
func (g *Gateway) removeStream(s *stream) {
	g.mu.Lock()
	if g.streams[s.key] == s {
		delete(g.streams, s.key)
	}
	if s.token != "" && g.stokens[s.token] == s {
		delete(g.stokens, s.token)
	}
	g.mu.Unlock()
}

// rotateStreamToken gives a stream a new reconnect token.
func (g *Gateway) rotateStreamToken(s *stream) []byte {
	token := randomBytes(16)
	raw := string(token)
	g.mu.Lock()
	if s.token != "" {
		delete(g.stokens, s.token)
	}
	s.token = raw
	g.stokens[raw] = s
	g.mu.Unlock()
	return token
}

// streamFor returns the stream a reconnect token names.
func (g *Gateway) streamFor(token string) *stream {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stokens[token]
}

// platform is what server.version reports as the server's platform.
func platform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// logf logs a message from the gateway.
func logf(format string, args ...any) { log.Printf("rexapi: "+format, args...) }
