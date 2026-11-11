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
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/internal/rexpeer"
	"gorex/internal/testrex"
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

	api, err := testrex.New(testrex.Options{Socket: rex.SocketPath(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.Handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	peer, err := rexpeer.Dial(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
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

// sameShape tells whether two trees have the same splits, in the same
// directions and with the same ratios.
func sameShape(a, b *layout.Node) bool {
	if a.IsLeaf() || b.IsLeaf() {
		return a.IsLeaf() && b.IsLeaf()
	}
	return a.Vertical == b.Vertical && math.Abs(float64(a.Ratio-b.Ratio)) < 0.01 && sameShape(a.A, b.A) && sameShape(a.B, b.B)
}
