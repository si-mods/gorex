package coordproto

import (
	"strings"
	"testing"
)

func TestJoinURL(t *testing.T) {
	host, fp, err := SplitJoinURL("https://192.168.88.234:7465/0123456789abcdef")
	if err != nil || host != "192.168.88.234:7465" || fp != "0123456789abcdef" {
		t.Fatalf("SplitJoinURL: %v %v %v", host, fp, err)
	}
	if got := JoinURL(host, fp); got != "https://192.168.88.234:7465/0123456789abcdef" {
		t.Errorf("JoinURL gave %q", got)
	}
	// 443, the default, reads without a port on both ways.
	host, fp, err = SplitJoinURL("https://coord.yibo.si/0123456789abcdef")
	if err != nil || host != "coord.yibo.si" || fp != "0123456789abcdef" {
		t.Fatalf("SplitJoinURL without a port: %v %v %v", host, fp, err)
	}
	if got := JoinURL("coord.yibo.si:443", fp); got != "https://coord.yibo.si/0123456789abcdef" {
		t.Errorf("JoinURL hid 443: %q", got)
	}
	if _, _, err := SplitJoinURL("https:///0123456789abcdef"); err == nil {
		t.Error("a URL without a host split")
	}
	if _, _, err := SplitJoinURL("https://coord.yibo.si/"); err == nil {
		t.Error("a URL without a fingerprint split")
	}
}

func TestFingerprintOfKey(t *testing.T) {
	k := "nodekey:" + strings.Repeat("0123456789abcdef", 4)
	if got := (Member{NodeKey: k}).Fingerprint(); got != "01234567-cdef" {
		t.Fatalf("fingerprint of a key: %q", got)
	}
	if got := (Member{NodeKey: "not a key"}).Fingerprint(); got != "" {
		t.Fatalf("fingerprint of what is not a key: %q", got)
	}
}

func TestNewClientChecks(t *testing.T) {
	if _, err := NewClient("https://coord.example/0123456789abcdef", ""); err == nil {
		t.Error("an empty network key made a client")
	}
	if _, err := NewClient("not a URL", "key"); err == nil {
		t.Error("a wrong URL made a client")
	}
}
