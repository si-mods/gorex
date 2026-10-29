//go:build darwin || linux || windows

package rex

import (
	"encoding/json"

	"gorex/internal/layout"
)

// filterLayout removes from a stored layout the panes peer may not use.
// A remote peer never sees panes of another host, so access cannot pass
// through this machine.
func (s *Server) filterLayout(raw []byte, peer string) (json.RawMessage, error) {
	if peer == "" || len(raw) == 0 {
		return raw, nil
	}
	var st layout.Store
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	var sessions []layout.Workspace
	ids := map[string]bool{}
	for _, w := range st.Sessions {
		tabs := s.filterLayoutTabs(w.Tabs, peer)
		if len(tabs) == 0 {
			continue
		}
		w.Tabs = tabs
		w.Active = min(max(w.Active, 0), len(tabs)-1)
		sessions = append(sessions, w)
		ids[w.ID] = true
	}
	if len(sessions) == 0 && len(st.Tabs) > 0 {
		st.Tabs = s.filterLayoutTabs(st.Tabs, peer)
		st.Active = min(max(st.Active, 0), max(len(st.Tabs)-1, 0))
		return json.Marshal(st)
	}
	st.Sessions = sessions
	var windows []string
	for _, id := range st.Windows {
		if ids[id] {
			windows = append(windows, id)
		}
	}
	st.Windows = windows
	return json.Marshal(st)
}

func (s *Server) filterLayoutTabs(tabs []layout.Tab, peer string) []layout.Tab {
	var out []layout.Tab
	for _, t := range tabs {
		root := s.filterLayoutNode(t.Root, peer)
		if root == nil {
			continue
		}
		t.Root = root
		if t.Focus >= 0 && t.Focus >= len(sidsOf(root)) {
			t.Focus = 0
		}
		if t.Zoom && t.Focus == 0 {
			t.Zoom = false
		}
		out = append(out, t)
	}
	return out
}

func sidsOf(n *layout.Node) []string {
	if n == nil {
		return nil
	}
	if n.SID != "" {
		return []string{n.SID}
	}
	return append(sidsOf(n.A), sidsOf(n.B)...)
}

func (s *Server) filterLayoutNode(n *layout.Node, peer string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.SID != "" {
		ss, err := s.session(n.SID)
		if err != nil || !s.mayUse(peer, ss) || n.Host != "" {
			return nil
		}
		return n
	}
	a, b := s.filterLayoutNode(n.A, peer), s.filterLayoutNode(n.B, peer)
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	n.A, n.B = a, b
	return n
}
