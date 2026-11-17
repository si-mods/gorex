package rexapi

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
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

// TestProjectSetHome sets a session's home through the Rex API: the
// welcome lists the feature, the session list and view carry it, a new
// window without a cwd starts in the folder and one with a cwd there, and
// a folder gone fails the window with no shell started.
func TestProjectSetHome(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	if fs, _ := c.welcome["features"].([]any); !slices.Contains(fs, any(featureProjects)) {
		t.Fatalf("welcome features %v lack %s", fs, featureProjects)
	}
	self := f.g.hello.Machine
	if self == "" {
		t.Fatal("the server names no machine")
	}
	created := callChecked(t, c, "session.create", map[string]any{
		"initial_windows": []any{map[string]any{"window_label": "one", "layout": blockLayout()}},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	if p := projectOf(t, c, sid); p != nil {
		t.Fatalf("a session of no folder has a project %v", p)
	}
	if _, ok := viewOf(t, c, sid)["project"]; ok {
		t.Fatal("a view of no folder has a project")
	}

	folder := t.TempDir()
	res := callChecked(t, c, "gorex.project.set_home", map[string]any{"session_id": sid, "machine_id": self, "dir": folder})
	p, _ := res["project"].(map[string]any)
	if p["home_machine"] != self || p["dir"] != filepath.Clean(folder) {
		t.Fatalf("set_home answered %v", res)
	}
	if p := projectOf(t, c, sid); p["home_machine"] != self || p["dir"] != filepath.Clean(folder) {
		t.Errorf("the list's project is %v", p)
	}
	if v, _ := viewOf(t, c, sid)["project"].(map[string]any); v["dir"] != filepath.Clean(folder) {
		t.Errorf("the view's project is %v", v)
	}
	if w := f.spaceOfSession(t, sid); w.Host != "" || w.Dir != filepath.Clean(folder) {
		t.Errorf("the space's home is %q %q", w.Host, w.Dir)
	}

	// A new window without a cwd starts in the folder; with one, there.
	callChecked(t, c, "session.new_window", map[string]any{"session_id": sid, "layout": blockLayout()})
	if n := f.tabRoot(t, sid); !sameDir(n.Dir, folder) {
		t.Errorf("a new window starts in %q, want %q", n.Dir, folder)
	}
	other := t.TempDir()
	callChecked(t, c, "session.new_window", map[string]any{"session_id": sid, "layout": map[string]any{
		"block": map[string]any{"flavor": flavorShell, "options": map[string]any{"cwd": other}},
	}})
	if n := f.tabRoot(t, sid); !sameDir(n.Dir, other) {
		t.Errorf("a new window of a cwd starts in %q, want %q", n.Dir, other)
	}

	// The folder gone: the window fails, and no shell starts.
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o700)
	callChecked(t, c, "gorex.project.set_home", map[string]any{"session_id": sid, "machine_id": self, "dir": gone})
	os.Remove(gone)
	before, _ := f.desk.List()
	e := failure(t, c.request("gone", "session.new_window", map[string]any{"session_id": sid, "layout": blockLayout()}))
	if e["code"] != "conflict" || !strings.Contains(e["message"].(string), rex.FolderMissing) {
		t.Errorf("a new window in a folder gone: %v", e)
	}
	if after, _ := f.desk.List(); len(after) != len(before) {
		t.Errorf("%d sessions after the failed window, %d before", len(after), len(before))
	}

	for _, x := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"session_id": sid, "machine_id": self, "dir": "relative"}, "bad_request"},
		{map[string]any{"session_id": sid, "machine_id": self}, "bad_request"},
		{map[string]any{"session_id": sid, "machine_id": self, "dir": gone}, "not_found"},
		{map[string]any{"session_id": sid, "machine_id": self, "dir": folder, "x": 1}, "bad_request"},
		{map[string]any{"session_id": "session:none", "machine_id": ""}, "session_not_found"},
	} {
		if e := failure(t, c.request("bad", "gorex.project.set_home", x.payload)); e["code"] != x.code {
			t.Errorf("set_home %v: %v, want %s", x.payload, e, x.code)
		}
	}

	// A machine_id of "" clears the home.
	res = callChecked(t, c, "gorex.project.set_home", map[string]any{"session_id": sid, "machine_id": ""})
	if _, ok := res["project"]; ok {
		t.Errorf("a cleared home answered %v", res)
	}
	if p := projectOf(t, c, sid); p != nil {
		t.Errorf("a cleared home lists %v", p)
	}
}

// TestProjectHomeOfHosts sets homes on other machines: one of the app's
// hosts is named by its ID, one the app has no host of by the machine,
// and a host removed from hosts.json keeps the home, its machine unknown.
func TestProjectHomeOfHosts(t *testing.T) {
	f := startFixture(t)
	hosts := filepath.Join(rex.Dir(), "hosts.json")
	if err := os.WriteFile(hosts, []byte(`{"hosts":[{"id":"h1","name":"Studio","address":"tcat://x","machine":"machine-one"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.setLayout(spacesLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	work := sessionIDs(t, c)["work"]

	callChecked(t, c, "gorex.project.set_home", map[string]any{"session_id": work, "machine_id": "machine-one", "dir": `C:\code\app`})
	if w := f.spaceOfSession(t, work); w.Host != "h1" || w.Dir != `C:\code\app` {
		t.Fatalf("the home of a host is %q %q", w.Host, w.Dir)
	}
	if p := projectOf(t, c, work); p["home_machine"] != "machine-one" || p["dir"] != `C:\code\app` {
		t.Errorf("the project of a host is %v", p)
	}

	// The host removed: the home stays, its machine unknown.
	if err := os.WriteFile(hosts, []byte(`{"hosts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := projectOf(t, c, work); p == nil || p["home_machine"] != "" || p["dir"] != `C:\code\app` {
		t.Errorf("the project of a removed host is %v", p)
	}
	if w := f.spaceOfSession(t, work); w.Host != "h1" {
		t.Errorf("the removed host's home is %q", w.Host)
	}

	callChecked(t, c, "gorex.project.set_home", map[string]any{"session_id": work, "machine_id": "machine-far", "dir": "/srv/app"})
	if w := f.spaceOfSession(t, work); w.Host != layout.MachineHost("machine-far") || w.Dir != "/srv/app" {
		t.Fatalf("the home of an unknown machine is %q %q", w.Host, w.Dir)
	}
	if p := projectOf(t, c, work); p["home_machine"] != "machine-far" {
		t.Errorf("the project of an unknown machine is %v", p)
	}
}

// TestSpacesApplyHome syncs a project's home both ways: a manifest's home
// reaches the desktop's space, and the session list gives it back; a
// machine the desktop does not know keeps the folder; a space with no
// home fields keeps its home, and a dir of "" clears it.
func TestSpacesApplyHome(t *testing.T) {
	f := startFixture(t)
	f.setLayout(spacesLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	self := f.g.hello.Machine
	work := sessionIDs(t, c)["work"]
	folder := t.TempDir()

	apply := func(entry map[string]any) map[string]any {
		t.Helper()
		entry["name"] = "work"
		entry["block_ids"] = []string{}
		return callChecked(t, c, "gorex.spaces_apply", map[string]any{"spaces": []any{entry}})
	}
	res := apply(map[string]any{"home_machine": self, "dir": folder})
	if res["homes"] != float64(1) {
		t.Errorf("the apply answered %v", res)
	}
	if p := projectOf(t, c, work); p["home_machine"] != self || p["dir"] != filepath.Clean(folder) {
		t.Fatalf("the synced project is %v", p)
	}
	if res := apply(map[string]any{"home_machine": self, "dir": folder}); res["homes"] != nil {
		t.Errorf("the same home again answered %v", res)
	}

	apply(map[string]any{"home_machine": "machine-unknown", "dir": "/home/me/code"})
	if w := f.spaceOfSession(t, work); w.Host != layout.MachineHost("machine-unknown") || w.Dir != "/home/me/code" {
		t.Errorf("the home of an unknown machine is %q %q", w.Host, w.Dir)
	}
	if p := projectOf(t, c, work); p["home_machine"] != "machine-unknown" || p["dir"] != "/home/me/code" {
		t.Errorf("the project of an unknown machine goes back as %v", p)
	}

	// A client of before projects sends no home: it stays.
	apply(map[string]any{})
	if w := f.spaceOfSession(t, work); w.Dir != "/home/me/code" {
		t.Errorf("an apply of no home changed it to %q", w.Dir)
	}
	if e := failure(t, c.request("rel", "gorex.spaces_apply", map[string]any{"spaces": []any{
		map[string]any{"name": "work", "block_ids": []string{}, "home_machine": self, "dir": "rel"},
	}})); e["code"] != "bad_request" {
		t.Errorf("a relative dir: %v", e)
	}
	apply(map[string]any{"dir": ""})
	if w := f.spaceOfSession(t, work); w.Host != "" || w.Dir != "" {
		t.Errorf("a cleared home is %q %q", w.Host, w.Dir)
	}
}

// sameDir tells two paths of one directory, as a process resolves its own.
func sameDir(a, b string) bool {
	a, _ = filepath.EvalSymlinks(a)
	b, _ = filepath.EvalSymlinks(b)
	return a != "" && a == b
}
