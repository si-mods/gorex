package rexapi

import (
	"encoding/json"
	"strings"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// featureSpacesSync is the welcome's feature of a gateway that accepts
// passive space synchronization from a centralized control plane
// (gorex.spaces_apply).
const featureSpacesSync = "gorex.spaces_sync"

type spacesApplySpace struct {
	Name     string   `json:"name"`
	Color    string   `json:"color,omitempty"`
	BlockIDs []string `json:"block_ids"`
	// HomeMachine and Dir are the project's home (projects.go): the
	// machine of its home host and its folder there. Left out, the home
	// stays as it is, as from a client of before projects; a dir of ""
	// clears it.
	HomeMachine *string `json:"home_machine,omitempty"`
	Dir         *string `json:"dir,omitempty"`
}

type spacesApplyResult struct {
	Applied int `json:"applied"`
	// Homes counts the projects whose home changed.
	Homes int `json:"homes,omitempty"`
}

// applyHome gives the space of the session of id the home of entry, when
// entry has one and the server keeps homes, and tells whether it changed.
// A machine this desktop has no host of keeps the folder (hostOfMachine).
func (c *control) applyHome(id string, entry spacesApplySpace) (bool, *apiError) {
	if entry.Dir == nil || !c.g.hello.Can(rex.CapabilitySpaceHome) {
		return false, nil
	}
	machine := ""
	if entry.HomeMachine != nil {
		machine = *entry.HomeMachine
	}
	if machine == "" && *entry.Dir != "" {
		return false, nil
	}
	m := c.g.modelNow()
	s := m.byID[id]
	if s == nil || s.space == desktopSpace {
		return false, nil
	}
	host, dir, aerr := m.homeOf(machine, *entry.Dir)
	if aerr != nil {
		return false, aerr
	}
	if s.home == host && s.dir == dir {
		return false, nil
	}
	if _, aerr := c.g.setHome(s, host, dir); aerr != nil {
		return false, aerr
	}
	return true, nil
}

// methodGorexSpacesApply applies centralized space groupings from the control
// plane onto this machine's layout: creating missing spaces when local blocks
// belong to them, and moving local windows/tabs into their assigned spaces.
func methodGorexSpacesApply(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityTabToSpace) {
		return nil, errNotSupported("the GoRex server does not sync spaces; update GoRex")
	}
	if aerr := a.only("spaces"); aerr != nil {
		return nil, aerr
	}
	rawSpaces, ok := a["spaces"]
	if !ok {
		return nil, errBadRequest("spaces is required")
	}
	b, err := json.Marshal(rawSpaces)
	if err != nil {
		return nil, errBadRequest("invalid spaces: %v", err)
	}
	var desired []spacesApplySpace
	if err := json.Unmarshal(b, &desired); err != nil {
		return nil, errBadRequest("invalid spaces: %v", err)
	}

	for _, entry := range desired {
		if entry.Dir != nil && *entry.Dir != "" && !layout.IsAbsDir(strings.TrimSpace(*entry.Dir)) {
			return nil, errBadRequest("the dir of space %q must be an absolute path", entry.Name)
		}
	}
	applied, homes := 0, 0
	home := func(id string, entry spacesApplySpace) *apiError {
		changed, aerr := c.applyHome(id, entry)
		if changed {
			homes++
		}
		return aerr
	}
	for _, entry := range desired {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		m := c.g.modelNow()
		target := findSessionByLabel(m, name)

		// Check if any requested block lives on this machine and needs moving.
		var toMove []blockMoveTarget
		for _, bid := range entry.BlockIDs {
			s, w, blk := findBlockInModel(m, bid)
			if s == nil || w == nil || blk == nil {
				continue
			}
			if target != nil && s.id == target.id {
				continue
			}
			toMove = append(toMove, blockMoveTarget{session: s, window: w, block: blk})
		}
		if len(toMove) == 0 {
			if target != nil {
				if aerr := home(target.id, entry); aerr != nil {
					return nil, aerr
				}
			}
			continue
		}

		if target == nil {
			if _, aerr := c.g.command(rex.LayoutCommand{
				Op: "space.create", Name: name, Color: entry.Color,
			}); aerr != nil {
				return nil, aerr
			}
			m, aerr := c.g.afterCommand(func(m *model) bool {
				return findSessionByLabel(m, name) != nil
			})
			if aerr != nil {
				return nil, aerr
			}
			target = findSessionByLabel(m, name)
			if target == nil {
				continue
			}
		}

		// Deduplicate moves by window ID (moving a window moves all its blocks).
		movedWindows := map[string]bool{}
		for _, item := range toMove {
			if movedWindows[item.window.id] {
				continue
			}
			movedWindows[item.window.id] = true
			// Re-read current model in case a previous move destroyed/shifted sessions.
			cur := c.g.modelNow()
			s, w, blk := findBlockInModel(cur, item.block.id)
			if s == nil || w == nil || blk == nil || s.id == target.id {
				continue
			}
			if _, aerr := c.g.command(rex.LayoutCommand{
				Op: "tab.to_space", Space: s.space, Tab: w.tab, SID: blk.sid, ToSpace: target.space,
			}); aerr != nil {
				return nil, aerr
			}
			if _, aerr := c.g.afterCommand(func(m *model) bool {
				now := m.byID[target.id]
				return now == nil || now.window(w.id) != nil
			}); aerr != nil {
				return nil, aerr
			}
			applied++
		}
		if aerr := home(target.id, entry); aerr != nil {
			return nil, aerr
		}
	}
	return spacesApplyResult{Applied: applied, Homes: homes}, nil
}

type blockMoveTarget struct {
	session *mSession
	window  *mWindow
	block   *mBlock
}

func findSessionByLabel(m *model, name string) *mSession {
	for _, s := range m.sessions {
		if s.id == "gorex-desktop-terminals" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s.label), name) {
			return s
		}
	}
	return nil
}

func findBlockInModel(m *model, blockID string) (*mSession, *mWindow, *mBlock) {
	for _, s := range m.sessions {
		for _, w := range s.windows {
			for _, b := range w.allBlocks() {
				if b.id == blockID {
					return s, w, b
				}
			}
		}
	}
	return nil, nil, nil
}

var spacesApplyEntry = map[string]any{
	"name":        "gorex.spaces_apply",
	"description": "Apply centralized space assignments for blocks running on this machine. GoRex's own method: use it only when the welcome's features list gorex.spaces_sync.",
	"invoke":      map[string]any{"method": "gorex.spaces_apply"},
	"args": map[string]any{
		"spaces": map[string]any{
			"type":        "array",
			"description": "List of {name, color?, block_ids, home_machine?, dir?} space definitions. home_machine and dir are a project's home (gorex.projects): left out, the home stays; a dir of \"\" clears it.",
		},
	},
	"result": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"applied": map[string]any{"type": "integer"},
			"homes":   map[string]any{"type": "integer"},
		},
		"required": []string{"applied"},
	},
	"errors": []string{
		"A server that can't move windows between spaces, whose welcome doesn't list gorex.spaces_sync, fails with not_supported.",
	},
}

var llmsSpacesApply = []byte(`
### gorex.spaces_apply

Feature: ` + "`gorex.spaces_sync`" + `. Apply centralized space assignments from the control plane onto this machine's layout.

- Request: ` + "`{\"spaces\": [{\"name\": \"Work\", \"color\": \"blue\", \"block_ids\": [\"block:...\"]}]}`" + `
- Result: ` + "`{\"applied\": 1}`" + `
- A space may carry its project's home, ` + "`home_machine`" + ` and ` + "`dir`" + ` (feature ` + "`gorex.projects`" + `): left
  out, the home stays; a ` + "`dir`" + ` of ` + "`\"\"`" + ` clears it. A machine this desktop has no host of keeps the folder.
  The result's ` + "`homes`" + ` counts the projects whose home changed.
`)
