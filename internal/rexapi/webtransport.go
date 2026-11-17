package rexapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

const (
	wtStreamSocket byte = 0x01
	wtStreamHTTP   byte = 0x03

	wtFrameText   byte = 0x01
	wtFrameBinary byte = 0x02
	wtFrameClose  byte = 0x03

	wtMaxFrame = 16 << 20 // 16 MiB
)

type wtListener struct {
	g     *Gateway
	certs *wtCertManager

	mu     sync.Mutex
	srv    *webtransport.Server
	pc     net.PacketConn
	ln     *quic.EarlyListener
	// served closes when the goroutine accepting on ln has returned.
	served chan struct{}
	addr   string
	listen string
	err    string
}

func newWTListener(g *Gateway, dir string) *wtListener {
	return &wtListener{
		g:     g,
		certs: newWTCertManager(dir),
	}
}

// wtListenAddr derives the UDP listen address from the LAN HTTP listen
// address by incrementing the port by 1 (7297 -> 7298). Port 0 stays 0.
func wtListenAddr(httpListen string) (string, error) {
	host, portStr, err := net.SplitHostPort(httpListen)
	if err != nil {
		return "", err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", err
	}
	if port == 0 {
		return net.JoinHostPort(host, "0"), nil
	}
	return net.JoinHostPort(host, strconv.Itoa(port+1)), nil
}

// start listens for WebTransport beside the HTTP listener at httpListen.
// A CONNECT is admitted as an HTTP request on the local network is
// (lanListener.admit), the pairing token with it; the session's streams
// then need no token of their own (withWTAuth).
func (w *wtListener) start(httpListen string) {
	addr, err := wtListenAddr(httpListen)
	if err != nil {
		w.mu.Lock()
		w.err = err.Error()
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	if w.srv != nil && w.listen == addr {
		w.mu.Unlock()
		return
	}
	w.closeLocked()
	w.listen = addr
	w.err = ""

	if _, _, err := w.certs.get(); err != nil {
		w.err = err.Error()
		w.mu.Unlock()
		return
	}

	pc, err := net.ListenPacket("udp", addr)
	// A restart, as a new pairing token makes, binds the port the old
	// server has just let go: give the system a moment to free it.
	for i := 0; err != nil && i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		pc, err = net.ListenPacket("udp", addr)
	}
	if err != nil {
		w.err = err.Error()
		w.mu.Unlock()
		logf("lan webtransport: %v", err)
		return
	}

	mux := http.NewServeMux()
	wt := &webtransport.Server{
		H3: &http3.Server{
			TLSConfig: &tls.Config{
				GetCertificate: w.certs.getCertificate,
				NextProtos:     []string{http3.NextProtoH3},
			},
			Handler: mux,
		},
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			return w.g.web.allowed(origin)
		},
	}
	webtransport.ConfigureHTTP3Server(wt.H3)

	mux.HandleFunc("/api/wt", func(rw http.ResponseWriter, r *http.Request) {
		if !w.g.checkOrigin(rw, r) {
			return
		}
		if checksHost(transportLAN) && !w.g.hostAllowed(r) {
			refuseHost(rw, r)
			return
		}
		if w.g.lan == nil {
			writeRefusal(rw, "the local network listener is off")
			return
		}
		if _, ok := w.g.lan.admit(rw, r); !ok {
			return
		}
		sess, err := wt.Upgrade(rw, r)
		if err != nil {
			tracef("webtransport upgrade: %v", err)
			return
		}
		go w.serveSession(sess, r.Header.Get("Origin"), r.Host)
	})

	// The QUIC listener is made here, not in wt.Serve: Serve's
	// WaitGroup.Add would race Close's Wait when a restart closes the
	// server before its Serve goroutine got going. Each connection goes
	// to ServeQUICConn, which Close ends.
	ln, err := quic.ListenEarly(pc, wt.H3.TLSConfig, &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true})
	if err != nil {
		pc.Close()
		w.err = err.Error()
		w.mu.Unlock()
		logf("lan webtransport: %v", err)
		return
	}
	served := make(chan struct{})
	w.pc = pc
	w.ln = ln
	w.srv = wt
	w.served = served
	w.addr = pc.LocalAddr().String()
	w.mu.Unlock()

	logf("lan webtransport: listening on https://%s/api/wt", pc.LocalAddr())
	go func() {
		defer close(served)
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				return
			}
			go func() {
				if err := wt.ServeQUICConn(conn); err != nil && !errors.Is(err, http.ErrServerClosed) {
					tracef("lan webtransport: %v", err)
				}
			}()
		}
	}()
}

func (w *wtListener) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeLocked()
}

// closeLocked stops the server: its sessions first, while the socket can
// still tell the peers, then the listener and the socket.
func (w *wtListener) closeLocked() {
	if w.srv != nil {
		w.srv.Close()
		w.srv = nil
	}
	if w.ln != nil {
		w.ln.Close()
		w.ln = nil
	}
	if w.served != nil {
		<-w.served
		w.served = nil
	}
	if w.pc != nil {
		w.pc.Close()
		w.pc = nil
	}
	w.addr = ""
	w.listen = ""
}

func (w *wtListener) status() (listening, certHash, errMsg string) {
	w.mu.Lock()
	addr := w.addr
	errMsg = w.err
	w.mu.Unlock()
	if addr != "" {
		_, hash, err := w.certs.get()
		if err != nil && errMsg == "" {
			errMsg = err.Error()
		}
		return addr, hash, errMsg
	}
	return "", "", errMsg
}

func (w *wtListener) serveSession(sess *webtransport.Session, origin, host string) {
	defer sess.CloseWithError(0, "")
	// Only a session whose CONNECT showed the token gets here: its
	// streams' requests carry the mark in their context.
	ctx := withWTAuth(sess.Context())
	for {
		str, err := sess.AcceptStream(ctx)
		if err != nil {
			return
		}
		go w.handleStream(ctx, str, sess.RemoteAddr(), origin, host)
	}
}

func (w *wtListener) handleStream(ctx context.Context, str *webtransport.Stream, remote net.Addr, origin, host string) {
	defer str.Close()
	var kind [1]byte
	if _, err := io.ReadFull(str, kind[:]); err != nil {
		return
	}
	switch kind[0] {
	case wtStreamSocket:
		w.handleSocketStream(ctx, str, remote, origin, host)
	case wtStreamHTTP:
		w.handleHTTPStream(ctx, str, remote, origin, host)
	}
}

func (w *wtListener) handleSocketStream(ctx context.Context, str *webtransport.Stream, remote net.Addr, origin, host string) {
	path, err := readShortString(str)
	if err != nil {
		return
	}
	proto, err := readShortString(str)
	if err != nil {
		return
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	wrappedServer := &remoteAddrConn{Conn: serverConn, remote: remote}
	srv := &http.Server{
		Handler:           w.g.HandlerFor(transportLAN),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return withWTAuth(context.Background()) },
	}
	ln := &singleConnListener{conn: wrappedServer, addr: wrappedServer.LocalAddr()}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer srv.Close()

	var subprotocols []string
	if proto != "" {
		subprotocols = []string{proto}
	}
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ws, resp, err := websocket.Dial(dialCtx, "ws://"+host+path, &websocket.DialOptions{
		Subprotocols: subprotocols,
		HTTPHeader:   hdr,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					return clientConn, nil
				},
			},
		},
		CompressionMode: websocket.CompressionDisabled,
	})
	cancel()
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		_ = writeWTClose(str, websocket.StatusPolicyViolation, err.Error())
		return
	}
	ws.SetReadLimit(maxMessage)
	defer ws.CloseNow()

	// Acknowledge WebSocket open.
	if _, err := str.Write([]byte{0x00}); err != nil {
		return
	}

	errCh := make(chan error, 2)
	// WS -> WT stream
	go func() {
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				var ce websocket.CloseError
				if errors.As(err, &ce) {
					_ = writeWTClose(str, ce.Code, ce.Reason)
				} else {
					_ = writeWTClose(str, websocket.StatusNormalClosure, "")
				}
				errCh <- err
				return
			}
			fType := wtFrameBinary
			if typ == websocket.MessageText {
				fType = wtFrameText
			}
			if err := writeWTFrame(str, fType, data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	// WT stream -> WS
	go func() {
		for {
			fType, data, err := readWTFrame(str)
			if err != nil {
				errCh <- err
				return
			}
			switch fType {
			case wtFrameText:
				if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
					errCh <- err
					return
				}
			case wtFrameBinary:
				if err := ws.Write(ctx, websocket.MessageBinary, data); err != nil {
					errCh <- err
					return
				}
			case wtFrameClose:
				code := websocket.StatusNormalClosure
				reason := ""
				if len(data) >= 2 {
					code = websocket.StatusCode(binary.BigEndian.Uint16(data[:2]))
					reason = string(data[2:])
				}
				_ = ws.Close(code, reason)
				errCh <- io.EOF
				return
			}
		}
	}()
	<-errCh
}

type wtHTTPReq struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
}

type wtHTTPResp struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (w *wtListener) handleHTTPStream(ctx context.Context, str *webtransport.Stream, remote net.Addr, origin, host string) {
	hdrBytes, err := readLenBytes(str)
	if err != nil {
		return
	}
	var reqMeta wtHTTPReq
	if err := json.Unmarshal(hdrBytes, &reqMeta); err != nil {
		return
	}
	bodyBytes, err := readLenBytes(str)
	if err != nil {
		return
	}
	method := reqMeta.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, reqMeta.Path, bytes.NewReader(bodyBytes))
	if err != nil {
		return
	}
	req.Host = host
	if remote != nil {
		req.RemoteAddr = remote.String()
	}
	for k, v := range reqMeta.Headers {
		req.Header.Set(k, v)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	rec := httptest.NewRecorder()
	w.g.HandlerFor(transportLAN).ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()

	respHeaders := make(map[string]string, len(res.Header))
	for k, vs := range res.Header {
		if len(vs) > 0 {
			respHeaders[k] = vs[0]
		}
	}
	respMeta, err := json.Marshal(wtHTTPResp{
		Status:  res.StatusCode,
		Headers: respHeaders,
	})
	if err != nil {
		return
	}
	if err := writeLenBytes(str, respMeta); err != nil {
		return
	}
	_ = writeLenBytes(str, rec.Body.Bytes())
}

func readShortString(r io.Reader) (string, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	n := int(binary.BigEndian.Uint16(lenBuf[:]))
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readLenBytes(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > wtMaxFrame {
		return nil, fmt.Errorf("frame too large: %d", n)
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeLenBytes(w io.Writer, data []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if len(data) > 0 {
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func writeWTFrame(w io.Writer, fType byte, data []byte) error {
	var hdr [5]byte
	hdr[0] = fType
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(data) > 0 {
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func readWTFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > wtMaxFrame {
		return 0, nil, fmt.Errorf("frame too large: %d", n)
	}
	var data []byte
	if n > 0 {
		data = make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return 0, nil, err
		}
	}
	return hdr[0], data, nil
}

func writeWTClose(w io.Writer, code websocket.StatusCode, reason string) error {
	buf := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(buf[:2], uint16(code))
	copy(buf[2:], reason)
	return writeWTFrame(w, wtFrameClose, buf)
}

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c *remoteAddrConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

type singleConnListener struct {
	once sync.Once
	conn net.Conn
	addr net.Addr
	done chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() {
		c = l.conn
		l.done = make(chan struct{})
	})
	if c != nil {
		return c, nil
	}
	if l.done != nil {
		<-l.done
	}
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	if l.done != nil {
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.addr
}
