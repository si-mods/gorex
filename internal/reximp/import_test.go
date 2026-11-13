package reximp

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/internal/rexpeer"
)

// TestNames checks what a tab and a pane keep of Rex's names: a window's
// own label stays, Rex's made-up one does not, and a block named "shell"
// leaves its pane to follow the program.
func TestNames(t *testing.T) {
	id := "window:01a18b87-cffa-7e48-b7dc-929d2b63efe2"
	if got := tabName(rexpeer.Window{WindowID: id, Label: "Window 2b63efe2"}); got != "" {
		t.Errorf("a window's default label made a tab %q", got)
	}
	if got := tabName(rexpeer.Window{WindowID: id, Label: "window 2B63EFE2"}); got != "" {
		t.Errorf("the default label in another case made a tab %q", got)
	}
	if got := tabName(rexpeer.Window{WindowID: id, Label: "editor"}); got != "editor" {
		t.Errorf("a window's label made a tab %q", got)
	}
	if got := tabName(rexpeer.Window{WindowID: id}); got != "" {
		t.Errorf("a window without a label made a tab %q", got)
	}
	for _, label := range []string{"shell", "Shell", " shell ", ""} {
		if namedBlock(label) {
			t.Errorf("%q is a name a person gave", label)
		}
	}
	if !namedBlock("FromRex") {
		t.Error("a named block lost its label")
	}
}

// TestNestedLayoutShape: a Rex window whose splits nest either way is
// imported as a tab of the same shape, with the same ratios.
func TestNestedLayoutShape(t *testing.T) {
	g, dir := startGoRex(t)

	pane := func() *layout.Node {
		info, err := g.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		return &layout.Node{SID: info.ID}
	}
	h := func(r float32, a, b *layout.Node) *layout.Node { return &layout.Node{Ratio: r, A: a, B: b} }
	v := func(r float32, a, b *layout.Node) *layout.Node {
		return &layout.Node{Vertical: true, Ratio: r, A: a, B: b}
	}
	p := pane
	shapes := map[string]*layout.Node{
		"V{H{a,b},c}":      v(0.5, h(0.5, p(), p()), p()),
		"H{V{a,b},c}":      h(0.5, v(0.5, p(), p()), p()),
		"H{a,V{b,c}}":      h(0.5, p(), v(0.5, p(), p())),
		"H{H{a,b},H{c,d}}": h(0.5, h(0.5, p(), p()), h(0.5, p(), p())),
		"V{H{a,V{b,c}},d}": v(0.5, h(0.5, p(), v(0.5, p(), p())), p()),
		"uneven":           v(0.7, h(0.25, p(), v(0.6, p(), p())), h(0.35, p(), p())),
		"uneven same way":  h(0.3, p(), h(0.8, p(), p())),
	}
	store := layout.Store{Version: 2}
	for name, root := range shapes {
		store.Sessions = append(store.Sessions, layout.Workspace{ID: layout.NewSpaceID(), Name: name,
			Layout: layout.Layout{Tabs: []layout.Tab{{Root: root}}}})
	}
	raw, _ := json.Marshal(store)
	if err := g.SetLayout(raw); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	peer := dialPeer(t, ctx, dir)
	res, err := Run(ctx, g, peer, Options{Shell: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != len(shapes) {
		t.Fatalf("imported %+v", res.Imported)
	}
	raw, err = g.Layout()
	if err != nil {
		t.Fatal(err)
	}
	spaces := map[string]layout.Workspace{}
	for _, w := range layout.Parse(raw).Sessions {
		spaces[w.ID] = w
	}
	for _, imp := range res.Imported {
		w := spaces[imp.Space]
		if len(w.Tabs) != 1 || !sameShape(w.Tabs[0].Root, shapes[imp.Label]) {
			got, _ := json.Marshal(w.Tabs)
			t.Errorf("%s: imported %s", imp.Label, got)
		}
	}
}

// startGoRex serves a GoRex layout server in a new directory and connects.
func startGoRex(t *testing.T) (*rex.Client, string) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's panes run a Unix shell")
	}
	dir, err := os.MkdirTemp("/tmp", "reximp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	go rex.Serve()
	for range 500 {
		if _, err := os.Stat(rex.SocketPath()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	g, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Shutdown() })

	return g, dir
}

// dialPeer connects a Rex client to a test Rex server in dir.
func dialPeer(t *testing.T, ctx context.Context, dir string) *rexpeer.Client {
	apiClient, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	api, err := rexapi.New(rexapi.Options{Client: apiClient})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { api.Close() })
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.Handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	peer, err := rexpeer.Dial(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	return peer
}

// TestMissingBlock: a block the window's layout names and its layer does
// not have is left out and reported, and the rest of the window keeps its
// shape and ratios.
func TestMissingBlock(t *testing.T) {
	g, dir := startGoRex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	peer := dialPeer(t, ctx, dir)

	leaf := func(id string) *rexpeer.Layout { return &rexpeer.Layout{BlockID: id} }
	node := func(dir string, r float64, a, b *rexpeer.Layout) *rexpeer.Layout {
		return &rexpeer.Layout{Split: &rexpeer.Split{Direction: dir, Ratio: r, Before: a, After: b}}
	}
	for _, c := range []struct {
		name    string
		tree    *rexpeer.Layout
		missing []string
		want    *layout.Node // nil: no tab
	}{
		{"b of V{H{a,b},c}", node("vertical", 0.7, node("horizontal", 0.3, leaf("a"), leaf("b")), leaf("c")),
			[]string{"b"}, &layout.Node{Vertical: true, Ratio: 0.7, A: &layout.Node{SID: "a"}, B: &layout.Node{SID: "c"}}},
		{"first leaf of V{H{a,b},c}", node("vertical", 0.7, node("horizontal", 0.3, leaf("a"), leaf("b")), leaf("c")),
			[]string{"a"}, &layout.Node{Vertical: true, Ratio: 0.7, A: &layout.Node{SID: "b"}, B: &layout.Node{SID: "c"}}},
		{"the inner side keeps its ratios", node("horizontal", 0.5, leaf("a"), node("vertical", 0.4, leaf("b"), node("horizontal", 0.6, leaf("c"), leaf("d")))),
			[]string{"b"}, &layout.Node{Ratio: 0.5, A: &layout.Node{SID: "a"}, B: &layout.Node{Ratio: 0.6, A: &layout.Node{SID: "c"}, B: &layout.Node{SID: "d"}}}},
		{"every block", node("vertical", 0.5, leaf("a"), node("horizontal", 0.5, leaf("b"), leaf("c"))),
			[]string{"a", "b", "c"}, nil},
	} {
		var have []rexpeer.Block
		var wantMissing = map[string]bool{}
		for _, id := range c.missing {
			wantMissing[id] = true
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			if !wantMissing[id] {
				have = append(have, rexpeer.Block{BlockID: id})
			}
		}
		view := rexpeer.View{Windows: []rexpeer.Window{{WindowID: "w", Layers: []rexpeer.Layer{{Kind: "tiled", Layout: c.tree, Blocks: have}}}}}
		imp, err := importSession(ctx, g, peer, rexpeer.Session{SessionID: "s", Label: c.name}, view, Options{Shell: true})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if strings.Join(imp.Missing, ",") != strings.Join(c.missing, ",") {
			t.Errorf("%s: left out %v, want %v", c.name, imp.Missing, c.missing)
		}
		raw, err := g.Layout()
		if err != nil {
			t.Fatal(err)
		}
		var tabs []layout.Tab
		for _, w := range layout.Parse(raw).Sessions {
			if w.ID == imp.Space {
				tabs = w.Tabs
			}
		}
		if c.want == nil {
			if len(tabs) != 0 || imp.Windows != 0 || imp.Blocks != 0 {
				t.Errorf("%s: imported %d windows, %d blocks, tabs %v", c.name, imp.Windows, imp.Blocks, tabs)
			}
			continue
		}
		if len(tabs) != 1 || !sameShape(tabs[0].Root, c.want) || imp.Windows != 1 || imp.Blocks != leaves(c.want) {
			got, _ := json.Marshal(tabs)
			t.Errorf("%s: imported %s, %d windows, %d blocks", c.name, got, imp.Windows, imp.Blocks)
		}
	}
}

// leaves counts a tree's panes.
func leaves(n *layout.Node) int {
	if n.IsLeaf() {
		return 1
	}
	return leaves(n.A) + leaves(n.B)
}

// sameShape tells whether two trees have the same splits, in the same
// directions and with the same ratios.
func sameShape(a, b *layout.Node) bool {
	if a.IsLeaf() || b.IsLeaf() {
		return a.IsLeaf() && b.IsLeaf()
	}
	return a.Vertical == b.Vertical && math.Abs(float64(a.Ratio-b.Ratio)) < 0.01 && sameShape(a.A, b.A) && sameShape(a.B, b.B)
}
