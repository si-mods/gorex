package main

import "testing"

func TestDataDir(t *testing.T) {
	for _, c := range []struct {
		dir  string
		dev  bool
		want string
	}{
		{"/s/GoRex", false, "/s/GoRex"},
		{"/s/gorex", false, "/s/gorex"},
		{"/s/GoRex Dev", true, "/s/GoRex Dev"},
		{"/s/gorex dev", true, "/s/gorex dev"},
		{"/s/gorex", true, "/s/gorex Dev"},
		{"/s/GoRex", true, "/s/GoRex Dev"},
	} {
		if got := dataDir(c.dir, c.dev); got != c.want {
			t.Errorf("dataDir(%q, %v) = %q, want %q", c.dir, c.dev, got, c.want)
		}
	}
}
