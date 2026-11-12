//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorex/internal/pairaddr"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// The commitment and the code are pinned: a change breaks every peer.
func TestPairVectors(t *testing.T) {
	var kc, ks, nc, ns [32]byte
	for i := range 32 {
		kc[i], ks[i], nc[i], ns[i] = byte(i), byte(0x40+i), byte(0x80+i), byte(0xc0+i)
	}
	commit := pairCommit(kc, nc, []byte("laptop"))
	if got := hex.EncodeToString(commit[:]); got != "fe7f7ca9a331ffce67fb5340191bd85198c502e482f8f468aab106f5e25d8f60" {
		t.Errorf("commit %s", got)
	}
	if got := pairCode(bytes.Repeat([]byte{7}, 32), []byte("certificate"), kc, ks, nc, ns); got != "249468" {
		t.Errorf("code %s", got)
	}
	// The layout, written out once more.
	h := sha256.Sum256(bytes.Join([][]byte{[]byte("gorex-pair-v1/commit\x00"), kc[:], nc[:], []byte{0, 6}, []byte("laptop")}, nil))
	if h != commit {
		t.Error("the commitment is not of its documented layout")
	}
}

// TestPairByAddress: on the relay of Reachable at, a client that knows
// the IP address only pairs while the window is open, both sides show
// the same code, and the user approves it on the machine.
func TestPairByAddress(t *testing.T) {
	reg := tailcatEnv(t)
	s, _ := testServer(t)
	relayInsecure = true
	oldPort, oldIdle := ownRelayPort, ownRelayIdle
	ownRelayPort, ownRelayIdle = 0, 500*time.Millisecond
	t.Cleanup(func() { relayInsecure, ownRelayPort, ownRelayIdle = false, oldPort, oldIdle })
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
	st := status(func(st RemoteStatus) bool { return st.Running && st.ReachableAddress != "" })
	second, _ := tailcat.ParseAddr(tailcat.Addr(st.ReachableAddress))
	addr := fmt.Sprintf("127.0.0.1:%d", second.Region[0].Nodes[0].DERPPort)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// A second source, where the loopback has more than one address: macOS
	// has 127.0.0.1 only, and tests what needs two on the others.
	from2 := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)}}
	if ln, err := net.Listen("tcp", "127.0.0.2:0"); err != nil {
		from2 = nil
	} else {
		ln.Close()
	}
	ask := func(d *net.Dialer, k key.NodePublic, name string) (*AddressPairing, error) {
		if d == nil {
			d = &net.Dialer{}
		}
		// Every request that ends without approval closes the window.
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		return pairByAddressAs(ctx, d, addr, name, k)
	}
	waiting := func(n int) []RemoteAddrPending {
		t.Helper()
		return status(func(st RemoteStatus) bool { return len(st.PendingByAddress) == n }).PendingByAddress
	}
	get := func(path string) (int, string) {
		t.Helper()
		nc, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", path, pairUpgrade)
		res, err := http.ReadResponse(bufio.NewReader(nc), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	t.Run("window closed", func(t *testing.T) {
		code, body := get(pairPath)
		ucode, ubody := get("/gorex/nothing")
		if code != http.StatusNotFound || code != ucode || body != ubody {
			t.Errorf("closed: %d %q, an unknown path: %d %q", code, body, ucode, ubody)
		}
		if _, err := pairByAddressAs(ctx, &net.Dialer{}, addr, "laptop", key.NewNode().Public()); err == nil || !strings.Contains(err.Error(), "not pairing") {
			t.Errorf("asked with the window closed: %v", err)
		}
	})
	if err := c.StartPairing(); err != nil {
		t.Fatal(err)
	}

	t.Run("happy path", func(t *testing.T) {
		me, _ := parsePublicKey(ClientPublicKey())
		p, err := ask(nil, me, "laptop")
		if err != nil {
			t.Fatal(err)
		}
		w := waiting(1)
		if w[0].Code != p.Code || w[0].Name != "laptop" || w[0].Source != "127.0.0.1" || len(p.Code) != 6 {
			t.Fatalf("client's code %s, server's %+v", p.Code, w[0])
		}
		// The old path does not approve it, nor a wrong code.
		if err := c.ApprovePairing(me.String()); err == nil {
			t.Error("approved by key")
		}
		if err := c.ApprovePairingCode("x" + p.Code); err == nil {
			t.Error("approved by a wrong code")
		}
		if err := c.ApprovePairingCode(p.Code); err != nil {
			t.Fatal(err)
		}
		addrs, err := p.Confirm(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(addrs) != 2 || addrs[0] != st.ReachableAddress || addrs[1] != st.Address {
			t.Fatalf("addresses: %d", len(addrs))
		}
		allowed := readConfig(t).Allowed
		if len(allowed) != 1 || allowed[0].PublicKey != me.String() || allowed[0].Name != "laptop" {
			t.Fatalf("allowed %+v", allowed)
		}
		rc, err := ConnectRemote(ctx, addrs[0])
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		if h, err := rc.Hello(); err != nil || h.Client == nil || h.Client.PublicKey != me.String() {
			t.Fatalf("hello %+v %v", h.Client, err)
		}
	})

	t.Run("already allowed or waiting", func(t *testing.T) {
		me, _ := parsePublicKey(ClientPublicKey())
		p, err := ask(nil, me, "again")
		if err == nil {
			_, err = p.Confirm(ctx)
		}
		if err == nil || !strings.Contains(err.Error(), "allowed there already") {
			t.Errorf("allowed key: %v", err)
		}
		k := key.NewNode().Public()
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		s.remotePair(k.String(), "tailcat")
		if p, err = ask(nil, k, "laptop"); err == nil {
			_, err = p.Confirm(ctx)
		}
		if err == nil || !strings.Contains(err.Error(), "waits there already") {
			t.Errorf("a key waiting over tailcat: %v", err)
		}
		k = key.NewNode().Public()
		first, err := ask(nil, k, "laptop")
		if err != nil {
			t.Fatal(err)
		}
		defer first.Close()
		waiting(1)
		if from2 != nil {
			if p, err = ask(from2, k, "laptop"); err == nil {
				_, err = p.Confirm(ctx)
			}
			if err == nil || !strings.Contains(err.Error(), "waits there already") {
				t.Errorf("a key waiting by address: %v", err)
			}
		}
		first.Close()
		waiting(0)
	})

	t.Run("wrong opening", func(t *testing.T) {
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		nc, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		br := bufio.NewReader(nc)
		fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", pairPath, pairUpgrade)
		if res, err := http.ReadResponse(br, nil); err != nil || res.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("upgrade: %v", err)
		}
		kc, nonce := key.NewNode().Public().Raw32(), [32]byte{1}
		commit := pairCommit(kc, nonce, []byte("laptop"))
		nc.Write(commit[:])
		io.ReadFull(br, make([]byte, 64))
		opening := append(append(kc[:], nonce[:]...), binary.BigEndian.AppendUint16(nil, 6)...)
		nc.Write(append(opening, "laptoq"...))
		nc.SetReadDeadline(time.Now().Add(5 * time.Second))
		if n, err := br.Read(make([]byte, 1)); n != 0 || err == nil || strings.Contains(err.Error(), "timeout") {
			t.Errorf("a wrong opening: %d %v", n, err)
		}
		waiting(0)
	})

	t.Run("two waiting", func(t *testing.T) {
		if from2 == nil {
			t.Skip("the loopback has one address here")
		}
		a, err := ask(nil, key.NewNode().Public(), "one")
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		// One per source: a second from the same one is refused.
		if _, err := ask(nil, key.NewNode().Public(), "two"); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("a second from one source: %v", err)
		}
		b, err := ask(from2, key.NewNode().Public(), "two")
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		waiting(2)
		if err := c.ApprovePairingCode(a.Code); err == nil || !strings.Contains(err.Error(), "reject them all") {
			t.Errorf("approved one of two: %v", err)
		}
		// A rejection ends the window, and the other request with it.
		if err := c.RejectPairingCode(a.Code); err != nil {
			t.Fatal(err)
		}
		for _, p := range []*AddressPairing{a, b} {
			if _, err := p.Confirm(ctx); err == nil || !strings.Contains(err.Error(), "rejected") {
				t.Errorf("rejected: %v", err)
			}
		}
		waiting(0)
		if st, _ := c.RemoteStatus(); st.Pairing.Open {
			t.Error("the window is open after a rejection")
		}
		if len(readConfig(t).Allowed) != 1 {
			t.Error("a rejected key is allowed")
		}
	})

	t.Run("one code per window", func(t *testing.T) {
		// A client that takes the server's nonce, and so knows its code,
		// and leaves without opening: the window closes, or it would ask
		// until a code matched.
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		nc, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		br := bufio.NewReader(nc)
		fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", pairPath, pairUpgrade)
		if res, err := http.ReadResponse(br, nil); err != nil || res.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("upgrade: %v", err)
		}
		nc.Write(make([]byte, 32))
		if _, err := io.ReadFull(br, make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
		nc.Close()
		status(func(st RemoteStatus) bool { return !st.Pairing.Open })
	})

	t.Run("TLS 1.2", func(t *testing.T) {
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		nc, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", pairPath, pairUpgrade)
		if res, err := http.ReadResponse(bufio.NewReader(nc), nil); err != nil || res.StatusCode != http.StatusNotFound {
			t.Errorf("pairing over TLS 1.2: %v %v", res, err)
		}
	})

	t.Run("connection closed", func(t *testing.T) {
		p, err := ask(nil, key.NewNode().Public(), "laptop")
		if err != nil {
			t.Fatal(err)
		}
		waiting(1)
		p.Close()
		waiting(0)
	})

	t.Run("no confirmation", func(t *testing.T) {
		p, err := ask(nil, key.NewNode().Public(), "laptop")
		if err != nil {
			t.Fatal(err)
		}
		waiting(1)
		if err := c.ApprovePairingCode(p.Code); err != nil {
			t.Fatal(err)
		}
		// The user says the codes differ: nothing kept, and the warning.
		if err := p.Mismatch(); err != pairaddr.ErrUnknownDevice {
			t.Errorf("mismatch: %v", err)
		}
		if _, err := p.Confirm(ctx); err == nil {
			t.Error("addresses after a mismatch")
		}
	})

	t.Run("man in the middle", func(t *testing.T) {
		mitm := mitmProxy(t, addr)
		p, err := pairByAddressAs(ctx, &net.Dialer{}, mitm, "laptop", key.NewNode().Public())
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if w := waiting(1); w[0].Code == p.Code {
			t.Errorf("both sides show %s through a proxy of another certificate", p.Code)
		}
		p.Close()
		waiting(0)
	})

	t.Run("answer not the machine's", func(t *testing.T) {
		ks := second.ServerPublic.NodePublic
		pin := second.Region[0].Nodes[0].CertName
		if err := pairaddr.CheckAddrs([]string{st.ReachableAddress, st.Address}, ks, pin); err != nil {
			t.Fatal(err)
		}
		for what, err := range map[string]error{
			"another key":    pairaddr.CheckAddrs([]string{st.ReachableAddress, st.Address}, key.NewNode().Public(), pin),
			"another pin":    pairaddr.CheckAddrs([]string{st.ReachableAddress}, ks, "sha256-raw:"+strings.Repeat("00", 32)),
			"default first":  pairaddr.CheckAddrs([]string{st.Address, st.ReachableAddress}, ks, pin),
			"not an address": pairaddr.CheckAddrs([]string{st.ReachableAddress, "192.0.2.10"}, ks, pin),
			"none":           pairaddr.CheckAddrs(nil, ks, pin),
		} {
			if err != pairaddr.ErrUnknownDevice {
				t.Errorf("%s: %v", what, err)
			}
		}
	})
}

// mitmProxy relays TLS connections to addr, as somebody in the middle
// would: its own certificate to the client, its own connection on.
func mitmProxy(t *testing.T, addr string) string {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			out, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				in.Close()
				continue
			}
			go func() { io.Copy(out, in); out.Close() }()
			go func() { io.Copy(in, out); in.Close() }()
		}
	}()
	return ln.Addr().String()
}
