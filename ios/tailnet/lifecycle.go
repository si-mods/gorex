package tailnet

// What iOS asks of the device that tsnet can't do itself: follow the
// phone's network, and open its sockets again after a suspension. After
// rootshell (kitknox/trzsz-ssh-rootshell, iosbridge/tailnet.go and
// vpntunnel/tsengine/sockets.go, MIT). Both reach into tsnet's
// subsystems (Server.Sys), which are not a stable API; the tests catch a
// change.

import (
	"context"
	"errors"
	"reflect"
	"time"
	"unsafe"

	"github.com/tailscale/wireguard-go/device"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsd"
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
	if n, err := running(); err == nil {
		if m, ok := n.Sys().NetMon.GetOK(); ok {
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

// ResetSockets opens the device's UDP sockets anew, for the app back from
// the background: iOS reclaims a suspended app's sockets, WireGuard's
// receive loops end on the read error, and Tailscale never starts them
// again, leaving every peer on DERP. Open connections go on. It fails when
// the device isn't running or the reset didn't finish in time; the device
// then needs a new start.
func ResetSockets() error {
	n, err := running()
	if err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	st, _ := n.StatusWithPeers(ctx)
	done()
	return resetSockets(n.Sys(), st)
}

// resetSockets closes WireGuard's bind, has magicsock open new sockets and
// opens the bind on them, which starts new receive loops; an update alone
// would read the old sockets. Only the bind restarts: the peers keep their
// sessions. st gives the peers whose staged packets may be dropped.
func resetSockets(sys *tsd.System, st *ipnstate.Status) error {
	mc, ok := sys.MagicSock.GetOK()
	e, ok2 := sys.Engine.GetOK()
	if !ok || !ok2 {
		return errors.New("tailnet not running")
	}
	dev, err := wireguardDevice(e)
	if err != nil {
		return err
	}
	var peers []*device.Peer
	if st != nil {
		for _, k := range st.Peers() {
			if p := dev.LookupPeer(device.NoisePublicKey(k.Raw32())); p != nil {
				peers = append(peers, p)
			}
		}
	}
	// The close waits for the receive loops. One reading a socket a
	// racing rebind (a link change) handed it, or waiting for a buffer
	// that packets staged for a peer with no session hold, never sees it:
	// close the sockets again and drop the staged packets until it does.
	if !await(func() { dev.BindClose() }, func() {
		closeSockets(mc)
		for _, p := range peers {
			p.FlushStagedPackets()
		}
	}) {
		return errors.New("socket reset is stuck")
	}
	mc.Rebind()
	if !await(func() { err = dev.BindUpdate() }, nil) {
		return errors.New("socket reset is stuck")
	}
	if err != nil {
		return err
	}
	mc.ReSTUN("rexgo-foreground")
	return nil
}

// await runs fn and tells whether it returned within resetWait, calling
// unstick, when given, every unstickEvery meanwhile.
func await(fn, unstick func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	tick := time.NewTicker(unstickEvery)
	defer tick.Stop()
	deadline := time.After(resetWait)
	for {
		select {
		case <-done:
			return true
		case <-tick.C:
			if unstick != nil {
				unstick()
			}
		case <-deadline:
			return false
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
