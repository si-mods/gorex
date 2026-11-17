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

// TestResumeReadsALateTurnOn turns built-in Tailscale on in a gateway that
// read tailscale.json before the turn-on was written to it, as one that
// starts while the server turns the mode on.
func TestResumeReadsALateTurnOn(t *testing.T) {
	f := startTailnetFixture(t)
	if f.g.tailnet.conf.Enabled {
		t.Fatal("on before the turn-on")
	}
	if err := enableBuiltin(f.dir, nil); err != nil {
		t.Fatal(err)
	}
	f.g.tailnet.resume()
	if f.g.tailnet.running() == nil {
		t.Fatal("the turn-on written while the gateway started was lost")
	}
}

// TestResumeServesOnlyWhatTheUserTurnedOn starts the gateway's built-in
// Tailscale as tailscale.json has it: one never turned on, as after an
// upgrade with the tailnet mode on from before, makes no device and no
// listener on port 443, and runs no gateway; one the user turned on
// comes back with its listener.
func TestResumeServesOnlyWhatTheUserTurnedOn(t *testing.T) {
	t.Run("never turned on", func(t *testing.T) {
		f := startTailnetFixture(t)
		made := 0
		newNode := f.g.tailnet.newNode
		f.g.tailnet.newNode = func(dir, hostname, authKey, controlURL string, tags []string) tailnetNode {
			made++
			return newNode(dir, hostname, authKey, controlURL, tags)
		}
		close(f.node.ready)
		f.g.tailnet.resume()
		if made != 0 || f.g.tailnet.running() != nil {
			t.Fatalf("resume made %d devices, running %v; want none", made, f.g.tailnet.running())
		}
		if f.node.addr() != "" {
			t.Fatal("port 443 listens with built-in Tailscale never turned on")
		}
		if Wanted(f.dir) {
			t.Fatal("the server would run a gateway for built-in Tailscale never turned on")
		}
	})
	t.Run("turned on", func(t *testing.T) {
		f := startTailnetFixture(t)
		if err := writeJSON(tailscaleConfPath(f.dir), TailscaleConfig{Enabled: true, Hostname: "box-gorex"}); err != nil {
			t.Fatal(err)
		}
		close(f.node.ready)
		f.g.tailnet.resume()
		if f.g.tailnet.running() == nil {
			t.Fatal("built-in Tailscale the user turned on stays off")
		}
		waitFor(t, "listener", func() bool { return f.node.addr() != "" })
	})
}
