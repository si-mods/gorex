package rexapi

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// dirListHome makes a temporary home directory, its symlinks resolved,
// the one the test's GoRex server lists.
func dirListHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, d := range []string{filepath.Join("src", "api"), filepath.Join("src", "Web"), filepath.Join("src", ".cache"), ".config"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "src", "README"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestDirListMethod checks gorex.dir.list: the welcome lists it, it lists
// the home directory and a nested one, hidden ones only when asked, says
// not_found of a missing one, refuses bad arguments, and the help
// documents describe it.
func TestDirListMethod(t *testing.T) {
	home := dirListHome(t)
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); !slices.Contains(features, any(featureDirList)) {
		t.Fatalf("the welcome's features %v lack %s", features, featureDirList)
	}

	r := callChecked(t, c, "gorex.dir.list", nil)
	if r["path"] != home || r["home"] != home || r["parent"] != filepath.Dir(home) || r["more"] != false || !reflect.DeepEqual(r["dirs"], []any{"src"}) {
		t.Errorf("home: %v", r)
	}
	if _, ok := r["code"]; ok {
		t.Errorf("home has a code: %v", r)
	}
	src := filepath.Join(home, "src")
	r = callChecked(t, c, "gorex.dir.list", map[string]any{"path": "~/src"})
	if r["path"] != src || r["parent"] != home || !reflect.DeepEqual(r["dirs"], []any{"api", "Web"}) {
		t.Errorf("~/src: %v", r)
	}
	r = callChecked(t, c, "gorex.dir.list", map[string]any{"path": src + "/api/..", "hidden": true})
	if r["path"] != src || !reflect.DeepEqual(r["dirs"], []any{".cache", "api", "Web"}) {
		t.Errorf("src/api/.. with hidden: %v", r)
	}
	missing := filepath.Join(home, "missing")
	r = callChecked(t, c, "gorex.dir.list", map[string]any{"path": missing})
	if r["code"] != rex.DirNotFound || r["path"] != missing || r["parent"] != home || !reflect.DeepEqual(r["dirs"], []any{}) {
		t.Errorf("missing: %v", r)
	}

	for _, payload := range []map[string]any{{"path": 3}, {"hidden": "yes"}, {"path": "~", "extra": 1}} {
		if e := failure(t, c.request("bad", "gorex.dir.list", payload)); e["code"] != "bad_request" {
			t.Errorf("%v: %v, want bad_request", payload, e)
		}
	}

	if schemaFor(t, "gorex.dir.list") == nil {
		t.Error("gorex.dir.list has no result schema")
	}
	res, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if body, _ := io.ReadAll(res.Body); !strings.Contains(string(body), "### gorex.dir.list") {
		t.Error("/llms.txt does not describe gorex.dir.list")
	}
}

// TestDirListOldServer: over a GoRex server that does not list
// directories, the welcome lacks gorex.dir_list and the method fails with
// not_supported.
func TestDirListOldServer(t *testing.T) {
	dirListHome(t)
	f := startFixture(t)
	f.g.hello.Capabilities = slices.DeleteFunc(slices.Clone(f.g.hello.Capabilities), func(s string) bool {
		return s == rex.CapabilityDirList
	})
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); slices.Contains(features, any(featureDirList)) {
		t.Errorf("the welcome lists %s without the capability: %v", featureDirList, features)
	}
	if e := failure(t, c.request("old", "gorex.dir.list", nil)); e["code"] != "not_supported" {
		t.Errorf("gorex.dir.list on an old server: %v, want not_supported", e)
	}
}

// TestDirListCallers: gorex.dir.list is allowed to the callers that may
// call session.create. An anonymous caller of the local network, with the
// pairing token, may call both, where a local-socket-only method fails
// with 403; and a stopping gateway refuses both with conflict.
func TestDirListCallers(t *testing.T) {
	home := dirListHome(t)
	g, dir := startStateGateway(t)
	g.lan.apply(LANConfig{Enabled: true, Listen: "127.0.0.1:0", Allow: []string{"127.0.0.1"}, Token: testLANToken})
	s := waitLAN(t, dir, func(s LANStatus) bool { return s.Listening != "" })
	c, err := dialLANControl(t, s.Listening, "?k="+testLANToken)
	if err != nil {
		t.Fatal(err)
	}
	created := callChecked(t, c, "session.create", nil)
	if id, _ := created["session_id"].(string); id != "" {
		t.Cleanup(func() { c.request("destroy", "session.destroy", map[string]any{"session_id": id}) })
	} else {
		t.Fatalf("session.create over the local network: %v", created)
	}
	if r := callChecked(t, c, "gorex.dir.list", nil); r["path"] != home {
		t.Errorf("gorex.dir.list over the local network: %v", r)
	}
	if e := failure(t, c.request("ts", "server.tailscale.enable", map[string]any{"allow_tailnet_members": false})); e["status"] != float64(403) {
		t.Errorf("server.tailscale.enable over the local network: %v, want 403", e)
	}

	g.stopping.Store(true)
	defer g.stopping.Store(false)
	for _, method := range []string{"session.create", "gorex.dir.list"} {
		if e := failure(t, c.request("stop", method, nil)); e["code"] != "conflict" {
			t.Errorf("%s while stopping: %v, want conflict", method, e)
		}
	}

	// No token, no call: the listener refuses the connection.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws://"+s.Listening+"/api/control", &websocket.DialOptions{Subprotocols: []string{controlProtocol}})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err == nil {
		ws.CloseNow()
		t.Error("a caller without the token got in")
	}
}
