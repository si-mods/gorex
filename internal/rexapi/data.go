package rexapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// dataProtocol is the subprotocol of a terminal data connection.
const dataProtocol = "rex.data.terminal.v1"

// urlNoPad is base64url without padding, which Rex tickets use.
func urlNoPad() *base64.Encoding { return base64.URLEncoding.WithPadding(base64.NoPadding) }

// ticket issues a one-time pass for a data connection.
func (s *Server) ticket(m *mSession, b *mBlock, subprotocol string) (any, *apiError) {
	if subprotocol != dataProtocol {
		return nil, errNotSupported("GoRex does not stream %s", subprotocol)
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, errInternal("%v", err)
	}
	id := urlNoPad().EncodeToString(token)
	s.mu.Lock()
	for k, t := range s.tickets {
		if time.Now().After(t.expires) {
			delete(s.tickets, k)
		}
	}
	s.tickets[id] = &ticket{
		session:     b.Session,
		block:       b.SID,
		subprotocol: subprotocol,
		expires:     time.Now().Add(60 * time.Second),
	}
	s.mu.Unlock()
	return map[string]any{"ticket": id, "expires_in_ms": 60000}, nil
}

// takeTicket spends a ticket, nil when it is unknown or expired.
func (s *Server) takeTicket(id, subprotocol string) *ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tickets[id]
	if t == nil {
		return nil
	}
	delete(s.tickets, id)
	if time.Now().After(t.expires) || t.subprotocol != subprotocol {
		return nil
	}
	return t
}

// handleData upgrades a request to a data connection, which streams one
// block's output.
func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	offered := offeredProtocols(r)
	if len(offered) == 0 {
		offered = []string{dataProtocol}
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    offered,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(1 << 20)
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	typ, hello, err := ws.Read(ctx)
	cancel()
	if err != nil || typ != websocket.MessageBinary || len(hello) < 17 {
		ws.Close(websocket.StatusPolicyViolation, "data handshake rejected")
		return
	}
	if hello[0] != 0x01 {
		// A resume needs a stream this server kept, which it does not
		// keep: REJECT, reason 0x01, as for an unknown ticket.
		ws.Write(context.Background(), websocket.MessageBinary, []byte{0x82, 0x01})
		ws.Close(websocket.StatusPolicyViolation, "data handshake rejected")
		return
	}
	id := urlNoPad().EncodeToString(hello[1:17])
	t := s.takeTicket(id, ws.Subprotocol())
	if t == nil {
		// REJECT, reason 0x01: the ticket is unknown, used or expired.
		ws.Write(context.Background(), websocket.MessageBinary, []byte{0x82, 0x01})
		ws.Close(websocket.StatusPolicyViolation, "data handshake rejected")
		return
	}
	s.streamData(ws, t)
}

// streamData sends a block's screen and output over a data connection.
func (s *Server) streamData(ws *websocket.Conn, t *ticket) {
	info := s.sessionInfo(t.block)
	cols, rows := info.Cols, info.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	conn, err := s.client.AttachConn(rex.Attach{SID: t.block, Cols: cols, Rows: rows, Framed: true})
	if err != nil {
		ws.Write(context.Background(), websocket.MessageBinary, []byte{0x82, 0x03})
		ws.Close(websocket.StatusInternalError, "data handshake rejected")
		return
	}
	defer conn.Close()
	br := bufio.NewReaderSize(conn, 64<<10)
	line, err := br.ReadBytes('\n')
	if err != nil {
		ws.Close(websocket.StatusInternalError, "data provider failed")
		return
	}
	var head rex.SnapshotHeader
	if json.Unmarshal(line, &head) != nil {
		ws.Close(websocket.StatusInternalError, "data provider failed")
		return
	}
	snapshot := make([]byte, head.Snapshot)
	if _, err := io.ReadFull(br, snapshot); err != nil {
		ws.Close(websocket.StatusInternalError, "data provider failed")
		return
	}
	nonce := uint64(0)
	var b [8]byte
	for nonce == 0 {
		if _, err := rand.Read(b[:]); err != nil {
			nonce = uint64(time.Now().UnixNano()) | 1
			break
		}
		nonce = binary.LittleEndian.Uint64(b[:])
	}
	// WELCOME: 0x81, a reconnect token, padding, the server's wait, and
	// the stream's nonce.
	welcome := make([]byte, 32)
	welcome[0] = 0x81
	rand.Read(welcome[1:17])
	binary.LittleEndian.PutUint32(welcome[20:24], 60000)
	binary.LittleEndian.PutUint64(welcome[24:32], nonce)
	if err := ws.Write(context.Background(), websocket.MessageBinary, welcome); err != nil {
		return
	}
	// The stream starts with what the screen shows, then what the program
	// prints; Rex's own messages sit between them, in APC sequences.
	resize := map[string]any{
		"type": "resize", "cols": head.Cols, "rows": head.Rows,
		"cell_width_px": 0, "cell_height_px": 0, "owner": nil,
	}
	apc := "\x1b_rex1;" + itoa(int(nonce)) + ";" + string(jsonOf(resize)) + "\x1b\\"
	if err := writeStream(ws, []byte(apc)); err != nil {
		return
	}
	if err := writeStream(ws, snapshot); err != nil {
		return
	}
	// A reader that stops is noticed by the connection closing.
	go func() {
		for {
			if _, _, err := ws.Read(context.Background()); err != nil {
				conn.Close()
				return
			}
		}
	}()
	buf := make([]byte, 64<<10)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			if werr := writeStream(ws, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			ws.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}

// writeStream sends bytes as one binary message, in pieces of 64 KiB.
func writeStream(ws *websocket.Conn, p []byte) error {
	const max = 64 << 10
	for len(p) > 0 {
		n := min(len(p), max)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := ws.Write(ctx, websocket.MessageBinary, p[:n])
		cancel()
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// sessionInfo returns the record of a session.
func (s *Server) sessionInfo(sid string) rex.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.infos[sid]
}

// offeredProtocols lists the subprotocols a request offers.
func offeredProtocols(r *http.Request) []string {
	raw := r.Header.Values("Sec-WebSocket-Protocol")
	var out []string
	for _, v := range raw {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// itoa formats an integer.
func itoa(n int) string { return jsonNumber(n) }

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
