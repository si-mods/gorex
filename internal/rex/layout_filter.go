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
		st.Sessions, st.Windows = nil, nil
		return json.Marshal(st)
	}
	st.Tabs = s.filterLayoutTabs(st.Tabs, peer)
	st.Active = min(max(st.Active, 0), max(len(st.Tabs)-1, 0))
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
		// The focus names a place among every leaf the host keeps, a
		// browser pane's among them; the peer adopts it by the sessions it
		// may use, in order, which is what its own count names.
		before := leavesOf(t.Root)
		root := s.filterLayoutNode(t.Root, peer)
		if root == nil {
			continue
		}
		t.Root = root
		after := leavesOf(root)
		var wanted *layout.Node
		if t.Focus >= 0 && t.Focus < len(before) {
			wanted = before[t.Focus]
		}
		focus, kept, named := 0, false, 0
		for _, n := range after {
			if n.SID == "" {
				continue
			}
			if n == wanted {
				focus, kept = named, true
			}
			named++
		}
		t.Focus = min(max(focus, 0), max(named-1, 0))
		t.Zoom = t.Zoom && kept && named > 1
		wantedFloat := -1
		if t.FocusFloat > 0 {
			wantedFloat = t.FocusFloat - 1
		}
		newFocusFloat := 0
		var floats []layout.Float
		for i, f := range t.Floats {
			pane := s.filterLayoutNode(f.Pane, peer)
			if pane == nil {
				continue
			}
			f.Pane = pane
			floats = append(floats, f)
			if i == wantedFloat {
				newFocusFloat = len(floats)
			}
		}
		t.Floats = floats
		t.FocusFloat = newFocusFloat
		out = append(out, t)
	}
	return out
}

// leavesOf returns the panes of the tree of n, browser panes and the
// unavailable among them, as the layout's focus counts them.
func leavesOf(n *layout.Node) []*layout.Node {
	if n == nil {
		return nil
	}
	if n.A == nil || n.B == nil {
		return []*layout.Node{n}
	}
	return append(leavesOf(n.A), leavesOf(n.B)...)
}

func (s *Server) filterLayoutNode(n *layout.Node, peer string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.SID != "" {
		ss, err := s.session(n.SID)
		if err != nil || !s.mayUse(peer, ss) || n.Host != "" {
			return &layout.Node{Name: "Unavailable", Unavailable: true}
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
