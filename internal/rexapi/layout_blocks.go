package rexapi

import (
	"encoding/json"
	"slices"
	"sort"

	"gorex/internal/rex"
)

// methodSessionSetLayerState minimizes a floating layer or returns it to
// normal, optionally raising and focusing it.
func methodSessionSetLayerState(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("session_id", "layer_id", "state", "focus"); aerr != nil {
		return nil, aerr
	}
	layerID, ok, aerr := a.str("layer_id")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("layer_id is required")
	}
	state, ok, aerr := a.str("state")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("state is required")
	}
	if state != "normal" && state != "minimized" {
		return nil, errBadRequest("state is normal or minimized")
	}
	focus, _, aerr := a.boolean("focus")
	if aerr != nil {
		return nil, aerr
	}
	if focus && state == "minimized" {
		return nil, errBadRequest("focus needs state normal")
	}
	w, f := s.findLayer(layerID)
	if w == nil {
		return nil, errNotFound("no layer %s in %s", layerID, s.id)
	}
	if f == nil {
		// The tiled layer: it cannot be minimized, but focus still
		// works, and setting it normal changes nothing.
		if state == "minimized" {
			return nil, errBadRequest("the tiled layer cannot be minimized")
		}
		if !focus {
			return revisionResult{Revision: s.revision}, nil
		}
		b := (*mBlock)(nil)
		if w.focused != "" {
			if f := s.findBlock(w.focused); slices.Contains(w.blocks, f) {
				b = f
			}
		}
		if b == nil && len(w.blocks) > 0 {
			b = w.blocks[0]
		}
		if b == nil {
			return nil, errBadRequest("%s has no block to focus", layerID)
		}
		if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: b.sid}); aerr != nil {
			return nil, aerr
		}
		m, aerr := c.g.afterCommand(func(m *model) bool {
			now := m.byID[s.id]
			if now == nil {
				return true
			}
			nowW := now.window(w.id)
			return nowW != nil && now.active == w.id && nowW.focused == b.id
		})
		if aerr != nil {
			return nil, aerr
		}
		return revisionResult{Revision: revisionOf(m, s.id)}, nil
	}
	minimize := state == "minimized"
	if f.minimized == minimize && !focus {
		// The layer already has the state asked for.
		return revisionResult{Revision: s.revision}, nil
	}
	if minimize && w.visibleBlocks() <= 1 {
		return nil, errBadRequest("a window keeps a visible block: %s is its last", layerID)
	}
	raw, _ := json.Marshal(map[string]any{
		"sid": f.block.sid, "state": state, "focus": focus,
	})
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "action", Space: s.space, Action: "float.set_state", Args: raw}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		nowW, nowF := now.findLayer(layerID)
		if nowW == nil || nowF == nil {
			return true
		}
		if nowF.minimized != minimize {
			return false
		}
		if !focus {
			return true
		}
		return now.active == nowW.id && nowW.focused == nowF.block.id
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// blockViewResult is session.list_blocks' and session.describe_block's
// block.
type blockViewResult struct {
	BlockID     string  `json:"block_id"`
	Label       string  `json:"label"`
	CreatorName string  `json:"creator_name,omitempty"`
	Flavor      string  `json:"flavor,omitempty"`
	HostID      string  `json:"host_id,omitempty"`
	Placement   string  `json:"placement"`
	WindowID    *string `json:"window_id,omitempty"`
	Owner       *string `json:"owner,omitempty"`
}

// blockViewOf returns a block as the API reports it.
func (m *model) blockViewOf(b *mBlock) blockViewResult {
	out := blockViewResult{
		BlockID: b.id, Label: b.label, CreatorName: creatorName,
		Flavor: flavorShell, HostID: m.host, Placement: "placed",
	}
	if w := b.window; w != "" {
		window := w
		out.WindowID = &window
	}
	if b.owner != "" {
		owner := b.owner
		out.Owner = &owner
	}
	return out
}

// methodSessionListBlocks lists every block of a session.
func methodSessionListBlocks(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	now := m.byID[s.id]
	if now == nil {
		return nil, errSessionNotFound("no session %s", s.id)
	}
	blocks := make([]blockViewResult, 0, len(now.blocks))
	seen := map[string]bool{}
	for _, b := range now.blocks {
		if seen[b.id] {
			continue
		}
		seen[b.id] = true
		blocks = append(blocks, m.blockViewOf(b))
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].BlockID < blocks[j].BlockID })
	return map[string]any{"blocks": blocks}, nil
}

// clientViewResult is a client of session.list_clients.
type clientViewResult struct {
	ClientID        string         `json:"client_id"`
	Principal       map[string]any `json:"principal"`
	Info            map[string]any `json:"info,omitempty"`
	ConnectedAt     string         `json:"connected_at"`
	ConnectionState string         `json:"connection_state,omitempty"`
	LostAt          *string        `json:"connection_lost_at,omitempty"`
}

// methodSessionListClients lists the clients attached to a session.
func methodSessionListClients(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	c.g.mu.Lock()
	recs := make([]*clientRecord, 0, len(c.g.clients))
	for _, r := range c.g.clients {
		recs = append(recs, r)
	}
	c.g.mu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].connectedAt.Before(recs[j].connectedAt) })
	out := []clientViewResult{}
	for _, r := range recs {
		if !r.attachedTo(s.id) {
			continue
		}
		v := clientViewResult{
			ClientID: r.id, Principal: r.principal,
			Info: r.clientInfo().json(), ConnectedAt: connectedAtString(r.connectedAt),
		}
		if lost, at := r.state(); lost {
			v.ConnectionState = "lost"
			s := connectedAtString(at)
			v.LostAt = &s
		}
		out = append(out, v)
	}
	return map[string]any{"clients": out}, nil
}

// methodSessionHelp describes the methods a session accepts.
func methodSessionHelp(c *control, a args) (any, *apiError) {
	if _, aerr := c.attachedArg(a); aerr != nil {
		return nil, aerr
	}
	return helpDocument(c.rev, true), nil
}

// methodSessionDescribeBlock describes a block and its methods.
func methodSessionDescribeBlock(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, present, aerr := a.str("block_id")
	if aerr != nil {
		return nil, aerr
	}
	if !present || blockID == "" {
		return nil, errBadRequest("block_id is required")
	}
	m, aerr := c.g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	now := m.byID[s.id]
	if now == nil {
		return nil, errSessionNotFound("no session %s", s.id)
	}
	b := now.findBlock(blockID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	out := map[string]any{"block": m.blockViewOf(b)}
	if api := blockCreatorAPI(); api != nil {
		out["api"] = api
	}
	return out, nil
}

// methodBlockClose closes a block and ends its program.
func methodBlockClose(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, hasBlock, aerr := a.str("block_id")
	if aerr != nil {
		return nil, aerr
	}
	windowID, hasWindow, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	keepLast, _, aerr := a.boolean("keep_last")
	if aerr != nil {
		return nil, aerr
	}
	if hasBlock == hasWindow {
		return nil, errBadRequest("send block_id or window_id, not both")
	}
	if hasBlock && blockID == "" || hasWindow && windowID == "" {
		return nil, errBadRequest("the ID is empty")
	}
	var b *mBlock
	if blockID != "" {
		b = s.findBlock(blockID)
		if b == nil {
			if tb := c.g.tunnel(blockID); tb != nil && tb.session == s.id {
				c.g.removeTunnel(blockID)
				return map[string]any{"block_id": blockID, "revision": s.revision}, nil
			}
			return nil, errNotFound("no block %s in %s", blockID, s.id)
		}
	} else {
		w := s.window(windowID)
		if w == nil {
			return nil, errNotFound("no window %s in %s", windowID, s.id)
		}
		b = w.focusedBlock()
		if b == nil {
			return nil, errNotFound("window %s has no focused block", windowID)
		}
	}
	if keepLast {
		if w := s.window(b.window); w != nil && len(w.blocks) <= 1 {
			return nil, errBadRequest("the block is the only one in its window")
		}
	}
	closed, hadFocus := c.lastFocus()
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.close", Space: s.space, SID: b.sid}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.findBlock(b.id) == nil
	})
	if aerr != nil {
		return nil, aerr
	}
	if hadFocus && closed.session == s.id && closed.block == b.id {
		m = c.focusBack(m, closed)
	}
	return map[string]any{"block_id": b.id, "revision": revisionOf(m, s.id)}, nil
}

// methodSessionOwnBlock claims blocks for the calling client.
func methodSessionOwnBlock(c *control, a args) (any, *apiError) {
	if aerr := c.g.requireLocks(); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.attachedSessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	mode, _, aerr := a.str("mode")
	if aerr != nil {
		return nil, aerr
	}
	if mode == "" {
		mode = "take"
	}
	if mode != "take" && mode != "request" {
		return nil, errBadRequest("mode is take or request")
	}
	ids, aerr := c.claimIDs(a, s)
	if aerr != nil {
		return nil, aerr
	}
	blocks, changes := c.g.claimBlocks(c.rec, s, ids, mode, mode)
	c.g.emitOwnerChanges(changes)
	c.g.rebuild()
	return claimsResult{Blocks: blocks}, nil
}

// methodSessionDisownBlock gives up the calling client's claims.
func methodSessionDisownBlock(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedSessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	ids, aerr := c.claimIDs(a, s)
	if aerr != nil {
		return nil, aerr
	}
	blocks, changes := c.g.giveUp(c.rec, s, ids)
	c.g.emitOwnerChanges(changes)
	c.g.rebuild()
	return claimsResult{Blocks: blocks}, nil
}

// attachedSessionArg is attachedArg for own_block and disown_block: a
// connection that only called session.attach_all is turned away.
func (c *control) attachedSessionArg(a args) (*mSession, *apiError) {
	s, aerr := c.sessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	if !c.rec.attachedTo(s.id) {
		return nil, errBadRequest("session.attach or session.bootstrap is required")
	}
	return s, nil
}

// claimIDs returns the blocks own_block or disown_block names, or every
// block of the session, sorted, when it names none. A block that isn't in
// the session is bad_request.
func (c *control) claimIDs(a args, s *mSession) ([]string, *apiError) {
	ids, present, aerr := a.strs("block_ids")
	if aerr != nil {
		return nil, aerr
	}
	if !present || len(ids) == 0 {
		out := make([]string, 0, len(s.blocks))
		for _, b := range s.blocks {
			out = append(out, b.id)
		}
		sort.Strings(out)
		return out, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, errBadRequest("a block ID is empty")
		}
		if s.findBlock(id) == nil {
			return nil, errBadRequest("%s is not a block of %s", id, s.id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}
