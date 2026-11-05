package cli

import "testing"

func TestSplitRequest(t *testing.T) {
	c := splitRequest("s1", true, "/tmp", []string{"top", "-d", "1"})
	if c.Op != "pane.split" || c.SID != "s1" || !c.Vertical || c.Dir != "/tmp" {
		t.Fatalf("split request %+v", c)
	}
	if len(c.Command) != 3 || c.Command[0] != "top" || c.Command[2] != "1" {
		t.Fatalf("split command %q", c.Command)
	}
	plain := splitRequest("s2", false, "", nil)
	if plain.Op != "pane.split" || plain.Vertical || plain.Dir != "" || len(plain.Command) != 0 {
		t.Fatalf("plain split %+v", plain)
	}
}
