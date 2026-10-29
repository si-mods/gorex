//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Server owns the sessions.
type Server struct {
	mu         sync.Mutex
	sessions   map[string]*session
	order      []string
	layout     json.RawMessage
	layoutRev  uint64
	layoutFile sync.Mutex
	started    time.Time
	host       HostInfo
	hostDone   chan struct{}
	ln         net.Listener
	lock       *os.File
	exe        string
	exeTime    time.Time
	controls   int
	idleFrom   time.Time
	quit       chan struct{}
	quitOnce   sync.Once
	rem        remote
	// ops is held by the ops that add or end sessions, and an upgrade
	// takes it alone; acceptMu holds Accept while an upgrade stops it.
	ops      sync.RWMutex
	acceptMu sync.Mutex
	// kept are the control connections kept alive (Request.Keepalive), by
	// Hello.Conn, with the connections attached through them.
	kept map[string]*keptConn
	// cmds are the layout commands clients asked for (LayoutCommand), in
	// order, which the app has not taken; taken are those it took, by id.
	// cmdWake closes as one comes; takers counts the app's takes waiting,
	// and desks the connections open that took commands, the app's: while
	// one is, a command waits for its next take.
	cmdMu   sync.Mutex
	cmds    []*layoutCmd
	taken   map[int64]*layoutCmd
	cmdWake chan struct{}
	takers  int
	desks   int
	nextCmd int64
	// subs are the control connections that subscribed, told of each
	// change (notify).
	subMu sync.Mutex
	subs  map[*subscriber]struct{}
}

// layoutCmd is a layout command waiting for the app's answer, done.
type layoutCmd struct {
	LayoutCommand
	done chan LayoutCommand
}

// commandWait is how long a layout command waits for the app to make it,
// and takeWait how long the app's take waits for one.
var (
	commandWait = newDuration(CommandTimeout)
	takeWait    = newDuration(10 * time.Second)
)

// command asks the app for c, and returns its answer: Code tells why it
// did not make it, when the app has no window to take commands, or it
// did not answer in time; it may make it still.
func (s *Server) command(c LayoutCommand) LayoutCommand {
	s.cmdMu.Lock()
	if s.desks == 0 {
		s.cmdMu.Unlock()
		return LayoutCommand{Code: "desktop_unavailable", Error: "no GoRex window is open"}
	}
	s.nextCmd++
	c.ID = s.nextCmd
	lc := &layoutCmd{c, make(chan LayoutCommand, 1)}
	s.cmds = append(s.cmds, lc)
	if s.cmdWake != nil {
		close(s.cmdWake)
		s.cmdWake = nil
	}
	s.cmdMu.Unlock()
	select {
	case res := <-lc.done:
		return res
	case <-time.After(commandWait.get()):
	}
	s.cmdMu.Lock()
	s.cmds = slices.DeleteFunc(s.cmds, func(x *layoutCmd) bool { return x == lc })
	delete(s.taken, c.ID)
	s.cmdMu.Unlock()
	return LayoutCommand{Code: "layout_timeout", Error: "GoRex did not apply the change in time; it may still appear"}
}

// take returns the first layout command waiting, after waiting takeWait
// at most for one; nil when none came, or once gone closes, as the
// connection of the take does: a take of no one counts no more.
func (s *Server) take(gone <-chan struct{}) *LayoutCommand {
	timeout := time.After(takeWait.get())
	s.cmdMu.Lock()
	s.takers++
	defer func() {
		s.takers--
		s.cmdMu.Unlock()
	}()
	for {
		select {
		case <-gone:
			return nil
		default:
		}
		if len(s.cmds) > 0 {
			break
		}
		if s.cmdWake == nil {
			s.cmdWake = make(chan struct{})
		}
		wake := s.cmdWake
		s.cmdMu.Unlock()
		select {
		case <-wake:
		case <-gone:
		case <-timeout:
			s.cmdMu.Lock()
			return nil
		}
		s.cmdMu.Lock()
	}
	lc := s.cmds[0]
	s.cmds = s.cmds[1:]
	if s.taken == nil {
		s.taken = map[int64]*layoutCmd{}
	}
	s.taken[lc.ID] = lc
	return &lc.LayoutCommand
}

// commandDone gives the answer the app made to a command it took.
func (s *Server) commandDone(c LayoutCommand) {
	s.cmdMu.Lock()
	lc := s.taken[c.ID]
	delete(s.taken, c.ID)
	s.cmdMu.Unlock()
	if lc != nil {
		lc.done <- c
	}
}

// keptConn is a control connection kept alive, conn, of peer (remote
// client's key, "" for local), and the connections attached through it,
// which close with it: only the same peer's may.
type keptConn struct {
	id       string
	conn     net.Conn
	peer     string
	mu       sync.Mutex
	attached map[net.Conn]struct{}
	gone     bool
}

// keepaliveTimeout is how long a control connection kept alive may send
// nothing before the server drops it, and the sessions attached through
// it: a peer gone without closing them, over a network that dropped or
// a NAT that forgot it, leaves them open, and only silence tells.
var keepaliveTimeout = newDuration(30 * time.Second)

// writeTimeout is how long a remote connection may take to take any of
// what the server writes it before the server drops it: a peer gone
// takes nothing.
var writeTimeout = newDuration(30 * time.Second)

// keep keeps a control connection alive, of peer, under a new id.
func (s *Server) keep(conn net.Conn, peer string) *keptConn {
	k := &keptConn{id: newID(), conn: conn, peer: peer, attached: map[net.Conn]struct{}{}}
	s.mu.Lock()
	if s.kept == nil {
		s.kept = map[string]*keptConn{}
	}
	s.kept[k.id] = k
	s.mu.Unlock()
	return k
}

// unkeep forgets a control connection gone, closes the connections
// attached through it, and unlocks the sizes it locked.
func (s *Server) unkeep(k *keptConn) {
	s.mu.Lock()
	delete(s.kept, k.id)
	all := make([]*session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		all = append(all, ss)
	}
	s.mu.Unlock()
	for _, ss := range all {
		ss.releaseSize(k.conn)
	}
	k.mu.Lock()
	k.gone = true
	conns := k.attached
	k.attached = nil
	k.mu.Unlock()
	for c := range conns {
		c.Close()
	}
}

// bind attaches conn through the control connection id; false when that
// is gone.
func (s *Server) bind(id string, conn net.Conn, peer string) (*keptConn, bool) {
	s.mu.Lock()
	k := s.kept[id]
	s.mu.Unlock()
	if k == nil || k.peer != peer {
		return nil, false // another's: its sessions answer queries
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.gone {
		return nil, false
	}
	k.attached[conn] = struct{}{}
	return k, true
}

func (k *keptConn) unbind(conn net.Conn) {
	k.mu.Lock()
	delete(k.attached, conn)
	k.mu.Unlock()
}

// idleTimeout is how long a server with no sessions and no app waits
// before it exits.
const idleTimeout = 10 * time.Minute

// Serve runs the server until it is shut down: it returns an error at
// once when another server already runs.
func Serve() error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := lockFile(lock); err != nil {
		return errors.New("rex: a server already runs")
	}
	defer lock.Close()
	sock := SocketPath()
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	os.Chmod(sock, 0o600)
	s := newServer(ln, lock)
	if b, err := os.ReadFile(filepath.Join(dir, "layout.json")); err == nil && json.Valid(b) {
		s.layout = b
	}
	return s.serve()
}

func newServer(ln net.Listener, lock *os.File) *Server {
	s := &Server{
		sessions: map[string]*session{}, started: time.Now(), ln: ln, lock: lock,
		layoutRev: 1, hostDone: make(chan struct{}), idleFrom: time.Now(), quit: make(chan struct{}),
	}
	s.exe, s.exeTime = Executable()
	return s
}

// serve accepts connections until the server is shut down.
func (s *Server) serve() error {
	ln, sock := s.ln, SocketPath()
	go func() {
		s.host = hostInfo()
		close(s.hostDone)
	}()
	go s.watchIdle()
	s.remoteLoad()
	go func() {
		<-s.quit
		ln.Close()
	}()
	log.Printf("rex: serving on %s (pid %d)", sock, os.Getpid())
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.quit:
				s.mu.Lock()
				all := make([]*session, 0, len(s.sessions))
				for _, ss := range s.sessions {
					all = append(all, ss)
				}
				s.mu.Unlock()
				s.remoteClose()
				for _, ss := range all {
					ss.kill()
				}
				os.Remove(sock)
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				// An upgrade stopped it: wait for it to fail.
				s.acceptMu.Lock()
				s.acceptMu.Unlock()
				continue
			}
			return err
		}
		go s.handle(conn, "")
	}
}

func (s *Server) watchIdle() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
		s.mu.Lock()
		idle := len(s.sessions) == 0 && s.controls == 0 && time.Since(s.idleFrom) > idleTimeout
		s.mu.Unlock()
		if idle && !s.remoteEnabled() {
			log.Print("rex: idle, exiting")
			s.shutdown()
			return
		}
	}
}

func (s *Server) shutdown() { s.quitOnce.Do(func() { close(s.quit) }) }

// handle serves a connection: peer is the key of a remote client, "" for
// a local connection (a remote one always has a key: remoteAccept refuses
// a peer without). A remote one may make the ops of remoteAllowed only, on
// the sessions it may use (mayUse).
func (s *Server) handle(conn net.Conn, peer string) {
	r := bufio.NewReaderSize(conn, 64<<10)
	first, err := readLine(r)
	if err != nil {
		refuseLine(conn, err)
		conn.Close()
		return
	}
	var a Attach
	if json.Unmarshal(first, &a) == nil && a.Op == "attach" {
		s.mu.Lock()
		ss := s.sessions[a.SID]
		s.mu.Unlock()
		if ss == nil {
			conn.Write([]byte(`{"error":"no such session"}` + "\n"))
			conn.Close()
			return
		}
		if !s.mayUse(peer, ss) {
			conn.Write([]byte(`{"error":"` + errNotShared.Error() + `"}` + "\n"))
			conn.Close()
			return
		}
		var via net.Conn
		if a.Conn != "" {
			k, ok := s.bind(a.Conn, conn, peer)
			if !ok {
				conn.Write([]byte(`{"error":"` + errConnGone + `"}` + "\n"))
				conn.Close()
				return
			}
			defer k.unbind(conn)
			via = k.conn
		}
		// The session takes the window's size before the answer, after
		// which the client may resize it on its control connection: that
		// size must not be overwritten by this older one.
		if a.Cols > 0 && a.Rows > 0 {
			ss.resize(a.Cols, a.Rows)
		}
		conn.Write([]byte(`{"ok":true}` + "\n"))
		// What the reader buffered past the first line is input.
		var typed []byte
		if n := r.Buffered(); n > 0 {
			typed, _ = r.Peek(n)
		}
		ss.attach(conn, peer, a, via, func() bool { return s.mayUse(peer, ss) }, typed)
		return
	}
	s.control(conn, r, first, peer)
}

// control serves a control connection: a request per line.
func (s *Server) control(conn net.Conn, r *bufio.Reader, first []byte, peer string) {
	s.mu.Lock()
	s.controls++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.controls--
		s.idleFrom = time.Now()
		s.mu.Unlock()
		conn.Close()
	}()
	var kept *keptConn
	var sub *subscriber
	defer func() {
		if kept != nil {
			s.unkeep(kept)
		}
		if sub != nil {
			s.unsubscribe(sub)
		}
	}()
	remote := peer != ""
	var wmu sync.Mutex
	write := func(b []byte) {
		wmu.Lock()
		if remote {
			conn.SetWriteDeadline(time.Now().Add(writeTimeout.get()))
		}
		if _, err := conn.Write(append(b, '\n')); err != nil && remote {
			conn.Close() // gone, or as good as
		}
		wmu.Unlock()
	}
	reply := func(res Response) {
		b, _ := json.Marshal(res)
		write(b)
	}
	aside := make(chan struct{}, maxAside)
	// gone closes with the connection, ending its take; desk tells that it
	// took commands, the app's.
	gone := make(chan struct{})
	defer close(gone)
	desk := false
	defer func() {
		if desk {
			s.cmdMu.Lock()
			s.desks--
			s.cmdMu.Unlock()
		}
	}()
	line := first
	for {
		var req Request
		if err := json.Unmarshal(line, &req); err == nil {
			if req.Op == "hello" && req.Keepalive && kept == nil {
				kept = s.keep(conn, peer)
			}
			// Before the answer: no change after it goes untold.
			if req.Op == "subscribe" && !remote && sub == nil {
				sub = s.subscribe(write)
			}
			// Kills wait for the session to end, and layout commands for
			// the app: answer them aside, a few at a time; the others wait
			// their turn.
			if req.Op == "command.take" && !remote && !desk {
				desk = true
				s.cmdMu.Lock()
				s.desks++
				s.cmdMu.Unlock()
			}
			if req.Op == "kill" || req.Op == "command" || req.Op == "command.take" {
				aside <- struct{}{}
				kept := kept // a later hello sets it, as this runs
				go func() {
					defer func() { <-aside }()
					if killHook != nil && req.Op == "kill" {
						killHook()
					}
					if req.Op == "command.take" && !remote {
						res := Response{ID: req.ID}
						if c := s.take(gone); c != nil {
							res.Data, _ = json.Marshal(c)
						}
						reply(res)
						return
					}
					reply(s.request(req, peer, kept))
				}()
			} else {
				reply(s.request(req, peer, kept))
			}
		}
		if kept != nil {
			conn.SetReadDeadline(time.Now().Add(keepaliveTimeout.get()))
		}
		var err error
		line, err = readLine(r)
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				reply(Response{Error: err.Error()})
			}
			return
		}
	}
}

// maxLine is the longest line of control a connection may send, and
// maxAside how many of its requests answered aside (kills, layout
// commands) run at once.
const (
	maxLine  = 1 << 20
	maxAside = 8
)

var errLineTooLong = errors.New("rex: the request is too long")

// errConnGone answers an attach through a control connection that is
// gone: the client lost it too, or is about to.
const errConnGone = "rex: the connection attached through is gone"

// killHook, when set, runs in each kill before it starts: for tests.
var killHook func()

// readLine reads a line of at most maxLine bytes, the newline included.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxLine {
			return nil, errLineTooLong
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return line, nil
	}
}

// refuseLine tells a connection its first line was too long.
func refuseLine(conn net.Conn, err error) {
	if errors.Is(err, errLineTooLong) {
		conn.Write([]byte(`{"error":"` + err.Error() + `"}` + "\n"))
	}
}

func (s *Server) request(req Request, peer string, kept *keptConn) Response {
	var data any
	var err error
	remote := peer != ""
	if remote && req.Op == "remote.pair" {
		err = errors.New("rex: this machine is allowed there already")
	} else if remote && !remoteAllowed(req.Op) {
		log.Printf("rex: denied %q to a remote connection", req.Op)
		err = fmt.Errorf("%s is not allowed remotely", req.Op)
	} else {
		data, err = s.do(req, peer, kept)
	}
	res := Response{ID: req.ID}
	if err != nil {
		res.Error = err.Error()
	} else if data != nil {
		res.Data, _ = json.Marshal(data)
	}
	// A client polling the list gets it only when it changed.
	if req.Op == "list" && err == nil {
		sum := sha256.Sum256(res.Data)
		res.ETag = hex.EncodeToString(sum[:12])
		if req.ETag == res.ETag {
			res.Data = nil
		}
	}
	return res
}

func (s *Server) session(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil {
		return ss, nil
	}
	return nil, fmt.Errorf("no session %q", id)
}

var errSizeUnlocked = errors.New("rex: the session's size is not locked by this owner")

var errNotShared = errors.New("rex: that session is not shared with this machine: the host shares only the sessions each client opened")

// NotShared reports whether err is the host refusing a session it does not share
// with this client (the error arrives as text).
func NotShared(err error) bool {
	return err != nil && strings.Contains(err.Error(), "that session is not shared with this machine")
}

// mayUse tells whether peer, a remote client's key or "" for a local
// connection, may see and use ss: a local one always, a remote one the
// sessions it created, and any while the server shares them.
func (s *Server) mayUse(peer string, ss *session) bool {
	if peer == "" || ss.owner == peer {
		return true
	}
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	return s.rem.cfg.ShareSessions
}

// use returns the session id for peer (mayUse).
func (s *Server) use(id, peer string) (*session, error) {
	ss, err := s.session(id)
	if err == nil && !s.mayUse(peer, ss) {
		return nil, errNotShared
	}
	return ss, err
}

// dropUnshared detaches the remote windows from the sessions their client
// may no longer use, as sharing turns off.
func (s *Server) dropUnshared() {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		all = append(all, ss)
	}
	s.mu.Unlock()
	for _, ss := range all {
		ss.detach(func(peer string) bool { return !s.mayUse(peer, ss) })
	}
}

// do serves a request of a control connection, kept alive when kept is
// not nil.
func (s *Server) do(req Request, peer string, kept *keptConn) (any, error) {
	if strings.HasPrefix(req.Op, "remote.") {
		return s.remoteDo(req)
	}
	switch req.Op {
	case "hello":
		select {
		case <-s.hostDone:
		case <-time.After(3 * time.Second):
		}
		h := Hello{Version: ProtocolVersion, PID: os.Getpid(), Started: s.started, Host: s.host, Exe: s.exe, ExeTime: s.exeTime, Upgrade: canUpgrade, Capabilities: append([]string(nil), capabilities...)}
		if kept != nil {
			h.Keepalive, h.Conn = keepaliveTimeout.get(), kept.id
		}
		return h, nil
	case "ping", "subscribe":
		return nil, nil
	case "list":
		s.mu.Lock()
		all := make([]*session, 0, len(s.order))
		for _, id := range s.order {
			all = append(all, s.sessions[id])
		}
		s.mu.Unlock()
		infos := make([]SessionInfo, 0, len(all))
		for _, ss := range all {
			if s.mayUse(peer, ss) {
				infos = append(infos, ss.info())
			}
		}
		return infos, nil
	case "create":
		s.ops.RLock()
		defer s.ops.RUnlock()
		o := CreateOptions{}
		if req.Create != nil {
			o = *req.Create
		}
		id := newID()
		ss, err := newSession(id, o, s.sessionsChanged)
		if err != nil {
			return nil, err
		}
		ss.owner = peer
		s.mu.Lock()
		s.sessions[id] = ss
		s.order = append(s.order, id)
		s.mu.Unlock()
		s.notify("sessions")
		return ss.info(), nil
	case "kill":
		s.ops.RLock()
		defer s.ops.RUnlock()
		ss, err := s.use(req.SID, peer)
		if err == errNotShared {
			return nil, err
		}
		if err != nil {
			return nil, nil // already gone
		}
		ss.kill()
		s.mu.Lock()
		delete(s.sessions, req.SID)
		s.order = slices.DeleteFunc(s.order, func(id string) bool { return id == req.SID })
		if len(s.sessions) == 0 {
			s.idleFrom = time.Now()
		}
		s.mu.Unlock()
		s.notify("sessions")
		return nil, nil
	case "resize", "lockSize", "lockedResize", "unlockSize", "renewSize":
		ss, err := s.use(req.SID, peer)
		if err != nil {
			return nil, err
		}
		switch req.Op {
		case "resize":
			ss.resize(req.Cols, req.Rows)
		case "lockSize":
			if kept == nil || req.Owner == "" {
				return nil, errors.New("rex: lockSize needs an owner, on a connection kept alive")
			}
			ss.lockSize(req.Owner, req.Cols, req.Rows, kept.conn)
		case "lockedResize":
			if !ss.lockedResize(req.Owner, req.Cols, req.Rows) {
				return nil, errSizeUnlocked
			}
		case "unlockSize":
			ss.unlockSize(req.Owner, req.Cols, req.Rows)
		case "renewSize":
			if !ss.renewSize(req.Owner) {
				return nil, errSizeUnlocked
			}
		}
		return nil, nil
	case "getLayout":
		s.mu.Lock()
		l := s.layout
		s.mu.Unlock()
		if l == nil {
			return nil, nil
		}
		return l, nil
	case "getLayoutState":
		s.mu.Lock()
		st := LayoutState{Layout: s.layout, Revision: s.layoutRev}
		s.mu.Unlock()
		return st, nil
	case "setLayout":
		if !json.Valid(req.Layout) {
			return nil, errors.New("layout is not JSON")
		}
		l := bytes.Clone(req.Layout)
		s.mu.Lock()
		if req.LayoutRevision != 0 && req.LayoutRevision != s.layoutRev {
			rev := s.layoutRev
			s.mu.Unlock()
			return nil, fmt.Errorf("rex: layout is at revision %d", rev)
		}
		same := bytes.Equal(s.layout, l)
		s.layout = l
		if !same {
			s.layoutRev++
		}
		rev := s.layoutRev
		s.mu.Unlock()
		if !same {
			s.notify("layout")
		}
		// Write the layout the server has now, not this request's: two
		// writes that cross must not leave the older one on disk.
		s.layoutFile.Lock()
		s.mu.Lock()
		current := bytes.Clone(s.layout)
		s.mu.Unlock()
		path := filepath.Join(Dir(), "layout.json")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, current, 0o600); err == nil {
			os.Rename(tmp, path)
		}
		s.layoutFile.Unlock()
		return LayoutState{Layout: l, Revision: rev}, nil
	case "command", "command.done":
		if req.Command == nil {
			return nil, errors.New("rex: no command")
		}
		if req.Op == "command.done" {
			s.commandDone(*req.Command)
			return nil, nil
		}
		return s.command(*req.Command), nil
	case "upgrade":
		// Once it runs, the connection closes with this executable.
		return nil, s.upgrade(req.Exe)
	case "shutdown":
		go func() {
			time.Sleep(50 * time.Millisecond)
			s.shutdown()
		}()
		return nil, nil
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

func newID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Spawn starts a server in the background, as a process of its own that
// outlives the app: the executable run with -server.
func Spawn() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(Dir(), "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, "-server")
	cmd.Env = serverEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
