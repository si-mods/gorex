package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

var errAmbiguous = errors.New("target is ambiguous")

// resolveTarget turns a session ID, label, position or ID suffix into the
// session's full ID. Labels come from the window's layout when it is
// available; a remote server has none.
func resolveTarget(c *rex.Client, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errNoSession
	}
	infos, err := c.List()
	if err != nil {
		return "", err
	}
	for _, in := range infos {
		if in.ID == target {
			return in.ID, nil
		}
	}
	labels := map[string]string{}
	if raw, err := c.Layout(); err == nil && len(raw) > 0 {
		labels = sessionLabels(raw)
	}
	if sid, ok := labels[target]; ok {
		if sid == "" {
			return "", fmt.Errorf("%w: %s", errAmbiguous, target)
		}
		return sid, nil
	}
	if n, err := strconv.Atoi(target); err == nil && n >= 1 && n <= len(infos) {
		return infos[n-1].ID, nil
	}
	if len(target) >= 4 {
		var found []string
		for _, in := range infos {
			if strings.HasSuffix(in.ID, target) {
				found = append(found, in.ID)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return "", fmt.Errorf("%w: %s", errAmbiguous, strings.Join(found, ", "))
		}
	}
	return "", errNoSession
}

// sessionLabels maps a workspace's name and its tabs' names to the session
// of the focused pane.
func sessionLabels(raw json.RawMessage) map[string]string {
	st := layout.Parse(raw)
	out := map[string]string{}
	add := func(label, sid string) {
		if label == "" || sid == "" {
			return
		}
		if old, ok := out[label]; ok && old != sid {
			out[label] = "" // ambiguous
			return
		}
		out[label] = sid
	}
	for _, w := range st.Sessions {
		var focused string
		if w.Active >= 0 && w.Active < len(w.Tabs) {
			focused = tabSession(w.Tabs[w.Active])
		}
		for _, t := range w.Tabs {
			add(t.Name, tabSession(t))
			addNodeNames(t.Root, add)
			for _, f := range t.Floats {
				addNodeNames(f.Pane, add)
			}
		}
		add(w.Name, focused)
	}
	return out
}

func addNodeNames(n *layout.Node, add func(string, string)) {
	if n == nil {
		return
	}
	if n.Name != "" && n.SID != "" && n.Host == "" {
		add(n.Name, n.SID)
	}
	addNodeNames(n.A, add)
	addNodeNames(n.B, add)
}

// tabSession is the session of the tab's focused pane, the first of its
// tiled panes when Focus names none; "" when that pane has no session of
// this machine, as a browser pane or a remote host's.
func tabSession(t layout.Tab) string {
	panes := layout.Leaves(t.Root)
	if len(panes) == 0 {
		return ""
	}
	n := panes[0]
	if t.Focus >= 0 && t.Focus < len(panes) {
		n = panes[t.Focus]
	}
	if n.Host != "" {
		return ""
	}
	return n.SID
}
