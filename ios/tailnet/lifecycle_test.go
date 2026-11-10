package tailnet

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

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
	if !await(func() {}, nil) {
		t.Error("await of a step that returns: false")
	}
	// A step that waits for the unstick returns.
	free := make(chan struct{})
	if !await(func() { <-free }, func() {
		select {
		case <-free:
		default:
			close(free)
		}
	}) {
		t.Error("await of a step its unstick frees: false")
	}
	stuck := make(chan struct{})
	defer close(stuck)
	if await(func() { <-stuck }, func() {}) {
		t.Error("await of a stuck step: true")
	}
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
