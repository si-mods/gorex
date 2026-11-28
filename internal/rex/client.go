package rex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

	// caps are what this client does beyond the protocol, which its hello
	// tells the server (DeclareCapabilities).
	caps []string

	// events are the changes the server told of (Subscribe); eventsOnce
	// closes the channel when the connection ends.
	events     chan Event
	eventsOnce sync.Once
	// revision is the last layout revision the server told of.
	revision uint64
}

// callTimeout is how long a request waits for its answer.
var callTimeout = newDuration(15 * time.Second)

// Connect connects to the server, starting one in the background when
// none runs, but none for a session, whose server is its own.
func Connect() (*Client, error) {
	conn, err := dialSocket()
	if err != nil {
		if clientSocket() != SocketPath() {
			return nil, fmt.Errorf("rex: connecting to the session's server: %w", err)
		}
		if err := Spawn(); err != nil {
			return nil, fmt.Errorf("rex: starting the server: %w", err)
		}
		if conn, err = dialUntil(time.Now().Add(5 * time.Second)); err != nil {
			return nil, fmt.Errorf("rex: connecting to the server: %w", err)
		}
	}
	return newClient(conn, dialSocket), nil
}

func dialSocket() (net.Conn, error) { return net.Dial("unix", clientSocket()) }

// clientSocket is the socket a client dials: in a session, GOREX_SERVER,
// that of the server that runs it, unless GOREX_DIR names another.
func clientSocket() string {
	if s := os.Getenv("GOREX_SERVER"); s != "" && os.Getenv("GOREX_DIR") == "" {
		return s
	}
	return SocketPath()
}

// ConnectExisting connects to the server that runs, starting none: one a
// client started from an SSH session would end with the session, as
// Windows ends what the session's job runs. The connection is kept alive
// (Hello), so that the sizes it locks unlock as it goes.
func ConnectExisting() (*Client, error) {
	conn, err := dialSocket()
	if err != nil {
		return nil, errNotRunning
	}
	c := newClient(conn, dialSocket)
	c.keepalive = true
	return c, nil
}

var errNotRunning = errors.New("GoRex is not running on this machine; open GoRex first")

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
	c := &Client{conn: conn, dial: dial, pending: map[int64]chan Response{}, closed: make(chan struct{}),
		events: make(chan Event, 256)}
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

// SpeaksProtocol reports whether the executable exe serves this build's
// protocol, as it says with -server -protocol-version. A build of before
// the flag tries to serve instead, fails as a server runs, and says
// nothing: it does not. The answer is kept for exe's path and time of
// modification, so that a build is probed once.
func SpeaksProtocol(exe string) bool {
	path, at := executable(exe)
	spoke.Lock()
	defer spoke.Unlock()
	if at.IsZero() || path != spoke.exe || !at.Equal(spoke.at) {
		spoke.exe, spoke.at, spoke.ok = path, at, probeProtocol(path)
	}
	return spoke.ok
}

// spoke is the last answer of SpeaksProtocol.
var spoke struct {
	sync.Mutex
	exe string
	at  time.Time
	ok  bool
}

// probeProtocol asks exe for its protocol. Replaced by tests.
var probeProtocol = func(exe string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-server", "-protocol-version")
	hideConsole(cmd)
	out, err := cmd.Output()
	v, verr := strconv.Atoi(strings.TrimSpace(string(out)))
	return err == nil && verr == nil && v == ProtocolVersion
}

// Restart shuts the server down to have it replaced, waits for it to exit,
// and connects to a new one, which starts the sessions of the layout again:
// new shells at the directories their panes were in, with the same ids. What
// the old sessions printed is lost with the old server. A server of before
// CapabilityShutdownRestart ends its sessions instead. A server that did not
// stop in 15s is an error: connecting to it would undo the restart.
func (c *Client) Restart(pid int) (*Client, error) {
	err := c.StopToRestart()
	c.Close()
	if err != nil {
		return nil, err
	}
	if pid > 0 {
		deadline := time.Now().Add(15 * time.Second)
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAlive(pid) {
			return nil, errors.New("rex: the server did not stop")
		}
	}
	return Connect()
}

// ProcessAlive reports whether the process pid runs.
func ProcessAlive(pid int) bool { return processAlive(pid) }

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
			c.eventsOnce.Do(func() { close(c.events) })
			close(c.closed)
			return
		}
		var res Response
		if json.Unmarshal(line, &res) != nil {
			continue
		}
		c.mu.Lock()
		if res.ID == 0 && res.Kind != "" {
			if res.Revision > 0 {
				c.revision = res.Revision
			}
			c.mu.Unlock()
			select {
			case c.events <- res.Event:
			default: // a reader that stopped reading does not hold the client
			}
			continue
		}
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
func (c *Client) ping(every, timeout time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
		}
		if err := c.callWithin(Request{Op: "ping"}, nil, timeout); err != nil {
			c.conn.Close()
			c.closeAttached()
			return
		}
	}
}

// Ping measures a request's round trip to the server, for display.
func (c *Client) Ping(timeout time.Duration) (rtt time.Duration, err error) {
	start := time.Now()
	err = c.callWithin(Request{Op: "ping"}, nil, timeout)
	return time.Since(start), err
}

// call sends a request and decodes the response's data into out.
func (c *Client) call(req Request, out any) error {
	return c.callWithin(req, out, callTimeout.get())
}

// callWithin is call, waiting at most timeout for the answer.
func (c *Client) callWithin(req Request, out any, timeout time.Duration) error {
	res, err := c.exchange(req, timeout)
	if err != nil {
		return err
	}
	if out != nil && len(res.Data) > 0 {
		return json.Unmarshal(res.Data, out)
	}
	return nil
}

// exchange sends a request and returns the response, its error as one.
func (c *Client) exchange(req Request, timeout time.Duration) (Response, error) {
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.err != nil {
		refused := c.refused
		c.mu.Unlock()
		if refused != "" {
			return Response{}, errors.New(refused)
		}
		return Response{}, errors.New("rex: connection to the server lost")
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
		return Response{}, err
	}
	var res Response
	select {
	case res = <-ch:
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return Response{}, fmt.Errorf("rex: %s %w", req.Op, ErrTimeout)
	}
	if res.Error != "" {
		return res, errors.New(res.Error)
	}
	return res, nil
}

// Hello greets the server. A client that keeps its connection alive
// starts pinging once the server says it keeps it so; an older server
// does not, and it goes on as before.
func (c *Client) Hello() (h Hello, err error) {
	c.mu.Lock()
	caps := append([]string(nil), c.caps...)
	c.mu.Unlock()
	err = c.call(Request{Op: "hello", Keepalive: c.keepalive, Capabilities: caps}, &h)
	if err == nil && c.keepalive && h.Keepalive > 0 && h.Conn != "" {
		c.pinging.Do(func() {
			c.mu.Lock()
			c.connID = h.Conn
			c.mu.Unlock()
			// A ping every third of the server's wait, each with the rest
			// of it to be answered: as long as the server waits, no more,
			// on a link slow but alive.
			every := max(h.Keepalive/3, 10*time.Millisecond)
			go c.ping(every, max(h.Keepalive-every, every))
		})
	}
	return
}

// DeclareCapabilities tells the server what this client does beyond the
// protocol, before Hello: CapabilityPrompts for one that shows the
// questions a config action asks (docs/MOD-009-client-prompts.md).
func (c *Client) DeclareCapabilities(caps ...string) {
	c.mu.Lock()
	c.caps = append(c.caps, caps...)
	c.mu.Unlock()
}

// PromptOpen asks every client that can show a question to show spec, as
// action, and waits for the first answer or timeout. A server before the
// capability answers an error.
func (c *Client) PromptOpen(action string, spec PromptSpec, timeout time.Duration) (PromptAnswer, error) {
	var ans PromptAnswer
	wait := timeout + 10*time.Second
	err := c.callWithin(Request{Op: "prompt.open", Prompt: &spec, PromptAction: action, PromptTimeout: int(timeout / time.Millisecond)}, &ans, wait)
	return ans, err
}

// PromptReply answers the question that came with callID.
func (c *Client) PromptReply(callID string, ans PromptAnswer) error {
	return c.call(Request{Op: "prompt.answer", CallID: callID, Answer: &ans}, nil)
}

// Subscribe asks the server to tell this connection of the changes of
// the layout and the sessions as they come (Events). A server before it
// answers an error: poll it then.
func (c *Client) Subscribe() error { return c.call(Request{Op: "subscribe"}, nil) }

// SubscribeTransfers is Subscribe, and asks for the file transfers' events
// too (Event.Transfer). A server before CapabilityFileTransfer tells of
// none.
func (c *Client) SubscribeTransfers() error {
	return c.call(Request{Op: "subscribe", Transfers: true}, nil)
}

// Events returns the changes the server tells of, in order, while the
// connection lasts; the channel closes with it.
func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) List() (s []SessionInfo, err error) {
	err = c.call(Request{Op: "list"}, &s)
	return
}

// ListSince is List for a client that polls it: given the ETag of the
// list it has, it returns changed false, and no list, while the list is
// the same, and the new list's ETag otherwise. A server before version 2
// always sends the list, with no ETag.
func (c *Client) ListSince(etag string) (s []SessionInfo, next string, changed bool, err error) {
	res, err := c.exchange(Request{Op: "list", ETag: etag}, callTimeout.get())
	if err != nil {
		return nil, "", false, err
	}
	if etag != "" && res.ETag == etag && len(res.Data) == 0 {
		return nil, etag, false, nil
	}
	err = json.Unmarshal(res.Data, &s)
	return s, res.ETag, err == nil, err
}

func (c *Client) Create(o CreateOptions) (s SessionInfo, err error) {
	err = c.call(Request{Op: "create", Create: &o}, &s)
	return
}

func (c *Client) Kill(sid string) error { return c.call(Request{Op: "kill", SID: sid}, nil) }

// Ports returns the TCP ports that the session's programs listen on.
func (c *Client) Ports(sid string) (PortsResult, error) {
	var res PortsResult
	err := c.call(Request{Op: "ports", SID: sid}, &res)
	return res, err
}

func (c *Client) Resize(sid string, cols, rows int) error {
	return c.call(Request{Op: "resize", SID: sid, Cols: cols, Rows: rows}, nil)
}

// Resync asks the server to send the session's screen again, in band, on
// the stream whose attach gave client (Stream.ClientID), with what the
// session prints after it: ErrResyncSize's message while the session's
// size is not cols×rows.
func (c *Client) Resync(sid, client string, cols, rows int) error {
	return c.call(Request{Op: "resync", SID: sid, Client: client, Cols: cols, Rows: rows}, nil)
}

// Focus tells the server the user of this client looks at the session
// now: its size follows the machine the user's focus is on.
func (c *Client) Focus(sid string) error { return c.call(Request{Op: "focus", SID: sid}, nil) }

// SetTheme tells the server the host terminal's active colors, so
// headless snapshots and remote Host Colors clients follow them.
func (c *Client) SetTheme(t *Theme) error { return c.call(Request{Op: "theme.set", Theme: t}, nil) }

// UnlockSize takes the session's size back, at cols×rows when given, from
// owner, or from whoever holds it when "".
func (c *Client) UnlockSize(sid, owner string, cols, rows int) error {
	return c.call(Request{Op: "unlockSize", SID: sid, Owner: owner, Cols: cols, Rows: rows}, nil)
}

// LockSize takes the session's size for owner at cols×rows, leased:
// RenewSize renews it. Hello must have kept the connection alive.
func (c *Client) LockSize(sid, owner string, cols, rows int) error {
	return c.call(Request{Op: "lockSize", SID: sid, Owner: owner, Cols: cols, Rows: rows}, nil)
}

// LockedResize resizes the session for owner, which fails once owner no
// longer holds its size.
func (c *Client) LockedResize(sid, owner string, cols, rows int) error {
	return c.call(Request{Op: "lockedResize", SID: sid, Owner: owner, Cols: cols, Rows: rows}, nil)
}

// SeeStatus tells the server that the user saw the session's status
// records updated till at (CapabilityStatusSeen): its info's StatusSeen
// moves forward to it.
func (c *Client) SeeStatus(sid string, at time.Time) error {
	return c.call(Request{Op: "seeStatus", SID: sid, Seen: at}, nil)
}

// DirList lists the directories in path on the server's machine
// (DirList); a server without CapabilityDirList answers an error.
func (c *Client) DirList(path string, hidden bool) (r DirListResult, err error) {
	err = c.call(Request{Op: "dir.list", DirList: &DirList{Path: path, Hidden: hidden}}, &r)
	return
}

// DirMkdir makes the directory path on the server's machine (DirMkdir);
// a server without CapabilityDirMkdir answers an error.
func (c *Client) DirMkdir(path string) error {
	return c.call(Request{Op: "dir.mkdir", DirMkdir: &DirMkdir{Path: path}}, nil)
}

// AgentsList lists the known agent CLIs installed on the server's machine
// (AgentsResult); a server without CapabilityAgents answers an error.
func (c *Client) AgentsList() (r AgentsResult, err error) {
	err = c.call(Request{Op: "agents.list"}, &r)
	return
}

// Delegate starts a delegated coding-agent tab on the server
// (CapabilityDelegate, docs/MOD-016-peer-delegation.md).
func (c *Client) Delegate(opt DelegateOptions) (r DelegateResult, err error) {
	timeout := delegateCallTimeout(opt.Async, opt.TimeoutMs)
	err = c.callWithin(Request{Op: "delegate", Delegate: &opt}, &r, timeout)
	return
}

// DelegateSend sends a warm follow-up prompt to a live delegated session.
func (c *Client) DelegateSend(opt DelegateSendOptions) (r DelegateResult, err error) {
	timeout := delegateCallTimeout(opt.Async, opt.TimeoutMs)
	err = c.callWithin(Request{Op: "delegate.send", DelegateSend: &opt}, &r, timeout)
	return
}

func delegateCallTimeout(async bool, timeoutMs int) time.Duration {
	if async {
		return max(callTimeout.get(), 45*time.Second)
	}
	base := delegateDefaultTimout
	if timeoutMs > 0 {
		base = time.Duration(timeoutMs) * time.Millisecond
	}
	// Allow extra headroom for TUI startup, paste delivery, and remote round-trip.
	return base + 30*time.Second
}

// DelegateResult fetches the latest status and extracted output of a
// delegated session.
func (c *Client) DelegateResult(sid string) (r DelegateResult, err error) {
	err = c.call(Request{Op: "delegate.result", SID: sid}, &r)
	return
}

func (c *Client) RenewSize(sid, owner string) error {
	return c.call(Request{Op: "renewSize", SID: sid, Owner: owner}, nil)
}

// Command asks the app to change the layout, and returns its answer
// (LayoutCommand).
func (c *Client) Command(cmd LayoutCommand) (res LayoutCommand, err error) {
	err = c.call(Request{Op: "command", Command: &cmd}, &res)
	return
}

// SplitTree makes the panes of a tree of splits in its shape, from first,
// the pane that stands for the whole tree. A split splits its pane, which
// goes on standing for its first side and the new one for its second,
// moves the divider to its ratio, and then makes each side. split reads a
// tree: a split's direction, ratio and sides, ok false for a pane. do runs
// c, a pane.split that makes the pane of leaf or a pane.resize, and returns
// the new pane. SplitTree returns the panes in leaf order.
func SplitTree[T any](tree T, first string, split func(T) (vertical bool, ratio float64, a, b T, ok bool), do func(c LayoutCommand, leaf T) (string, error)) ([]string, error) {
	vertical, ratio, a, b, ok := split(tree)
	if !ok {
		return []string{first}, nil
	}
	leaf := b
	for {
		_, _, la, _, ok := split(leaf)
		if !ok {
			break
		}
		leaf = la
	}
	second, err := do(LayoutCommand{Op: "pane.split", SID: first, Vertical: vertical}, leaf)
	if err != nil {
		return nil, err
	}
	// The new split is at a half. Its divider moves by an edge no split
	// above it has: the first pane's growing, the second's shrinking.
	if d := float32(ratio - 0.5); ratio > 0 && ratio < 1 && (d > 0.01 || d < -0.01) {
		grow, shrink := "right", "left"
		if vertical {
			grow, shrink = "down", "up"
		}
		c := LayoutCommand{Op: "pane.resize", SID: first, Direction: grow, Amount: d}
		if d < 0 {
			c.SID, c.Direction, c.Amount = second, shrink, -d
		}
		if _, err := do(c, leaf); err != nil {
			return nil, err
		}
	}
	panes, err := SplitTree(a, first, split, do)
	if err != nil {
		return nil, err
	}
	more, err := SplitTree(b, second, split, do)
	return append(panes, more...), err
}

// TakeCommand returns the next layout command for the app to make, after
// waiting a while for one: nil when none came.
func (c *Client) TakeCommand() (cmd *LayoutCommand, err error) {
	err = c.call(Request{Op: "command.take"}, &cmd)
	return
}

// CommandDone answers a command the app took.
func (c *Client) CommandDone(res LayoutCommand) error {
	return c.call(Request{Op: "command.done", Command: &res}, nil)
}

// AttachConn attaches to a session as a, through this connection if kept
// alive, and returns the connection once the server took it: what
// follows is the session's output, after a SnapshotHeader with a.Framed.
func (c *Client) AttachConn(a Attach) (net.Conn, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	id, ok := c.track(conn)
	if !ok {
		conn.Close()
		return nil, errors.New("rex: connection to the server lost")
	}
	a.Op, a.Conn = "attach", id
	if a.Client == "" {
		a.Client = newClientID()
	}
	b, _ := json.Marshal(a)
	conn.SetDeadline(time.Now().Add(callTimeout.get()))
	if _, err = conn.Write(append(b, '\n')); err == nil {
		err = readOK(conn)
	}
	conn.SetDeadline(time.Time{})
	if err != nil {
		conn.Close()
		c.untrack(conn)
		return nil, err
	}
	return trackedConn{conn, c}, nil
}

// trackedConn is a connection attached through a client, which forgets it
// as it closes.
type trackedConn struct {
	net.Conn
	c *Client
}

func (t trackedConn) Close() error {
	t.c.untrack(t.Conn)
	return t.Conn.Close()
}

// Input types b into the session, as a CLI or programmatic client.
func (c *Client) Input(sid string, b []byte) error {
	return c.InputWithAgent(sid, b, "")
}

// InputWithAgent types b into the session and labels the active agent badge
// with agent (defaulting to ActorCLI on the server when empty).
func (c *Client) InputWithAgent(sid string, b []byte, agent string) error {
	return c.InputFromSession(sid, b, agent, "")
}

// InputFromSession types b into the session, recording the agent label and
// the caller's parent session ID for takeover events.
func (c *Client) InputFromSession(sid string, b []byte, agent, parentSession string) error {
	return c.call(Request{Op: "input", SID: sid, Bytes: b, Actor: ActorCLI, Agent: agent, ParentSession: parentSession}, nil)
}

// InputHuman types b into the session on behalf of a human UI action,
// clearing any active agent mark rather than stamping CLI input.
func (c *Client) InputHuman(sid string, b []byte) error {
	return c.call(Request{Op: "input", SID: sid, Bytes: b, Actor: ActorHuman}, nil)
}

// SendKey types a key event into the session's program, encoded as the
// program's modes ask.
func (c *Client) SendKey(sid string, k KeyInput) error {
	return c.call(Request{Op: "key", SID: sid, Key: &k}, nil)
}

// SendKeyWithAgent types a key event into the session on behalf of the CLI,
// stamping lastCLIInputAt and labeling the active agent badge with agent.
func (c *Client) SendKeyWithAgent(sid string, k KeyInput, agent string) error {
	return c.SendKeyFromSession(sid, k, agent, "")
}

// SendKeyFromSession types a key event into the session on behalf of the CLI,
// recording the agent label and the caller's parent session ID.
func (c *Client) SendKeyFromSession(sid string, k KeyInput, agent, parentSession string) error {
	return c.call(Request{Op: "key", SID: sid, Key: &k, Actor: ActorCLI, Agent: agent, ParentSession: parentSession}, nil)
}

// SendMouse sends a pointer event to the session's program, encoded as
// its mouse modes ask.
func (c *Client) SendMouse(sid string, m MouseInput) error {
	return c.call(Request{Op: "mouse", SID: sid, Mouse: &m}, nil)
}

// SendFocus tells the session's program its window gained or lost the
// focus, when it asked for focus reports.
func (c *Client) SendFocus(sid string, in bool) error {
	return c.call(Request{Op: "focusReport", SID: sid, FocusReport: &in}, nil)
}

// Modes returns the modes the session's program set, and the screen it
// shows.
func (c *Client) Modes(sid string) (m ModeStatus, err error) {
	err = c.call(Request{Op: "modes", SID: sid}, &m)
	return
}

// Format returns the session's screen as text, or as the escape
// sequences that redraw it.
func (c *Client) Format(sid, kind string) (f FormatResult, err error) {
	err = c.call(Request{Op: "format", SID: sid, Format: &FormatOptions{Kind: kind}}, &f)
	return
}

// InputMode reports how the session's terminal handles typed input.
func (c *Client) InputMode(sid string) (m InputMode, err error) {
	err = c.call(Request{Op: "inputMode", SID: sid}, &m)
	return
}

// ClearScreen clears the session's scrollback and the screen above the
// cursor's line, unless a full-screen program shows.
func (c *Client) ClearScreen(sid string) error {
	return c.call(Request{Op: "clear", SID: sid}, nil)
}

// ResetTerminal puts the session's terminal back to its starting state,
// clearing the screen and scrollback.
func (c *Client) ResetTerminal(sid string) error {
	return c.call(Request{Op: "reset", SID: sid}, nil)
}

// pasteChunk bounds one "paste" request: a control connection's line
// holds maxLine, which a string may take six times over as JSON escapes
// it, and a paste of a larger text goes as several.
const pasteChunk = 64 << 10

// Paste writes text to a session's program as a paste, as the terminal's
// paste keys do: bracketed when the program asked for bracketed pastes,
// as SessionInfo.ConPTY needs, a console that drops the terminal's paste
// event. A text longer than pasteChunk goes as several pastes, each of a
// piece of its own, in order.
func (c *Client) Paste(sid, text string) error {
	return c.pasteActor(sid, text, ActorCLI, "", "")
}

// PasteWithAgent pastes text into the session and labels the active agent
// badge with agent (defaulting to ActorCLI on the server when empty).
func (c *Client) PasteWithAgent(sid, text, agent string) error {
	return c.PasteFromSession(sid, text, agent, "")
}

// PasteFromSession pastes text into the session, recording the agent label and
// the caller's parent session ID for takeover events.
func (c *Client) PasteFromSession(sid, text, agent, parentSession string) error {
	return c.pasteActor(sid, text, ActorCLI, agent, parentSession)
}

// PasteHuman pastes text into the session on behalf of a human UI action,
// clearing any active agent mark rather than stamping CLI input.
func (c *Client) PasteHuman(sid, text string) error {
	return c.pasteActor(sid, text, ActorHuman, "", "")
}

func (c *Client) pasteActor(sid, text, actor, agent, parentSession string) error {
	for len(text) > 0 {
		n := min(len(text), pasteChunk)
		for n > 0 && n < len(text) && !utf8.RuneStart(text[n]) {
			n-- // a piece never splits a rune
		}
		if err := c.call(Request{Op: "paste", SID: sid, Paste: text[:n], Actor: actor, Agent: agent, ParentSession: parentSession}, nil); err != nil {
			return err
		}
		text = text[n:]
	}
	return nil
}

// Clients returns the clients attached to the sessions this one may use.
func (c *Client) Clients() ([]ClientInfo, error) {
	var out []ClientInfo
	err := c.call(Request{Op: "clients"}, &out)
	return out, err
}

// Capture returns the session's screen as an attach would send it, cut to
// about history bytes of scrollback when history is positive.
func (c *Client) Capture(sid string, history int) (CaptureResult, error) {
	var r CaptureResult
	err := c.call(Request{Op: "capture", SID: sid, Capture: &CaptureOptions{History: history}}, &r)
	return r, err
}

// Action runs an app action with args, through the window that owns the
// layout, with the pane of session sid focused when sid is not "". It
// fails when no window is open.
func (c *Client) Action(sid, id string, args map[string]any) (LayoutCommand, error) {
	var raw json.RawMessage
	if args != nil {
		var err error
		if raw, err = json.Marshal(args); err != nil {
			return LayoutCommand{}, err
		}
	}
	var res LayoutCommand
	err := c.call(Request{Op: "action", SID: sid, Action: &ActionCall{ID: id, Args: raw}}, &res)
	return res, err
}

func (c *Client) Layout() (json.RawMessage, error) {
	var l json.RawMessage
	err := c.call(Request{Op: "getLayout"}, &l)
	return l, err
}

// LayoutState returns the layout and the revision it is at (ProtocolVersion
// 3). A server before 3 answers an error: use Layout then.
func (c *Client) LayoutState() (LayoutState, error) {
	var st LayoutState
	if err := c.call(Request{Op: "getLayoutState"}, &st); err != nil {
		return st, err
	}
	c.mu.Lock()
	c.revision = st.Revision
	c.mu.Unlock()
	return st, nil
}

// SetLayout sets the whole layout, whatever the server has: for tests and
// tools that seed one. The app changes it with commands (Command).
func (c *Client) SetLayout(l json.RawMessage) error {
	return c.SetLayoutAt(l, 0)
}

// SetLayoutAt sets the layout only when it is still at revision; a
// revision of 0 sets it whatever it is. A server before 3 answers the
// check with an error instead of ignoring the revision.
func (c *Client) SetLayoutAt(l json.RawMessage, revision uint64) error {
	if revision != 0 {
		if _, err := c.LayoutState(); err != nil {
			return fmt.Errorf("rex: layout revisions need a version 3 server: %w", err)
		}
	}
	_, err := c.WriteLayout(l, revision)
	return err
}

// WriteLayout is SetLayoutAt without the check of the server's version,
// and returns the revision the layout is at once set: a writer that
// subscribed tells its own change from another's by it.
func (c *Client) WriteLayout(l json.RawMessage, revision uint64) (uint64, error) {
	var st LayoutState
	if err := c.call(Request{Op: "setLayout", Layout: l, LayoutRevision: revision, Capabilities: []string{CapabilityLayoutServer}}, &st); err != nil {
		return 0, err
	}
	if st.Revision > 0 {
		c.mu.Lock()
		c.revision = st.Revision
		c.mu.Unlock()
	}
	return st.Revision, nil
}

// Revision returns the layout revision the server last stated, from a
// LayoutState, a SetLayoutAt, or a layout event; 0 before any.
func (c *Client) Revision() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.revision
}

func (c *Client) Shutdown() error { return c.call(Request{Op: "shutdown"}, nil) }

// ShutdownIfIdle is Shutdown for a server that holds no session, and starts
// none: another leaves the server running and fails with an error that
// starts with BusyPrefix (CapabilityShutdownIfIdle).
func (c *Client) ShutdownIfIdle() error {
	return c.call(Request{Op: "shutdown", IfIdle: true}, nil)
}

// StopToRestart asks the server to stop as one a new server replaces: the
// next one starts the sessions of its layout again, as new shells, where
// Shutdown ends them with the server (CapabilityShutdownRestart).
func (c *Client) StopToRestart() error {
	return c.call(Request{Op: "shutdown", Restart: true}, nil)
}

func (c *Client) Close() error {
	err := c.conn.Close()
	c.closeAttached()
	if c.closer != nil {
		c.closer.Close()
	}
	return err
}
