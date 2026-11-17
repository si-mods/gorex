package rexapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// Projects: a GoRex space bound to a folder (docs/projects.md). A
// project's home is a host and a folder there; a new window made in it
// without a cwd starts in the folder when its home is this machine, which
// the GoRex server does (rex.CapabilitySpaceHome). A client reads the
// home as each session's project, in session lists and views, and sets
// it with gorex.project.set_home. GoRex's own, which Rex has not: the
// welcome lists featureProjects.

// featureProjects is the welcome's feature of a gateway whose server
// keeps projects' homes.
const featureProjects = "gorex.projects"

// desktopSpace is the space of the desktop's sessions of no tab
// (desktopWorkspace), which no layout keeps: no project.
const desktopSpace = "gorex-desktop-terminals"

// projectInfo is a session's home as a client reads it: the machine of
// its home host (rex.MachineID), "" when this desktop no longer knows
// the host's, and its folder there.
type projectInfo struct {
	HomeMachine string `json:"home_machine"`
	Dir         string `json:"dir"`
}

// entry is the session as session.list reports it.
func (s *mSession) entry() sessionListEntry {
	return sessionListEntry{SessionID: s.id, Label: s.label, Project: s.project}
}

// homeMachine is the machine of a space's home host (layout.Workspace.Host).
func (m *model) homeMachine(host string) string {
	if host == "" {
		return m.self
	}
	if machine, ok := layout.HostMachine(host); ok {
		return machine
	}
	return m.machines[host]
}

// hostOfMachine is the home host (layout.Workspace.Host) of a machine:
// "" for this one, the app's host of it, else one that names the machine
// itself, which the app shows as unknown until it has a host of it.
func (m *model) hostOfMachine(machine string) string {
	if machine == m.self {
		return ""
	}
	var ids []string
	for id, mid := range m.machines {
		if mid == machine {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return layout.MachineHost(machine)
	}
	slices.Sort(ids)
	return ids[0]
}

// homeOf is the home host and folder a home of machine and dir is in
// this layout: none for a machine or dir of "", the folder cleaned when
// it is on this machine; bad_request for a folder that is not absolute.
func (m *model) homeOf(machine, dir string) (host, folder string, aerr *apiError) {
	dir = strings.TrimSpace(dir)
	if machine == "" || dir == "" {
		return "", "", nil
	}
	if !layout.IsAbsDir(dir) {
		return "", "", errBadRequest("dir must be an absolute path")
	}
	host = m.hostOfMachine(machine)
	if host == "" {
		dir = filepath.Clean(dir)
	}
	return host, dir, nil
}

// setHome gives session s the home host and folder, unless it has them,
// and waits for the layout to show it. It returns the session as then.
func (g *Gateway) setHome(s *mSession, host, dir string) (*mSession, *apiError) {
	if s.home == host && s.dir == dir {
		return s, nil
	}
	if _, aerr := g.command(rex.LayoutCommand{Op: "space.home", Space: s.space, Host: host, Dir: dir}); aerr != nil {
		return nil, aerr
	}
	m, aerr := g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.home == host && now.dir == dir
	})
	if aerr != nil {
		return nil, aerr
	}
	if now := m.byID[s.id]; now != nil {
		return now, nil
	}
	return nil, errSessionNotFound("no session %s", s.id)
}

// methodGorexProjectSetHome sets or clears a session's home: a machine,
// and a folder on it; a machine_id of "" clears it.
func methodGorexProjectSetHome(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilitySpaceHome) {
		return nil, errNotSupported("the GoRex server does not keep projects' folders; update GoRex")
	}
	if aerr := a.only("session_id", "machine_id", "dir"); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.sessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	if s.space == desktopSpace {
		return nil, errBadRequest("%s is no project of the desktop's", s.id)
	}
	machine, aerr := a.reqStr("machine_id")
	if aerr != nil {
		return nil, aerr
	}
	dir, _, aerr := a.str("dir")
	if aerr != nil {
		return nil, aerr
	}
	if machine != "" && strings.TrimSpace(dir) == "" {
		return nil, errBadRequest("dir is required with a machine_id")
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	host, folder, aerr := m.homeOf(machine, dir)
	if aerr != nil {
		return nil, aerr
	}
	if machine != "" && host == "" {
		if st, err := os.Stat(folder); err != nil || !st.IsDir() {
			return nil, errNotFound("no folder %s on this machine", folder)
		}
	}
	now, aerr := c.g.setHome(s, host, folder)
	if aerr != nil {
		return nil, aerr
	}
	return projectSetHomeResult{Revision: now.revision, Project: now.project}, nil
}

// projectSetHomeResult is gorex.project.set_home's result.
type projectSetHomeResult struct {
	Revision uint64       `json:"revision"`
	Project  *projectInfo `json:"project,omitempty"`
}

// projectSchema is the schema of a session's project in the help
// document's results.
var projectSchema = map[string]any{
	"type":        "object",
	"description": "GoRex's own: the project's home, the machine (machine_id, as /api/spaces names machines) of its home host and its folder there. Left out for a session whose space has no folder. home_machine is empty when this desktop no longer knows the home host's machine.",
	"properties": map[string]any{
		"home_machine": map[string]any{"type": "string"},
		"dir":          map[string]any{"type": "string"},
	},
	"required":             []any{"home_machine", "dir"},
	"additionalProperties": false,
}

// withProject adds project to every object of a method's result schema
// that is a session, by its session_id and label: the session lists and
// views of Rex's methods, which GoRex's projects carry.
func withProject(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	changed := false
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				_, id := props["session_id"]
				_, label := props["label"]
				if _, has := props["project"]; id && label && !has {
					props["project"] = projectSchema
					changed = true
				}
			}
			for k, x := range v {
				if k != "project" {
					walk(x)
				}
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(m["output_schema"])
	if !changed {
		return raw
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

// projectSetHomeEntry is gorex.project.set_home's entry of the help
// document, in the shape of Rex's own.
var projectSetHomeEntry = map[string]any{
	"name":        "gorex.project.set_home",
	"description": "Set or clear a session's home: a machine and a folder on it, where its new windows start. GoRex's own method: use it only when the welcome's features list gorex.projects.",
	"invoke":      map[string]any{"method": "gorex.project.set_home"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "The session, a GoRex project."},
			"machine_id": map[string]any{"type": "string", "description": "The machine of the home host, as /api/spaces names machines; empty to clear the home."},
			"dir":        map[string]any{"type": "string", "description": "The folder on that machine, an absolute path. Required with a machine_id."},
		},
		"required":             []any{"session_id", "machine_id"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"revision": map[string]any{"type": "integer", "minimum": 0, "description": "The session's revision after the call."},
			"project":  projectSchema,
		},
		"required":             []any{"revision"},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"session_id\": \"session:<uuid>\",\n  \"machine_id\": \"9c63…\",\n  \"dir\": \"/home/me/code/windflow\"\n}",
	"response_example": "{\n  \"revision\": 5,\n  \"project\": {\"home_machine\": \"9c63…\", \"dir\": \"/home/me/code/windflow\"}\n}",
	"validation_notes": []any{
		"A machine_id of \"\" clears the home: the session's new windows start as before, and its project is left out.",
		"On this machine the folder must exist, or the call fails with not_found. A folder on another machine is not checked here.",
		"A machine this desktop has no host of is kept, and its project shows that machine; the desktop shows the home host as unknown.",
		"session.new_window without a cwd, in a session whose home is this machine, starts in the folder, and fails with conflict (folder_missing) when it is gone. Splits start where the block they split started.",
		"A server that doesn't keep projects, whose welcome doesn't list gorex.projects, fails with not_supported.",
	},
}

// llmsProjects documents gorex.project.set_home, after GoRex's other
// methods.
var llmsProjects = []byte(`
### gorex.project.set_home

Feature: ` + "`gorex.projects`" + `. Set or clear a session's home: a machine and a folder on it, where its new
windows start, as a GoRex project's.

Arguments:

- ` + "`session_id`" + ` (string, required): The session, a GoRex project.
- ` + "`machine_id`" + ` (string, required): The machine of the home host, as ` + "`/api/spaces`" + ` names machines;
  ` + "`\"\"`" + ` clears the home.
- ` + "`dir`" + ` (string): The folder on that machine, an absolute path. Required with a machine_id.

The result is ` + "`{\"revision\": 5, \"project\": {\"home_machine\": \"…\", \"dir\": \"…\"}}`" + `.

Notes:

- Each session in ` + "`session.list`" + `, ` + "`session_created`" + ` and every view carries
  ` + "`project: {home_machine, dir}`" + ` when it has a home, and leaves it out otherwise.
- ` + "`session.new_window`" + ` without a cwd, in a session whose home is this machine, starts in the folder; a
  folder that is gone fails the call (` + "`folder_missing`" + `) and starts no shell. A cwd given wins. Splits start
  where the block they split started.
- On this machine the folder must exist, or the call fails with ` + "`not_found`" + `. A server that doesn't keep
  projects fails with ` + "`not_supported`" + `.
`)
