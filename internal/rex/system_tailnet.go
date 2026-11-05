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
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

const (
	tailnetPrefix  = "tailscale:"
	tailnetPort    = RemotePort
	tailnetRefresh = 5 * time.Second
	tailnetFresh   = 15 * time.Second
	tailnetIO      = 3 * time.Second
	tailnetProbe   = "gorex-tailnet/1 probe\n"
	tailnetConnect = "gorex-tailnet/1 relay\n"
	tailnetReady   = "gorex-tailnet/1 ok "
)

var errTailnetUnavailable = errors.New("rex: Tailscale connection unavailable")

// TailnetStatus is the local server's view of the system tailnet. Its host
// targets contain opaque node identities, not addresses or credentials.
type TailnetStatus struct {
	Enabled bool          `json:"enabled"`
	Running bool          `json:"running"`
	Scope   string        `json:"scope,omitempty"`
	Hosts   []TailnetHost `json:"hosts,omitempty"`
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
	scope    string
	user     tailcfg.UserID
	ip       netip.Addr
	device   net.Interface
	byIP     map[netip.Addr]tailnetPeer
	byTarget map[string]tailnetPeer
	expires  time.Time
}

func tailnetSnapshotFrom(st *ipnstate.Status) (*tailnetSnapshot, error) {
	if st == nil || !st.TUN || st.BackendState != "Running" || st.CurrentTailnet == nil || st.Self == nil ||
		st.Self.UserID == 0 || st.Self.ID == "" || st.Self.IsTagged() || st.Self.Expired || st.CurrentTailnet.MagicDNSSuffix == "" {
		return nil, errTailnetUnavailable
	}
	ip := tailnetIPv4(st.TailscaleIPs)
	dev, err := tailnetInterface(ip)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", st.CurrentTailnet.MagicDNSSuffix, st.Self.UserID, st.Self.ID)))
	s := &tailnetSnapshot{
		scope: fmt.Sprintf("%x", sum[:16]), user: st.Self.UserID, ip: ip, device: dev,
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
		name := pairName(p.HostName)
		if name == "" {
			name = string(p.ID)
		}
		peer := tailnetPeer{id: p.ID, name: name, ip: addr}
		s.byIP[addr], s.byTarget[s.target(peer)] = peer, peer
	}
	return s, nil
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

func (s *tailnetSnapshot) sameBinding(other *tailnetSnapshot) bool {
	return s != nil && other != nil && s.scope == other.scope && s.ip == other.ip &&
		s.device.Index == other.device.Index && s.device.Name == other.device.Name
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

type systemTailnet struct {
	api       systemTailscale
	ctx       context.Context
	cancel    context.CancelFunc
	slots     chan struct{}
	nextProbe int

	mu    sync.Mutex
	snap  *tailnetSnapshot
	ln    net.Listener
	links map[net.Conn]tailnetLink
	hosts []TailnetHost
}

// RexPairURL reports the local gateway's phone endpoint for the active
// system Tailscale address. The application wires it to the Rex gateway.
var RexPairURL func(netip.Addr) string

func (s *Server) tailnetAddress() netip.Addr {
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	if t := s.rem.tailnet; t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.ln != nil && t.snap.valid() {
			return t.snap.ip
		}
	}
	return netip.Addr{}
}

func (s *Server) tailnetStartLocked() {
	api := systemTailscaleClient()
	if api == nil || s.rem.tailnet != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &systemTailnet{api: api, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 16), links: map[net.Conn]tailnetLink{}}
	s.rem.tailnet = t
	go t.run(s)
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
	if t.snap.valid() {
		if p, ok := t.snap.byTarget[target]; ok {
			return &RemoteClient{Name: p.name, PublicKey: target}
		}
	}
	return nil
}

func (t *systemTailnet) status() TailnetStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.snap.valid() || t.ln == nil {
		return TailnetStatus{Enabled: true}
	}
	return TailnetStatus{Enabled: true, Running: true, Scope: t.snap.scope, Hosts: slices.Clone(t.hosts)}
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

func (t *systemTailnet) run(s *Server) {
	ticker := time.NewTicker(tailnetRefresh)
	defer ticker.Stop()
	for t.ctx.Err() == nil {
		t.refresh(s)
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (t *systemTailnet) refresh(s *Server) {
	ctx, cancel := context.WithTimeout(t.ctx, tailnetIO)
	st, err := t.api.Status(ctx)
	cancel()
	if err != nil {
		t.replace(nil, nil)
		return
	}
	snap, err := tailnetSnapshotFrom(st)
	if err != nil {
		t.replace(nil, nil)
		return
	}
	t.mu.Lock()
	if t.ctx.Err() != nil {
		t.mu.Unlock()
		return
	}
	ln := t.ln
	start := ln == nil || !t.snap.sameBinding(snap)
	if start {
		t.replaceLocked(nil, nil)
		lc := net.ListenConfig{Control: tailnetSocketControl(snap.device)}
		ln, err = lc.Listen(t.ctx, "tcp4", netip.AddrPortFrom(snap.ip, tailnetPort).String())
		if err != nil {
			t.mu.Unlock()
			return
		}
	}
	t.replaceLocked(snap, ln)
	t.mu.Unlock()
	if start {
		go t.accept(s, ln)
	}
	t.discover(snap, ln)
}

func (t *systemTailnet) discover(snap *tailnetSnapshot, ln net.Listener) {
	ctx, cancel := context.WithTimeout(t.ctx, tailnetIO)
	defer cancel()
	targets := make([]string, 0, len(snap.byTarget))
	for target := range snap.byTarget {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	jobs := make(chan string)
	found := make(chan TailnetHost, len(targets))
	checked := make(chan string, len(targets))
	var workers sync.WaitGroup
	for range min(len(targets), 4) {
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
	// Rotate which candidates go first when the pass runs out of time.
	// Only the refresh loop reads or writes this index.
	sent := 0
send:
	for i := range targets {
		target := targets[(t.nextProbe+i)%len(targets)]
		select {
		case jobs <- target:
			sent++
		case <-ctx.Done():
			break send
		}
	}
	t.nextProbe += sent
	if len(targets) > 0 {
		t.nextProbe %= len(targets)
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

func tailnetGreeting() string { return tailnetReady + strconv.Itoa(ProtocolVersion) + "\n" }

func (t *systemTailnet) dial(ctx context.Context, snap *tailnetSnapshot, p tailnetPeer, hello string) (net.Conn, error) {
	addr := netip.AddrPortFrom(p.ip, tailnetPort).String()
	who, err := t.api.WhoIs(ctx, addr)
	if err != nil || !snap.verifies(p, who) {
		return nil, errTailnetUnavailable
	}
	d := net.Dialer{Control: tailnetSocketControl(snap.device), LocalAddr: &net.TCPAddr{IP: snap.ip.AsSlice()}}
	c, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	want := tailnetGreeting()
	reply := make([]byte, len(want))
	if _, err := io.WriteString(c, hello); err == nil {
		_, err = io.ReadFull(c, reply)
		if err == nil && string(reply) == want {
			if hello != tailnetConnect {
				return c, nil
			}
			actual, err := t.api.WhoIs(ctx, c.RemoteAddr().String())
			if err == nil && snap.verifies(p, actual) {
				c.SetDeadline(time.Time{})
				return c, nil
			}
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
		addr := c.RemoteAddr().(*net.TCPAddr).AddrPort()
		var p tailnetPeer
		if t.ln == ln && snap.valid() {
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
		who, err := t.api.WhoIs(ctx, addr)
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
		want := tailnetGreeting()
		reply := make([]byte, len(want))
		if _, err := c.Write(b); err == nil {
			_, err = io.ReadFull(c, reply)
			if err == nil && string(reply) == want && stop() {
				return c, nil
			}
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
