package rexapi

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

// args are a request's arguments, read one field at a time so that a
// field of the wrong type is bad_request, as Rex says.
type args map[string]json.RawMessage

// decodeArgs reads a request's payload, which must be a JSON object or
// absent.
func decodeArgs(raw json.RawMessage) (args, *apiError) {
	if len(raw) == 0 || string(raw) == "null" {
		return args{}, nil
	}
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed[0] != '{' {
		return nil, errBadRequest("the arguments must be an object")
	}
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, errBadRequest("the arguments must be an object")
	}
	return a, nil
}

// str returns a string field, and whether it was there.
func (a args) str(name string) (string, bool, *apiError) {
	raw, ok := a[name]
	if !ok || string(raw) == "null" {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, errBadRequest("%s must be a string", name)
	}
	return s, true, nil
}

// reqStr returns a required string field.
func (a args) reqStr(name string) (string, *apiError) {
	s, ok, err := a.str(name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errBadRequest("%s is required", name)
	}
	return s, nil
}

// strs returns an array of strings, whether it was there, and whether it
// was null.
func (a args) strs(name string) ([]string, bool, *apiError) {
	raw, ok := a[name]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var ss []string
	if err := json.Unmarshal(raw, &ss); err != nil {
		return nil, false, errBadRequest("%s must be an array of strings", name)
	}
	return ss, true, nil
}

// obj returns an object field, whether it was there, and whether it was
// null.
func (a args) obj(name string) (args, bool, *apiError) {
	raw, ok := a[name]
	if !ok {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	var o args
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, false, errBadRequest("%s must be an object", name)
	}
	return o, true, nil
}

// methods are the methods this server implements.
var methods = map[string]func(*control, args) (any, *apiError){
	"server.version":     methodServerVersion,
	"server.status":      methodServerStatus,
	"server.whoami":      methodServerWhoami,
	"help.all":           methodHelpAll,
	"session.list":       methodSessionList,
	"session.attach":     methodSessionAttach,
	"session.attach_all": methodSessionAttachAll,
	"session.detach":     methodSessionDetach,
	"session.bootstrap":  methodSessionBootstrap,
	"session.view":       methodSessionView,
	"target.resolve":     methodTargetResolve,
	"target.complete":    methodTargetComplete,
	"client.list":        methodClientList,
	"client.inspect":     methodClientInspect,
	"data.connect":       methodDataConnect,
}

// versionResult is server.version's result.
type versionResult struct {
	RexVersion     string  `json:"rex_version"`
	Platform       string  `json:"platform"`
	Commit         string  `json:"commit,omitempty"`
	BuiltAt        *string `json:"built_at,omitempty"`
	Dirty          bool    `json:"dirty"`
	APIRevision    int     `json:"api_revision,omitempty"`
	APIMinRevision int     `json:"api_min_revision,omitempty"`
}

func methodServerVersion(*control, args) (any, *apiError) {
	return versionResult{RexVersion: "0.0.0-gorex", Platform: platform(), Dirty: false, APIRevision: apiRevision}, nil
}

// statusResult is server.status's result.
type statusResult struct {
	PID            int          `json:"pid"`
	InstanceID     string       `json:"instance_id"`
	HostID         string       `json:"host_id,omitempty"`
	StartedAt      string       `json:"started_at"`
	ExitEmpty      bool         `json:"exit_empty"`
	SessionCount   int          `json:"session_count"`
	Tailscale      any          `json:"tailscale,omitempty"`
	APIRevision    int          `json:"api_revision,omitempty"`
	APIMinRevision int          `json:"api_min_revision,omitempty"`
	Config         configResult `json:"config"`
}

type configResult struct {
	Generation int    `json:"generation"`
	Hash       string `json:"hash"`
}

func (g *Gateway) status() statusResult {
	m := g.modelNow()
	if fresh, err := g.current(); err == nil {
		m = fresh
	}
	return statusResult{
		PID:          os.Getpid(),
		InstanceID:   g.instanceID,
		HostID:       g.host,
		StartedAt:    connectedAtString(g.started),
		ExitEmpty:    false,
		SessionCount: len(m.sessions),
		APIRevision:  apiRevision,
		Config:       configResult{Generation: 1},
	}
}

func methodServerStatus(c *control, _ args) (any, *apiError) {
	return c.g.status(), nil
}

// whoamiResult is server.whoami's result.
type whoamiResult struct {
	Principal map[string]any `json:"principal"`
	Account   string         `json:"account,omitempty"`
}

func methodServerWhoami(c *control, _ args) (any, *apiError) {
	return whoamiResult{Principal: c.rec.principal, Account: c.g.user}, nil
}

func methodHelpAll(*control, args) (any, *apiError) {
	doc := map[string]json.RawMessage{}
	for k, v := range helpDoc() {
		if k != "http" {
			doc[k] = v
		}
	}
	return doc, nil
}

// sessionsResult is session.list's and session.attach_all's result.
type sessionsResult struct {
	Sessions []sessionListEntry `json:"sessions"`
}

func methodSessionList(c *control, _ args) (any, *apiError) {
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	return sessionsResult{Sessions: m.list()}, nil
}

// attachResult is session.attach's result.
type attachResult struct {
	ClientID string `json:"client_id"`
	View     view   `json:"view"`
}

func methodSessionAttach(c *control, a args) (any, *apiError) {
	sid, aerr := a.reqStr("session_id")
	if aerr != nil {
		return nil, aerr
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	s := m.byID[sid]
	if s == nil {
		return nil, errSessionNotFound("no session %s", sid)
	}
	if c.rec.attach(sid) {
		c.g.emitClientEvent(c.rec, sid, "client_connected")
	}
	return attachResult{ClientID: c.rec.id, View: s.view(c.g.host)}, nil
}

func methodSessionAttachAll(c *control, a args) (any, *apiError) {
	scope, aerr := a.reqStr("event_scope")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := checkScope(scope); aerr != nil {
		return nil, aerr
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	c.rec.setAll(scope)
	return sessionsResult{Sessions: m.list()}, nil
}

// checkScope checks an attach_all or bootstrap event_scope.
func checkScope(scope string) *apiError {
	if scope != "session" && scope != "all" {
		return errBadRequest("event_scope must be session or all")
	}
	return nil
}

func methodSessionDetach(c *control, a args) (any, *apiError) {
	sid, aerr := a.reqStr("session_id")
	if aerr != nil {
		return nil, aerr
	}
	if c.rec.detach(sid) {
		c.g.emitClientEvent(c.rec, sid, "client_disconnected")
	}
	return struct{}{}, nil
}

// attachedSession is one session a bootstrap attached.
type attachedSession struct {
	Session sessionListEntry `json:"session"`
	View    view             `json:"view"`
}

// bootstrapAttachAll is session.bootstrap's attach_all field.
type bootstrapAttachAll struct {
	Sessions []sessionListEntry `json:"sessions"`
}

// bootstrapResult is session.bootstrap's result.
type bootstrapResult struct {
	ClientID         string             `json:"client_id"`
	ServerStatus     statusResult       `json:"server_status"`
	AttachAll        bootstrapAttachAll `json:"attach_all"`
	SelectedSession  *attachedSession   `json:"selected_session"`
	AttachedSessions *[]attachedSession `json:"attached_sessions,omitempty"`
}

func methodSessionBootstrap(c *control, a args) (any, *apiError) {
	scope, aerr := a.reqStr("event_scope")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := checkScope(scope); aerr != nil {
		return nil, aerr
	}
	preferred, _, aerr := a.str("preferred_session_id")
	if aerr != nil {
		return nil, aerr
	}
	ids, present, aerr := a.strs("attach_session_ids")
	if aerr != nil {
		return nil, aerr
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	c.rec.setAll(scope)
	res := bootstrapResult{
		ClientID:     c.rec.id,
		ServerStatus: c.g.status(),
		AttachAll:    bootstrapAttachAll{Sessions: m.list()},
	}
	attach := func(s *mSession) attachedSession {
		if c.rec.attach(s.id) {
			c.g.emitClientEvent(c.rec, s.id, "client_connected")
		}
		return attachedSession{
			Session: sessionListEntry{SessionID: s.id, Label: s.label},
			View:    s.view(c.g.host),
		}
	}
	if present && ids != nil {
		seen := map[string]bool{}
		attached := []attachedSession{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if s := m.byID[id]; s != nil {
				attached = append(attached, attach(s))
			}
		}
		res.AttachedSessions = &attached
		return res, nil
	}
	var chosen *mSession
	if preferred != "" {
		chosen = m.byID[preferred]
	}
	if chosen == nil && len(m.sessions) > 0 {
		chosen = m.sessions[0]
	}
	if chosen != nil {
		one := attach(chosen)
		res.SelectedSession = &one
	}
	return res, nil
}

func methodSessionView(c *control, a args) (any, *apiError) {
	sid, aerr := a.reqStr("session_id")
	if aerr != nil {
		return nil, aerr
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	s := m.byID[sid]
	if s == nil {
		return nil, errSessionNotFound("no session %s", sid)
	}
	all, _ := c.rec.allScope()
	if !all && !c.rec.attachedTo(sid) {
		return nil, errConflict("the connection is not attached to %s", sid)
	}
	return s.view(c.g.host), nil
}

// clientView is a client as client.list and client.inspect report it.
type clientView struct {
	ClientID         string         `json:"client_id"`
	Info             map[string]any `json:"info,omitempty"`
	Principal        map[string]any `json:"principal"`
	ConnectedAt      string         `json:"connected_at"`
	ConnectionState  string         `json:"connection_state,omitempty"`
	ConnectionLostAt *string        `json:"connection_lost_at,omitempty"`
	SessionIDs       []string       `json:"session_ids,omitempty"`
}

// view returns the client as the API reports it.
func (r *clientRecord) view() clientView {
	lost, lostAt := r.state()
	v := clientView{
		ClientID:    r.id,
		Info:        r.info.json(),
		Principal:   r.principal,
		ConnectedAt: connectedAtString(r.connectedAt),
	}
	if lost {
		v.ConnectionState = "lost"
		at := connectedAtString(lostAt)
		v.ConnectionLostAt = &at
	}
	if ids := r.attachedList(); len(ids) > 0 {
		v.SessionIDs = sortedStrings(ids)
	}
	return v
}

// listResult is client.list's result.
type listResult struct {
	Clients []clientView `json:"clients"`
}

func methodClientList(c *control, a args) (any, *apiError) {
	sid, _, aerr := a.str("session_id")
	if aerr != nil {
		return nil, aerr
	}
	kinds, hasKinds, aerr := a.strs("kinds")
	if aerr != nil {
		return nil, aerr
	}
	hasKinds = hasKinds && kinds != nil
	wanted := map[string]bool{}
	for _, k := range kinds {
		if k == "app" || k == "cli" || k == "agent" || k == "other" {
			wanted[k] = true
		} else {
			wanted["other"] = true
		}
	}
	c.g.mu.Lock()
	recs := make([]*clientRecord, 0, len(c.g.clients))
	for _, r := range c.g.clients {
		recs = append(recs, r)
	}
	c.g.mu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].order < recs[j].order })
	out := make([]clientView, 0, len(recs))
	for _, r := range recs {
		if sid != "" && !r.attachedTo(sid) {
			continue
		}
		if hasKinds {
			if r.info == nil || r.info.Kind == "" || !wanted[r.info.Kind] {
				continue
			}
		}
		out = append(out, r.view())
	}
	return listResult{Clients: out}, nil
}

// inspectResult is client.inspect's result.
type inspectResult struct {
	Client clientView `json:"client"`
}

func methodClientInspect(c *control, a args) (any, *apiError) {
	id, ok, aerr := a.str("client_id")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || id == "" {
		return nil, errBadRequest("client_id is required")
	}
	if !isUUIDID(id, "client") {
		return nil, errBadRequest("%s is not a client ID", id)
	}
	c.g.mu.Lock()
	rec := c.g.clients[id]
	c.g.mu.Unlock()
	if rec == nil {
		return nil, errNotFound("no client %s", id)
	}
	return inspectResult{Client: rec.view()}, nil
}

// connectResult is data.connect's result.
type connectResult struct {
	Ticket      string `json:"ticket"`
	ExpiresInMS int    `json:"expires_in_ms"`
}

func methodDataConnect(c *control, a args) (any, *apiError) {
	sid, aerr := a.reqStr("session_id")
	if aerr != nil {
		return nil, aerr
	}
	block, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	dataRef, aerr := a.reqStr("data_ref_id")
	if aerr != nil {
		return nil, aerr
	}
	subprotocol, _, aerr := a.str("subprotocol")
	if aerr != nil {
		return nil, aerr
	}
	if !wellFormedRef(dataRef) {
		return nil, errBadRequest("data_ref_id is malformed")
	}
	if subprotocol != "" && !wellFormedRef(subprotocol) {
		return nil, errBadRequest("subprotocol is malformed")
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	s := m.byID[sid]
	if s == nil {
		return nil, errSessionNotFound("no session %s", sid)
	}
	b := s.findBlock(block)
	if b == nil {
		return nil, errNotFound("no block %s in %s", block, sid)
	}
	if !c.rec.attachedTo(sid) {
		return nil, errConflict("session.attach or session.bootstrap is required for data connections")
	}
	ticket := c.g.issueTicket(&ticket{
		record:      c.rec.id,
		session:     sid,
		block:       block,
		sid:         b.sid,
		dataRef:     dataRef,
		subprotocol: subprotocol,
		expires:     time.Now().Add(ticketWait),
	})
	return connectResult{Ticket: ticket, ExpiresInMS: int(ticketWait / time.Millisecond)}, nil
}

// wellFormedRef tells whether a data source name or subprotocol name is
// well formed: a short name of letters, digits and the punctuation Rex
// uses.
func wellFormedRef(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
