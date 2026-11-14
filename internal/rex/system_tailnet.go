package rex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

const (
	tailnetPrefix = "tailscale:"
	tailnetPort   = RemotePort
	// tailnetRefresh is how often the broker reads the system Tailscale
	// client's status. It is slow on purpose: each read asks the client.
	tailnetRefresh = 60 * time.Second
	// builtinRefresh is how often it reads the built-in node's, which is
	// in the process: the status line follows its sign-in quickly.
	builtinRefresh = 5 * time.Second
	// tailnetFresh is how long a snapshot stays usable between reads.
	tailnetFresh = 2*tailnetRefresh + time.Second
	// tailnetDiscover is the least time between two discovery passes. A
	// pass probes the tailnet's peers, which each ask the Tailscale
	// client who they are, so it runs rarely even while the Hosts page
	// is open.
	tailnetDiscover = 2 * time.Minute
	// tailnetBurst is how many peers a pass probes besides the hosts it
	// already knows.
	tailnetBurst = 8
	// tailnetFailures is how many reads in a row may fail before the
	// broker decides the tailnet is gone, not merely slow, and drops its
	// links.
	tailnetFailures = 3
	tailnetIO       = 3 * time.Second
	tailnetProbe    = "gorex-tailnet/1 probe\n"
	tailnetConnect  = "gorex-tailnet/1 relay\n"
	tailnetReady    = "gorex-tailnet/1 ok "
)

var errTailnetUnavailable = errors.New("rex: Tailscale connection unavailable")

// TailnetStatus is the local server's view of the tailnet. Its host
// targets contain opaque node identities, not addresses or credentials.
type TailnetStatus struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Side is what carries discovery on this OS: TailnetSystem,
	// TailnetBuiltin, or empty where neither does.
	Side  string        `json:"side"`
	Scope string        `json:"scope,omitempty"`
	Hosts []TailnetHost `json:"hosts,omitempty"`
	// Error is why the broker cannot serve, such as a kernel that does
	// not let it bind to the Tailscale interface, or a built-in node
	// that is off or not signed in.
	Error string `json:"error,omitempty"`
	// Node is the built-in node's state, while the broker runs on that
	// side: NodeOff, NodeStarting, NodeNeedsLogin, NodeNeedsApproval or
	// NodeRunning. The sign-in link is not here: the gateway's local
	// socket gives it.
	Node string `json:"node,omitempty"`
	// Account is the login name that signed the running node in, and
	// Device its name on the tailnet.
	Account string `json:"account,omitempty"`
	Device  string `json:"device,omitempty"`
}

// TailnetStatus.Side's values.
const (
	TailnetSystem  = "system"
	TailnetBuiltin = "built-in"
)

// TailnetStatus.Node's values.
const (
	NodeOff           = "off"
	NodeStarting      = "starting"
	NodeNeedsLogin    = "needs-login"
	NodeNeedsApproval = "needs-approval"
	NodeRunning       = "running"
)

// TailnetSide is the side that carries discovery on this OS, as
// TailnetStatus.Side tells it.
func TailnetSide() string {
	switch {
	case tailnetBuiltin:
		return TailnetBuiltin
	case systemTailscaleClient() != nil:
		return TailnetSystem
	}
	return ""
}

// Owns tells whether a previously known target belongs to this profile.
func (s TailnetStatus) Owns(target string) bool {
	return s.Scope != "" && strings.HasPrefix(target, tailnetPrefix+s.Scope+":")
}

type TailnetHost struct {
	Target string `json:"target"`
	Name   string `json:"name"`
}

// IsTailnetTarget distinguishes an identity returned by discovery from a
// Tailcat address. The local broker resolves it against the current tailnet.
func IsTailnetTarget(target string) bool { return strings.HasPrefix(target, tailnetPrefix) }

type systemTailscale interface {
	Status(context.Context) (*ipnstate.Status, error)
	WhoIs(context.Context, string) (*apitype.WhoIsResponse, error)
}

type tailnetPeer struct {
	id   tailcfg.StableNodeID
	name string
	ip   netip.Addr
}

type tailnetSnapshot struct {
	scope string
	user  tailcfg.UserID
	ip    netip.Addr
	// device is the system client's interface; node the built-in node,
	// nil on the system side.
	device   net.Interface
	node     TailnetDevice
	byIP     map[netip.Addr]tailnetPeer
	byTarget map[string]tailnetPeer
	expires  time.Time
}

// tailnetSnapshotFrom is the system client's snapshot: its device must be
// a TUN, and the interface that holds its address is the one its sockets
// bind to.
func tailnetSnapshotFrom(st *ipnstate.Status) (*tailnetSnapshot, error) {
	if st == nil || !st.TUN {
		return nil, errTailnetUnavailable
	}
	if err := tailnetUsable(st); err != nil {
		return nil, err
	}
	ip := tailnetIPv4(st.TailscaleIPs)
	dev, err := tailnetInterface(ip)
	if err != nil {
		return nil, err
	}
	return tailnetSnapshotOf(st, ip, dev, nil), nil
}

// tailnetUsable tells whether a device of either side may discover: it
// runs, and a user, not a tag, signed it in.
func tailnetUsable(st *ipnstate.Status) error {
	if st == nil || st.BackendState != "Running" || st.CurrentTailnet == nil || st.Self == nil ||
		st.Self.UserID == 0 || st.Self.ID == "" || st.Self.IsTagged() || st.Self.Expired || st.CurrentTailnet.MagicDNSSuffix == "" {
		return errTailnetUnavailable
	}
	return nil
}

// tailnetSnapshotOf makes the snapshot of a usable status: the device's
// same-user, untagged, unexpired, unshared peers.
func tailnetSnapshotOf(st *ipnstate.Status, ip netip.Addr, dev net.Interface, node TailnetDevice) *tailnetSnapshot {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", st.CurrentTailnet.MagicDNSSuffix, st.Self.UserID, st.Self.ID)))
	s := &tailnetSnapshot{
		scope: fmt.Sprintf("%x", sum[:16]), user: st.Self.UserID, ip: ip, device: dev, node: node,
		byIP: map[netip.Addr]tailnetPeer{}, byTarget: map[string]tailnetPeer{}, expires: time.Now().Add(tailnetFresh),
	}
	suffix := "." + strings.ToLower(st.CurrentTailnet.MagicDNSSuffix)
	for _, p := range st.Peer {
		if p == nil || p.ID == "" || p.ID == st.Self.ID || p.UserID != s.user || p.AltSharerUserID != 0 ||
			p.IsTagged() || p.Expired || !p.InNetworkMap || !strings.HasSuffix(strings.ToLower(strings.TrimSuffix(p.DNSName, ".")), suffix) {
			continue
		}
		addr := tailnetIPv4(p.TailscaleIPs)
		if !addr.IsValid() || addr == ip {
			continue
		}
		name := tailnetName(p.HostName)
		if name == "" {
			name = string(p.ID)
		}
		peer := tailnetPeer{id: p.ID, name: name, ip: addr}
		s.byIP[addr], s.byTarget[s.target(peer)] = peer, peer
	}
	return s
}

// tailnetName is a peer's name as hosts show it: a built-in node's,
// MACHINE-gorex, without its suffix.
func tailnetName(host string) string {
	name := pairName(host)
	if base, ok := strings.CutSuffix(name, "-gorex"); ok && base != "" {
		return base
	}
	return name
}

func tailnetIPv4(addrs []netip.Addr) netip.Addr {
	for _, ip := range addrs {
		if ip.Is4() {
			return ip
		}
	}
	return netip.Addr{}
}

func tailnetInterface(ip netip.Addr) (net.Interface, error) {
	if !ip.IsValid() {
		return net.Interface{}, errTailnetUnavailable
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return net.Interface{}, err
	}
	var found net.Interface
	for _, dev := range ifaces {
		if dev.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := dev.Addrs()
		if err != nil {
			return net.Interface{}, err
		}
		for _, addr := range addrs {
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil && prefix.Addr() == ip {
				if found.Index != 0 {
					return net.Interface{}, errTailnetUnavailable
				}
				found = dev
			}
		}
	}
	if found.Index == 0 {
		return net.Interface{}, errTailnetUnavailable
	}
	return found, nil
}

func (s *tailnetSnapshot) target(p tailnetPeer) string {
	return tailnetPrefix + s.scope + ":" + string(p.id)
}
func (s *tailnetSnapshot) valid() bool { return s != nil && time.Now().Before(s.expires) }

// sameBinding tells whether two snapshots reach the tailnet the same way:
// the same identity and address, over the same interface or the same
// built-in node. A node turned off and on, or signed out and in, is
// another one.
func (s *tailnetSnapshot) sameBinding(other *tailnetSnapshot) bool {
	return s != nil && other != nil && s.scope == other.scope && s.ip == other.ip &&
		s.device.Index == other.device.Index && s.device.Name == other.device.Name && s.node == other.node
}

func (s *tailnetSnapshot) verifies(peer tailnetPeer, w *apitype.WhoIsResponse) bool {
	return w != nil && w.Node != nil && w.UserProfile != nil && w.UserProfile.ID == s.user &&
		w.Node.User == s.user && w.Node.Sharer == 0 && w.Node.StableID == peer.id &&
		len(w.Node.Tags) == 0 && slices.Contains(w.Node.Addresses, netip.PrefixFrom(peer.ip, 32))
}

type tailnetLink struct {
	peer tailnetPeer
	// Outgoing links also own their local socket, so revocation wakes the
	// broker immediately even if the client is waiting for terminal output.
	local net.Conn
}

// systemTailnet is the broker: discovery, and the links of both
// directions, on the system Tailscale client or the built-in node.
type systemTailnet struct {
	// api is the system client; node, when set, returns the built-in
	// node instead, nil while it is off.
	api       systemTailscale
	node      func() TailnetDevice
	ctx       context.Context
	cancel    context.CancelFunc
	slots     chan struct{}
	nextProbe int
	// wake asks the run loop to read the status now, when a page that
	// shows hosts wants it.
	wake chan struct{}

	mu           sync.Mutex
	snap         *tailnetSnapshot
	ln           net.Listener
	links        map[net.Conn]tailnetLink
	hosts        []TailnetHost
	wanted       time.Time
	lastRead     time.Time
	lastDiscover time.Time
	errText      string
	failures     int
	// enableErr is why turning the built-in node on at start failed.
	enableErr string
	// nodeState, account and device are the built-in node's, as
	// TailnetStatus tells them.
	nodeState, account, device string
}

func (s *Server) tailnetStartLocked() {
	if s.rem.tailnet != nil {
		return
	}
	var t *systemTailnet
	switch {
	case tailnetBuiltin:
		t = newTailnetBroker(nil, currentTailnetNode)
	default:
		api := systemTailscaleClient()
		if api == nil {
			return
		}
		t = newTailnetBroker(api, nil)
	}
	s.rem.tailnet = t
	go t.run(s)
}

func newTailnetBroker(api systemTailscale, node func() TailnetDevice) *systemTailnet {
	ctx, cancel := context.WithCancel(context.Background())
	return &systemTailnet{api: api, node: node, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 16),
		links: map[net.Conn]tailnetLink{}, wake: make(chan struct{}, 1)}
}

func (t *systemTailnet) side() string {
	if t.node != nil {
		return TailnetBuiltin
	}
	return TailnetSystem
}

// via is how a snapshot reaches the tailnet.
func (t *systemTailnet) via(snap *tailnetSnapshot) tailnetVia {
	if snap.node != nil {
		return builtinVia{snap.node}
	}
	return systemVia{api: t.api, device: snap.device}
}

func (s *Server) tailnetStopLocked() {
	if t := s.rem.tailnet; t != nil {
		t.cancel()
		t.replace(nil, nil)
		s.rem.tailnet = nil
	}
}

func (t *systemTailnet) clientFor(target string) *RemoteClient {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.wanted = time.Now()
	if t.snap.valid() {
		if p, ok := t.snap.byTarget[target]; ok {
			return &RemoteClient{Name: p.name, PublicKey: target}
		}
	}
	return nil
}

// linked tells whether a remote client is attached over the tailnet.
func (t *systemTailnet) linked() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.links) > 0
}

func (t *systemTailnet) status() TailnetStatus {
	t.mu.Lock()
	t.wanted = time.Now()
	// A page that asked for the hosts sees them now, not at the next
	// read, which is a minute away. A read asks the system client, so
	// asking does not start one more than every few seconds.
	wake := time.Since(t.lastRead) > 10*time.Second
	if t.node != nil {
		wake = time.Since(t.lastRead) > time.Second
	}
	st := TailnetStatus{Enabled: true, Side: t.side(), Error: t.errText}
	if t.node != nil {
		st.Node, st.Account, st.Device = t.nodeState, t.account, t.device
		if st.Node == "" {
			st.Node = NodeOff
		}
	}
	if t.snap.valid() && t.ln != nil {
		st.Running = true
		st.Scope = t.snap.scope
		st.Hosts = slices.Clone(t.hosts)
	} else if t.enableErr != "" && st.Node == NodeOff {
		st.Error = t.enableErr
	}
	t.mu.Unlock()
	if wake {
		select {
		case t.wake <- struct{}{}:
		default:
		}
	}
	return st
}

// replace invalidates listeners and established links under the same lock
// admission uses, so an identity check racing revocation cannot slip through.
func (t *systemTailnet) replace(snap *tailnetSnapshot, ln net.Listener) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.replaceLocked(snap, ln)
}

func (t *systemTailnet) replaceLocked(snap *tailnetSnapshot, ln net.Listener) {
	same := t.snap.sameBinding(snap) && t.ln == ln
	if t.ln != nil && t.ln != ln {
		t.ln.Close()
	}
	for c, link := range t.links {
		if same && snap.byIP[link.peer.ip].id == link.peer.id {
			continue
		}
		t.dropLocked(c, link)
	}
	if !same {
		t.hosts = nil
	}
	t.snap, t.ln = snap, ln
}

func (t *systemTailnet) dropLocked(c net.Conn, link tailnetLink) {
	delete(t.links, c)
	c.Close()
	if link.local != nil {
		link.local.Close()
	}
}

// noteFailure counts a read that failed. A single failure says nothing of
// the peers, whose links stay; only a run of them means the tailnet is
// gone rather than slow.
func (t *systemTailnet) noteFailure() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRead = time.Now()
	t.failures++
	if t.failures < tailnetFailures {
		return
	}
	t.replaceLocked(nil, nil)
}

func (t *systemTailnet) run(s *Server) {
	every := tailnetRefresh
	if t.node != nil {
		every = builtinRefresh
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for t.ctx.Err() == nil {
		t.refresh(s)
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
		case <-t.wake:
		}
	}
}

// read reads the status and makes its snapshot. A definite answer that
// there is none, from the built-in node, is a *tailnetOff.
func (t *systemTailnet) read() (*tailnetSnapshot, error) {
	ctx, cancel := context.WithTimeout(t.ctx, tailnetIO)
	defer cancel()
	if t.node != nil {
		return t.readBuiltin(ctx)
	}
	st, err := t.api.Status(ctx)
	if err != nil {
		return nil, err
	}
	return tailnetSnapshotFrom(st)
}

func (t *systemTailnet) refresh(s *Server) {
	snap, err := t.read()
	if off := (*tailnetOff)(nil); errors.As(err, &off) {
		if off.passing {
			t.mu.Lock()
			t.errText = off.reason
			t.mu.Unlock()
			t.noteFailure()
		} else {
			t.unavailable(off.reason)
		}
		return
	}
	if err != nil {
		t.noteFailure()
		return
	}
	t.mu.Lock()
	if t.ctx.Err() != nil {
		t.mu.Unlock()
		return
	}
	t.failures = 0
	t.lastRead = time.Now()
	ln := t.ln
	start := ln == nil || !t.snap.sameBinding(snap)
	if start {
		t.replaceLocked(nil, nil)
		ln, err = t.via(snap).listen(t.ctx, snap.ip)
		if err != nil {
			// An older Linux kernel refuses to bind to an interface:
			// the user has to hear why the mode finds nothing.
			t.errText = err.Error()
			t.mu.Unlock()
			return
		}
	}
	t.errText = ""
	t.replaceLocked(snap, ln)
	// Probing asks the Tailscale client about every peer it reaches, so
	// a pass runs rarely, and only while a page that shows hosts asked
	// for them.
	probe := time.Since(t.wanted) < 2*tailnetRefresh && time.Since(t.lastDiscover) >= tailnetDiscover
	var known []string
	if probe {
		t.lastDiscover = time.Now()
		for _, h := range t.hosts {
			known = append(known, h.Target)
		}
	}
	t.mu.Unlock()
	if start {
		go t.accept(s, ln)
	}
	if probe {
		t.discover(snap, ln, known)
	}
}

func (t *systemTailnet) discover(snap *tailnetSnapshot, ln net.Listener, knownTargets []string) {
	ctx, cancel := context.WithTimeout(t.ctx, tailnetIO)
	defer cancel()
	targets := make([]string, 0, len(snap.byTarget))
	for target := range snap.byTarget {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	// Probe the hosts already found and a few others. Probing every
	// device, as a pass over the snapshot would, asks the Tailscale
	// client about phones and servers that never run GoRex.
	chosen := make([]string, 0, len(knownTargets)+tailnetBurst)
	seen := make(map[string]bool, len(knownTargets)+tailnetBurst)
	for _, target := range knownTargets {
		if _, ok := snap.byTarget[target]; ok && !seen[target] {
			seen[target] = true
			chosen = append(chosen, target)
		}
	}
	extra := 0
	for i := 0; i < len(targets) && extra < tailnetBurst; i++ {
		target := targets[(t.nextProbe+i)%len(targets)]
		if seen[target] {
			continue
		}
		seen[target] = true
		chosen = append(chosen, target)
		extra++
	}
	// Rotate which candidates go first when the pass runs out of time.
	// Only the refresh loop reads or writes this index.
	if len(targets) > 0 {
		t.nextProbe = (t.nextProbe + extra) % len(targets)
	}
	jobs := make(chan string)
	found := make(chan TailnetHost, len(chosen))
	checked := make(chan string, len(chosen))
	var workers sync.WaitGroup
	for range min(len(chosen), 4) {
		workers.Go(func() {
			for target := range jobs {
				p := snap.byTarget[target]
				attempt, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
				c, err := t.dial(attempt, snap, p, tailnetProbe)
				stop()
				if err == nil {
					c.Close()
					found <- TailnetHost{Target: target, Name: p.name}
				}
				if ctx.Err() == nil {
					checked <- target
				}
			}
		})
	}
send:
	for _, target := range chosen {
		select {
		case jobs <- target:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	close(found)
	close(checked)

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snap != snap || t.ln != ln {
		return
	}
	known := make(map[string]TailnetHost)
	for _, h := range t.hosts {
		if _, ok := snap.byTarget[h.Target]; ok {
			known[h.Target] = h
		}
	}
	for target := range checked {
		delete(known, target)
	}
	for h := range found {
		known[h.Target] = h
	}
	hosts := make([]TailnetHost, 0, len(known))
	for _, h := range known {
		hosts = append(hosts, h)
	}
	slices.SortFunc(hosts, func(a, b TailnetHost) int {
		if n := strings.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return strings.Compare(a.Target, b.Target)
	})
	t.hosts = hosts
}

// tailnetGreeting answers a verified probe or relay. Its number stays 3,
// whatever ProtocolVersion is: servers up to 0.21 take no other, and the
// Hello that follows tells the version.
func tailnetGreeting() string { return tailnetReady + "3\n" }

// tailnetGreeted reads a greeting, of any version, a byte at a time: what
// follows it is the server's.
func tailnetGreeted(c net.Conn) bool {
	line, err := readByteLine(c)
	return err == nil && strings.HasPrefix(string(line), tailnetReady)
}

func (t *systemTailnet) dial(ctx context.Context, snap *tailnetSnapshot, p tailnetPeer, hello string) (net.Conn, error) {
	via := t.via(snap)
	addr := netip.AddrPortFrom(p.ip, tailnetPort).String()
	who, err := via.WhoIs(ctx, addr)
	if err != nil || !snap.verifies(p, who) {
		return nil, errTailnetUnavailable
	}
	c, err := via.dial(ctx, snap.ip, addr)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	if _, err := io.WriteString(c, hello); err == nil && tailnetGreeted(c) {
		if hello != tailnetConnect {
			return c, nil
		}
		actual, err := via.WhoIs(ctx, c.RemoteAddr().String())
		if err == nil && snap.verifies(p, actual) {
			c.SetDeadline(time.Time{})
			return c, nil
		}
	}
	c.Close()
	return nil, errTailnetUnavailable
}

func (t *systemTailnet) accept(s *Server, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			t.mu.Lock()
			if t.ln == ln {
				t.replaceLocked(nil, nil)
			}
			t.mu.Unlock()
			return
		}
		t.mu.Lock()
		snap := t.snap
		addr, _ := netip.ParseAddrPort(c.RemoteAddr().String())
		var p tailnetPeer
		if t.ln == ln && snap.valid() && addr.IsValid() {
			p = snap.byIP[addr.Addr().Unmap()]
		}
		if p.id == "" {
			t.mu.Unlock()
			c.Close()
			continue
		}
		select {
		case t.slots <- struct{}{}:
			t.links[c] = tailnetLink{peer: p}
			t.mu.Unlock()
			go t.admit(s, snap, ln, c, p, addr.String())
		default:
			t.mu.Unlock()
			c.Close()
		}
	}
}

func (t *systemTailnet) admit(s *Server, snap *tailnetSnapshot, ln net.Listener, c net.Conn, p tailnetPeer, addr string) {
	defer t.closeLink(c)
	ctx, cancel := context.WithTimeout(t.ctx, tailnetIO)
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	b := make([]byte, len(tailnetProbe))
	_, err := io.ReadFull(c, b)
	var verified bool
	if hello := string(b); err == nil && (hello == tailnetProbe || hello == tailnetConnect) {
		who, err := t.via(snap).WhoIs(ctx, addr)
		if err == nil && snap.verifies(p, who) {
			t.mu.Lock()
			verified = t.ln == ln && t.snap.valid() && t.snap.sameBinding(snap) && t.snap.byIP[p.ip].id == p.id
			t.mu.Unlock()
		}
	}
	cancel()
	<-t.slots
	if !verified {
		return
	}
	if _, err := io.WriteString(c, tailnetGreeting()); err == nil && string(b) == tailnetConnect {
		c.SetDeadline(time.Time{})
		s.handle(c, snap.target(p))
	}
}

func (t *systemTailnet) closeLink(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropLocked(c, t.links[c])
}

// relayTailnet is reached through the local socket, after a local client
// names a discovered node. Keeping the outgoing links here makes the
// mode's switch and identity revocation apply to both directions.
func (s *Server) relayTailnet(local net.Conn, input io.Reader, target string) {
	defer local.Close()
	s.rem.mu.Lock()
	t := s.rem.tailnet
	s.rem.mu.Unlock()
	if t == nil {
		return
	}
	ctx, cancel := context.WithTimeout(t.ctx, tailnetIO)
	defer cancel()
	t.mu.Lock()
	snap := t.snap
	if !snap.valid() || t.ln == nil {
		t.mu.Unlock()
		return
	}
	p, ok := snap.byTarget[target]
	t.mu.Unlock()
	if !ok {
		return
	}
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	c, err := t.dial(ctx, snap, p, tailnetConnect)
	<-t.slots
	if err != nil {
		return
	}
	t.mu.Lock()
	if t.ctx.Err() != nil || !t.snap.valid() || !t.snap.sameBinding(snap) || t.snap.byTarget[target].ip != p.ip {
		t.mu.Unlock()
		c.Close()
		return
	}
	t.links[c] = tailnetLink{peer: p, local: local}
	t.mu.Unlock()
	defer t.closeLink(c)
	if _, err := io.WriteString(local, tailnetGreeting()); err != nil {
		return
	}
	splice(local, input, c)
}

// splice copies between a local client and the connection relayed for it,
// both ways, until both are done.
func splice(local net.Conn, input io.Reader, c net.Conn) {
	done := make(chan struct{})
	go func() {
		io.Copy(c, input)
		c.Close()
		close(done)
	}()
	io.Copy(local, c)
	local.Close()
	<-done
}

func connectTailnet(ctx context.Context, target string) (*Client, error) {
	dial := func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{}
		c, err := d.DialContext(ctx, "unix", SocketPath())
		if err != nil {
			return nil, err
		}
		stop := context.AfterFunc(ctx, func() { c.Close() })
		defer stop()
		b, _ := json.Marshal(Request{Op: "remote.tailnetDial", TailnetTarget: target})
		b = append(b, '\n')
		if _, err := c.Write(b); err == nil && tailnetGreeted(c) && stop() {
			return c, nil
		}
		c.Close()
		return nil, errTailnetUnavailable
	}
	c, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	client := newClient(c, func() (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*tailnetIO)
		defer cancel()
		return dial(ctx)
	})
	client.keepalive = true
	return client, nil
}
