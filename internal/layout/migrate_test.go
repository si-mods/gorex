package layout

import (
	"encoding/json"
	"slices"
	"testing"
)

// v039 is a layout of version 2 (v0.38.0 to v0.40.x) with the Cases of
// docs/projects.md's Migration: host "h1" is office-h800, of machine
// "m1"; a home of "machine:m1" is the same host.
const v039 = `{"version":2,"windows":["gorex","work"],
"teleports":[{"host":"h1","sid":"t1","from":"l9"}],"kills":[{"host":"h1","sid":"k1"}],
"sessions":[
 {"id":"gorex","name":"gorex","dir":"/code/gorex","active":0,"tabs":[
  {"id":"dev","name":"dev","focus":1,"root":{"ratio":0.4,"a":{"sid":"l1"},"b":{"sid":"l2"}}}]},
 {"id":"trainer","name":"trainer","host":"machine:m1","dir":"/work/trainer","active":1,"tabs":[
  {"id":"train","name":"train","root":{"sid":"r1","host":"h1"}},
  {"id":"mon","name":"mon","focus":2,"root":{"ratio":0.5,"a":{"vertical":true,"ratio":0.3,"a":{"sid":"r2","host":"h1"},"b":{"sid":"r3","host":"h1"}},"b":{"sid":"l3"}}}]},
 {"id":"work","name":"Work","color":"blue","active":0,"tabs":[
  {"id":"api","name":"api","focus":1,"zoom":true,"root":{"ratio":0.5,"a":{"sid":"l4"},"b":{"sid":"r4","host":"h1"}}}]},
 {"id":"gorex2","name":"gorex again","dir":"/code/gorex/","tabs":[
  {"id":"rev","name":"review","root":{"sid":"l5"}}]},
 {"id":"life","name":"Life","tabs":[]}
]}`

func machines(host string) string {
	if host == "h1" {
		return "m1"
	}
	return ""
}

// sids are the sessions a store's tabs show, in order.
func sids(ws []Workspace) []string {
	var out []string
	for _, w := range ws {
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, l := range Leaves(r) {
					out = append(out, l.SID)
				}
			}
		}
	}
	return out
}

// TestMigrateCases checks each case of the Migration table of
// docs/projects.md on the layout: what stays here, and what each host
// adopts.
func TestMigrateCases(t *testing.T) {
	st, changed := Migrate([]byte(v039), machines)
	if !changed || st.Version != Version {
		t.Fatalf("not migrated: version %d, changed %v", st.Version, changed)
	}
	// Case 1: project gorex here, tab dev, same split; the project of the
	// same folder joins it with its tab.
	gorex := st.Space("gorex")
	if gorex == nil || gorex.Dir != "/code/gorex" || gorex.Space != "" || len(gorex.Tabs) != 2 {
		t.Fatalf("project gorex is %+v", gorex)
	}
	dev := gorex.Tabs[0]
	if dev.ID != "dev" || dev.Root.Ratio != 0.4 || dev.Root.A.SID != "l1" || dev.Root.B.SID != "l2" || dev.Focus != 1 {
		t.Fatalf("tab dev is %+v", dev)
	}
	if gorex.Tabs[1].ID != "rev" || st.Space("gorex2") != nil {
		t.Fatalf("the second project of /code/gorex did not join the first: %+v", st.Sessions)
	}
	// Case 3 and 4: the tabs with no project have api's left pane and
	// mon's local one; Work and Life are spaces offered.
	np := st.NoProject()
	if np == nil || np.Name != NoProjectName || np.Dir != "" {
		t.Fatalf("no tabs with no project: %+v", st.Sessions)
	}
	// They take the ID of the first old space, not of a project homed
	// elsewhere: Rex's apps of before keep that session.
	if np.ID != "work" {
		t.Fatalf("the tabs with no project took the ID %q", np.ID)
	}
	var api, mon *Tab
	for i := range np.Tabs {
		switch np.Tabs[i].ID {
		case "api":
			api = &np.Tabs[i]
		case "mon":
			mon = &np.Tabs[i]
		}
	}
	if api == nil || !api.Root.IsLeaf() || api.Root.SID != "l4" || api.Zoom || api.Focus != 0 {
		t.Fatalf("tab api here is %+v", api)
	}
	if mon == nil || !mon.Root.IsLeaf() || mon.Root.SID != "l3" || mon.Root.Host != "" {
		t.Fatalf("tab mon here is %+v", mon)
	}
	// The active tab is that of the space a window showed: api of Work,
	// not mon of trainer, which no window showed.
	if np.Tabs[np.Active].ID != "api" {
		t.Fatalf("the active tab with no project is %q", np.Tabs[np.Active].ID)
	}
	if !slices.Equal(st.Spaces, []string{"Work", "Life"}) {
		t.Fatalf("spaces offered %v", st.Spaces)
	}
	// Every pane of this machine is in a tab still, and no other host's.
	got := sids(st.Sessions)
	slices.Sort(got)
	if !slices.Equal(got, []string{"l1", "l2", "l3", "l4", "l5"}) {
		t.Fatalf("panes here %v", got)
	}
	// Case 2 and 4: trainer is made on h1 with train and mon's two panes;
	// case 3: api's right pane is a tab with no project there.
	if len(st.Adopt) != 2 {
		t.Fatalf("adoptions %+v", st.Adopt)
	}
	pa, na := st.Adopt[0], st.Adopt[1]
	if pa.Project == nil || pa.Project.Dir != "/work/trainer" || pa.Project.Name != "trainer" || pa.Machine != "m1" || pa.Host != "h1" {
		t.Fatalf("the adoption of trainer is %+v", pa)
	}
	if len(pa.Tabs) != 2 || pa.Tabs[0].Title != "train" || pa.Tabs[0].Split.SID != "r1" || pa.Tabs[0].Split.Host != "" {
		t.Fatalf("trainer's tabs are %+v", pa.Tabs)
	}
	ms := pa.Tabs[1].Split
	if pa.Tabs[1].Title != "mon" || !ms.Vertical || ms.Ratio != 0.3 || ms.A.SID != "r2" || ms.B.SID != "r3" {
		t.Fatalf("mon on h1 is %+v", ms)
	}
	if na.Project != nil || na.Machine != "m1" || len(na.Tabs) != 1 || na.Tabs[0].Title != "api" || na.Tabs[0].Split.SID != "r4" {
		t.Fatalf("the adoption of tabs with no project is %+v", na)
	}
	if pa.ID == "" || na.ID == "" || pa.ID == na.ID {
		t.Fatalf("adoption IDs %q %q", pa.ID, na.ID)
	}
	// Teleports and kills of other hosts' panes go; the windows show
	// what their workspaces became.
	if len(st.Teleports) != 0 || len(st.Kills) != 0 {
		t.Fatalf("teleports %v kills %v", st.Teleports, st.Kills)
	}
	if len(st.Windows) != 2 || st.Windows[0] != "gorex" || st.Windows[1] != np.ID {
		t.Fatalf("windows %v", st.Windows)
	}

	// It survives a round trip, and runs once.
	raw, _ := json.Marshal(st)
	again, changed := Migrate(raw, machines)
	if changed || len(again.Adopt) != 2 || len(again.Sessions) != len(st.Sessions) {
		t.Fatalf("a second migration changed %v: %+v", changed, again)
	}
}

// TestMigrateHostUnknown keeps the adoption of a host hosts.json no
// longer has by its ID, and of a machine no host is by its machine.
func TestMigrateHostUnknown(t *testing.T) {
	raw := `{"version":2,"sessions":[
 {"id":"p","name":"site","host":"machine:m9","dir":"/site","tabs":[{"name":"web","root":{"sid":"x","host":"gone"}}]}]}`
	st, _ := Migrate([]byte(raw), nil)
	if len(st.Sessions) != 0 {
		t.Fatalf("a project homed elsewhere stayed here: %+v", st.Sessions)
	}
	if len(st.Adopt) != 1 || st.Adopt[0].Host != "gone" || st.Adopt[0].Machine != "" || st.Adopt[0].Project != nil {
		t.Fatalf("adoptions %+v", st.Adopt)
	}
}

// TestMigrateFloats takes another host's floats into its split, and
// keeps this machine's.
func TestMigrateFloats(t *testing.T) {
	raw := `{"version":2,"sessions":[{"id":"s","name":"S","tabs":[{"id":"t","name":"t","focusFloat":2,
 "root":{"sid":"r1","host":"h1"},
 "floats":[{"pane":{"sid":"l1"},"x":0.1,"y":0.1,"w":0.3,"h":0.3},{"pane":{"sid":"r2","host":"h1"},"x":0.5,"y":0.5,"w":0.3,"h":0.3}]}]}]}`
	st, _ := Migrate([]byte(raw), machines)
	np := st.NoProject()
	if np == nil || len(np.Tabs) != 1 {
		t.Fatalf("no tab here: %+v", st.Sessions)
	}
	tab := np.Tabs[0]
	// The only float of this machine's becomes the tab's pane.
	if tab.Root == nil || tab.Root.SID != "l1" || len(tab.Floats) != 0 || tab.FocusFloat != 0 {
		t.Fatalf("the tab here is %+v", tab)
	}
	sp := st.Adopt[0].Tabs[0].Split
	if sp.A == nil || sp.A.SID != "r1" || sp.B.SID != "r2" {
		t.Fatalf("h1's split is %+v", sp)
	}
}

// TestMigrateKeepsActive keeps the active tab of an old space, which the
// window shows and whose session the server restores first.
func TestMigrateKeepsActive(t *testing.T) {
	raw := `{"version":2,"windows":["w1"],"sessions":[{"id":"w1","name":"w","active":2,"tabs":[
 {"id":"a","root":{"sid":"a"}},{"id":"b","root":{"sid":"b","host":"h1"}},{"id":"c","root":{"sid":"c"}}]}]}`
	st, _ := Migrate([]byte(raw), machines)
	np := st.NoProject()
	if np == nil || np.ID != "w1" || len(np.Tabs) != 2 || np.Tabs[np.Active].ID != "c" {
		t.Fatalf("tabs with no project %+v", np)
	}
	if !slices.Equal(st.Windows, []string{"w1"}) {
		t.Fatalf("windows %v", st.Windows)
	}
}
