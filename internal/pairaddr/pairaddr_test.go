package pairaddr

import (
	"context"
	"net"
	"strings"
	"testing"

	"tailscale.com/types/key"
)

// Ask takes what the app's Add Host shows its button for: an IP address,
// an IPv6 one in brackets, either with a port; nothing else.
func TestAskAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no dial: only the parse counts
	k := key.NewNode().Public()
	for _, a := range []string{"192.0.2.10", "192.0.2.10:7464", "::1", "[::1]", "[::1]:7464"} {
		if _, err := Ask(ctx, &net.Dialer{}, a, 7464, "phone", k); err == nil || !strings.Contains(err.Error(), "connecting to") {
			t.Errorf("Ask(%q): %v", a, err)
		}
	}
	for _, a := range []string{"", "[192.0.2.10]", "[::1", "mac.example.com", "http://192.0.2.10", "192.0.2.10:x"} {
		if _, err := Ask(ctx, &net.Dialer{}, a, 7464, "phone", k); err == nil || !strings.Contains(err.Error(), "pair by an IP address") {
			t.Errorf("Ask(%q): %v", a, err)
		}
	}
}

func TestPairTailcat(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		buf := make([]byte, 256)
		n, _ := server.Read(buf)
		if !strings.Contains(string(buf[:n]), `"op":"remote.pair"`) {
			return
		}
		_, _ = server.Write([]byte(`{"data":{"fingerprint":"1234-5678"}}` + "\n"))
	}()
	fp, err := PairTailcat(client, "web-rex-test")
	if err != nil || fp != "1234-5678" {
		t.Fatalf("PairTailcat = %q, %v", fp, err)
	}

	c2, s2 := net.Pipe()
	defer c2.Close()
	go func() {
		defer s2.Close()
		buf := make([]byte, 256)
		_, _ = s2.Read(buf)
		_, _ = s2.Write([]byte(`{"error":"rex: this machine is not allowed here"}` + "\n"))
	}()
	if _, err := PairTailcat(c2, "web-rex-test"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("PairTailcat error = %v", err)
	}
}
