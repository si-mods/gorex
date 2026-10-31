// Package rexpeer speaks Rex's control API as a client: it lists a Rex
// server's sessions and windows, reads a block's screen, types into it,
// sizes it, and streams its output. GoRex uses it to mirror a Rex session
// in its own panes, and to import one's shape.
package rexpeer

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// APIRevision is the revision of Rex's API this client speaks.
const APIRevision = 7

// APIVersion is the protocol of the control connection.
const APIVersion = "rex.control.v1"

// DataTerminal is the subprotocol of a terminal's data connection.
const DataTerminal = "rex.data.terminal.v1"

// Client is a control connection to a Rex server.
type Client struct {
	conn *websocket.Conn
	sock string

	mu      sync.Mutex
	next    int
	pending map[string]chan answer
	err     error
	closed  chan struct{}
}

// APIError is a failed call, as Rex reports it.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// answer is a response to a call.
type answer struct {
	payload json.RawMessage
	err     error
}

// Dial opens a control connection to the Rex server on socket.
func Dial(ctx context.Context, socket string) (*Client, error) {
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialUnix(ctx, socket)
	}}}
	ws, _, err := websocket.Dial(ctx, "ws://localhost/api/control", &websocket.DialOptions{
		Subprotocols: []string{APIVersion},
		HTTPClient:   hc,
	})
	if err != nil {
		return nil, err
	}
	hello := map[string]any{
		"type": "hello", "api_revision": APIRevision,
		"client": map[string]any{"kind": "other", "name": "gorex", "platform": platform()},
	}
	data, _ := json.Marshal(hello)
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		ws.CloseNow()
		return nil, err
	}
	_, data, err = ws.Read(ctx)
	if err != nil {
		ws.CloseNow()
		return nil, err
	}
	var w struct {
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	}
	if json.Unmarshal(data, &w) != nil || w.Type != "welcome" || w.Protocol != APIVersion {
		ws.CloseNow()
		return nil, fmt.Errorf("rex: %s, not a welcome", data)
	}
	ws.SetReadLimit(1 << 20)
	c := &Client{conn: ws, sock: socket, pending: map[string]chan answer{}, closed: make(chan struct{})}
	go c.read()
	return c, nil
}

// Close ends the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	c.mu.Unlock()
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

// socket returns the server's socket, for a data connection.
func (c *Client) socket() string { return c.sock }

// read answers the calls and drops the events, until the connection ends.
func (c *Client) read() {
	defer func() {
		c.mu.Lock()
		select {
		case <-c.closed:
		default:
			close(c.closed)
		}
		for _, ch := range c.pending {
			ch <- answer{err: errors.New("rex: connection lost")}
		}
		c.pending = map[string]chan answer{}
		c.mu.Unlock()
	}()
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
		var m struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Payload json.RawMessage `json:"payload"`
			Error   *APIError       `json:"error"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "response" {
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if m.Error != nil {
			ch <- answer{err: m.Error}
		} else {
			ch <- answer{payload: m.Payload}
		}
	}
}

// Call runs a method and decodes its result into out, when out is not nil.
// A call without a deadline waits at most callTimeout.
func (c *Client) Call(ctx context.Context, method string, payload, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
	ch := make(chan answer, 1)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return errors.New("rex: connection lost")
	default:
	}
	c.next++
	id := "g" + strconv.Itoa(c.next)
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"type": "request", "id": id, "method": method}
	if payload != nil {
		req["payload"] = payload
	}
	data, _ := json.Marshal(req)
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case a := <-ch:
		if a.err != nil {
			return a.err
		}
		if out != nil && len(a.payload) > 0 {
			return json.Unmarshal(a.payload, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return errors.New("rex: connection lost")
	}
}

// Session is one of a Rex server's sessions.
type Session struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

// Sessions lists the server's sessions.
func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var out struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.Call(ctx, "session.list", nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// View is a session's windows, their layouts and their blocks.
type View struct {
	Revision       uint64   `json:"revision"`
	SessionID      string   `json:"session_id"`
	Label          string   `json:"label"`
	ActiveWindowID string   `json:"active_window_id"`
	Windows        []Window `json:"windows"`
}

// Window is one of a session's windows.
type Window struct {
	WindowID       string  `json:"window_id"`
	Label          string  `json:"label"`
	FocusedBlockID string  `json:"focused_block_id"`
	ZoomedBlockID  string  `json:"zoomed_block_id"`
	Layers         []Layer `json:"layers"`
}

// Layer is a window's tiled or floating layer.
type Layer struct {
	LayerID string     `json:"layer_id"`
	Kind    string     `json:"kind"`
	Bounds  [4]float64 `json:"bounds"`
	Layout  *Layout    `json:"layout"`
	Blocks  []Block    `json:"blocks"`
}

// Layout is where blocks sit in a layer: a block, or a split of two.
type Layout struct {
	Rect    [4]float64 `json:"rect"`
	BlockID string     `json:"block_id"`
	Split   *Split     `json:"split"`
}

// Split divides a layout in two.
type Split struct {
	SplitID   string  `json:"split_id"`
	Direction string  `json:"direction"`
	Ratio     float64 `json:"ratio"`
	Before    *Layout `json:"before"`
	After     *Layout `json:"after"`
}

// Block is one of a session's blocks.
type Block struct {
	BlockID     string `json:"block_id"`
	Label       string `json:"label"`
	CreatorName string `json:"creator_name"`
	Flavor      string `json:"flavor"`
	HostID      string `json:"host_id"`
	Placement   string `json:"placement"`
	WindowID    string `json:"window_id"`
	Owner       string `json:"owner"`
}

// Attach attaches the control connection to a session and returns its
// view.
func (c *Client) Attach(ctx context.Context, session string) (View, error) {
	var out struct {
		View View `json:"view"`
	}
	err := c.Call(ctx, "session.attach", map[string]any{"session_id": session}, &out)
	return out.View, err
}

// Blocks lists a session's blocks, detached ones included.
func (c *Client) Blocks(ctx context.Context, session string) ([]Block, error) {
	var out struct {
		Blocks []Block `json:"blocks"`
	}
	err := c.Call(ctx, "session.list_blocks", map[string]any{"session_id": session}, &out)
	return out.Blocks, err
}

// Pwd returns the working directory a block's program reported, "" when it
// reported none.
func (c *Client) Pwd(ctx context.Context, session, block string) string {
	var out struct {
		Pwd string `json:"pwd"`
	}
	if err := c.Call(ctx, "com.superlogical.terminal.pwd", map[string]any{
		"session_id": session, "block_id": block, "args": map[string]any{},
	}, &out); err != nil {
		return ""
	}
	if strings.HasPrefix(out.Pwd, "file://") {
		if u, err := urlParse(out.Pwd); err == nil {
			return u
		}
	}
	return out.Pwd
}

// Status is what a block's program reported it is doing.
type Status struct {
	State    string `json:"state"`
	Kind     string `json:"kind,omitempty"`
	App      string `json:"app,omitempty"`
	Title    string `json:"title,omitempty"`
	Msg      string `json:"msg,omitempty"`
	Progress int    `json:"progress"`
}

// ProgramStatus returns the status a block's program reported, its root
// record, and whether it reported one at all.
func (c *Client) ProgramStatus(ctx context.Context, session, block string) (Status, bool) {
	var out struct {
		Records []struct {
			ID           string `json:"id"`
			State        string `json:"state"`
			Kind         string `json:"kind"`
			App          string `json:"app"`
			EffectiveApp string `json:"effective_app"`
			Title        string `json:"title"`
			Msg          string `json:"msg"`
			Progress     *int   `json:"progress"`
		} `json:"records"`
	}
	if err := c.Call(ctx, "com.superlogical.terminal.program_status", map[string]any{
		"session_id": session, "block_id": block, "args": map[string]any{},
	}, &out); err != nil || len(out.Records) == 0 {
		return Status{}, false
	}
	r := out.Records[0]
	for _, o := range out.Records {
		if o.ID == "" {
			r = o
			break
		}
	}
	st := Status{State: r.State, Kind: r.Kind, App: r.EffectiveApp, Title: r.Title, Msg: r.Msg, Progress: -1}
	if st.App == "" {
		st.App = r.App
	}
	if r.Progress != nil {
		st.Progress = *r.Progress
	}
	return st, true
}

// Title returns the title a block's program set, "" when it set none.
func (c *Client) Title(ctx context.Context, session, block string) string {
	var out struct {
		Title string `json:"title"`
	}
	if err := c.Call(ctx, "com.superlogical.terminal.title", map[string]any{
		"session_id": session, "block_id": block, "args": map[string]any{},
	}, &out); err != nil {
		return ""
	}
	return out.Title
}

// Format renders a block's screen and scrollback as escape sequences.
func (c *Client) Format(ctx context.Context, session, block, format string) (string, error) {
	var out struct {
		Content string `json:"content"`
	}
	err := c.Call(ctx, "com.superlogical.terminal.format", map[string]any{
		"session_id": session, "block_id": block,
		"args": map[string]any{"format": format},
	}, &out)
	return out.Content, err
}

// Write types text into a block, escape sequences included.
func (c *Client) Write(ctx context.Context, session, block, data string) error {
	return c.Call(ctx, "com.superlogical.terminal.write", map[string]any{
		"session_id": session, "block_id": block, "args": map[string]any{"data": data},
	}, nil)
}

// Resize gives a block's terminal a size.
func (c *Client) Resize(ctx context.Context, session, block string, cols, rows int) error {
	return c.Call(ctx, "com.superlogical.terminal.resize", map[string]any{
		"session_id": session, "block_id": block,
		"args": map[string]any{"columns": cols, "rows": rows},
	}, nil)
}

// Data opens a data connection to a block's terminal, which starts with
// what the screen shows and goes on with what the program prints.
func (c *Client) Data(ctx context.Context, session, block string) (*Stream, error) {
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	if err := c.Call(ctx, "data.connect", map[string]any{
		"session_id": session, "block_id": block,
		"data_ref_id": "terminal", "subprotocol": DataTerminal,
	}, &ticket); err != nil {
		return nil, err
	}
	raw, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(ticket.Ticket)
	if err != nil || len(raw) != 16 {
		return nil, fmt.Errorf("rex: bad ticket %q", ticket.Ticket)
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialUnix(ctx, c.sock)
	}}}
	ws, _, err := websocket.Dial(ctx, "ws://localhost/api/data", &websocket.DialOptions{
		Subprotocols: []string{DataTerminal},
		HTTPClient:   hc,
	})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(1 << 20)
	if err := ws.Write(ctx, websocket.MessageBinary, append([]byte{0x01}, raw...)); err != nil {
		ws.CloseNow()
		return nil, err
	}
	_, first, err := ws.Read(ctx)
	if err != nil {
		ws.CloseNow()
		return nil, err
	}
	if len(first) < 1 || first[0] != 0x81 || len(first) < 32 {
		ws.CloseNow()
		if len(first) >= 2 && first[0] == 0x82 {
			return nil, fmt.Errorf("rex: data handshake refused (%d)", first[1])
		}
		return nil, errors.New("rex: data handshake refused")
	}
	st := &Stream{
		conn:  ws,
		nonce: binary.LittleEndian.Uint64(first[24:32]),
	}
	st.rd = bufio.NewReaderSize(&messageReader{conn: ws}, 64<<10)
	return st, nil
}

// Stream is a block's output, as a byte stream. It starts with a snapshot
// of the screen in Ghostty's snapshot format, which a terminal of Ghostty's
// restores; SkipSnapshot passes it.
type Stream struct {
	conn  *websocket.Conn
	nonce uint64
	rd    *bufio.Reader
}

// Nonce names the stream's own control messages, which the server wraps in
// APC escape sequences.
func (s *Stream) Nonce() uint64 { return s.nonce }

// messageReader reads a WebSocket's binary messages as one stream.
type messageReader struct {
	conn *websocket.Conn
	cur  []byte
}

func (m *messageReader) Read(p []byte) (int, error) {
	for len(m.cur) == 0 {
		_, data, err := m.conn.Read(context.Background())
		if err != nil {
			return 0, err
		}
		m.cur = data
	}
	n := copy(p, m.cur)
	m.cur = m.cur[n:]
	return n, nil
}

// SkipSnapshot reads past the snapshot the stream starts with: the
// envelope "GHOSTSNP" and a u16 version, then records of a u16 tag, a u32
// length, a CRC32C and the payload, the last being FINISH.
func (s *Stream) SkipSnapshot() error {
	head, err := s.rd.Peek(8)
	if err != nil {
		return err
	}
	if string(head) != "GHOSTSNP" {
		// A stream of escape sequences: GoRex's own, or a server that
		// sends none.
		return nil
	}
	var env [10]byte
	if _, err := io.ReadFull(s.rd, env[:]); err != nil {
		return err
	}
	for {
		var h [10]byte
		if _, err := io.ReadFull(s.rd, h[:]); err != nil {
			return err
		}
		tag := binary.LittleEndian.Uint16(h[0:2])
		size := binary.LittleEndian.Uint32(h[2:6])
		if size > 64<<20 {
			return errors.New("rex: a snapshot record is too large")
		}
		if _, err := io.CopyN(io.Discard, s.rd, int64(size)); err != nil {
			return err
		}
		if tag == 6 { // FINISH
			return nil
		}
	}
}

// Read reads the stream's next bytes, after SkipSnapshot.
func (s *Stream) Read(p []byte) (int, error) { return s.rd.Read(p) }

// Close ends the stream. A stream ended this way does not wait to resume.
func (s *Stream) Close() error { return s.conn.Close(websocket.StatusNormalClosure, "") }

// dialUnix connects to a Unix socket, honoring ctx.
func dialUnix(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}

// platform names this machine, as the API reports it.
func platform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// urlParse decodes a file:// URL's path.
func urlParse(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	return u.Path, nil
}

// callTimeout is how long a call waits when its caller sets no deadline.
const callTimeout = 30 * time.Second
