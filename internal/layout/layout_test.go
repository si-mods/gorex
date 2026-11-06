package layout

import (
	"regexp"
	"strings"
	"testing"
)

// tree is a tab of three panes: a left of (b above c).
func tree() *Tab {
	a, b, c := &Node{SID: "a"}, &Node{SID: "b"}, &Node{SID: "c"}
	return &Tab{Root: &Node{Ratio: 0.5, A: a, B: &Node{Vertical: true, Ratio: 0.5, A: b, B: c}}, Focus: 2}
}

func TestTree(t *testing.T) {
	tab := tree()
	for _, c := range []struct {
		sid  string
		want int
	}{{"a", 0}, {"b", 1}, {"c", 2}, {"x", -1}} {
		if got := LeafIndex(tab.Root, c.sid); got != c.want {
			t.Errorf("LeafIndex(%s) = %d, want %d", c.sid, got, c.want)
		}
		if n := Find(tab.Root, c.sid); (n != nil) != (c.want >= 0) || n != nil && n.SID != c.sid {
			t.Errorf("Find(%s) = %+v", c.sid, n)
		}
	}
	if FirstLeaf(tab.Root).SID != "a" || len(Leaves(tab.Root)) != 3 || !Find(tab.Root, "b").IsLeaf() || tab.Root.IsLeaf() {
		t.Error("leaves")
	}
	b := Find(tab.Root, "b")
	if p := Parent(tab.Root, b); p != tab.Root.B || Parent(tab.Root, tab.Root) != nil {
		t.Errorf("Parent(b) = %+v", p)
	}
	// The edges of b: its bottom is the vertical split's, its left the root's.
	for _, c := range []struct {
		dir  string
		want *Node
	}{{"down", tab.Root.B}, {"up", tab.Root.B}, {"left", tab.Root}, {"right", tab.Root}} {
		if got := SplitAt(tab.Root, b, c.dir); got != c.want {
			t.Errorf("SplitAt(b, %s) = %p, want %p", c.dir, got, c.want)
		}
	}
	if SplitAt(tab.Root, Find(tab.Root, "a"), "down") != nil {
		t.Error("a has a vertical edge")
	}
	// c goes, b takes the vertical split's place; the focus stays in range.
	if keep := tab.Remove(Find(tab.Root, "c")); keep != b || tab.Root.B != b || tab.Focus != 1 {
		t.Errorf("after removing c: kept %+v, root %+v, focus %d", keep, tab.Root, tab.Focus)
	}
	tab.Replace(b, &Node{SID: "d"})
	if LeafIndex(tab.Root, "d") != 1 {
		t.Error("replaced")
	}
	one := &Tab{Root: &Node{SID: "x"}, Zoom: true}
	if one.Remove(one.Root) != nil || one.Root != nil || one.Zoom {
		t.Errorf("the last pane: %+v", one)
	}
}

func TestStoreFind(t *testing.T) {
	s := Store{Sessions: []Workspace{{ID: "w1", Layout: Layout{Tabs: []Tab{*tree()}}}, {ID: "w2", Layout: Layout{Tabs: []Tab{{Root: &Node{SID: "z"}}}}}}}
	if s.Space("w2") != &s.Sessions[1] || s.Space("none") != nil {
		t.Error("Space")
	}
}

func TestNames(t *testing.T) {
	if !regexp.MustCompile(`^[a-z]+-[a-z]+$`).MatchString(SpaceName(nil)) {
		t.Error("SpaceName")
	}
	if n := SpaceName(func(n string) bool { return !strings.HasSuffix(n, "-2") }); !regexp.MustCompile(`^[a-z]+-[a-z]+-\d+$`).MatchString(n) {
		t.Errorf("every name taken: %q", n)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(NewTabID()) {
		t.Error("NewTabID")
	}
	if id := NewSpaceID(); len(id) != 12 || id == NewSpaceID() {
		t.Errorf("NewSpaceID %q", id)
	}
	for _, c := range []struct{ in, label, pane string }{
		{"  logs\x01 ", "logs", "logs"},
		{strings.Repeat("界", 130), strings.Repeat("界", 128), strings.Repeat("界", 64)},
	} {
		if got := Label(c.in); got != c.label {
			t.Errorf("Label(%q) = %q", c.in, got)
		}
		if got := PaneName(c.in); got != c.pane {
			t.Errorf("PaneName(%q) = %q", c.in, got)
		}
	}
}

func TestNextColor(t *testing.T) {
	all := make([]string, 0, len(Palette))
	for _, p := range Palette {
		all = append(all, p.ID)
	}
	for _, c := range []struct {
		used []string
		want string
	}{
		{nil, "rose"},
		{[]string{"rose", "#123456"}, "orange"},
		{all, "rose"},
		{append(all, "rose"), "orange"},
	} {
		if got := NextColor(c.used); got != c.want {
			t.Errorf("NextColor(%q) = %q, want %q", c.used, got, c.want)
		}
	}
}

// TestParseNamesBrowsers gives a browser pane of a layout without an ID
// one, in splits, floats and an older layout alike, and keeps one it has.
func TestParseNamesBrowsers(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"sessions":[{"id":"w","name":"n","tabs":[{"root":{"a":{"sid":"s"},"b":{"url":"https://a.test"}},"floats":[{"pane":{"url":"https://b.test"}}]}]}]}`,
		`{"tabs":[{"root":{"a":{"sid":"s"},"b":{"url":"https://a.test"}},"floats":[{"pane":{"url":"https://b.test"}}]}]}`,
	} {
		s := Parse([]byte(raw))
		tab := s.Sessions[0].Tabs[0]
		b, f := tab.Root.B, tab.Floats[0].Pane
		if b.ID == "" || f.ID == "" || b.ID == f.ID || tab.Root.A.ID != "" || !s.Named {
			t.Errorf("%s: ids %q %q %q, named %v", raw, tab.Root.A.ID, b.ID, f.ID, s.Named)
		}
	}
	// A layout of browser panes without IDs gets the same at every read,
	// as from a writer that drops them.
	raw := []byte(`{"version":2,"sessions":[{"id":"w","name":"n","tabs":[{"id":"t1","root":{"a":{"sid":"s"},"b":{"url":"https://a.test"}}}]}]}`)
	if x, y := Parse(raw).Sessions[0].Tabs[0].Root.B.ID, Parse(raw).Sessions[0].Tabs[0].Root.B.ID; x != y {
		t.Errorf("ids %q then %q", x, y)
	}
	kept := Parse([]byte(`{"version":2,"sessions":[{"id":"w","tabs":[{"root":{"url":"https://a.test","id":"web1"}}]}]}`))
	if id := kept.Sessions[0].Tabs[0].Root.ID; id != "web1" || kept.Named {
		t.Errorf("kept id %q, named %v", id, kept.Named)
	}
}
