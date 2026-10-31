package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"gorex/internal/rex"
	"gorex/locales"
)

// Remote hosts: the session servers of other machines, connected to over
// tailcat, whose sessions open in tabs of the window beside this
// machine's. A host's address is a secret, as on the server: it is kept
// in hosts.json, readable by the user only, and never logged nor shown.

// savedHost is a remote host as hosts.json keeps it.
type savedHost struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	// Network marks a host the coordinator's directory made: it comes
	// and goes with that directory, not by hand.
	Network bool `json:"network,omitempty"`
	// Kills are the sessions closed here while the host was away, to end
	// there once connected again, though GoRex quit meanwhile.
	Kills []string `json:"kills,omitempty"`
}

func hostsPath() string { return filepath.Join(rex.Dir(), "hosts.json") }

type hostsFile struct {
	Hosts []savedHost `json:"hosts"`
}

// loadHosts reads the saved hosts; none when the file does not exist. A
// file that does not parse is moved aside, not lost: its addresses are
// secrets that cannot be had again but from each host.
func loadHosts() ([]savedHost, error) {
	var f hostsFile
	path := hostsPath()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		bad := path + ".bad-" + time.Now().Format("20060102-150405")
		if err := os.Rename(path, bad); err != nil {
			return nil, errors.New("gorex: hosts.json is not valid, and stays as it is")
		}
		os.Chmod(bad, 0o600)
		return nil, fmt.Errorf("gorex: hosts.json is not valid: moved aside to %s", filepath.Base(bad))
	}
	return slices.DeleteFunc(f.Hosts, func(h savedHost) bool { return h.ID == "" || h.Address == "" }), nil
}

// saveHosts writes the hosts, readable by the user only, atomically. It
// does not write over a file it cannot read, nor one that does not parse.
func saveHosts(hosts []savedHost) error {
	b, err := json.MarshalIndent(hostsFile{hosts}, "", "  ")
	if err != nil {
		return err
	}
	path := hostsPath()
	if old, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(old, &hostsFile{}) != nil {
			return errors.New("gorex: hosts.json is not valid: not writing over it")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func newHostID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// dialHost connects to the server of a host at its address; tests replace
// it, and validAddress, to serve hosts on Unix sockets.
var (
	dialHost = func(ctx context.Context, address string) (*rex.Client, error) {
		return rex.ConnectRemote(ctx, address)
	}
	validAddress = rex.ValidRemoteAddress
)

type hostState int

const (
	hostConnecting   hostState = iota // the first attempt
	hostConnected                     // hello answered
	hostReconnecting                  // an attempt after a drop or a failure
	hostFailed                        // the last attempt failed: another comes
)

// hostConn is a saved host and the app's connection to it. The main thread
// owns its state; the connection's goroutine and the streams of its panes
// share its client.
type hostConn struct {
	savedHost
	state   hostState
	attempt int           // of the attempt under way, or the next one
	reason  func() string // why it is not connected, said when drawn
	hello   rex.Hello
	// closed are the sessions closed here since the host connected, which
	// adopt does not open again though a list still has them.
	closed map[string]bool

	// mu guards client, and Kills of savedHost.
	mu     sync.Mutex
	client *rex.Client // nil while not connected
	retry  chan struct{}
	stop   chan struct{}
	once   sync.Once
	// update runs a change on the main thread, and persist saves the
	// hosts there; startHost sets them.
	update  func(func())
	persist func()
}

func newHostConn(h savedHost) *hostConn {
	return &hostConn{savedHost: h, retry: make(chan struct{}, 1), stop: make(chan struct{})}
}

// current returns the host's client, nil while not connected.
func (h *hostConn) current() *rex.Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.client
}

// poke makes the next attempt now.
func (h *hostConn) poke() {
	select {
	case h.retry <- struct{}{}:
	default:
	}
}

// close drops the connection and stops trying; the host's sessions go on.
func (h *hostConn) close() {
	h.once.Do(func() { close(h.stop) })
	h.mu.Lock()
	c := h.client
	h.client = nil
	h.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// saved returns the host as hosts.json keeps it.
func (h *hostConn) saved() savedHost {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.savedHost
	s.Kills = slices.Clone(s.Kills)
	return s
}

// kill ends a session of the host: it is kept to end until the host says
// it did, through the client of the moment, or the next one. Call it on the
// main thread.
func (h *hostConn) kill(sid string) {
	if h.closed == nil {
		h.closed = map[string]bool{}
	}
	h.closed[sid] = true
	h.mu.Lock()
	if !slices.Contains(h.Kills, sid) {
		h.Kills = append(h.Kills, sid)
	}
	c := h.client
	h.mu.Unlock()
	if h.persist != nil {
		h.persist()
	}
	if c != nil {
		go h.runKills(c)
	}
}

// runKills ends the sessions kept to end, through c, and forgets those the
// host ended; those it could not stay for the next connection.
func (h *hostConn) runKills(c *rex.Client) {
	h.mu.Lock()
	kills := slices.Clone(h.Kills)
	h.mu.Unlock()
	var done []string
	for _, sid := range kills {
		// A session the host does not share is not this machine's to end: forget it.
		if err := c.Kill(sid); err == nil || rex.NotShared(err) {
			done = append(done, sid)
		}
	}
	if len(done) == 0 || h.update == nil {
		return
	}
	h.update(func() {
		h.mu.Lock()
		h.Kills = slices.DeleteFunc(h.Kills, func(sid string) bool { return slices.Contains(done, sid) })
		h.mu.Unlock()
		if h.persist != nil {
			h.persist()
		}
	})
}

// hostBackoff is how long a host waits after its nth failed attempt.
func hostBackoff(n int) time.Duration {
	return min(time.Second<<min(max(n-1, 0), 5), 30*time.Second)
}

// hostHealthy is how long a connection stays up for a drop to try again
// at once, the failures before forgotten; tests shorten it.
var hostHealthy = 10 * time.Second

// hostFailure is why connecting to a host failed.
type hostFailure int

const (
	failUnreachable hostFailure = iota + 1 // not reached, or the connection lost on the way
	failRefused                            // the host does not allow this machine
	failPending                            // nor yet: its user has a request to answer
	failOther                              // the host said something else
)

// connectHost connects to a host and greets it; else why says what
// failed, and fail what kind of failure it is. It logs nothing: its
// callers log what changes.
func connectHost(ctx context.Context, address string) (c *rex.Client, hello rex.Hello, why func() string, fail hostFailure) {
	c, err := dialHost(ctx, address)
	if err != nil {
		return nil, hello, locales.HostsUnreachable, failUnreachable
	}
	if hello, err = c.Hello(); err != nil {
		c.Close()
		switch {
		case rex.IsPairingPending(err):
			return nil, hello, locales.HostsNotAllowed, failPending
		case rex.IsRefused(err):
			return nil, hello, locales.HostsNotAllowed, failRefused
		case strings.Contains(err.Error(), "connection to the server lost"), strings.Contains(err.Error(), "timed out"):
			// Lost on the way: nothing tells that the host refused.
			return nil, hello, locales.HostsUnreachable, failUnreachable
		}
		msg := serverMessage(errors.New(rex.Scrub(err.Error(), address)))
		return nil, hello, func() string { return msg }, failOther
	}
	return c, hello, nil, 0
}

// failureLog logs why connecting to a host failed, once until it changes.
type failureLog struct {
	last string
}

func (l *failureLog) log(name string, why func() string) {
	msg := ""
	if why != nil {
		msg = why()
	}
	if msg != l.last && msg != "" {
		log.Printf("gorex: host %s: %s", name, msg)
	}
	l.last = msg
}

// hostUpdate returns how the hosts' goroutines change the state: on the
// main thread, drawing the windows again. Call it on the main thread.
func (a *App) hostUpdate() func(func()) {
	win, set := a.win, settingsUI.win
	if st := a.st; st != nil && (win != nil || set != nil) {
		// Every window may show the host's tabs, and this one may close
		// before the others.
		return func(fn func()) {
			mygo.RunOnMain(func() {
				fn()
				st.redraw()
			})
		}
	}
	return func(fn func()) {
		switch {
		case win != nil:
			win.Update(func() {
				fn()
				if w := settingsUI.win; w != nil {
					w.Update(func() {})
				}
			})
		case set != nil:
			set.Update(fn)
		default:
			a.post(fn)
		}
	}
}

// startHost connects to a host in the background, and again whenever the
// connection drops, until the host is closed.
func (a *App) startHost(h *hostConn) {
	h.update, h.persist = a.hostUpdate(), func() { a.saveHosts() }
	go a.runHost(h, h.update)
}

// runHost connects to a host until it is closed. A connection that drops
// after it stayed up a while is tried again at once; one that drops sooner
// counts as a failure, which waits longer each time, so that a host that
// greets and drops is not dialed in a loop.
func (a *App) runHost(h *hostConn, update func(func())) {
	attempt := 0
	var logged failureLog
	for {
		n := attempt
		update(func() {
			h.state, h.attempt = hostConnecting, n
			if n > 0 {
				h.state = hostReconnecting
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		go func() {
			select {
			case <-h.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		c, hello, why, _ := connectHost(ctx, h.Address)
		cancel()
		if c != nil {
			h.mu.Lock()
			select {
			case <-h.stop:
				h.mu.Unlock()
				c.Close()
				return
			default:
			}
			h.client = c
			h.mu.Unlock()
			logged.log(h.Name, nil)
			update(func() { h.state, h.reason, h.hello, h.closed = hostConnected, nil, hello, nil })
			since := time.Now()
			h.runKills(c)
			a.watchHost(h, c, update)
			h.mu.Lock()
			if h.client == c {
				h.client = nil
			}
			h.mu.Unlock()
			c.Close()
			select {
			case <-h.stop:
				return
			default:
			}
			why = locales.PaneConnectionDropped
			if time.Since(since) >= hostHealthy {
				attempt = 1
				update(func() { h.reason = why })
				continue
			}
		}
		select {
		case <-h.stop:
			return
		default:
		}
		logged.log(h.Name, why)
		attempt++
		n = attempt
		update(func() { h.state, h.attempt, h.reason = hostFailed, n, why })
		select {
		case <-h.stop:
			return
		case <-h.retry:
		case <-time.After(hostBackoff(attempt)):
		}
	}
}

// hostLayoutGrace is how many polls a session the host's shared layout
// does not name waits for it: the host saves its layout at most once a
// second, so a session it just opened may be listed here before the save,
// and would then get a tab of its own.
const hostLayoutGrace = 2

// watchHost asks a host what runs in its sessions every second, until its
// connection closes, an ask fails, or the host is closed.
func (a *App) watchHost(h *hostConn, c *rex.Client, update func(func())) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	round := 0
	first := map[string]int{}    // the poll each session first appeared in
	prev := map[string]bool{}    // the sessions of the poll before
	var l hostLayout             // the host's layout, as last read
	shared, read := false, false // the host shares its layout, and it was read
	for {
		select {
		case <-c.Closed():
			return
		case <-h.stop:
			return
		case <-h.retry: // connected: nothing to retry
			continue
		case <-tick.C:
		}
		round++
		infos, err := c.List()
		if err != nil {
			// A connection that stopped answering is as good as dropped.
			return
		}
		byID := make(map[string]rex.SessionInfo, len(infos))
		next := make(map[string]bool, len(infos))
		fresh := false
		for _, in := range infos {
			byID[in.ID] = in
			next[in.ID] = true
			if !prev[in.ID] {
				fresh = true
			}
			if _, ok := first[in.ID]; !ok {
				first[in.ID] = round
			}
		}
		prev = next
		// A session may be adopted in the tab the host's shared layout
		// shows it in, with its splits. The layout is read when it was not
		// yet, when a session appears, and while one the layout does not
		// name may still be on its way into it.
		retry := false
		for id, in := range byID {
			if in.Exited || l.named[id] {
				continue
			}
			if round-first[id] <= hostLayoutGrace {
				retry = true
				break
			}
		}
		if !read || fresh || shared && retry {
			read = true
			raw, err := c.Layout()
			if shared = err == nil; shared {
				l = hostLayout{named: map[string]bool{}}
				for _, w := range parseLayout(raw).Sessions {
					l.tabs = append(l.tabs, w.Tabs...)
				}
				for _, t := range l.tabs {
					for _, n := range t.Roots() {
						namedSIDs(n, l.named)
					}
				}
			} else {
				// Not shared, or not now: each session gets a tab of its
				// own, as before there were layouts.
				l = hostLayout{}
			}
		}
		if shared {
			l.hold = nil
			for id, in := range byID {
				if in.Exited || l.named[id] || round-first[id] >= hostLayoutGrace {
					continue
				}
				if l.hold == nil {
					l.hold = map[string]bool{}
				}
				l.hold[id] = true
			}
		}
		layout := l
		update(func() {
			for _, o := range a.peers() {
				o.apply(h, byID, layout)
			}
		})
	}
}

// hostLayout is what a host's shared layout says of its sessions, for
// adopt: the sessions in its tabs, and those seen too lately for the host
// to have saved them there yet.
type hostLayout struct {
	tabs  []savedTab
	named map[string]bool
	hold  map[string]bool
}

// namedSIDs adds the sessions n's panes are to out.
func namedSIDs(n *savedNode, out map[string]bool) {
	if n == nil {
		return
	}
	if n.SID != "" {
		out[n.SID] = true
	}
	namedSIDs(n.A, out)
	namedSIDs(n.B, out)
}

// adopt opens a tab for each live session of host h that no pane here
// shows, as those of the host's own window or of another client while the
// host shares them; not those closed here, which end. Call it on the main
// thread.
//
// With the host's shared layout, the sessions of one of its tabs open in
// one tab here, with its splits: two panes side by side there are not two
// tabs here. Without it, each gets a tab of its own. A session the layout
// does not name yet waits for the host to save it there (hostLayout.hold)
// before it gets a tab of its own.
//
// With windows of workspaces, the window focused last opens them, and a
// session shows already when a tab of any workspace has it, shown in a
// window or not.
func (a *App) adopt(h *hostConn, byID map[string]rex.SessionInfo, l hostLayout) {
	// A list on its way as the host was removed, or the window closed.
	if a.quitting || !slices.Contains(a.hosts, h) {
		return
	}
	if st := a.st; st != nil && (len(st.apps) == 0 || st.apps[0] != a) {
		return
	}
	shown := map[string]bool{}
	where := map[string]*Pane{} // the panes here of h's sessions
	for _, o := range a.peers() {
		for _, t := range o.tabs {
			for _, p := range t.allPanes() {
				if p.host == h {
					shown[p.SID] = true
					where[p.SID] = p
				}
			}
		}
	}
	if a.st != nil {
		for sid := range a.st.hostSIDs(h) {
			shown[sid] = true
		}
	}
	h.mu.Lock()
	for _, sid := range h.Kills {
		shown[sid] = true
	}
	h.mu.Unlock()
	// free are the sessions to open, by id, until a tab of the layout
	// takes them; order keeps their creation order for those it does not.
	free := map[string]rex.SessionInfo{}
	var order []rex.SessionInfo
	for _, in := range sortedInfos(byID) {
		if in.Exited || shown[in.ID] || h.closed[in.ID] || l.hold[in.ID] {
			continue
		}
		free[in.ID] = in
		order = append(order, in)
	}
	if len(order) == 0 {
		return
	}
	added := false
	for _, t := range l.tabs {
		// A tab here that shows panes of t already takes its new ones,
		// as when the host splits a pane of it.
		if a.adoptInto(h, t, free, where) {
			added = true
		}
		if at := a.adoptTab(h, t, free); at != nil {
			a.tabs = append(a.tabs, at)
			added = true
		}
	}
	// A session the layout does not name, as of a host that shares no
	// layout, gets a tab of its own.
	for _, in := range order {
		if _, ok := free[in.ID]; !ok {
			continue
		}
		a.tabs = append(a.tabs, a.sessionTab(h, in))
		added = true
	}
	if added {
		a.groupTabs()
		a.changed()
	}
}

// adoptInto puts the panes of the host's tab st that free has into the
// tabs here that show its other panes, where its splits put them, and takes
// them from free; where are the panes here of h's sessions, and gets those
// it adds. It reports whether it added any. The tabs keep their focus.
func (a *App) adoptInto(h *hostConn, st savedTab, free map[string]rex.SessionInfo, where map[string]*Pane) bool {
	up := map[*savedNode]*savedNode{}
	var leaves func(n *savedNode) []*savedNode
	leaves = func(n *savedNode) []*savedNode {
		if n == nil {
			return nil
		}
		if n.A == nil || n.B == nil {
			return []*savedNode{n}
		}
		up[n.A], up[n.B] = n, n
		return append(leaves(n.A), leaves(n.B)...)
	}
	tiled := leaves(st.Root)
	// pane makes the pane of n's session in tab t of window o.
	pane := func(o *App, t *Tab, n *savedNode) *Pane {
		in := free[n.SID]
		delete(free, n.SID)
		p := &Pane{ID: o.id(), SID: in.ID, Name: n.Name, info: in, startDir: in.Dir, host: h}
		o.attach(p, in.Cols, in.Rows)
		p.Tab = t
		where[in.ID] = p
		o.changed()
		return p
	}
	added := false
	// A pane is placed beside one placed before it, so go on while any is.
	for more := true; more; {
		more = false
		for _, n := range tiled {
			if _, ok := free[n.SID]; n.SID == "" || n.Host != "" || !ok {
				continue
			}
			// The nearest split with a side shown here: the new pane
			// splits what shows that side.
			for c, s := n, up[n]; s != nil; c, s = s, up[s] {
				first, other := c == s.A, s.A
				if first {
					other = s.B
				}
				var near []*Pane
				for _, o := range leaves(other) {
					if p := where[o.SID]; p != nil && p.float == nil && p.Node != nil {
						near = append(near, p)
					}
				}
				if len(near) == 0 {
					continue
				}
				// The pane next to the new one there, and those of its tab.
				next := near[len(near)-1]
				if first {
					next = near[0]
				}
				t := next.Tab
				near = slices.DeleteFunc(near, func(p *Pane) bool { return p.Tab != t })
				// The split here of all those panes, when it has no other,
				// else the pane next to the new one.
				target := next.Node
				for x := next.Node; x != nil; x = x.Parent {
					ps := x.panes()
					if !slices.ContainsFunc(near, func(p *Pane) bool { return !slices.Contains(ps, p) }) {
						if len(ps) == len(near) {
							target = x
						}
						break
					}
				}
				o := a.owner(t)
				if o == nil {
					break
				}
				p := pane(o, t, n)
				splitNode(o, target, p, !first, s.Vertical, s.Ratio)
				added, more = true, true
				break
			}
		}
	}
	// A float of st goes over the tab here that shows st's other panes.
	var t *Tab
	for _, n := range tiled {
		if p := where[n.SID]; p != nil {
			t = p.Tab
			break
		}
	}
	for _, sf := range st.Floats {
		if t != nil {
			break
		}
		if sf.Pane != nil && where[sf.Pane.SID] != nil {
			t = where[sf.Pane.SID].Tab
		}
	}
	o := a.owner(t)
	if o == nil {
		return added
	}
	for _, sf := range st.Floats {
		n := sf.Pane
		if n == nil || n.A != nil && n.B != nil {
			continue
		}
		if _, ok := free[n.SID]; n.SID == "" || n.Host != "" || !ok {
			continue
		}
		p := pane(o, t, n)
		f := &Float{Pane: p, X: sf.X, Y: sf.Y, W: sf.W, H: sf.H, Hidden: sf.Hidden, Zoom: sf.Zoom}
		f.clamp()
		p.Node = &Node{ID: o.id(), Pane: p}
		p.float = f
		t.Floats = append(t.Floats, f)
		added = true
	}
	return added
}

// owner returns the window of the peers whose tabs have t, nil for none.
func (a *App) owner(t *Tab) *App {
	for _, o := range a.peers() {
		if t != nil && slices.Contains(o.tabs, t) {
			return o
		}
	}
	return nil
}

// splitNode puts pane p beside node n of window o, after it (right or
// below) when after, else before it, with ratio as the first side's share.
// The tab's focus stays.
func splitNode(o *App, n *Node, p *Pane, after, vertical bool, ratio float32) {
	if ratio == 0 {
		ratio = 0.5
	}
	old := &Node{ID: o.id(), Pane: n.Pane, Parent: n, Vertical: n.Vertical, Ratio: n.Ratio, A: n.A, B: n.B}
	if old.Pane != nil {
		old.Pane.Node = old
	}
	if old.A != nil {
		old.A.Parent, old.B.Parent = old, old
	}
	node := &Node{ID: o.id(), Pane: p, Parent: n}
	p.Node = node
	n.Pane, n.Vertical, n.Ratio = nil, vertical, min(max(ratio, 0.05), 0.95)
	n.A, n.B = old, node
	if !after {
		n.A, n.B = node, old
	}
}

// adoptTab makes the tab of the panes of the host's tab st that free has,
// in its splits, and takes them from it. It returns nil when none is left,
// as they all show here already.
func (a *App) adoptTab(h *hostConn, st savedTab, free map[string]rex.SessionInfo) *Tab {
	t := &Tab{ID: a.id(), Name: st.Name}
	// named are st's panes in order, as its focus counts them: the host's
	// filter leaves out the panes this client may not use, and compresses
	// the focus over those left.
	var named []string
	var load func(n *savedNode, parent *Node) *Node
	load = func(n *savedNode, parent *Node) *Node {
		if n == nil {
			return nil
		}
		if n.A == nil || n.B == nil {
			if n.SID != "" {
				named = append(named, n.SID)
			}
			in, ok := free[n.SID]
			if n.SID == "" || n.Host != "" || !ok {
				return nil
			}
			delete(free, n.SID)
			node := &Node{ID: a.id(), Parent: parent}
			p := &Pane{ID: a.id(), SID: in.ID, Name: n.Name, info: in, startDir: in.Dir, host: h}
			a.attach(p, in.Cols, in.Rows)
			p.Tab, p.Node = t, node
			node.Pane = p
			return node
		}
		r := n.Ratio
		if r == 0 {
			r = 0.5
		}
		node := &Node{ID: a.id(), Parent: parent, Vertical: n.Vertical, Ratio: min(max(r, 0.05), 0.95)}
		node.A, node.B = load(n.A, node), load(n.B, node)
		// The side left takes the place of a split whose other side does
		// not come, as a pane not shown here leaves no split of one pane.
		switch {
		case node.A == nil && node.B == nil:
			return nil
		case node.A == nil:
			node.B.Parent = parent
			return node.B
		case node.B == nil:
			node.A.Parent = parent
			return node.A
		}
		return node
	}
	if st.Root != nil {
		t.Root = load(st.Root, nil)
	}
	tiled := len(named)
	// The saved float with the focus, by its place among the saved floats:
	// a float that does not come shifts no index as it does.
	wanted := -1
	if st.FocusFloat > 0 {
		wanted = st.FocusFloat - 1
	}
	var focusedFloat *Float
	for i, sf := range st.Floats {
		if sf.Pane == nil {
			continue
		}
		n := load(sf.Pane, nil)
		if n == nil || n.Pane == nil {
			continue
		}
		p := n.Pane
		f := &Float{Pane: p, X: sf.X, Y: sf.Y, W: sf.W, H: sf.H, Hidden: sf.Hidden, Zoom: sf.Zoom}
		f.clamp()
		p.Node = &Node{ID: a.id(), Pane: p}
		p.float = f
		t.Floats = append(t.Floats, f)
		if i == wanted {
			focusedFloat = f
		}
	}
	named = named[:tiled]
	if t.Root == nil {
		if len(t.Floats) == 0 {
			return nil
		}
		// The tab's tiled panes are not here: its topmost float takes
		// their place, as it does when the last tiled pane closes.
		a.promoteFloat(t)
	}
	ps := t.panes()
	for _, p := range ps {
		t.setFocus(p)
	}
	for _, f := range t.Floats {
		t.setFocus(f.Pane)
	}
	// The pane the host focuses, or the first when it does not come.
	focused := ps[0]
	if st.Focus >= 0 && st.Focus < len(named) {
		for _, p := range ps {
			if p.SID == named[st.Focus] {
				focused = p
				break
			}
		}
	}
	t.setFocus(focused)
	if st.Zoom && len(ps) > 1 {
		t.Zoom = t.Focus
	}
	if focusedFloat != nil && slices.Contains(t.Floats, focusedFloat) && !focusedFloat.Hidden {
		t.setFocus(focusedFloat.Pane)
	}
	return t
}

// hostByID returns the saved host of an id, nil when none.
func (a *App) hostByID(id string) *hostConn {
	for _, h := range a.hosts {
		if h.ID == id {
			return h
		}
	}
	return nil
}

// allHosts are this machine, as nil, then the saved hosts, in their order.
func (a *App) allHosts() []*hostConn { return append([]*hostConn{nil}, a.hosts...) }

// tabsOn returns the tabs of host h, nil for this machine, in their order.
func (a *App) tabsOn(h *hostConn) []*Tab {
	var out []*Tab
	for _, t := range a.tabs {
		if t.host() == h {
			out = append(out, t)
		}
	}
	return out
}

// loadHosts reads the saved hosts and connects to them, closing the
// connections of any before.
func (a *App) loadHosts() {
	a.stopHosts()
	saved, err := loadHosts()
	if err != nil {
		log.Print(err)
	}
	a.hosts = nil
	for _, s := range saved {
		h := newHostConn(s)
		a.hosts = append(a.hosts, h)
		a.startHost(h)
	}
	a.syncHosts()
}

// peers are the windows of a's store, a alone without one.
func (a *App) peers() []*App {
	if a.st == nil {
		return []*App{a}
	}
	return a.st.apps
}

// syncHosts gives every window the hosts a has: they share them.
func (a *App) syncHosts() {
	if a.st == nil {
		return
	}
	a.st.hosts, a.st.hostsUp = a.hosts, true
	for _, o := range a.st.apps {
		o.hosts = a.hosts
	}
}

func (a *App) stopHosts() {
	for _, h := range a.hosts {
		h.close()
	}
}

// saveHosts writes the app's hosts: the store's, which every window
// shares, whichever window saves them, open or closed since.
func (a *App) saveHosts() error {
	hosts := a.hosts
	if a.st != nil {
		hosts = a.st.hosts
	}
	saved := make([]savedHost, len(hosts))
	for i, h := range hosts {
		saved[i] = h.saved()
	}
	err := saveHosts(saved)
	if err != nil {
		log.Printf("gorex: saving the hosts: %v", err)
	}
	return err
}

// addHost saves a host and connects to it.
func (a *App) addHost(s savedHost) *hostConn {
	h := newHostConn(s)
	a.hosts = append(slices.Clone(a.hosts), h)
	a.syncHosts()
	a.saveHosts()
	a.startHost(h)
	return h
}

// removeHost forgets a host and closes its panes in the windows; its
// sessions go on, on its server. The workspaces no window shows drop
// theirs as they show.
func (a *App) removeHost(h *hostConn) {
	if !slices.Contains(a.hosts, h) {
		return
	}
	if hostsUI.layoutHost == h {
		closeHostLayout()
	}
	for _, o := range a.peers() {
		// The window keeps a tab, as it would close with its last.
		if len(o.tabsOn(h)) == len(o.tabs) {
			home, _ := os.UserHomeDir()
			o.newTabOn(nil, home)
		}
		for _, t := range slices.Clone(o.tabs) {
			for _, p := range t.allPanes() {
				if p.host == h {
					o.dropPane(p, false)
				}
			}
		}
		o.changed()
	}
	a.hosts = slices.DeleteFunc(slices.Clone(a.hosts), func(o *hostConn) bool { return o == h })
	a.syncHosts()
	h.close()
	a.saveHosts()
}

// networkHostID is the ID of a host the network makes of a member, so
// that it is the same one in every file and every window.
func networkHostID(publicKey string) string { return "net:" + publicKey }

// reconcileNetwork mirrors the members of the network this machine
// joined into hosts: each member becomes a host of its own, and a member
// gone goes, its tabs with it. Hosts added by hand stay. It runs on the
// UI thread.
func (a *App) reconcileNetwork(n *rex.NetworkStatus) {
	for _, m := range n.Members {
		id := networkHostID(m.PublicKey)
		if h := a.hostByID(id); h != nil {
			if h.Address == m.Address && h.Name == m.Name {
				continue
			}
			if h.Address != m.Address {
				// A member that regenerated its address: a fresh
				// connection, so remove and add again below.
				a.removeHost(h)
			} else {
				h.Name = m.Name
				a.saveHosts()
				continue
			}
		}
		a.addHost(savedHost{ID: id, Name: m.Name, Address: m.Address, Network: true})
	}
	for _, h := range slices.Clone(a.hosts) {
		if !h.Network {
			continue
		}
		if !slices.ContainsFunc(n.Members, func(m rex.NetworkMember) bool {
			return networkHostID(m.PublicKey) == h.ID
		}) {
			a.removeHost(h)
		}
	}
}

// host is the host of a tab's panes, nil for this machine: a tab's panes
// are all on one, as splits open on the host of the pane they split.
func (t *Tab) host() *hostConn {
	if ps := t.allPanes(); len(ps) > 0 {
		return ps[0].host
	}
	return nil
}

// hostRank orders the hosts: this machine first, then the saved hosts in
// their order.
func (a *App) hostRank(h *hostConn) int {
	if h == nil {
		return 0
	}
	return slices.Index(a.hosts, h) + 1
}

// groupTabs keeps the tabs of a host together, in the order of the hosts,
// as the sidebar lists them.
func (a *App) groupTabs() {
	if len(a.hosts) == 0 {
		return
	}
	active := a.tab()
	slices.SortStableFunc(a.tabs, func(x, y *Tab) int { return a.hostRank(x.host()) - a.hostRank(y.host()) })
	a.active = max(slices.Index(a.tabs, active), 0)
}

// hostName names a host, this machine for nil.
func (a *App) hostName(h *hostConn) string {
	if h == nil {
		return thisMachine()
	}
	return h.Name
}

// hostStatus says what a host's connection is doing: a label, a detail,
// and whether it is up.
func hostStatus(h *hostConn) (label, detail string, up bool) {
	if h.reason != nil {
		detail = h.reason()
	}
	switch h.state {
	case hostConnected:
		return locales.HostsConnected(), "", true
	case hostReconnecting:
		return locales.HostsReconnecting(h.attempt), detail, false
	case hostFailed:
		return locales.HostsFailed(), detail, false
	}
	return locales.HostsConnecting(), detail, false
}

// screenReset clears a terminal attached again to its session, whose
// snapshot of the screen and scrollback follows.
const screenReset = "\x1bc\x1b[H\x1b[2J\x1b[3J"

// hostStream is the Conn of a remote pane's terminal: its session on the
// host, attached again whenever the host's connection comes back, until
// the session ends. What is typed while it is detached is dropped, not
// queued for a screen nobody sees.
type hostStream struct {
	h   *hostConn
	sid string
	// OnData is told the size of what the session prints, and onChange that
	// the stream detached or attached again.
	OnData   func(n int)
	onChange func()

	sizeMu     sync.Mutex // sendSize, one at a time
	mu         sync.Mutex
	cur        *rex.Stream
	fresh      bool // cur printed nothing yet
	cols, rows int
	// focused tells that the user looks at the pane, whose session the
	// server sizes for it; cur is told as it changes.
	focused bool
	// present, when set, tells whether the user is at this machine: each
	// stream attached again claims the focus only then.
	present  func() bool
	closed   bool
	detached bool
	gone     bool // the host no longer lists the session to this machine
	attaches int  // the streams that printed
	fails    int  // the streams lost since one printed
	pending  []byte
}

func newHostStream(h *hostConn, sid string, cols, rows int) *hostStream {
	return &hostStream{h: h, sid: sid, cols: cols, rows: rows}
}

// Detached tells that the stream was lost and is not back yet.
func (s *hostStream) Detached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detached
}

func (s *hostStream) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

// stream returns the stream of the moment, attaching a new one through
// the host's client once it has one; io.EOF once closed.
func (s *hostStream) stream() (*rex.Stream, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.cur != nil {
			st := s.cur
			s.mu.Unlock()
			return st, nil
		}
		cols, rows, fails, present := s.cols, s.rows, s.fails, s.present
		s.mu.Unlock()
		c := s.h.current()
		if c == nil || fails > 0 {
			time.Sleep(min(250*time.Millisecond<<min(fails, 3), 2*time.Second))
			if c == nil {
				continue
			}
		}
		st := c.Stream(s.sid, cols, rows)
		// Before it attaches: a host's session is claimed again on each
		// new stream only while the user is at this machine.
		st.SetPresent(present)
		st.Resize(cols, rows) // attaches now
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			st.Close()
			return nil, io.EOF
		}
		s.cur, s.fresh = st, true
		s.mu.Unlock()
		// A Resize while it attached, over a slow network as the view
		// first lays out, only noted the size: the session takes it now.
		// Left at the old size, its console draws for a screen of other
		// rows than the window's, and lines land on each other.
		s.sendSize()
		s.mu.Lock()
		focused := s.focused
		s.mu.Unlock()
		if focused {
			st.SetFocused(true)
		}
		return st, nil
	}
}

func (s *hostStream) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.pending) > 0 {
			n := copy(p, s.pending)
			s.pending = s.pending[n:]
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()
		st, err := s.stream()
		if err != nil {
			return 0, err
		}
		n, err := st.Read(p)
		if n > 0 {
			s.mu.Lock()
			first := s.fresh && s.cur == st
			if first {
				s.fresh, s.detached, s.fails = false, false, 0
				s.attaches++
				// Attached again: its snapshot replaces what shows.
				if s.attaches > 1 {
					s.pending = append([]byte(screenReset), p[:n]...)
					n = copy(p, s.pending)
					s.pending = s.pending[n:]
				}
			}
			s.mu.Unlock()
			if first {
				s.changed()
			}
			if s.OnData != nil {
				s.OnData(n)
			}
			return n, nil
		}
		if err == nil {
			continue
		}
		if s.lost(st, err) {
			return 0, io.EOF
		}
	}
}

// lost drops a stream that ended, and tells whether its session ended
// too, rather than the connection.
func (s *hostStream) lost(st *rex.Stream, err error) bool {
	st.Close()
	s.mu.Lock()
	if s.cur == st {
		s.cur = nil
	}
	closed, was := s.closed, s.detached
	s.detached = true
	s.fails++
	s.mu.Unlock()
	if closed {
		return true
	}
	if !was {
		s.changed()
	}
	if err.Error() != "no such session" {
		c := s.h.current()
		if c == nil {
			return false
		}
		infos, lerr := c.List()
		if lerr != nil {
			return false
		}
		for _, in := range infos {
			if in.ID == s.sid {
				return in.Exited
			}
		}
	}
	// Ended, or no longer shared with this machine: not for it to end.
	s.mu.Lock()
	s.gone = true
	s.mu.Unlock()
	return true
}

// Gone tells that the session ended without a trace, or that the host no
// longer shares it with this machine.
func (s *hostStream) Gone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gone
}

func (s *hostStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	st := s.cur
	if s.detached {
		st = nil
	}
	s.mu.Unlock()
	if st != nil {
		st.Write(p)
	}
	return len(p), nil
}

// Paste writes text to the session as a paste, as the terminal's paste
// keys would: the host's server brackets it as the program asked.
func (s *hostStream) Paste(text string) error {
	c := s.h.current()
	if c == nil {
		return errors.New("gorex: the host is away")
	}
	return c.Paste(s.sid, text)
}

// Resize tells the session the size of the screen, when attached; the
// next stream attaches at it.
func (s *hostStream) Resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	s.sendSize()
	return nil
}

// SetFocused tells the session whether the user looks at the pane, once
// attached; a stream attaching again is told when it does (stream).
func (s *hostStream) SetFocused(on bool) {
	s.mu.Lock()
	s.focused = on
	st := s.cur
	s.mu.Unlock()
	if st != nil {
		st.SetFocused(on)
	}
}

// sendSize tells the stream of the moment the latest size, one at a time,
// so that an older size never comes last.
func (s *hostStream) sendSize() {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	s.mu.Lock()
	st, cols, rows := s.cur, s.cols, s.rows
	s.mu.Unlock()
	if st != nil {
		st.Resize(cols, rows)
	}
}

// Close detaches; the session goes on.
func (s *hostStream) Close() error {
	s.mu.Lock()
	s.closed = true
	st := s.cur
	s.cur = nil
	s.mu.Unlock()
	if st != nil {
		st.Close()
	}
	return nil
}
