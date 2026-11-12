// Package wsclient is a small WebSocket client (RFC 6455) over a
// connection the caller dialed, for the web client's tailnet engine
// (web/tailnet): it speaks to a GoRex gateway over TLS on the engine's
// userspace network stack, where neither the browser's WebSocket nor
// github.com/coder/websocket's dialer (none under js) can go. Unlike
// golang.org/x/net/websocket it reports the peer's close code, which the
// web client reads (4000: the block closed), and joins fragmented
// messages.
//
// It has no build constraint, so its test runs with the desktop's.
package wsclient

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Message kinds, as the frames' opcodes.
const (
	Text   = 1
	Binary = 2
)

const (
	opContinuation = 0
	opClose        = 8
	opPing         = 9
	opPong         = 10
)

// MaxMessage is the largest message Read takes; a larger one closes the
// connection with 1009, as a browser would.
const MaxMessage = 64 << 20

// CloseError is how a connection ended: the peer's close code and reason,
// or 1006 when it ended without a close frame.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string { return fmt.Sprintf("websocket closed (%d %s)", e.Code, e.Reason) }

// Conn is one client connection. Read is for one goroutine; Write and
// Close may be called from any.
type Conn struct {
	c        net.Conn
	r        *bufio.Reader
	wmu      sync.Mutex
	closed   bool
	Protocol string
}

// Handshake asks for a WebSocket at path on host over c, with origin and
// protocol, and returns the connection once the server switched
// protocols. A refusal is an error with the server's status line and the
// start of its body.
func Handshake(c net.Conn, host, path, origin, protocol string) (*Conn, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n", path, host)
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", key)
	if origin != "" {
		fmt.Fprintf(&b, "Origin: %s\r\n", origin)
	}
	if protocol != "" {
		fmt.Fprintf(&b, "Sec-WebSocket-Protocol: %s\r\n", protocol)
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(c, b.String()); err != nil {
		return nil, err
	}
	r := bufio.NewReader(c)
	res, err := http.ReadResponse(r, &http.Request{Method: "GET"})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		return nil, &RefusedError{Status: res.StatusCode, Body: string(body)}
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if res.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return nil, errors.New("websocket: bad Sec-WebSocket-Accept")
	}
	got := res.Header.Get("Sec-WebSocket-Protocol")
	if protocol != "" && got != protocol {
		return nil, fmt.Errorf("websocket: the server chose protocol %q, not %q", got, protocol)
	}
	return &Conn{c: c, r: r, Protocol: got}, nil
}

// RefusedError is a handshake the server answered with another status.
type RefusedError struct {
	Status int
	Body   string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("websocket refused: %d %s", e.Status, strings.TrimSpace(e.Body))
}

// Read returns the next message and its kind. Pings are answered, and a
// close frame is answered and returned as a *CloseError.
func (c *Conn) Read() (kind int, data []byte, err error) {
	for {
		fin, op, payload, err := c.frame()
		if err != nil {
			return 0, nil, c.lost(err)
		}
		switch op {
		case opPing:
			c.write(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			ce := &CloseError{Code: 1005}
			if len(payload) >= 2 {
				ce.Code = int(binary.BigEndian.Uint16(payload))
				ce.Reason = string(payload[2:])
			}
			c.write(opClose, payload[:min(len(payload), 2)])
			c.c.Close()
			return 0, nil, ce
		case Text, Binary:
		default:
			c.Close(1002, "unexpected opcode")
			return 0, nil, &CloseError{Code: 1002}
		}
		kind, data = int(op), payload
		for !fin {
			var more []byte
			var op2 byte
			fin, op2, more, err = c.frame()
			if err != nil {
				return 0, nil, c.lost(err)
			}
			switch op2 {
			case opPing:
				c.write(opPong, more)
				fin = false
				continue
			case opPong:
				fin = false
				continue
			case opContinuation:
			default:
				c.Close(1002, "expected a continuation")
				return 0, nil, &CloseError{Code: 1002}
			}
			if len(data)+len(more) > MaxMessage {
				c.Close(1009, "message too big")
				return 0, nil, &CloseError{Code: 1009}
			}
			data = append(data, more...)
		}
		return kind, data, nil
	}
}

func (c *Conn) lost(err error) error {
	c.c.Close()
	var ce *CloseError
	if errors.As(err, &ce) {
		return ce
	}
	return &CloseError{Code: 1006, Reason: err.Error()}
}

// frame reads one frame from the server, which sends them unmasked.
func (c *Conn) frame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.r, h[:]); err != nil {
		return
	}
	fin, op = h[0]&0x80 != 0, h[0]&0x0f
	if h[1]&0x80 != 0 {
		return false, 0, nil, &CloseError{Code: 1002, Reason: "masked frame from the server"}
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var e [2]byte
		if _, err = io.ReadFull(c.r, e[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(e[:]))
	case 127:
		var e [8]byte
		if _, err = io.ReadFull(c.r, e[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(e[:])
	}
	if n > MaxMessage {
		return false, 0, nil, &CloseError{Code: 1009, Reason: "message too big"}
	}
	payload = make([]byte, n)
	_, err = io.ReadFull(c.r, payload)
	return
}

// Write sends one message of kind (Text or Binary) in one frame.
func (c *Conn) Write(kind int, data []byte) error {
	return c.write(byte(kind), data)
}

func (c *Conn) write(op byte, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if op == opClose {
		c.closed = true
	}
	h := make([]byte, 0, 14+len(data))
	h = append(h, 0x80|op)
	switch n := len(data); {
	case n < 126:
		h = append(h, 0x80|byte(n))
	case n <= 0xffff:
		h = append(h, 0x80|126)
		h = binary.BigEndian.AppendUint16(h, uint16(n))
	default:
		h = append(h, 0x80|127)
		h = binary.BigEndian.AppendUint64(h, uint64(n))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	h = append(h, mask[:]...)
	start := len(h)
	h = append(h, data...)
	for i := range data {
		h[start+i] ^= mask[i&3]
	}
	_, err := c.c.Write(h)
	return err
}

// Close sends a close frame with code and reason, and closes the
// connection once the server's close arrives (Read returns it) or at once
// when it cannot be sent.
func (c *Conn) Close(code int, reason string) error {
	p := binary.BigEndian.AppendUint16(nil, uint16(code))
	p = append(p, reason[:min(len(reason), 123)]...)
	if err := c.write(opClose, p); err != nil {
		return c.c.Close()
	}
	return nil
}

// Abort closes the connection without a close frame.
func (c *Conn) Abort() error { return c.c.Close() }
