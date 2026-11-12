package rexapi

import (
	"sort"

	"gorex/internal/rex"
)

// gorex.move_window moves a window to another session, as the desktop's
// Move to Space moves a tab to another space. It is GoRex's own, outside
// Rex's API: Rex's session.move orders sessions, and session.move_window
// and session.move_block stay within one session. The GoRex server makes
// the move (tab.to_space), so the desktop and every client see it as
// another client's change of the layout. The welcome lists
// featureMoveWindow when the server can make it.

// featureMoveWindow is the welcome's feature of a gateway that moves a
// window to another session (gorex.move_window).
const featureMoveWindow = "gorex.move_window"

// moveWindowResult is gorex.move_window's result.
type moveWindowResult struct {
	// Revision is the revision of the session the window left; left out
	// when the move destroyed it.
	Revision *uint64 `json:"revision,omitempty"`
	// ToRevision is the revision of the session the window went to.
	ToRevision uint64 `json:"to_revision"`
	// SessionDestroyed tells that the window was the session's last, and
	// the session went with it, as the desktop's space does.
	SessionDestroyed bool `json:"session_destroyed,omitempty"`
}

// methodGorexMoveWindow moves a window, with its blocks and their
// programs, its layout, label, floating layers, focus and zoom, to another
// session: before one of its windows, or at its end. That session's
// active window stays; the session it leaves makes the next window
// active, and goes once it has none. The blocks keep their IDs; an owner
// not attached to the session they went to lets them go, as one that
// detaches does.
func methodGorexMoveWindow(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityTabToSpace) {
		return nil, errNotSupported("the GoRex server does not move windows between sessions; update GoRex")
	}
	if aerr := a.only("session_id", "window_id", "to_session_id", "before_window_id"); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	windowID, present, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	if !present || windowID == "" {
		return nil, errBadRequest("window_id is required")
	}
	toID, aerr := a.reqStr("to_session_id")
	if aerr != nil {
		return nil, aerr
	}
	beforeID, _, aerr := a.str("before_window_id")
	if aerr != nil {
		return nil, aerr
	}
	if toID == s.id {
		return nil, errBadRequest("the window is in %s already; session.move_window orders a session's windows", s.id)
	}
	w := s.window(windowID)
	if w == nil {
		return nil, errNotFound("no window %s in %s", windowID, s.id)
	}
	to := c.g.modelNow().byID[toID]
	if to == nil {
		return nil, errSessionNotFound("no session %s", toID)
	}
	target := ""
	if beforeID != "" {
		before := to.window(beforeID)
		if before == nil {
			return nil, errNotFound("no window %s in %s", beforeID, toID)
		}
		// A tab of an app before tab IDs is named by a session of it.
		if target = before.tab; target == "" {
			if b := before.focusedBlock(); b != nil {
				target = b.sid
			}
		}
	}
	first := w.focusedBlock()
	if first == nil {
		return nil, errNotFound("window %s has no block", windowID)
	}
	blocks := w.allBlocks()
	if _, aerr := c.g.command(rex.LayoutCommand{
		Op: "tab.to_space", Space: s.space, Tab: w.tab, SID: first.sid, ToSpace: to.space, Target: target,
	}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[toID]
		return now == nil || now.window(windowID) != nil
	})
	if aerr != nil {
		return nil, aerr
	}
	if changes := c.g.keepAttached(blocks, toID); len(changes) > 0 {
		c.g.emitOwnerChanges(changes)
		if now, err := c.g.refresh(); err == nil {
			m = now
		}
	}
	res := moveWindowResult{ToRevision: revisionOf(m, toID)}
	if left := m.byID[s.id]; left != nil {
		rev := left.revision
		res.Revision = &rev
	} else {
		res.SessionDestroyed = true
	}
	return res, nil
}

// keepAttached keeps the claims on blocks that went to the session to
// only of the clients attached to it: an owner that is not gives its block
// to the next of them in line, else to none, as one that detaches does
// ("disconnect"). It returns the changes of owner.
func (g *Gateway) keepAttached(blocks []*mBlock, to string) []ownerChange {
	attached := func(client string) bool {
		g.mu.Lock()
		rec := g.clients[client]
		g.mu.Unlock()
		return rec != nil && rec.attachedTo(to)
	}
	g.ownMu.Lock()
	var changes []ownerChange
	for _, b := range blocks {
		o := g.owners[b.id]
		if o == nil {
			continue
		}
		kept := o.claims[:0]
		for _, cl := range o.claims {
			if attached(cl.client) {
				kept = append(kept, cl)
			}
		}
		o.claims = kept
		prev := o.owner
		if prev == "" || attached(prev) {
			continue
		}
		next := g.electLocked(o)
		var size *desiredSize
		if d, ok := o.desired[next]; ok && next != "" {
			size = &d
		}
		g.tookOwnership(b.id, b.sid, next, size)
		changes = append(changes, ownerChange{block: b.id, owner: o.owner, previous: prev, reason: "disconnect"})
	}
	g.ownMu.Unlock()
	sort.Slice(changes, func(i, j int) bool { return changes[i].block < changes[j].block })
	return changes
}

// moveWindowEntry is gorex.move_window's entry of the help document, in
// the shape of Rex's own.
var moveWindowEntry = map[string]any{
	"name":        "gorex.move_window",
	"description": "Move a window, with its blocks, to another session. GoRex's own method: use it only when the welcome's features list gorex.move_window.",
	"invoke":      map[string]any{"method": "gorex.move_window"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "The session the window is in."},
			"window_id":  map[string]any{"type": []any{"string", "null"}, "description": "The window to move."},
			"to_session_id": map[string]any{
				"type": "string", "description": "The session to move it to. It must be another session.",
			},
			"before_window_id": map[string]any{
				"type":        []any{"string", "null"},
				"description": "The window of to_session_id to place it just before. Leave it out to move the window to the end.",
			},
		},
		"required":             []any{"session_id", "window_id", "to_session_id"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"revision": map[string]any{
				"type": "integer", "minimum": 0,
				"description": "The revision of the session the window left. Left out when the move destroyed it.",
			},
			"to_revision": map[string]any{
				"type": "integer", "minimum": 0,
				"description": "The revision of the session the window went to.",
			},
			"session_destroyed": map[string]any{
				"type":        "boolean",
				"description": "True when the window was its session's last, and the session went with it. Left out otherwise.",
			},
		},
		"required":             []any{"to_revision"},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"session_id\": \"session:<uuid>\",\n  \"window_id\": \"window:01234567-89ab-cdef-0123-456789abcde1\",\n  \"to_session_id\": \"session:<other uuid>\",\n  \"before_window_id\": \"window:01234567-89ab-cdef-0123-456789abcde2\"\n}",
	"response_example": "{\"revision\": 42, \"to_revision\": 7}",
	"validation_notes": []any{
		"The window keeps its ID, its label, its layout, its floating layers, its focus and zoom. Its blocks keep their IDs and their programs go on.",
		"The active window of to_session_id doesn't change. If the window was its session's active one, the next window becomes active, else the one before it.",
		"If the window was its session's last, the session is destroyed, as the GoRex desktop's Move to Space removes the space it empties: session_destroyed is true, and the connections that hear of every session get session_destroyed.",
		"The session it leaves sends window_closed, block_closed for each block, and session_view_changed; the session it goes to sends window_created, block_created for each block, and session_view_changed.",
		"An owner of a block that isn't attached to to_session_id loses it, with block_owner_changed reason disconnect, and the next attached client in line gets it.",
		"The connection must be attached to session_id, or hear of every session's events, or the call fails with conflict.",
		"If session_id or to_session_id doesn't exist, the call fails with session_not_found. If either window isn't in its session, it fails with not_found. If to_session_id is session_id, it fails with bad_request: session.move_window orders a session's windows.",
		"A server that can't move windows between sessions, whose welcome doesn't list gorex.move_window, fails with not_supported.",
	},
}

// llmsGoRex documents GoRex's own methods, after Rex's revisions.
var llmsGoRex = []byte(`
## GoRex's own methods

These methods are GoRex's own, which a Rex server doesn't have. Call one only when the welcome's
` + "`features`" + ` lists its feature.

### gorex.move_window

Feature: ` + "`gorex.move_window`" + `. Move a window, with its blocks, to another session, as the GoRex desktop's
Move to Space moves a tab to another space.

Arguments:

- ` + "`session_id`" + ` (string, required): The session the window is in.
- ` + "`window_id`" + ` (string, required): The window to move.
- ` + "`to_session_id`" + ` (string, required): The session to move it to. It must be another session.
- ` + "`before_window_id`" + ` (string or null): The window of ` + "`to_session_id`" + ` to place it just before. Leave it out to move
  the window to the end.

Result:

- ` + "`revision`" + ` (integer): The revision of the session the window left. Left out when the move destroyed it.
- ` + "`to_revision`" + ` (integer, required): The revision of the session the window went to.
- ` + "`session_destroyed`" + ` (boolean): True when the window was its session's last, and the session went with it.

Notes:

- The window keeps its ID, its label, its layout, its floating layers, its focus and zoom. Its blocks keep
  their IDs and their programs go on.
- The active window of ` + "`to_session_id`" + ` doesn't change. If the window was its session's active one, the next
  window becomes active, else the one before it.
- If the window was its session's last, the session is destroyed, as the desktop's Move to Space removes
  the space it empties, and the connections that hear of every session get ` + "`session_destroyed`" + `.
- The session it leaves sends ` + "`window_closed`" + `, ` + "`block_closed`" + ` for each block and ` + "`session_view_changed`" + `; the
  session it goes to sends ` + "`window_created`" + `, ` + "`block_created`" + ` for each block and ` + "`session_view_changed`" + `.
- An owner of a block that isn't attached to ` + "`to_session_id`" + ` loses it (` + "`block_owner_changed`" + `, reason
  ` + "`disconnect`" + `), and the next attached client in line gets it.
- The connection must be attached to ` + "`session_id`" + `, or hear of every session's events, or the call fails
  with ` + "`conflict`" + `. An unknown session fails with ` + "`session_not_found`" + `, an unknown window with ` + "`not_found`" + `, and
  ` + "`to_session_id`" + ` equal to ` + "`session_id`" + ` with ` + "`bad_request`" + ` (` + "`session.move_window`" + ` orders a session's windows). A
  server that can't move windows between sessions fails with ` + "`not_supported`" + `.

Example arguments:

` + "```json" + `
{
  "session_id": "session:<uuid>",
  "window_id": "window:01234567-89ab-cdef-0123-456789abcde1",
  "to_session_id": "session:<other uuid>"
}
` + "```" + `

Example result:

` + "```json" + `
{"revision": 42, "to_revision": 7}
` + "```" + `
`)
