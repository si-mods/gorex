package rexapi

import (
	"encoding/json"
	"strings"

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
	// HomeMachine and Dir were a project's home in v0.38.0 to v0.40.x.
	// They are read and ignored: a project's folder does not change, and
	// is made on its host (gorex.project.create).
	HomeMachine *string `json:"home_machine,omitempty"`
	Dir         *string `json:"dir,omitempty"`
}

type spacesApplyResult struct {
	Applied int `json:"applied"`
}

// methodGorexSpacesApply applies centralized space groupings from the control
// plane onto this machine's layout: moving local windows/tabs into the
// sessions of their assigned spaces. A space no session is makes none
// (docs/MOD-002-projects.md: one workspace with no folder per host): its windows
// go to the tabs with no project, but those of a project.
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

	applied := 0
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
			continue
		}

		// A space is a name on projects now (docs/MOD-002-projects.md): a space no
		// session is makes no workspace. Its tabs go to the host's tabs
		// with no project, but those of a project, which stay in it.
		toNoProject := target == nil
		if toNoProject {
			for _, x := range m.sessions {
				if x.dir == "" && x.space != desktopSpace && x.project != nil {
					target = x
					break
				}
			}
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
			if s == nil || w == nil || blk == nil || s.id == target.id || toNoProject && s.dir != "" {
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
	}
	return spacesApplyResult{Applied: applied}, nil
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
			"description": "List of {name, color?, block_ids} space definitions. home_machine and dir, a project's home in GoRex 0.38 to 0.40, are read and ignored: a project's folder does not change (gorex.project.create).",
		},
	},
	"result": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"applied": map[string]any{"type": "integer"},
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
- A space's ` + "`home_machine`" + ` and ` + "`dir`" + `, a project's home in GoRex 0.38 to 0.40, are read and ignored: a
  project's folder does not change (` + "`gorex.project.create`" + `).
`)
