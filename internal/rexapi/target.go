package rexapi

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// targetInfo is a session, window, block, or client as target.resolve and
// target.complete report one.
type targetInfo struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	Label     string `json:"label"`
	Ordinal   int    `json:"ordinal,omitempty"`
	Where     string `json:"where,omitempty"`
}

// targetContext is where the caller is.
type targetContext struct {
	session string
	block   string
	client  string
}

// targetScope limits a search.
type targetScope struct {
	present bool
	session string
	window  string
}

// candidate is one thing a query may match, with the steps target.resolve
// tries in order.
type candidate struct {
	kind       string
	target     targetInfo
	label      string
	ordinal    int
	fullID     string
	clientKind string
}

// targetSearch is the set of things one resolve or complete searches.
type targetSearch struct {
	m       *model
	kinds   []string
	ctx     targetContext
	scope   targetScope
	all     []*mSession
	search  []*mSession
	window  *mWindow
	failure *scopeFailure
	clients []candidate
}

// scopeFailure says why a scope matched nothing or several things.
type scopeFailure struct {
	status     string
	reason     string
	candidates []targetInfo
}

// parseContext reads a target context, ignoring IDs that no longer
// exist.
func parseContext(a args, name string) (targetContext, *apiError) {
	var ctx targetContext
	o, present, err := a.obj(name)
	if err != nil || !present || o == nil {
		return ctx, err
	}
	for _, f := range []struct {
		name string
		into *string
	}{{"session_id", &ctx.session}, {"block_id", &ctx.block}, {"client_id", &ctx.client}} {
		s, _, err := o.str(f.name)
		if err != nil {
			return ctx, err
		}
		*f.into = s
	}
	return ctx, nil
}

// parseScope reads a target scope.
func parseScope(a args, name string) (targetScope, *apiError) {
	var scope targetScope
	o, present, err := a.obj(name)
	if err != nil {
		return scope, err
	}
	scope.present = present && o != nil
	if !scope.present {
		return scope, nil
	}
	s, _, err := o.str("session")
	if err != nil {
		return scope, err
	}
	w, _, err := o.str("window")
	if err != nil {
		return scope, err
	}
	scope.session, scope.window = s, w
	return scope, nil
}

// checkKinds checks a kinds argument.
func checkKinds(kinds []string) *apiError {
	if len(kinds) == 0 {
		return errBadRequest("kinds must list at least one kind")
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if k != "session" && k != "window" && k != "block" && k != "client" {
			return errBadRequest("%s is not a kind", k)
		}
		if seen[k] {
			return errBadRequest("kinds lists %s twice", k)
		}
		seen[k] = true
	}
	if seen["client"] && len(kinds) != 1 {
		return errBadRequest("client must be the only kind")
	}
	return nil
}

// hasControl reports whether s has a control character in it, which a
// query or scope may not.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// search builds what a target.resolve or target.complete searches.
func (g *Gateway) search(kinds []string, ctx targetContext, scope targetScope) (*targetSearch, *apiError) {
	if scope.present {
		for _, k := range kinds {
			if k == "session" {
				return nil, errBadRequest("scope may not be used with sessions")
			}
		}
		if scope.window != "" && !(len(kinds) == 1 && kinds[0] == "block") {
			return nil, errBadRequest("scope.window is only allowed when kinds is just block")
		}
	}
	m, err := g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	ts := &targetSearch{m: m, kinds: kinds, ctx: ctx, scope: scope, all: m.sessions}
	ts.search = m.sessions
	if scope.present && scope.session != "" {
		cands := sessionCandidates(m.sessions)
		matches, step := matchCandidates(cands, scope.session)
		switch {
		case len(matches) == 1:
			ts.search = []*mSession{m.byID[matches[0].target.SessionID]}
		case len(matches) > 1:
			ts.failure = &scopeFailure{status: "ambiguous", reason: "the session scope matches several sessions", candidates: targetsOf(matches)}
		default:
			ts.failure = &scopeFailure{status: "not_found", reason: "the session scope matches no session"}
		}
		_ = step
	} else if ctx.session != "" && m.byID[ctx.session] != nil {
		ts.search = []*mSession{m.byID[ctx.session]}
	}
	ts.clients = g.clientCandidates(ts)
	if scope.present && scope.window != "" {
		cands := windowCandidates(ts)
		matches, _ := matchCandidates(cands, scope.window)
		switch {
		case len(matches) == 1:
			if s := m.byID[matches[0].target.SessionID]; s != nil {
				ts.window = s.window(matches[0].target.WindowID)
			}
		case len(matches) > 1:
			ts.failure = &scopeFailure{status: "ambiguous", reason: "the window scope matches several windows", candidates: targetsOf(matches)}
		default:
			ts.failure = &scopeFailure{status: "not_found", reason: "the window scope matches no window"}
		}
	}
	return ts, nil
}

// candidates returns the candidates of one kind, in the search.
func (ts *targetSearch) candidates(kind string) []candidate {
	switch kind {
	case "session":
		return sessionCandidates(ts.all)
	case "window":
		return windowCandidates(ts)
	case "block":
		return blockCandidates(ts)
	case "client":
		return ts.clients
	}
	return nil
}

// sessionCandidates lists the sessions.
func sessionCandidates(sessions []*mSession) []candidate {
	out := make([]candidate, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, candidate{
			kind: "session",
			target: targetInfo{
				Kind: "session", SessionID: s.id, Label: s.label,
			},
			label: s.label, fullID: s.id,
		})
	}
	return out
}

// windowCandidates lists the windows of the sessions searched.
func windowCandidates(ts *targetSearch) []candidate {
	var out []candidate
	several := len(ts.search) > 1
	for _, s := range ts.search {
		for _, w := range s.windows {
			where := ""
			if several {
				where = "in " + s.label
			}
			out = append(out, candidate{
				kind: "window",
				target: targetInfo{
					Kind: "window", SessionID: s.id, WindowID: w.id, Label: w.label,
					Ordinal: s.ordinal(w), Where: where,
				},
				label: w.label, ordinal: s.ordinal(w), fullID: w.id,
			})
		}
	}
	return out
}

// blockCandidates lists the blocks of the sessions searched, or of the
// one window a scope names.
func blockCandidates(ts *targetSearch) []candidate {
	var out []candidate
	several := len(ts.search) > 1
	for _, s := range ts.search {
		for _, w := range s.windows {
			if ts.window != nil && w != ts.window {
				continue
			}
			where := "window " + strconv.Itoa(s.ordinal(w))
			if several {
				where += " of " + s.label
			}
			for _, b := range w.blocks {
				out = append(out, candidate{
					kind: "block",
					target: targetInfo{
						Kind: "block", SessionID: s.id, WindowID: w.id, BlockID: b.id,
						Label: b.label, Where: where,
					},
					label: b.label, fullID: b.id,
				})
			}
		}
	}
	return out
}

// clientCandidates lists the clients searched.
func (g *Gateway) clientCandidates(ts *targetSearch) []candidate {
	var only string
	if ts.failure == nil && ts.scope.present && ts.scope.session != "" && len(ts.search) == 1 {
		only = ts.search[0].id
	}
	g.mu.Lock()
	recs := make([]*clientRecord, 0, len(g.clients))
	for _, r := range g.clients {
		recs = append(recs, r)
	}
	g.mu.Unlock()
	connected := make([]*clientRecord, 0, len(recs))
	for _, r := range recs {
		r.mu.Lock()
		c := r.conn != nil
		r.mu.Unlock()
		if c {
			connected = append(connected, r)
		}
	}
	sort.Slice(connected, func(i, j int) bool { return connected[i].order < connected[j].order })
	var out []candidate
	for _, r := range connected {
		if only != "" && !r.attachedTo(only) {
			continue
		}
		label, where, kind := "", "", ""
		if r.info != nil {
			label = r.info.Label
			kind = r.info.Kind
			where = joinNonEmpty(r.info.Name, r.info.Version)
		}
		out = append(out, candidate{
			kind: "client",
			target: targetInfo{
				Kind: "client", ClientID: r.id, Label: label, Where: where,
			},
			label: label, fullID: r.id, clientKind: kind,
		})
	}
	return out
}

// targetsOf lists the candidates' targets.
func targetsOf(cands []candidate) []targetInfo {
	out := make([]targetInfo, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.target)
	}
	return out
}

// matchCandidates tries the steps target.resolve documents, and returns
// the matches of the first step that matches anything.
func matchCandidates(cands []candidate, query string) ([]candidate, string) {
	if hasControl(query) {
		return nil, "control"
	}
	if hasIDPrefix(query) {
		return filterCandidates(cands, func(c candidate) bool { return c.fullID == query }), "id"
	}
	if m := filterCandidates(cands, func(c candidate) bool { return c.fullID == query }); len(m) > 0 {
		return m, "id"
	}
	if m := filterCandidates(cands, func(c candidate) bool { return strings.EqualFold(c.label, query) }); len(m) > 0 {
		return m, "label"
	}
	if n, err := strconv.Atoi(query); err == nil && n > 0 && strconv.Itoa(n) == query {
		if m := filterCandidates(cands, func(c candidate) bool { return c.ordinal == n }); len(m) > 0 {
			return m, "ordinal"
		}
	}
	if isIDEnd(query) {
		q := strings.ToLower(query)
		if m := filterCandidates(cands, func(c candidate) bool {
			return strings.HasSuffix(strings.ToLower(c.fullID), q)
		}); len(m) > 0 {
			return m, "id"
		}
	}
	return nil, ""
}

// hasIDPrefix tells whether a query starts like a full ID, in which case
// it is only tried as one.
func hasIDPrefix(q string) bool {
	for _, p := range []string{"session:", "window:", "block:", "client:"} {
		if strings.HasPrefix(q, p) {
			return true
		}
	}
	return false
}

// isIDEnd tells whether q is an end of an ID: four to thirty-two
// hexadecimal digits.
func isIDEnd(q string) bool {
	if len(q) < 4 || len(q) > 32 {
		return false
	}
	for _, r := range q {
		if !isHex(r) {
			return false
		}
	}
	return true
}

func isHex(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

func filterCandidates(cands []candidate, keep func(candidate) bool) []candidate {
	var out []candidate
	for _, c := range cands {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

// resolveResult is one query's result.
type resolveResult struct {
	Query      string       `json:"query"`
	Status     string       `json:"status"`
	Target     *targetInfo  `json:"target,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	Candidates []targetInfo `json:"candidates,omitempty"`
}

// resolveAnswer is target.resolve's result.
type resolveAnswer struct {
	Results []resolveResult `json:"results"`
}

func methodTargetResolve(c *control, a args) (any, *apiError) {
	kinds, _, aerr := a.strs("kinds")
	if aerr != nil {
		return nil, aerr
	}
	if kinds == nil {
		return nil, errBadRequest("kinds is required")
	}
	if aerr := checkKinds(kinds); aerr != nil {
		return nil, aerr
	}
	queries, _, aerr := a.strs("queries")
	if aerr != nil {
		return nil, aerr
	}
	if queries == nil {
		return nil, errBadRequest("queries is required")
	}
	if len(queries) == 0 {
		return nil, errBadRequest("queries must have at least one entry")
	}
	for _, q := range queries {
		if hasControl(q) {
			return nil, errBadRequest("a query has a control character")
		}
	}
	ctx, aerr := parseContext(a, "context")
	if aerr != nil {
		return nil, aerr
	}
	scope, aerr := parseScope(a, "scope")
	if aerr != nil {
		return nil, aerr
	}
	if hasControl(scope.session) || hasControl(scope.window) {
		return nil, errBadRequest("a scope has a control character")
	}
	ts, aerr := c.g.search(kinds, ctx, scope)
	if aerr != nil {
		return nil, aerr
	}
	out := resolveAnswer{Results: make([]resolveResult, 0, len(queries))}
	for _, q := range queries {
		out.Results = append(out.Results, ts.resolveOne(q))
	}
	return out, nil
}

// resolveOne resolves one query.
func (ts *targetSearch) resolveOne(query string) resolveResult {
	res := resolveResult{Query: query}
	if ts.failure != nil {
		res.Status = ts.failure.status
		res.Reason = ts.failure.reason
		res.Candidates = ts.failure.candidates
		return res
	}
	if strings.TrimSpace(query) == "" {
		return ts.resolveDefault()
	}
	var cands []candidate
	for _, k := range ts.kinds {
		cands = append(cands, ts.candidates(k)...)
	}
	matches, _ := matchCandidates(cands, query)
	switch len(matches) {
	case 1:
		res.Status = "resolved"
		res.Target = &matches[0].target
	case 0:
		res.Status = "not_found"
		res.Reason = "nothing matches " + query
	default:
		res.Status = "ambiguous"
		res.Candidates = targetsOf(matches)
	}
	return res
}

// resolveDefault resolves an empty query to the default for the most
// specific kind asked for.
func (ts *targetSearch) resolveDefault() resolveResult {
	res := resolveResult{Status: "not_found"}
	pick := func(cands []candidate, reason string) resolveResult {
		switch len(cands) {
		case 1:
			res.Status, res.Target = "resolved", &cands[0].target
		case 0:
			res.Reason = reason
		default:
			res.Status, res.Candidates = "ambiguous", targetsOf(cands)
		}
		return res
	}
	has := func(kind string) bool {
		for _, k := range ts.kinds {
			if k == kind {
				return true
			}
		}
		return false
	}
	switch {
	case has("block"):
		return ts.defaultBlock()
	case has("window"):
		return ts.defaultWindow()
	case has("client"):
		var cands []candidate
		if ts.ctx.client != "" {
			cands = filterCandidates(ts.clients, func(c candidate) bool { return c.fullID == ts.ctx.client })
		}
		if len(cands) == 0 {
			cands = filterCandidates(ts.clients, func(c candidate) bool { return c.kind == "client" })
			var apps []candidate
			for _, c := range cands {
				if c.clientKind == "app" {
					apps = append(apps, c)
				}
			}
			return pick(apps, "no client application is connected")
		}
		return pick(cands, "no client matches")
	default:
		var cands []candidate
		if ts.ctx.session != "" {
			cands = filterCandidates(sessionCandidates(ts.all), func(c candidate) bool { return c.fullID == ts.ctx.session })
		}
		if len(cands) == 0 {
			cands = sessionCandidates(ts.all)
		}
		return pick(cands, "there are no sessions")
	}
}

// defaultWindow resolves an empty window query.
func (ts *targetSearch) defaultWindow() resolveResult {
	if len(ts.search) > 1 {
		cands := sessionCandidates(ts.search)
		return resolveResult{Status: "ambiguous", Candidates: targetsOf(cands)}
	}
	if len(ts.search) == 0 {
		return resolveResult{Status: "not_found", Reason: "there are no windows"}
	}
	s := ts.search[0]
	if ts.ctx.block != "" {
		if b := s.findBlock(ts.ctx.block); b != nil {
			if w := s.window(b.window); w != nil {
				cands := filterCandidates(windowCandidates(ts), func(c candidate) bool { return c.fullID == w.id })
				return pickOne(cands, "the block's window is not searched")
			}
		}
	}
	w := s.window(s.active)
	if w == nil && len(s.windows) > 0 {
		w = s.windows[0]
	}
	if w == nil {
		return resolveResult{Status: "not_found", Reason: "the session has no windows"}
	}
	cands := filterCandidates(windowCandidates(ts), func(c candidate) bool { return c.fullID == w.id })
	return pickOne(cands, "the window is not searched")
}

// defaultBlock resolves an empty block query.
func (ts *targetSearch) defaultBlock() resolveResult {
	var cands []candidate
	if ts.window != nil {
		cands = filterCandidates(blockCandidates(ts), func(c candidate) bool { return c.target.WindowID == ts.window.id })
	} else if ts.ctx.block != "" {
		cands = filterCandidates(blockCandidates(ts), func(c candidate) bool { return c.fullID == ts.ctx.block })
	}
	if len(cands) == 0 && ts.window == nil {
		// A block that is no longer among those searched asks for the
		// focused block of the window an empty window query chooses.
		w := ts.defaultWindow()
		if w.Status != "resolved" {
			return w
		}
		cands = filterCandidates(blockCandidates(ts), func(c candidate) bool { return c.target.WindowID == w.Target.WindowID })
	}
	if len(cands) == 0 {
		return resolveResult{Status: "not_found", Reason: "no block matches"}
	}
	var focused []candidate
	for _, c := range cands {
		if w := ts.windowOf(c.target); w != nil && w.focused == c.target.BlockID {
			focused = append(focused, c)
		}
	}
	if len(focused) == 1 {
		return resolveResult{Status: "resolved", Target: &focused[0].target}
	}
	if len(focused) == 0 {
		return resolveResult{Status: "not_found", Reason: "the window has no focused block"}
	}
	return pickOne(focused, "several blocks are focused")
}

// windowOf returns the window a target names, in the search.
func (ts *targetSearch) windowOf(t targetInfo) *mWindow {
	if s := ts.m.byID[t.SessionID]; s != nil {
		return s.window(t.WindowID)
	}
	return nil
}

// pickOne returns the one candidate, or why there is none.
func pickOne(cands []candidate, reason string) resolveResult {
	switch len(cands) {
	case 1:
		return resolveResult{Status: "resolved", Target: &cands[0].target}
	case 0:
		return resolveResult{Status: "not_found", Reason: reason}
	default:
		return resolveResult{Status: "ambiguous", Candidates: targetsOf(cands)}
	}
}

// completion is one of target.complete's results.
type completion struct {
	Value  string     `json:"value"`
	Form   string     `json:"form"`
	Target targetInfo `json:"target"`
}

// completeAnswer is target.complete's result.
type completeAnswer struct {
	Completions []completion `json:"completions"`
}

func methodTargetComplete(c *control, a args) (any, *apiError) {
	kinds, _, aerr := a.strs("kinds")
	if aerr != nil {
		return nil, aerr
	}
	if kinds == nil {
		return nil, errBadRequest("kinds is required")
	}
	if aerr := checkKinds(kinds); aerr != nil {
		return nil, aerr
	}
	prefix, ok, aerr := a.str("prefix")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("prefix is required")
	}
	if hasControl(prefix) {
		return nil, errBadRequest("prefix has a control character")
	}
	prefix = strings.TrimSpace(prefix)
	ctx, aerr := parseContext(a, "context")
	if aerr != nil {
		return nil, aerr
	}
	scope, aerr := parseScope(a, "scope")
	if aerr != nil {
		return nil, aerr
	}
	if hasControl(scope.session) || hasControl(scope.window) {
		return nil, errBadRequest("a scope has a control character")
	}
	ts, aerr := c.g.search(kinds, ctx, scope)
	if aerr != nil {
		return nil, aerr
	}
	out := completeAnswer{Completions: []completion{}}
	if ts.failure != nil {
		return out, nil
	}
	for _, kind := range kinds {
		cands := ts.candidates(kind)
		out.Completions = append(out.Completions, completeKind(cands, kind, prefix)...)
	}
	return out, nil
}

// completeKind lists the labels, positions and short IDs of one kind
// that start with prefix.
func completeKind(cands []candidate, kind, prefix string) []completion {
	lower := strings.ToLower(prefix)
	var labels []candidate
	for _, c := range cands {
		if c.label != "" && strings.HasPrefix(strings.ToLower(c.label), lower) {
			labels = append(labels, c)
		}
	}
	sort.SliceStable(labels, func(i, j int) bool {
		a, b := strings.ToLower(labels[i].label), strings.ToLower(labels[j].label)
		if a != b {
			return a < b
		}
		return labels[i].fullID < labels[j].fullID
	})
	var out []completion
	for _, c := range labels {
		out = append(out, completion{Value: c.label, Form: "label", Target: c.target})
	}
	if kind == "window" && (prefix == "" || allDigits(prefix)) {
		windows := append([]candidate(nil), cands...)
		sort.SliceStable(windows, func(i, j int) bool { return windows[i].ordinal < windows[j].ordinal })
		for _, c := range windows {
			if c.ordinal <= 0 {
				continue
			}
			value := strconv.Itoa(c.ordinal)
			if !strings.HasPrefix(value, prefix) {
				continue
			}
			out = append(out, completion{Value: value, Form: "ordinal", Target: c.target})
		}
	}
	if prefix == "" || allHexDigits(prefix) {
		ids := append([]candidate(nil), cands...)
		sort.SliceStable(ids, func(i, j int) bool {
			a, b := shortHex(ids[i].fullID), shortHex(ids[j].fullID)
			if a != b {
				return a < b
			}
			return ids[i].fullID < ids[j].fullID
		})
		for _, c := range ids {
			value := shortHex(c.fullID)
			if !strings.HasPrefix(strings.ToLower(value), lower) {
				continue
			}
			out = append(out, completion{Value: value, Form: "id", Target: c.target})
		}
	}
	return out
}

// allDigits tells whether s is one or more decimal digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// allHexDigits tells whether s is one or more hexadecimal digits.
func allHexDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isHex(r) {
			return false
		}
	}
	return true
}

// shortHex is the last six hexadecimal digits of an ID.
func shortHex(id string) string {
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}
