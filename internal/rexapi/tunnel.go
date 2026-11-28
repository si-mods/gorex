package rexapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	tunnelCreatorName  = "com.superlogical.tunnel"
	tunnelFlavorDial   = "com.superlogical.tunnel.dial"
	tunnelFlavorListen = "com.superlogical.tunnel.listen"
	tunnelAcceptWait   = 30 * time.Second
	tunnelDialTimeout  = 10 * time.Second
)

var tunnelConnRefPattern = regexp.MustCompile(`^conn:.+$`)

type pendingTunnelConn struct {
	conn  net.Conn
	timer *time.Timer
}

type tunnelBlock struct {
	id            string
	session       string
	label         string
	flavor        string // "dial" or "listen"
	target        string // dial host:port
	address       string // listen ip:port
	clientAddress string
	clientTarget  string
	listener      net.Listener

	mu        sync.Mutex
	owner     string
	lastOwner string
	conns     int
	pending   map[string]*pendingTunnelConn
	active    map[io.Closer]struct{}
	closed    bool
}

func newUUIDv4() string {
	return randomUUID()
}

func (tb *tunnelBlock) currentOwner() string {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.owner
}

func (tb *tunnelBlock) close() {
	tb.mu.Lock()
	if tb.closed {
		tb.mu.Unlock()
		return
	}
	tb.closed = true
	ln := tb.listener
	tb.listener = nil
	pending := tb.pending
	tb.pending = nil
	active := make([]io.Closer, 0, len(tb.active))
	for c := range tb.active {
		active = append(active, c)
	}
	tb.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, p := range pending {
		p.timer.Stop()
		_ = p.conn.Close()
	}
	for _, c := range active {
		_ = c.Close()
	}
}

func (tb *tunnelBlock) info() map[string]any {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	res := map[string]any{
		"flavor":      tb.flavor,
		"connections": tb.conns,
	}
	if tb.flavor == "dial" {
		res["target"] = tb.target
		if tb.clientAddress != "" {
			res["client_address"] = tb.clientAddress
		}
	} else {
		res["address"] = tb.address
		if tb.clientTarget != "" {
			res["client_target"] = tb.clientTarget
		}
	}
	if tb.lastOwner != "" {
		res["last_owner"] = tb.lastOwner
	}
	return res
}

func (g *Gateway) tunnel(blockID string) *tunnelBlock {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tunnels[blockID]
}

func (g *Gateway) addTunnel(tb *tunnelBlock) {
	g.mu.Lock()
	g.tunnels[tb.id] = tb
	g.mu.Unlock()
}

func (g *Gateway) removeTunnel(blockID string) {
	g.mu.Lock()
	tb := g.tunnels[blockID]
	delete(g.tunnels, blockID)
	g.mu.Unlock()
	if tb != nil {
		tb.close()
	}
}

func (g *Gateway) removeSessionTunnels(sessionID string) {
	g.mu.Lock()
	var list []*tunnelBlock
	for id, tb := range g.tunnels {
		if tb.session == sessionID {
			delete(g.tunnels, id)
			list = append(list, tb)
		}
	}
	g.mu.Unlock()
	for _, tb := range list {
		tb.close()
	}
}

func (g *Gateway) disownClientTunnels(clientID string) {
	g.mu.Lock()
	list := make([]*tunnelBlock, 0, len(g.tunnels))
	for _, tb := range g.tunnels {
		list = append(list, tb)
	}
	g.mu.Unlock()
	for _, tb := range list {
		tb.mu.Lock()
		if tb.owner == clientID {
			tb.owner = ""
		}
		tb.mu.Unlock()
	}
}

func (g *Gateway) closeAllTunnels() {
	g.mu.Lock()
	list := make([]*tunnelBlock, 0, len(g.tunnels))
	for id, tb := range g.tunnels {
		delete(g.tunnels, id)
		list = append(list, tb)
	}
	g.mu.Unlock()
	for _, tb := range list {
		tb.close()
	}
}

func methodSessionNewBlock(c *control, a args) (any, *apiError) {
	for name := range a {
		switch name {
		case "session_id", "flavor", "options", "label":
		default:
			return nil, errBadRequest("the method takes no %s", name)
		}
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	flavor, aerr := a.reqStr("flavor")
	if aerr != nil {
		return nil, aerr
	}
	label, hasLabel, aerr := a.str("label")
	if aerr != nil {
		return nil, aerr
	}
	if hasLabel {
		label = strings.TrimSpace(label)
		if len(label) > 128 {
			return nil, errBadRequest("label is longer than 128 characters")
		}
		for _, r := range label {
			if r < 0x20 || r == 0x7f {
				return nil, errBadRequest("label contains control characters")
			}
		}
	}
	opts, _, aerr := a.obj("options")
	if aerr != nil {
		return nil, aerr
	}

	switch flavor {
	case tunnelFlavorDial, tunnelFlavorListen:
		if c.rev < 14 {
			return nil, errNotFound("no creator provides flavor %q", flavor)
		}
		return c.g.createTunnelBlock(c, s, flavor, label, opts)
	default:
		return nil, errNotFound("no creator provides flavor %q", flavor)
	}
}

func (g *Gateway) createTunnelBlock(c *control, s *mSession, flavor, label string, opts args) (any, *apiError) {
	tb := &tunnelBlock{
		id:        "block:" + newUUIDv4(),
		session:   s.id,
		owner:     c.rec.id,
		lastOwner: c.rec.id,
		pending:   map[string]*pendingTunnelConn{},
		active:    map[io.Closer]struct{}{},
	}
	if label != "" {
		tb.label = label
	} else if flavor == tunnelFlavorDial {
		tb.label = "dial"
	} else {
		tb.label = "listen"
	}

	if flavor == tunnelFlavorDial {
		tb.flavor = "dial"
		for name := range opts {
			switch name {
			case "host", "port", "client_address":
			default:
				return nil, errBadRequest("unknown option %s", name)
			}
		}
		host, aerr := opts.reqStr("host")
		if aerr != nil {
			return nil, aerr
		}
		port, ok, aerr := opts.int("port")
		if aerr != nil {
			return nil, aerr
		}
		if !ok || port < 1 || port > 65535 {
			return nil, errBadRequest("port must be between 1 and 65535")
		}
		ca, _, aerr := opts.str("client_address")
		if aerr != nil {
			return nil, aerr
		}
		if len(ca) > 255 {
			return nil, errBadRequest("client_address is longer than 255 bytes")
		}
		tb.target = net.JoinHostPort(host, strconv.Itoa(port))
		tb.clientAddress = ca
	} else {
		tb.flavor = "listen"
		for name := range opts {
			switch name {
			case "address", "port", "client_target":
			default:
				return nil, errBadRequest("unknown option %s", name)
			}
		}
		addr, hasAddr, aerr := opts.str("address")
		if aerr != nil {
			return nil, aerr
		}
		if !hasAddr || addr == "" {
			addr = "127.0.0.1"
		}
		port, hasPort, aerr := opts.int("port")
		if aerr != nil {
			return nil, aerr
		}
		if hasPort && (port < 0 || port > 65535) {
			return nil, errBadRequest("port must be between 0 and 65535")
		}
		ct, _, aerr := opts.str("client_target")
		if aerr != nil {
			return nil, aerr
		}
		if len(ct) > 255 {
			return nil, errBadRequest("client_target is longer than 255 bytes")
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err != nil {
			return nil, errInternal("tunnel listen: %v", err)
		}
		tb.listener = ln
		tb.address = ln.Addr().String()
		tb.clientTarget = ct
	}

	g.addTunnel(tb)
	if tb.listener != nil {
		go tb.acceptLoop(g, tb.listener)
	}
	return map[string]any{
		"block_id": tb.id,
		"revision": s.revision,
	}, nil
}

func (tb *tunnelBlock) acceptLoop(g *Gateway, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		owner := tb.currentOwner()
		if owner == "" {
			_ = conn.Close()
			continue
		}
		ref := "conn:" + newUUIDv4()
		tb.mu.Lock()
		if tb.closed {
			tb.mu.Unlock()
			_ = conn.Close()
			return
		}
		p := &pendingTunnelConn{conn: conn}
		p.timer = time.AfterFunc(tunnelAcceptWait, func() {
			if c := tb.takePending(ref); c != nil {
				_ = c.Close()
			}
		})
		tb.pending[ref] = p
		tb.mu.Unlock()

		g.emitSession(tb.session, "block_event", map[string]any{
			"block_id":     tb.id,
			"creator_name": tunnelCreatorName,
			"name":         "connection_requested",
			"payload": map[string]any{
				"ref":    ref,
				"peer":   conn.RemoteAddr().String(),
				"client": owner,
			},
		})
	}
}

func (tb *tunnelBlock) takePending(ref string) net.Conn {
	tb.mu.Lock()
	p := tb.pending[ref]
	if p != nil {
		delete(tb.pending, ref)
	}
	tb.mu.Unlock()
	if p == nil {
		return nil
	}
	p.timer.Stop()
	return p.conn
}

func (tb *tunnelBlock) trackConn(c net.Conn, add bool) {
	tb.mu.Lock()
	if add {
		tb.conns++
		tb.active[c] = struct{}{}
	} else {
		tb.conns--
		delete(tb.active, c)
	}
	tb.mu.Unlock()
}

func (g *Gateway) startTunnelStream(ws *websocket.Conn, t *ticket) {
	tb := g.tunnel(t.block)
	if tb == nil {
		g.welcomeThenClose(ws, websocket.StatusPolicyViolation, "tunnel closed")
		return
	}
	if !writeData(ws, welcomeBytes(randomBytes(16), nonzeroNonce())) {
		ws.CloseNow()
		return
	}
	var conn net.Conn
	var err error
	if tb.flavor == "dial" {
		conn, err = net.DialTimeout("tcp", tb.target, tunnelDialTimeout)
		if err != nil {
			ws.Close(websocket.StatusInternalError, fmt.Sprintf("dial %s: %v", tb.target, err))
			return
		}
	} else {
		conn = tb.takePending(t.dataRef)
		if conn == nil {
			ws.Close(websocket.StatusPolicyViolation, "connection request expired or unknown")
			return
		}
	}
	tb.trackConn(conn, true)
	defer func() {
		tb.trackConn(conn, false)
		_ = conn.Close()
	}()
	relayTunnelStream(ws, conn)
}

func relayTunnelStream(ws *websocket.Conn, conn net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wsErr := make(chan error, 1)
	tcpErr := make(chan error, 1)

	// WS -> TCP
	go func() {
		for {
			typ, msg, err := ws.Read(ctx)
			if err != nil {
				wsErr <- err
				return
			}
			if typ != websocket.MessageBinary {
				wsErr <- fmt.Errorf("unexpected non-binary message")
				return
			}
			if len(msg) == 0 {
				if tc, ok := conn.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
				wsErr <- nil
				return
			}
			if _, err := conn.Write(msg); err != nil {
				wsErr <- err
				return
			}
		}
	}()

	// TCP -> WS
	go func() {
		buf := make([]byte, maxChunk)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, downloadWriteWait)
				werr := ws.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					tcpErr <- werr
					return
				}
			}
			if err == io.EOF {
				wctx, wcancel := context.WithTimeout(ctx, downloadWriteWait)
				_ = ws.Write(wctx, websocket.MessageBinary, []byte{})
				wcancel()
				tcpErr <- nil
				return
			}
			if err != nil {
				tcpErr <- err
				return
			}
		}
	}()

	var e1, e2 error
	for i := 0; i < 2; i++ {
		select {
		case e := <-wsErr:
			e1 = e
			if e != nil {
				cancel()
				_ = conn.Close()
				ws.Close(websocket.StatusInternalError, e.Error())
				return
			}
		case e := <-tcpErr:
			e2 = e
			if e != nil {
				cancel()
				_ = conn.Close()
				ws.Close(websocket.StatusInternalError, e.Error())
				return
			}
		}
	}
	if e1 == nil && e2 == nil {
		ws.Close(websocket.StatusNormalClosure, "ok")
	}
}

var tunnelCreatorEntry = map[string]any{
	"name":        tunnelCreatorName,
	"title":       "Tunnel",
	"description": "Forward TCP connections between the server and the block's owner.",
	"flavors": []map[string]any{
		{
			"flavor":      tunnelFlavorDial,
			"name":        "dial",
			"title":       "Dial",
			"description": "Forward TCP connections to host:port from the server.",
		},
		{
			"flavor":      tunnelFlavorListen,
			"name":        "listen",
			"title":       "Listen",
			"description": "Listen on a TCP port on the server and forward accepted connections to the owner.",
		},
	},
	"api": map[string]any{
		"methods": map[string]any{
			"info": map[string]any{
				"description": "Return what the tunnel forwards and how many connections it is relaying.",
			},
		},
	},
}
