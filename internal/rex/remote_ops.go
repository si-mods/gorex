//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// remoteDo serves the remote.* ops, of local connections only.
func (s *Server) remoteDo(req Request) (any, error) {
	var done []func()
	defer func() {
		for _, f := range done {
			f()
		}
	}()
	if req.Op == "remote.firewallCheck" {
		s.firewallCheck(true)
		return nil, nil
	}
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.Code != "" && (req.Op == "remote.approve" || req.Op == "remote.reject") {
		if err := s.addrDecideLocked(req.Op == "remote.approve", req.Code); err != nil {
			return nil, err
		}
		log.Printf("rex: %s by address", req.Op)
		return nil, nil
	}
	if res, handled, err := s.remoteNonConfigOpLocked(req, &done); handled {
		return res, err
	}
	return nil, s.remoteConfigOpLocked(req, &done)
}

func remoteReqKey(req Request) (string, error) {
	if req.Remote == nil {
		return "", fmt.Errorf("rex: %s needs a client", req.Op)
	}
	k, err := parsePublicKey(req.Remote.PublicKey)
	return k.String(), err
}

func (r *remote) pendingIndexLocked(k string) int {
	if !r.pairOpenLocked() {
		return -1
	}
	return slices.IndexFunc(r.pending, func(p RemotePending) bool { return p.PublicKey == k })
}

func (s *Server) remoteNonConfigOpLocked(req Request, done *[]func()) (any, bool, error) {
	r := &s.rem
	switch req.Op {
	case "remote.status":
		if r.tailnet != nil {
			status := r.tailnet.status()
			return s.remoteStatusLocked(&status), true, nil
		}
		return s.remoteStatusLocked(&TailnetStatus{Enabled: r.cfg.Tailnet}), true, nil
	case "remote.tailnetSet":
		cfg := r.cfg
		cfg.Tailnet = req.On
		if err := writeJSON(remotePath(), cfg); err != nil {
			return nil, true, err
		}
		r.cfg = cfg
		if cfg.Tailnet {
			s.tailnetStartLocked()
		} else {
			s.tailnetStopLocked()
		}
		return nil, true, nil
	case "remote.pairStart":
		if !r.cfg.Enabled {
			return nil, true, errors.New("rex: turn remote access on to pair")
		}
		if !r.pairOpenLocked() {
			r.pairGen++
		}
		r.pairUntil = time.Now().Add(pairWindow)
		time.AfterFunc(pairWindow+time.Second, s.ownDropStrangers)
		log.Printf("rex: %s", req.Op)
		return nil, true, nil
	case "remote.pairStop":
		r.pairUntil, r.pending = time.Time{}, nil
		*done = append(*done, s.ownDropStrangers)
		log.Printf("rex: %s", req.Op)
		return nil, true, nil
	case "remote.reject":
		k, err := remoteReqKey(req)
		if err != nil {
			return nil, true, err
		}
		i := r.pendingIndexLocked(k)
		if i < 0 {
			return nil, true, errors.New("rex: no such request")
		}
		r.pending = slices.Delete(r.pending, i, i+1)
		log.Printf("rex: %s", req.Op)
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func (s *Server) remoteConfigOpLocked(req Request, done *[]func()) error {
	r := &s.rem
	cfg := r.cfg
	cfg.Allowed = slices.Clone(cfg.Allowed)
	var revoked string
	approved := -1

	switch req.Op {
	case "remote.enable":
		if cfg.NodeKey.IsZero() || cfg.PresharedKey.IsZero() {
			cfg.NodeKey, cfg.PresharedKey = key.NewNode(), tailcat.NewPresharedKey()
		}
		cfg.Enabled = true
	case "remote.disable":
		cfg.Enabled = false
	case "remote.allow":
		if err := allowRemoteClient(&cfg, req.Remote); err != nil {
			return err
		}
	case "remote.approve":
		k, err := remoteReqKey(req)
		if err != nil {
			return err
		}
		i := r.pendingIndexLocked(k)
		if i < 0 {
			return errors.New("rex: no such request")
		}
		if !r.allowedLocked(k) {
			cfg.Allowed = append(cfg.Allowed, RemoteClient{Name: r.pending[i].Name, PublicKey: k, Created: time.Now()})
		}
		approved = i
	case "remote.revoke":
		k, err := remoteReqKey(req)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(cfg.Allowed, func(c RemoteClient) bool { return c.PublicKey == k })
		if i < 0 {
			return errors.New("rex: no such client")
		}
		cfg.Allowed = slices.Delete(cfg.Allowed, i, i+1)
		revoked = k
	case "remote.regenerate":
		cfg.NodeKey, cfg.PresharedKey = key.NewNode(), tailcat.NewPresharedKey()
	case "remote.relaySet":
		relay, err := NormalizeRelay(req.Relay)
		if err != nil {
			return err
		}
		cfg.Relay = relay
	case "remote.reachableSet":
		ip, err := NormalizeReachable(req.ReachableAt)
		if err != nil {
			return err
		}
		cfg.ReachableAt = ip
	case "remote.shareLayoutSet":
		cfg.ShareLayout = req.On
	case "remote.controlSet":
		cfg.RemoteControl = req.On
	default:
		return fmt.Errorf("unknown op %q", req.Op)
	}

	if err := writeJSON(remotePath(), cfg); err != nil {
		return err
	}
	relayWas, reachableWas := r.cfg.Relay, r.cfg.ReachableAt
	r.cfg = cfg
	if approved >= 0 {
		r.pending = slices.Delete(r.pending, approved, approved+1)
	}
	s.applyRemoteEffectsLocked(req.Op, revoked, relayWas, reachableWas, done)
	log.Printf("rex: %s", req.Op)
	return nil
}

func allowRemoteClient(cfg *RemoteConfig, rc *RemoteClient) error {
	if rc == nil {
		return errors.New("rex: remote.allow needs a client")
	}
	name := strings.TrimSpace(rc.Name)
	k, err := parsePublicKey(rc.PublicKey)
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("rex: name the client")
	}
	if slices.ContainsFunc(cfg.Allowed, func(c RemoteClient) bool { return c.PublicKey == k.String() }) {
		return errors.New("rex: that client is allowed already")
	}
	cfg.Allowed = append(cfg.Allowed, RemoteClient{Name: name, PublicKey: k.String(), Created: time.Now()})
	return nil
}

func (s *Server) applyRemoteEffectsLocked(op, revoked, relayWas, reachableWas string, done *[]func()) {
	r := &s.rem
	switch {
	case !r.cfg.Enabled:
		*done = append(*done, s.remoteStopLocked())
		r.lastErr = ""
	case revoked != "":
		// Drop its connections; accepting checks the allowlist again.
		for c, k := range r.conns {
			if k == revoked {
				delete(r.conns, c)
				*done = append(*done, func() { c.Close() })
			}
		}
		if o := r.own; o != nil {
			for c, k := range o.conns {
				if k == revoked {
					delete(o.conns, c)
					*done = append(*done, func() { c.Close() })
				}
			}
			// The relay knows no key of a connection: all connect again,
			// and are asked again.
			*done = append(*done, o.relay.dropClients)
		}
	case op == "remote.reachableSet":
		if reachableWas != r.cfg.ReachableAt {
			*done = append(*done, s.ownStopLocked())
			s.ownStartLocked()
		}
	case op == "remote.regenerate", op == "remote.relaySet" && relayWas != r.cfg.Relay,
		op == "remote.enable" && r.cancel == nil:
		// New keys, a new relay, or off until now: start again, which
		// makes the address anew and drops the connections.
		*done = append(*done, s.remoteStopLocked())
		s.remoteStartLocked()
	}
}
