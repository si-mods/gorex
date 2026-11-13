package tailnet

import (
	"strings"
	"testing"
)

// Pairing by address against a real machine is internal/rex's
// TestPairByAddressRexGo, where the server's test harness is.
func TestPairByAddressInput(t *testing.T) {
	tailcatEnv(t)
	for _, a := range []string{"", "mac.example.com", "http://192.0.2.10", "192.0.2.10:x"} {
		if _, err := PairByAddress(a, "rexgo-kqzt"); err == nil || strings.HasPrefix(err.Error(), "rex: ") {
			t.Errorf("PairByAddress(%q): %v", a, err)
		}
	}
	if _, err := ConfirmAddressPairing("123456"); err == nil {
		t.Error("confirmed with no pairing")
	}
	CancelAddressPairing("123456")
	if msg := MismatchAddressPairing("123456"); msg != AddressPairingWarning() || !strings.HasPrefix(msg, "the pairing did not match") {
		t.Errorf("warning %q", msg)
	}
}
