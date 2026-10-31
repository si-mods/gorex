package rexapi

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// target is one thing a query matched.
type target struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	Label     string `json:"label"`
	Ordinal   int    `json:"ordinal,omitempty"`
	Where     string `json:"where,omitempty"`
}

// result is one query's answer.
type result struct {
	Query      string   `json:"query"`
	Status     string   `json:"status"`
	Target     *target  `json:"target,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Candidates []target `json:"candidates,omitempty"`
}

// resolveRequest is target.resolve's arguments.
type resolveRequest struct {
	Kinds   []string `json:"kinds"`
	Queries []string `json:"queries"`
	Context *struct {
		SessionID string `json:"session_id"`
		BlockID   string `json:"block_id"`
		ClientID  string `json:"client_id"`
	} `json:"context"`
	Scope *struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	} `json:"scope"`
}

// resolve looks up each query, as Rex's target.resolve does.
func (c *control) resolve(raw json.RawMessage) (any, *apiError) {
	var p resolveRequest
	if apiErr := decode(raw, &p); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := checkKinds(p.Kinds, p.Scope); apiErr != nil {
		return nil, apiErr
	}
	if len(p.Queries) == 0 {
		return nil, errBadRequest("target.resolve needs a query")
	}
	m := c.s.current()
	sessions := m.sessions
	if p.Scope != nil && p.Scope.Session != "" {
		s, apiErr := c.scopeSession(m, p.Scope.Session)
		if apiErr != nil {
			return nil, apiErr
		}
		sessions = []*mSession{s}
	}
	// A scope window limits a block search to that window, and leaves out
	// detached blocks.
	window := ""
	if p.Scope != nil && p.Scope.Window != "" {
		w, apiErr := c.scopeWindow(m, sessions, p.Scope.Window)
		if apiErr != nil {
			out := make([]result, 0, len(p.Queries))
			for _, q := range p.Queries {
				out = append(out, result{Query: q, Status: "not_found", Reason: apiErr.Message})
			}
			return map[string]any{"results": out}, nil
		}
		window = w.ID
	}
	out := make([]result, 0, len(p.Queries))
	for _, q := range p.Queries {
		out = append(out, c.resolveOne(m, sessions, window, p.Kinds, q, &p))
	}
	return map[string]any{"results": out}, nil
}

// checkKinds refuses kinds and scopes Rex refuses.
func checkKinds(kinds []string, scope *struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}) *apiError {
	if len(kinds) == 0 {
		return errBadRequest("target.resolve needs at least one kind")
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k] {
			return errBadRequest("%q is listed twice", k)
		}
		seen[k] = true
		switch k {
		case "session", "window", "block", "client":
		default:
			return errBadRequest("no such kind %q", k)
		}
	}
	if seen["client"] && len(kinds) > 1 {
		return errBadRequest("client is looked up on its own")
	}
	if scope != nil {
		if seen["session"] {
			return errBadRequest("scope is not allowed when kinds includes session")
		}
		if scope.Window != "" && !(len(kinds) == 1 && kinds[0] == "block") {
			return errBadRequest("scope.window is allowed only when kinds is just block")
		}
	}
	return nil
}

// scopeSession looks a scope's session up.
func (c *control) scopeSession(m *model, query string) (*mSession, *apiError) {
	var matches []*mSession
	for _, s := range m.sessions {
		if s.ID == query {
			return s, nil
		}
	}
	for _, s := range m.sessions {
		if strings.EqualFold(s.Label, query) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, errConflict("scope.session %q is ambiguous", query)
	}
	return nil, errNotFound("no session matches scope.session %q", query)
}

// scopeWindow looks a scope's window up.
func (c *control) scopeWindow(m *model, sessions []*mSession, query string) (*mWindow, *apiError) {
	var matches []*mWindow
	for _, s := range sessions {
		for _, w := range s.Windows {
			if w.ID == query {
				return w, nil
			}
		}
	}
	for _, s := range sessions {
		for _, w := range s.Windows {
			if strings.EqualFold(w.Label, query) {
				matches = append(matches, w)
			}
		}
	}
	if n, err := strconv.Atoi(query); err == nil {
		for _, s := range sessions {
			if n >= 1 && n <= len(s.Windows) {
				return s.Windows[n-1], nil
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, errConflict("scope.window %q is ambiguous", query)
	}
	if len(query) >= 4 && len(query) <= 32 && isHex(query) {
		q := strings.ToLower(query)
		for _, s := range sessions {
			for _, w := range s.Windows {
				if strings.HasSuffix(strings.ToLower(w.ID), q) {
					matches = append(matches, w)
				}
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return nil, errConflict("scope.window %q is ambiguous", query)
		}
	}
	return nil, errNotFound("no window matches scope.window %q", query)
}

// resolveOne answers one query, in one window when window is set.
func (c *control) resolveOne(m *model, sessions []*mSession, window string, kinds []string, q string, p *resolveRequest) result {
	q = strings.TrimSpace(q)
	if q == "" {
		if t, ok := c.defaultTarget(m, sessions, kinds, p); ok {
			return result{Query: q, Status: "resolved", Target: t}
		}
		return result{Query: q, Status: "not_found", Reason: "there is nothing to select"}
	}
	if strings.ContainsAny(q, "\r\n\x00") {
		return result{Query: q, Status: "not_found", Reason: "the query holds a control character"}
	}
	// A query that starts like a full ID is only tried as one.
	for _, kind := range []string{"session", "window", "block", "client"} {
		if strings.HasPrefix(q, kind+":") {
			if t := c.byID(m, sessions, window, kinds, q); t != nil {
				return result{Query: q, Status: "resolved", Target: t}
			}
			return result{Query: q, Status: "not_found"}
		}
	}
	if cands := c.byLabel(m, sessions, window, kinds, q); len(cands) == 1 {
		return result{Query: q, Status: "resolved", Target: &cands[0]}
	} else if len(cands) > 1 {
		return result{Query: q, Status: "ambiguous", Candidates: cands}
	}
	if n, err := strconv.Atoi(q); err == nil {
		if cands := c.byPosition(m, sessions, window, kinds, n); len(cands) == 1 {
			return result{Query: q, Status: "resolved", Target: &cands[0]}
		} else if len(cands) > 1 {
			return result{Query: q, Status: "ambiguous", Candidates: cands}
		}
	}
	if len(q) >= 4 && len(q) <= 32 && isHex(q) {
		if cands := c.bySuffix(m, sessions, window, kinds, strings.ToLower(q)); len(cands) == 1 {
			return result{Query: q, Status: "resolved", Target: &cands[0]}
		} else if len(cands) > 1 {
			return result{Query: q, Status: "ambiguous", Candidates: cands}
		}
	}
	return result{Query: q, Status: "not_found"}
}

// defaultTarget selects what an empty query asks for.
func (c *control) defaultTarget(m *model, sessions []*mSession, kinds []string, p *resolveRequest) (*target, bool) {
	ctxSession := ""
	ctxBlock := ""
	if p.Context != nil {
		ctxSession, ctxBlock = p.Context.SessionID, p.Context.BlockID
	}
	has := func(k string) bool {
		for _, o := range kinds {
			if o == k {
				return true
			}
		}
		return false
	}
	search := sessions
	if ctxSession != "" {
		if s := m.session[ctxSession]; s != nil {
			search = []*mSession{s}
		}
	}
	if has("block") {
		if b := m.blocks[ctxBlock]; b != nil {
			for _, s := range search {
				if s.findBlock(b.ID) != nil {
					return c.targetOfBlock(s, b), true
				}
			}
		}
		var windows []*mWindow
		for _, s := range search {
			windows = append(windows, s.Windows...)
		}
		if len(windows) == 1 {
			for _, b := range windows[0].Blocks {
				if b.ID == windows[0].Focused {
					return c.targetOfBlock(m.session[b.Session], b), true
				}
			}
		}
	}
	if has("window") {
		var windows []*mWindow
		for _, s := range search {
			windows = append(windows, s.Windows...)
		}
		if len(windows) == 1 {
			return c.targetOfWindow(m.session[windows[0].Blocks[0].Session], windows[0]), true
		}
	}
	if has("session") {
		if s := m.session[ctxSession]; s != nil {
			return c.targetOfSession(s), true
		}
		if len(search) == 1 {
			return c.targetOfSession(search[0]), true
		}
	}
	if has("client") {
		c.s.mu.Lock()
		defer c.s.mu.Unlock()
		if p.Context != nil {
			if st := c.s.clients[p.Context.ClientID]; st != nil {
				return c.targetOfClient(st), true
			}
		}
		var apps []*clientState
		for _, st := range c.s.clients {
			if st.Kind == "app" {
				apps = append(apps, st)
			}
		}
		if len(apps) == 1 {
			return c.targetOfClient(apps[0]), true
		}
	}
	return nil, false
}

// byID looks a query up as a full ID.
func (c *control) byID(m *model, sessions []*mSession, window string, kinds []string, q string) *target {
	switch {
	case strings.HasPrefix(q, "session:"):
		if !includes(kinds, "session") {
			return nil
		}
		s := m.session[q]
		if s == nil || !containsSession(sessions, s) {
			return nil
		}
		return c.targetOfSession(s)
	case strings.HasPrefix(q, "window:"):
		if !includes(kinds, "window") {
			return nil
		}
		for _, s := range sessions {
			if w := s.window(q); w != nil && (window == "" || window == w.ID) {
				return c.targetOfWindow(s, w)
			}
		}
	case strings.HasPrefix(q, "block:"):
		if !includes(kinds, "block") {
			return nil
		}
		for _, s := range sessions {
			if b := s.findBlock(q); b != nil && inWindow(b, window) {
				return c.targetOfBlock(s, b)
			}
		}
	case strings.HasPrefix(q, "client:"):
		if !includes(kinds, "client") {
			return nil
		}
		c.s.mu.Lock()
		st := c.s.clients[q]
		c.s.mu.Unlock()
		if st != nil {
			return c.targetOfClient(st)
		}
	}
	return nil
}

// byLabel matches labels, ignoring case.
func (c *control) byLabel(m *model, sessions []*mSession, window string, kinds []string, q string) []target {
	var out []target
	for _, s := range sessions {
		if includes(kinds, "session") && strings.EqualFold(s.Label, q) {
			out = append(out, *c.targetOfSession(s))
		}
		if !includes(kinds, "window") && !includes(kinds, "block") {
			continue
		}
		for _, w := range s.Windows {
			if includes(kinds, "window") && strings.EqualFold(w.Label, q) && (window == "" || window == w.ID) {
				out = append(out, *c.targetOfWindow(s, w))
			}
			if includes(kinds, "block") && (window == "" || window == w.ID) {
				for _, b := range w.Blocks {
					if strings.EqualFold(b.Label, q) && inWindow(b, window) {
						out = append(out, *c.targetOfBlock(s, b))
					}
				}
			}
		}
		if includes(kinds, "block") && window == "" {
			for _, b := range s.Blocks {
				if b.Placement == "detached" && strings.EqualFold(b.Label, q) {
					out = append(out, *c.targetOfBlock(s, b))
				}
			}
		}
	}
	return out
}

// byPosition matches windows by their position in their session.
func (c *control) byPosition(m *model, sessions []*mSession, window string, kinds []string, n int) []target {
	if !includes(kinds, "window") || n < 1 {
		return nil
	}
	var out []target
	for _, s := range sessions {
		if n <= len(s.Windows) && (window == "" || window == s.Windows[n-1].ID) {
			out = append(out, *c.targetOfWindow(s, s.Windows[n-1]))
		}
	}
	return out
}

// bySuffix matches the end of IDs.
func (c *control) bySuffix(m *model, sessions []*mSession, window string, kinds []string, q string) []target {
	var out []target
	for _, s := range sessions {
		if includes(kinds, "session") && strings.HasSuffix(strings.ToLower(s.ID), q) {
			out = append(out, *c.targetOfSession(s))
		}
		for _, w := range s.Windows {
			if includes(kinds, "window") && strings.HasSuffix(strings.ToLower(w.ID), q) && (window == "" || window == w.ID) {
				out = append(out, *c.targetOfWindow(s, w))
			}
		}
		if includes(kinds, "block") {
			for _, b := range s.Blocks {
				if strings.HasSuffix(strings.ToLower(b.ID), q) && inWindow(b, window) {
					out = append(out, *c.targetOfBlock(s, b))
				}
			}
		}
	}
	return out
}

// targetOfSession names a session.
func (c *control) targetOfSession(s *mSession) *target {
	w := s.activeWindow()
	where := ""
	if w != nil {
		where = "in " + s.Label
	}
	return &target{Kind: "session", SessionID: s.ID, Label: s.Label, Where: where}
}

// targetOfWindow names a window, with its position.
func (c *control) targetOfWindow(s *mSession, w *mWindow) *target {
	n := s.ordinal(w)
	where := "window " + strconv.Itoa(n)
	return &target{Kind: "window", SessionID: s.ID, WindowID: w.ID, Label: w.Label, Ordinal: n, Where: where}
}

// targetOfBlock names a block.
func (c *control) targetOfBlock(s *mSession, b *mBlock) *target {
	t := &target{Kind: "block", SessionID: s.ID, BlockID: b.ID, Label: b.Label}
	if b.Placement == "detached" {
		t.Where = "detached"
		return t
	}
	if w := s.window(b.WindowID); w != nil {
		t.WindowID = w.ID
		t.Where = "window " + strconv.Itoa(s.ordinal(w))
	}
	return t
}

// targetOfClient names a client.
func (c *control) targetOfClient(st *clientState) *target {
	label := st.Label
	if label == "" {
		label = st.Name
	}
	where := st.Name
	if st.Version != "" {
		where += " " + st.Version
	}
	return &target{Kind: "client", ClientID: st.ID, Label: label, Where: where}
}

// inWindow tells whether a block is searched, in a scope window or any.
func inWindow(b *mBlock, window string) bool {
	return window == "" || b.WindowID == window
}

// includes tells whether kinds holds a kind.
func includes(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// containsSession tells whether a list holds a session.
func containsSession(list []*mSession, s *mSession) bool {
	for _, o := range list {
		if o == s {
			return true
		}
	}
	return false
}

// isHex tells whether a query could be the end of an ID.
func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// complete lists what a prefix could name.
func (c *control) complete(raw json.RawMessage) (any, *apiError) {
	var p struct {
		Kinds  []string `json:"kinds"`
		Prefix string   `json:"prefix"`
		Scope  *struct {
			Session string `json:"session"`
			Window  string `json:"window"`
		} `json:"scope"`
	}
	if apiErr := decode(raw, &p); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := checkKinds(p.Kinds, p.Scope); apiErr != nil {
		return nil, apiErr
	}
	m := c.s.current()
	sessions := m.sessions
	if p.Scope != nil && p.Scope.Session != "" {
		if s := m.session[p.Scope.Session]; s != nil {
			sessions = []*mSession{s}
		}
	}
	prefix := strings.ToLower(p.Prefix)
	var out []target
	match := func(s string) bool {
		if prefix == "" {
			return true
		}
		return strings.HasPrefix(strings.ToLower(s), prefix)
	}
	for _, s := range sessions {
		if includes(p.Kinds, "session") && match(s.Label) {
			out = append(out, *c.targetOfSession(s))
		}
		for _, w := range s.Windows {
			if includes(p.Kinds, "window") && (match(w.Label) || match(strconv.Itoa(s.ordinal(w)))) {
				out = append(out, *c.targetOfWindow(s, w))
			}
			if includes(p.Kinds, "block") {
				for _, b := range w.Blocks {
					if match(b.Label) || match(short(b.ID)) {
						out = append(out, *c.targetOfBlock(s, b))
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return map[string]any{"completions": out}, nil
}
