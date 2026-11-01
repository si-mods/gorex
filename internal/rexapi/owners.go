package rexapi

import (
	"sort"
	"time"

	"gorex/internal/rex"
)

// ownerChange is a block whose owner changed, and why: the fields of a
// block_owner_changed event.
type ownerChange struct {
	block    string
	owner    string
	previous string
	reason   string
}

// claim is a client's place in line for a block: "take" ahead of any
// "request".
type claim struct {
	client string
	mode   string
}

// desiredSize is a size a client asked for, and when it asked.
type desiredSize struct {
	cols, rows int
	at         time.Time
}

// blockOwner is the ownership of one block: its owner, the clients in
// line for it, and the size each client last asked for.
type blockOwner struct {
	owner   string
	claims  []claim
	desired map[string]desiredSize
}

// blockClaim is a block as own_block and disown_block report it.
type blockClaim struct {
	BlockID string  `json:"block_id"`
	Owner   *string `json:"owner,omitempty"`
}

// claimsResult is own_block's and disown_block's result.
type claimsResult struct {
	Blocks []blockClaim `json:"blocks"`
}

// ownerReport is a block's owner in a result.
func ownerReport(id, owner string) blockClaim {
	c := blockClaim{BlockID: id}
	if owner != "" {
		c.Owner = &owner
	}
	return c
}

// requireLocks fails when the gateway cannot hold size locks, which the
// ownership of blocks is made of.
func (g *Gateway) requireLocks() *apiError {
	if g.canLock {
		return nil
	}
	return errNotSupported("the GoRex server does not keep this gateway's connection alive, and the size locks need it")
}

// ownerOf returns the client that owns a block, "" for none. It is read
// for the model, so that a view shows owners.
func (g *Gateway) ownerOf(block string) string {
	g.ownMu.Lock()
	defer g.ownMu.Unlock()
	if o := g.owners[block]; o != nil {
		return o.owner
	}
	return ""
}

// ownersNow returns the owner of every block, for a model build.
func (g *Gateway) ownersNow() map[string]string {
	g.ownMu.Lock()
	defer g.ownMu.Unlock()
	out := make(map[string]string, len(g.owners))
	for id, o := range g.owners {
		if o.owner != "" {
			out[id] = o.owner
		}
	}
	return out
}

// ownerState returns the ownership of a block, made when it is new. Its
// ownMu is held.
func (g *Gateway) ownerState(block string) *blockOwner {
	o := g.owners[block]
	if o == nil {
		o = &blockOwner{desired: map[string]desiredSize{}}
		g.owners[block] = o
	}
	return o
}

// tookOwnership records that a block now belongs to owner, and takes the
// session's size for it, or gives the size back when owner is empty. Its
// ownMu is held.
func (g *Gateway) tookOwnership(block, sid, owner string, size *desiredSize) {
	o := g.ownerState(block)
	o.owner = owner
	c := g.opt.Client
	if owner == "" {
		if sid != "" {
			_ = c.UnlockSize(sid, "", 0, 0)
			delete(g.locks, sid)
		}
		return
	}
	if sid == "" {
		// The block's session is gone, so there is no size to lock.
		o.owner = ""
		return
	}
	cols, rows := 0, 0
	if size != nil {
		cols, rows = size.cols, size.rows
	}
	if err := c.LockSize(sid, owner, cols, rows); err != nil {
		// The session is gone or ended: the block is no one's.
		o.owner = ""
		delete(g.locks, sid)
		return
	}
	g.locks[sid] = owner
}

// claimBlocks claims blocks for a client, in the order given, with the
// mode its claim has and the reason its events give, and returns each
// block's owner after the call and the changes of owner.
func (g *Gateway) claimBlocks(rec *clientRecord, s *mSession, ids []string, mode, reason string) ([]blockClaim, []ownerChange) {
	g.ownMu.Lock()
	var changes []ownerChange
	out := make([]blockClaim, 0, len(ids))
	for _, id := range ids {
		b := s.findBlock(id)
		if b == nil {
			continue
		}
		o := g.ownerState(id)
		prev := o.owner
		o.claims = removeClaim(o.claims, rec.id)
		o.claims = append(o.claims, claim{client: rec.id, mode: mode})
		var became string
		switch {
		case mode == "take" && prev != rec.id:
			became = "take"
		case o.owner == "":
			became = "request"
		}
		if became != "" {
			var size *desiredSize
			if d, ok := o.desired[rec.id]; ok {
				size = &d
			}
			g.tookOwnership(id, b.sid, rec.id, size)
			if o.owner == rec.id {
				changes = append(changes, ownerChange{block: id, owner: rec.id, previous: prev, reason: reason})
			}
		}
		out = append(out, ownerReport(id, o.owner))
	}
	g.ownMu.Unlock()
	return out, changes
}

// giveUp gives up a client's claims on blocks, in the order given.
func (g *Gateway) giveUp(rec *clientRecord, s *mSession, ids []string) ([]blockClaim, []ownerChange) {
	g.ownMu.Lock()
	var changes []ownerChange
	out := make([]blockClaim, 0, len(ids))
	for _, id := range ids {
		b := s.findBlock(id)
		if b == nil {
			continue
		}
		o := g.ownerState(id)
		prev := o.owner
		o.claims = removeClaim(o.claims, rec.id)
		if prev == rec.id {
			next := g.electLocked(o)
			var size *desiredSize
			if d, ok := o.desired[next]; ok && next != "" {
				size = &d
			}
			g.tookOwnership(id, b.sid, next, size)
			changes = append(changes, ownerChange{block: id, owner: o.owner, previous: prev, reason: "disown"})
		}
		out = append(out, ownerReport(id, o.owner))
	}
	g.ownMu.Unlock()
	return out, changes
}

// releaseBlocks lets go of every block a client holds, as its connection
// drops ("lost") or it detaches ("disconnect"), and returns the changes.
// The blocks it owned are remembered for a reconnect (restoreBlocks).
func (g *Gateway) releaseBlocks(rec *clientRecord, reason string) []ownerChange {
	g.ownMu.Lock()
	var changes []ownerChange
	for id, o := range g.owners {
		if o.owner != rec.id && !hasClaim(o.claims, rec.id) {
			continue
		}
		prev := o.owner
		o.claims = removeClaim(o.claims, rec.id)
		if prev != rec.id {
			continue
		}
		rec.rememberOwned(id)
		next := g.electLocked(o)
		var size *desiredSize
		if d, ok := o.desired[next]; ok && next != "" {
			size = &d
		}
		g.tookOwnership(id, g.sidOfBlock(id), next, size)
		changes = append(changes, ownerChange{block: id, owner: o.owner, previous: prev, reason: reason})
	}
	g.ownMu.Unlock()
	sort.Slice(changes, func(i, j int) bool { return changes[i].block < changes[j].block })
	return changes
}

// releaseIn lets go of a client's blocks of one session, as it detaches
// from it, and returns the changes of owner.
func (g *Gateway) releaseIn(rec *clientRecord, s *mSession, reason string) []ownerChange {
	g.ownMu.Lock()
	var changes []ownerChange
	for _, b := range s.blocks {
		o := g.owners[b.id]
		if o == nil {
			continue
		}
		prev := o.owner
		o.claims = removeClaim(o.claims, rec.id)
		if prev != rec.id {
			continue
		}
		next := g.electLocked(o)
		var size *desiredSize
		if d, ok := o.desired[next]; ok && next != "" {
			size = &d
		}
		g.tookOwnership(b.id, b.sid, next, size)
		changes = append(changes, ownerChange{block: b.id, owner: o.owner, previous: prev, reason: reason})
	}
	g.ownMu.Unlock()
	sort.Slice(changes, func(i, j int) bool { return changes[i].block < changes[j].block })
	return changes
}

// restoreBlocks gives a client back the blocks it owned before its
// connection dropped, if no one took them since.
func (g *Gateway) restoreBlocks(rec *clientRecord) []ownerChange {
	g.ownMu.Lock()
	var changes []ownerChange
	for _, id := range rec.ownedList() {
		o := g.ownerState(id)
		if o.owner != "" {
			continue
		}
		var size *desiredSize
		if d, ok := o.desired[rec.id]; ok {
			size = &d
		}
		o.claims = removeClaim(o.claims, rec.id)
		o.claims = append(o.claims, claim{client: rec.id, mode: "take"})
		g.tookOwnership(id, g.sidOfBlock(id), rec.id, size)
		if o.owner == rec.id {
			changes = append(changes, ownerChange{block: id, owner: rec.id, reason: "restore"})
		}
	}
	rec.clearOwned()
	g.ownMu.Unlock()
	sort.Slice(changes, func(i, j int) bool { return changes[i].block < changes[j].block })
	return changes
}

// electLocked picks the next owner from the clients in line: the newest
// take, else the newest request, among the connected. Its ownMu is held.
func (g *Gateway) electLocked(o *blockOwner) string {
	var take, request string
	for _, c := range o.claims {
		if !g.clientConnected(c.client) {
			continue
		}
		if c.mode == "take" {
			take = c.client
		} else {
			request = c.client
		}
	}
	if take != "" {
		return take
	}
	return request
}

// clientConnected tells whether a client still has a connection.
func (g *Gateway) clientConnected(id string) bool {
	g.mu.Lock()
	rec := g.clients[id]
	g.mu.Unlock()
	if rec == nil {
		return false
	}
	lost, _ := rec.state()
	return !lost
}

// sidOfBlock returns the GoRex session of a block, from the last model.
func (g *Gateway) sidOfBlock(block string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.model != nil {
		if b := g.model.blockByID(block); b != nil {
			return b.sid
		}
	}
	return ""
}

// removeClaim returns claims without client.
func removeClaim(claims []claim, client string) []claim {
	out := claims[:0]
	for _, c := range claims {
		if c.client != client {
			out = append(out, c)
		}
	}
	return out
}

// hasClaim tells whether client is in line for a block.
func hasClaim(claims []claim, client string) bool {
	for _, c := range claims {
		if c.client == client {
			return true
		}
	}
	return false
}

// resizeBlock applies a client's size to a block: at once when it owns
// the block or nobody does, and otherwise when it becomes the owner.
func (g *Gateway) resizeBlock(rec *clientRecord, b *mBlock, cols, rows int) {
	g.ownMu.Lock()
	o := g.ownerState(b.id)
	o.desired[rec.id] = desiredSize{cols: cols, rows: rows, at: time.Now()}
	apply, owner := o.owner == "" || o.owner == rec.id, o.owner
	g.ownMu.Unlock()
	c := g.opt.Client
	switch {
	case apply && owner == rec.id:
		_ = c.LockedResize(b.sid, rec.id, cols, rows)
	case apply:
		_ = c.Resize(b.sid, cols, rows)
	}
}

// sizeResult is com.superlogical.terminal.size's result.
type sizeResult struct {
	Columns      int             `json:"columns"`
	Rows         int             `json:"rows"`
	CellWidthPX  int             `json:"cell_width_px"`
	CellHeightPX int             `json:"cell_height_px"`
	Owner        *string         `json:"owner"`
	Desired      []desiredResult `json:"desired"`
}

// desiredResult is one client's last requested size.
type desiredResult struct {
	ClientID  *string `json:"client_id"`
	Columns   int     `json:"columns"`
	Rows      int     `json:"rows"`
	UpdatedAt string  `json:"updated_at"`
}

// blockSize reports a block's size, its owner, and each client's last
// requested size, newest first.
func (g *Gateway) blockSize(block string, info *rex.SessionInfo) sizeResult {
	res := sizeResult{Desired: []desiredResult{}}
	if info != nil {
		res.Columns, res.Rows = info.Cols, info.Rows
	}
	g.ownMu.Lock()
	o := g.owners[block]
	if o != nil {
		if o.owner != "" {
			owner := o.owner
			res.Owner = &owner
		}
		for id, d := range o.desired {
			client := id
			res.Desired = append(res.Desired, desiredResult{
				ClientID: &client, Columns: d.cols, Rows: d.rows,
				UpdatedAt: connectedAtString(d.at),
			})
		}
	}
	g.ownMu.Unlock()
	sort.Slice(res.Desired, func(i, j int) bool {
		if res.Desired[i].UpdatedAt != res.Desired[j].UpdatedAt {
			return res.Desired[i].UpdatedAt > res.Desired[j].UpdatedAt
		}
		return *res.Desired[i].ClientID < *res.Desired[j].ClientID
	})
	return res
}

// renewOwned renews the leases of the size locks the gateway holds, as a
// window that keeps a size does.
func (g *Gateway) renewOwned() {
	g.ownMu.Lock()
	defer g.ownMu.Unlock()
	for sid, owner := range g.locks {
		if err := g.opt.Client.RenewSize(sid, owner); err == nil {
			continue
		}
		// The lease went while nothing renewed it: take it again.
		if err := g.opt.Client.LockSize(sid, owner, 0, 0); err != nil {
			delete(g.locks, sid)
			for _, o := range g.owners {
				if o.owner == owner {
					o.owner = ""
				}
			}
		}
	}
}

// pruneOwners forgets the blocks a model no longer has, and the locks of
// sessions that closed.
func (g *Gateway) pruneOwners(m *model) {
	live := map[string]string{} // GoRex session of each live block
	for id, b := range m.blocks {
		live[id] = b.sid
	}
	g.ownMu.Lock()
	for id := range g.owners {
		if _, ok := live[id]; !ok {
			delete(g.owners, id)
		}
	}
	for sid := range g.locks {
		held := false
		for _, s := range live {
			if s == sid {
				held = true
				break
			}
		}
		if !held {
			delete(g.locks, sid)
		}
	}
	g.ownMu.Unlock()
}
