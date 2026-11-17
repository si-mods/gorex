package rexapi

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

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
// Rex's API on its local socket, on --listen if given, on the tailnet
// while built-in Tailscale is on, and on the server's tailcat address
// while remote access is on; "pair", "tailscale" and "call"
// talk to a gateway that runs; "lan" and "web" change its state. It returns the process's exit status.
func Main(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "pair":
			return pairMain(args[1:])
		case "tailscale":
			return tailscaleMain(args[1:])
		case "call":
			return callMain(args[1:])
		case "lan":
			return lanMain(args[1:])
		case "web":
			return webMain(args[1:])
		}
	}
	return serveMain(args)
}

func serveMain(args []string) int {
	fs := flag.NewFlagSet("rexapi", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", "", "also serve anyone on this address, as http://127.0.0.1:PORT (no authentication)")
	socket := fs.String("socket", SocketPath(), "the local socket to serve")
	tailnetDir := fs.String("tailnet-dir", TailnetDir(), "the gateway's state: built-in Tailscale and the local network listener")
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
	stop := make(chan struct{})
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupt
		close(stop)
	}()
	err = run(c, runOptions{socket: *socket, dir: *tailnetDir, listen: tcp, stop: stop, announce: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi: %v\n", err)
		return 1
	}
	return 0
}

// runOptions are what run serves.
type runOptions struct {
	// socket is the local socket; dir the state of built-in Tailscale and
	// of the local network listener.
	socket, dir string
	// listen, if set, is an address to serve anyone on, for tests.
	listen string
	// stop ends the run.
	stop <-chan struct{}
	// announce prints where the gateway listens.
	announce bool
	// desktop tells that the GoRex server of this process runs the
	// gateway: its tailnet broker then uses the gateway's node.
	desktop bool
}

// errGatewayRuns is what a second gateway on the same state gets.
var errGatewayRuns = errors.New("another Rex API gateway runs on this data directory: the GoRex server's, while remote access, the local network or built-in Tailscale is on, or a gorex rexapi")

// lockGateway takes the gateway's lock on its state, which one gateway
// holds at a time: two would serve the same socket, port and tailnet
// device.
func lockGateway(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "gateway.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := rex.LockFile(f); err != nil {
		f.Close()
		return nil, errGatewayRuns
	}
	return f, nil
}

// run serves the gateway of c until stop closes or the GoRex server goes:
// on its local socket, on the tailnet while built-in Tailscale is on, on
// the local network while lan.json says so, which it reads again each
// second it changes, and on the server's tailcat address, whose
// connections the server hands over on the gateway's socket while remote
// access is on.
func run(c *rex.Client, o runOptions) error {
	lock, err := lockGateway(o.dir)
	if err != nil {
		c.Close()
		return err
	}
	defer lock.Close()
	g, err := New(Options{Client: c, TailnetDir: o.dir})
	if err != nil {
		c.Close()
		return err
	}
	defer g.Close()
	if o.desktop {
		setDesktop(g)
		defer clearDesktop(g)
	}

	serveErr := make(chan error, 3)
	unixLn, err := listenUnix(o.socket)
	if err != nil {
		return err
	}
	defer os.Remove(o.socket)
	defer unixLn.Close()
	g.addListener(Listener{
		Endpoint: unixEndpoint(o.socket), Addresses: []string{o.socket},
		Transport: transportUnix, Admission: unixAdmission, Primary: true,
	})
	go func() { serveErr <- g.serveOn(unixLn, transportUnix) }()
	if o.announce {
		fmt.Printf("rexapi: listening on unix://%s\n", o.socket)
	}
	tcLn, err := listenUnix(rex.GatewaySocketPath())
	if err != nil {
		return err
	}
	defer os.Remove(rex.GatewaySocketPath())
	defer tcLn.Close()
	go func() { serveErr <- g.serveOn(tailcatListener{tcLn}, transportTailcat) }()
	if o.listen != "" {
		ln, err := net.Listen("tcp", o.listen)
		if err != nil {
			return err
		}
		tl := newTrackedListener(ln)
		defer tl.Close()
		g.addListener(Listener{
			Endpoint: o.listen, Addresses: []string{ln.Addr().String()},
			Transport: transportTCP, Admission: AdmissionAnonymous,
		})
		go func() { serveErr <- g.serveOn(tl, transportTCP) }()
		if o.announce {
			fmt.Printf("rexapi: listening on http://%s\n", ln.Addr())
		}
	}
	if g.tailnet != nil {
		g.tailnet.resume()
	}
	w := &confWatch{path: lanConfPath(o.dir)}
	applyLAN := func() {
		conf, err := LoadLAN(o.dir)
		if err != nil {
			logf("lan: %v", err)
			return
		}
		g.lan.apply(conf)
	}
	w.changed()
	applyLAN()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-o.stop:
			return nil
		case <-c.Closed():
			return nil
		case err := <-serveErr:
			return err
		case <-tick.C:
			if w.changed() {
				applyLAN()
			}
		}
	}
}

// confWatch tells when a file changed since it last looked.
type confWatch struct {
	path string
	last string
}

func (w *confWatch) changed() bool {
	sig := ""
	if fi, err := os.Stat(w.path); err == nil {
		sig = fmt.Sprint(fi.ModTime().UnixNano(), fi.Size())
	}
	if sig == w.last {
		return false
	}
	w.last = sig
	return true
}

// Wanted reports whether the gateway in dir has anything to serve beyond
// its local socket: remote access, the local network listener or
// built-in Tailscale is on. The GoRex server runs it only then.
func Wanted(dir string) bool {
	if remoteOn(dir) {
		return true
	}
	if c, err := LoadLAN(dir); err == nil && c.Enabled {
		return true
	}
	var t TailscaleConfig
	if b, err := os.ReadFile(filepath.Join(dir, "tailscale.json")); err == nil && json.Unmarshal(b, &t) == nil && t.Enabled {
		return true
	}
	return false
}

// remoteWatches remember, by its path, whether remote.json says remote
// access is on, read again only when the file changes.
var (
	remoteMu      sync.Mutex
	remoteWatches = map[string]*remoteWatch{}
)

type remoteWatch struct {
	confWatch
	on bool
}

// remoteOn tells whether remote access is on, as remote.json in the data
// directory that holds dir says. The file holds the server's keys: only
// its switch is decoded, and only when the file changed.
func remoteOn(dir string) bool {
	path := filepath.Join(filepath.Dir(dir), "remote.json")
	remoteMu.Lock()
	defer remoteMu.Unlock()
	w := remoteWatches[path]
	if w == nil {
		w = &remoteWatch{confWatch: confWatch{path: path}}
		remoteWatches[path] = w
	}
	if w.changed() {
		var c struct {
			Enabled bool `json:"enabled"`
		}
		b, err := os.ReadFile(path)
		w.on = err == nil && json.Unmarshal(b, &c) == nil && c.Enabled
	}
	return w.on
}

// retryEvery is how long Supervise waits to start the gateway again after
// a start failed: the server was not reachable, or the gateway of the
// server it replaced still held the lock, which it lets go of moments
// after an in-place upgrade.
const retryEvery = time.Second

// Supervise runs in the GoRex server (rex.OnServe) until stop closes: it
// runs the gateway while Wanted says so, and stops it when none of
// remote access, the local network and built-in Tailscale is on, so that
// a server with nothing to serve stays as it was. A gorex rexapi of its
// own holds the gateway's lock meanwhile: the server's waits for it to go.
func Supervise(stop <-chan struct{}) {
	dir := TailnetDir()
	var runStop chan struct{}
	var done chan error
	var retryAt time.Time
	lastErr := ""
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	end := func() {
		if runStop != nil {
			close(runStop)
			<-done
			runStop, done = nil, nil
		}
	}
	for {
		want := Wanted(dir)
		switch {
		case want && runStop == nil && time.Now().After(retryAt):
			c, err := rex.ConnectExisting()
			if err != nil {
				retryAt = time.Now().Add(retryEvery)
				break
			}
			runStop, done = make(chan struct{}), make(chan error, 1)
			go func(c *rex.Client, stop chan struct{}, done chan error) {
				done <- run(c, runOptions{socket: SocketPath(), dir: dir, stop: stop, desktop: true})
			}(c, runStop, done)
		case !want && runStop != nil:
			end()
			saveLANStatus(dir, LANStatus{})
		}
		select {
		case <-stop:
			end()
			return
		case err := <-done:
			// The run ended by itself: another gateway holds the lock,
			// or the server went. Try again shortly.
			runStop, done = nil, nil
			retryAt = time.Now().Add(retryEvery)
			if err != nil && err.Error() != lastErr {
				lastErr = err.Error()
				logf("%v", err)
				if errors.Is(err, errGatewayRuns) {
					saveLANStatus(dir, LANStatus{Error: err.Error()})
				}
			}
		case <-tick.C:
		}
	}
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

// unixEndpoint is the unix:// URL of the socket at path, absolute and
// with forward slashes, escaped as Rex writes it: a space is %20, as in
// unix:///Users/me/Library/Application%20Support/rex/server.sock, and a
// Windows path reads unix:///C:/...
func unixEndpoint(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "unix", Path: path}).String()
}
