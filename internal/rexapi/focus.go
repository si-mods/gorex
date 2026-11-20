package rexapi

import "fmt"

// focusHistoryLimit is how many places one principal's history keeps.
const focusHistoryLimit = 64

// focusEntry is one place a principal focused: a block of a window of a
// session, with the sequence number of the change that focused it.
type focusEntry struct {
	seq     uint64
	session string
	window  string
	block   string
}

// focusHistory is one principal's most-recent-first places and the
// counter that orders them.
type focusHistory struct {
	entries []focusEntry
	seq     uint64
}

// focusMethods are the calls whose success records where focus landed.
var focusMethods = map[string]bool{
	"session.focus_block":           true,
	"session.focus_window":          true,
	"session.focus_direction":       true,
	"session.focus_next_pane":       true,
	"session.focus_previous_pane":   true,
	"session.focus_next_window":     true,
	"session.focus_previous_window": true,
	"session.new_split":             true,
	"session.new_window":            true,
	"session.new_block":             true,
	"session.new_layer":             true,
	"session.close_window":          true,
	"block.close":                   true,
	"focus_history.reverse":         true,
}

// focusKey names the principal whose history a connection walks: the
// person where the transport identifies one, and the client elsewhere.
func (c *control) focusKey() string {
	if c.rec == nil {
		return "client:none"
	}
	p := c.rec.principal
	switch kind, _ := p["kind"].(string); kind {
	case "peer":
		if uid, ok := p["uid"]; ok {
			return "peer:" + fmt.Sprint(uid)
		}
	case "tailscale":
		if id, ok := p["user_id"]; ok {
			return "tailscale:" + fmt.Sprint(id)
		}
	}
	return "client:" + c.rec.id
}

// focusEntries copies the principal's places, newest first.
func (g *Gateway) focusEntries(key string) []focusEntry {
	g.focusMu.Lock()
	defer g.focusMu.Unlock()
	h := g.focus[key]
	if h == nil {
		return nil
	}
	return append([]focusEntry(nil), h.entries...)
}

// recordFocus puts a place at the front of the principal's history, after
// the same place is not already there.
func (c *control) recordFocus(e focusEntry) {
	if c.rec == nil {
		return
	}
	g := c.g
	g.focusMu.Lock()
	defer g.focusMu.Unlock()
	if g.focus == nil {
		g.focus = map[string]*focusHistory{}
	}
	key := c.focusKey()
	h := g.focus[key]
	if h == nil {
		h = &focusHistory{}
		g.focus[key] = h
	}
	if len(h.entries) > 0 {
		top := h.entries[0]
		if top.session == e.session && top.window == e.window && top.block == e.block {
			return
		}
	}
	h.seq++
	e.seq = h.seq
	h.entries = append(h.entries, focusEntry{})
	copy(h.entries[1:], h.entries)
	h.entries[0] = e
	if len(h.entries) > focusHistoryLimit {
		h.entries = h.entries[:focusHistoryLimit]
	}
}

// lastFocus returns the principal's newest place.
func (c *control) lastFocus() (focusEntry, bool) {
	entries := c.g.focusEntries(c.focusKey())
	if len(entries) == 0 {
		return focusEntry{}, false
	}
	return entries[0], true
}

// noteFocus records where the session's focus is now, after a call that
// changed it.
func (c *control) noteFocus(sid string) {
	if sid == "" || c.rec == nil {
		return
	}
	m := c.g.modelNow()
	s := m.byID[sid]
	if s == nil {
		return
	}
	b := s.focused()
	if b == nil {
		return
	}
	c.recordFocus(focusEntry{session: sid, window: b.window, block: b.id})
}

// focusFilter limits the places a reverse walk may land on.
type focusFilter struct {
	session    string
	notSession string
	window     string
	notWindow  string
}

// reverseEntry returns the newest place that the filters allow, older
// than cursor when cursor is not zero, and not the place skip names.
func (c *control) reverseEntry(m *model, cursor uint64, f focusFilter, skip focusEntry) (focusEntry, bool) {
	entries := c.g.focusEntries(c.focusKey())
	if len(entries) == 0 {
		return focusEntry{}, false
	}
	for i, e := range entries {
		if i == 0 {
			// The newest place is where the principal is now.
			continue
		}
		if cursor != 0 && e.seq >= cursor {
			continue
		}
		if skip.session != "" && e.session == skip.session && e.window != "" && e.window == skip.window {
			continue
		}
		if skip.block != "" && e.session == skip.session && e.block == skip.block {
			continue
		}
		if f.session != "" && e.session != f.session {
			continue
		}
		if f.notSession != "" && e.session == f.notSession {
			continue
		}
		if f.window != "" && e.window != f.window {
			continue
		}
		if f.notWindow != "" && e.window == f.notWindow {
			continue
		}
		s := m.byID[e.session]
		if s == nil {
			continue
		}
		b := s.findBlock(e.block)
		if b == nil {
			continue
		}
		if w := s.window(e.window); w != nil && s.active == e.window && w.focused == e.block {
			// The block already has the focus.
			continue
		}
		return e, true
	}
	return focusEntry{}, false
}

// methodFocusHistoryReverse steps back through the calling principal's
// focus history.
func methodFocusHistoryReverse(c *control, a args) (any, *apiError) {
	sid, _, aerr := a.str("session_id")
	if aerr != nil {
		return nil, aerr
	}
	notSid, _, aerr := a.str("not_session_id")
	if aerr != nil {
		return nil, aerr
	}
	wid, _, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	notWid, _, aerr := a.str("not_window_id")
	if aerr != nil {
		return nil, aerr
	}
	cursor, hasCursor, aerr := a.int("cursor")
	if aerr != nil {
		return nil, aerr
	}
	if hasCursor && cursor < 0 {
		return nil, errBadRequest("cursor must not be negative")
	}
	if aerr := a.only("session_id", "not_session_id", "window_id", "not_window_id", "cursor"); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	if sid != "" {
		if m.byID[sid] == nil {
			return nil, errSessionNotFound("no session %s", sid)
		}
		if !c.hearsOf(sid) {
			return nil, errConflict("the connection is not attached to %s", sid)
		}
	}
	f := focusFilter{session: sid, notSession: notSid, window: wid, notWindow: notWid}
	var at uint64
	if hasCursor {
		at = uint64(cursor)
	}
	e, ok := c.reverseEntry(m, at, f, focusEntry{})
	if !ok {
		return map[string]any{"reversed": false}, nil
	}
	s := m.byID[e.session]
	b := s.findBlock(e.block)
	if _, aerr := c.focusBlock(s, b); aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"reversed": true, "cursor": e.seq,
		"session_id": e.session, "window_id": e.window, "block_id": e.block,
	}, nil
}

// focusBack focuses the newest place older than closed that still exists,
// and returns the model after that focus.
func (c *control) focusBack(m *model, closed focusEntry) *model {
	e, ok := c.reverseEntry(m, closed.seq, focusFilter{}, closed)
	if !ok {
		return m
	}
	s := m.byID[e.session]
	if s == nil {
		return m
	}
	b := s.findBlock(e.block)
	if b == nil {
		return m
	}
	after, aerr := c.focusBlock(s, b)
	if aerr != nil {
		return m
	}
	return after
}
