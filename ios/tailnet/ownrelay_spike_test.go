package tailnet

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Spike, as internal/rex's TestOwnRelayDial: Rex Go's engine dials a
// machine that is its own relay, a DERP server on 127.0.0.1 with a
// self-signed certificate pinned in the address, no STUN, with its
// tailcat code unchanged (dialAddr).
func TestOwnRelaySpike(t *testing.T) {
	tailcatEnv(t)
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)

	d := derpserver.New(key.NewNode(), func(string, ...any) {})
	mux := http.NewServeMux()
	mux.Handle("/derp", derpserver.Handler(d))
	mux.Handle("/derp/", derpserver.Handler(d))
	hs := httptest.NewUnstartedServer(mux)
	hs.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}}
	hs.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	hs.StartTLS()
	t.Cleanup(func() { hs.CloseClientConnections(); hs.Close(); d.Close() })

	ts := &tailcat.Server{Key: key.NewNode(), PresharedKey: tailcat.NewPresharedKey(), Logf: func(string, ...any) {},
		Region: &tailcfg.DERPRegion{RegionID: 900, RegionCode: "own", Nodes: []*tailcfg.DERPNode{{
			Name: "900a", RegionID: 900, HostName: "127.0.0.1", IPv4: "127.0.0.1", IPv6: "none",
			DERPPort: hs.Listener.Addr().(*net.TCPAddr).Port, STUNPort: -1,
			CertName: "sha256-raw:" + hex.EncodeToString(sum[:]),
		}}}}
	t.Cleanup(func() { ts.Close() })
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
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()

	addr := string(ts.TailcatAddr())
	setHosts(t, tailcatHost{ID: "mac", Address: addr}) // SetTailcatHosts takes it
	ctx, cancel := context.WithTimeout(context.Background(), tailcatDial)
	defer cancel()
	t0 := time.Now()
	c, err := dialAddr(ctx, addr, 7465)
	if err != nil {
		t.Fatalf("dialAddr: %v", err)
	}
	defer c.Close()
	fmt.Fprintf(c, "ping\n")
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if line, err := bufio.NewReader(c).ReadString('\n'); err != nil || line != "ping\n" {
		t.Fatalf("echo = %q, %v", line, err)
	}
	t.Logf("Rex Go's dialAddr through the machine's own relay: connected and echoed in %v", time.Since(t0).Round(time.Millisecond))
}
