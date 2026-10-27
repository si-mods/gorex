package control

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// rootshell drives a bridge as rootshell does, over pipes.
type rootshell struct {
	t     *testing.T
	in    io.Writer
	lines chan map[string]any
	n     int
}

func (r *rootshell) send(id, method string, params any) {
	r.t.Helper()
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if _, err := r.in.Write(append(b, '\n')); err != nil {
		r.t.Fatal(err)
	}
}

// next returns the next line that match takes, skipping the others.
func (r *rootshell) next(what string, match func(m map[string]any) bool) map[string]any {
	r.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-r.lines:
			if !ok {
				r.t.Fatalf("%s: the bridge ended", what)
			}
			if match(m) {
				return m
			}
		case <-timeout:
			r.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// call sends a request and returns its answer.
func (r *rootshell) call(method string, params any) map[string]any {
	r.t.Helper()
	r.n++
	id := fmt.Sprintf("r%d", r.n)
	r.send(id, method, params)
	return r.next(method, func(m map[string]any) bool { return m["id"] == id })
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	return res
}

func ofType(typ string) func(m map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == typ }
}

func sessions(t *testing.T, c *rex.Client) map[string]rex.SessionInfo {
	t.Helper()
	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]rex.SessionInfo{}
	for _, in := range list {
		m[in.ID] = in
	}
	return m
}

// serve starts a server for the test, and returns a client of it, as the
// desktop's.
func serve(t *testing.T) *rex.Client {
	t.Helper()
	dir, err := os.MkdirTemp(map[bool]string{true: "", false: "/tmp"}[runtime.GOOS == "windows"], "ctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	go rex.Serve()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(dir, "server.sock")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	desk, err := rex.ConnectSocket(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { desk.Shutdown() })
	return desk
}

// TestPollKeepsOnError checks that a poll that cannot read the server
// changes nothing: no tab is given back for a list it did not get.
func TestPollKeepsOnError(t *testing.T) {
	desk := serve(t)
	c, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	b := &bridge{c: c, owned: map[string]bool{"t1": true}, locked: map[string]bool{"s": true}, infos: map[string]rex.SessionInfo{}}
	if b.poll() || !b.owned["t1"] || !b.locked["s"] {
		t.Errorf("a failed poll changed the locks: %v %v", b.owned, b.locked)
	}
	desk.Close()
}

// start runs a bridge to the server of the test, and returns rootshell's
// end of it, and its exit status once it ends.
func start(t *testing.T) (*rootshell, chan int) {
	t.Helper()
	c, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	exit := make(chan int, 1)
	ended := make(chan struct{})
	go func() {
		exit <- Run(c, inR, outW)
		outW.Close()
		close(ended)
	}()
	// Ended before the next test sets the intervals.
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
		}
	})
	r := &rootshell{t: t, in: inW, lines: make(chan map[string]any, 1024)}
	go func() {
		defer close(r.lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 16<<20)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("not JSON: %q", sc.Bytes())
				continue
			}
			r.lines <- m
		}
	}()
	return r, exit
}

func TestBridge(t *testing.T) {
	desk := serve(t)
	shell, typed := []string{"/bin/sh"}, "echo mar''ker\n"
	if runtime.GOOS == "windows" {
		shell, typed = []string{"cmd.exe"}, "echo mar^ker\r"
	}
	var sids []string
	for range 3 {
		in, err := desk.Create(rex.CreateOptions{Command: shell, Dir: os.TempDir(), Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, in.ID)
	}
	// A tab of two panes side by side, and a pane of a remote host,
	// which rootshell does not see; a tab of only a remote pane neither;
	// a tab of one pane.
	layout := fmt.Sprintf(`{"active":0,"tabs":[
		{"name":"work","focus":1,"root":{"ratio":0.5,"a":{"sid":%q},"b":{"vertical":true,"ratio":0.5,"a":{"sid":%q},"b":{"sid":"far","host":"h1"}}}},
		{"root":{"sid":"far2","host":"h1"}},
		{"name":"two","root":{"sid":%q}}]}`, sids[0], sids[1], sids[2])
	if err := desk.SetLayout(json.RawMessage(layout)); err != nil {
		t.Fatal(err)
	}

	r, exit := start(t)
	open := result(t, r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" }))
	caps := open["capabilities"].(map[string]any)
	if open["version"] != "0.9.0-gorex" || caps["terminal_control_stream"] != 2.0 || len(caps["control_features"].([]any)) != 3 {
		t.Errorf("control.open %v", open)
	}
	mine := open["connection_id"].(float64)

	result(t, r.call("events.subscribe", map[string]any{"subscriptions": []any{map[string]any{"type": "layout.updated"}}}))
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	tabs, panes := snap["tabs"].([]any), snap["panes"].([]any)
	if len(tabs) != 2 || len(panes) != 3 || snap["focused_pane_id"] != "p-"+sids[1] {
		t.Fatalf("snapshot %v", snap)
	}
	tabID := tabs[0].(map[string]any)["tab_id"].(string)
	l := snap["layouts"].([]any)[0].(map[string]any)
	if gc := l["geometry_controller"].(map[string]any); gc["kind"] != "client" {
		t.Errorf("an unlocked tab's controller %v", gc)
	}
	if a := l["area"].(map[string]any); a["width"] != 160.0 || a["height"] != 24.0 {
		t.Errorf("packed area %v", a)
	}

	// The attach's snapshot comes before its output.
	res := result(t, r.call("terminal.attach", map[string]any{"target": sids[0], "answer_queries": "client", "history_limit_bytes": 1 << 20}))
	aid := res["attach_id"].(string)
	first := r.next("the attach's first record", func(m map[string]any) bool { return m["attach_id"] == aid })
	if first["type"] != "terminal.snapshot" {
		t.Fatalf("first record %v", first)
	}
	ts := first["snapshot"].(map[string]any)
	if st := ts["state"].(map[string]any); st["cols"] != 80.0 || st["rows"] != 24.0 || !strings.HasPrefix(ts["primary"].(string), "\x1b[?25h") {
		t.Errorf("snapshot record %v", ts)
	}
	r.send("i1", "terminal.input", map[string]any{"attach_id": aid, "bytes": base64.StdEncoding.EncodeToString([]byte(typed))})
	var out strings.Builder
	seq := 0.0
	r.next("the output typed", func(m map[string]any) bool {
		if m["type"] != "terminal.output" || m["attach_id"] != aid {
			return false
		}
		if s := m["seq"].(float64); s <= seq {
			t.Errorf("seq %v after %v", s, seq)
		} else {
			seq = s
		}
		b, _ := base64.StdEncoding.DecodeString(m["bytes"].(string))
		out.Write(b)
		return strings.Contains(out.String(), "marker")
	})

	// A viewer's sizes change nothing.
	result(t, r.call("tab.set_geometry", map[string]any{"tab_id": tabID, "cols": 100, "rows": 30, "chrome": "none", "claim": false}))
	if in := sessions(t, desk)[sids[0]]; in.SizeLock != "" || in.Cols != 80 {
		t.Errorf("a viewer sized %+v", in)
	}
	// Claimed, the tab's panes tile its size, locked.
	r.send("r100", "tab.set_geometry", map[string]any{"tab_id": tabID, "cols": 100, "rows": 30, "chrome": "none", "claim": true})
	ev := r.next("tab.geometry_changed", func(m map[string]any) bool { return m["event"] == "tab.geometry_changed" })
	if gc := ev["data"].(map[string]any)["geometry_controller"].(map[string]any); gc["kind"] != "control" || gc["connection_id"] != mine {
		t.Errorf("claimed controller %v", gc)
	}
	lay := r.next("tab.layout", ofType("tab.layout"))["layout"].(map[string]any)
	if a := lay["area"].(map[string]any); a["width"] != 100.0 || a["height"] != 30.0 {
		t.Errorf("claimed area %v", a)
	}
	r.next("the claim's answer", func(m map[string]any) bool { return m["id"] == "r100" })
	size := func(sid string) string {
		in := sessions(t, desk)[sid]
		return fmt.Sprintf("%dx%d %v", in.Cols, in.Rows, in.SizeLock != "")
	}
	if size(sids[0]) != "50x30 true" || size(sids[1]) != "50x30 true" {
		t.Errorf("claimed sessions %s, %s", size(sids[0]), size(sids[1]))
	}
	// Another client's resize is ignored; the holder's sizes go through.
	desk.Resize(sids[0], 70, 20)
	if size(sids[0]) != "50x30 true" {
		t.Errorf("a plain resize of a locked session: %s", size(sids[0]))
	}
	result(t, r.call("tab.set_geometry", map[string]any{"tab_id": tabID, "cols": 120, "rows": 30, "chrome": "none", "claim": false}))
	time.Sleep(3 * geometrySettle)
	if size(sids[0]) != "60x30 true" {
		t.Errorf("the holder's resize: %s", size(sids[0]))
	}

	// Going to another tab gives the one before back to the desktop.
	tab2 := tabs[1].(map[string]any)["tab_id"].(string)
	result(t, r.call("tab.set_geometry", map[string]any{"tab_id": tab2, "cols": 90, "rows": 25, "chrome": "none", "claim": true}))
	if size(sids[0]) != "60x30 false" || size(sids[1]) != "60x30 false" || size(sids[2]) != "90x25 true" {
		t.Errorf("after going to another tab: %s, %s, %s", size(sids[0]), size(sids[1]), size(sids[2]))
	}
	// A pane whose session is gone detaches.
	aid2 := result(t, r.call("terminal.attach", map[string]any{"target": sids[2]}))["attach_id"].(string)
	r.next("its snapshot", func(m map[string]any) bool { return m["type"] == "terminal.snapshot" && m["attach_id"] == aid2 })
	desk.Kill(sids[2])
	if m := r.next("terminal.detached", ofType("terminal.detached")); m["attach_id"] != aid2 || m["reason"] != "closed" {
		t.Errorf("detached %v", m)
	}

	if e, ok := r.call("pane.resize", map[string]any{})["error"].(map[string]any); !ok || e["code"] != "read_only" || e["message"] == "" {
		t.Errorf("pane.resize answered %v", e)
	}
	// With no window to make it, a change of the layout fails at once.
	start := time.Now()
	if e, ok := r.call("tab.create", map[string]any{"workspace_id": "w", "focus": true})["error"].(map[string]any); !ok || e["code"] != "desktop_unavailable" || time.Since(start) > time.Second {
		t.Errorf("tab.create without the desktop answered %v after %v", e, time.Since(start))
	}
	r.send("r200", "terminal.snapshot", map[string]any{"attach_id": aid})
	r.next("a snapshot asked for", func(m map[string]any) bool { return m["type"] == "terminal.snapshot" && m["attach_id"] == aid })

	r.send("control:close", "control.close", map[string]any{})
	r.next("control.close's answer", func(m map[string]any) bool { return m["id"] == "control:close" })
	select {
	case code := <-exit:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge did not exit")
	}
	for _, sid := range sids {
		if in := sessions(t, desk)[sid]; in.SizeLock != "" {
			t.Errorf("still locked after close: %+v", in)
		}
	}
}
