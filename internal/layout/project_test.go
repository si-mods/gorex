package layout

import (
	"encoding/json"
	"testing"
)

// TestProjectRoundTrip writes a layout of a project with a home and reads
// it back; an older layout reads as spaces of no folder; a layout of a
// newer GoRex, with fields this one does not know, keeps its spaces.
func TestProjectRoundTrip(t *testing.T) {
	st := Store{Version: Version, Windows: []string{"p"}, Sessions: []Workspace{
		{ID: "p", Name: "windflow", Color: "blue", Host: "a1b2c3", Dir: "/home/me/code/windflow"},
		{ID: "q", Name: "plain"},
	}}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	got := Parse(raw)
	if w := got.Space("p"); w == nil || w.Host != "a1b2c3" || w.Dir != "/home/me/code/windflow" || w.Name != "windflow" {
		t.Fatalf("the project read back is %+v", w)
	}
	if w := got.Space("q"); w == nil || w.Host != "" || w.Dir != "" {
		t.Fatalf("the space of no folder read back is %+v", w)
	}

	older := `{"version":2,"sessions":[{"id":"o","name":"old","color":"red","active":0,"tabs":[]}],"windows":["o"]}`
	got = Parse([]byte(older))
	if w := got.Space("o"); w == nil || w.Host != "" || w.Dir != "" || w.Name != "old" {
		t.Fatalf("a space of an older layout is %+v", w)
	}

	newer := `{"version":2,"sessions":[{"id":"n","name":"new","dir":"/srv/app","icon":"rocket","tabs":[],"later":{"x":1}}],"windows":["n"],"future":true}`
	got = Parse([]byte(newer))
	if w := got.Space("n"); w == nil || w.Dir != "/srv/app" || w.Name != "new" || len(got.Windows) != 1 {
		t.Fatalf("a space of a newer layout is %+v, windows %v", w, got.Windows)
	}
}

// TestMachineHost names a machine as a home host and reads it back.
func TestMachineHost(t *testing.T) {
	if m, ok := HostMachine(MachineHost("9c63")); !ok || m != "9c63" {
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
