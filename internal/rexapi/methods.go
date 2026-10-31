package rexapi

import (
	"encoding/json"
	"math"
	"os"
	"sort"

	"gorex/internal/rex"
)

// method is one method of Rex's API.
type method struct {
	name    string
	desc    string
	in      map[string]any
	out     map[string]any
	ordered bool
	handler func(*control, json.RawMessage) (any, *apiError)
}

// methods is every method this server provides, by name.
var methods = map[string]*method{}

// register adds a method to the table.
func register(m *method) { methods[m.name] = m }

// props builds a JSON Schema object.
func props(required []string, p map[string]any) map[string]any {
	o := map[string]any{"type": "object", "properties": p}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

// str, num, boolean, integer and array are JSON Schema fields.
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func array(desc, items string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": items}}
}

func init() {
	register(&method{
		name: "server.whoami", desc: "Get how the server identified you, and the account the server runs as.",
		in:  props(nil, nil),
		out: props(nil, map[string]any{"principal": map[string]any{"type": "object"}, "account": str("The account the server runs as.")}),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return map[string]any{"principal": c.principal(), "account": c.s.user}, nil
		},
	})
	register(&method{
		name: "server.status", desc: "Get the server's identity, session count, configuration, and Tailscale status.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return c.s.status(), nil
		},
	})
	register(&method{
		name: "server.version", desc: "Get the version of Rex that the server is running and how it was built.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			version := c.s.opt.Version
			if version == "" {
				version = "gorex"
			}
			out := map[string]any{
				"rex_version":      version,
				"platform":         rexPlatform(),
				"dirty":            false,
				"api_revision":     APIRevision,
				"api_min_revision": 0,
			}
			// The fields a client parses as a time are left out when the
			// build does not know them.
			if c.s.opt.Commit != "" {
				out["commit"] = c.s.opt.Commit
			}
			if c.s.opt.BuiltAt != "" {
				out["built_at"] = c.s.opt.BuiltAt
			}
			return out, nil
		},
	})
	register(&method{
		name: "server.config_reload", desc: "Reload the configuration file.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return nil, errNotSupported("GoRex reads its configuration itself")
		},
	})
	register(&method{
		name: "session.list", desc: "List the sessions on the server.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			m := c.s.current()
			out := make([]map[string]any, 0, len(m.sessions))
			for _, s := range m.sessions {
				out = append(out, map[string]any{"session_id": s.ID, "label": s.Label})
			}
			return map[string]any{"sessions": out}, nil
		},
	})
	register(&method{
		name: "session.attach", desc: "Attach to a session: start receiving its events and get its current view.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to attach to.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			m := c.s.current()
			s := m.session[p.SessionID]
			if s == nil {
				return nil, errSessionNotFound("no session has the id %q", p.SessionID)
			}
			c.mu.Lock()
			fresh := !c.attached[s.ID]
			c.attached[s.ID] = true
			c.mu.Unlock()
			if fresh {
				c.s.notifyClient(c, s.ID, "client_connected")
			}
			return map[string]any{"client_id": c.id, "view": m.viewOf(s, c.s.hostID)}, nil
		},
	})
	register(&method{
		name: "session.attach_all", desc: "Start receiving events from the whole server and get the list of sessions.",
		in: props([]string{"event_scope"}, map[string]any{"event_scope": str("What events to receive: session or all.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				EventScope string `json:"event_scope"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			c.mu.Lock()
			c.all = true
			c.scope = p.EventScope
			c.mu.Unlock()
			return map[string]any{"sessions": sessionList(c.s.current())}, nil
		},
	})
	register(&method{
		name: "session.bootstrap", desc: "Set up a new connection in one call: receive events, list sessions, and attach.",
		in: props([]string{"event_scope"}, map[string]any{
			"event_scope":          str("What events to receive: session or all."),
			"preferred_session_id": str("The session to select."),
			"attach_session_ids":   array("The sessions to attach to.", "string"),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				EventScope       string   `json:"event_scope"`
				PreferredSession string   `json:"preferred_session_id"`
				AttachSessionIDs []string `json:"attach_session_ids"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			m := c.s.current()
			c.mu.Lock()
			c.all = true
			c.scope = p.EventScope
			for _, sid := range p.AttachSessionIDs {
				if m.session[sid] != nil {
					c.attached[sid] = true
				}
			}
			c.mu.Unlock()
			selected := ""
			if m.session[p.PreferredSession] != nil {
				selected = p.PreferredSession
			} else if len(m.sessions) > 0 {
				selected = m.sessions[0].ID
			}
			attached := make([]string, 0, len(p.AttachSessionIDs))
			for _, sid := range p.AttachSessionIDs {
				if m.session[sid] != nil {
					attached = append(attached, sid)
				}
			}
			return map[string]any{
				"client_id":         c.id,
				"server_status":     c.s.status(),
				"attach_all":        true,
				"selected_session":  selected,
				"attached_sessions": attached,
			}, nil
		},
	})
	register(&method{
		name: "session.detach", desc: "Detach the connection from a session.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to detach.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			c.mu.Lock()
			was := c.attached[p.SessionID]
			delete(c.attached, p.SessionID)
			c.mu.Unlock()
			if was {
				c.s.notifyClient(c, p.SessionID, "client_disconnected")
			}
			return nil, nil
		},
	})
	register(&method{
		name: "session.view", desc: "Get the session's windows, their layouts, and their blocks in one snapshot.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			s, apiErr := c.sessionArg(raw)
			if apiErr != nil {
				return nil, apiErr
			}
			return c.s.current().viewOf(s, c.s.hostID), nil
		},
	})
	register(&method{
		name: "session.list_blocks", desc: "List every block in the session, including detached blocks.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			s, apiErr := c.sessionArg(raw)
			if apiErr != nil {
				return nil, apiErr
			}
			return map[string]any{"blocks": c.s.current().listBlocks(s, c.s.hostID)}, nil
		},
	})
	register(&method{
		name: "session.list_clients", desc: "List the clients attached to the session.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			s, apiErr := c.sessionArg(raw)
			if apiErr != nil {
				return nil, apiErr
			}
			c.s.mu.Lock()
			conns := make([]*control, 0, len(c.s.clients))
			for _, o := range c.s.clients {
				conns = append(conns, o)
			}
			c.s.mu.Unlock()
			out := []map[string]any{}
			for _, o := range conns {
				if o.wants(s.ID) {
					out = append(out, clientInfo(o.state, o.attachedList()))
				}
			}
			sort.Slice(out, func(i, j int) bool {
				return out[i]["client_id"].(string) < out[j]["client_id"].(string)
			})
			return map[string]any{"clients": out}, nil
		},
	})
	register(&method{
		name: "session.describe_block", desc: "Get a block's details and the methods, events, and actions it provides.",
		in: props([]string{"session_id", "block_id"}, map[string]any{
			"session_id": str("The session that holds the block."),
			"block_id":   str("The block to describe."),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s := c.s.current().session[p.SessionID]
			if s == nil {
				return nil, errSessionNotFound("no session has the id %q", p.SessionID)
			}
			b := s.findBlock(p.BlockID)
			if b == nil {
				return nil, errNotFound("no block %q in the session", p.BlockID)
			}
			info := blockInfo{
				BlockID: b.ID, Label: b.Label, CreatorName: CreatorName, Flavor: FlavorShell,
				HostID: c.s.hostID, Placement: b.Placement, WindowID: b.WindowID,
			}
			return map[string]any{"block": info, "api": terminalAPI()}, nil
		},
	})
	register(&method{
		name: "session.help", desc: "Describe the methods this session accepts and the methods its blocks provide.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on.")}),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return map[string]any{"methods": helpMethods()}, nil
		},
	})
	register(&method{
		name: "session.create", desc: "Create a session, optionally with its first windows already in it.",
		in: props(nil, map[string]any{
			"label":           str("The session's label."),
			"initial_windows": array("Windows to create with the session.", "object"),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				Label          string      `json:"label"`
				InitialWindows []windowDef `json:"initial_windows"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			return c.createSession(p.Label, p.InitialWindows)
		},
	})
	register(&method{
		name: "session.new_window", desc: "Create a window and the blocks in its layout.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id":   str("The session to act on."),
			"window_label": str("The window's label."),
			"layout":       map[string]any{"type": "object", "description": "The blocks to create and how to arrange them."},
			"focus":        boolean("Whether the window takes focus."),
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID   string          `json:"session_id"`
				WindowLabel string          `json:"window_label"`
				Layout      json.RawMessage `json:"layout"`
				Focus       bool            `json:"focus"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			l, apiErr := parseLayout(p.Layout)
			if apiErr != nil {
				return nil, apiErr
			}
			wid, bids, apiErr := c.createWindow(s, p.WindowLabel, l, p.Focus)
			if apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"window_id": wid, "block_ids": bids, "revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.new_split", desc: "Create blocks in a new split beside an existing block or split.",
		in: props([]string{"session_id", "direction", "side"}, map[string]any{
			"session_id": str("The session to act on."),
			"anchor_id":  str("The block or split to split beside."),
			"window_id":  str("The window whose focused block to split."),
			"direction":  str("The direction: horizontal or vertical."),
			"side":       str("Which side the new blocks go: before or after."),
			"ratio":      map[string]any{"type": "number", "description": "The share the new side gets."},
			"layout":     map[string]any{"type": "object", "description": "The blocks to create."},
			"focus":      boolean("Whether the new block takes focus."),
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string          `json:"session_id"`
				AnchorID  string          `json:"anchor_id"`
				WindowID  string          `json:"window_id"`
				Direction string          `json:"direction"`
				Side      string          `json:"side"`
				Ratio     float64         `json:"ratio"`
				Layout    json.RawMessage `json:"layout"`
				Focus     bool            `json:"focus"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			if p.Direction != "horizontal" && p.Direction != "vertical" {
				return nil, errBadRequest("a split's direction is horizontal or vertical")
			}
			if p.Side == "before" {
				return nil, errNotSupported("GoRex splits a block on its after side only")
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			anchor := s.findBlock(p.AnchorID)
			if anchor == nil && p.WindowID != "" {
				w := s.window(p.WindowID)
				if w == nil || w.Focused == "" {
					return nil, errNotFound("no window %q in the session", p.WindowID)
				}
				anchor = s.findBlock(w.Focused)
			}
			if anchor == nil || anchor.Placement != "placed" {
				return nil, errNotFound("no placed block %q in the session", p.AnchorID)
			}
			l, apiErr := parseLayout(p.Layout)
			if apiErr != nil {
				return nil, apiErr
			}
			leaves := l.leaves()
			if len(leaves) == 0 {
				return nil, errBadRequest("the layout has no blocks")
			}
			vertical := p.Direction == "vertical"
			blockIDs := make([]string, 0, len(leaves))
			prev := anchor.SID
			for _, leaf := range leaves {
				res, apiErr := c.command(s, rex.LayoutCommand{
					Op:       "pane.split",
					SID:      prev,
					Dir:      leaf.cwd(),
					Command:  leaf.command(),
					Vertical: vertical,
					Name:     leaf.Label,
				})
				if apiErr != nil {
					return nil, apiErr
				}
				prev = res.New
				blockIDs = append(blockIDs, blockID(res.New))
			}
			c.s.changed()
			return map[string]any{
				"anchor_id": anchor.ID,
				"block_ids": blockIDs,
				"split_ids": []string{},
				"revision":  c.s.current().rev,
			}, nil
		},
	})
	register(&method{
		name: "session.zoom_block", desc: "Zoom a block to fill its window, or undo the zoom.",
		in: props(nil, map[string]any{
			"session_id": str("The session to act on."),
			"block_id":   str("The block to zoom."),
			"window_id":  str("A window whose focused block to zoom."),
			"zoom":       boolean("Set true to zoom, false to restore."),
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
				WindowID  string `json:"window_id"`
				Zoom      *bool  `json:"zoom"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, p.WindowID)
			if apiErr != nil {
				return nil, apiErr
			}
			mode := "toggle"
			want := !isZoomed(s, b)
			if p.Zoom != nil {
				want = *p.Zoom
				if want {
					mode = "on"
				} else {
					mode = "off"
				}
			}
			res, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.zoom", SID: b.SID, Mode: mode})
			if apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"block_id": b.ID, "zoomed": res.Zoomed, "revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.set_label", desc: "Set the session's label.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session to act on."),
			"label":      str("The new label."),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				Label     string `json:"label"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "space.rename", Name: p.Label}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.set_window_label", desc: "Set a window's label.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session to act on."),
			"window_id":  str("The window to rename."),
			"label":      str("The new label."),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				WindowID  string `json:"window_id"`
				Label     string `json:"label"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			w := s.window(p.WindowID)
			if w == nil || len(w.Blocks) == 0 {
				return nil, errNotFound("no window %q in the session", p.WindowID)
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "tab.rename", SID: w.Blocks[0].SID, Name: p.Label}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.set_block_label", desc: "Set a block's label.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session to act on."),
			"block_id":   str("The block to rename."),
			"label":      str("The new label."),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
				Label     string `json:"label"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.rename", SID: b.SID, Name: p.Label}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.close_window", desc: "Close a window and every block in it.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session to act on."),
			"window_id":  str("The window to close."),
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				WindowID  string `json:"window_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			w := s.window(p.WindowID)
			if w == nil || len(w.Blocks) == 0 {
				return nil, errNotFound("no window %q in the session", p.WindowID)
			}
			if len(s.Windows) == 1 {
				return nil, errConflict("GoRex closes a space's last window only by closing its space")
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "tab.close", SID: w.Blocks[0].SID}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "block.close", desc: "Close a block and end the program running in it.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session that holds the block."),
			"block_id":   str("The block to close."),
			"window_id":  str("The window whose focused block to close."),
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
				WindowID  string `json:"window_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, p.WindowID)
			if apiErr != nil {
				return nil, apiErr
			}
			if b.Placement == "detached" {
				if err := c.s.conn().Kill(b.SID); err != nil {
					return nil, errInternal("%v", err)
				}
			} else if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.close", SID: b.SID}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"block_id": b.ID, "revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.destroy", desc: "Destroy a session and close every block in it.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to destroy.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			s, apiErr := c.sessionArg(raw)
			if apiErr != nil {
				return nil, apiErr
			}
			if s.Workspace == "" {
				return nil, errNotSupported("GoRex cannot destroy the session of detached blocks")
			}
			for _, b := range s.Blocks {
				if b.Placement == "detached" {
					if err := c.s.conn().Kill(b.SID); err != nil {
						return nil, errInternal("%v", err)
					}
				}
			}
			// Closing the space ends its windows and their programs, as
			// Rex's session.destroy does.
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "space.close"}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{}, nil
		},
	})
	register(&method{
		name: "session.focus_block", desc: "Focus a block, and make its window the active window.",
		in:      props([]string{"session_id", "block_id"}, map[string]any{"session_id": str("The session to act on."), "block_id": str("The block to focus.")}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			if b.Placement != "placed" {
				return nil, errConflict("the block is not in a window")
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.focus", SID: b.SID}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.focus_window", desc: "Make a window the active window.",
		in:      props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on."), "window_id": str("The window to focus.")}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				WindowID  string `json:"window_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			w := s.window(p.WindowID)
			if w == nil || w.Focused == "" {
				return nil, errNotFound("no window %q in the session", p.WindowID)
			}
			b := s.findBlock(w.Focused)
			if b == nil {
				return nil, errNotFound("the window has no block")
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.focus", SID: b.SID}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.move_block", desc: "Move a block, or place a detached one, in a new split beside another block.",
		in: props([]string{"session_id", "block_id", "anchor_block_id"}, map[string]any{
			"session_id":      str("The session to act on."),
			"block_id":        str("The block to move."),
			"anchor_block_id": str("The block to place it beside."),
			"direction":       str("The direction: horizontal or vertical."),
			"side":            str("Which side it goes: before or after."),
			"ratio":           map[string]any{"type": "number", "description": "The share the moved block gets."},
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string  `json:"session_id"`
				BlockID   string  `json:"block_id"`
				AnchorID  string  `json:"anchor_block_id"`
				Direction string  `json:"direction"`
				Side      string  `json:"side"`
				Ratio     float64 `json:"ratio"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			if p.Direction != "horizontal" && p.Direction != "vertical" {
				return nil, errBadRequest("a move's direction is horizontal or vertical")
			}
			if p.Side == "before" {
				return nil, errNotSupported("GoRex moves a block to its target's after side only")
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			if b.Placement == "detached" {
				return nil, errNotSupported("GoRex cannot place a session no tab holds")
			}
			a, apiErr := c.blockIn(s, p.AnchorID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			ratio := p.Ratio
			if ratio <= 0 || ratio > 1 {
				ratio = 0.5
			}
			dir := "right"
			if p.Direction == "vertical" {
				if p.Side == "before" {
					dir = "up"
				} else {
					dir = "down"
				}
			} else if p.Side == "before" {
				dir = "left"
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.move", SID: b.SID, Target: a.SID, Direction: dir, Ratio: float32(ratio)}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.swap_blocks", desc: "Swap the places of two blocks.",
		in:      props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on."), "block_id": str("The first block."), "other_block_id": str("The other block.")}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
				OtherID   string `json:"other_block_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			o, apiErr := c.blockIn(s, p.OtherID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.swap", SID: b.SID, Target: o.SID}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	for _, m := range []struct {
		name   string
		desc   string
		next   bool
		window bool
	}{
		{"session.focus_next_pane", "Move focus to the next block in the active window.", true, false},
		{"session.focus_previous_pane", "Move focus to the previous block in the active window.", false, false},
		{"session.focus_next_window", "Make the next window the active window.", true, true},
		{"session.focus_previous_window", "Make the previous window the active window.", false, true},
	} {
		f := m
		register(&method{
			name: f.name, desc: f.desc,
			in: props([]string{"session_id"}, map[string]any{
				"session_id": str("The session to act on."),
				"wrap":       boolean("Whether to wrap around at the end."),
			}),
			ordered: true,
			handler: func(c *control, raw json.RawMessage) (any, *apiError) {
				var p struct {
					SessionID string `json:"session_id"`
					Wrap      bool   `json:"wrap"`
				}
				if apiErr := decode(raw, &p); apiErr != nil {
					return nil, apiErr
				}
				s, apiErr := c.sessionFor(p.SessionID)
				if apiErr != nil {
					return nil, apiErr
				}
				var target *mWindow
				if f.window {
					target = neighborWindow(s, f.next, p.Wrap)
				} else {
					b := neighborBlock(s, f.next, p.Wrap)
					if b != nil {
						target = s.window(b.WindowID)
					}
					if target == nil {
						return map[string]any{"revision": c.s.current().rev}, nil
					}
					if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.focus", SID: b.SID}); apiErr != nil {
						return nil, apiErr
					}
					c.s.changed()
					return map[string]any{"revision": c.s.current().rev}, nil
				}
				if target == nil {
					return map[string]any{"revision": c.s.current().rev}, nil
				}
				if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.focus", SID: target.block(target.Focused).SID}); apiErr != nil {
					return nil, apiErr
				}
				c.s.changed()
				return map[string]any{"revision": c.s.current().rev}, nil
			},
		})
	}
	register(&method{
		name: "session.focus_direction", desc: "Move focus to the nearest block to the left, right, above, or below.",
		in: props([]string{"session_id", "direction"}, map[string]any{
			"session_id": str("The session to act on."),
			"direction":  str("The direction: left, right, up or down."),
			"wrap":       boolean("Whether to wrap around at the end."),
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				Direction string `json:"direction"`
				Wrap      bool   `json:"wrap"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			switch p.Direction {
			case "left", "right", "up", "down":
			default:
				return nil, errBadRequest("a direction is left, right, up or down")
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b := neighborDirection(s, p.Direction, p.Wrap)
			if b == nil {
				return map[string]any{"revision": c.s.current().rev}, nil
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "pane.focus", SID: b.SID}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "session.move_window", desc: "Move a window to another place in the session's window order.",
		in: props([]string{"session_id"}, map[string]any{"session_id": str("The session to act on."), "window_id": str("The window to move."), "before_window_id": str("The window to go before.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				WindowID  string `json:"window_id"`
				BeforeID  string `json:"before_window_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			w := s.window(p.WindowID)
			if w == nil || len(w.Blocks) == 0 {
				return nil, errNotFound("no window %q in the session", p.WindowID)
			}
			index := len(s.Windows)
			if before := s.window(p.BeforeID); before != nil {
				index = s.ordinal(before) - 1
			}
			if _, apiErr := c.command(s, rex.LayoutCommand{Op: "tab.move", SID: w.Blocks[0].SID, Index: index}); apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"revision": c.s.current().rev}, nil
		},
	})
	register(&method{
		name: "client.list", desc: "List the clients connected to the server, in the order they connected.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			c.s.mu.Lock()
			conns := make([]*control, 0, len(c.s.clients))
			for _, o := range c.s.clients {
				conns = append(conns, o)
			}
			c.s.mu.Unlock()
			out := make([]map[string]any, 0, len(conns))
			for _, o := range conns {
				out = append(out, clientInfo(o.state, o.attachedList()))
			}
			sort.Slice(out, func(i, j int) bool { return out[i]["client_id"].(string) < out[j]["client_id"].(string) })
			return map[string]any{"clients": out}, nil
		},
	})
	register(&method{
		name: "client.inspect", desc: "Get one connected client's details, including the actions it offers.",
		in: props([]string{"client_id"}, map[string]any{"client_id": str("The client to inspect.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				ClientID string `json:"client_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			c.s.mu.Lock()
			o := c.s.clients[p.ClientID]
			c.s.mu.Unlock()
			if o == nil {
				return nil, errNotFound("no client %q", p.ClientID)
			}
			return map[string]any{"client": clientInfo(o.state, o.attachedList()), "actions": []any{}}, nil
		},
	})
	register(&method{
		name: "client.set_label", desc: "Set or remove this client's label.",
		in: props([]string{"label"}, map[string]any{"label": str("The label, or empty to remove it.")}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				Label string `json:"label"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			c.state.setLabel(p.Label)
			return nil, nil
		},
	})
	register(&method{
		name: "client.keymap", desc: "Get what each key does for this client.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return map[string]any{"bindings": []any{}, "diagnostics": []any{}, "actions": []any{}, "config": map[string]any{}}, nil
		},
	})
	register(&method{
		name: "client.action", desc: "Perform an action from the configuration file or a block, as this client.",
		in: props([]string{"action"}, map[string]any{"action": str("The action to perform."), "args": map[string]any{"type": "object"}}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				Action    string          `json:"action"`
				Args      json.RawMessage `json:"args"`
				SessionID string          `json:"session_id"`
				BlockID   string          `json:"block_id"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s := c.s.current().sessions
			var target *mSession
			if p.SessionID != "" {
				target = c.s.current().session[p.SessionID]
			}
			if target == nil && len(s) > 0 {
				target = s[0]
			}
			_, apiErr := c.command(target, rex.LayoutCommand{Op: "action", Action: p.Action, Args: p.Args, SID: blockSID(c.s.current(), p.BlockID)})
			if apiErr != nil {
				return nil, apiErr
			}
			c.s.changed()
			return map[string]any{"result": nil}, nil
		},
	})
	register(&method{
		name: "block.method", desc: "Call a method a block provides.",
		in: props([]string{"session_id", "block_id", "method"}, map[string]any{
			"session_id": str("The session that holds the block."),
			"block_id":   str("The block."),
			"method":     str("The method's short name."),
			"args":       map[string]any{"type": "object", "description": "The method's arguments."},
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string          `json:"session_id"`
				BlockID   string          `json:"block_id"`
				Method    string          `json:"method"`
				Args      json.RawMessage `json:"args"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			return c.blockMethod(p.SessionID, p.BlockID, p.Method, p.Args)
		},
	})
	for _, name := range []string{"format", "write", "size", "title", "pwd", "process", "program_status", "reset", "clear", "resize"} {
		n := name
		register(&method{
			name: CreatorName + "." + n,
			desc: blockMethodDesc(n),
			in:   props([]string{"session_id", "block_id"}, map[string]any{"session_id": str("The session that holds the block."), "block_id": str("The block."), "args": map[string]any{"type": "object"}}),
			handler: func(c *control, raw json.RawMessage) (any, *apiError) {
				var p struct {
					SessionID string          `json:"session_id"`
					BlockID   string          `json:"block_id"`
					Args      json.RawMessage `json:"args"`
				}
				if apiErr := decode(raw, &p); apiErr != nil {
					return nil, apiErr
				}
				return c.blockMethod(p.SessionID, p.BlockID, n, p.Args)
			},
		})
	}
	register(&method{
		name: "block.event", desc: "Send an input event, such as a key press or a paste, to a block.",
		in: props([]string{"session_id", "block_id", "event"}, map[string]any{
			"session_id": str("The session that holds the block."),
			"block_id":   str("The block to send the event to."),
			"event":      map[string]any{"type": "object", "description": "The event: its type and payload."},
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
				Event     struct {
					Type    string          `json:"type"`
					Payload json.RawMessage `json:"payload"`
				} `json:"event"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			return nil, c.sendEvent(b, p.Event.Type, p.Event.Payload)
		},
	})
	register(&method{
		name: "client.key", desc: "Send a key to a block, as a terminal would.",
		in: props([]string{"session_id", "key"}, map[string]any{
			"session_id": str("The session that holds the block."),
			"block_id":   str("The block to type in."),
			"key":        map[string]any{"type": "object", "description": "The key event."},
		}),
		ordered: true,
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
				Key       struct {
					Key    string `json:"key"`
					Mods   string `json:"mods"`
					Text   string `json:"text"`
					Action string `json:"action"`
				} `json:"key"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			if p.Key.Action != "" && p.Key.Action != "press" {
				return map[string]any{"handled": true}, nil
			}
			data := keyBytes(p.Key.Key, p.Key.Mods, p.Key.Text)
			if len(data) > 0 {
				if err := c.s.conn().Input(b.SID, data); err != nil {
					return nil, errInternal("%v", err)
				}
			}
			return map[string]any{"handled": true}, nil
		},
	})
	register(&method{
		name: "data.connect", desc: "Get a one-time ticket for opening a data connection to a block.",
		in: props([]string{"session_id", "block_id", "data_ref_id"}, map[string]any{
			"session_id":  str("The session that holds the block."),
			"block_id":    str("The block to stream."),
			"data_ref_id": str("The data source, such as terminal."),
			"subprotocol": str("The data subprotocol, such as rex.data.terminal.v1."),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID   string `json:"session_id"`
				BlockID     string `json:"block_id"`
				DataRefID   string `json:"data_ref_id"`
				Subprotocol string `json:"subprotocol"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			b, apiErr := c.blockIn(s, p.BlockID, "")
			if apiErr != nil {
				return nil, apiErr
			}
			if p.Subprotocol == "" {
				p.Subprotocol = "rex.data.terminal.v1"
			}
			return c.s.ticket(s, b, p.Subprotocol)
		},
	})
	register(&method{
		name: "target.resolve", desc: "Look up sessions, windows, blocks, or clients by ID, label, or position.",
		in: props([]string{"kinds", "queries"}, map[string]any{
			"kinds":   array("What each query may name.", "string"),
			"queries": array("The names to look up.", "string"),
			"scope":   map[string]any{"type": "object"},
			"context": map[string]any{"type": "object"},
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			return c.resolve(raw)
		},
	})
	register(&method{
		name: "target.complete", desc: "List the labels, window positions, and short IDs that start with a prefix.",
		in: props([]string{"kinds", "prefix"}, map[string]any{
			"kinds":  array("What to complete.", "string"),
			"prefix": str("The prefix to complete."),
			"scope":  map[string]any{"type": "object"},
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			return c.complete(raw)
		},
	})
	register(&method{
		name: "block_creator.list", desc: "List the creators the server can make blocks with, and their flavors.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return map[string]any{"creators": []any{map[string]any{
				"name":        CreatorName,
				"title":       "Terminal",
				"description": "A terminal that runs a shell or any other command-line program.",
				"host_id":     c.s.hostID,
				"flavors": []any{map[string]any{
					"name":        "shell",
					"flavor":      FlavorShell,
					"title":       "Shell",
					"description": "A terminal that runs the user's shell, or a command you choose.",
					"options_schema": props(nil, map[string]any{
						"command": array("A program and its arguments to run instead of the shell.", "string"),
						"cwd":     str("The directory the program starts in."),
					}),
				}},
				"api": terminalAPI(),
			}}}, nil
		},
	})
	register(&method{
		name: "session.own_block", desc: "Claim blocks for the calling client.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session to act on."),
			"block_ids":  array("The blocks to claim.", "string"),
			"mode":       str("How to claim them."),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string   `json:"session_id"`
				BlockIDs  []string `json:"block_ids"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			out := []map[string]any{}
			for _, id := range p.BlockIDs {
				b := s.findBlock(id)
				if b == nil {
					return nil, errNotFound("no block %q in the session", id)
				}
				out = append(out, map[string]any{"block_id": b.ID, "owner": c.id})
			}
			return map[string]any{"blocks": out}, nil
		},
	})
	register(&method{
		name: "session.disown_block", desc: "Give up the calling client's claim on blocks.",
		in: props([]string{"session_id"}, map[string]any{
			"session_id": str("The session to act on."),
			"block_ids":  array("The blocks to give up.", "string"),
		}),
		handler: func(c *control, raw json.RawMessage) (any, *apiError) {
			var p struct {
				SessionID string   `json:"session_id"`
				BlockIDs  []string `json:"block_ids"`
			}
			if apiErr := decode(raw, &p); apiErr != nil {
				return nil, apiErr
			}
			s, apiErr := c.sessionFor(p.SessionID)
			if apiErr != nil {
				return nil, apiErr
			}
			out := []map[string]any{}
			for _, id := range p.BlockIDs {
				b := s.findBlock(id)
				if b == nil {
					return nil, errNotFound("no block %q in the session", id)
				}
				out = append(out, map[string]any{"block_id": b.ID})
			}
			return map[string]any{"blocks": out}, nil
		},
	})
	register(&method{
		name: "help.all", desc: "Describe every method the server provides, including block methods.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return map[string]any{"http": httpPaths(), "methods": helpMethods()}, nil
		},
	})
	register(&method{
		name: "hosts.list", desc: "List the server's hosts.",
		in: props(nil, nil),
		handler: func(c *control, _ json.RawMessage) (any, *apiError) {
			return map[string]any{"hosts": []any{}, "hash": "", "diagnostics": []any{}}, nil
		},
	})
	// Methods Rex has that GoRex does not, which answer as an older server's
	// would, with a clear message rather than silence.
	for _, name := range []string{
		"session.new_block", "session.detach_block", "session.float_block",
		"session.new_layer", "session.close_layer", "session.raise_layer",
		"session.lower_layer", "session.resize_layer", "session.move",
		"client.perform", "client.reply", "client.set_script",
		"server.stop", "server.apple.grant_file_access", "server.apple.privacy_status",
		"hosts.put", "hosts.remove",
	} {
		n := name
		register(&method{
			name: n, desc: "Not provided by GoRex yet.",
			in: props(nil, nil),
			handler: func(c *control, _ json.RawMessage) (any, *apiError) {
				return nil, errNotSupported("%s is not provided by GoRex", n)
			},
		})
	}
}

// decode unmarshals a request's payload.
func decode(raw json.RawMessage, v any) *apiError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errBadRequest("invalid arguments: %v", err)
	}
	return nil
}

// sessionArg reads a request's session and looks it up.
func (c *control) sessionArg(raw json.RawMessage) (*mSession, *apiError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if apiErr := decode(raw, &p); apiErr != nil {
		return nil, apiErr
	}
	return c.sessionFor(p.SessionID)
}

// sessionFor looks a session up, and checks the caller may use it.
func (c *control) sessionFor(id string) (*mSession, *apiError) {
	m := c.s.current()
	s := m.session[id]
	if s == nil {
		return nil, errSessionNotFound("no session has the id %q", id)
	}
	c.mu.Lock()
	ok := c.all || c.attached[id]
	c.mu.Unlock()
	if !ok {
		return nil, errConflict("this connection is not attached to the session")
	}
	return s, nil
}

// blockIn looks a block up in a session, by ID or by its window's focus.
func (c *control) blockIn(s *mSession, id, windowID string) (*mBlock, *apiError) {
	if id != "" {
		b := s.findBlock(id)
		if b == nil {
			return nil, errNotFound("no block %q in the session", id)
		}
		return b, nil
	}
	w := s.window(windowID)
	if w == nil {
		w = s.activeWindow()
	}
	if w == nil || w.Focused == "" {
		return nil, errNotFound("the session has no focused block")
	}
	b := s.findBlock(w.Focused)
	if b == nil {
		return nil, errNotFound("the window has no focused block")
	}
	return b, nil
}

// command runs a layout command in a session's space.
func (c *control) command(s *mSession, cmd rex.LayoutCommand) (*rex.LayoutCommand, *apiError) {
	if s != nil {
		cmd.Space = s.Workspace
	}
	client := c.s.conn()
	if client == nil {
		return nil, errInternal("the server connection is gone")
	}
	res, err := client.Command(cmd)
	if err != nil {
		return nil, errConflict("%v", err)
	}
	if res.Code == "" && res.Reason != "" {
		return nil, errConflict("the layout command was refused: %s", res.Reason)
	}
	if res.Code != "" {
		code := res.Code
		switch code {
		case "pane_not_found", "tab_not_found":
			return nil, errNotFound("%s", res.Error)
		case "not_allowed":
			return nil, errorf("refused", 403, "%s", res.Error)
		default:
			return nil, errConflict("%s: %s", code, res.Error)
		}
	}
	return &res, nil
}

// isZoomed tells whether the block's window shows it zoomed.
func isZoomed(s *mSession, b *mBlock) bool {
	w := s.window(b.WindowID)
	return w != nil && w.Zoomed == b.ID
}

// blockSID returns the GoRex session of a block ID, "" for none.
func blockSID(m *model, id string) string {
	if b := m.blocks[id]; b != nil {
		return b.SID
	}
	return ""
}

// status is server.status's result.
func (s *Server) status() map[string]any {
	s.current()
	s.mu.Lock()
	count := len(s.model.sessions)
	s.mu.Unlock()
	return map[string]any{
		"pid":              os.Getpid(),
		"instance_id":      s.instID,
		"host_id":          s.hostID,
		"started_at":       s.hello.Started,
		"exit_empty":       false,
		"session_count":    count,
		"tailscale":        map[string]any{"state": "off"},
		"api_revision":     APIRevision,
		"api_min_revision": 0,
		"config":           map[string]any{"generation": 0, "hash": ""},
	}
}

// sessionList is the list session.list returns.
func sessionList(m *model) []map[string]any {
	out := make([]map[string]any, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, map[string]any{"session_id": s.ID, "label": s.Label})
	}
	return out
}

// clientInfo is a client as client.list reports it, and the sessions it
// is attached to.
func clientInfo(state *clientState, attached []string) map[string]any {
	st := state.snapshot()
	info := map[string]any{}
	if st.Kind != "" {
		info["kind"] = st.Kind
	}
	for k, v := range map[string]string{"name": st.Name, "version": st.Version, "platform": st.Platform, "label": st.Label} {
		if v != "" {
			info[k] = v
		}
	}
	out := map[string]any{
		"client_id":        st.ID,
		"principal":        st.Principal,
		"connected_at":     st.Connected,
		"connection_state": "connected",
	}
	if len(info) > 0 {
		out["info"] = info
	}
	sessions := append([]string(nil), attached...)
	sort.Strings(sessions)
	out["session_ids"] = sessions
	return out
}

// terminalAPI describes a terminal block's methods and events.
func terminalAPI() map[string]any {
	methods := map[string]any{}
	descs := map[string]string{
		"format":         "Read the terminal's screen as text, HTML, or escape sequences.",
		"write":          "Write text to the terminal's program as if typed.",
		"size":           "Get the terminal's size.",
		"title":          "Get the terminal's title.",
		"pwd":            "Get the working directory of the program in the foreground.",
		"process":        "Get the program running in the foreground.",
		"program_status": "Get what the program running in the foreground reported it is doing.",
		"reset":          "Reset the terminal.",
		"clear":          "Clear the terminal's screen.",
		"resize":         "Give the terminal a size.",
	}
	for name, desc := range descs {
		methods[name] = map[string]any{
			"description":   desc,
			"input_schema":  map[string]any{"type": "object"},
			"output_schema": map[string]any{"type": "object"},
		}
	}
	return map[string]any{
		"methods": methods,
		"events": map[string]any{
			"bell":         map[string]any{"type": "object"},
			"child_exited": map[string]any{"type": "object"},
		},
	}
}

// blockMethodDesc describes a terminal method.
func blockMethodDesc(name string) string {
	switch name {
	case "format":
		return "Read the terminal's screen as text, HTML, or escape sequences."
	case "write":
		return "Write text to the terminal's program as if typed."
	case "size":
		return "Get the terminal's size."
	case "title":
		return "Get the terminal's title."
	case "pwd":
		return "Get the working directory of the program in the foreground."
	case "process":
		return "Get the program running in the foreground."
	case "program_status":
		return "Get what the program running in the foreground reported it is doing."
	case "reset":
		return "Reset the terminal."
	case "clear":
		return "Clear the terminal's screen."
	case "resize":
		return "Give the terminal a size."
	}
	return ""
}

// helpMethods is the method list help.all and /api/help return.
func helpMethods() []map[string]any {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		m := methods[name]
		in := m.in
		if in == nil {
			in = map[string]any{"type": "object"}
		}
		out = append(out, map[string]any{
			"name":          m.name,
			"description":   m.desc,
			"invoke":        map[string]any{"method": m.name},
			"input_schema":  in,
			"output_schema": m.out,
		})
	}
	return out
}

// httpPaths are the HTTP endpoints, as /api/help reports them.
func httpPaths() map[string]any {
	return map[string]any{
		"help_path":    "/api/help",
		"control_path": "/api/control",
		"data_path":    "/api/data",
	}
}

// rexPlatform names the platform server.version reports.
func rexPlatform() string {
	return platformName()
}

// errUnsupportedMethod is the error a block method GoRex does not know
// gets.
func errUnsupportedMethod(name string) *apiError {
	return errMethodNotFound("the terminal has no method %q", name)
}

// neighborBlock returns the block after or before the focused one.
func neighborBlock(s *mSession, next, wrap bool) *mBlock {
	w := s.activeWindow()
	if w == nil || len(w.Blocks) < 2 {
		return nil
	}
	i := 0
	for j, b := range w.Blocks {
		if b.ID == w.Focused {
			i = j
		}
	}
	if next {
		i++
	} else {
		i--
	}
	if i < 0 || i >= len(w.Blocks) {
		if !wrap {
			return nil
		}
		i = (i + len(w.Blocks)) % len(w.Blocks)
	}
	return s.findBlock(w.Blocks[i].ID)
}

// neighborWindow returns the window after or before the active one.
func neighborWindow(s *mSession, next, wrap bool) *mWindow {
	if len(s.Windows) < 2 {
		return nil
	}
	i := 0
	for j, w := range s.Windows {
		if w.ID == s.ActiveWindow {
			i = j
		}
	}
	if next {
		i++
	} else {
		i--
	}
	if i < 0 || i >= len(s.Windows) {
		if !wrap {
			return nil
		}
		i = (i + len(s.Windows)) % len(s.Windows)
	}
	return s.Windows[i]
}

// neighborDirection returns the block nearest the focused one toward a
// direction, by where they sit in the window.
func neighborDirection(s *mSession, dir string, wrap bool) *mBlock {
	w := s.activeWindow()
	if w == nil || w.Tab == nil || len(w.Blocks) < 2 {
		return nil
	}
	rects := map[string][4]float64{}
	var walk func(*layoutView)
	walk = func(n *layoutView) {
		if n == nil {
			return
		}
		if n.BlockID != "" {
			rects[n.BlockID] = n.Rect
			return
		}
		if n.Split != nil {
			walk(n.Split.Before)
			walk(n.Split.After)
		}
	}
	walk(layoutOf(w.Tab.Root, [4]float64{0, 0, 1, 1}))
	cur, ok := rects[w.Focused]
	if !ok {
		return nil
	}
	ccx, ccy := cur[0]+cur[2]/2, cur[1]+cur[3]/2
	best, bestDist := "", math.Inf(1)
	for id, r := range rects {
		if id == w.Focused {
			continue
		}
		cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
		dx, dy := cx-ccx, cy-ccy
		toward := false
		switch dir {
		case "left":
			toward = dx < -1e-6 && math.Abs(dy) < math.Abs(dx)*2
		case "right":
			toward = dx > 1e-6 && math.Abs(dy) < math.Abs(dx)*2
		case "up":
			toward = dy < -1e-6 && math.Abs(dx) < math.Abs(dy)*2
		case "down":
			toward = dy > 1e-6 && math.Abs(dx) < math.Abs(dy)*2
		}
		if !toward {
			continue
		}
		if d := dx*dx + dy*dy; d < bestDist {
			best, bestDist = id, d
		}
	}
	if best == "" && wrap {
		// Nothing on that side: take the block farthest on the other
		// side, as a focus that wraps around does.
		bestDist = math.Inf(-1)
		for id, r := range rects {
			if id == w.Focused {
				continue
			}
			cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
			dx, dy := cx-ccx, cy-ccy
			var d float64
			switch dir {
			case "left":
				d = dx
			case "right":
				d = -dx
			case "up":
				d = dy
			case "down":
				d = -dy
			}
			if d > bestDist {
				best, bestDist = id, d
			}
		}
	}
	if best == "" {
		return nil
	}
	return s.findBlock(best)
}
