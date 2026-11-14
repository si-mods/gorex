package rexapi

import (
	"context"
	"errors"
	"net"
	"testing"

	"gorex/internal/rex"

	"tailscale.com/ipn/ipnstate"
)

// desktopFakeNode is a fakeNode the broker can use.
type desktopFakeNode struct{ *fakeNode }

func (n desktopFakeNode) StatusWithPeers(ctx context.Context) (*ipnstate.Status, error) {
	return n.Status(ctx)
}

func (desktopFakeNode) Listen(string, string) (net.Listener, error) {
	return nil, errors.New("not in this test")
}

func (desktopFakeNode) DialPeer(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("not in this test")
}

// TestDesktopNode gives the broker the running node of the gateway in
// this process, and nothing while there is none or it is off.
func TestDesktopNode(t *testing.T) {
	f := startTailnetFixture(t)
	node := &desktopFakeNode{f.node}
	f.g.tailnet.newNode = func(string, string, string, string, []string) tailnetNode { return node }
	if DesktopNode() != nil {
		t.Fatal("a node with no gateway in the process")
	}
	setDesktop(f.g)
	defer clearDesktop(f.g)
	if n := DesktopNode(); n != nil {
		t.Fatalf("node %v while built-in Tailscale is off", n)
	}
	if err := enableBuiltin(f.dir, f.g); err != nil {
		t.Fatal(err)
	}
	if n := DesktopNode(); n != rex.TailnetDevice(node) {
		t.Fatalf("DesktopNode = %v; want the gateway's node", n)
	}
	f.g.tailnet.disable()
	if n := DesktopNode(); n != nil {
		t.Fatalf("node %v after built-in Tailscale was turned off", n)
	}
}

// TestEnableBuiltinTailscale turns built-in Tailscale on for the tailnet
// mode, and keeps allow_tailnet_members as it was: the mode never widens
// port 443.
func TestEnableBuiltinTailscale(t *testing.T) {
	f := startTailnetFixture(t)
	if err := enableBuiltin(f.dir, f.g); err != nil {
		t.Fatal(err)
	}
	if f.g.tailnet.running() == nil {
		t.Fatal("built-in Tailscale stays off")
	}
	conf, err := LoadTailscale(f.dir)
	if err != nil || !conf.Enabled || conf.AllowTailnetMembers {
		t.Fatalf("tailscale.json = %+v, %v; want enabled, members not allowed", conf, err)
	}
	if err := enableBuiltin(f.dir, f.g); err != nil {
		t.Fatalf("enabling again: %v", err)
	}

	// With no gateway in the process, the state turns it on, and the
	// server's gateway follows.
	dir := t.TempDir()
	if err := writeJSON(tailscaleConfPath(dir), TailscaleConfig{AllowTailnetMembers: true, Hostname: "box-gorex"}); err != nil {
		t.Fatal(err)
	}
	if Wanted(dir) {
		t.Fatal("wanted before")
	}
	if err := enableBuiltin(dir, nil); err != nil {
		t.Fatal(err)
	}
	conf, err = LoadTailscale(dir)
	if err != nil || !conf.Enabled || !conf.AllowTailnetMembers || conf.Hostname != "box-gorex" {
		t.Fatalf("tailscale.json = %+v, %v; want enabled with its settings kept", conf, err)
	}
	if !Wanted(dir) {
		t.Fatal("the server would run no gateway")
	}
}

// TestTailscaleAuthURL reads the sign-in link over the gateway's local
// socket, and asks for a sign-in.
func TestTailscaleAuthURL(t *testing.T) {
	f := startTailnetFixture(t)
	t.Setenv("GOREX_REXAPI_SOCKET", f.sock)
	if u, err := TailscaleAuthURL(); err != nil || u != "" {
		t.Fatalf("auth URL while off = %q, %v", u, err)
	}
	if _, err := StartTailscaleLogin(); err == nil {
		t.Fatal("a sign-in while built-in Tailscale is off")
	}
	if err := enableBuiltin(f.dir, f.g); err != nil {
		t.Fatal(err)
	}
	f.node.set("NeedsLogin", "https://login.example.com/a/1")
	if u, err := TailscaleAuthURL(); err != nil || u != "https://login.example.com/a/1" {
		t.Fatalf("auth URL = %q, %v", u, err)
	}
	if u, err := StartTailscaleLogin(); err != nil || u != "https://login.example.com/a/1" {
		t.Fatalf("login = %q, %v", u, err)
	}
	f.node.mu.Lock()
	logins := f.node.logins
	f.node.mu.Unlock()
	if logins != 1 {
		t.Fatalf("%d sign-ins asked; want 1", logins)
	}
}
