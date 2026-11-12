//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorex/ios/tailnet"

	"github.com/tailscale/tailcat"
)

// TestPairByAddressRexGo: Rex Go's engine pairs by address with the
// phone's tailcat key, as the app calls it; then it reaches the gateway
// port through the address it got, as a host of a Rex Go code. A
// mismatch keeps nothing; a cancel ends the request.
func TestPairByAddressRexGo(t *testing.T) {
	reg := tailcatEnv(t)
	s, _ := testServer(t)
	relayInsecure = true
	oldPort, oldIdle := ownRelayPort, ownRelayIdle
	ownRelayPort, ownRelayIdle = 0, 500*time.Millisecond
	t.Cleanup(func() { relayInsecure, ownRelayPort, ownRelayIdle = false, oldPort, oldIdle })
	_, gw := fakeGateway(t)
	c := pipe(s, "")
	defer c.Close()
	for _, err := range []error{c.SetRelay(fmt.Sprintf("localhost:%d", reg.Nodes[0].DERPPort)), c.EnableRemote(), c.SetReachable("127.0.0.1")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	status := func(ok func(RemoteStatus) bool) RemoteStatus {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			st, err := c.RemoteStatus()
			if err != nil {
				t.Fatal(err)
			}
			if ok(st) {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("status %+v", st)
			}
		}
	}
	waiting := func(n int) []RemoteAddrPending {
		t.Helper()
		return status(func(st RemoteStatus) bool { return len(st.PendingByAddress) == n }).PendingByAddress
	}
	st := status(func(st RemoteStatus) bool { return st.Running && st.ReachableAddress != "" })
	own, _ := tailcat.ParseAddr(tailcat.Addr(st.ReachableAddress))
	addr := fmt.Sprintf("127.0.0.1:%d", own.Region[0].Nodes[0].DERPPort)

	if err := tailnet.SetTailcatKey(tailnet.NewTailcatKey()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tailnet.SetTailcatHosts("[]") })
	// ask opens the window, as each request that ends unapproved closes it.
	ask := func() string {
		t.Helper()
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		code, err := tailnet.PairByAddress(addr, "rexgo-test")
		if err != nil {
			t.Fatal(err)
		}
		if w := waiting(1); w[0].Code != code || w[0].Name != "rexgo-test" {
			t.Fatalf("phone's code %s, machine's %+v", code, w[0])
		}
		return code
	}

	t.Run("cancel", func(t *testing.T) {
		ask()
		tailnet.CancelAddressPairing()
		waiting(0)
		if _, err := tailnet.ConfirmAddressPairing(); err == nil {
			t.Error("confirmed a pairing given up")
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		ask()
		if msg := tailnet.MismatchAddressPairing(); !strings.HasPrefix(msg, "the pairing did not match") {
			t.Errorf("warning %q", msg)
		}
		waiting(0)
		if a := readConfig(t).Allowed; len(a) != 0 {
			t.Errorf("allowed after a mismatch: %+v", a)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		code := ask()
		type confirmed struct {
			addr string
			err  error
		}
		done := make(chan confirmed, 1)
		go func() {
			a, err := tailnet.ConfirmAddressPairing()
			done <- confirmed{a, err}
		}()
		if err := c.ApprovePairingCode(code); err != nil {
			t.Fatal(err)
		}
		got := <-done
		if got.err != nil || got.addr != st.ReachableAddress {
			t.Fatalf("confirm: %v; the address of Reachable at: %v", got.err, got.addr == st.ReachableAddress)
		}
		if a := readConfig(t).Allowed; len(a) != 1 || a[0].PublicKey != tailnet.TailcatKey() || a[0].Name != "rexgo-test" {
			t.Fatalf("allowed %+v", a)
		}

		// The app saves the address as a scanned one, and its URL is
		// http://<id>.tailcat.invalid:7297 through the engine's proxy.
		b, _ := json.Marshal([]map[string]string{{"id": "mac", "address": got.addr}})
		if err := tailnet.SetTailcatHosts(string(b)); err != nil {
			t.Fatal(err)
		}
		j, err := tailnet.Proxy()
		if err != nil {
			t.Fatal(err)
		}
		var p struct{ Address, Password string }
		json.Unmarshal([]byte(j), &p)
		hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true,
			Proxy: http.ProxyURL(&url.URL{Scheme: "socks5", User: url.UserPassword("tsnet", p.Password), Host: p.Address})}}
		res := make(chan string, 1)
		go func() {
			r, err := hc.Get("http://mac.tailcat.invalid:7297/")
			if err != nil {
				res <- err.Error()
				return
			}
			defer r.Body.Close()
			body, _ := io.ReadAll(r.Body)
			res <- string(body)
		}()
		select {
		case g := <-gw:
			defer g.c.Close()
			if g.peer.PublicKey != tailnet.TailcatKey() || g.peer.Name != "rexgo-test" {
				t.Errorf("the gateway got %+v", g.peer)
			}
			io.WriteString(g.c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
		case r := <-res:
			t.Fatalf("no gateway connection: %s", r)
		}
		if r := <-res; r != "ok" {
			t.Errorf("GET through the paired address = %q", r)
		}
	})
}
