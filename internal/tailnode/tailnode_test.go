package tailnode

import (
	"strings"
	"testing"
)

func TestHostnames(t *testing.T) {
	for name, ok := range map[string]bool{"studio": true, "a": true, "a-b-1": true, "-a": false, "a-": false, "": false, "a_b": false, strings.Repeat("a", 64): false} {
		if ValidHostname(name) != ok {
			t.Errorf("ValidHostname(%q) = %v", name, !ok)
		}
	}
}

// TestControlURLCleartext: a control server is https, or http on this
// machine or a private network, judged by the URL's literal host.
func TestControlURLCleartext(t *testing.T) {
	for _, s := range []string{
		"https://headscale.example.com",
		"https://203.0.113.7",
		"https://[2001:db8::1]:8443",
		"http://localhost:8080",
		"http://LocalHost",
		"http://localhost.:8080",
		"http://127.0.0.1",
		"http://[::1]:8080",
		"http://10.0.0.1:8080",
		"http://172.16.5.4",
		"http://192.168.1.10",
		"http://169.254.1.1",
		"http://[fd00::1]",
		"http://[fe80::1%25en0]:8080",
		"http://[::ffff:192.168.1.10]",
	} {
		if !ValidControlURL(s) {
			t.Errorf("%s was refused", s)
		}
	}
	for _, s := range []string{
		"http://headscale.example.com",
		"http://203.0.113.7",
		"http://[2001:db8::1]:8080",
		"http://[::ffff:203.0.113.7]",
		"http://100.64.0.1",
		"http://[fd7a:115c:a1e0::1]",
		"https://:8443",
		"http://0.0.0.0",
		"http://headscale",
		"http://headscale.local",
		"http://localhost.example.com",
	} {
		if ValidControlURL(s) {
			t.Errorf("%s was accepted", s)
		}
	}
}
