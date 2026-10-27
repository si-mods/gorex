//go:build darwin || linux || windows

package rex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gorex/internal/coordproto"

	"tailscale.com/types/key"
)

// fakeCoord is a coordinator in memory, for the network loop to follow.
type fakeCoord struct {
	mu       sync.Mutex
	members  []coordproto.Member
	revision int
	joins    []coordproto.Member // what this machine joined with
	left     []string
	kicked   []string
	err      error // what Join, Members and Leave answer
}

func (f *fakeCoord) Join(_ context.Context, name, nodeKey, address string) ([]coordproto.Member, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, 0, f.err
	}
	m := coordproto.Member{Name: name, NodeKey: nodeKey, Address: address, Joined: time.Now(), LastSeen: time.Now()}
	if i := slices.IndexFunc(f.members, func(x coordproto.Member) bool { return x.NodeKey == nodeKey }); i >= 0 {
		f.members[i].Name, f.members[i].Address = name, address
	} else {
		f.members = append(f.members, m)
	}
	f.joins = append(f.joins, m)
	f.revision++
	return slices.Clone(f.members), f.revision, nil
}

func (f *fakeCoord) Members(_ context.Context, nodeKey string, since int) ([]coordproto.Member, int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, 0, false, f.err
	}
	for i := range f.members { // a member's poll marks it seen
		if f.members[i].NodeKey == nodeKey {
			f.members[i].LastSeen = time.Now()
		}
	}
	if since == f.revision {
		return nil, since, false, nil
	}
	return slices.Clone(f.members), f.revision, true, nil
}

func (f *fakeCoord) Leave(_ context.Context, nodeKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.left = append(f.left, nodeKey)
	f.members = slices.DeleteFunc(f.members, func(m coordproto.Member) bool { return m.NodeKey == nodeKey })
	f.revision++
	return nil
}

func (f *fakeCoord) Kick(_ context.Context, nodeKey, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kicked = append(f.kicked, nodeKey+name)
	f.members = slices.DeleteFunc(f.members, func(m coordproto.Member) bool {
		return nodeKey != "" && m.NodeKey == nodeKey || nodeKey == "" && m.Name == name
	})
	f.revision++
	return nil
}

// netTest serves a server with a fake coordinator; answer gives the
// tests the coordinator to steer. The network asks it quickly.
func netTest(t *testing.T) (*Server, *fakeCoord) {
	t.Helper()
	s, _ := testServer(t)
	fake := &fakeCoord{}
	oldClient, oldInterval, oldWait := newNetClient, networkInterval, networkAddrWait
	newNetClient = func(string, string) (netClient, error) { return fake, nil }
	networkInterval, networkAddrWait = 20*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { newNetClient, networkInterval, networkAddrWait = oldClient, oldInterval, oldWait })
	return s, fake
}

// waitNetwork polls the status until the network says test, or fails.
func waitNetwork(t *testing.T, c *Client, test func(*NetworkStatus) bool) *NetworkStatus {
	t.Helper()
	for range 300 {
		st, err := c.RemoteStatus()
		if err != nil {
			t.Fatal(err)
		}
		if st.Network != nil && test(st.Network) {
			return st.Network
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := c.RemoteStatus()
	t.Fatalf("the network never said so: %+v", st.Network)
	return nil
}

func TestNetworkJoin(t *testing.T) {
	s, fake := netTest(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, c, true)

	// Joining needs the three parts to read.
	for _, tt := range []struct{ url, key, name, want string }{
		{"no url", "key", "alice", "join URL"},
		{"https://coord.test/", "key", "alice", "join URL"},
		{"https://coord.test/0123456789abcdef", "", "alice", "key"},
		{"https://coord.test/0123456789abcdef", "key", " ", "name"},
	} {
		if err := c.JoinNetwork(tt.url, tt.key, tt.name); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("joining with %q: %v, want it refused", tt.url, err)
		}
	}
	if err := c.JoinNetwork("https://coord.test/0123456789abcdef", "the key", "alice"); err != nil {
		t.Fatal(err)
	}

	// The directory comes back without this machine: it is a member of
	// the other side's hosts, not of its own allowlist.
	fake.mu.Lock()
	fake.members = append(fake.members,
		coordproto.Member{Name: "bob", NodeKey: key.NewNode().Public().String()},
		coordproto.Member{Name: "carol", NodeKey: ClientPublicKey()})
	fake.revision++
	fake.mu.Unlock()
	st := waitNetwork(t, c, func(n *NetworkStatus) bool {
		return len(n.Members) == 1 && n.Members[0].Name == "bob" && n.Coordinator == "coord.test"
	})
	if st.Name != "alice" || st.Revision == 0 {
		t.Fatalf("the network status: %+v", st)
	}

	// The join carried this machine's client key and its address.
	fake.mu.Lock()
	joins := slices.Clone(fake.joins)
	fake.mu.Unlock()
	if len(joins) != 1 || joins[0].Name != "alice" || joins[0].NodeKey != ClientPublicKey() || joins[0].Address != "tcFAKE" {
		t.Fatalf("the coordinator was joined: %+v", joins)
	}

	// The configuration keeps the network, revision included.
	b, err := os.ReadFile(filepath.Join(Dir(), "remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Network *NetworkConfig
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Network == nil || saved.Network.Name != "alice" ||
		saved.Network.URL != "https://coord.test/0123456789abcdef" ||
		saved.Network.Key != "the key" || saved.Network.Revision != st.Revision {
		t.Fatalf("the saved network: %+v", saved.Network)
	}
}

func TestNetworkMemberServed(t *testing.T) {
	s, fake := netTest(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, c, true)
	if err := c.JoinNetwork("https://coord.test/0123456789abcdef", "the key", "alice"); err != nil {
		t.Fatal(err)
	}

	// A member of the directory is served like an allowed client: it
	// lists the sessions, and its connection drops when it is kicked.
	bob := key.NewNode().Public().String()
	fake.mu.Lock()
	fake.members = append(fake.members, coordproto.Member{Name: "bob", NodeKey: bob})
	fake.revision++
	fake.mu.Unlock()
	waitNetwork(t, c, func(n *NetworkStatus) bool {
		return len(n.Members) == 1 && n.Members[0].PublicKey == bob
	})
	rc := dialAs(t, s, bob)
	if _, err := rc.List(); err != nil {
		t.Fatalf("a member listed: %v", err)
	}
	if err := c.KickNetworkMember("", bob); err != nil {
		t.Fatal(err)
	}
	wantClosed(t, rc, "kicked")
	waitNetwork(t, c, func(n *NetworkStatus) bool { return len(n.Members) == 0 })

	// A stranger is refused as before.
	stranger := dialAs(t, s, key.NewNode().Public().String())
	wantClosed(t, stranger, "a stranger")
}

func TestNetworkFollowsAndLeaves(t *testing.T) {
	s, fake := netTest(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, c, true)
	if err := c.JoinNetwork("https://coord.test/0123456789abcdef", "the key", "alice"); err != nil {
		t.Fatal(err)
	}

	// The directory changing is followed without joining again.
	bob, carol := key.NewNode().Public().String(), key.NewNode().Public().String()
	push := func(ms ...coordproto.Member) {
		fake.mu.Lock()
		fake.members = append(slices.DeleteFunc(fake.members, func(m coordproto.Member) bool {
			return m.NodeKey != ClientPublicKey()
		}), ms...)
		fake.revision++
		fake.mu.Unlock()
	}
	push(coordproto.Member{Name: "bob", NodeKey: bob})
	waitNetwork(t, c, func(n *NetworkStatus) bool { return len(n.Members) == 1 })
	push(coordproto.Member{Name: "bob", NodeKey: bob}, coordproto.Member{Name: "carol", NodeKey: carol})
	waitNetwork(t, c, func(n *NetworkStatus) bool { return len(n.Members) == 2 })
	fake.mu.Lock()
	joins := len(fake.joins)
	fake.mu.Unlock()
	if joins != 1 {
		t.Fatalf("joined %d times for one address", joins)
	}

	// The coordinator going wrong says so, and does not drop the members.
	fake.mu.Lock()
	fake.err = errFakeCoord
	fake.mu.Unlock()
	waitNetwork(t, c, func(n *NetworkStatus) bool { return n.LastError != "" })
	if st, _ := c.RemoteStatus(); len(st.Network.Members) != 2 {
		t.Fatalf("the members went with the error: %+v", st.Network)
	}
	fake.mu.Lock()
	fake.err = nil
	fake.mu.Unlock()
	waitNetwork(t, c, func(n *NetworkStatus) bool { return n.LastError == "" })

	// Leaving tells the coordinator and forgets the members.
	if err := c.LeaveNetwork(); err != nil {
		t.Fatal(err)
	}
	for range 300 {
		if st, _ := c.RemoteStatus(); st.Network == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st, _ := c.RemoteStatus(); st.Network != nil {
		t.Fatalf("the network stayed: %+v", st.Network)
	}
	fake.mu.Lock()
	left := slices.Clone(fake.left)
	fake.mu.Unlock()
	if len(left) != 1 || left[0] != ClientPublicKey() {
		t.Fatalf("the coordinator was told of leaving: %v", left)
	}
	if err := c.LeaveNetwork(); err == nil || !strings.Contains(err.Error(), "no network") {
		t.Fatalf("leaving twice: %v", err)
	}
}

func TestNetworkJoinNeedsRemote(t *testing.T) {
	s, _ := netTest(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.JoinNetwork("https://coord.test/0123456789abcdef", "the key", "alice"); err == nil ||
		!strings.Contains(err.Error(), "turn remote access on") {
		t.Fatalf("joined with remote access off: %v", err)
	}
}

var errFakeCoord = &fakeError{"the coordinator is away"}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }
