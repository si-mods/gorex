package rexapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
)

func TestWTCertManagerRotation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cert1, hash1, err := loadOrCreateWTCert(dir, now)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pub, ok := cert1.Leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Fatalf("expected ECDSA P-256 key, got %T", cert1.Leaf.PublicKey)
	}
	if d := cert1.Leaf.NotAfter.Sub(cert1.Leaf.NotBefore); d > 14*24*time.Hour {
		t.Fatalf("cert lifetime %v exceeds 14d WebTransport limit", d)
	}

	// Reloading within validity window returns the same cert & hash.
	_, hash2, err := loadOrCreateWTCert(dir, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("reload cert: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("expected same hash on reload, got %q vs %q", hash1, hash2)
	}

	// Within 3 days of expiry, cert rotates automatically.
	_, hash3, err := loadOrCreateWTCert(dir, now.Add(8*24*time.Hour))
	if err != nil {
		t.Fatalf("rotate cert: %v", err)
	}
	if hash3 == hash1 {
		t.Fatalf("expected cert rotation within 3 days of expiry")
	}
}

func TestWebTransportControlAndHTTP(t *testing.T) {
	f := startFixture(t)
	if err := SaveWeb(TailnetDir(), WebConfig{Origins: []string{"https://web.gorex.uk"}}); err != nil {
		t.Fatalf("save web config: %v", err)
	}
	f.g.web = newWebOrigins(TailnetDir())
	f.g.lan = newLANListener(f.g, TailnetDir())
	f.g.lan.apply(LANConfig{
		Enabled: true,
		Listen:  "127.0.0.1:0",
		Allow:   []string{"127.0.0.1"},
	})
	defer f.g.lan.close()

	addr, hash, errMsg := f.g.lan.wt.status()
	if errMsg != "" || addr == "" || hash == "" {
		t.Fatalf("wt status invalid: addr=%q hash=%q err=%q", addr, hash, errMsg)
	}

	dialWT := func(origin string) (*webtransport.Session, error) {
		d := &webtransport.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"h3"},
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return errors.New("no cert")
					}
					if got := wtCertHash(rawCerts[0]); got != hash {
						return errors.New("cert hash mismatch: " + got)
					}
					return nil
				},
			},
		}
		defer d.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		_, sess, err := d.Dial(ctx, "https://"+addr+"/api/wt", hdr)
		return sess, err
	}

	// Untrusted origin is rejected.
	if sess, err := dialWT("https://evil.example"); err == nil {
		sess.CloseWithError(0, "")
		t.Fatal("expected untrusted origin to be rejected")
	}

	// Trusted origin succeeds.
	sess, err := dialWT("https://web.gorex.uk")
	if err != nil {
		t.Fatalf("dial trusted wt: %v", err)
	}
	defer sess.CloseWithError(0, "")

	// 1. Control stream (0x01) -> hello / welcome.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	str, err := sess.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}
	if _, err := str.Write([]byte{wtStreamSocket}); err != nil {
		t.Fatalf("write stream kind: %v", err)
	}
	writeShortString(str, "/api/control")
	writeShortString(str, controlProtocol)

	var ack [1]byte
	if _, err := io.ReadFull(str, ack[:]); err != nil || ack[0] != 0x00 {
		t.Fatalf("expected ack 0x00, got %v err=%v", ack, err)
	}
	hello, _ := json.Marshal(map[string]any{
		"type":             "hello",
		"api_revision":     apiRevision,
		"api_min_revision": apiMinRevision,
		"client":           map[string]any{"name": "wt-test", "version": "1"},
	})
	if err := writeWTFrame(str, wtFrameText, hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	fType, payload, err := readWTFrame(str)
	if err != nil || fType != wtFrameText {
		t.Fatalf("read welcome: type=%d err=%v", fType, err)
	}
	var welcome map[string]any
	if err := json.Unmarshal(payload, &welcome); err != nil {
		t.Fatalf("unmarshal welcome: %v", err)
	}
	if welcome["type"] != "welcome" {
		t.Fatalf("expected welcome, got %v", welcome)
	}
	_ = writeWTClose(str, 1000, "")
	str.Close()

	// 2. HTTP stream (0x03) -> GET /api/help.
	hstr, err := sess.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open http stream: %v", err)
	}
	reqMeta, _ := json.Marshal(wtHTTPReq{Method: http.MethodGet, Path: "/api/help"})
	if _, err := hstr.Write([]byte{wtStreamHTTP}); err != nil {
		t.Fatalf("write http kind: %v", err)
	}
	if err := writeLenBytes(hstr, reqMeta); err != nil {
		t.Fatalf("write req meta: %v", err)
	}
	if err := writeLenBytes(hstr, nil); err != nil {
		t.Fatalf("write req body: %v", err)
	}
	respMetaBytes, err := readLenBytes(hstr)
	if err != nil {
		t.Fatalf("read resp meta: %v", err)
	}
	var respMeta wtHTTPResp
	if err := json.Unmarshal(respMetaBytes, &respMeta); err != nil {
		t.Fatalf("unmarshal resp meta: %v", err)
	}
	if respMeta.Status != http.StatusOK {
		t.Fatalf("expected 200 from /api/help, got %d", respMeta.Status)
	}
	body, err := readLenBytes(hstr)
	if err != nil || len(body) == 0 {
		t.Fatalf("expected non-empty /api/help body, len=%d err=%v", len(body), err)
	}
	hstr.Close()

	// Verify wtListenAddr increments port.
	if got, err := wtListenAddr("192.168.1.50:7297"); err != nil || got != "192.168.1.50:7298" {
		t.Fatalf("wtListenAddr = %q, %v", got, err)
	}
	_ = net.IPv4(127, 0, 0, 1)
}

func writeShortString(w io.Writer, s string) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(len(s)))
	_, _ = w.Write(b[:])
	if len(s) > 0 {
		_, _ = w.Write([]byte(s))
	}
}
