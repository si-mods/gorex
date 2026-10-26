package main

import (
	"errors"
	"strings"
	"testing"

	"gorex/internal/rex"
)

var testKey = "nodekey:" + strings.Repeat("0123456789abcdef", 4)

// wantMessage checks got against want: "" for no message, else a part of it.
func wantMessage(t *testing.T, what, got, want string) {
	t.Helper()
	if (want == "") != (got == "") || !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", what, got, want)
	}
}

func TestValidateClientKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{testKey, ""},
		{"  " + testKey + "\n", ""},
		{"", "Paste"},
		{testKey[8:], "not a public key"},
		{testKey[:20], "not a public key"},
		{testKey + "0", "not a public key"},
		{"nodekey:" + strings.Repeat("g", 64), "not a public key"},
	} {
		wantMessage(t, "validateClientKey("+tc.in+")", validateClientKey(tc.in), tc.want)
	}
}

func TestValidateNewClient(t *testing.T) {
	allowed := []rex.RemoteClient{{Name: "bob-desktop", PublicKey: testKey}}
	other := "nodekey:" + strings.Repeat("fedcba9876543210", 4)
	for _, tc := range []struct{ name, key, want string }{
		{"alice-laptop", other, ""},
		{" ", other, "Name the client"},
		{"alice-laptop", "x", "not a public key"},
		{"alice-laptop", " " + testKey, "bob-desktop"},
	} {
		wantMessage(t, "validateNewClient("+tc.name+")", validateNewClient(tc.name, tc.key, allowed), tc.want)
	}
}

func TestShortKey(t *testing.T) {
	if got, want := shortKey(testKey), "nodekey:012345…abcdef"; got != want {
		t.Errorf("shortKey = %q, want %q", got, want)
	}
	for _, s := range []string{"", "alice", "nodekey:abc"} {
		if got := shortKey(s); got != s {
			t.Errorf("shortKey(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestRemoteState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		st    rex.RemoteStatus
		down  bool
		label string
		kind  remoteKind
	}{
		{"off", rex.RemoteStatus{}, false, "Off", remoteOff},
		{"starting", rex.RemoteStatus{Enabled: true}, false, "Starting…", remoteStarting},
		{"listening", rex.RemoteStatus{Enabled: true, Running: true}, false, "Listening", remoteListening},
		{"retrying", rex.RemoteStatus{Enabled: true, LastError: "no route"}, false, "Error", remoteError},
		{"off with error", rex.RemoteStatus{LastError: "bad file"}, false, "Error", remoteError},
		{"down", rex.RemoteStatus{Enabled: true, Running: true}, true, "Server not reachable", remoteDown},
	} {
		label, detail, kind := remoteState(tc.st, tc.down)
		if label != tc.label || kind != tc.kind {
			t.Errorf("%s: got %q, %v; want %q, %v", tc.name, label, kind, tc.label, tc.kind)
		}
		if !strings.Contains(detail, tc.st.LastError) {
			t.Errorf("%s: detail %q lacks the server's error", tc.name, detail)
		}
	}
}

func TestServerMessage(t *testing.T) {
	if got := serverMessage(errors.New("rex: that client is allowed already")); got != "That client is allowed already" {
		t.Errorf("serverMessage = %q", got)
	}
}
