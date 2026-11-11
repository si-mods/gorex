package rexapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"gorex/internal/rex"
)

// configPath is where Rex keeps its configuration file; GoRex reads none,
// and reports the path a client could create it at.
func (g *Gateway) configPath() string { return filepath.Join(rex.Dir(), "init.lua") }

// keymapSettings returns the configuration generation, the file's hash
// and the server's instance ID.
func (g *Gateway) keymapSettings() (int, string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.generation, g.keymapHash, g.instanceID
}

// methodServerConfigReload reloads the configuration file. GoRex has no
// configuration file to read, so this counts the load and tells every
// client its keymap may have changed.
func methodServerConfigReload(c *control, a args) (any, *apiError) {
	g := c.g
	g.mu.Lock()
	g.generation++
	generation, hash := g.generation, g.keymapHash
	g.mu.Unlock()
	g.emitServerEvent("keymap_changed", map[string]any{"generation": generation, "hash": hash})
	return map[string]any{"generation": generation, "diagnostics": []any{}}, nil
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
	bindings, modes, diags, aerr := resolveKeymap(in.Defaults, in.Overrides)
	if aerr != nil {
		return nil, aerr
	}
	generation, hash, instance := c.g.keymapSettings()
	var actions []keymapAction
	if in.ServerActions == nil || (*in.ServerActions != "none" && *in.ServerActions != "configured") {
		actions = terminalActions()
	}
	return keymapResult{
		Bindings:    bindings,
		Diagnostics: diags,
		Actions:     actions,
		Modes:       modes,
		Config: keymapConfigResult{
			Path: c.g.configPath(), Exists: false,
			Generation: generation, InstanceID: instance, Hash: hash,
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
		return nil, errActionNotFound(action)
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

// performResult is client.perform's result.
type performResult struct {
	Result      any    `json:"result,omitempty"`
	PerformedBy string `json:"performed_by"`
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

// methodClientReply answers a client_call_requested or
// client_perform_requested event. GoRex sends neither, so nothing waits.
func methodClientReply(c *control, a args) (any, *apiError) {
	if _, aerr := a.reqStr("call_id"); aerr != nil {
		return nil, aerr
	}
	_, hasResult := a["result"]
	raw, hasError := a["error"]
	if hasResult == hasError {
		return nil, errBadRequest("send a result or an error, not both")
	}
	if hasError && string(raw) != "null" {
		if _, _, aerr := a.obj("error"); aerr != nil {
			return nil, aerr
		}
	}
	return nil, errNotFound("nothing is waiting for that call")
}
