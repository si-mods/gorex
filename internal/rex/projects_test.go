//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gorex/internal/layout"
)

// pids are the processes of the server's sessions, by session.
func pids(t *testing.T, c *Client) map[string]int {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, in := range infos {
		out[in.ID] = in.PID
	}
	return out
}

// shells starts n sessions and returns their IDs.
func shells(t *testing.T, c *Client, n int) []string {
	t.Helper()
	cmd, dir := testShell()
	var ids []string
	for range n {
		in, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, in.ID)
	}
	return ids
}

// localPanes are the sessions of this machine the layout's tabs show.
func localPanes(st layout.Store) []string {
	var out []string
	for _, w := range st.Sessions {
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, l := range layout.Leaves(r) {
					if l.Host == "" && l.SID != "" {
						out = append(out, l.SID)
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestMigrateKeepsShells loads a layout of v0.39.0 with every case of
// docs/projects.md's Migration table, of real shells of this machine and
// panes of host h1, and migrates it as a server does as it starts or
// resumes after an upgrade: the same shells run, with the same processes,
// each in a tab still, and none starts as the lost panes are restored;
// h1's panes wait in adoptions; the old layout is kept.
func TestMigrateKeepsShells(t *testing.T) {
	s, c := applyFixture(t)
	hosts := `{"hosts":[{"id":"h1","address":"tcat://x","machine":"m1"}]}`
	if err := os.WriteFile(filepath.Join(Dir(), "hosts.json"), []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	l := shells(t, c, 4)
	old := fmt.Sprintf(`{"version":2,"windows":["gorex","work"],"sessions":[
 {"id":"gorex","name":"gorex","dir":%q,"tabs":[{"id":"dev","name":"dev","root":{"ratio":0.4,"a":{"sid":%q},"b":{"sid":%q}}}]},
 {"id":"trainer","name":"trainer","host":"h1","dir":"/work/trainer","tabs":[
  {"id":"train","name":"train","root":{"sid":"r1","host":"h1"}},
  {"id":"mon","name":"mon","root":{"ratio":0.5,"a":{"vertical":true,"a":{"sid":"r2","host":"h1"},"b":{"sid":"r3","host":"h1"}},"b":{"sid":%q}}}]},
 {"id":"work","name":"Work","tabs":[{"id":"api","name":"api","root":{"ratio":0.5,"a":{"sid":%q},"b":{"sid":"r4","host":"h1"}}}]}]}`,
		t.TempDir(), l[0], l[1], l[2], l[3])
	before := pids(t, c)
	s.mu.Lock()
	s.layout = []byte(old)
	s.mu.Unlock()
	s.migrateLayout()
	s.restoreLost(nil, s.lostPanes())

	if after := pids(t, c); !maps.Equal(before, after) {
		t.Fatalf("the shells changed: before %v, after %v", before, after)
	}
	st := storeOf(t, c)
	if st.Version != layout.Version {
		t.Fatalf("the layout is of version %d", st.Version)
	}
	want := slices.Clone(l)
	slices.Sort(want)
	if got := localPanes(st); !slices.Equal(got, want) {
		t.Fatalf("panes here %v, want %v", got, want)
	}
	// Case 1: the project here keeps its tab and split.
	if p := st.Space("gorex"); p == nil || p.Dir == "" || len(p.Tabs) != 1 || p.Tabs[0].Root.A.SID != l[0] || p.Tabs[0].Root.B.SID != l[1] {
		t.Fatalf("project gorex is %+v", p)
	}
	// Cases 3 and 4: this machine's panes of api and mon are tabs with no
	// project; Work is offered as a space.
	np := st.NoProject()
	if np == nil || len(np.Tabs) != 2 || !slices.Equal(st.Spaces, []string{"Work"}) {
		t.Fatalf("tabs with no project %+v, spaces %v", np, st.Spaces)
	}
	// Cases 2 to 5: h1's panes wait for h1, whenever it connects.
	if len(st.Adopt) != 2 || st.Adopt[0].Machine != "m1" || st.Adopt[0].Project == nil || len(st.Adopt[0].Tabs) != 2 || len(st.Adopt[1].Tabs) != 1 {
		t.Fatalf("adoptions %+v", st.Adopt)
	}
	kept, err := os.ReadFile(filepath.Join(Dir(), "layout.json.v039"))
	if err != nil || string(kept) != old {
		t.Fatalf("the old layout kept: %v", err)
	}
	// A second load, as after another upgrade, changes nothing; the app
	// takes an adoption once its host adopted it.
	raw, _ := c.Layout()
	s.migrateLayout()
	if again, _ := c.Layout(); string(again) != string(raw) {
		t.Fatal("a second migration changed the layout")
	}
	command(t, c, LayoutCommand{Op: "adopt.taken", Target: st.Adopt[0].ID})
	if st := storeOf(t, c); len(st.Adopt) != 1 {
		t.Fatalf("adoptions after one was taken: %+v", st.Adopt)
	}
	// A remote client takes none.
	if r := s.request(Request{ID: 1, Op: "command", Command: &LayoutCommand{Op: "adopt.taken", Target: st.Adopt[1].ID}}, "peer", nil); r.Error == "" || len(storeOf(t, c).Adopt) != 1 {
		t.Fatalf("a remote client took this machine's adoption: %+v", r)
	}
}

// TestProjectAdopt adopts, as host h1 does once the app sends it the
// adoption, shells that run already into the tabs given, in a project it
// makes: it starts no shell, a pane of no session here is skipped, the
// panes leave the tabs they were in, and adopting again changes nothing.
func TestProjectAdopt(t *testing.T) {
	_, c := applyFixture(t)
	cmd, _ := testShell()
	folder := t.TempDir()
	// Two shells a remote desktop started here, each in a tab of its own,
	// and a third that ended.
	a := command(t, c, LayoutCommand{Op: "tab.create", Command: cmd})
	b := command(t, c, LayoutCommand{Op: "tab.create", Command: cmd})
	gone := shells(t, c, 1)[0]
	if err := c.Kill(gone); err != nil {
		t.Fatal(err)
	}
	before := pids(t, c)
	ad := layout.Adoption{Project: &layout.AdoptProject{Name: "trainer", Dir: folder, Space: "Work"}, Tabs: []layout.AdoptTab{
		{Title: "mon", Split: &layout.Node{Vertical: true, Ratio: 0.3, A: &layout.Node{SID: a.New}, B: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: b.New}, B: &layout.Node{SID: gone}}}},
		{Title: "none", Split: &layout.Node{SID: "unknown"}},
	}}
	res := command(t, c, LayoutCommand{Op: "project.adopt", Adopt: &ad})
	st := storeOf(t, c)
	p := st.Project(folder)
	if p == nil || p.ID != res.Space || p.Name != "trainer" || p.Space != "Work" || len(p.Tabs) != 1 {
		t.Fatalf("the project adopted is %+v (answer %+v)", p, res)
	}
	mon := p.Tabs[0]
	if mon.ID != res.Tab || mon.Name != "mon" || !mon.Root.Vertical || mon.Root.Ratio != 0.3 || mon.Root.A.SID != a.New || mon.Root.B.SID != b.New {
		t.Fatalf("tab mon is %+v", mon)
	}
	if got := localPanes(st); len(got) != 2 {
		t.Fatalf("panes %v: each shell once", got)
	}
	for _, w := range st.Sessions {
		if w.ID != p.ID && len(w.Tabs) != 0 {
			t.Fatalf("the tabs the panes left stayed: %+v", w)
		}
	}
	if after := pids(t, c); !maps.Equal(before, after) {
		t.Fatalf("the shells changed: before %v, after %v", before, after)
	}
	raw, _ := c.Layout()
	command(t, c, LayoutCommand{Op: "project.adopt", Adopt: &ad})
	if again, _ := c.Layout(); string(again) != string(raw) {
		t.Fatalf("adopting again changed the layout:\n%s\n%s", raw, again)
	}
	// Tabs with no project go to the host's tabs with no project.
	d := command(t, c, LayoutCommand{Op: "tab.create", Space: p.ID, Command: cmd})
	res = command(t, c, LayoutCommand{Op: "project.adopt", Adopt: &layout.Adoption{Tabs: []layout.AdoptTab{{Title: "api", Split: &layout.Node{SID: d.New}}}}})
	if st := storeOf(t, c); st.NoProject() == nil || st.NoProject().ID != res.Space || len(st.NoProject().Tabs) != 1 || st.NoProject().Tabs[0].Name != "api" {
		t.Fatalf("tabs with no project %+v", st.Sessions)
	}
	// A project whose folder is not here is not made.
	res = command(t, c, LayoutCommand{Op: "project.adopt", Adopt: &layout.Adoption{
		Project: &layout.AdoptProject{Name: "x", Dir: filepath.Join(folder, "gone")},
		Tabs:    []layout.AdoptTab{{Title: "x", Split: &layout.Node{SID: d.New}}}}})
	if st := storeOf(t, c); st.Project(filepath.Join(folder, "gone")) != nil || st.NoProject() == nil || st.NoProject().ID != res.Space {
		t.Fatalf("a project of no folder: %+v", st.Sessions)
	}
}

// TestProjectCreate makes a project of a folder, with its first tab in
// it; a second of the folder answers exists with the first; a folder not
// there, a relative one and a device are refused, and start no shell.
func TestProjectCreate(t *testing.T) {
	_, c := applyFixture(t)
	cmd, _ := testShell()
	folder := t.TempDir()
	work := "Work"
	res := command(t, c, LayoutCommand{Op: "project.create", Dir: folder, SpaceName: &work, Command: cmd})
	p := spaceOf(t, c, res.Space)
	if p == nil || p.Dir != filepath.Clean(folder) || p.Space != "Work" || p.Name != filepath.Base(folder) || len(p.Tabs) != 1 || p.Tabs[0].ID != res.Tab || p.Used.IsZero() {
		t.Fatalf("the project is %+v", p)
	}
	if !sameDir(p.Tabs[0].Root.Dir, folder) {
		t.Errorf("its first tab starts in %q", p.Tabs[0].Root.Dir)
	}
	n := sessionCount(t, c)
	if again, _ := c.Command(LayoutCommand{Op: "project.create", Dir: folder + string(filepath.Separator), Command: cmd}); again.Code != "exists" || again.Space != res.Space {
		t.Fatalf("a second project of the folder: %+v", again)
	}
	device := "/dev/null"
	if filepath.Separator == '\\' {
		device = `\\.\pipe\x`
	}
	for dir, code := range map[string]string{
		filepath.Join(folder, "gone"): FolderMissing, "relative": "bad_dir", device: "bad_dir",
	} {
		if r, _ := c.Command(LayoutCommand{Op: "project.create", Dir: dir, Command: cmd}); r.Code != code {
			t.Errorf("project.create of %q: %+v, want %s", dir, r, code)
		}
	}
	if m := sessionCount(t, c); m != n {
		t.Errorf("%d sessions after the refusals, %d before", m, n)
	}
	if st := storeOf(t, c); len(st.Sessions) != 1 {
		t.Errorf("workspaces after the refusals: %+v", st.Sessions)
	}
}

// TestProjectMove moves a tab to another project and out of projects,
// with its panes' processes; updates a project and renames a space.
func TestProjectMove(t *testing.T) {
	_, c := applyFixture(t)
	cmd, _ := testShell()
	one := command(t, c, LayoutCommand{Op: "project.create", Dir: t.TempDir(), Command: cmd})
	two := command(t, c, LayoutCommand{Op: "project.create", Dir: t.TempDir(), Command: cmd})
	split := command(t, c, LayoutCommand{Op: "pane.split", SID: one.New, Command: cmd})
	before := pids(t, c)
	command(t, c, LayoutCommand{Op: "project.move", Tab: one.Tab, ToSpace: two.Space})
	st := storeOf(t, c)
	if p := st.Space(one.Space); p == nil || len(p.Tabs) != 0 {
		t.Fatalf("the project the tab left: %+v", p)
	}
	q := st.Space(two.Space)
	if len(q.Tabs) != 2 || q.Tabs[1].ID != one.Tab || q.Tabs[1].Root.A.SID != one.New || q.Tabs[1].Root.B.SID != split.New {
		t.Fatalf("the project the tab went to: %+v", q)
	}
	command(t, c, LayoutCommand{Op: "project.move", Tab: one.Tab})
	st = storeOf(t, c)
	if np := st.NoProject(); np == nil || len(np.Tabs) != 1 || np.Tabs[0].ID != one.Tab {
		t.Fatalf("tabs with no project: %+v", np)
	}
	if after := pids(t, c); !maps.Equal(before, after) {
		t.Fatalf("the shells changed: before %v, after %v", before, after)
	}
	for _, x := range []LayoutCommand{
		{Op: "project.move", Tab: "none", ToSpace: two.Space},
		{Op: "project.move", Tab: one.Tab, ToSpace: st.NoProject().ID},
	} {
		if r, _ := c.Command(x); r.Code == "" {
			t.Errorf("%+v was made", x)
		}
	}

	life, empty := "Life", ""
	command(t, c, LayoutCommand{Op: "project.update", Space: one.Space, Name: "renamed", Color: "blue", SpaceName: &life})
	command(t, c, LayoutCommand{Op: "project.update", Space: two.Space, SpaceName: &life})
	if p := spaceOf(t, c, one.Space); p.Name != "renamed" || p.Color != "blue" || p.Space != "Life" {
		t.Fatalf("the project updated is %+v", p)
	}
	if r := command(t, c, LayoutCommand{Op: "spaces.rename", From: "Life", To: "Home"}); r.Index != 2 {
		t.Fatalf("spaces.rename moved %d projects", r.Index)
	}
	command(t, c, LayoutCommand{Op: "project.update", Space: two.Space, SpaceName: &empty})
	if a, b := spaceOf(t, c, one.Space), spaceOf(t, c, two.Space); a.Space != "Home" || b.Space != "" || a.Name != "renamed" {
		t.Fatalf("after the rename: %+v, %+v", a, b)
	}
}

// TestProjectUsed notes a project's use as a tab of it takes input, at
// most once per usedEvery.
func TestProjectUsed(t *testing.T) {
	s, c := applyFixture(t)
	cmd, _ := testShell()
	p := command(t, c, LayoutCommand{Op: "project.create", Dir: t.TempDir(), Command: cmd})
	// Before any input: as the tab was made.
	s.change(&applying{s: s}, func(st *layout.Store) LayoutCommand {
		st.Space(p.Space).Used = st.Space(p.Space).Used.AddDate(0, 0, -1)
		return LayoutCommand{}
	})
	was := spaceOf(t, c, p.Space).Used
	ss, err := s.session(p.New)
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.inputProgram([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(10*time.Second, func() bool { return spaceOf(t, c, p.Space).Used.After(was) }) {
		t.Fatal("the input did not note the project's use")
	}
	raw, _ := json.Marshal(spaceOf(t, c, p.Space).Used)
	// Input again within usedEvery writes nothing.
	rev := s.layoutRevision()
	ss.inputProgram([]byte(" "))
	s.uses.mu.Lock()
	noted := s.uses.last[p.New]
	s.uses.mu.Unlock()
	if noted.IsZero() || s.layoutRevision() != rev {
		t.Fatalf("a second input wrote the layout: %s", raw)
	}
}
