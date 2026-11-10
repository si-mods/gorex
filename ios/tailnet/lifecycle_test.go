package tailnet

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/tailnode"

	"tailscale.com/tsd"
	"tailscale.com/wgengine"
	"tailscale.com/wgengine/magicsock"
)

func TestNotRunning(t *testing.T) {
	Stop()
	if err := ResetSockets(); err == nil {
		t.Error("ResetSockets with no device: no error")
	}
	DefaultInterfaceChanged("en0")
	DefaultInterfaceChanged("")
}

func TestIfName(t *testing.T) {
	for in, want := range map[string]string{"en0": "en0", "pdp_ip0": "pdp_ip0", "": noInterface} {
		if got := ifName(in); got != want {
			t.Errorf("ifName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAwait(t *testing.T) {
	defer func(w, e time.Duration) { resetWait, unstickEvery = w, e }(resetWait, unstickEvery)
	resetWait, unstickEvery = 200*time.Millisecond, 10*time.Millisecond
	if err := await(func() error { return nil }, nil); err != nil {
		t.Errorf("await of a step that returns: %v", err)
	}
	if err := await(func() error { return errors.New("x") }, nil); err == nil || err.Error() != "x" {
		t.Errorf("await of a step that fails: %v", err)
	}
	// A step that waits for the unstick returns.
	free := make(chan struct{})
	if err := await(func() error { <-free; return nil }, func() {
		select {
		case <-free:
		default:
			close(free)
		}
	}); err != nil {
		t.Errorf("await of a step its unstick frees: %v", err)
	}
	stuck := make(chan struct{})
	defer close(stuck)
	if err := await(func() error { <-stuck; return nil }, func() {}); err != errStuck {
		t.Errorf("await of a stuck step: %v", err)
	}
}

// A device stuck in a socket reset doesn't hold up the next start: its
// close, which would wait for the reset, is left to run by itself. One
// not stuck closes before Stop returns.
func TestStuckDeviceDoesNotHoldStart(t *testing.T) {
	defer func(f func(*tailnode.Node)) { closeNode = f }(closeNode)
	wedged := make(chan struct{})
	defer close(wedged)
	closeNode = func(*tailnode.Node) { <-wedged }
	fake := func(s bool) {
		p, err := startProxy(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		node, prox, cancel, stuck = tailnode.New(tailnode.Options{}), p, func() {}, s
		mu.Unlock()
	}

	fake(false)
	done := make(chan struct{})
	go func() { Stop(); close(done) }()
	select {
	case <-done:
		t.Fatal("Stop returned before the device closed")
	case <-time.After(100 * time.Millisecond):
	}
	wedged <- struct{}{}
	<-done

	fake(true)
	started := make(chan error, 1)
	go func() {
		started <- Start(`{"hostname":"rexgo-test","control_url":"http://127.0.0.1:1"}`, t.TempDir(), nil)
	}()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Start waited for the stuck device")
	}
	closeNode = func(n *tailnode.Node) { n.Close() }
	Stop()
}

// fakeSystem is a system with a WireGuard engine of a fake tun, which
// binds UDP sockets on the host and reaches no network.
func fakeSystem(t *testing.T) *tsd.System {
	sys := tsd.NewSystem()
	e, err := wgengine.NewFakeUserspaceEngine(t.Logf, 0, sys.Set, sys.HealthTracker.Get(), sys.UserMetricsRegistry(), sys.Bus.Get())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	sys.Set(e)
	return sys
}

func TestResetSockets(t *testing.T) {
	if err := resetSockets(tsd.NewSystem(), nil); err == nil {
		t.Error("reset with no engine: no error")
	}
	sys := fakeSystem(t)
	if _, err := wireguardDevice(sys.Engine.Get()); err != nil {
		t.Fatal(err)
	}
	// As iOS reclaiming the sockets: the UDP receive loops end, DERP's stays.
	waitLoops(t, 3)
	closeSockets(sys.MagicSock.Get())
	waitLoops(t, 1)
	if err := resetSockets(sys, nil); err != nil {
		t.Fatal(err)
	}
	waitLoops(t, 3)
	if err := resetSockets(sys, nil); err != nil {
		t.Fatal(err)
	}
	waitLoops(t, 3)
}

// waitLoops waits for n of WireGuard's receive loops to run.
func waitLoops(t *testing.T, n int) {
	t.Helper()
	var got int
	for range 100 {
		buf := make([]byte, 1<<20)
		got = strings.Count(string(buf[:runtime.Stack(buf, true)]), "device.(*Device).RoutineReceiveIncoming(")
		if got == n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d receive loops, want %d", got, n)
}

// The fields closeSockets reaches; it skips any it doesn't find.
func TestMagicsockFields(t *testing.T) {
	ty := reflect.TypeFor[magicsock.Conn]()
	for _, name := range []string{"pconn4", "pconn6"} {
		if f, ok := ty.FieldByName(name); !ok || f.Type != reflect.TypeFor[magicsock.RebindingUDPConn]() {
			t.Errorf("magicsock.Conn.%s: not a RebindingUDPConn", name)
		}
	}
}
