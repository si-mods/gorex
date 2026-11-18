package layout

import (
	"encoding/json"
	"testing"
	"time"
)

// TestProjectRoundTrip writes a layout of a project in a space and reads
// it back; a layout of a newer GoRex, with fields this one does not know,
// keeps its workspaces.
func TestProjectRoundTrip(t *testing.T) {
	used := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	st := Store{Version: Version, Windows: []string{"p"}, Spaces: []string{"Life"}, Sessions: []Workspace{
		{ID: "p", Name: "windflow", Color: "blue", Space: "Work", Dir: "/home/me/code/windflow", Used: used},
		{ID: "q", Name: NoProjectName},
	}, Adopt: []Adoption{{ID: "a", Host: "h1", Machine: "m1", Project: &AdoptProject{Name: "x", Dir: "/srv/x"},
		Tabs: []AdoptTab{{Title: "dev", Split: &Node{SID: "s1"}}}}}}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	got := Parse(raw)
	if w := got.Space("p"); w == nil || w.Space != "Work" || w.Dir != "/home/me/code/windflow" || w.Name != "windflow" || !w.Used.Equal(used) {
		t.Fatalf("the project read back is %+v", w)
	}
	if w := got.NoProject(); w == nil || w.ID != "q" || w.Space != "" {
		t.Fatalf("the tabs with no project read back are %+v", w)
	}
	if len(got.Adopt) != 1 || got.Adopt[0].Project == nil || got.Adopt[0].Tabs[0].Split.SID != "s1" || len(got.Spaces) != 1 {
		t.Fatalf("the adoptions and spaces read back are %+v, %v", got.Adopt, got.Spaces)
	}
	if got.Project("/home/me/code/windflow/") == nil || got.Project("/home/me/code") != nil {
		t.Fatal("Project finds the project by its folder")
	}

	newer := `{"version":3,"sessions":[{"id":"n","name":"new","dir":"/srv/app","icon":"rocket","tabs":[],"later":{"x":1}}],"windows":["n"],"future":true}`
	got = Parse([]byte(newer))
	if w := got.Space("n"); w == nil || w.Dir != "/srv/app" || w.Name != "new" || len(got.Windows) != 1 {
		t.Fatalf("a space of a newer layout is %+v, windows %v", w, got.Windows)
	}
}

// TestMachineHost names a machine as a home host and reads it back.
func TestMachineHost(t *testing.T) {
	if m, ok := HostMachine((machineHost + "9c63")); !ok || m != "9c63" {
		t.Errorf("HostMachine(MachineHost) = %q, %v", m, ok)
	}
	for _, host := range []string{"", "a1b2c3", "machine:"} {
		if m, ok := HostMachine(host); ok {
			t.Errorf("HostMachine(%q) = %q, true", host, m)
		}
	}
}

func TestIsAbsDir(t *testing.T) {
	for dir, want := range map[string]bool{
		"/home/me": true, `C:\code`: true, "c:/code": true, `\\server\share`: true,
		"": false, "code": false, "./code": false, "C:code": false, "~/code": false, `1:\x`: false,
	} {
		if got := IsAbsDir(dir); got != want {
			t.Errorf("IsAbsDir(%q) = %v, want %v", dir, got, want)
		}
	}
}
