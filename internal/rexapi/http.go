package rexapi

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Handler serves Rex's endpoints: the control connection, data
// connections, and the help page.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control", s.handleControl)
	mux.HandleFunc("/api/data", s.handleData)
	mux.HandleFunc("/api/help", s.handleHelp)
	mux.HandleFunc("/llms.txt", s.handleLLMs)
	mux.HandleFunc("/llms.html", s.handleLLMsHTML)
	return mux
}

// handleControl upgrades a request to the control connection.
func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{"rex.control.v1"},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	s.serveControl(ws)
}

// handleHelp serves the methods this server provides, as /api/help does.
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"http":        httpPaths(),
		"methods":     helpMethods(),
		"block_types": blockTypes(),
	})
}

// handleLLMs writes a short guide to this server's API.
func (s *Server) handleLLMs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# Rex API, as GoRex serves it\n\n")
	fmt.Fprintf(w, "This server is GoRex. It answers Rex's API, so Rex clients and the rex CLI connect to it as they connect to Rex.\n\n")
	fmt.Fprintf(w, "A Rex session is a GoRex workspace, a window is a tab, and a block is a pane. A pane's block ID is `block:` and the session's short ID.\n\n")
	fmt.Fprintf(w, "## Endpoints\n\n")
	fmt.Fprintf(w, "- `GET /api/control`: the control connection, a WebSocket with subprotocol `rex.control.v1`.\n")
	fmt.Fprintf(w, "- `GET /api/data`: data connections, WebSockets that stream a block's output.\n")
	fmt.Fprintf(w, "- `GET /api/help`: every method, with its schemas.\n")
	fmt.Fprintf(w, "- `GET /llms.txt` and `GET /llms.html`: this page.\n\n")
	fmt.Fprintf(w, "## Methods\n\n")
	for _, m := range helpMethods() {
		fmt.Fprintf(w, "- `%s`: %s\n", m["name"], m["description"])
	}
}

// handleLLMsHTML writes the guide as HTML.
func (s *Server) handleLLMsHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html><title>Rex API, as GoRex serves it</title>")
	io.WriteString(w, "<h1>Rex API, as GoRex serves it</h1><p>This server is GoRex: a Rex session is a workspace, a window is a tab, and a block is a pane.</p><ul>")
	for _, m := range helpMethods() {
		fmt.Fprintf(w, "<li><code>%s</code>: %s</li>", m["name"], m["description"])
	}
	io.WriteString(w, "</ul>")
}

// writeJSON writes a value as JSON.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonOf(v))
}

// blockTypes describes the one creator GoRex has, as help.all reports it.
func blockTypes() []any {
	return []any{map[string]any{
		"creator": CreatorName,
		"flavors": []string{FlavorShell},
		"methods": []any{},
	}}
}

// ServeConn serves one HTTP connection, whose first bytes a reader has
// already buffered, and returns once http closes it.
func ServeConn(conn net.Conn, r *bufio.Reader, h http.Handler) {
	wc := &watchConn{Conn: conn, closed: make(chan struct{})}
	bc := &bufferedConn{Conn: wc, r: r}
	ln := &oneConnListener{conn: bc}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go srv.Serve(ln)
	<-wc.closed
	srv.Close()
}

// bufferedConn reads through a reader that already holds part of the
// connection.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// watchConn closes a channel when the connection does.
type watchConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *watchConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

// oneConnListener serves one connection, then reports no more.
type oneConnListener struct {
	conn net.Conn
	done bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.done {
		return nil, io.EOF
	}
	l.done = true
	return l.conn, nil
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// platformName names the platform server.version reports.
func platformName() string { return runtime.GOOS + "_" + runtime.GOARCH }
