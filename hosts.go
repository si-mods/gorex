package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"gorex/internal/hosts"
	"gorex/internal/rex"
	"gorex/locales"
)

// Remote hosts: the session servers of other machines, connected to over
// tailcat, whose sessions open in tabs of the window beside this
// machine's. A host's address is a secret, as on the server: it is kept
// in hosts.json, readable by the user only, and never logged nor shown.

// savedHost is a remote host as hosts.json keeps it.
type savedHost = hosts.Host

// label is the name the host shows by: its alias, else its name.
func (h *hostConn) label() string { return h.savedHost.Label() }

// hostsPath is the app's hosts.json, which the CLI and the server read.
func hostsPath() string { return hosts.Path(rex.Dir()) }

// loadHosts reads the saved hosts; none when the file does not exist. A
// file that does not parse is moved aside, not lost: its addresses are
// secrets that cannot be had again but from each host.
func loadHosts() ([]savedHost, error) {
	dir := rex.Dir()
	list, err := hosts.Load(dir)
	if !errors.Is(err, hosts.ErrInvalid) {
		if err != nil {
			return nil, err
		}
		return keptHosts(dir, list), nil
	}
	path := hosts.Path(dir)
	bad := path + ".bad-" + time.Now().Format("20060102-150405")
	if err := os.Rename(path, bad); err != nil {
		return nil, errors.New("gorex: hosts.json is not valid, and stays as it is")
	}
	os.Chmod(bad, 0o600)
	return nil, fmt.Errorf("gorex: hosts.json is not valid: moved aside to %s", filepath.Base(bad))
}

// keptHosts are the hosts the app takes of a file it reads: a host the
// tailnet's discovery saved, which the user never added, opened, renamed
// nor gave an icon to, was none the user asked for. It goes from the file
// too, so that the next load, and the command line, see the hosts the app
// shows; the machines it names are offered again by the add sheet.
func keptHosts(dir string, list []savedHost) []savedHost {
	kept := slices.DeleteFunc(slices.Clone(list), func(h savedHost) bool {
		return rex.IsTailnetTarget(h.Address) && !h.TailnetOpened && h.Alias == "" && h.Icon == ""
	})
	if len(kept) != len(list) {
		if err := hosts.Save(dir, kept); err != nil {
			log.Print(err)
		}
	}
	return kept
}

// saveHosts writes the hosts, readable by the user only, atomically. It
// does not write over a file it cannot read, nor one that does not parse.
func saveHosts(list []savedHost) error { return hosts.Save(rex.Dir(), list) }

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
	// agents are the known agent CLIs installed on this host (agents.list).
	agents []rex.AgentInfo
	// closed are the sessions closed here since the host connected, which
	// adopt does not open again though a list still has them.
	closed map[string]bool
	// running tells that the app connects to the host (startHost), which
	// it does from the moment the host is saved.
	running bool
	// transfers are those of the host's sessions, by session.
	transfers map[string]*sessionTransfers
	// creating closes once the pane of the session asked of the host last
	// is placed, or would have been (newPaneRun).
	creating chan struct{}

	// mu guards client, changed, Kills of savedHost and expect, the
	// sessions a teleport starts here.
	mu     sync.Mutex
	client *rex.Client // nil while not connected
	// changed closes as client changes (setClient).
	changed chan struct{}
	expect  map[string]bool
	retry   chan struct{}
	stop    chan struct{}
	once    sync.Once
	// infos are the host's sessions, as its last list had them, for the
	// status of the spaces no window shows (store.hiddenStatus); changed
	// on the main thread.
	infos map[string]rex.SessionInfo
	// lay is the host's layout as the app follows it (hostlayout.go);
	// the main thread's.
	lay *hostSync
	// update runs a change on the main thread, and persist saves the
	// hosts there; startHost sets them.
	update  func(func())
	persist func()
}

func newHostConn(h savedHost) *hostConn {
	return &hostConn{savedHost: h, retry: make(chan struct{}, 1), stop: make(chan struct{})}
}

// listed is what the host's last list says of session sid, while the host
// is connected: a list of before is old.
func (h *hostConn) listed(sid string) (rex.SessionInfo, bool) {
	in, ok := h.infos[sid]
	return in, ok && h.state == hostConnected
}

// current returns the host's client, nil while not connected.
func (h *hostConn) current() *rex.Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.client
}

// expecting marks session sid, which a teleport starts on the host, as
// one its pane takes, or unmarks it.
func (h *hostConn) expecting(sid string, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.expect == nil {
		h.expect = map[string]bool{}
	}
	if on {
		h.expect[sid] = true
	} else {
		delete(h.expect, sid)
	}
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
	h.setClient(nil)
	h.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// setClient makes c the host's client, and tells those waiting for one
// (next). h.mu is held.
func (h *hostConn) setClient(c *rex.Client) {
	h.client = c
	if h.changed != nil {
		close(h.changed)
	}
	h.changed = make(chan struct{})
}

// next waits, until ctx is done or for d at most, for a client of the host
// other than c, nil for any; nil when none comes.
func (h *hostConn) next(ctx context.Context, c *rex.Client, d time.Duration) *rex.Client {
	timeout := time.NewTimer(d)
	defer timeout.Stop()
	for {
		h.mu.Lock()
		if h.changed == nil {
			h.changed = make(chan struct{})
		}
		n, changed := h.client, h.changed
		h.mu.Unlock()
		if n != nil && n != c {
			return n
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil
		case <-timeout.C:
			return nil
		}
	}
}

// learn keeps the machine the host's hello named, none from a server
// before machines, and the icon of the machine it described, if it did,
// and tells whether either changed. A host whose address changes is a new
// savedHost, with neither until it connects.
func (h *hostConn) learn(hello rex.Hello) bool {
	glyph := ""
	if m := hello.Host; m.Model != "" || m.OS != "" {
		glyph = hostGlyph(m, strings.HasPrefix(m.OS, "Windows"))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// A server that had not read its machine yet describes none: the icon
	// learned before stays.
	glyph = cmp.Or(glyph, h.Glyph)
	if h.Machine == hello.Machine && h.Glyph == glyph {
		return false
	}
	h.Machine, h.Glyph = hello.Machine, glyph
	return true
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
	c := h.keepKills([]string{sid})
	if h.persist != nil {
		h.persist()
	}
	if c != nil {
		go h.runKills(c)
	}
}

// keepKills keeps sessions sids to end, as kill does, saving nothing, and
// returns the client to end them through, nil while not connected. Call
// it on the main thread.
func (h *hostConn) keepKills(sids []string) *rex.Client {
	if h.closed == nil {
		h.closed = map[string]bool{}
	}
	for _, sid := range sids {
		h.stopTransfers(sid)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sid := range sids {
		h.closed[sid] = true
		if !slices.Contains(h.Kills, sid) {
			h.Kills = append(h.Kills, sid)
		}
	}
	return h.client
}

// runKills ends the sessions kept to end, through c, and forgets those the
// host ended; those it could not stay for the next connection.
func (h *hostConn) runKills(c *rex.Client) {
	h.mu.Lock()
	kills := slices.Clone(h.Kills)
	h.mu.Unlock()
	var done []string
	for _, sid := range kills {
		if err := c.Kill(sid); err == nil {
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
		case strings.Contains(err.Error(), "connection to the server lost"), errors.Is(err, rex.ErrTimeout):
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
// main thread, drawing the windows again, and counting the badge again,
// as the hosts' lists and states count in it (hiddenNeeds), with no
// window too. Call it on the main thread.
func (a *App) hostUpdate() func(func()) {
	update := a.hostRedraw()
	if st := a.st; st != nil {
		return func(fn func()) {
			update(func() {
				fn()
				st.syncBadge()
			})
		}
	}
	return update
}

// hostRedraw is hostUpdate but for the badge.
func (a *App) hostRedraw() func(func()) {
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
	h.running = true
	h.update, h.persist = a.hostUpdate(), func() { a.saveHosts() }
	go a.runHost(h, h.update)
}

// runHost connects to a host until it is closed. A connection that drops
// after it stayed up a while is tried again at once; one that drops sooner
// counts as a failure, which waits longer each time, so that a host that
// greets and drops is not dialed in a loop.
func (a *App) runHost(h *hostConn, update func(func())) {
	h.mu.Lock()
	name := h.Name
	h.mu.Unlock()
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
			h.setClient(c)
			h.mu.Unlock()
			logged.log(name, nil)
			update(func() {
				h.state, h.reason, h.hello, h.closed = hostConnected, nil, hello, nil
				if h.learn(hello) && h.persist != nil {
					h.persist()
				}
				if a.st != nil && h.current() == c {
					a.st.followHost(h, c, hello, update)
				}
			})
			if hello.Can(rex.CapabilityAgents) {
				go func(cl *rex.Client) {
					if res, err := cl.AgentsList(); err == nil {
						update(func() {
							if h.current() == cl {
								h.agents = res.Agents
							}
						})
					}
				}(c)
			}
			since := time.Now()
			h.runKills(c)
			a.watchHost(h, c, update)
			h.mu.Lock()
			if h.client == c {
				h.setClient(nil)
			}
			h.mu.Unlock()
			c.Close()
			update(func() {
				if a.st != nil {
					a.st.lostHost(h, c)
					a.st.redraw()
				}
			})
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
		logged.log(name, why)
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

// watchHost asks a host what runs in its sessions every second, until its
// connection closes, an ask fails, or the host is closed, and follows its
// layout: the windows take the list, and draw again, as it changes (its
// ETag), and the host's layout as its server tells it changed
// (store.followHost).
func (a *App) watchHost(h *hostConn, c *rex.Client, update func(func())) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var events <-chan rex.Event
	if err := c.Subscribe(); err == nil {
		events = c.Events()
	}
	etag := ""
	var prev map[string]rex.SessionInfo
	for {
		select {
		case <-c.Closed():
			return
		case <-h.stop:
			return
		case <-h.retry: // connected: the next list is taken all the same (a poke)
			etag = ""
			continue
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if ev.Kind == "layout" {
				if l := h.lay; l != nil {
					l.out.tell(c, ev.Revision)
				}
			}
			continue
		case <-tick.C:
		}
		infos, next, changed, err := c.ListSince(etag)
		if err != nil {
			// A connection that stopped answering is as good as dropped.
			return
		}
		etag = next
		if !changed && prev != nil {
			continue
		}
		byID := make(map[string]rex.SessionInfo, len(infos))
		for _, in := range infos {
			byID[in.ID] = in
		}
		prev = byID
		update(func() {
			if h.current() != c {
				return
			}
			h.infos = byID
			if a.st != nil {
				a.st.takeOld(h, byID)
			}
			for _, o := range a.peers() {
				o.apply(h, byID)
			}
		})
	}
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

// sessionsOn is how many sessions the tabs of host h have open, nil for
// this machine.
func (a *App) sessionsOn(h *hostConn) int {
	n := 0
	for _, t := range a.tabsOn(h) {
		n += len(t.allPanes())
	}
	return n
}

// loadHosts reads the saved hosts and connects to them, closing the
// connections of any before.
func (a *App) loadHosts() {
	a.stopHosts()
	saved, err := loadHosts()
	if err != nil {
		log.Print(err)
	}
	if a.st != nil {
		a.st.hostsLost = err != nil
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
	a.st.takeKills(a)
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
	if hostsUI.renaming == h.ID {
		closeHostRename()
	}
	// The host's workspaces go from the windows, its sessions going on
	// there: a window that showed one shows another.
	if st := a.st; st != nil {
		for _, o := range slices.Clone(st.apps) {
			if o.ws != nil && o.ws.host == h {
				o.showWorkspace(st.freshWorkspace())
			}
		}
		st.reconcileOn(h, savedStore{})
		forgetHostCache(h.ID)
	}
	for _, o := range a.peers() {
		changed := false
		for _, t := range slices.Clone(o.tabs) {
			for _, p := range t.allPanes() {
				if p.host == h {
					o.dropPane(p, false)
					changed = true
				}
			}
		}
		if changed {
			o.changed()
		}
	}
	a.hosts = slices.DeleteFunc(slices.Clone(a.hosts), func(o *hostConn) bool { return o == h })
	if slices.Contains(prefs.FoldedHosts, h.ID) {
		prefs.FoldedHosts = slices.DeleteFunc(prefs.FoldedHosts, func(s string) bool { return s == h.ID })
		saveSettings()
	}
	a.syncHosts()
	h.close()
	a.saveHosts()
}

// reconcileTailnet takes the GoRex hosts the tailnet shows, for the sheet
// that adds one: a machine found there is no host until the user adds it.
func (a *App) reconcileTailnet(st *rex.TailnetStatus) {
	hosts := []rex.TailnetHost(nil)
	if st.Running {
		hosts = slices.Clone(st.Hosts)
	}
	if a.st != nil {
		a.st.tailnetHosts = hosts
	} else {
		a.tailnetHosts = hosts
	}
}

// tailnetFound are the GoRex hosts the tailnet showed: the sheet that adds
// a host offers each, and saves one as the user adds it.
func (a *App) tailnetFound() []rex.TailnetHost {
	if a.st != nil {
		return a.st.tailnetHosts
	}
	return a.tailnetHosts
}

// addFoundHost saves a host the tailnet showed, and connects to it. The
// node's target is the host's ID too, so that the node found again is the
// same host here, with its tabs and its project's home. TailnetOpened
// marks it the user's, as any host here is: a load keeps it (loadHosts).
func (a *App) addFoundHost(peer rex.TailnetHost) *hostConn {
	if peer.Name == "" || peer.Target == "" || slices.ContainsFunc(a.hosts, func(h *hostConn) bool {
		return h.Address == peer.Target
	}) {
		return nil
	}
	return a.addHost(savedHost{ID: peer.Target, Name: peer.Name, Address: peer.Target, TailnetOpened: true})
}

// connectHostNow connects to host h now, at the user's asking: a host
// whose connection loops pokes it, and one that stopped starts again.
func (a *App) connectHostNow(h *hostConn) {
	if h.running {
		h.poke()
		return
	}
	a.startHost(h)
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
	return h.label()
}

// hostKey names a host where the app keeps something of it: its id, or
// "local" for this machine, nil.
func hostKey(h *hostConn) string {
	if h == nil {
		return "local"
	}
	return h.ID
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

// attachGrace is how long a pane of this machine waits for the view to ask
// for its size before it attaches at the one it has, as rex.Stream waited
// for its first Resize: the box of a pane made by a split is laid out a
// moment after its session, and the program must draw for that box.
const attachGrace = 400 * time.Millisecond

// maxQueuedInput bounds what waits for a stream: typed at a pane whose
// server never answers, or while the screen was lost, it would hold every
// key.
const maxQueuedInput = 64 << 10

// queuedInputTTL is how long after the screen was lost what is typed still
// waits for the stream: past it, the keys go to a program the user has
// stopped seeing, and are dropped instead.
const queuedInputTTL = 10 * time.Second

// streamNoteDelay is how long a pane's stream is lost before the pane
// says so: a stream that resumed at once shows nothing.
const streamNoteDelay = time.Second

// paneStream is the Conn of a pane's terminal: its session on this
// machine's server or on a host, attached again whenever its connection
// comes back, until the session ends. What is typed while it is detached
// waits for it, in order, up to maxQueuedInput and queuedInputTTL.
type paneStream struct {
	// client returns the client to attach through: this machine's, or the
	// host's, nil while it has none.
	client func() *rex.Client
	sid    string
	// name is the host's label, or "local" for this machine, for the log.
	name string
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
	// detachedAt is when the stream was lost, zero while attached: a
	// stream lost a moment shows no note, and takes what is typed.
	detachedAt time.Time
	// attachedAt is when the stream attached, for the log of its loss.
	attachedAt time.Time
	// lostAt is when the stream was last lost, for the log of its return.
	lostAt time.Time

	gone     bool // the host no longer lists the session to this machine
	attaches int  // the streams that printed
	fails    int  // the streams lost since one printed
	pending  []byte
	// queued is what was typed while the stream is away, or before the
	// first one attached, which it writes in order once it has one.
	queued []byte
	// made is when the pane was made, zero for a pane of a host: a pane
	// of this machine attaches at the size its box takes, which the view
	// asks within attachGrace of it, not at the one its session had. It
	// attaches as the view asks, once it does: the session takes that size
	// now, as a split's child session is made at its parent's.
	made time.Time
	// asked is closed as the view asks for a size the first time.
	asked     chan struct{}
	askedOnce sync.Once
}

func newHostStream(h *hostConn, sid string, cols, rows int) *paneStream {
	return &paneStream{client: h.current, name: h.label(), sid: sid, cols: cols, rows: rows}
}

// newLocalStream is newHostStream for a session of this machine: it
// attaches through the app's client, and attaches again when the stream
// is lost, as a host's does, so that a pane whose connection dropped
// takes what is typed again instead of showing a screen that goes on.
func newLocalStream(c *rex.Client, sid string, cols, rows int) *paneStream {
	return &paneStream{client: func() *rex.Client { return c }, name: "local", sid: sid, cols: cols, rows: rows, made: time.Now(), asked: make(chan struct{})}
}

// Detached tells that the stream was lost and is not back yet.
func (s *paneStream) Detached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detached
}

// DetachedFor tells how long the stream has been lost, zero while it is
// attached: the pane says so (streamNoteDelay) once it has been a while.
func (s *paneStream) DetachedFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.detached {
		return 0
	}
	return time.Since(s.detachedAt)
}

func (s *paneStream) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

// stream returns the stream of the moment, attaching a new one through
// its client once it has one; io.EOF once closed.
func (s *paneStream) stream() (*rex.Stream, error) {
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
		made := s.made
		s.mu.Unlock()
		// The first stream of a pane of this machine attaches as the view
		// asks for the size of the box it lays out, or after a moment, as
		// rex.Stream did: the program draws for that box, not for the size
		// its session had when the pane was made.
		if !made.IsZero() && fails == 0 {
			s.mu.Lock()
			s.made = time.Time{}
			s.mu.Unlock()
			if d := time.Until(made.Add(attachGrace)); d > 0 {
				select {
				case <-s.asked:
				case <-time.After(d):
				}
			}
			s.mu.Lock()
			cols, rows = s.cols, s.rows
			s.mu.Unlock()
		}
		c := s.client()
		if c == nil || fails > 0 {
			time.Sleep(min(250*time.Millisecond<<min(fails, 3), 2*time.Second))
			if c == nil {
				continue
			}
		}
		st := c.Stream(s.sid, cols, rows)
		// Before it attaches: a session is claimed again on each new
		// stream only while the user is at this machine.
		st.SetPresent(present)
		st.Resize(cols, rows) // attaches now
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			st.Close()
			return nil, io.EOF
		}
		s.cur, s.fresh = st, true
		queued := s.queued
		s.queued = nil
		s.mu.Unlock()
		if len(queued) > 0 {
			st.Write(queued)
		}
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

func (s *paneStream) Read(p []byte) (int, error) {
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
			var away time.Duration
			back := false
			if first {
				s.fresh, s.detached, s.fails = false, false, 0
				s.detachedAt = time.Time{}
				s.attaches++
				now := time.Now()
				// Back from a loss, even one before the first attach.
				if !s.lostAt.IsZero() {
					away, back = now.Sub(s.lostAt).Round(time.Millisecond), true
					s.lostAt = time.Time{}
				}
				s.attachedAt = now
				// Attached again: its snapshot replaces what shows.
				if s.attaches > 1 {
					s.pending = append([]byte(screenReset), p[:n]...)
					n = copy(p, s.pending)
					s.pending = s.pending[n:]
				}
			}
			s.mu.Unlock()
			if back {
				log.Printf("gorex: pane stream back: host=%s sid=%s away=%s", s.name, s.sid, away)
			}
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
func (s *paneStream) lost(st *rex.Stream, err error) bool {
	st.Close()
	s.mu.Lock()
	if s.cur == st {
		s.cur = nil
	}
	closed, was := s.closed, s.detached
	after := "never attached"
	if !s.attachedAt.IsZero() {
		after = time.Since(s.attachedAt).Round(time.Millisecond).String()
	}
	s.detached, s.detachedAt = true, time.Now()
	if !was {
		s.lostAt = s.detachedAt
	}
	s.fails++
	s.mu.Unlock()
	if closed {
		return true
	}
	if !was {
		log.Printf("gorex: pane stream lost: host=%s sid=%s after=%s: %v", s.name, s.sid, after, err)
		s.changed()
		// The note comes due a moment later, when the pane draws again.
		time.AfterFunc(streamNoteDelay, func() {
			s.mu.Lock()
			show := s.detached && !s.closed
			s.mu.Unlock()
			if show {
				s.changed()
			}
		})
	}
	if err.Error() != "no such session" {
		c := s.client()
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
func (s *paneStream) Gone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gone
}

func (s *paneStream) Write(p []byte) (int, error) {
	typed := len(p)
	s.mu.Lock()
	if st := s.cur; st != nil && !s.detached {
		s.mu.Unlock()
		st.Write(p)
		return typed, nil
	}
	// The stream is lost, or the pane's first one is still on its way:
	// what is typed goes once it attached, in order, as it waited for the
	// attach of old. Dropped, a shell asked to exit as its pane was made
	// would go on, and a key typed the moment a stream dropped would be
	// lost though the pane said it reconnected.
	if s.closed || !s.detachedAt.IsZero() && time.Since(s.detachedAt) > queuedInputTTL {
		// Typed long after the screen was lost: not for a program the user
		// has stopped seeing.
		s.queued = nil
		s.mu.Unlock()
		return typed, nil
	}
	if room := maxQueuedInput - len(s.queued); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		s.queued = append(s.queued, p...)
	}
	s.mu.Unlock()
	return typed, nil
}

// Paste writes text to the session as a paste, as the terminal's paste
// keys would: the host's server brackets it as the program asked.
func (s *paneStream) Paste(text string) error {
	c := s.client()
	if c == nil {
		return errors.New("gorex: the host is away")
	}
	return c.Paste(s.sid, text)
}

// Resize tells the session the size of the screen, when attached; the
// next stream attaches at it.
func (s *paneStream) Resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	asked := s.asked
	s.mu.Unlock()
	if asked != nil {
		s.askedOnce.Do(func() { close(asked) })
	}
	s.sendSize()
	return nil
}

// SetPresent sets what tells whether the user is at this machine, which
// each stream attaching again reads: a pane of this machine claims the
// session's size only then.
func (s *paneStream) SetPresent(present func() bool) {
	s.mu.Lock()
	s.present = present
	s.mu.Unlock()
}

// SetFocused tells the session whether the user looks at the pane, once
// attached; a stream attaching again is told when it does (stream).
func (s *paneStream) SetFocused(on bool) {
	s.mu.Lock()
	s.focused = on
	st := s.cur
	s.mu.Unlock()
	if st != nil {
		st.SetFocused(on)
	}
}

// Size returns the size the pane asked the session for, which its next
// stream attaches at.
func (s *paneStream) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// ClientID returns the id of the stream attached now, "" while none is:
// the server names the client whose terminal sizes the session
// (SessionInfo.SizedBy) by it.
func (s *paneStream) ClientID() string {
	s.mu.Lock()
	st := s.cur
	s.mu.Unlock()
	if st == nil {
		return ""
	}
	return st.ClientID()
}

// sendSize tells the stream of the moment the latest size, one at a time,
// so that an older size never comes last.
func (s *paneStream) sendSize() {
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
func (s *paneStream) Close() error {
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
