package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// readOnly are herdr's methods that GoRex has no part of: dividers,
// equalizing, moving panes, workspaces and worktrees. rootshell shows
// their error, or logs it, and goes on.
var readOnly = map[string]bool{
	"pane.resize": true, "pane.move": true, "pane.swap": true, "pane.rename": true, "pane.scroll": true,
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

// later is the error of a request answered later, once the app made the
// change it asks for (finish).
var later = errors.New("answered later")

// serve serves a line of rootshell, and reports whether it asked to close.
func (b *bridge) serve(line []byte) bool {
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
		return false
	}
	result, err := b.do(req)
	// Typing goes unanswered, as rootshell waits for no answer.
	if err == later || err == nil && req.Method == "terminal.input" && strings.HasPrefix(string(req.ID), `"i`) {
		return false
	}
	b.answer(req, result, err)
	return req.Method == "control.close"
}

func (b *bridge) answer(req request, result any, err error) {
	if err != nil {
		f, ok := err.(*failure)
		if !ok {
			f = &failure{"failed", err.Error()}
		}
		b.send(map[string]any{"id": req.ID, "error": f})
		return
	}
	if result == nil {
		result = struct{}{}
	}
	b.send(map[string]any{"id": req.ID, "result": result})
}

// params are the parameters of every method.
type params struct {
	TabID    string `json:"tab_id"`
	PaneID   string `json:"pane_id"`
	Pane     string `json:"target_pane_id"`
	AttachID string `json:"attach_id"`
	Target   string `json:"target"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Claim    bool   `json:"claim"`
	History  int    `json:"history_limit_bytes"`
	Bytes    []byte `json:"bytes"`
	Cwd      string `json:"cwd"`
	Dir      string `json:"direction"`
	Label    string `json:"label"`
	Mode     string `json:"mode"`
	Index    int    `json:"insert_index"`
}

func (b *bridge) do(req request) (any, error) {
	if readOnly[req.Method] {
		return nil, &failure{"read_only", "GoRex does not do this from rootshell"}
	}
	var p params
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &failure{"invalid_params", err.Error()}
		}
	}
	switch req.Method {
	case "ping", "events.subscribe", "tab.focus", "control.close":
		return nil, nil
	case "pane.focus":
		// Rootshell's focus, not the desktop's, is what it shows, and the
		// tab of it the one a new tab goes after.
		sid := sidOf(p.PaneID)
		if t := b.tabOf(sid); t != nil {
			b.picked[t.id], b.anchor = sid, t.id
			if !t.zoom {
				t.focus = sid
			}
		}
		return nil, nil
	case "session.snapshot":
		b.tellQuietly()
		return map[string]any{"snapshot": b.snapshot()}, nil
	case "tab.list":
		return map[string]any{"tabs": b.tabInfos()}, nil
	case "layout.export":
		t := b.tab(p.TabID)
		if t == nil {
			return nil, &failure{"tab_not_found", "no tab " + p.TabID}
		}
		return map[string]any{"layout": map[string]any{"workspace_id": workspace, "tab_id": t.id, "root": exportNode(t.root)}}, nil
	case "tab.set_geometry":
		t := b.tab(p.TabID)
		if t == nil {
			return nil, &failure{"tab_not_found", "no tab " + p.TabID}
		}
		if p.Cols < 1 || p.Rows < 1 {
			return nil, &failure{"invalid_params", "cols and rows must be positive"}
		}
		// Rootshell sizes a tab it holds, claiming it as the user goes to
		// it; another's sizes it only notes, a viewer. It hears of the new
		// layout first, and holds the output until its panes are at it.
		b.sizes[t.id] = [2]int{p.Cols, p.Rows}
		if p.Claim {
			b.claim(t)
			b.anchor = t.id
		}
		if b.owned[t.id] {
			b.tell()
			b.lockTab(t)
		}
		b.tell()
		return nil, nil
	case "tab.create", "pane.split", "pane.close", "tab.close", "tab.rename", "pane.zoom", "tab.move":
		return nil, b.ask(req, p)
	case "terminal.attach":
		sid := sidOf(p.Target)
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

// tabInfo is t as herdr's TabInfo.
func (b *bridge) tabInfo(t *tab) map[string]any {
	return map[string]any{
		"tab_id": t.id, "workspace_id": workspace, "number": slices.Index(b.tabs, t) + 1, "label": t.name,
		"focused": t.id == b.active, "pane_count": len(t.root.sids()), "agent_status": "unknown",
	}
}

func (b *bridge) tabInfos() []any {
	tabs := []any{}
	for _, t := range b.tabs {
		tabs = append(tabs, b.tabInfo(t))
	}
	return tabs
}

// paneInfo is the pane of session sid, in t, as herdr's PaneInfo.
func (b *bridge) paneInfo(t *tab, sid string) map[string]any {
	in := b.infos[sid]
	pane := map[string]any{
		"pane_id": paneID(sid), "terminal_id": sid, "workspace_id": workspace, "tab_id": t.id,
		"focused": t.id == b.active && sid == t.focus, "agent_status": "unknown",
		"terminal_title": in.Title, "title": in.Title,
		"foreground_process": map[string]any{"name": in.Program, "argv": in.Args, "is_shell": in.Idle},
	}
	if strings.HasPrefix(in.Dir, "/") {
		pane["cwd"], pane["foreground_cwd"] = in.Dir, in.Dir
	}
	return pane
}

// snapshot is the session as herdr's session.snapshot has it: this
// machine is its one workspace.
func (b *bridge) snapshot() map[string]any {
	panes, layouts := []any{}, []any{}
	focusedPane := ""
	for _, t := range b.tabs {
		for _, sid := range t.root.sids() {
			panes = append(panes, b.paneInfo(t, sid))
		}
		if t.id == b.active {
			focusedPane = paneID(t.focus)
		}
		layouts = append(layouts, b.layoutOf(t))
	}
	return map[string]any{
		"version": version, "protocol": 1,
		"focused_workspace_id": workspace, "focused_tab_id": b.active, "focused_pane_id": focusedPane,
		"workspaces": []any{map[string]any{
			"workspace_id": workspace, "label": b.hello.Host.Name, "number": 1, "focused": true,
			"active_tab_id": b.active, "agent_status": "unknown", "tab_count": len(b.tabs), "pane_count": len(panes),
		}},
		"tabs": b.tabInfos(), "panes": panes, "layouts": layouts, "agents": []any{},
	}
}
