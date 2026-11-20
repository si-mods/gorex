package rexapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"gorex/internal/config"
)

// actionDeadline is how long one configured action may run. A test sets
// it to be short.
var actionDeadline = 30 * time.Second

// actionsRuntime is the configuration the gateway runs actions from: the
// file's actions, bindings and modes, and the state of the last load.
type actionsRuntime struct {
	cfg         *config.Config
	path        string
	exists      bool
	generation  int
	hash        string
	diagnostics []keymapDiagnostic
}

// actionsNowLocked returns the active configuration, loading it the
// first time. The caller holds actionsMu.
func (g *Gateway) actionsNowLocked() *actionsRuntime {
	if g.actions == nil {
		rt := loadActions()
		rt.generation = 1
		g.actions = rt
	}
	return g.actions
}

// loadActions reads the configuration file, its hash and its
// diagnostics. A file that fails to load leaves cfg nil.
func loadActions() *actionsRuntime {
	path := config.Path()
	rt := &actionsRuntime{path: path}
	if data, err := os.ReadFile(path); err == nil {
		rt.exists = true
		sum := sha256.Sum256(data)
		rt.hash = hex.EncodeToString(sum[:])
	}
	cfg, err := config.LoadCheck(path)
	if err != nil {
		rt.diagnostics = append(rt.diagnostics, keymapDiagnostic{
			Severity: "error", Source: path, Message: err.Error(),
		})
		return rt
	}
	rt.cfg = cfg
	for _, w := range cfg.Warnings {
		rt.diagnostics = append(rt.diagnostics, keymapDiagnostic{
			Severity: "warning", Source: path, Message: w,
		})
	}
	return rt
}

// reloadActions reads the file again. A file that fails to load leaves
// the last good actions in place. It returns the generation of the load,
// the active file's hash and the diagnostics.
func (g *Gateway) reloadActions() (int, string, []keymapDiagnostic) {
	g.actionsMu.Lock()
	defer g.actionsMu.Unlock()
	prev := g.actionsNowLocked()
	next := loadActions()
	next.generation = prev.generation + 1
	if next.cfg == nil {
		next.cfg = prev.cfg
		next.hash = prev.hash
	} else if prev.cfg != nil {
		prev.cfg.Close()
	}
	g.actions = next
	return next.generation, next.hash, next.diagnostics
}

// closeActions frees the configuration's Lua state.
func (g *Gateway) closeActions() {
	g.actionsMu.Lock()
	defer g.actionsMu.Unlock()
	if g.actions != nil && g.actions.cfg != nil {
		g.actions.cfg.Close()
		g.actions.cfg = nil
	}
}

// actionsInfo returns the path, existence, generation and hash of the
// active configuration.
func (g *Gateway) actionsInfo() (path string, exists bool, generation int, hash string) {
	g.actionsMu.Lock()
	defer g.actionsMu.Unlock()
	rt := g.actionsNowLocked()
	return rt.path, rt.exists, rt.generation, rt.hash
}

// configKeymap builds the configuration's layer, its actions, its modes
// and its diagnostics for client.keymap.
func (g *Gateway) configKeymap() (keymapLayer, []keymapAction, []keymapMode, []keymapDiagnostic, string, bool, int, string) {
	g.actionsMu.Lock()
	defer g.actionsMu.Unlock()
	rt := g.actionsNowLocked()
	layer := keymapLayer{Name: "init.lua", Bindings: []keymapBinding{}}
	var actions []keymapAction
	var modes []keymapMode
	if rt.cfg != nil {
		for _, b := range rt.cfg.Bindings {
			entry := keymapBinding{Keys: splitKeySequence(b.Key), Action: b.Action, Mode: b.Mode}
			if len(b.Args) > 0 {
				entry.Args, _ = json.Marshal(b.Args)
			}
			layer.Bindings = append(layer.Bindings, entry)
		}
		for _, a := range rt.cfg.Actions {
			actions = append(actions, keymapAction{
				Name: a.Name, Kind: "config", Title: a.Title, Description: a.Description,
				Args: actionArgsSchema(a.Args),
			})
		}
		for _, m := range rt.cfg.Modes {
			modes = append(modes, keymapMode{Name: m.Name, Exclusive: m.Exclusive})
		}
	}
	return layer, actions, modes, append([]keymapDiagnostic(nil), rt.diagnostics...), rt.path, rt.exists, rt.generation, rt.hash
}

// splitKeySequence splits a key sequence at ">".
func splitKeySequence(s string) []string {
	parts := strings.Split(s, ">")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// actionArgsSchema is an action's declared args as a JSON Schema.
func actionArgsSchema(args []config.Arg) json.RawMessage {
	props := map[string]any{}
	var required []string
	for _, a := range args {
		prop := map[string]any{}
		switch a.Type {
		case "string", "number", "integer", "boolean":
			prop["type"] = a.Type
		}
		props[a.Name] = prop
		if !a.Optional {
			required = append(required, a.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, _ := json.Marshal(schema)
	return b
}

// runConfigAction runs an action of the configuration, with the caller's
// connection as its host. It runs one action at a time.
func (g *Gateway) runConfigAction(c *control, name, origin, sid, block string, callArgs map[string]any) (any, *apiError) {
	g.actionsMu.Lock()
	defer g.actionsMu.Unlock()
	rt := g.actionsNowLocked()
	if rt.cfg == nil {
		return nil, errActionNotFound(name)
	}
	var act *config.Action
	for i := range rt.cfg.Actions {
		if rt.cfg.Actions[i].Name == name {
			act = &rt.cfg.Actions[i]
			break
		}
	}
	if act == nil {
		return nil, errActionNotFound(name)
	}
	if aerr := checkActionArgs(*act, callArgs); aerr != nil {
		return nil, aerr
	}
	host := &actionHost{c: c, session: sid, block: block}
	rt.cfg.SetInvoke(host.invoke)
	ctx, cancel := context.WithTimeout(context.Background(), actionDeadline)
	defer cancel()
	vals := map[string]any{"origin": origin}
	if sid != "" {
		vals["session_id"] = sid
	}
	if block != "" {
		vals["block_id"] = block
	}
	if err := rt.cfg.InvokeWithin(ctx, name, vals, callArgs); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errorf("timeout", 408, "%s did not finish in %s", name, actionDeadline)
		}
		return nil, errInternal("%s: %v", name, err)
	}
	return actionResult{ClientActions: []any{}}, nil
}

// checkActionArgs checks a call's args against the action's declaration.
func checkActionArgs(a config.Action, args map[string]any) *apiError {
	declared := make(map[string]config.Arg, len(a.Args))
	for _, d := range a.Args {
		declared[d.Name] = d
	}
	for name := range args {
		if _, ok := declared[name]; !ok {
			return errBadRequest("%s: no argument %q", a.Name, name)
		}
	}
	for _, d := range a.Args {
		v, ok := args[d.Name]
		if !ok || v == nil {
			if d.Optional {
				continue
			}
			return errBadRequest("%s: %s is required", a.Name, d.Name)
		}
		if !actionArgType(d.Type, v) {
			return errBadRequest("%s: %s must be a %s", a.Name, d.Name, d.Type)
		}
	}
	return nil
}

// actionArgType tells whether v has the declared type.
func actionArgType(typ string, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	case "boolean":
		_, ok := v.(bool)
		return ok
	default:
		return true
	}
}

// actionHost runs a built-in action for a configured action. The gateway
// performs the built-ins it can, and names the others.
type actionHost struct {
	c       *control
	session string
	block   string
}

// invoke runs one built-in action.
func (h *actionHost) invoke(id string, args map[string]any) error {
	need := func() (string, error) {
		if h.session == "" {
			return "", errors.New("session_id is required for " + id)
		}
		return h.session, nil
	}
	str := func(name string) (string, error) {
		v, ok := args[name].(string)
		if !ok || v == "" {
			return "", fmt.Errorf("%s: %s is required", id, name)
		}
		return v, nil
	}
	switch id {
	case "tab.new":
		sid, err := need()
		if err != nil {
			return err
		}
		return h.call("session.new_window", map[string]any{"session_id": sid, "layout": shellLayout()})
	case "tab.close":
		sid, err := need()
		if err != nil {
			return err
		}
		return h.call("session.close_window", map[string]any{"session_id": sid})
	case "tab.rename":
		sid, err := need()
		if err != nil {
			return err
		}
		label, err := str("label")
		if err != nil {
			return err
		}
		return h.call("session.set_window_label", map[string]any{"session_id": sid, "label": label})
	case "pane.split.right", "pane.split.down":
		sid, err := need()
		if err != nil {
			return err
		}
		direction := "horizontal"
		if id == "pane.split.down" {
			direction = "vertical"
		}
		return h.call("session.new_split", map[string]any{
			"session_id": sid, "direction": direction, "side": "after", "layout": shellLayout(),
		})
	case "pane.close":
		sid, err := need()
		if err != nil {
			return err
		}
		return h.call("block.close", map[string]any{"session_id": sid})
	case "pane.focus.left", "pane.focus.right", "pane.focus.up", "pane.focus.down":
		sid, err := need()
		if err != nil {
			return err
		}
		return h.call("session.focus_direction", map[string]any{
			"session_id": sid, "direction": strings.TrimPrefix(id, "pane.focus."),
		})
	case "pane.zoom":
		sid, err := need()
		if err != nil {
			return err
		}
		return h.call("session.zoom_block", map[string]any{"session_id": sid})
	case "pane.swap":
		sid, err := need()
		if err != nil {
			return err
		}
		block, err := str("block_id")
		if err != nil {
			return err
		}
		other, err := str("other_block_id")
		if err != nil {
			return err
		}
		return h.call("session.swap_blocks", map[string]any{
			"session_id": sid, "block_id": block, "other_block_id": other,
		})
	default:
		return fmt.Errorf("not supported by the gateway: %s", id)
	}
}

// call runs a gateway method with a payload.
func (h *actionHost) call(method string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ar, aerr := decodeArgs(raw)
	if aerr != nil {
		return errors.New(aerr.Message)
	}
	fn := actionMethod(method)
	if fn == nil {
		return fmt.Errorf("%s is not supported by the gateway", method)
	}
	if _, aerr := fn(h.c, ar); aerr != nil {
		return errors.New(aerr.Message)
	}
	return nil
}

// actionMethod returns the handler of a method that a configured action
// may run. The table is spelled out so that this file does not depend on
// the methods table, which names the action handler.
func actionMethod(name string) func(*control, args) (any, *apiError) {
	switch name {
	case "session.new_window":
		return methodSessionNewWindow
	case "session.close_window":
		return methodSessionCloseWindow
	case "session.set_window_label":
		return methodSessionSetWindowLabel
	case "session.new_split":
		return methodSessionNewSplit
	case "block.close":
		return methodBlockClose
	case "session.focus_direction":
		return methodSessionFocusDirection
	case "session.zoom_block":
		return methodSessionZoomBlock
	case "session.swap_blocks":
		return methodSessionSwapBlocks
	}
	return nil
}

// shellLayout is a new window or split's layout: one shell block.
func shellLayout() map[string]any {
	return map[string]any{"block": map[string]any{"flavor": flavorShell}}
}

// mergeKeymapModes adds the configuration's modes to the ones the
// bindings made, keeping exclusivity.
func mergeKeymapModes(modes, extra []keymapMode) []keymapMode {
	byName := make(map[string]int, len(modes))
	for i, m := range modes {
		byName[m.Name] = i
	}
	for _, m := range extra {
		if i, ok := byName[m.Name]; ok {
			modes[i].Exclusive = modes[i].Exclusive || m.Exclusive
			continue
		}
		byName[m.Name] = len(modes)
		modes = append(modes, m)
	}
	return modes
}
