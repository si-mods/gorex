package rexapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	// As on the control connection, Handler has checked the Origin and
	// the access rule decides who connects.
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
	tracef("data handshake: subprotocols %v, %d bytes, first %x, err %v", offered, len(msg), msg[:min(len(msg), 1)], err)
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
	tracef("data reject 0x%02x", reason)
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
	case ws.Subprotocol() == bytesProtocol:
		if t.subprotocol != bytesProtocol {
			g.welcomeThenClose(ws, 1008, "no upload is reserved for this ticket")
			return
		}
		g.startUploadStream(ws, t)
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
	tracef("data close after welcome %d %q", status, reason)
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
	binary.LittleEndian.PutUint32(w[20:24], uint32(reconnectWait/time.Millisecond))
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
	conn, err := g.opt.Client.AttachConn(rex.Attach{SID: t.sid, Snapshot: rex.SnapshotGhostty, Framed: true, Mute: true})
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
		chunks: make(chan chunk, 32), resumes: make(chan *resumeCall),
		goners: make(chan gone, 8), msgs: make(chan any, 8), done: make(chan struct{}),
		finished: make(chan struct{}), resyncs: make(chan resyncResult),
		drained: make(chan struct{}, 1),
		lists:   make(chan []rex.SessionInfo, 1),
		cols:    head.Cols, rows: head.Rows, block: t.block,
		fed: true,
	}
	token := g.rotateStreamToken(s)
	s.setWS(ws)
	g.mu.Lock()
	g.streams[key] = s
	g.mu.Unlock()
	// The stream starts with the screen, then the size that screen has
	// and who owns it, then the colors a client replaced, if any.
	var owner any
	if o := g.ownerOf(t.block); o != "" {
		owner = o
	}
	resize := resizeMessage(head.Cols, head.Rows, owner)
	s.buf = append(s.buf, snap...)
	s.buf = append(s.buf, apcMessage(nonce, resize)...)
	if th, ok := g.themeOf(t.block); ok {
		s.buf = append(s.buf, apcMessage(nonce, th.message())...)
	}
	s.total = int64(len(s.buf))
	s.mark = s.total
	if !writeData(ws, welcomeBytes(token, nonce)) {
		s.discard()
		return
	}
	// The writer sends the snapshot while the stream reads the session's
	// output on, so that a slow connection does not hold up the attach.
	s.startWriter(ws, 0)
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
	g      *Gateway
	key    string
	record string
	sid    string
	// block is the Rex block the stream shows, for its themes and owner.
	block       string
	subprotocol string
	nonce       uint64

	conn  net.Conn
	rd    *bufio.Reader
	token string

	mu     sync.Mutex
	ws     *websocket.Conn
	wsdone chan struct{}

	chunks  chan chunk
	resumes chan *resumeCall
	goners  chan gone
	// msgs carries Rex's own messages to send where the output allows.
	msgs     chan any
	done     chan struct{}
	once     sync.Once
	finished chan struct{}
	// lists carries the gateway's newest session list (listSessions).
	lists chan []rex.SessionInfo
	// resyncs carries the attach a resync made (startResync).
	resyncs chan resyncResult
	// drained tells that the writer sent all there is (write).
	drained chan struct{}

	// out guards what the run goroutine and the writer share: the
	// stream's bytes, buf, which are those from total-len(buf) to total;
	// sent, how far the writer took them; and writer, which a new
	// connection's writer counts up, to stop the one before. Only the
	// run goroutine changes buf and total.
	out    sync.Mutex
	wake   *sync.Cond
	buf    []byte
	total  int64
	sent   int64
	writer int

	// The run goroutine owns these.
	base         int64
	cols, rows   int
	exited       bool
	exitCode     int
	resume       *time.Timer
	vt           vtParser
	pending      [][]byte
	pendingBytes int
	// gen counts the attaches to the session, whose output carries it
	// (chunk); fed tells that the attach of the moment gave output after
	// its snapshot.
	gen int
	fed bool
	// mark is where the newest snapshot in the stream ends: what comes
	// before it is never dropped, so that a snapshot goes whole. behind
	// tells that the stream dropped the output it could not send, and
	// waits for a resync; resyncing, that an attach for it is under way.
	mark      int64
	behind    bool
	resyncing bool
	// resynced is when the stream sent its last resync; later waits for
	// resyncEvery to pass since.
	resynced time.Time
	later    *time.Timer
}

// chunk is output of the session's attach gen; nil p for its end.
type chunk struct {
	gen int
	p   []byte
}

// resyncResult is a new attach to the session, or why there is none.
type resyncResult struct {
	conn net.Conn
	rd   *bufio.Reader
	head rex.SnapshotHeader
	snap []byte
	err  error
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

// run adds the session's output to the stream as it comes, for the
// connection's writer (write), and handles resyncs, resumes, drops and
// the block's state.
func (s *stream) run() {
	defer close(s.finished)
	defer s.g.removeStream(s)
	defer func() { s.conn.Close() }() // the attach of the moment (resync)
	defer s.stopWriter()
	go s.readOutput(s.gen, s.rd)
	s.poll(s.g.sessionsNow())
	for {
		select {
		case c := <-s.chunks:
			switch {
			case c.gen != s.gen:
				// An attach a resync replaced.
			case c.p == nil:
				s.outputEnded()
			default:
				s.feedOutput(c.p)
			}
		case r := <-s.resyncs:
			s.applyResync(r)
		case <-s.drained:
			s.resyncIfIdle()
		case <-s.laterWait():
			s.later = nil
			s.resyncIfIdle()
		case call := <-s.resumes:
			s.resumeStream(call)
		case msg := <-s.msgs:
			s.appendApc(msg)
		case info := <-s.goners:
			if !s.clearWS(info.ws) {
				continue
			}
			s.stopWriter()
			if info.status == 1000 {
				s.closeWith(1000, "")
				return
			}
			s.startResumeTimer()
		case list := <-s.lists:
			s.poll(list)
		case <-s.resumeWait():
			s.closeWith(1000, "")
			return
		case <-s.done:
			return
		}
	}
}

// readOutput reads the output of the session's attach gen from rd into
// the stream's queue, as fast as it comes: the GoRex server drops a
// client that falls too far behind (attached.send), and a stream whose
// connection is slow drops what it cannot send itself (feedOutput).
func (s *stream) readOutput(gen int, rd io.Reader) {
	buf := make([]byte, maxChunk)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			p := append([]byte(nil), buf[:n]...)
			select {
			case s.chunks <- chunk{gen, p}:
			case <-s.done:
				return
			}
		}
		if err != nil {
			select {
			case s.chunks <- chunk{gen, nil}:
			case <-s.done:
			}
			return
		}
	}
}

// collapseBytes is the most output a stream holds that its connection did
// not take yet. Past it, the stream drops that output and goes on from a
// new snapshot (resync), which a client far behind needs sooner than old
// output. It is a quarter of what the stream keeps for a resume
// (resumeBytes), so that the bytes not sent yet stay among those, and of
// what the GoRex server lets a remote client fall behind (8 MiB,
// maxQueuedBytes): about a second of a connection of 2 MiB/s.
const collapseBytes = 2 << 20

// resyncHistory is the most scrollback, in bytes, of a resync's snapshot:
// some 3000 lines of 80 columns, an eighth of collapseBytes, so that a
// resync sends far less than the output it drops.
const resyncHistory = 256 << 10

// feedOutput adds the session's output, and Rex's messages queued for a
// safe place once the output leaves an escape sequence. Output of a
// stream behind is dropped: the resync's snapshot shows it.
func (s *stream) feedOutput(p []byte) {
	s.fed = true
	if s.behind {
		return
	}
	s.vt.feed(p)
	s.appendBytes(p)
	s.flushPending(false)
	s.out.Lock()
	// What follows the newest snapshot, and the connection did not take.
	late := s.total - max(s.sent, s.mark)
	s.out.Unlock()
	if late > int64(s.g.collapseBytes) {
		s.collapse()
	}
}

// collapse drops the bytes the connection did not take, back to the end
// of the newest snapshot at most, and resyncs the stream once the
// connection took the rest (resyncIfIdle).
func (s *stream) collapse() {
	s.dropUnsent()
	s.behind = true
	s.pending, s.pendingBytes = nil, 0
	s.resyncIfIdle()
}

// dropUnsent drops the bytes the connection did not take, back to the end
// of the newest snapshot at most, and to the oldest byte kept.
func (s *stream) dropUnsent() {
	s.out.Lock()
	defer s.out.Unlock()
	keep := max(s.sent, s.mark, s.total-int64(len(s.buf)))
	if keep < s.total {
		s.buf = s.buf[:len(s.buf)-int(s.total-keep)]
		s.total = keep
	}
}

// resyncEvery is the least time between a stream's resyncs: a program
// that prints far faster than the connection takes gets the newest screen
// once a second, rather than snapshots that fill the connection.
const resyncEvery = time.Second

// laterWait returns the wait for resyncEvery, if one is running.
func (s *stream) laterWait() <-chan time.Time {
	if s.later == nil {
		return nil
	}
	return s.later.C
}

// resyncIfIdle resyncs a stream behind once a connection took all its
// bytes, and resyncEvery passed since the last resync: the snapshot then
// goes out as soon as it is taken, and a connection gets one at a time,
// however fast the program prints. A stream nobody reads waits for a
// resume (resumeStream), so that it attaches again once at most
// meanwhile.
func (s *stream) resyncIfIdle() {
	if !s.behind || s.resyncing || !s.active() {
		return
	}
	if wait := resyncEvery - time.Since(s.resynced); wait > 0 {
		if s.later == nil {
			s.later = time.NewTimer(wait)
		}
		return
	}
	s.out.Lock()
	idle := s.sent >= s.total
	s.out.Unlock()
	if idle {
		s.startResync()
	}
}

// appendBytes adds bytes to the stream, for the writer to send.
func (s *stream) appendBytes(p []byte) {
	s.out.Lock()
	defer s.out.Unlock()
	s.buf = append(s.buf, p...)
	s.total += int64(len(p))
	// A stream that waited too long keeps only its newest bytes, which a
	// resume counted too far back cannot use, and those the writer did not
	// take yet, as the start of a large snapshot. The buffer holds up to
	// twice what it keeps before it moves them to its front, so that bytes
	// are copied once per resumeBytes rather than once per chunk.
	if len(s.buf) > resumeBytes {
		s.base = s.total - int64(resumeBytes)
		keep := s.total - min(s.base, s.sent)
		if int64(len(s.buf)) > keep+int64(resumeBytes) {
			s.buf = s.buf[:copy(s.buf, s.buf[int64(len(s.buf))-keep:])]
		}
	}
	s.cond().Broadcast()
}

// cond is the writer's wait for bytes, made at first use; s.out is held.
func (s *stream) cond() *sync.Cond {
	if s.wake == nil {
		s.wake = sync.NewCond(&s.out)
	}
	return s.wake
}

// startWriter starts sending the stream to ws from byte from, and stops
// the writer of the connection before.
func (s *stream) startWriter(ws *websocket.Conn, from int64) {
	s.out.Lock()
	s.writer++
	s.sent = from
	n := s.writer
	s.cond().Broadcast()
	s.out.Unlock()
	go s.write(ws, n)
}

// stopWriter stops the writer of the moment.
func (s *stream) stopWriter() {
	s.out.Lock()
	s.writer++
	s.cond().Broadcast()
	s.out.Unlock()
}

// write is writer n: it sends the stream's bytes to ws as they come, in
// the 64 KiB messages a data connection carries, until a newer writer or
// the stream's end stops it, or ws fails, which the run goroutine hears
// as the connection's end.
func (s *stream) write(ws *websocket.Conn, n int) {
	for {
		s.out.Lock()
		for s.writer == n && s.sent >= s.total {
			select {
			case s.drained <- struct{}{}:
			default:
			}
			s.cond().Wait()
		}
		if s.writer != n {
			s.out.Unlock()
			return
		}
		start := s.total - int64(len(s.buf))
		if s.sent < start {
			// The bytes it was to send are gone: the client starts over.
			s.out.Unlock()
			s.writeFailed(ws)
			return
		}
		i := int(s.sent - start)
		p := append([]byte(nil), s.buf[i:i+int(min(s.total-s.sent, maxChunk))]...)
		s.sent += int64(len(p))
		s.out.Unlock()
		if !writeData(ws, p) {
			s.writeFailed(ws)
			return
		}
	}
}

// writeFailed closes a connection the stream could not write, for the
// client to resume, and tells the run goroutine.
func (s *stream) writeFailed(ws *websocket.Conn) {
	ws.Close(1013, "data reconnect required")
	select {
	case s.goners <- gone{ws: ws, status: 1013}:
	case <-s.done:
	}
}

// appendApc adds one of Rex's own messages where the output is not in
// the middle of an escape sequence, so that it cannot be mistaken for
// part of one.
func (s *stream) appendApc(payload any) {
	if s.behind {
		// The resync sends the state the message would (applyResync).
		return
	}
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
		// Its writer may wait on a dead connection for a while: the close
		// must not hold up the session's output.
		go old.Close(1000, "stream moved")
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
	s.startWriter(call.ws, int64(call.count))
	s.resyncIfIdle()
}

// startResumeTimer starts the wait for a resume after a connection
// dropped.
func (s *stream) startResumeTimer() {
	if s.resume == nil {
		s.resume = time.NewTimer(s.g.reconnectWait)
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

// poll sends the block's new size, its exit, and whether it closed, as
// list tells.
func (s *stream) poll(list []rex.SessionInfo) {
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
		s.exited, s.exitCode = true, info.ExitCode
		s.appendApc(map[string]any{"type": "child_exited", "exit_code": info.ExitCode})
	}
	if info.Cols != s.cols || info.Rows != s.rows {
		s.cols, s.rows = info.Cols, info.Rows
		var owner any
		if o := s.g.ownerOf(s.block); o != "" {
			owner = o
		}
		s.appendApc(resizeMessage(s.cols, s.rows, owner))
	}
}

// inject queues one of Rex's messages for the stream's output.
func (s *stream) inject(payload any) {
	select {
	case s.msgs <- payload:
	case <-s.done:
	default:
		// A stream far behind keeps only the newest state: the next
		// message says it all.
	}
}

// session tells whether the server still has the stream's session, and
// whether and how it exited.
func (s *stream) session() (found, exited bool, code int) {
	for _, info := range s.g.sessionsNow() {
		if info.ID == s.sid {
			return true, info.Exited, info.ExitCode
		}
	}
	return false, false, 0
}

// outputEnded handles the end of the GoRex session's output.
func (s *stream) outputEnded() {
	found, exited, code := s.session()
	switch {
	case !found:
		s.closeWith(4000, "block closed")
	case s.resyncing:
		// The resync's attach replaces this one.
	case s.behind, !exited && s.fed:
		// The stream dropped output, and needs the newest screen; or the
		// server dropped the attach, as it drops a client far behind
		// (attached.send), though the stream reads all the time.
		s.startResync()
	case exited:
		s.ended(code)
	default:
		// The attach gave nothing past its snapshot.
		s.closeWith(1011, "data provider failed")
	}
}

// ended sends child_exited, once: no more output follows, but the stream
// stays open, as the doc says. No output follows the message, so a
// sequence left open ends here.
func (s *stream) ended(code int) {
	s.vt = vtParser{}
	s.flushPending(true)
	if !s.exited {
		s.exited, s.exitCode = true, code
		s.appendApc(map[string]any{"type": "child_exited", "exit_code": code})
	}
}

// resyncReset starts a resync: ST ends a string the output left open,
// the client's own read of Rex's messages too, and an escape sequence;
// then a reset clears the screen and scrollback for the snapshot that
// follows.
const resyncReset = "\x1b\\\x1bc\x1b[H\x1b[2J\x1b[3J"

// startResync attaches to the session again, in the background, for
// applyResync, unless an attach is under way.
func (s *stream) startResync() {
	if s.resyncing {
		return
	}
	s.resyncing = true
	go func() {
		r := s.attachAgain()
		// resyncs has no buffer: the run goroutine takes the attach, or the
		// stream ended and the attach is closed here.
		select {
		case s.resyncs <- r:
		case <-s.done:
			if r.conn != nil {
				r.conn.Close()
			}
		}
	}()
}

// attachAgain attaches to the session for a snapshot of its screen in
// escape sequences, with resyncHistory of its scrollback, and reads it.
func (s *stream) attachAgain() resyncResult {
	conn, err := s.g.opt.Client.AttachConn(rex.Attach{SID: s.sid, Framed: true, History: resyncHistory, Mute: true})
	if err != nil {
		return resyncResult{err: err}
	}
	br := bufio.NewReaderSize(conn, 64<<10)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := br.ReadBytes('\n')
	var head rex.SnapshotHeader
	if err == nil {
		err = json.Unmarshal(line, &head)
	}
	var snap []byte
	if err == nil {
		snap = make([]byte, max(head.Snapshot, 0))
		_, err = io.ReadFull(br, snap)
	}
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return resyncResult{err: err}
	}
	return resyncResult{conn: conn, rd: br, head: head, snap: snap}
}

// applyResync goes on with the attach a resync made: the stream drops
// the bytes the connection did not take, which the snapshot shows newer,
// and sends a reset; the size, before the screen of that size; the
// terminal_reset message, for the client to set again what it set on the
// terminal itself; the snapshot; and the state Rex's messages gave that
// the dropped bytes may have held. The stream then reads the new
// attach's output. Without an attach, the stream ends as the session
// says: 4000 once it is gone, child_exited once it exited, else 1011.
func (s *stream) applyResync(r resyncResult) {
	s.resyncing = false
	if r.err != nil {
		found, exited, code := s.session()
		switch {
		case !found:
			s.closeWith(4000, "block closed")
		case exited:
			s.behind = false
			s.ended(code)
		default:
			s.closeWith(1011, "data provider failed")
		}
		return
	}
	s.dropUnsent()
	s.conn.Close()
	s.gen++
	s.conn, s.rd = r.conn, r.rd
	s.behind, s.fed = false, false
	s.resynced = time.Now()
	s.vt = vtParser{}
	s.pending, s.pendingBytes = nil, 0
	s.cols, s.rows = r.head.Cols, r.head.Rows
	var owner any
	if o := s.g.ownerOf(s.block); o != "" {
		owner = o
	}
	s.appendBytes([]byte(resyncReset))
	s.appendBytes(apcMessage(s.nonce, resizeMessage(s.cols, s.rows, owner)))
	s.appendBytes(apcMessage(s.nonce, map[string]any{"type": "terminal_reset"}))
	s.vt.feed(r.snap)
	s.appendBytes(r.snap)
	if th, ok := s.g.themeOf(s.block); ok {
		s.appendApc(th.message())
	}
	if s.exited {
		s.vt = vtParser{}
		s.appendApc(map[string]any{"type": "child_exited", "exit_code": s.exitCode})
	}
	s.out.Lock()
	s.mark = s.total
	s.out.Unlock()
	go s.readOutput(s.gen, r.rd)
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
		s.stopWriter()
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
