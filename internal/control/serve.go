package control

import (
	"encoding/json"
	"fmt"
	"strings"
)

// readOnly are herdr's methods that change the layout, which is the
// desktop's: rootshell shows their error, and goes on.
var readOnly = map[string]bool{
	"pane.split": true, "pane.close": true, "pane.resize": true, "pane.zoom": true, "pane.move": true,
	"pane.swap": true, "pane.rename": true, "pane.scroll": true,
	"tab.create": true, "tab.close": true, "tab.rename": true, "tab.move": true, "tab.list": true,
	"workspace.create": true, "workspace.close": true, "workspace.rename": true, "workspace.move": true,
	"workspace.move_block": true, "workspace.focus": true, "workspace.list": true,
	"worktree.list": true, "worktree.create": true, "worktree.open": true, "worktree.remove": true,
	"layout.set_split_ratio": true,
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// failure is a request's error: rootshell needs both strings.
type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *failure) Error() string { return f.Message }

// serve serves a line of rootshell, and reports whether it asked to close.
func (b *bridge) serve(line []byte) bool {
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
		return false
	}
	result, err := b.do(req)
	// Typing goes unanswered, as rootshell waits for no answer.
	if err == nil && req.Method == "terminal.input" && strings.HasPrefix(string(req.ID), `"i`) {
		return false
	}
	if err != nil {
		f, ok := err.(*failure)
		if !ok {
			f = &failure{"failed", err.Error()}
		}
		b.send(map[string]any{"id": req.ID, "error": f})
	} else {
		if result == nil {
			result = struct{}{}
		}
		b.send(map[string]any{"id": req.ID, "result": result})
	}
	return req.Method == "control.close"
}

func (b *bridge) do(req request) (any, error) {
	if readOnly[req.Method] {
		return nil, &failure{"read_only", "GoRex layouts are read-only from rootshell"}
	}
	var p struct {
		TabID    string `json:"tab_id"`
		AttachID string `json:"attach_id"`
		Target   string `json:"target"`
		Cols     int    `json:"cols"`
		Rows     int    `json:"rows"`
		Claim    bool   `json:"claim"`
		History  int    `json:"history_limit_bytes"`
		Bytes    []byte `json:"bytes"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &failure{"invalid_params", err.Error()}
		}
	}
	switch req.Method {
	case "ping", "events.subscribe", "pane.focus", "tab.focus", "control.close":
		return nil, nil
	case "session.snapshot":
		b.tellQuietly()
		return map[string]any{"snapshot": b.snapshot()}, nil
	case "layout.export":
		t := b.tab(p.TabID)
		if t == nil {
			return nil, &failure{"tab_not_found", "no tab " + p.TabID}
		}
		return map[string]any{"layout": map[string]any{"workspace_id": "w", "tab_id": t.id, "root": exportNode(t.root)}}, nil
	case "tab.set_geometry":
		t := b.tab(p.TabID)
		if t == nil {
			return nil, &failure{"tab_not_found", "no tab " + p.TabID}
		}
		if p.Cols < 1 || p.Rows < 1 {
			return nil, &failure{"invalid_params", "cols and rows must be positive"}
		}
		// Rootshell sizes a tab it holds, claiming it as the user goes to
		// it; another's sizes it only notes, a viewer.
		b.sizes[t.id] = [2]int{p.Cols, p.Rows}
		if p.Claim || b.owned[t.id] {
			if b.lockTab(t, p.Claim) && p.Claim {
				b.owned[t.id] = true
			}
		}
		b.tell()
		return nil, nil
	case "terminal.attach":
		sid := strings.TrimPrefix(p.Target, "p-")
		if _, ok := b.infos[sid]; !ok {
			return nil, &failure{"terminal_not_found", "no terminal " + p.Target}
		}
		if p.History <= 0 {
			p.History = 1 << 20
		}
		b.nextAttach++
		at := &attach{id: fmt.Sprintf("a%d", b.nextAttach), sid: sid, limit: p.History, b: b}
		b.attaches[at.id] = at
		at.reattach(b.infos[sid])
		return map[string]any{"attach_id": at.id, "terminal_id": sid, "pane_id": paneID(sid)}, nil
	case "terminal.snapshot", "terminal.input", "terminal.detach":
		at := b.attaches[p.AttachID]
		if at == nil {
			return nil, &failure{"attach_not_found", "no attach " + p.AttachID}
		}
		switch req.Method {
		case "terminal.snapshot":
			at.reattach(b.infos[at.sid])
		case "terminal.input":
			at.input(p.Bytes)
		case "terminal.detach":
			at.detach()
			delete(b.attaches, at.id)
		}
		return nil, nil
	}
	return nil, &failure{"unknown_method", "GoRex does not serve " + req.Method}
}

// snapshot is the session as herdr's session.snapshot has it: this
// machine is its one workspace.
func (b *bridge) snapshot() map[string]any {
	tabs, panes, layouts := []any{}, []any{}, []any{}
	focusedPane := ""
	for i, t := range b.tabs {
		sids := t.root.sids()
		active := t.id == b.active
		tabs = append(tabs, map[string]any{
			"tab_id": t.id, "workspace_id": "w", "number": i + 1, "label": t.name,
			"focused": active, "pane_count": len(sids), "agent_status": "unknown",
		})
		for _, sid := range sids {
			in := b.infos[sid]
			pane := map[string]any{
				"pane_id": paneID(sid), "terminal_id": sid, "workspace_id": "w", "tab_id": t.id,
				"focused": active && sid == t.focus, "agent_status": "unknown",
				"terminal_title": in.Title, "title": in.Title,
				"foreground_process": map[string]any{"name": in.Program, "argv": in.Args, "is_shell": in.Idle},
			}
			if strings.HasPrefix(in.Dir, "/") {
				pane["cwd"], pane["foreground_cwd"] = in.Dir, in.Dir
			}
			panes = append(panes, pane)
		}
		if active {
			focusedPane = paneID(t.focus)
		}
		layouts = append(layouts, b.layoutOf(t))
	}
	return map[string]any{
		"version": version, "protocol": 1,
		"focused_workspace_id": "w", "focused_tab_id": b.active, "focused_pane_id": focusedPane,
		"workspaces": []any{map[string]any{
			"workspace_id": "w", "label": b.hello.Host.Name, "number": 1, "focused": true,
			"active_tab_id": b.active, "agent_status": "unknown", "tab_count": len(b.tabs), "pane_count": len(panes),
		}},
		"tabs": tabs, "panes": panes, "layouts": layouts, "agents": []any{},
	}
}
