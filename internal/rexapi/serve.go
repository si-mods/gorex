package rexapi

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gorex/internal/rex"
)

// SocketPath is where the gateway listens for the clients of this
// machine: the Rex CLI reaches it with -S unix://PATH, and the server's
// administering methods (server.tailscale.*) are accepted only there.
// GOREX_REXAPI_SOCKET moves it, for a data directory whose path is too
// long for a socket's.
func SocketPath() string {
	if p := os.Getenv("GOREX_REXAPI_SOCKET"); p != "" {
		return p
	}
	return filepath.Join(rex.Dir(), "rexapi.sock")
}

// TailnetDir is where built-in Tailscale keeps its device and settings.
func TailnetDir() string { return filepath.Join(rex.Dir(), "rexapi") }

// Main runs "GoRex rexapi": with no command, the Rex API gateway, which
// connects to the GoRex server that runs (it starts none) and serves
// Rex's API on its local socket, on --listen if given, and on the
// tailnet while built-in Tailscale is on; "pair", "tailscale" and "call"
// talk to a gateway that runs. It returns the process's exit status.
func Main(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "pair":
			return pairMain(args[1:])
		case "tailscale":
			return tailscaleMain(args[1:])
		case "call":
			return callMain(args[1:])
		}
	}
	return serveMain(args)
}

func serveMain(args []string) int {
	fs := flag.NewFlagSet("rexapi", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", "", "also serve anyone on this address, as http://127.0.0.1:PORT (no authentication)")
	socket := fs.String("socket", SocketPath(), "the local socket to serve")
	tailnetDir := fs.String("tailnet-dir", TailnetDir(), "where built-in Tailscale keeps its device")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var tcp string
	if *listen != "" {
		u, err := url.Parse(*listen)
		if err != nil || u.Scheme != "http" || u.Host == "" {
			fmt.Fprintf(os.Stderr, "gorex rexapi: %q is not an http:// address\n", *listen)
			return 2
		}
		tcp = u.Host
	}
	c, err := rex.ConnectExisting()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	g, err := New(Options{Client: c, TailnetDir: *tailnetDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	defer g.Close()

	serveErr := make(chan error, 2)
	unixLn, err := listenUnix(*socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	defer os.Remove(*socket)
	go func() { serveErr <- g.serveOn(unixLn, transportUnix) }()
	fmt.Printf("rexapi: listening on unix://%s\n", *socket)
	if tcp != "" {
		ln, err := net.Listen("tcp", tcp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
			return 1
		}
		tl := newTrackedListener(ln)
		defer tl.Close()
		go func() { serveErr <- g.serveOn(tl, transportTCP) }()
		fmt.Printf("rexapi: listening on http://%s\n", ln.Addr())
	}
	if g.tailnet != nil && g.tailnet.conf.Enabled {
		// Built-in Tailscale was on when the gateway last stopped: it
		// comes back with its sign-in and settings.
		if _, aerr := g.tailnet.enable(enableOptions{allowMembers: g.tailnet.conf.AllowTailnetMembers}); aerr != nil {
			logf("tailscale: %s", aerr.Message)
		}
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	code := 0
	select {
	case <-interrupt:
	case <-c.Closed():
	case err := <-serveErr:
		if err != nil {
			logf("%v", err)
			code = 1
		}
	}
	unixLn.Close()
	return code
}

// listenUnix listens on the socket at path, for this user only. A socket
// left by a gateway that stopped is replaced; one that a gateway still
// serves is not.
func listenUnix(path string) (*trackedListener, error) {
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, fmt.Errorf("a gateway already serves %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	restore := privateUmask()
	ln, err := net.Listen("unix", path)
	restore()
	if err != nil {
		return nil, err
	}
	os.Chmod(path, 0o600)
	return newTrackedListener(ln), nil
}
