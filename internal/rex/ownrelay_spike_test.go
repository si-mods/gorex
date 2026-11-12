package rex

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Spike: the machine as its own relay (docs/host-links.md, "2. Path").
// The server runs a DERP server in its process on an address of the
// machine, with a self-signed certificate pinned in the tailcat address
// (DERPNode.CertName "sha256-raw:"), and no STUN. Clients get only that
// address: no other relay, no DERP map URL.

// ownRelay serves DERP on /derp and a GoRex path, /gorex/pair, on one
// TLS listener at ip, with a self-signed certificate (with ip in its
// SANs when ipSAN). It returns the region a tailcat address carries.
func ownRelay(t *testing.T, ip netip.Addr, ipSAN bool) (*tailcfg.DERPRegion, string) {
	t.Helper()
	cert, pin := selfSigned(t, ip, ipSAN)
	d := derpserver.New(key.NewNode(), func(string, ...any) {})
	mux := http.NewServeMux()
	mux.Handle("/derp", derpserver.Handler(d))
	mux.Handle("/derp/", derpserver.Handler(d)) // probe and latency-check, which netcheck asks without STUN
	mux.HandleFunc("/gorex/pair", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Hello string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"Reply": "hi " + req.Hello})
	})
	ln, err := net.Listen("tcp", netip.AddrPortFrom(ip, 0).String())
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(mux)
	hs.Listener.Close()
	hs.Listener = ln
	hs.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	hs.Config.ErrorLog = log.New(io.Discard, "", 0) // a wrong pin's handshakes
	// DERP upgrades an HTTP/1.1 connection: no HTTP/2.
	hs.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	hs.StartTLS()
	t.Cleanup(func() {
		hs.CloseClientConnections()
		hs.Close()
		d.Close()
	})
	return &tailcfg.DERPRegion{
		RegionID: 900, RegionCode: "own",
		Nodes: []*tailcfg.DERPNode{{
			Name: "900a", RegionID: 900,
			HostName: ip.String(), IPv4: ip.String(), IPv6: "none",
			DERPPort: ln.Addr().(*net.TCPAddr).Port,
			STUNPort: -1, // no STUN: nothing to measure, nowhere to ask
			CertName: "sha256-raw:" + pin,
		}},
	}, "https://" + ln.Addr().String()
}

// selfSigned makes a P-256 certificate and its pin, the hex SHA-256 of
// its DER.
func selfSigned(t *testing.T, ip netip.Addr, ipSAN bool) (tls.Certificate, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gorex"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ipSAN {
		tmpl.IPAddresses = []net.IP{ip.AsSlice()}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, hex.EncodeToString(sum[:])
}

// ownEnv is a GOREX_DIR of its own and an empty pool, as tailcatEnv,
// without its loopback DERP and STUN.
func ownEnv(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	old := spawnServer
	spawnServer = func() error { return errors.New("no server in tests") }
	t.Cleanup(func() { spawnServer = old })
	t.Cleanup(func() {
		tailcatMu.Lock()
		pool := tailcatClients
		tailcatClients = map[string]*pooledTailcat{}
		tailcatMu.Unlock()
		for _, p := range pool {
			p.tc.Close()
		}
	})
}

// ownServer starts a tailcat server in reg with remoteListen, as the
// session server does, and echoes lines on RemotePort.
func ownServer(t *testing.T, reg *tailcfg.DERPRegion) (*tailcat.Server, tailcat.Addr) {
	t.Helper()
	ts := &tailcat.Server{Key: key.NewNode(), PresharedKey: tailcat.NewPresharedKey(), Region: reg,
		Logf: func(string, ...any) {}}
	t.Cleanup(func() { ts.Close() })
	ln, addr, _, err := remoteListen(context.Background(), ts, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := c.Write([]byte(line)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ts, addr
}

// lanIPv4 is an IPv4 address of an interface of this machine that is up
// and not the loopback, found at run time.
func lanIPv4() (netip.Addr, bool) {
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		as, _ := i.Addrs()
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(n.IP); ok && ip.Unmap().Is4() && !ip.IsLinkLocalUnicast() {
					return ip.Unmap(), true
				}
			}
		}
	}
	return netip.Addr{}, false
}

// Q1: the address carries the region, pin, port and IP, and round-trips.
func TestOwnRelayAddress(t *testing.T) {
	reg, _ := ownRelay(t, netip.MustParseAddr("127.0.0.1"), true)
	_, addr := ownServer(t, reg)
	t.Logf("address length: %d characters", len(addr))
	raw, _ := tailcat.ParseAddrRaw(addr)
	j, _ := json.Marshal(raw)
	t.Logf("wire form: %s", scrubKeys(string(j)))

	ci, err := tailcat.ParseAddr(addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(ci.Region) != 1 || len(ci.Region[0].Nodes) != 1 {
		t.Fatalf("regions in the address: %+v", ci.Region)
	}
	got, want := ci.Region[0].Nodes[0], reg.Nodes[0]
	if got.HostName != want.HostName || got.IPv4 != want.IPv4 || got.IPv6 != want.IPv6 ||
		got.DERPPort != want.DERPPort || got.STUNPort != want.STUNPort || got.CertName != want.CertName {
		t.Errorf("node after ParseAddr = %+v\nwant %+v", got, want)
	}
	t.Logf("parsed: RegionID %d (server's %d), HostName %s, DERPPort %d, STUNPort %d, CertName %s",
		ci.Region[0].RegionID, reg.RegionID, got.HostName, got.DERPPort, got.STUNPort, got.CertName[:20]+"...")
	if again := ci.Addr(); again != addr {
		t.Errorf("ParseAddr then Addr differs:\n%s\n%s", again, addr)
	}
	if _, err := NormalizeRelay("127.0.0.1:8443"); err == nil {
		t.Error("NormalizeRelay took an IP address")
	} else {
		t.Logf("today's relay setting refuses an IP: %v", err)
	}
}

// scrubKeys shortens the keys of a wire form for the log.
func scrubKeys(s string) string {
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return s
	}
	for _, k := range []string{"ServerPublic", "ServerDiscoPublic", "PresharedKey"} {
		if _, ok := m[k]; ok {
			m[k] = "..."
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// Q2, Q3, Q4: GoRex's own client (dialTailcat), given only the address,
// meets the server at the machine's relay, exchanges bytes, and asks for
// no DERP map; then disco pings tell whether the path went direct.
func TestOwnRelayDial(t *testing.T) {
	ips := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if ip, ok := lanIPv4(); ok {
		ips = append(ips, ip)
	} else {
		t.Log("no non-loopback IPv4 interface: only the loopback is tried")
	}
	for _, ip := range ips {
		name := "loopback"
		if !ip.IsLoopback() {
			name = "interface"
		}
		for _, san := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/ipSAN=%v", name, san), func(t *testing.T) {
				ownEnv(t)
				watch := watchRemoteTCP(t)
				reg, _ := ownRelay(t, ip, san)
				ts, addr := ownServer(t, reg)

				ctx, cancel := context.WithTimeout(context.Background(), tailcatDial)
				defer cancel()
				t0 := time.Now()
				c, err := dialTailcat(ctx, string(addr))
				if err != nil {
					t.Fatalf("dialTailcat: %v", err)
				}
				defer c.Close()
				if err := lineTrip(c, 5*time.Second); err != nil {
					t.Fatalf("echo: %v", err)
				}
				t.Logf("first connection and echo, server just started: %v", time.Since(t0).Round(time.Millisecond))

				// Another client, now that the server is up: what a client
				// of a running session server waits.
				tc := &tailcat.Client{Server: addr, Logf: func(string, ...any) {}}
				defer tc.Close()
				t1 := time.Now()
				c2, err := tc.DialTCPPort(ctx, RemotePort)
				if err != nil {
					t.Fatalf("second client: %v", err)
				}
				if err := lineTrip(c2, 5*time.Second); err != nil {
					t.Fatalf("second client's echo: %v", err)
				}
				c2.Close()
				t.Logf("a new client's first connection and echo, server up: %v", time.Since(t1).Round(time.Millisecond))

				p := c.(*tailcatConn).p
				direct := ""
				for i := 0; i < 20 && direct == ""; i++ {
					pctx, cancel := context.WithTimeout(context.Background(), time.Second)
					r, err := p.tc.DiscoPing(pctx)
					cancel()
					switch {
					case err != nil:
						t.Logf("disco ping %d: %v", i, err)
					case r.Endpoint != "":
						direct = r.Endpoint
						t.Logf("disco ping %d: direct, endpoint %s, %.1f ms", i, r.Endpoint, r.LatencySeconds*1000)
					case i == 0 || i == 19:
						t.Logf("disco ping %d: over DERP region %d (%s)", i, r.DERPRegionID, r.DERPRegionCode)
					}
					if direct == "" {
						time.Sleep(250 * time.Millisecond)
					}
				}
				if direct == "" {
					t.Log("the path stayed on the relay after 20 disco pings")
				}
				if err := lineTrip(c, 5*time.Second); err != nil {
					t.Errorf("echo after the pings: %v", err)
				}
				ts.Close()
				watch()
			})
		}
	}
}

// A pin that does not match the relay's certificate: no connection.
func TestOwnRelayWrongPin(t *testing.T) {
	ownEnv(t)
	reg, _ := ownRelay(t, netip.MustParseAddr("127.0.0.1"), true)
	ts, _ := ownServer(t, reg)
	ci, err := tailcat.ParseAddr(ts.TailcatAddr())
	if err != nil {
		t.Fatal(err)
	}
	ci.Region[0].Nodes[0].CertName = "sha256-raw:" + strings.Repeat("00", 32)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c, err := dialTailcat(ctx, string(ci.Addr()))
	if err == nil {
		c.Close()
		t.Fatal("connected through a relay whose certificate is not the pinned one")
	}
	t.Logf("wrong pin: %v", err)
}

// Q3, by a running client: a tailcat client and server given a DERP map
// URL of a recorder ask it nothing, as the address carries the region.
func TestOwnRelayNoDERPMapFetch(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	rec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.String())
		mu.Unlock()
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer rec.Close()
	reg, _ := ownRelay(t, netip.MustParseAddr("127.0.0.1"), true)
	ts := &tailcat.Server{Region: reg, DERPMapURL: rec.URL, Logf: func(string, ...any) {}}
	defer ts.Close()
	ln, err := ts.Listen(context.Background(), "tcp", ":7465")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("hello\n"))
			c.Close()
		}
	}()
	tc := &tailcat.Client{Server: ts.TailcatAddr(), DERPMapURL: rec.URL, Logf: func(string, ...any) {}}
	defer tc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := tc.DialTCPPort(ctx, 7465)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	c.Close()
	if err != nil || line != "hello\n" {
		t.Fatalf("read %q, %v", line, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) > 0 {
		t.Fatalf("the DERP map URL was asked: %v", asked)
	}
	t.Log("connected; the DERP map URL was asked nothing")
}

// Q5: the relay's listener serves a GoRex path beside DERP, and a client
// reads the certificate's SHA-256 from the TLS state and checks it
// against the pin in the address.
func TestOwnRelayPairingPath(t *testing.T) {
	reg, base := ownRelay(t, netip.MustParseAddr("127.0.0.1"), false)
	pin := strings.TrimPrefix(reg.Nodes[0].CertName, "sha256-raw:")
	var seen string
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // the pin is the check
		VerifyConnection: func(cs tls.ConnectionState) error {
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			seen = hex.EncodeToString(sum[:])
			if seen != pin {
				return errors.New("certificate is not the pinned one")
			}
			return nil
		},
	}}}
	res, err := hc.Post(base+"/gorex/pair", "application/json", strings.NewReader(`{"Hello":"phone"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got struct{ Reply string }
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil || got.Reply != "hi phone" {
		t.Fatalf("reply %+v, %v", got, err)
	}
	t.Logf("pairing path on the DERP port: reply %q, proto %s, cert sha256 %s... matches the pin", got.Reply, res.Proto, seen[:16])

	// The same port still answers DERP's probe.
	res2, err := hc.Get(base + "/derp/probe")
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	t.Logf("/derp/probe on the same port: %s", res2.Status)
}

// watchRemoteTCP polls netstat for TCP connections of this process to an
// address that is not of this machine, and fails the test if it sees
// one. Windows only; a connection shorter than the poll may be missed.
func watchRemoteTCP(t *testing.T) (stop func()) {
	if runtime.GOOS != "windows" {
		t.Log("no netstat watch on " + runtime.GOOS)
		return func() {}
	}
	local := map[netip.Addr]bool{}
	as, _ := net.InterfaceAddrs()
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				local[ip.Unmap()] = true
			}
		}
	}
	pid := strconv.Itoa(os.Getpid())
	done, finished := make(chan struct{}), make(chan struct{})
	var foreign []string
	polls := 0
	go func() {
		defer close(finished)
		for {
			out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
			if err == nil {
				polls++
				for _, l := range strings.Split(string(out), "\n") {
					f := strings.Fields(l)
					if len(f) != 5 || f[4] != pid {
						continue
					}
					ap, err := netip.ParseAddrPort(f[2])
					if err != nil || ap.Addr().IsUnspecified() {
						continue
					}
					if ip := ap.Addr().Unmap(); !ip.IsLoopback() && !local[ip] {
						foreign = append(foreign, f[2])
					}
				}
			}
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	return func() {
		close(done)
		<-finished
		if len(foreign) > 0 {
			t.Errorf("TCP to addresses not of this machine: %v", foreign)
		} else {
			t.Logf("netstat: %d polls, no TCP connection of this process to an address not of this machine", polls)
		}
	}
}
