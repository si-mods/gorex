package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
		return sid, nil
	}
	if n, err := strconv.Atoi(target); err == nil && n >= 1 && n <= len(infos) {
		return infos[n-1].ID, nil
	}
	if len(target) >= 4 {
		var found []string
		for _, in := range infos {
			if strings.HasPrefix(in.ID, target) || strings.HasSuffix(in.ID, target) {
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

// layoutStore is the part of the app's saved layout the CLI reads.
type layoutStore struct {
	Sessions []layoutWorkspace `json:"sessions"`
}

type layoutWorkspace struct {
	Name   string      `json:"name"`
	Tabs   []layoutTab `json:"tabs"`
	Active int         `json:"active"`
}

type layoutTab struct {
	Name  string      `json:"name"`
	Focus int         `json:"focus"`
	Root  *layoutNode `json:"root"`
}

type layoutNode struct {
	SID string      `json:"sid"`
	A   *layoutNode `json:"a"`
	B   *layoutNode `json:"b"`
}

// sessionLabels maps a workspace's name and its tabs' names to the session
// of the focused pane.
func sessionLabels(raw json.RawMessage) map[string]string {
	var st layoutStore
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	out := map[string]string{}
	for _, w := range st.Sessions {
		var focused string
		if w.Active >= 0 && w.Active < len(w.Tabs) {
			focused = tabSession(w.Tabs[w.Active])
		}
		for _, t := range w.Tabs {
			sid := tabSession(t)
			if t.Name != "" && sid != "" {
				out[t.Name] = sid
			}
		}
		if w.Name != "" && focused != "" {
			out[w.Name] = focused
		}
	}
	return out
}

func tabSession(t layoutTab) string {
	var panes []string
	var walk func(*layoutNode)
	walk = func(n *layoutNode) {
		if n == nil {
			return
		}
		if n.SID != "" {
			panes = append(panes, n.SID)
		}
		walk(n.A)
		walk(n.B)
	}
	walk(t.Root)
	if t.Focus >= 0 && t.Focus < len(panes) {
		return panes[t.Focus]
	}
	if len(panes) > 0 {
		return panes[0]
	}
	return ""
}
