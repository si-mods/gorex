package rexapi

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// Projects (docs/MOD-002-projects.md): a host keeps its projects, each a folder of
// its own, as GoRex workspaces, which this API shows as sessions, and one
// workspace of its tabs with no project. GoRex's own methods name them
// with GoRex's words: project_id is the project's Rex session ID, tab_id a
// tab's Rex window ID and pane_id a pane's Rex block ID, so that a client
// passes an ID from a Rex method unchanged. The welcome lists
// featureProjects, with featureProjectsVersion in feature_versions, when
// the GoRex server keeps projects so (rex.CapabilityProjects).

// featureProjects is the welcome's feature of a gateway whose server
// keeps projects.
const featureProjects = "gorex.projects"

// featureProjectsVersion is the version of featureProjects: 2 for
// projects that are folders of their host, in a space. A client that sees
// none talks to a host of v0.38.0 to v0.40.x.
const featureProjectsVersion = 2

// desktopSpace is the space of the desktop's sessions of no tab
// (desktopWorkspace), which no layout keeps: no project.
const desktopSpace = "gorex-desktop-terminals"

// projectInfo is a session's project as a client reads it: its folder, its
// space ("" or left out for Default), when it was used last and its color
// (left out for none); {} for the host's tabs with no project. HomeMachine is this machine for a project,
// as a client of v0.38.0 to v0.40.x reads it. Deprecated: a project is
// always of its host.
type projectInfo struct {
	Dir         string `json:"dir,omitempty"`
	Space       string `json:"space,omitempty"`
	Used        string `json:"used,omitempty"`
	Color       string `json:"color,omitempty"`
	HomeMachine string `json:"home_machine,omitempty"`
}

// projectOf is the project of workspace ws as a client reads it.
func (m *model) projectOf(ws *layout.Workspace) *projectInfo {
	if ws.Dir == "" {
		return &projectInfo{}
	}
	p := &projectInfo{Dir: ws.Dir, Space: ws.Space, Color: ws.Color, HomeMachine: m.self}
	if !ws.Used.IsZero() {
		p.Used = ws.Used.UTC().Format(time.RFC3339)
	}
	return p
}

// entry is the session as session.list reports it.
func (s *mSession) entry() sessionListEntry {
	return sessionListEntry{SessionID: s.id, Label: s.label, Project: s.project}
}

// projectsCan fails a project method of a server that keeps no projects.
func (c *control) projectsCan() *apiError {
	if !c.g.hello.Can(rex.CapabilityProjects) {
		return errNotSupported("the GoRex server does not keep projects; update GoRex")
	}
	return nil
}

// projectArg reads a project_id: the session of a project, "" for the
// tabs with no project when empty is true.
func (c *control) projectArg(a args, empty bool) (*mSession, *apiError) {
	id, present, aerr := a.str("project_id")
	if aerr != nil {
		return nil, aerr
	}
	if !present || id == "" {
		if empty && present {
			return nil, nil
		}
		return nil, errBadRequest("project_id is required")
	}
	m, aerr := c.g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	s := m.byID[id]
	if s == nil || s.dir == "" {
		return nil, errNotFound("no project %s", id)
	}
	return s, nil
}

// errExists is the error of a project of a folder that is one already,
// project naming it.
func errExists(dir, project string) *apiError {
	e := errorf("exists", 409, "%s is a project already", dir)
	e.ProjectID = project
	return e
}

// folderArg reads a project's folder, dir: absolute, and not a device or
// pipe (rex.VolumePath), which is checked before anything reads it.
func folderArg(dir string) (string, *apiError) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return "", errBadRequest("dir must be an absolute path")
	}
	dir = filepath.Clean(dir)
	if _, ok := rex.VolumePath(dir); !ok {
		return "", errBadRequest("dir must be a folder, not a device")
	}
	return dir, nil
}

// folderThere is dir, a folderArg, as dir.list names it, its symlinks
// resolved (rex.ProjectFolder), and fails when it is no folder here: two
// paths of one folder are one project's.
func folderThere(dir string) (string, *apiError) {
	real, ok := rex.ProjectFolder(dir)
	if !ok {
		return "", errNotFound("no folder %s on this machine", dir)
	}
	return real, nil
}

// windowOfTab waits for the window of GoRex tab tab in the session of
// workspace space, and returns it with the model.
func (g *Gateway) windowOfTab(space, tab string) (*model, *mWindow, *apiError) {
	var w *mWindow
	find := func(m *model) bool {
		s := m.byID[sessionID(space)]
		if s == nil {
			return false
		}
		for _, x := range s.windows {
			if x.tab == tab {
				w = x
				return true
			}
		}
		return false
	}
	m, aerr := g.afterCommand(find)
	return m, w, aerr
}

// projectCreateResult is gorex.project.create's result.
type projectCreateResult struct {
	ProjectID string `json:"project_id"`
	TabID     string `json:"tab_id"`
}

// methodGorexProjectCreate makes a project of a folder of this machine,
// in a space, and its first tab in the folder.
func methodGorexProjectCreate(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("name", "dir", "space"); aerr != nil {
		return nil, aerr
	}
	name, _, aerr := a.str("name")
	if aerr != nil {
		return nil, aerr
	}
	raw, aerr := a.reqStr("dir")
	if aerr != nil {
		return nil, aerr
	}
	space, _, aerr := a.str("space")
	if aerr != nil {
		return nil, aerr
	}
	dir, aerr := folderArg(raw)
	if aerr != nil {
		return nil, aerr
	}
	if dir, aerr = folderThere(dir); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	for _, s := range m.sessions {
		if s.dir != "" && layout.SameDir(s.dir, dir) {
			return nil, errExists(dir, s.id)
		}
	}
	res, err := c.g.opt.Client.Command(rex.LayoutCommand{Op: "project.create", Name: name, Dir: dir, SpaceName: &space})
	if err != nil {
		return nil, errInternal("asking the GoRex server: %v", err)
	}
	if res.Code == "exists" {
		return nil, errExists(dir, sessionID(res.Space))
	}
	if res.Code != "" {
		return nil, layoutError(c.g, res)
	}
	_, w, aerr := c.g.windowOfTab(res.Space, res.Tab)
	if aerr != nil {
		return nil, aerr
	}
	return projectCreateResult{ProjectID: sessionID(res.Space), TabID: w.id}, nil
}

// adoptNode is a tab's split of gorex.project.adopt: a pane (pane_id) or
// a split of two, as layout.Node.
type adoptNode struct {
	PaneID   string     `json:"pane_id,omitempty"`
	Vertical bool       `json:"vertical,omitempty"`
	Ratio    float32    `json:"ratio,omitempty"`
	A        *adoptNode `json:"a,omitempty"`
	B        *adoptNode `json:"b,omitempty"`
}

// node is the split as the GoRex server takes it, each pane by its
// session.
func (n *adoptNode) node(sids map[string]string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.A == nil && n.B == nil {
		// A pane of no session here has none, which the server skips.
		return &layout.Node{SID: sids[n.PaneID]}
	}
	return &layout.Node{Vertical: n.Vertical, Ratio: n.Ratio, A: n.A.node(sids), B: n.B.node(sids)}
}

// projectAdoptResult is gorex.project.adopt's result.
type projectAdoptResult struct {
	// ProjectID is where the tabs went: the project, or the tabs with no
	// project; empty when no tab had a pane here.
	ProjectID string `json:"project_id,omitempty"`
}

// methodGorexProjectAdopt arranges panes that run already into the tabs
// given, in the project given, made unless it is there: it starts no
// shell and opens no tab of its own; a pane it does not know is skipped,
// and adopting again changes nothing (docs/MOD-002-projects.md, Migration).
func methodGorexProjectAdopt(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("project", "tabs"); aerr != nil {
		return nil, aerr
	}
	var in struct {
		Project *layout.AdoptProject `json:"project"`
		Tabs    []struct {
			Title string     `json:"title"`
			Split *adoptNode `json:"split"`
		} `json:"tabs"`
	}
	b, _ := json.Marshal(a)
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, errBadRequest("invalid adoption: %v", err)
	}
	ad := layout.Adoption{Project: in.Project}
	if ad.Project != nil {
		dir, aerr := folderArg(ad.Project.Dir)
		if aerr != nil {
			return nil, aerr
		}
		ad.Project.Dir = dir
	}
	sids := map[string]string{}
	for _, in := range c.g.sessionsNow() {
		sids[blockID(in.ID)] = in.ID
	}
	for _, t := range in.Tabs {
		ad.Tabs = append(ad.Tabs, layout.AdoptTab{Title: t.Title, Split: t.Split.node(sids)})
	}
	res, aerr := c.g.command(rex.LayoutCommand{Op: "project.adopt", Adopt: &ad})
	if aerr != nil {
		return nil, aerr
	}
	if res.Space == "" {
		return projectAdoptResult{}, nil
	}
	id := sessionID(res.Space)
	if _, aerr := c.g.afterCommand(func(m *model) bool { return m.byID[id] != nil }); aerr != nil {
		return nil, aerr
	}
	return projectAdoptResult{ProjectID: id}, nil
}

// methodGorexProjectMove moves a tab, with its panes, to another project
// of this host, or with a project_id of "" to the tabs with no project.
func methodGorexProjectMove(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("tab_id", "project_id"); aerr != nil {
		return nil, aerr
	}
	tabID, aerr := a.reqStr("tab_id")
	if aerr != nil {
		return nil, aerr
	}
	to, aerr := c.projectArg(a, true)
	if aerr != nil {
		return nil, aerr
	}
	m := c.g.modelNow()
	var w *mWindow
	for _, s := range m.sessions {
		if x := s.window(tabID); x != nil {
			w = x
		}
	}
	if w == nil || w.tab == "" {
		return nil, errNotFound("no tab %s", tabID)
	}
	cmd := rex.LayoutCommand{Op: "project.move", Tab: w.tab}
	if to != nil {
		cmd.ToSpace = to.space
	}
	res, aerr := c.g.command(cmd)
	if aerr != nil {
		return nil, aerr
	}
	_, now, aerr := c.g.windowOfTab(res.Space, w.tab)
	if aerr != nil {
		return nil, aerr
	}
	return map[string]string{"project_id": sessionID(res.Space), "tab_id": now.id}, nil
}

// methodGorexProjectUpdate renames a project, colors it, or moves it to
// another space.
func methodGorexProjectUpdate(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("project_id", "name", "color", "space"); aerr != nil {
		return nil, aerr
	}
	p, aerr := c.projectArg(a, false)
	if aerr != nil {
		return nil, aerr
	}
	name, _, aerr := a.str("name")
	if aerr != nil {
		return nil, aerr
	}
	color, _, aerr := a.str("color")
	if aerr != nil {
		return nil, aerr
	}
	space, hasSpace, aerr := a.str("space")
	if aerr != nil {
		return nil, aerr
	}
	cmd := rex.LayoutCommand{Op: "project.update", Space: p.space, Name: name, Color: color}
	if hasSpace {
		cmd.SpaceName = &space
	}
	if _, aerr := c.g.command(cmd); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(nil)
	if aerr != nil {
		return nil, aerr
	}
	if now := m.byID[p.id]; now != nil {
		return map[string]any{"project_id": p.id, "project": now.project}, nil
	}
	return nil, errNotFound("no project %s", p.id)
}

// methodGorexSpaceRename moves every project of this host of the space
// from to the space to.
func methodGorexSpaceRename(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("from", "to"); aerr != nil {
		return nil, aerr
	}
	from, _, aerr := a.str("from")
	if aerr != nil {
		return nil, aerr
	}
	to, _, aerr := a.str("to")
	if aerr != nil {
		return nil, aerr
	}
	res, aerr := c.g.command(rex.LayoutCommand{Op: "spaces.rename", From: from, To: to})
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := c.g.afterCommand(nil); aerr != nil {
		return nil, aerr
	}
	return map[string]int{"projects": res.Index}, nil
}

// methodGorexSpaceDelete deletes the space name of this host: every
// project of it goes to Default, and the name is offered no more. No tab
// closes. Default ("") is not deleted.
func methodGorexSpaceDelete(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("name"); aerr != nil {
		return nil, aerr
	}
	name, _, aerr := a.str("name")
	if aerr != nil {
		return nil, aerr
	}
	if strings.TrimSpace(name) == "" {
		return nil, errBadRequest("Default is not deleted")
	}
	cmd := rex.LayoutCommand{Op: "spaces.delete", From: name}
	if !c.g.hello.Can(rex.CapabilitySpacesDelete) {
		// A server of v0.41.0: a rename to Default moves the projects.
		cmd = rex.LayoutCommand{Op: "spaces.rename", From: name}
	}
	res, aerr := c.g.command(cmd)
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := c.g.afterCommand(nil); aerr != nil {
		return nil, aerr
	}
	return map[string]int{"projects": res.Index}, nil
}

// methodGorexSpaceList lists the space names of this host: those its
// projects carry, and those it offers though none does, as the spaces of
// a layout of before projects.
func methodGorexSpaceList(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
	}
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	names := []string{}
	for _, s := range m.sessions {
		if s.dir != "" && s.project != nil && !slices.Contains(names, s.project.Space) {
			names = append(names, s.project.Space)
		}
	}
	for _, n := range m.spaces {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return map[string][]string{"spaces": names}, nil
}

// methodGorexProjectSetHome is v0.38.0's gorex.project.set_home, kept for
// Rex Go 1.1 and the web client of v0.39.0, which make a project as a
// session.create and a set_home of its folder. Deprecated: it goes once
// phases 3 and 4 of docs/MOD-002-projects.md ship. A home is a folder of this
// machine only (space.home): another machine's fails with bad_request; a
// machine_id of "" clears it.
func methodGorexProjectSetHome(c *control, a args) (any, *apiError) {
	if aerr := c.projectsCan(); aerr != nil {
		return nil, aerr
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
	raw, _, aerr := a.str("dir")
	if aerr != nil {
		return nil, aerr
	}
	dir := ""
	switch {
	case machine == "":
	case machine != c.g.hello.Machine:
		return nil, errBadRequest("a project of another host is made on that host (gorex.project.create there)")
	default:
		if dir, aerr = folderArg(raw); aerr != nil {
			return nil, aerr
		}
		if dir, aerr = folderThere(dir); aerr != nil {
			return nil, aerr
		}
	}
	res, err := c.g.opt.Client.Command(rex.LayoutCommand{Op: "space.home", Space: s.space, Dir: dir})
	if err != nil {
		return nil, errInternal("asking the GoRex server: %v", err)
	}
	if res.Code == "exists" {
		return nil, errExists(dir, sessionID(res.Space))
	}
	if res.Code != "" {
		return nil, layoutError(c.g, res)
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.dir == dir
	})
	if aerr != nil {
		return nil, aerr
	}
	now := m.byID[s.id]
	if now == nil {
		return nil, errSessionNotFound("no session %s", s.id)
	}
	out := map[string]any{"revision": now.revision}
	if now.dir != "" {
		out["project"] = now.project
	}
	return out, nil
}

// projectSchema is the schema of a session's project in the help
// document's results.
var projectSchema = map[string]any{
	"type":        "object",
	"description": "GoRex's own: the session's project, a folder of this host: its folder, its space (left out for Default), when a tab of it last took input or was made (RFC 3339), and its color (a preset or #rrggbb, left out for the default). {} for the host's tabs with no project. home_machine is this machine, for clients of v0.38.0 to v0.40.x; deprecated.",
	"properties": map[string]any{
		"dir":          map[string]any{"type": "string"},
		"space":        map[string]any{"type": "string"},
		"used":         map[string]any{"type": "string"},
		"color":        map[string]any{"type": "string"},
		"home_machine": map[string]any{"type": "string"},
	},
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

// projectEntry is a help document's entry of a GoRex project method, in
// the shape of Rex's own.
func projectEntry(name, description string, props map[string]any, required []any, out map[string]any, request, response string, notes ...any) map[string]any {
	if required == nil {
		required = []any{}
	}
	return map[string]any{
		"name":        name,
		"description": description + " GoRex's own method: use it only when the welcome's features list gorex.projects, of version 2 (feature_versions).",
		"invoke":      map[string]any{"method": name},
		"input_schema": map[string]any{
			"type": "object", "properties": props, "required": required, "additionalProperties": false,
		},
		"output_schema":    out,
		"request_example":  request,
		"response_example": response,
		"validation_notes": append(notes, "A server that does not keep projects so fails with not_supported."),
	}
}

func strProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// projectEntries are the help document's entries of the project methods.
var projectEntries = []map[string]any{
	projectEntry("gorex.project.create", "Make a project of a folder of this machine, in a space, and its first tab in the folder.",
		map[string]any{"name": strProp("The project's name; the folder's name when empty."), "dir": strProp("The folder, an absolute path on this machine."), "space": strProp("The space; empty or left out for Default.")},
		[]any{"dir"},
		map[string]any{"type": "object", "properties": map[string]any{"project_id": strProp("The project, a session."), "tab_id": strProp("Its first tab, a window.")}, "required": []any{"project_id", "tab_id"}, "additionalProperties": false},
		"{\n  \"name\": \"windflow\",\n  \"dir\": \"/home/me/code/windflow\",\n  \"space\": \"Work\"\n}",
		"{\n  \"project_id\": \"session:<uuid>\",\n  \"tab_id\": \"window:<uuid>\"\n}",
		"A device or pipe path fails with bad_request before anything reads it; a folder that is not there fails with not_found.",
		"A folder that is a project of this host already fails with code exists, and the error's project_id names that project.",
	),
	projectEntry("gorex.project.adopt", "Arrange panes that run already into tabs, in a project made unless it is there. It starts no shell and opens no tab of its own.",
		map[string]any{
			"project": map[string]any{"type": "object", "description": "The project the tabs go in, by its folder; left out for the tabs with no project.", "properties": map[string]any{"name": strProp(""), "dir": strProp(""), "space": strProp("")}, "required": []any{"dir"}},
			"tabs":    map[string]any{"type": "array", "description": "Each tab's title and split: a tree whose leaves are {pane_id} and whose splits are {vertical, ratio, a, b}, as GoRex's layout.Node.", "items": map[string]any{"type": "object"}},
		},
		[]any{"tabs"},
		map[string]any{"type": "object", "properties": map[string]any{"project_id": strProp("Where the tabs went: the project, or the tabs with no project.")}, "additionalProperties": false},
		"{\n  \"project\": {\"name\": \"trainer\", \"dir\": \"/work/trainer\"},\n  \"tabs\": [{\"title\": \"mon\", \"split\": {\"vertical\": true, \"ratio\": 0.5, \"a\": {\"pane_id\": \"block:<uuid>\"}, \"b\": {\"pane_id\": \"block:<uuid>\"}}}]\n}",
		"{\n  \"project_id\": \"session:<uuid>\"\n}",
		"A pane this host does not run is skipped, and a tab of none. Each pane leaves the tab it was in; a tab left empty goes.",
		"Adopting again what is adopted changes nothing. A project whose folder is not here is not made: its tabs go to the tabs with no project.",
	),
	projectEntry("gorex.project.move", "Move a tab, with its panes and their programs, to another project of this host, or to the tabs with no project.",
		map[string]any{"tab_id": strProp("The tab, a window."), "project_id": strProp("The project, a session; empty for the tabs with no project.")},
		[]any{"tab_id", "project_id"},
		map[string]any{"type": "object", "properties": map[string]any{"project_id": strProp(""), "tab_id": strProp("The tab, which keeps its ID.")}, "required": []any{"project_id", "tab_id"}, "additionalProperties": false},
		"{\n  \"tab_id\": \"window:<uuid>\",\n  \"project_id\": \"session:<uuid>\"\n}",
		"{\n  \"project_id\": \"session:<uuid>\",\n  \"tab_id\": \"window:<uuid>\"\n}",
		"The project the tab leaves stays, with no tab perhaps.",
	),
	projectEntry("gorex.project.update", "Rename a project, color it, or move it to another space. Its folder does not change.",
		map[string]any{"project_id": strProp("The project, a session."), "name": strProp("Its new name; empty keeps it."), "color": strProp("A preset color, a #rrggbb, or default; empty keeps it."), "space": strProp("Its space; empty for Default, left out to keep it.")},
		[]any{"project_id"},
		map[string]any{"type": "object", "properties": map[string]any{"project_id": strProp(""), "project": projectSchema}, "required": []any{"project_id"}, "additionalProperties": false},
		"{\n  \"project_id\": \"session:<uuid>\",\n  \"space\": \"Life\"\n}",
		"{\n  \"project_id\": \"session:<uuid>\",\n  \"project\": {\"dir\": \"/home/me/notes\", \"space\": \"Life\"}\n}",
	),
	projectEntry("gorex.space.rename", "Move every project of this host of one space to another: a rename of the space on this host.",
		map[string]any{"from": strProp("The space's name; empty for Default."), "to": strProp("Its new name; empty for Default.")},
		[]any{"from", "to"},
		map[string]any{"type": "object", "properties": map[string]any{"projects": map[string]any{"type": "integer", "description": "How many projects it moved."}}, "required": []any{"projects"}, "additionalProperties": false},
		"{\n  \"from\": \"Work\",\n  \"to\": \"Job\"\n}",
		"{\n  \"projects\": 3\n}",
	),
	projectEntry("gorex.space.delete", "Delete a space of this host: every project of it goes to Default, and the name is offered no more. No tab closes, no shell stops.",
		map[string]any{"name": strProp("The space's name; not empty, as Default is not deleted.")},
		[]any{"name"},
		map[string]any{"type": "object", "properties": map[string]any{"projects": map[string]any{"type": "integer", "description": "How many projects moved to Default."}}, "required": []any{"projects"}, "additionalProperties": false},
		"{\n  \"name\": \"Work\"\n}",
		"{\n  \"projects\": 3\n}",
		"An empty name fails with bad_request. A name of no project deletes the name alone, answering projects 0.",
	),
	projectEntry("gorex.project.set_home", "Deprecated: v0.38.0's way to give a session a folder of this machine, kept for Rex Go 1.1 and the web client of v0.39.0; use gorex.project.create. It goes once those clients are replaced.",
		map[string]any{"session_id": strProp("The session."), "machine_id": strProp("This machine's ID; empty to clear the folder."), "dir": strProp("The folder, an absolute path on this machine.")},
		[]any{"session_id", "machine_id"},
		map[string]any{"type": "object", "properties": map[string]any{"revision": map[string]any{"type": "integer"}, "project": projectSchema}, "required": []any{"revision"}, "additionalProperties": false},
		`{"session_id": "session:<uuid>", "machine_id": "9c63…", "dir": "/home/me/code/windflow"}`,
		`{"revision": 5, "project": {"dir": "/home/me/code/windflow", "home_machine": "9c63…"}}`,
		"Another machine's ID fails with bad_request: a project is made on its host. A folder that is a project already fails with code exists and the error's project_id.",
	),
	projectEntry("gorex.space.list", "List the space names of this host: those its projects carry, then those it offers though none does, as the spaces of a GoRex of before projects.",
		map[string]any{}, nil,
		map[string]any{"type": "object", "properties": map[string]any{"spaces": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []any{"spaces"}, "additionalProperties": false},
		"{}", "{\n  \"spaces\": [\"\", \"Work\", \"Life\"]\n}",
		"An empty name is Default.",
	),
}

// llmsProjects documents the project methods, after GoRex's other
// methods.
var llmsProjects = []byte(`
### Projects

Feature: ` + "`gorex.projects`" + `, version 2 in the welcome's ` + "`feature_versions`" + `. A project is a folder of
this host, shown as a session; the host's tabs with no project are one more session. Each session in
` + "`session.list`" + `, ` + "`session_created`" + ` and every view carries ` + "`project: {dir, space, used}`" + `, ` + "`{}`" + ` for
the tabs with no project. GoRex's own methods name a project by its session ID (` + "`project_id`" + `), a tab by
its window ID (` + "`tab_id`" + `) and a pane by its block ID (` + "`pane_id`" + `).

- ` + "`gorex.project.create {name, dir, space}`" + ` → ` + "`{project_id, tab_id}`" + `: the project and its first tab in
  the folder. A folder that is a project already fails with code ` + "`exists`" + ` and the error's
  ` + "`project_id`" + `; a device path fails with ` + "`bad_request`" + `, a folder not there with ` + "`not_found`" + `.
- ` + "`gorex.project.adopt {project?: {name, dir, space}, tabs: [{title, split}]}`" + ` → ` + "`{project_id}`" + `:
  arranges panes that run already into tabs, each split a tree of ` + "`{pane_id}`" + ` leaves and
  ` + "`{vertical, ratio, a, b}`" + ` splits. It starts no shell; an unknown pane is skipped; adopting again changes
  nothing.
- ` + "`gorex.project.move {tab_id, project_id}`" + `: moves a tab with its panes; ` + "`project_id`" + ` "" is the
  tabs with no project.
- ` + "`gorex.project.update {project_id, name?, color?, space?}`" + `.
- ` + "`gorex.space.rename {from, to}`" + ` → ` + "`{projects}`" + `; ` + "`gorex.space.list`" + ` → ` + "`{spaces}`" + `.
- ` + "`gorex.space.delete {name}`" + ` → ` + "`{projects}`" + `: every project of the space goes to Default and the name
  is offered no more; no tab closes. An empty name fails with ` + "`bad_request`" + `.
`)
