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
