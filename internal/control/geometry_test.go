package control

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"gorex/internal/rex"
)

// TestPaneGeometry drives a bridge with protocol 3: each pane of a tab
// gets the grid rootshell gives it.
func TestPaneGeometry(t *testing.T) {
	t.Setenv("HERDR_CONTROL_PROTOCOL", "3")
	desk := serve(t)
	shell := []string{"/bin/sh"}
	if runtime.GOOS == "windows" {
		shell = []string{"cmd.exe"}
	}
	var sids []string
	for range 2 {
		in, err := desk.Create(rex.CreateOptions{Command: shell, Dir: os.TempDir(), Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, in.ID)
	}
	layout := fmt.Sprintf(`{"tabs":[{"name":"work","root":{"ratio":0.5,"a":{"sid":%q},"b":{"sid":%q}}}]}`, sids[0], sids[1])
	if err := desk.SetLayout(json.RawMessage(layout)); err != nil {
		t.Fatal(err)
	}
	r, _ := start(t)
	open := result(t, r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" }))
	caps := open["capabilities"].(map[string]any)
	if open["control_protocol"] != 3.0 || caps["terminal_control_stream"] != 3.0 || !slices.Contains(caps["control_features"].([]any), any("pane_geometry")) {
		t.Errorf("control.open %v", open)
	}
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	tabID := snap["tabs"].([]any)[0].(map[string]any)["tab_id"].(string)
	size := func(sid string) string {
		in := sessions(t, desk)[sid]
		return fmt.Sprintf("%dx%d %v", in.Cols, in.Rows, in.SizeLock != "")
	}
	geometry := func(claim bool, a, b [2]int) map[string]any {
		return map[string]any{"tab_id": tabID, "cols": 100, "rows": 30, "cell_width_px": 9, "cell_height_px": 18, "chrome": "none", "claim": claim,
			"panes": map[string]any{
				"p-" + sids[0]: map[string]any{"cols": a[0], "rows": a[1], "cell_width_px": 9, "cell_height_px": 18},
				"p-" + sids[1]: map[string]any{"cols": b[0], "rows": b[1], "cell_width_px": 12, "cell_height_px": 24},
			}}
	}
	// A viewer's sizes change nothing.
	result(t, r.call("tab.set_pane_geometry", geometry(false, [2]int{40, 30}, [2]int{30, 20})))
	if size(sids[0]) != "80x24 false" || size(sids[1]) != "80x24 false" {
		t.Errorf("a viewer sized %s, %s", size(sids[0]), size(sids[1]))
	}
	// Claimed, each pane gets its own grid, which the layout tells.
	r.send("r100", "tab.set_pane_geometry", geometry(true, [2]int{50, 30}, [2]int{30, 20}))
	lay := r.next("tab.layout", ofType("tab.layout"))["layout"].(map[string]any)
	want := map[string]string{"p-" + sids[0]: "50x30 9x18", "p-" + sids[1]: "30x20 12x24"}
	check := func(what string, lay map[string]any) {
		t.Helper()
		if a := lay["area"].(map[string]any); a["width"] != 100.0 || a["height"] != 30.0 {
			t.Errorf("%s: area %v", what, a)
		}
		for _, p := range lay["panes"].([]any) {
			p := p.(map[string]any)
			ts, _ := p["terminal_size"].(map[string]any)
			if got := fmt.Sprintf("%vx%v %vx%v", ts["cols"], ts["rows"], ts["cell_width_px"], ts["cell_height_px"]); got != want[p["pane_id"].(string)] {
				t.Errorf("%s: %s terminal_size %s, want %s", what, p["pane_id"], got, want[p["pane_id"].(string)])
			}
			if p["rect"].(map[string]any)["width"] != 50.0 {
				t.Errorf("%s: rect %v", what, p["rect"])
			}
		}
	}
	check("tab.layout", lay)
	r.next("the claim's answer", func(m map[string]any) bool { return m["id"] == "r100" })
	if size(sids[0]) != "50x30 true" || size(sids[1]) != "30x20 true" {
		t.Errorf("claimed sessions %s, %s", size(sids[0]), size(sids[1]))
	}
	snap = result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	check("session.snapshot", snap["layouts"].([]any)[0].(map[string]any))

	// The holder's sizes go through without a claim; a grid is 4×2 at least.
	result(t, r.call("tab.set_pane_geometry", geometry(false, [2]int{2, 1}, [2]int{60, 25})))
	time.Sleep(3 * geometrySettle)
	if size(sids[0]) != "4x2 true" || size(sids[1]) != "60x25 true" {
		t.Errorf("the holder's sizes %s, %s", size(sids[0]), size(sids[1]))
	}
	// tab.set_geometry tiles the tab again, as with protocol 2.
	result(t, r.call("tab.set_geometry", map[string]any{"tab_id": tabID, "cols": 100, "rows": 30, "chrome": "none", "claim": false}))
	time.Sleep(3 * geometrySettle)
	if size(sids[0]) != "50x30 true" || size(sids[1]) != "50x30 true" {
		t.Errorf("tiled again %s, %s", size(sids[0]), size(sids[1]))
	}
	snap = result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	for _, p := range snap["layouts"].([]any)[0].(map[string]any)["panes"].([]any) {
		if _, ok := p.(map[string]any)["terminal_size"]; ok {
			t.Errorf("terminal_size after tab.set_geometry: %v", p)
		}
	}
}

// TestPaneGeometryProtocol2 checks that a client of protocol 2 has no
// pane geometry.
func TestPaneGeometryProtocol2(t *testing.T) {
	t.Setenv("HERDR_CONTROL_PROTOCOL", "")
	serve(t)
	r, _ := start(t)
	open := result(t, r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" }))
	if open["control_protocol"] != 2.0 || slices.Contains(open["capabilities"].(map[string]any)["control_features"].([]any), any("pane_geometry")) {
		t.Errorf("control.open %v", open)
	}
	if e, ok := r.call("tab.set_pane_geometry", map[string]any{"tab_id": "t1", "cols": 10, "rows": 10})["error"].(map[string]any); !ok || e["code"] != "unknown_method" {
		t.Errorf("tab.set_pane_geometry answered %v", e)
	}
}

// TestPaneGeometryFollowsLayout checks the grids of a held tab with
// protocol 3 as its layout changes: a zoomed pane fills the tab, the
// others keep their grids, and get them back as it unzooms; a split
// scales the old panes' grids, and gives the new one its place's.
func TestPaneGeometryFollowsLayout(t *testing.T) {
	t.Setenv("HERDR_CONTROL_PROTOCOL", "3")
	desk := serve(t)
	shell := []string{"/bin/sh"}
	if runtime.GOOS == "windows" {
		shell = []string{"cmd.exe"}
	}
	var sids []string
	for range 3 {
		in, err := desk.Create(rex.CreateOptions{Command: shell, Dir: os.TempDir(), Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, in.ID)
	}
	set := func(zoom bool, three bool) {
		t.Helper()
		b := fmt.Sprintf(`{"sid":%q}`, sids[1])
		if three {
			b = fmt.Sprintf(`{"vertical":true,"ratio":0.5,"a":{"sid":%q},"b":{"sid":%q}}`, sids[1], sids[2])
		}
		l := fmt.Sprintf(`{"tabs":[{"focus":0,"zoom":%v,"root":{"ratio":0.5,"a":{"sid":%q},"b":%s}}]}`, zoom, sids[0], b)
		if err := desk.SetLayout(json.RawMessage(l)); err != nil {
			t.Fatal(err)
		}
	}
	set(false, false)
	r, _ := start(t)
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	tabID := snap["tabs"].([]any)[0].(map[string]any)["tab_id"].(string)
	result(t, r.call("tab.set_pane_geometry", map[string]any{"tab_id": tabID, "cols": 100, "rows": 30, "cell_width_px": 9, "cell_height_px": 18, "claim": true,
		"panes": map[string]any{
			"p-" + sids[0]: map[string]any{"cols": 40, "rows": 20, "cell_width_px": 12, "cell_height_px": 24},
			"p-" + sids[1]: map[string]any{"cols": 50, "rows": 30, "cell_width_px": 9, "cell_height_px": 18},
		}}))
	size := func(sid string) string {
		in := sessions(t, desk)[sid]
		return fmt.Sprintf("%dx%d", in.Cols, in.Rows)
	}
	wait := func(what string, want ...string) {
		t.Helper()
		var got []string
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			got = nil
			for i := range want {
				got = append(got, size(sids[i]))
			}
			if slices.Equal(got, want) {
				return
			}
		}
		t.Errorf("%s: sizes %v, want %v", what, got, want)
	}
	wait("claimed", "40x20", "50x30")
	set(true, false)
	wait("zoomed", "100x30", "50x30")
	set(false, false)
	wait("unzoomed", "40x20", "50x30")
	// The right pane splits down: its grid halves, the new pane's is its place.
	set(false, true)
	wait("split", "40x20", "50x15", "50x15")
}

// TestZoomWithRemotePane checks that a tab of one pane of this machine
// and one of another shows zoomed, as the desktop zooms it.
func TestZoomWithRemotePane(t *testing.T) {
	desk := serve(t)
	in, err := desk.Create(rex.CreateOptions{Command: []string{map[bool]string{true: "cmd.exe", false: "/bin/sh"}[runtime.GOOS == "windows"]}, Dir: os.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	desk.SetLayout(json.RawMessage(fmt.Sprintf(`{"tabs":[{"focus":0,"zoom":true,"root":{"ratio":0.5,"a":{"sid":%q},"b":{"sid":"far","host":"h1"}}}]}`, in.ID)))
	r, _ := start(t)
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	if l := snap["layouts"].([]any)[0].(map[string]any); l["zoomed"] != true {
		t.Errorf("layout %v", l)
	}
}

// TestQueueFits checks that the changes queued are each answered within
// rootshell's wait.
func TestQueueFits(t *testing.T) {
	if n := queued(); n < 1 || time.Duration(n)*commandWait >= rootshellWait {
		t.Errorf("%d changes of %v each at once", n, commandWait)
	}
}
