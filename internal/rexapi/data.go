package rexapi

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// base64url writes bytes as a Rex ticket: base64url without padding.
func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// The reasons of a data connection's REJECT, as the doc lists them.
const (
	rejectTicket      = 0x01 // unknown, expired, used, or another user's
	rejectInUse       = 0x02 // the client already streams this source
	rejectUnavailable = 0x03 // the stream is not available
	rejectLimited     = 0x04
	rejectMalformed   = 0x05
	rejectFailed      = 0x06
	rejectProtocol    = 0x07
)

// handleData upgrades a request to a data connection and runs its
// handshake.
func (g *Gateway) handleData(w http.ResponseWriter, r *http.Request) {
	offered := offeredProtocols(r)
	// As on the control connection, the access rule decides who connects.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       offered,
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessage)
	ctx, cancel := context.WithTimeout(context.Background(), g.helloWait)
	typ, msg, err := ws.Read(ctx)
	cancel()
	if err != nil {
		ws.CloseNow()
		return
	}
	if typ != websocket.MessageBinary || len(msg) == 0 {
		rejectData(ws, rejectMalformed)
		return
	}
	switch msg[0] {
	case 0x01:
		g.dataHello(ws, msg[1:])
	case 0x02:
		g.dataResume(ws, msg[1:])
	default:
		rejectData(ws, rejectMalformed)
	}
}

// rejectData answers a handshake with a REJECT, then closes the
// connection with the reason the doc gives.
func rejectData(ws *websocket.Conn, reason byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws.Write(ctx, websocket.MessageBinary, []byte{0x82, reason})
	status := websocket.StatusPolicyViolation
	switch reason {
	case rejectTicket, rejectInUse, rejectProtocol:
		status = websocket.StatusPolicyViolation
	case rejectUnavailable:
		status = 1013
	case rejectMalformed:
		status = websocket.StatusProtocolError
	case rejectFailed:
		status = websocket.StatusInternalError
	}
	ws.Close(status, "data handshake rejected")
}

// dataHello opens the stream a ticket names.
func (g *Gateway) dataHello(ws *websocket.Conn, rest []byte) {
	if len(rest) < 16 {
		rejectData(ws, rejectMalformed)
		return
	}
	t, reason := g.takeTicket(base64url(rest[:16]), ws.Subprotocol())
	if t == nil || reason == rejectTicket || reason == rejectProtocol {
		rejectData(ws, reason)
		return
	}
	switch {
	case reason == rejectUnavailable:
		g.welcomeThenClose(ws, 1008, "data provider unavailable")
	case t.subprotocol != "" && t.subprotocol != terminalProtocol:
		g.welcomeThenClose(ws, 1008, "data provider unavailable")
	case ws.Subprotocol() != terminalProtocol:
		g.welcomeThenClose(ws, 1008, "data provider unavailable")
	case !g.hello.Can(rex.CapabilitySnapshotGhostty):
		// A stream starts with a Ghostty snapshot, which the server has
		// not taken before this capability.
		g.welcomeThenClose(ws, 1008, "data provider unavailable")
	default:
		g.startStream(ws, t)
	}
}

// welcomeThenClose sends a WELCOME, which a handshake that ends after it
// still owes the client, then closes with a status.
func (g *Gateway) welcomeThenClose(ws *websocket.Conn, status websocket.StatusCode, reason string) {
	nonce := nonzeroNonce()
	token := randomBytes(16)
	if !writeData(ws, welcomeBytes(token, nonce)) {
		ws.CloseNow()
		return
	}
	ws.Close(status, reason)
}

// nonzeroNonce returns a channel nonce that is never zero.
func nonzeroNonce() uint64 {
	for {
		var b [8]byte
		copy(b[:], randomBytes(8))
		if n := binary.LittleEndian.Uint64(b[:]); n != 0 {
			return n
		}
	}
}

// welcomeBytes builds a WELCOME message.
func welcomeBytes(token []byte, nonce uint64) []byte {
	w := make([]byte, 32)
	w[0] = 0x81
	copy(w[1:17], token)
	binary.LittleEndian.PutUint32(w[20:24], uint32(ticketWait/time.Millisecond))
	binary.LittleEndian.PutUint64(w[24:32], nonce)
	return w
}

// startStream attaches to the block's GoRex session and opens its stream.
func (g *Gateway) startStream(ws *websocket.Conn, t *ticket) {
	key := t.record + "\x00" + t.block + "\x00" + t.dataRef
	g.mu.Lock()
	old := g.streams[key]
	g.mu.Unlock()
	if old != nil {
		if old.active() {
			rejectData(ws, rejectInUse)
			return
		}
		// A HELLO starts over a stream that waits for a resume.
		old.closeWith(1000, "")
		select {
		case <-old.finished:
		case <-time.After(5 * time.Second):
		}
	}
	conn, err := g.opt.Client.AttachConn(rex.Attach{SID: t.sid, Snapshot: rex.SnapshotGhostty, Framed: true})
	if err != nil {
		rejectData(ws, rejectUnavailable)
		return
	}
	br := bufio.NewReaderSize(conn, 64<<10)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := br.ReadBytes('\n')
	var head rex.SnapshotHeader
	if err == nil {
		err = json.Unmarshal(line, &head)
	}
	if err == nil && head.Format != rex.SnapshotGhostty {
		// The server fell back to escape sequences, which a Rex stream
		// cannot start with: it carries a binary snapshot.
		conn.Close()
		g.welcomeThenClose(ws, 1008, "data provider unavailable")
		return
	}
	var snap []byte
	if err == nil {
		snap = make([]byte, max(head.Snapshot, 0))
		_, err = io.ReadFull(br, snap)
	}
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		rejectData(ws, rejectUnavailable)
		return
	}
	nonce := nonzeroNonce()
	s := &stream{
		g: g, key: key, record: t.record, sid: t.sid,
		subprotocol: terminalProtocol, nonce: nonce,
		conn: conn, rd: br,
		chunks: make(chan []byte, 32), resumes: make(chan *resumeCall),
		goners: make(chan gone, 8), done: make(chan struct{}),
		finished: make(chan struct{}),
		cols:     head.Cols, rows: head.Rows,
	}
	token := g.rotateStreamToken(s)
	s.setWS(ws)
	g.mu.Lock()
	g.streams[key] = s
	g.mu.Unlock()
	// The stream starts with the screen, then the size that screen has.
	resize := resizeMessage(head.Cols, head.Rows, nil)
	s.buf = append(s.buf, snap...)
	s.buf = append(s.buf, apcMessage(nonce, resize)...)
	s.total = int64(len(s.buf))
	welcome := welcomeBytes(token, nonce)
	if !writeData(ws, welcome) || writeStream(ws, s.buf) != nil {
		s.discard()
		return
	}
	go s.run()
	go s.watch(ws)
	s.startPing(ws, s.wsDone())
}

// dataResume continues a stream whose connection dropped.
func (g *Gateway) dataResume(ws *websocket.Conn, rest []byte) {
	if len(rest) < 24 {
		rejectData(ws, rejectMalformed)
		return
	}
	token := string(rest[:16])
	count := binary.LittleEndian.Uint64(rest[16:24])
	s := g.streamFor(token)
	if s == nil {
		rejectData(ws, rejectTicket)
		return
	}
	if s.subprotocol != "" && s.subprotocol != ws.Subprotocol() {
		rejectData(ws, rejectProtocol)
		return
	}
	call := &resumeCall{ws: ws, count: count, reply: make(chan error, 1)}
	select {
	case s.resumes <- call:
	case <-s.done:
		rejectData(ws, rejectTicket)
		return
	}
	err := <-call.reply
	if err != nil {
		rejectData(ws, rejectReason(err))
		return
	}
	go s.watch(ws)
	s.startPing(ws, s.wsDone())
}

// resumeError is why a resume did not continue a stream.
type resumeError struct{ reason byte }

func (e resumeError) Error() string { return "resume refused" }

// rejectReason is the REJECT reason of a resume error.
func rejectReason(err error) byte {
	var e resumeError
	if errors.As(err, &e) {
		return e.reason
	}
	return rejectTicket
}

// stream is one block's data stream, across the connections it is sent
// on.
type stream struct {
	g           *Gateway
	key         string
	record      string
	sid         string
	subprotocol string
	nonce       uint64

	conn  net.Conn
	rd    *bufio.Reader
	token string

	mu     sync.Mutex
	ws     *websocket.Conn
	wsdone chan struct{}

	chunks   chan []byte
	resumes  chan *resumeCall
	goners   chan gone
	done     chan struct{}
	once     sync.Once
	finished chan struct{}

	// The run goroutine owns these.
	buf          []byte
	total        int64
	base         int64
	cols, rows   int
	exited       bool
	resume       *time.Timer
	vt           vtParser
	pending      [][]byte
	pendingBytes int
}

// resumeCall asks the run goroutine to continue a stream on a new
// connection.
type resumeCall struct {
	ws    *websocket.Conn
	count uint64
	reply chan error
}

// gone tells the run goroutine that a connection ended.
type gone struct {
	ws     *websocket.Conn
	status websocket.StatusCode
}

// active tells whether a connection is receiving the stream now.
func (s *stream) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ws != nil
}

// currentWS returns the connection receiving the stream, if any.
func (s *stream) currentWS() *websocket.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ws
}

// setWS makes ws the stream's connection.
func (s *stream) setWS(ws *websocket.Conn) {
	s.mu.Lock()
	if s.wsdone != nil {
		close(s.wsdone)
	}
	s.ws = ws
	done := make(chan struct{})
	s.wsdone = done
	s.mu.Unlock()
}

// wsDone returns the current connection's done channel.
func (s *stream) wsDone() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wsdone
}

// clearWS forgets ws, if it is still the stream's.
func (s *stream) clearWS(ws *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws != ws {
		return false
	}
	s.ws = nil
	if s.wsdone != nil {
		close(s.wsdone)
		s.wsdone = nil
	}
	return true
}

// run sends the stream's bytes to its connection as they come, and
// handles resumes, drops and the block's state.
func (s *stream) run() {
	defer close(s.finished)
	defer s.g.removeStream(s)
	defer s.conn.Close()
	go s.readOutput()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case p := <-s.chunks:
			if p == nil {
				s.outputEnded()
			} else {
				s.feedOutput(p)
			}
		case call := <-s.resumes:
			s.resumeStream(call)
		case info := <-s.goners:
			if !s.clearWS(info.ws) {
				continue
			}
			if info.status == 1000 {
				s.closeWith(1000, "")
				return
			}
			s.startResumeTimer()
		case <-ticker.C:
			s.poll()
		case <-s.resumeWait():
			s.closeWith(1000, "")
			return
		case <-s.done:
			return
		}
	}
}

// readOutput reads the GoRex session's output into the stream's queue.
func (s *stream) readOutput() {
	buf := make([]byte, maxChunk)
	for {
		n, err := s.rd.Read(buf)
		if n > 0 {
			p := append([]byte(nil), buf[:n]...)
			select {
			case s.chunks <- p:
			case <-s.done:
				return
			}
		}
		if err != nil {
			select {
			case s.chunks <- nil:
			case <-s.done:
			}
			return
		}
	}
}

// feedOutput adds the session's output, and Rex's messages queued for a
// safe place once the output leaves an escape sequence.
func (s *stream) feedOutput(p []byte) {
	s.vt.feed(p)
	s.appendBytes(p)
	s.flushPending(false)
}

// appendBytes adds bytes to the stream and sends them to the connection.
func (s *stream) appendBytes(p []byte) {
	s.buf = append(s.buf, p...)
	s.total += int64(len(p))
	if ws := s.currentWS(); ws != nil {
		if err := writeStream(ws, p); err != nil {
			if s.clearWS(ws) {
				ws.Close(1013, "data reconnect required")
				s.startResumeTimer()
			}
		}
	}
	// A stream that waited too long keeps only its newest bytes, which a
	// resume counted too far back cannot use.
	if drop := len(s.buf) - resumeBytes; drop > 0 {
		s.buf = append([]byte(nil), s.buf[drop:]...)
		s.base += int64(drop)
	}
}

// appendApc adds one of Rex's own messages where the output is not in
// the middle of an escape sequence, so that it cannot be mistaken for
// part of one.
func (s *stream) appendApc(payload any) {
	msg := apcMessage(s.nonce, payload)
	if s.vt.ground() {
		s.appendBytes(msg)
		return
	}
	s.pending = append(s.pending, msg)
	s.pendingBytes += len(msg)
	if s.pendingBytes > maxChunk {
		// A program that leaves a sequence open must not hold Rex's
		// messages forever. The message goes where it is, which is the
		// best this server can do for a stream that is not text.
		s.flushPending(true)
	}
}

// flushPending writes the messages waiting for a safe place, when the
// output left its escape sequences, or when force says to.
func (s *stream) flushPending(force bool) {
	if !force && !s.vt.ground() {
		return
	}
	for _, msg := range s.pending {
		s.appendBytes(msg)
	}
	s.pending, s.pendingBytes = nil, 0
}

// vtParser follows enough of a terminal's escape sequences to know
// whether more output would continue one.
type vtParser struct {
	state int
}

const (
	vtGround = iota
	vtEscape
	vtCSI
	vtString
	vtStringEscape
)

// feed reads output to track its escape sequences.
func (p *vtParser) feed(b []byte) {
	for _, c := range b {
		switch p.state {
		case vtGround:
			if c == 0x1b {
				p.state = vtEscape
			}
		case vtEscape:
			switch c {
			case '[':
				p.state = vtCSI
			case ']', 'P', 'X', '^', '_':
				p.state = vtString
			default:
				p.state = vtGround
			}
		case vtCSI:
			switch {
			case c == 0x1b:
				p.state = vtEscape
			case c >= 0x40 && c <= 0x7e:
				p.state = vtGround
			}
		case vtString:
			switch c {
			case 0x07:
				p.state = vtGround
			case 0x1b:
				p.state = vtStringEscape
			}
		case vtStringEscape:
			if c == '\\' {
				p.state = vtGround
			} else {
				p.state = vtString
			}
		}
	}
}

// ground tells whether more output would start something new.
func (p *vtParser) ground() bool { return p.state == vtGround }

// resumeStream continues the stream on a new connection, from the byte
// the client says it has used.
func (s *stream) resumeStream(call *resumeCall) {
	select {
	case <-s.done:
		call.reply <- resumeError{rejectTicket}
		return
	default:
	}
	if call.count > uint64(s.total) {
		call.reply <- resumeError{rejectMalformed}
		s.closeWith(1013, "data reconnect required")
		return
	}
	if int64(call.count) < s.base {
		call.reply <- resumeError{rejectUnavailable}
		s.closeWith(1013, "data reconnect required")
		return
	}
	if old := s.currentWS(); old != nil && old != call.ws {
		s.clearWS(old)
		old.Close(1000, "stream moved")
	}
	s.setWS(call.ws)
	token := s.g.rotateStreamToken(s)
	s.stopResumeTimer()
	if !writeData(call.ws, welcomeBytes(token, s.nonce)) {
		s.clearWS(call.ws)
		call.reply <- resumeError{rejectFailed}
		return
	}
	call.reply <- nil
	from := int64(call.count) - s.base
	if err := writeStream(call.ws, s.buf[from:]); err != nil {
		if s.clearWS(call.ws) {
			call.ws.Close(1013, "data reconnect required")
			s.startResumeTimer()
		}
	}
}

// startResumeTimer starts the wait for a resume after a connection
// dropped.
func (s *stream) startResumeTimer() {
	if s.resume == nil {
		s.resume = time.NewTimer(reconnectWait)
	}
}

// stopResumeTimer stops the wait for a resume.
func (s *stream) stopResumeTimer() {
	if s.resume != nil {
		s.resume.Stop()
		s.resume = nil
	}
}

// resumeWait returns the wait for a resume, if one is running.
func (s *stream) resumeWait() <-chan time.Time {
	if s.resume == nil {
		return nil
	}
	return s.resume.C
}

// poll sends the block's new size, its exit, and whether it closed.
func (s *stream) poll() {
	list, err := s.g.opt.Client.List()
	if err != nil {
		return
	}
	var info *rex.SessionInfo
	for i := range list {
		if list[i].ID == s.sid {
			info = &list[i]
			break
		}
	}
	if info == nil {
		s.closeWith(4000, "block closed")
		return
	}
	if info.Exited && !s.exited {
		s.exited = true
		s.appendApc(map[string]any{"type": "child_exited", "exit_code": info.ExitCode})
	}
	if info.Cols != s.cols || info.Rows != s.rows {
		s.cols, s.rows = info.Cols, info.Rows
		s.appendApc(resizeMessage(s.cols, s.rows, nil))
	}
}

// outputEnded handles the end of the GoRex session's output.
func (s *stream) outputEnded() {
	list, err := s.g.opt.Client.List()
	exited, found, code := false, false, 0
	if err == nil {
		for _, info := range list {
			if info.ID == s.sid {
				found, exited, code = true, info.Exited, info.ExitCode
				break
			}
		}
	}
	switch {
	case !found:
		s.closeWith(4000, "block closed")
	case exited:
		// child_exited says no more output follows, but the stream stays
		// open, as the doc says. No output follows the message, so a
		// sequence left open ends here.
		s.vt = vtParser{}
		s.flushPending(true)
		if !s.exited {
			s.exited = true
			s.appendApc(map[string]any{"type": "child_exited", "exit_code": code})
		}
	default:
		s.closeWith(1011, "data provider failed")
	}
}

// apcMessage wraps JSON in the APC escape sequence Rex uses.
func apcMessage(nonce uint64, payload any) []byte {
	data, _ := json.Marshal(payload)
	return []byte("\x1b_rex1;" + strconv.FormatUint(nonce, 10) + ";" + string(data) + "\x1b\\")
}

// resizeMessage is a resize message's fields.
func resizeMessage(cols, rows int, owner any) map[string]any {
	return map[string]any{
		"type": "resize", "cols": cols, "rows": rows,
		"cell_width_px": 0, "cell_height_px": 0, "owner": owner,
	}
}

// discard ends a stream whose first connection did not take it, before
// its run goroutine started.
func (s *stream) discard() {
	s.g.removeStream(s)
	s.conn.Close()
	s.once.Do(func() { close(s.done) })
}

// closeWith closes the stream, its connection, and its session attach.
func (s *stream) closeWith(status websocket.StatusCode, reason string) {
	s.once.Do(func() {
		s.mu.Lock()
		ws := s.ws
		s.ws = nil
		if s.wsdone != nil {
			close(s.wsdone)
			s.wsdone = nil
		}
		s.mu.Unlock()
		if ws != nil {
			ws.Close(status, reason)
		}
		close(s.done)
	})
}

// watch reads the connection, which the client only writes nothing on,
// so that its end is noticed.
func (s *stream) watch(ws *websocket.Conn) {
	for {
		if _, _, err := ws.Read(context.Background()); err != nil {
			status := websocket.CloseStatus(err)
			select {
			case s.goners <- gone{ws: ws, status: status}:
			case <-s.done:
			}
			return
		}
	}
}

// startPing pings a data connection on Rex's schedule until it ends.
func (s *stream) startPing(ws *websocket.Conn, done chan struct{}) {
	if done == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(keepaliveEvery)
		defer ticker.Stop()
		misses := 0
		for {
			select {
			case <-done:
				return
			case <-s.done:
				return
			case <-ticker.C:
			}
			ctx, cancel := context.WithTimeout(context.Background(), keepaliveWait)
			err := ws.Ping(ctx)
			late := errors.Is(ctx.Err(), context.DeadlineExceeded)
			cancel()
			switch {
			case err == nil:
				misses = 0
			case late:
				misses++
				if misses >= 3 {
					ws.Close(1001, "keepalive timeout")
					return
				}
			default:
				return
			}
		}
	}()
}

// writeData writes one binary message.
func writeData(ws *websocket.Conn, p []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageBinary, p) == nil
}

// writeStream writes bytes in the 64 KiB messages a data connection
// carries.
func writeStream(ws *websocket.Conn, p []byte) error {
	for len(p) > 0 {
		n := min(len(p), maxChunk)
		if !writeData(ws, p[:n]) {
			return fmt.Errorf("data connection closed")
		}
		p = p[n:]
	}
	return nil
}

// offeredProtocols lists the subprotocols a request offers.
func offeredProtocols(r *http.Request) []string {
	var out []string
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
