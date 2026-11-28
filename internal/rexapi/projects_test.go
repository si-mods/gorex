package rexapi

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"gorex/internal/layout"
)

// projectOf returns a session's project in a session list, nil for none.
func projectOf(t *testing.T, c *testConn, sid string) map[string]any {
	t.Helper()
	res := callChecked(t, c, "session.list", nil)
	for _, raw := range res["sessions"].([]any) {
		s := raw.(map[string]any)
		if s["session_id"] == sid {
			p, _ := s["project"].(map[string]any)
			return p
		}
	}
	t.Fatalf("no session %s in the list", sid)
	return nil
}

// spaceOfSession returns the GoRex space of a session, as the server keeps it.
func (f *fixture) spaceOfSession(t *testing.T, sid string) layout.Workspace {
	t.Helper()
	raw, err := f.desk.Layout()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range layout.Parse(raw).Sessions {
		if sessionID(w.ID) == sid {
			return w
		}
	}
	t.Fatalf("no space of %s", sid)
	return layout.Workspace{}
}

// blocksOf are the block IDs of a session's view, in order.
func blocksOf(t *testing.T, c *testConn, sid string) []string {
	t.Helper()
	c.request("attach", "session.attach", map[string]any{"session_id": sid})
	var out []string
	for _, w := range viewOf(t, c, sid)["windows"].([]any) {
		for _, l := range w.(map[string]any)["layers"].([]any) {
			var walk func(n any)
			walk = func(n any) {
				m, _ := n.(map[string]any)
				if b, ok := m["block_id"].(string); ok {
					out = append(out, b)
				}
				for _, k := range []string{"a", "b", "first", "second", "root", "layout", "block"} {
					if x, ok := m[k]; ok {
						walk(x)
					}
				}
				if xs, ok := m["children"].([]any); ok {
					for _, x := range xs {
						walk(x)
					}
				}
			}
			walk(l)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// TestProjectCreate makes a project through the Rex API: the welcome lists
// gorex.projects of version 2; the project's first tab starts in the
// folder; the session list and view carry the project, and the tabs with
// no project {}; a second project of the folder answers exists with the
// first; a device path, a relative one and a folder not there are refused.
func TestProjectCreate(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Version: layout.Version, Windows: []string{"np"}, Sessions: []layout.Workspace{{ID: "np", Name: layout.NoProjectName}}})
	c := f.dialControl()
	c.hello(apiRevision)
	if fs, _ := c.welcome["features"].([]any); !slices.Contains(fs, any(featureProjects)) {
		t.Fatalf("welcome features %v lack %s", fs, featureProjects)
	}
	if vs, _ := c.welcome["feature_versions"].(map[string]any); vs[featureProjects] != float64(2) {
		t.Fatalf("welcome feature_versions %v", c.welcome["feature_versions"])
	}
	folder := realDir(t, t.TempDir())
	res := callChecked(t, c, "gorex.project.create", map[string]any{"name": "windflow", "dir": folder, "space": "Work"})
	pid, tid := res["project_id"].(string), res["tab_id"].(string)
	if pid == "" || tid == "" {
		t.Fatalf("project.create answered %v", res)
	}
	p := projectOf(t, c, pid)
	if p["dir"] != realDir(t, folder) || p["space"] != "Work" || p["used"] == nil || p["home_machine"] != f.g.hello.Machine {
		t.Fatalf("the list's project is %v", p)
	}
	callChecked(t, c, "session.attach", map[string]any{"session_id": pid})
	if v, _ := viewOf(t, c, pid)["project"].(map[string]any); v["dir"] != realDir(t, folder) {
		t.Errorf("the view's project is %v", v)
	}
	if n := f.tabRoot(t, pid); !sameDir(n.Dir, folder) {
		t.Errorf("the first tab starts in %q, want %q", n.Dir, folder)
	}
	if np := projectOf(t, c, sessionID("np")); np == nil || len(np) != 0 {
		t.Errorf("the tabs with no project have the project %v", np)
	}
	if ids := sessionIDs(t, c); ids["windflow"] != pid || ids[layout.NoProjectName] == "" {
		t.Errorf("the sessions an old client sees: %v", ids)
	}

	e := failure(t, c.request("again", "gorex.project.create", map[string]any{"dir": folder + string(filepath.Separator)}))
	if e["code"] != "exists" || e["project_id"] != pid {
		t.Errorf("a second project of the folder: %v", e)
	}
	refused := map[string]string{"relative": "bad_request", filepath.Join(folder, "gone"): "not_found"}
	// A device or pipe path is one only on Windows, where reading it
	// waits (rex.VolumePath).
	if runtime.GOOS == "windows" {
		refused[`\\.\pipe\x`] = "bad_request"
	}
	for dir, code := range refused {
		if e := failure(t, c.request("bad", "gorex.project.create", map[string]any{"dir": dir})); e["code"] != code {
			t.Errorf("project.create of %q: %v, want %s", dir, e, code)
		}
	}
}

// TestProjectMoveAdopt moves a tab between projects, keeping its blocks,
// and adopts blocks that run already into a project's tab: twice changes
// nothing, and a block it does not know is skipped.
func TestProjectMoveAdopt(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	one := callChecked(t, c, "gorex.project.create", map[string]any{"dir": t.TempDir()})
	two := callChecked(t, c, "gorex.project.create", map[string]any{"dir": t.TempDir()})
	p1, p2, tab := one["project_id"].(string), two["project_id"].(string), one["tab_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": p1})
	blocks := blocksOf(t, c, p1)
	if len(blocks) != 1 {
		t.Fatalf("blocks %v", blocks)
	}
	res := callChecked(t, c, "gorex.project.move", map[string]any{"tab_id": tab, "project_id": p2})
	if res["project_id"] != p2 {
		t.Fatalf("project.move answered %v", res)
	}
	if got := blocksOf(t, c, p2); !slices.Contains(got, blocks[0]) {
		t.Fatalf("the moved tab's blocks are %v, want %v", got, blocks)
	}
	res = callChecked(t, c, "gorex.project.move", map[string]any{"tab_id": res["tab_id"], "project_id": ""})
	np := res["project_id"].(string)
	if p := projectOf(t, c, np); p == nil || len(p) != 0 || !slices.Equal(blocksOf(t, c, np), blocks) {
		t.Fatalf("the tabs with no project: %v, %v", p, blocksOf(t, c, np))
	}

	adopt := map[string]any{
		"project": map[string]any{"name": "trainer", "dir": f.spaceOfSession(t, p1).Dir},
		"tabs": []any{map[string]any{"title": "mon", "split": map[string]any{"vertical": true, "ratio": 0.5,
			"a": map[string]any{"pane_id": blocks[0]}, "b": map[string]any{"pane_id": "block:00000000-0000-8000-8000-000000000000"}}}},
	}
	res = callChecked(t, c, "gorex.project.adopt", adopt)
	if res["project_id"] != p1 || !slices.Equal(blocksOf(t, c, p1), blocks) {
		t.Fatalf("adopt answered %v; blocks %v", res, blocksOf(t, c, p1))
	}
	before, _ := f.desk.Layout()
	callChecked(t, c, "gorex.project.adopt", adopt)
	if after, _ := f.desk.Layout(); string(after) != string(before) {
		t.Errorf("adopting again changed the layout")
	}
	if w := f.spaceOfSession(t, p1); len(w.Tabs) != 1 || w.Tabs[0].Name != "mon" || !w.Tabs[0].Root.IsLeaf() {
		t.Errorf("the adopted tab is %+v", w.Tabs)
	}
}

// TestProjectUpdateSpaces updates a project, renames its space, and lists
// the spaces, those offered among them.
func TestProjectUpdateSpaces(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Version: layout.Version, Spaces: []string{"Life"}})
	c := f.dialControl()
	c.hello(apiRevision)
	p := callChecked(t, c, "gorex.project.create", map[string]any{"dir": t.TempDir(), "space": "Work"})["project_id"].(string)
	res := callChecked(t, c, "gorex.project.update", map[string]any{"project_id": p, "name": "renamed", "color": "blue"})
	if pr, _ := res["project"].(map[string]any); pr["space"] != "Work" || pr["color"] != "blue" {
		t.Errorf("update answered %v", res)
	}
	if pr := projectOf(t, c, p); pr["color"] != "blue" {
		t.Errorf("the list's project of color blue is %v", pr)
	}
	if w := f.spaceOfSession(t, p); w.Name != "renamed" || w.Color != "blue" || w.Space != "Work" {
		t.Errorf("the project updated is %+v", w)
	}
	list := callChecked(t, c, "gorex.space.list", nil)
	if got := list["spaces"].([]any); !slices.Equal(got, []any{"Work", "Life"}) {
		t.Errorf("spaces %v", got)
	}
	if res := callChecked(t, c, "gorex.space.rename", map[string]any{"from": "Work", "to": "Job"}); res["projects"] != float64(1) {
		t.Errorf("space.rename answered %v", res)
	}
	if pr := projectOf(t, c, p); pr["space"] != "Job" {
		t.Errorf("the project after the rename is %v", pr)
	}
	callChecked(t, c, "gorex.project.update", map[string]any{"project_id": p, "space": ""})
	if pr := projectOf(t, c, p); pr["space"] != nil {
		t.Errorf("the project moved to Default is %v", pr)
	}
	callChecked(t, c, "gorex.project.update", map[string]any{"project_id": p, "color": "default"})
	if pr := projectOf(t, c, p); pr["color"] != nil {
		t.Errorf("the project of the default color is %v", pr)
	}
	if e := failure(t, c.request("np", "gorex.project.update", map[string]any{"project_id": "session:none"})); e["code"] != "not_found" {
		t.Errorf("update of no project: %v", e)
	}
}

// sameDir tells two paths of one directory, as a process resolves its own.
func sameDir(a, b string) bool {
	a, _ = filepath.EvalSymlinks(a)
	b, _ = filepath.EvalSymlinks(b)
	return a != "" && a == b
}

// realDir is dir as dir.list and a project name it: its symlinks
// resolved, as /private/var for macOS's /var.
func realDir(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// TestProjectFolderResolved makes a project through a symlink to a
// folder: the folder's own path, and a set_home of the link, answer
// exists with that project.
func TestProjectFolderResolved(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	real := realDir(t, t.TempDir())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlink here: %v", err)
	}
	pid := callChecked(t, c, "gorex.project.create", map[string]any{"dir": link})["project_id"].(string)
	if p := projectOf(t, c, pid); p["dir"] != real {
		t.Fatalf("the project of the link is %v, want %q", p, real)
	}
	if e := failure(t, c.request("again", "gorex.project.create", map[string]any{"dir": real})); e["code"] != "exists" || e["project_id"] != pid {
		t.Errorf("a project of the folder itself: %v", e)
	}
}

// TestSpaceDelete moves a deleted space's projects to Default, drops the
// name from those offered, and refuses Default.
func TestSpaceDelete(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Version: layout.Version, Spaces: []string{"Life"}})
	c := f.dialControl()
	c.hello(apiRevision)
	p := callChecked(t, c, "gorex.project.create", map[string]any{"dir": t.TempDir(), "space": "Work"})["project_id"].(string)
	if res := callChecked(t, c, "gorex.space.delete", map[string]any{"name": "Work"}); res["projects"] != float64(1) {
		t.Errorf("space.delete answered %v", res)
	}
	if pr := projectOf(t, c, p); pr["space"] != nil {
		t.Errorf("the project after the delete is %v", pr)
	}
	if res := callChecked(t, c, "gorex.space.delete", map[string]any{"name": "Life"}); res["projects"] != float64(0) {
		t.Errorf("space.delete of an offered name answered %v", res)
	}
	if got := callChecked(t, c, "gorex.space.list", nil)["spaces"].([]any); !slices.Equal(got, []any{""}) {
		t.Errorf("spaces after the delete: %v", got)
	}
	if e := failure(t, c.request("d", "gorex.space.delete", map[string]any{"name": ""})); e["code"] != "bad_request" {
		t.Errorf("delete of Default: %v", e)
	}
}

// TestSpaceRenameToDefault is what a client sends a host of v0.41.0, which
// has no gorex.space.delete: a rename to "" moves the projects to Default
// and drops the name.
func TestSpaceRenameToDefault(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Version: layout.Version, Spaces: []string{"Life"}})
	c := f.dialControl()
	c.hello(apiRevision)
	p := callChecked(t, c, "gorex.project.create", map[string]any{"dir": t.TempDir(), "space": "Work"})["project_id"].(string)
	if res := callChecked(t, c, "gorex.space.rename", map[string]any{"from": "Work", "to": ""}); res["projects"] != float64(1) {
		t.Errorf("space.rename answered %v", res)
	}
	if pr := projectOf(t, c, p); pr["space"] != nil {
		t.Errorf("the project after the rename is %v", pr)
	}
	callChecked(t, c, "gorex.space.rename", map[string]any{"from": "Life", "to": ""})
	if got := callChecked(t, c, "gorex.space.list", nil)["spaces"].([]any); !slices.Equal(got, []any{""}) {
		t.Errorf("spaces after the rename: %v", got)
	}
}
