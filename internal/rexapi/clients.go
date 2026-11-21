package rexapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/coder/websocket"

	"gorex/internal/config"
	"gorex/internal/rex"
)

// configPath is where GoRex keeps its configuration file.
func (g *Gateway) configPath() string { return config.Path() }

// keymapSettings returns the configuration generation, the file's hash
// and the server's instance ID.
func (g *Gateway) keymapSettings() (int, string, string) {
	_, _, generation, hash := g.actionsInfo()
	return generation, hash, g.instanceID
}

// methodServerConfigReload reloads the configuration file, keeping the
// last good actions when it fails, and tells every client its keymap may
// have changed.
func methodServerConfigReload(c *control, a args) (any, *apiError) {
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	g := c.g
	generation, hash, diagnostics := g.reloadActions()
	if diagnostics == nil {
		diagnostics = []keymapDiagnostic{}
	}
	g.emitServerEvent("keymap_changed", map[string]any{"generation": generation, "hash": hash})
	return map[string]any{"generation": generation, "diagnostics": diagnostics}, nil
}

// emptyHostHash names the server's host list, which is always empty.
func emptyHostHash() string {
	sum := sha256.Sum256([]byte("[]"))
	return hex.EncodeToString(sum[:])
}

// methodHostsList lists the server's hosts: GoRex keeps none.
func methodHostsList(c *control, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	return map[string]any{"hosts": []any{}, "hash": emptyHostHash()}, nil
}

// methodHostsPut adds a host to the server's list, which GoRex does not
// keep.
func methodHostsPut(c *control, a args) (any, *apiError) {
	if _, _, aerr := a.obj("host"); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("host"); aerr != nil {
		return nil, aerr
	}
	return nil, errNotSupported("GoRex keeps no host list")
}

// methodHostsRemove removes a host from the server's list, which GoRex
// does not keep.
func methodHostsRemove(c *control, a args) (any, *apiError) {
	if _, _, aerr := a.str("id"); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("id"); aerr != nil {
		return nil, aerr
	}
	return nil, errNotSupported("GoRex keeps no host list")
}

// methodClientSetLabel sets the calling client's label.
func methodClientSetLabel(c *control, a args) (any, *apiError) {
	if aerr := a.only("label"); aerr != nil {
		return nil, aerr
	}
	label, aerr := a.reqStr("label")
	if aerr != nil {
		return nil, aerr
	}
	label = strings.TrimSpace(label)
	if aerr := validText(label, 64); aerr != nil {
		return nil, aerr
	}
	rec := c.rec
	rec.mu.Lock()
	var info clientInfo
	if rec.info != nil {
		info = *rec.info
	}
	changed := info.Label != label
	info.Label = label
	rec.info = &info
	rec.mu.Unlock()
	if changed {
		c.g.emitServerEvent("client_changed", map[string]any{"client": rec.view()})
	}
	return struct{}{}, nil
}

// keymapResult is client.keymap's result.
type keymapResult struct {
	Bindings    []keymapBinding    `json:"bindings"`
	Diagnostics []keymapDiagnostic `json:"diagnostics"`
	Actions     []keymapAction     `json:"actions,omitempty"`
	Modes       []keymapMode       `json:"modes,omitempty"`
	Config      keymapConfigResult `json:"config"`
}

// keymapConfigResult is the configuration the keymap came from.
type keymapConfigResult struct {
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Generation int    `json:"generation"`
	InstanceID string `json:"instance_id"`
	Hash       string `json:"hash"`
}

// methodClientKeymap returns what each key does for the client: the
// layers it sends, its defaults and its overrides, stacked. GoRex has no
// configuration file and runs no Lua scripts, so nothing sits between
// them. The client's actions are accepted and not checked, as Rex skips
// checks for actions the client performs itself.
func methodClientKeymap(c *control, a args) (any, *apiError) {
	if _, ok := a["client_id"]; ok {
		return nil, errBadRequest("the keymap is always the calling client's")
	}
	if _, ok := a["bindings"]; ok {
		return nil, errBadRequest("send bindings as a layer in defaults")
	}
	if aerr := a.only("actions", "server_actions", "defaults", "overrides"); aerr != nil {
		return nil, aerr
	}
	var in struct {
		Actions       []json.RawMessage `json:"actions"`
		ServerActions *string           `json:"server_actions"`
		Defaults      []keymapLayer     `json:"defaults"`
		Overrides     []keymapLayer     `json:"overrides"`
	}
	raw, _ := json.Marshal(a)
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errBadRequest("client.keymap request: %v", err)
	}
	layer, configActions, configModes, configDiags, path, exists, generation, hash := c.g.configKeymap()
	defaults := append([]keymapLayer{layer}, in.Defaults...)
	bindings, modes, diags, aerr := resolveKeymap(defaults, in.Overrides)
	if aerr != nil {
		return nil, aerr
	}
	diags = append(diags, configDiags...)
	modes = mergeKeymapModes(modes, configModes)
	var actions []keymapAction
	switch {
	case in.ServerActions == nil || (*in.ServerActions != "none" && *in.ServerActions != "configured"):
		actions = append(terminalActions(), configActions...)
	case *in.ServerActions == "configured":
		actions = configActions
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Name < actions[j].Name })
	return keymapResult{
		Bindings:    bindings,
		Diagnostics: diags,
		Actions:     actions,
		Modes:       modes,
		Config: keymapConfigResult{
			Path: path, Exists: exists,
			Generation: generation, InstanceID: c.g.instanceID, Hash: hash,
		},
	}, nil
}

// scriptResult is client.set_script's result.
type scriptResult struct {
	Bindings    []string `json:"bindings,omitempty"`
	AllKeys     bool     `json:"all_keys,omitempty"`
	Diagnostics []any    `json:"diagnostics,omitempty"`
}

// methodClientSetScript loads Lua scripts for the client. GoRex runs no
// Lua; an empty list unloads nothing and succeeds.
func methodClientSetScript(c *control, a args) (any, *apiError) {
	if _, ok := a["session_id"]; ok {
		return nil, errBadRequest("scripts belong to the client, not a session")
	}
	if _, ok := a["client_id"]; ok {
		return nil, errBadRequest("scripts belong to the calling client")
	}
	if aerr := a.only("scripts"); aerr != nil {
		return nil, aerr
	}
	raw, present, aerr := a.raws("scripts")
	if aerr != nil {
		return nil, aerr
	}
	if !present {
		return nil, errBadRequest("scripts is required")
	}
	for _, one := range raw {
		var script struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(one, &script); err != nil {
			return nil, errBadRequest("a script is an object")
		}
	}
	if len(raw) > 0 {
		return nil, errNotSupported("GoRex runs no Lua scripts")
	}
	return scriptResult{Bindings: []string{}, Diagnostics: []any{}}, nil
}

// actionResult is client.action's result.
type actionResult struct {
	Result        any   `json:"result,omitempty"`
	ClientActions []any `json:"client_actions"`
}

// methodClientAction performs an action from the configuration file or a
// block. GoRex has no configuration file; a block's action is a block
// method.
func methodClientAction(c *control, a args) (any, *apiError) {
	action, aerr := a.reqStr("action")
	if aerr != nil {
		return nil, aerr
	}
	origin, _, aerr := a.str("origin")
	if aerr != nil {
		return nil, aerr
	}
	if origin == "script" {
		return nil, errBadRequest("only rex.invoke reports origin script")
	}
	if raw, ok := a["servers"]; ok && string(raw) != "null" {
		return nil, errNotSupported("GoRex connects to no other servers")
	}
	if raw, ok := a["server"]; ok && string(raw) != "null" && string(raw) != `""` {
		return nil, errNotSupported("GoRex connects to no other servers")
	}
	if _, _, aerr := a.obj("args"); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("action", "args", "origin", "session_id", "block_id"); aerr != nil {
		return nil, aerr
	}
	if !strings.Contains(action, ".") {
		var callArgs map[string]any
		if raw, ok := a["args"]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &callArgs); err != nil {
				return nil, errBadRequest("args is an object")
			}
		}
		sid, _, aerr := a.str("session_id")
		if aerr != nil {
			return nil, aerr
		}
		block, _, aerr := a.str("block_id")
		if aerr != nil {
			return nil, aerr
		}
		return c.g.runConfigAction(c, action, origin, sid, block, callArgs)
	}
	i := strings.LastIndexByte(action, '.')
	creator, short := action[:i], action[i+1:]
	if creator != creatorName {
		return nil, errActionNotFound(action)
	}
	if _, ok := blockMethods[short]; !ok {
		return nil, errActionNotFound(action)
	}
	inner := args{}
	if sid, ok, aerr := a.str("session_id"); aerr != nil {
		return nil, aerr
	} else if ok && sid != "" {
		inner["session_id"] = mustRaw(sid)
	}
	if bid, ok, aerr := a.str("block_id"); aerr != nil {
		return nil, aerr
	} else if ok && bid != "" {
		inner["block_id"] = mustRaw(bid)
	}
	if raw, ok := a["args"]; ok {
		inner["args"] = raw
	}
	res, aerr := runBlockMethod(c, short, inner, false)
	if aerr != nil {
		return nil, aerr
	}
	return actionResult{Result: res, ClientActions: []any{}}, nil
}

// errActionNotFound names the action in the error, as Rex does.
func errActionNotFound(action string) *apiError {
	e := errNotFound("GoRex has no action %s", action)
	e.Action = action
	return e
}

// mustRaw returns a string as the raw JSON a request carries.
func mustRaw(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// methodClientPerform asks another client to perform an action. GoRex's
// clients do not perform actions for each other.
func methodClientPerform(c *control, a args) (any, *apiError) {
	id, aerr := a.reqStr("client_id")
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := a.reqStr("action"); aerr != nil {
		return nil, aerr
	}
	if _, _, aerr := a.obj("args"); aerr != nil {
		return nil, aerr
	}
	timeout, _, aerr := a.int("timeout_ms")
	if aerr != nil {
		return nil, aerr
	}
	if timeout < 0 {
		return nil, errBadRequest("timeout_ms must not be negative")
	}
	c.g.mu.Lock()
	rec := c.g.clients[id]
	c.g.mu.Unlock()
	if rec == nil {
		return nil, errNotFound("no client %s", id)
	}
	if lost, _ := rec.state(); lost {
		return nil, errorf("client_unavailable", 409, "client %s is reconnecting", id)
	}
	return nil, errNotSupported("GoRex does not ask its clients to perform actions")
}

// methodClientReply answers a client_prompt_requested event. GoRex sends
// no client_call_requested or client_perform_requested.
func methodClientReply(c *control, a args) (any, *apiError) {
	callID, aerr := a.reqStr("call_id")
	if aerr != nil {
		return nil, aerr
	}
	rawResult, hasResult := a["result"]
	rawError, hasError := a["error"]
	if hasResult == hasError {
		return nil, errBadRequest("send a result or an error, not both")
	}
	if hasError && string(rawError) != "null" {
		if _, _, aerr := a.obj("error"); aerr != nil {
			return nil, aerr
		}
	}
	if aerr := a.only("call_id", "result", "error"); aerr != nil {
		return nil, aerr
	}
	if aerr := c.g.replyPrompt(c, callID, rawResult, hasResult, rawError, hasError); aerr != nil {
		return nil, aerr
	}
	return struct{}{}, nil
}

// methodServerStop stops the GoRex session server, as the app does when
// it quits and ends the sessions: the server records the stop and keeps
// the layout, so that the next one starts the sessions again. Like
// Rex's, it is for the local socket only. The answer goes out before the
// server stops. Once it began, the gateway takes no new connection and
// no call that changes anything, and a call to stop fails with conflict.
//
// With if_idle the server must hold no Rex session (a workspace of the
// layout, with or without a window), no shell, and no other client, and
// no call may be running; the check and the stop are one step.
func methodServerStop(c *control, a args) (any, *apiError) {
	if c.transport != transportUnix {
		return nil, errorf("internal", 403, "server.stop is accepted only over the server's local socket")
	}
	if aerr := a.only("if_idle"); aerr != nil {
		return nil, aerr
	}
	ifIdle, _, aerr := a.boolean("if_idle")
	if aerr != nil {
		return nil, aerr
	}
	g := c.g
	g.stopMu.Lock()
	defer g.stopMu.Unlock()
	if g.stopping.Load() {
		return nil, errConflict("the server is already stopping")
	}
	// Set before the checks, so that no call or connection starts under
	// them: that makes the check and the stop one step. The cost is that
	// while an if_idle check runs, other clients' calls that change
	// something and new connections are refused for a moment. Anything but
	// a stop that went through lifts it, a panic too.
	g.stopping.Store(true)
	stopped := false
	defer func() {
		if !stopped {
			g.stopping.Store(false)
		}
	}()
	refuse := func(e *apiError) (any, *apiError) { return nil, e }
	var err error
	if ifIdle {
		if !g.hello.Can(rex.CapabilityShutdownIfIdle) {
			return refuse(errNotSupported("the GoRex server can't check that it is idle"))
		}
		// Idleness needs the layout as it is now, not the last snapshot:
		// a block opened a moment ago must count.
		m, merr := g.reread(true)
		if merr != nil {
			return refuse(errInternal("server.stop could not read the layout: %v", merr))
		}
		g.mu.Lock()
		others := len(g.conns) - 1
		g.mu.Unlock()
		switch {
		case len(m.sessions) > 0:
			return refuse(errConflict("the server has %d sessions", len(m.sessions)))
		case others > 0:
			return refuse(errConflict("%d other clients are connected", others))
		case g.inflight.Load() > 1:
			return refuse(errConflict("%d other calls are running", g.inflight.Load()-1))
		}
		err = g.shutdownIfIdle()
		if err != nil && strings.Contains(err.Error(), rex.BusyPrefix) {
			_, msg, _ := strings.Cut(err.Error(), rex.BusyPrefix)
			return refuse(errConflict("the server is in use: %s", msg))
		}
	} else {
		err = g.shutdown()
	}
	if err != nil {
		return refuse(errInternal("the server did not stop: %v", err))
	}
	stopped = true
	g.goingAway(c)
	return map[string]any{}, nil
}

// goingAway closes the control connections except the caller, with 1001, Going
// Away, which tells a client that the server stops.
func (g *Gateway) goingAway(except *control) {
	g.mu.Lock()
	conns := make([]*control, 0, len(g.conns))
	for c := range g.conns {
		if c != except {
			conns = append(conns, c)
		}
	}
	g.mu.Unlock()
	for _, c := range conns {
		c.closeWith(websocket.StatusGoingAway, "the server is stopping")
	}
}

// stopAllowed are the methods a stopping gateway still answers: those that
// change nothing.
var stopAllowed = map[string]bool{
	"server.stop": true, "server.version": true, "server.status": true,
	"server.whoami": true, "session.list": true, "client.list": true,
}
