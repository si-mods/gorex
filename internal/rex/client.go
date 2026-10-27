package rex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Client is a control connection to the server.
type Client struct {
	conn net.Conn
	// dial connects again to the server, for a stream; closer, if any,
	// closes what dial dials through.
	dial   func() (net.Conn, error)
	closer io.Closer

	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan Response
	err     error
	// refused is what a server that does not allow this machine said, as
	// it closed the connection.
	refused string
	closed  chan struct{}

	// keepalive asks the server to keep the connection alive (Hello);
	// connID is its id there once it does, and attached the connections
	// attached through it, which close with it, as the server closes them.
	keepalive bool
	connID    string
	attached  map[net.Conn]struct{}
	lost      bool // the attached were closed
	pinging   sync.Once
}

// callTimeout is how long a request waits for its answer.
var callTimeout = newDuration(15 * time.Second)

// Connect connects to the server, starting one in the background when
// none runs.
func Connect() (*Client, error) {
	conn, err := dialSocket()
	if err != nil {
		if err := Spawn(); err != nil {
			return nil, fmt.Errorf("rex: starting the server: %w", err)
		}
		if conn, err = dialUntil(time.Now().Add(5 * time.Second)); err != nil {
			return nil, fmt.Errorf("rex: connecting to the server: %w", err)
		}
	}
	return newClient(conn, dialSocket), nil
}

func dialSocket() (net.Conn, error) { return net.Dial("unix", SocketPath()) }

// ConnectSocket connects to the server listening on the Unix socket at
// path, starting none.
func ConnectSocket(path string) (*Client, error) {
	dial := func() (net.Conn, error) { return net.Dial("unix", path) }
	conn, err := dial()
	if err != nil {
		return nil, err
	}
	return newClient(conn, dial), nil
}

// ConnectSocketKeepalive is ConnectSocket for a socket that leads to a
// remote server, as a proxy's: the connection is kept alive as
// ConnectRemote's is.
func ConnectSocketKeepalive(path string) (*Client, error) {
	c, err := ConnectSocket(path)
	if c != nil {
		c.keepalive = true
	}
	return c, err
}

// dialUntil connects to the server, trying again until deadline.
func dialUntil(deadline time.Time) (net.Conn, error) {
	for {
		conn, err := dialSocket()
		if err == nil || time.Now().After(deadline) {
			return conn, err
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func newClient(conn net.Conn, dial func() (net.Conn, error)) *Client {
	c := &Client{conn: conn, dial: dial, pending: map[int64]chan Response{}, closed: make(chan struct{})}
	go c.read()
	return c
}

// Executable returns the path of the running executable and when it was
// modified last.
func Executable() (string, time.Time) {
	exe, err := os.Executable()
	if err != nil {
		return "", time.Time{}
	}
	return executable(exe)
}

// executable returns the path of the executable exe, its links
// followed, and when it was modified last.
func executable(exe string) (string, time.Time) {
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	st, err := os.Stat(exe)
	if err != nil {
		return exe, time.Time{}
	}
	return exe, st.ModTime()
}

// Restart shuts the server down, ending its sessions, waits for it to
// exit, and connects to a new one.
func (c *Client) Restart(pid int) (*Client, error) {
	c.Shutdown()
	c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for pid > 0 && processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return Connect()
}

// Upgrade has the server run exe in its place, keeping its sessions,
// and connects to it again once it runs it (Hello.Upgrade tells a server
// that can). On an error from the server, c stays connected to it.
func (c *Client) Upgrade(exe string) (*Client, error) {
	want, at := executable(exe)
	err := c.call(Request{Op: "upgrade", Exe: exe}, nil)
	c.mu.Lock()
	lost := c.err != nil // the connection closed as the server ran exe
	c.mu.Unlock()
	if !lost {
		if err == nil {
			err = errors.New("rex: the server did not upgrade")
		}
		return nil, err
	}
	c.Close()
	// Not Connect, which would start a server: the listener is the old
	// server's, and the new executable answers what it queued.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := dialUntil(deadline)
		if err != nil {
			break
		}
		nc := newClient(conn, dialSocket)
		for time.Now().Before(deadline) {
			h, err := nc.Hello()
			if err != nil {
				break // lost: dial again
			}
			if h.Exe == want && h.ExeTime.Equal(at) {
				return nc, nil
			}
			time.Sleep(30 * time.Millisecond)
		}
		nc.Close()
	}
	return nil, fmt.Errorf("rex: the server did not come back as %s", exe)
}

// Closed is closed once the connection to the server is lost.
func (c *Client) Closed() <-chan struct{} { return c.closed }

func (c *Client) read() {
	r := bufio.NewReaderSize(c.conn, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			c.closeAttached()
			c.mu.Lock()
			c.err = err
			msg := "connection to the server lost"
			if c.refused != "" {
				msg = c.refused
			}
			for id, ch := range c.pending {
				ch <- Response{ID: id, Error: msg}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			close(c.closed)
			return
		}
		var res Response
		if json.Unmarshal(line, &res) != nil {
			continue
		}
		c.mu.Lock()
		if res.ID == 0 && res.Error != "" {
			c.refused = res.Error // unprompted: the server refuses
		}
		ch := c.pending[res.ID]
		delete(c.pending, res.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
}

// closeAttached closes the connections attached through a connection
// kept alive, as it is lost: the server closes them too.
func (c *Client) closeAttached() {
	c.mu.Lock()
	conns := c.attached
	c.attached, c.lost = nil, true
	c.mu.Unlock()
	for conn := range conns {
		conn.Close()
	}
}

// track keeps conn, to attach through the connection kept alive, if it
// is, and returns that connection's id; false once it is lost.
func (c *Client) track(conn net.Conn) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connID == "" {
		return "", true
	}
	if c.lost || c.err != nil {
		return "", false
	}
	if c.attached == nil {
		c.attached = map[net.Conn]struct{}{}
	}
	c.attached[conn] = struct{}{}
	return c.connID, true
}

func (c *Client) untrack(conn net.Conn) {
	c.mu.Lock()
	delete(c.attached, conn)
	c.mu.Unlock()
}

// ping asks the server every interval, so that it keeps the connection,
// and drops the connection once it does not answer in time: the server,
// or the network to it, is gone.
func (c *Client) ping(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
		}
		if err := c.callWithin(Request{Op: "ping"}, nil, every); err != nil {
			c.conn.Close()
			c.closeAttached()
			return
		}
	}
}

// call sends a request and decodes the response's data into out.
func (c *Client) call(req Request, out any) error {
	return c.callWithin(req, out, callTimeout.get())
}

// callWithin is call, waiting at most timeout for the answer.
func (c *Client) callWithin(req Request, out any, timeout time.Duration) error {
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.err != nil {
		refused := c.refused
		c.mu.Unlock()
		if refused != "" {
			return errors.New(refused)
		}
		return errors.New("rex: connection to the server lost")
	}
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(req)
	c.wmu.Lock()
	_, err := c.conn.Write(append(b, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return err
	}
	var res Response
	select {
	case res = <-ch:
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return fmt.Errorf("rex: %s timed out", req.Op)
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	if out != nil && len(res.Data) > 0 {
		return json.Unmarshal(res.Data, out)
	}
	return nil
}

// Hello greets the server. A client that keeps its connection alive
// starts pinging once the server says it keeps it so; an older server
// does not, and it goes on as before.
func (c *Client) Hello() (h Hello, err error) {
	err = c.call(Request{Op: "hello", Keepalive: c.keepalive}, &h)
	if err == nil && c.keepalive && h.Keepalive > 0 && h.Conn != "" {
		c.pinging.Do(func() {
			c.mu.Lock()
			c.connID = h.Conn
			c.mu.Unlock()
			go c.ping(max(h.Keepalive/3, 10*time.Millisecond))
		})
	}
	return
}

func (c *Client) List() (s []SessionInfo, err error) {
	err = c.call(Request{Op: "list"}, &s)
	return
}

func (c *Client) Create(o CreateOptions) (s SessionInfo, err error) {
	err = c.call(Request{Op: "create", Create: &o}, &s)
	return
}

func (c *Client) Kill(sid string) error { return c.call(Request{Op: "kill", SID: sid}, nil) }

func (c *Client) Resize(sid string, cols, rows int) error {
	return c.call(Request{Op: "resize", SID: sid, Cols: cols, Rows: rows}, nil)
}

func (c *Client) Layout() (json.RawMessage, error) {
	var l json.RawMessage
	err := c.call(Request{Op: "getLayout"}, &l)
	return l, err
}

func (c *Client) SetLayout(l json.RawMessage) error {
	return c.call(Request{Op: "setLayout", Layout: l}, nil)
}

func (c *Client) Shutdown() error { return c.call(Request{Op: "shutdown"}, nil) }

// RemoteStatus tells the server's remote access; the address it gives
// is a secret.
func (c *Client) RemoteStatus() (s RemoteStatus, err error) {
	err = c.call(Request{Op: "remote.status"}, &s)
	return
}

// EnableRemote turns remote access on. The server listens in the
// background: see RemoteStatus.
func (c *Client) EnableRemote() error { return c.call(Request{Op: "remote.enable"}, nil) }

func (c *Client) DisableRemote() error { return c.call(Request{Op: "remote.disable"}, nil) }

// StartPairing opens the pairing window for five minutes, or opens it
// five minutes more, while remote access is on.
func (c *Client) StartPairing() error { return c.call(Request{Op: "remote.pairStart"}, nil) }

// StopPairing closes the pairing window and forgets its requests.
func (c *Client) StopPairing() error { return c.call(Request{Op: "remote.pairStop"}, nil) }

// ApprovePairing allows the client of a pairing request, by the name it
// asked with.
func (c *Client) ApprovePairing(publicKey string) error {
	return c.call(Request{Op: "remote.approve", Remote: &RemoteClient{PublicKey: publicKey}}, nil)
}

// RejectPairing drops a pairing request.
func (c *Client) RejectPairing(publicKey string) error {
	return c.call(Request{Op: "remote.reject", Remote: &RemoteClient{PublicKey: publicKey}}, nil)
}

// RequestPairing asks the server, connected to with ConnectRemote, to
// allow this machine by name; it returns the fingerprint of this
// machine's key, which the server shows next to the request.
func (c *Client) RequestPairing(name string) (string, error) {
	var res PairResult
	err := c.call(Request{Op: "remote.pair", Remote: &RemoteClient{Name: name}}, &res)
	if err != nil && strings.Contains(err.Error(), refusedMessage) {
		// A server not pairing refuses the connection.
		return "", errors.New("rex: the server is not pairing now: start pairing there first")
	}
	return res.Fingerprint, err
}

// refusedMessage is what a server says to a client it does not allow,
// while it is not pairing, before it closes the connection.
const refusedMessage = "rex: this machine is not allowed here"

// pendingMessage ends what a pairing server answers a client whose
// request to pair waits for its user.
const pendingMessage = "the request waits for approval"

// IsRefused tells an error of a server that does not allow this machine:
// it refused the connection, or it is pairing and serves requests to pair
// only. A connection lost otherwise is not a refusal.
func IsRefused(err error) bool {
	s := err.Error()
	return strings.Contains(s, refusedMessage) || strings.Contains(s, "not allowed before pairing")
}

// IsPairingPending tells the refusal of a server pairing whose user has
// not answered this machine's request yet.
func IsPairingPending(err error) bool {
	return IsRefused(err) && strings.Contains(err.Error(), pendingMessage)
}

// AllowRemote allows a client by its public key (ClientPublicKey on its
// machine).
func (c *Client) AllowRemote(name, publicKey string) error {
	return c.call(Request{Op: "remote.allow", Remote: &RemoteClient{Name: name, PublicKey: publicKey}}, nil)
}

// RevokeRemote removes a client and drops its connections.
func (c *Client) RevokeRemote(publicKey string) error {
	return c.call(Request{Op: "remote.revoke", Remote: &RemoteClient{PublicKey: publicKey}}, nil)
}

// RegenerateRemote makes new keys: the address changes, the old one is
// dead.
func (c *Client) RegenerateRemote() error { return c.call(Request{Op: "remote.regenerate"}, nil) }

func (c *Client) Close() error {
	err := c.conn.Close()
	c.closeAttached()
	if c.closer != nil {
		c.closer.Close()
	}
	return err
}

// Stream is a session attached to, for a terminal's Conn: it attaches
// once it knows the size of the screen, from the first Resize, or after
// a moment at the size it had last.
type Stream struct {
	c        *Client
	sid      string
	attachMu sync.Mutex // one attach at a time
	sizeMu   sync.Mutex // the sizes the server is told, in order: the last is the latest
	mu       sync.Mutex
	cond     *sync.Cond
	conn     net.Conn
	dialed   net.Conn // attaching
	err      error
	closed   bool
	// cols and rows are the terminal's size; sentCols and sentRows the
	// size the server was told last.
	cols, rows         int
	sentCols, sentRows int

	// OnData, when set, is called with the size of what the session
	// prints, from the reading goroutine.
	OnData func(n int)
}

// Stream returns a stream of the session sid, which attaches lazily.
func (c *Client) Stream(sid string, cols, rows int) *Stream {
	s := &Stream{c: c, sid: sid, cols: cols, rows: rows}
	s.cond = sync.NewCond(&s.mu)
	time.AfterFunc(400*time.Millisecond, s.attach)
	return s
}

// attach attaches to the session at the terminal's size, once, then
// tells it a size the terminal took meanwhile.
func (s *Stream) attach() {
	s.attachOnce()
	s.sync()
}

// attachOnce attaches, once; a stream that attached or failed stays so.
func (s *Stream) attachOnce() {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.mu.Lock()
	if s.conn != nil || s.err != nil || s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	conn, err := s.c.dial()
	var cols, rows int
	if err == nil {
		// Closed with the client's connection, or the stream, while it
		// attaches too: a network gone answers nothing.
		connID, ok := s.c.track(conn)
		s.mu.Lock()
		s.dialed = conn
		closed := s.closed
		s.mu.Unlock()
		if !ok {
			err = errors.New("rex: connection to the server lost")
		} else if closed {
			err = io.EOF
		}
		// The size of now, not of before the dial, which may be slow: the
		// server gives it to the session before it answers.
		s.sizeMu.Lock()
		defer s.sizeMu.Unlock()
		s.mu.Lock()
		cols, rows = s.cols, s.rows
		s.mu.Unlock()
		if err == nil {
			conn.SetDeadline(time.Now().Add(callTimeout.get()))
			b, _ := json.Marshal(Attach{Op: "attach", SID: s.sid, Cols: cols, Rows: rows, Conn: connID})
			if _, err = conn.Write(append(b, '\n')); err == nil {
				err = readOK(conn)
			}
			conn.SetDeadline(time.Time{})
		}
		if err != nil {
			conn.Close()
			s.c.untrack(conn)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dialed = nil
	if s.closed {
		if err == nil {
			conn.Close()
			s.c.untrack(conn)
		}
		return
	}
	if err != nil {
		s.err = err
	} else {
		s.conn, s.sentCols, s.sentRows = conn, cols, rows
	}
	s.cond.Broadcast()
}

// sync tells the server the terminal's size, once attached, when it was
// told another. One at a time, each reading the size as it goes: the last
// one tells the latest size.
func (s *Stream) sync() error {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	s.mu.Lock()
	if s.conn == nil || s.cols == s.sentCols && s.rows == s.sentRows {
		s.mu.Unlock()
		return nil
	}
	cols, rows := s.cols, s.rows
	s.sentCols, s.sentRows = cols, rows
	s.mu.Unlock()
	return s.c.Resize(s.sid, cols, rows)
}

// readOK reads the server's answer to an attach, a byte at a time so as
// to read nothing past it.
func readOK(conn net.Conn) error {
	var line []byte
	b := make([]byte, 1)
	for {
		if _, err := conn.Read(b); err != nil {
			return err
		}
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
		if len(line) > 4096 {
			return errors.New("rex: bad answer")
		}
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	return nil
}

func (s *Stream) wait() (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.conn == nil && s.err == nil && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return nil, io.EOF
	}
	return s.conn, s.err
}

func (s *Stream) Read(p []byte) (int, error) {
	conn, err := s.wait()
	if err != nil {
		return 0, err
	}
	n, err := conn.Read(p)
	if n > 0 && s.OnData != nil {
		s.OnData(n)
	}
	return n, err
}

func (s *Stream) Write(p []byte) (int, error) {
	conn, err := s.wait()
	if err != nil {
		return 0, err
	}
	return conn.Write(p)
}

// Resize tells the session the size of the screen, attaching first.
func (s *Stream) Resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	s.attachOnce()
	return s.sync()
}

// Close detaches; the session goes on.
func (s *Stream) Close() error {
	s.mu.Lock()
	s.closed = true
	conn, dialed := s.conn, s.dialed
	s.cond.Broadcast()
	s.mu.Unlock()
	if dialed != nil {
		dialed.Close()
	}
	if conn != nil {
		s.c.untrack(conn)
		return conn.Close()
	}
	return nil
}

// Size returns the size the session was last given.
func (s *Stream) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}
