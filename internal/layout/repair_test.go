package layout

import (
	"encoding/json"
	"slices"
	"testing"
)

// v041 is a layout of Version that keeps panes of host h1, as a layout of
// before projects did and a client of one may put back in one of
// projects: a project's tab of one, a tab of this machine's beside one, an
// old space of nothing else, and a canvas box of one.
const v041 = `{"version":3,"windows":["proj","work"],
"sessions":[
 {"id":"proj","name":"gorex","dir":"/code/gorex","active":2,"tabs":[
  {"id":"dev","name":"dev","root":{"ratio":0.4,"a":{"sid":"l1"},"b":{"sid":"l2"}}},
  {"id":"host","name":"Projects V2","root":{"sid":"r1","host":"h1"}},
  {"id":"mixed","name":"mixed","root":{"ratio":0.5,"a":{"vertical":true,"ratio":0.3,"a":{"sid":"r2","host":"h1"},"b":{"sid":"r3","host":"h1"}},"b":{"sid":"l3"}}},
  {"id":"box","name":"box","root":{"sid":"l4"},"canvas":{"w":1200,"h":800},"floats":[{"pane":{"sid":"r4","host":"h1"},"x":0.1,"y":0.1,"w":0.4,"h":0.4}]}]},
 {"id":"work","name":"Work","active":0,"tabs":[
  {"id":"away","name":"away","root":{"sid":"r5","host":"h1"}},
  {"id":"here","name":"here","root":{"sid":"l5"}}]},
 {"id":"trainer","name":"trainer","dir":"/work/trainer","tabs":[
  {"id":"mon","name":"mon","root":{"sid":"l6"}}]},
 {"id":"empty","name":"Empty","tabs":[{"id":"gone","root":{"sid":"r6","host":"h1"}}]}
]}`

// TestRepairHostPanes: a layout of projects that keeps another host's
// panes gives them up into its adoption, its tabs and its spaces, as the
// migration leaves a layout of before projects, and keeps this machine's
// panes, their order and their focus.
func TestRepairHostPanes(t *testing.T) {
	st, taken := RepairHostPanes(Parse([]byte(v041)), machines)
	if taken != 6 {
		t.Fatalf("took %d panes, want 6", taken)
	}
	// The project keeps its tabs that are this machine's, its split and
	// the box, and the tab of h1's pane goes.
	proj := st.Space("proj")
	if proj == nil || proj.Dir != "/code/gorex" || len(proj.Tabs) != 3 {
		t.Fatalf("project proj is %+v", proj)
	}
	if ids := tabIDs(proj.Tabs); !slices.Equal(ids, []string{"dev", "mixed", "box"}) {
		t.Errorf("the project's tabs %v", ids)
	}
	// The mixed tab keeps this machine's pane alone, and its split's
	// ratio as far as one pane holds it.
	mixed := proj.Tabs[1]
	if mixed.Root == nil || mixed.Root.SID != "l3" || mixed.Root.A != nil {
		t.Errorf("the mixed tab is %+v", mixed.Root)
	}
	if got := Leaves(mixed.Root); len(got) != 1 || got[0].SID != "l3" {
		t.Errorf("the panes of mixed: %+v", got)
	}
	// The canvas keeps its box's tiled pane, and its float of h1 goes.
	box := proj.Tabs[2]
	if box.Root == nil || box.Root.SID != "l4" || len(box.Floats) != 0 {
		t.Errorf("the box is %+v", box)
	}
	// The active tab follows the one it was, moved up by the tab gone.
	if proj.Active != 1 || proj.Tabs[proj.Active].ID != "mixed" {
		t.Errorf("the project's active tab %d, %q", proj.Active, proj.Tabs[proj.Active].ID)
	}
	// Work keeps the tab of this machine's pane; the old space Empty,
	// of nothing else, goes, and its name stays one the picker offers.
	work := st.Space("work")
	if work == nil || !slices.Equal(tabIDs(work.Tabs), []string{"here"}) {
		t.Fatalf("space work is %+v", work)
	}
	if st.Space("empty") != nil || !slices.Equal(st.Spaces, []string{"Empty"}) {
		t.Errorf("the old space Empty: %+v, spaces %v", st.Space("empty"), st.Spaces)
	}
	if ids := st.Windows; !slices.Equal(ids, []string{"proj", "work"}) {
		t.Errorf("the windows %v", ids)
	}
	if st.Space("trainer") == nil {
		t.Error("a project of no other host's pane went")
	}
	// h1's panes wait for it in an adoption of its machine, its tabs'
	// parts of the splits, named as their tabs were.
	if len(st.Adopt) != 1 {
		t.Fatalf("the adoptions %+v", st.Adopt)
	}
	ad := st.Adopt[0]
	if ad.Machine != "m1" || ad.Host != "h1" || ad.Project != nil {
		t.Errorf("the adoption of h1 is %+v", ad)
	}
	if len(ad.Tabs) != 5 {
		t.Fatalf("the adoption's tabs %+v", ad.Tabs)
	}
	want := []struct {
		title string
		sids  []string
	}{
		{"Projects V2", []string{"r1"}},
		{"mixed", []string{"r2", "r3"}},
		{"box", []string{"r4"}},
		{"away", []string{"r5"}},
		{"", []string{"r6"}},
	}
	for i, w := range want {
		if ad.Tabs[i].Title != w.title || !slices.Equal(sidsOf(ad.Tabs[i].Split), w.sids) {
			t.Errorf("adoption tab %d: %q %v, want %q %v", i, ad.Tabs[i].Title, sidsOf(ad.Tabs[i].Split), w.title, w.sids)
		}
	}
	// The layout is the store it was, of version Version.
	if st.Version != Version {
		t.Errorf("the repaired layout is of version %d", st.Version)
	}
	if b, err := json.Marshal(st); err != nil || len(b) == 0 {
		t.Errorf("the repaired layout does not encode: %v", err)
	}
}

// TestRepairHostPanesNoProjectWorkspace: the workspace of tabs with no
// project, which a client of before projects keeps as the ID of an old
// space, gives up its panes of another host without its name joining the
// spaces the picker offers: it is no space.
func TestRepairHostPanesNoProjectWorkspace(t *testing.T) {
	raw := `{"version":3,"windows":["np"],"sessions":[
 {"id":"np","name":"` + NoProjectName + `","dir":"","active":0,"tabs":[
  {"id":"away","name":"away","root":{"sid":"r1","host":"h1"}}]},
 {"id":"proj","name":"gorex","dir":"/code/gorex","active":0,"tabs":[
  {"id":"dev","root":{"sid":"l1"}}]}]}`
	st, taken := RepairHostPanes(Parse([]byte(raw)), machines)
	if taken != 1 {
		t.Fatalf("took %d panes, want 1", taken)
	}
	if st.Space("np") != nil || slices.Contains(st.Spaces, NoProjectName) {
		t.Fatalf("the workspace of no project stayed, or named a space: %+v, %v", st.Space("np"), st.Spaces)
	}
	if !slices.Equal(st.Windows, []string{}) && slices.Contains(st.Windows, "np") {
		t.Errorf("the windows %v", st.Windows)
	}
	if len(st.Adopt) != 1 || st.Adopt[0].Tabs[0].Title != "away" {
		t.Fatalf("the adoptions %+v", st.Adopt)
	}
}

// TestRepairHostPanesChangesNothing: a layout that keeps this machine's
// panes only, and one of before projects, which Migrate makes, change
// not at all.
func TestRepairHostPanesChangesNothing(t *testing.T) {
	raw := `{"version":3,"windows":["proj"],"sessions":[
 {"id":"proj","name":"gorex","dir":"/code/gorex","active":0,"tabs":[
  {"id":"dev","root":{"ratio":0.4,"a":{"sid":"l1"},"b":{"sid":"l2"}}}]}]}`
	before := Parse([]byte(raw))
	st, taken := RepairHostPanes(before, machines)
	if taken != 0 || len(st.Adopt) != 0 {
		t.Fatalf("a layout of this machine's panes was repaired: %d panes, %+v", taken, st.Adopt)
	}
	old := Parse([]byte(v039))
	if _, taken := RepairHostPanes(old, machines); taken != 0 {
		t.Errorf("a layout of before projects was repaired: %d", taken)
	}
	// Twice changes nothing either.
	once, _ := RepairHostPanes(Parse([]byte(v041)), machines)
	again, taken := RepairHostPanes(once, machines)
	if taken != 0 || len(again.Adopt) != len(once.Adopt) {
		t.Errorf("a second repair took %d panes, adoptions %d", taken, len(again.Adopt))
	}
}

// tabIDs are the IDs of tabs.
func tabIDs(tabs []Tab) []string {
	out := make([]string, 0, len(tabs))
	for _, t := range tabs {
		out = append(out, t.ID)
	}
	return out
}

// sidsOf are the sessions of a split's panes, in order.
func sidsOf(n *Node) []string {
	var out []string
	for _, l := range Leaves(n) {
		out = append(out, l.SID)
	}
	return out
}
