package tailnet

// What iOS asks of the device that tsnet can't do itself: follow the
// phone's network, and open its sockets again after a suspension. After
// rootshell (kitknox/trzsz-ssh-rootshell, iosbridge/tailnet.go and
// vpntunnel/tsengine/sockets.go, MIT). Both reach into tsnet's
// subsystems (Server.Sys), and into a tailcat client's (tailcatSystem),
// which are not a stable API; the tests catch a change.

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"sync"
	"time"
	"unsafe"

	"github.com/tailscale/tailcat"
	"github.com/tailscale/wireguard-go/device"
	"tailscale.com/tsd"
	"tailscale.com/types/key"
	"tailscale.com/wgengine"
	"tailscale.com/wgengine/magicsock"
)

// noInterface names no interface. netmon ignores "", so this is how a
// lost network clears the one before.
const noInterface = "rexgo-none"

// DefaultInterfaceChanged tells the device the interface that carries
// the default route now, such as "en0" or "pdp_ip0"; "" for none. iOS
// lets no app watch the routes, so without it the device keeps using a
// network that is gone.
func DefaultInterfaceChanged(name string) {
	setDefaultInterface(ifName(name))
	systems := tailcatSystems()
	if n, err := running(); err == nil {
		systems = append(systems, systemPeers{sys: n.Sys()})
	}
	for _, s := range systems {
		if m, ok := s.sys.NetMon.GetOK(); ok {
			m.InjectEvent()
		}
	}
}

func ifName(name string) string {
	if name == "" {
		return noInterface
	}
	return name
}

var (
	// resetWait bounds each step of a reset.
	resetWait = 10 * time.Second
	// unstickEvery is how often a close that waits is helped along.
	unstickEvery = 250 * time.Millisecond
)

// ResetSockets opens the UDP sockets of the device and of the tailcat
// clients anew, for the app back from the background: iOS reclaims a
// suspended app's sockets, WireGuard's receive loops end on the read
// error, and Tailscale never starts them again, leaving every peer on
// DERP. Open connections go on. A tailcat client whose reset doesn't
// finish is replaced by a new one. It fails when the device runs and its
// reset didn't finish in time; the device then needs a new start, which
// leaves a stuck one to close by itself.
func ResetSockets() error {
	resetTailcat()
	n, err := running()
	if err != nil {
		return nil // no device to reset
	}
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	st, _ := n.StatusWithPeers(ctx)
	done()
	var peers []key.NodePublic
	if st != nil {
		peers = st.Peers()
	}
	err = resetSockets(n.Sys(), peers)
	if errors.Is(err, errStuck) {
		mu.Lock()
		if node == n {
			stuck = true
		}
		mu.Unlock()
	}
	return err
}

// errStuck is a reset that didn't finish: a goroutine still waits on
// WireGuard's bind, holding its lock, and closing the device would wait
// for it too.
var errStuck = errors.New("socket reset is stuck")

// resetSockets closes WireGuard's bind, has magicsock open new sockets and
// opens the bind on them, which starts new receive loops; an update alone
// would read the old sockets. Only the bind restarts: the peers keep their
// sessions. Staged packets for peers may be dropped.
func resetSockets(sys *tsd.System, peers []key.NodePublic) error {
	mc, ok := sys.MagicSock.GetOK()
	e, ok2 := sys.Engine.GetOK()
	if !ok || !ok2 {
		return errors.New("tailnet not running")
	}
	dev, err := wireguardDevice(e)
	if err != nil {
		return err
	}
	var staged []*device.Peer
	for _, k := range peers {
		if p := dev.LookupPeer(device.NoisePublicKey(k.Raw32())); p != nil {
			staged = append(staged, p)
		}
	}
	// The close waits for the receive loops. One reading a socket a
	// racing rebind (a link change) handed it, or waiting for a buffer
	// that packets staged for a peer with no session hold, never sees it:
	// close the sockets again and drop the staged packets until it does.
	if err := await(func() error { dev.BindClose(); return nil }, func() {
		closeSockets(mc)
		for _, p := range staged {
			p.FlushStagedPackets()
		}
	}); err != nil {
		return err
	}
	mc.Rebind()
	if err := await(dev.BindUpdate, nil); err != nil {
		return err
	}
	mc.ReSTUN("rexgo-foreground")
	return nil
}

// await runs fn and returns its error, or errStuck when it doesn't return
// within resetWait, calling unstick, when given, every unstickEvery
// meanwhile.
func await(fn func() error, unstick func()) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	tick := time.NewTicker(unstickEvery)
	defer tick.Stop()
	deadline := time.After(resetWait)
	for {
		select {
		case err := <-done:
			return err
		case <-tick.C:
			if unstick != nil {
				unstick()
			}
		case <-deadline:
			return errStuck
		}
	}
}

// wireguardDevice is the engine's WireGuard device, an unexported field.
func wireguardDevice(e wgengine.Engine) (*device.Device, error) {
	v := reflect.ValueOf(e)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return nil, errors.New("no WireGuard device")
	}
	f := v.Elem().FieldByName("wgdev")
	if !f.IsValid() || f.Type() != reflect.TypeFor[*device.Device]() {
		return nil, errors.New("no WireGuard device")
	}
	dev := *(**device.Device)(unsafe.Pointer(f.UnsafeAddr()))
	if dev == nil {
		return nil, errors.New("no WireGuard device")
	}
	return dev, nil
}

// closeSockets closes magicsock's UDP sockets, unexported fields, without
// replacing them, which ends a receive loop reading them.
func closeSockets(mc *magicsock.Conn) {
	v := reflect.ValueOf(mc).Elem()
	for _, name := range []string{"pconn4", "pconn6"} {
		f := v.FieldByName(name)
		if f.IsValid() && f.Type() == reflect.TypeFor[magicsock.RebindingUDPConn]() {
			(*magicsock.RebindingUDPConn)(unsafe.Pointer(f.UnsafeAddr())).Close()
		}
	}
}

// systemPeers is an engine's subsystems and its peers.
type systemPeers struct {
	addr  string
	c     *tailcat.Client
	sys   *tsd.System
	peers []key.NodePublic
}

// resetTailcat resets the sockets of each started tailcat client. One
// stuck leaves the pool and closes by itself; the next dial makes another.
func resetTailcat() {
	for _, s := range tailcatSystems() {
		if errors.Is(resetSockets(s.sys, s.peers), errStuck) {
			tcMu.Lock()
			if tcClients[s.addr] == s.c {
				delete(tcClients, s.addr)
			}
			tcMu.Unlock()
			go s.c.Close()
		}
	}
}

// tailcatSystems is the subsystems of each started tailcat client, with
// its server as its peer.
func tailcatSystems() []systemPeers {
	tcMu.Lock()
	clients := maps.Clone(tcClients)
	tcMu.Unlock()
	var systems []systemPeers
	for addr, c := range clients {
		sys := tailcatSystem(c)
		if sys == nil {
			continue
		}
		s := systemPeers{addr: addr, c: c, sys: sys}
		if ci, err := tailcat.ParseAddr(tailcat.Addr(addr)); err == nil {
			s.peers = []key.NodePublic{ci.ServerPublic.NodePublic}
		}
		systems = append(systems, s)
	}
	return systems
}

// tailcatSystem is a started tailcat client's subsystems, unexported:
// Client.lb.sys, read once Client.started says Client.lb is set. nil for a
// client not started, or starting now, whose sockets are new.
func tailcatSystem(c *tailcat.Client) *tsd.System {
	v := reflect.ValueOf(c).Elem()
	mu, started, lb := v.FieldByName("startMu"), v.FieldByName("started"), v.FieldByName("lb")
	if !mu.IsValid() || mu.Type() != reflect.TypeFor[sync.Mutex]() ||
		!started.IsValid() || started.Kind() != reflect.Bool ||
		!lb.IsValid() || lb.Kind() != reflect.Pointer || lb.Type().Elem().Kind() != reflect.Struct {
		return nil
	}
	sf, ok := lb.Type().Elem().FieldByName("sys")
	if !ok || sf.Type != reflect.TypeFor[tsd.System]() {
		return nil
	}
	m := (*sync.Mutex)(unsafe.Pointer(mu.UnsafeAddr()))
	if !m.TryLock() {
		return nil
	}
	on := *(*bool)(unsafe.Pointer(started.UnsafeAddr()))
	p := *(*unsafe.Pointer)(unsafe.Pointer(lb.UnsafeAddr()))
	m.Unlock()
	if !on || p == nil {
		return nil
	}
	return (*tsd.System)(unsafe.Add(p, sf.Offset))
}

// forgetPath has a started tailcat client forget its path to its server
// at addr: the direct address magicsock trusts for a few seconds more
// (resetSockets, as after a suspension) and the WireGuard session, so
// that the next packet shakes hands anew.
func forgetPath(c *tailcat.Client, addr string) {
	sys := tailcatSystem(c)
	ci, err := tailcat.ParseAddr(tailcat.Addr(addr))
	if sys == nil || err != nil {
		return
	}
	resetSockets(sys, []key.NodePublic{ci.ServerPublic.NodePublic})
	e, ok := sys.Engine.GetOK()
	if !ok {
		return
	}
	dev, err := wireguardDevice(e)
	if err != nil {
		return
	}
	if p := dev.LookupPeer(device.NoisePublicKey(ci.ServerPublic.Raw32())); p != nil {
		p.ExpireCurrentKeypairs()
	}
}
