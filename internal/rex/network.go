//go:build darwin || linux || windows

package rex

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"net/url"
	"slices"
	"strings"
	"time"

	"gorex/internal/coordproto"
)

// A network, joined once on a coordinator, replaces pairwise pairing:
// the coordinator's directory becomes this server's allowlist beside
// its own, and the app opens the members as hosts. See docs/coord.md.
// Whoever holds the network key may run commands on every member, so
// it is kept in remote.json (0600) and never logged; the join URL, too,
// is a secret, and neither leaves this file except to the coordinator.

// NetworkConfig is the network this machine joined, kept in
// remote.json beside the rest of the remote access.
type NetworkConfig struct {
	URL  string `json:"url"` // pins the coordinator
	Key  string `json:"key"` // the network key
	Name string `json:"name"`
	// Revision is the directory revision last seen, so a poll that
	// changed nothing costs one number.
	Revision int `json:"revision"`
}

// How often the network asks the coordinator, and how long a round
// waits for the address; tests replace them.
var (
	networkInterval = 30 * time.Second
	networkAddrWait = 2 * time.Second
)

// netClient is the coordinator as this server speaks it; tests replace
// newNetClient to serve one in memory.
type netClient interface {
	Join(ctx context.Context, name, nodeKey, address string) ([]coordproto.Member, int, error)
	Members(ctx context.Context, nodeKey string, since int) ([]coordproto.Member, int, bool, error)
	Leave(ctx context.Context, nodeKey string) error
	Kick(ctx context.Context, nodeKey, name string) error
}

var newNetClient = func(url, key string) (netClient, error) {
	return coordproto.NewClient(url, key)
}

// networkStartLocked starts following the network of the configuration,
// if there is one. It holds the remote lock.
func (s *Server) networkStartLocked() {
	r := &s.rem
	if r.cfg.Network == nil || r.netCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.netCancel = cancel
	go s.networkRun(ctx)
}

// networkStopLocked stops following the network, forgetting its
// members and its coordinator; the function it returns drops the
// members' connections, to call without the lock.
func (s *Server) networkStopLocked() func() {
	r := &s.rem
	if r.netCancel != nil {
		r.netCancel()
		r.netCancel, r.netC = nil, nil
	}
	drop := r.netMembers
	r.netMembers, r.netErr, r.netJoined = nil, "", ""
	return s.dropMembersLocked(drop)
}

// dropMembersLocked returns a function closing the connections of the
// given members' keys, to call without the lock. It holds the remote
// lock.
func (s *Server) dropMembersLocked(ms []NetworkMember) func() {
	r := &s.rem
	var done []func()
	for c, k := range r.conns {
		if slices.ContainsFunc(ms, func(m NetworkMember) bool { return m.PublicKey == k }) {
			delete(r.conns, c)
			conn := c
			done = append(done, func() { conn.Close() })
		}
	}
	return func() {
		for _, f := range done {
			f()
		}
	}
}

// netMemberLocked tells whether k is a member of the network, which
// the allowlist checks beside its own.
func (r *remote) netMemberLocked(k string) bool {
	return k != "" && slices.ContainsFunc(r.netMembers, func(m NetworkMember) bool { return m.PublicKey == k })
}

// networkRun follows the network: it joins once the address is known —
// and again whenever the address changes, as a regenerate makes a new
// one — then asks for the directory, and mirrors it into the members
// the allowlist serves and the status shows.
func (s *Server) networkRun(ctx context.Context) {
	r := &s.rem
	var c netClient
	var self, joinedAddr string
	for {
		r.mu.Lock()
		net, addr := r.cfg.Network, string(r.addr)
		r.mu.Unlock()
		if net == nil {
			return
		}
		if c == nil {
			var err error
			if c, err = newNetClient(net.URL, net.Key); err != nil {
				s.networkErr(err)
			} else {
				r.mu.Lock()
				r.netC = c
				r.mu.Unlock()
			}
		}
		if c == nil || addr == "" {
			// The coordinator or the address is not there yet; remote
			// access off means the address is empty.
			if !sleepCtx(ctx, networkAddrWait) {
				return
			}
			continue
		}
		if self == "" {
			self = ClientPublicKey()
		}
		if err := s.networkRound(ctx, c, net, self, addr, &joinedAddr); err != nil {
			s.networkErr(err)
		}
		if !sleepCtx(ctx, time.Duration(float64(networkInterval)*(0.9+rand.Float64()/5))) {
			return
		}
	}
}

// networkRound makes one ask of the coordinator: a join when the
// address is not the one joined with, else a poll.
func (s *Server) networkRound(ctx context.Context, c netClient, net *NetworkConfig, self, addr string, joinedAddr *string) error {
	var members []coordproto.Member
	var revision int
	changed := true
	var err error
	if addr != *joinedAddr {
		members, revision, err = c.Join(ctx, net.Name, self, addr)
		if err == nil {
			*joinedAddr = addr
		}
	} else {
		members, revision, changed, err = c.Members(ctx, self, net.Revision)
	}
	if err != nil {
		return err
	}
	if !changed {
		// The coordinator answered, even though nothing changed: an
		// error it said before is over.
		r := &s.rem
		r.mu.Lock()
		r.netErr = ""
		r.mu.Unlock()
		return nil
	}
	s.networkApply(members, revision, self)
	return nil
}

// networkApply mirrors the coordinator's directory into this server:
// the members beside the allowlist (its own stay its own), the revision
// it was seen at, and the connections of members gone.
func (s *Server) networkApply(ms []coordproto.Member, revision int, self string) {
	r := &s.rem
	r.mu.Lock()
	if r.cfg.Network == nil {
		r.mu.Unlock()
		return
	}
	old := r.netMembers
	r.netMembers = nil
	for _, m := range ms {
		if m.NodeKey == self {
			continue
		}
		r.netMembers = append(r.netMembers, NetworkMember{Name: m.Name, PublicKey: m.NodeKey, Address: m.Address})
	}
	r.cfg.Network.Revision = revision
	r.netErr = ""
	if err := writeJSON(remotePath(), r.cfg); err != nil {
		log.Printf("rex: saving the network revision: %v", err)
	}
	var gone []NetworkMember
	for _, m := range old {
		if !slices.ContainsFunc(r.netMembers, func(n NetworkMember) bool { return n.PublicKey == m.PublicKey }) {
			gone = append(gone, m)
		}
	}
	drop := s.dropMembersLocked(gone)
	r.mu.Unlock()
	drop()
}

// networkErr records what the coordinator last said, scrubbed.
func (s *Server) networkErr(err error) {
	msg := scrub(err.Error())
	s.rem.mu.Lock()
	s.rem.netErr = msg
	s.rem.mu.Unlock()
	log.Printf("rex: the network: %s", msg)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// networkJoin is the remote.join op: it takes the network in, and
// starts following it. The function it returns drops the members of the
// network left, to call without the lock. It holds the remote lock.
func (s *Server) networkJoin(j *NetworkJoin) (func(), error) {
	r := &s.rem
	if !r.cfg.Enabled {
		return nil, errors.New("rex: turn remote access on to join a network")
	}
	name := networkName(j.Name)
	if name == "" {
		return nil, errors.New("rex: name this machine to join a network")
	}
	url, key := strings.TrimSpace(j.URL), strings.TrimSpace(j.Key)
	if _, _, err := coordproto.SplitJoinURL(url); err != nil {
		return nil, errors.New("rex: the join URL does not read")
	}
	if key == "" {
		return nil, errors.New("rex: the network key is empty")
	}
	drop := s.networkStopLocked()
	cfg := r.cfg
	cfg.Network = &NetworkConfig{URL: url, Key: key, Name: name}
	if err := writeJSON(remotePath(), cfg); err != nil {
		return drop, err
	}
	r.cfg = cfg
	s.networkStartLocked()
	log.Print("rex: joined a network")
	return drop, nil
}

// networkLeave is the remote.leave op: it forgets the network and its
// members. The coordinator is told on the way, best effort: unreachable,
// its record of this machine goes stale until someone kicks it. The
// function it returns drops the members' connections, to call without
// the lock. It holds the remote lock.
func (s *Server) networkLeave() (func(), error) {
	r := &s.rem
	if r.cfg.Network == nil {
		return nil, errors.New("rex: no network to leave")
	}
	c, self := r.netC, ClientPublicKey()
	drop := s.networkStopLocked()
	cfg := r.cfg
	cfg.Network = nil
	if err := writeJSON(remotePath(), cfg); err != nil {
		return drop, err
	}
	r.cfg = cfg
	if c != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := c.Leave(ctx, self); err != nil {
				log.Printf("rex: telling the coordinator of leaving: %s", scrub(err.Error()))
			}
		}()
	}
	log.Print("rex: left the network")
	return drop, nil
}

// networkKick is the remote.kick op: the member goes from the whole
// network, not only from here. The coordinator takes seconds to answer,
// so this asks it off the lock and drops the member here once it did.
func (s *Server) networkKick(name, nodeKey string) error {
	r := &s.rem
	if r.cfg.Network == nil {
		return errors.New("rex: no network to kick from")
	}
	if nodeKey == "" && name == "" {
		return errors.New("rex: name the member by its key or its name")
	}
	c := r.netC
	if c == nil {
		return errors.New("rex: the coordinator is not reached yet")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Kick(ctx, nodeKey, name); err != nil {
			s.networkErr(err)
			return
		}
		r.mu.Lock()
		var drop []NetworkMember
		r.netMembers = slices.DeleteFunc(r.netMembers, func(m NetworkMember) bool {
			if nodeKey != "" && m.PublicKey == nodeKey || nodeKey == "" && m.Name == name {
				drop = append(drop, m)
				return true
			}
			return false
		})
		done := s.dropMembersLocked(drop)
		r.mu.Unlock()
		done()
	}()
	return nil
}

// networkName cleans the name this machine joins with, the same rules
// the coordinator applies.
func networkName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 0x20 && r < 0x7f {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > coordproto.MaxName {
		s = strings.TrimSpace(string(r[:coordproto.MaxName]))
	}
	return s
}

// coordinatorHost is the address of the join URL, for the status to
// say which coordinator this is without saying the URL.
func coordinatorHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Host
}
