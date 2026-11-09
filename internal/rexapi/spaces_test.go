package rexapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

func remotePane(host, sid string) *layout.Node { return &layout.Node{Host: host, SID: sid} }

// crossHostStore is a space of this machine's pane s1 and panes of hosts
// h1 and h2, and of h9, whose machine the app has not learned.
func crossHostStore() layout.Store {
	return layout.Store{Sessions: []layout.Workspace{
		{ID: "work", Name: "work", Layout: layout.Layout{Tabs: []layout.Tab{
			{ID: "t1", Name: "mixed", Root: split(pane("s1"), remotePane("h1", "r1"), false, 0.5),
				Floats: []layout.Float{{Pane: remotePane("h1", "r3"), W: 0.5, H: 0.5}}},
			// A tab of other hosts' panes only: no window here.
			{ID: "t2", Root: split(remotePane("h2", "r2"), remotePane("h9", "unknown"), true, 0.5)},
			// The same pane again, a pane the server would not share, and a
			// browser pane: none adds a member.
			{ID: "t3", Root: split(remotePane("h1", "r1"),
				split(&layout.Node{Unavailable: true, Host: "h2", SID: "gone"}, &layout.Node{URL: "https://example.com"}, false, 0.5), false, 0.5)},
		}}},
		{ID: "side", Name: "side", Layout: layout.Layout{Tabs: []layout.Tab{{ID: "t4", Root: pane("s2")}}}},
	}}
}

var crossHostMachines = map[string]string{"h1": "machine-one", "h2": "machine-two"}

// TestSpacesNameOtherHostsPanes checks that a space's panes of other
// hosts are named by their machine and their block there, in layout
// order, once each, and that a host of no known machine adds none.
func TestSpacesNameOtherHostsPanes(t *testing.T) {
	mp := newMapper()
	mp.machines = crossHostMachines
	m, err := mp.build(layoutOfStore(t, crossHostStore()), "host:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	work, side := m.sessionOfSpace("work"), m.sessionOfSpace("side")
	want := []remoteBlock{
		{Machine: "machine-one", BlockID: blockID("r1")},
		{Machine: "machine-one", BlockID: blockID("r3")},
		{Machine: "machine-two", BlockID: blockID("r2")},
	}
	if !reflect.DeepEqual(work.remote, want) {
		t.Errorf("remote blocks:\n got %+v\nwant %+v", work.remote, want)
	}
	if side.remote != nil {
		t.Errorf("a space of this machine only has remote blocks: %+v", side.remote)
	}
	// Other hosts' panes are still no blocks of this server's.
	for _, sid := range []string{"r1", "r2", "r3", "unknown"} {
		if m.blocks[blockID(sid)] != nil {
			t.Errorf("block of another host's session %s", sid)
		}
	}
}

// TestSpacesKeepTheView checks that the view clients have read before is
// the same with the app's machines known as without: its fields, and its
// revision.
func TestSpacesKeepTheView(t *testing.T) {
	raw := layoutOfStore(t, crossHostStore())
	plain, err := newMapper().build(raw, "host:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	mp := newMapper()
	mp.machines = crossHostMachines
	crossed, err := mp.build(raw, "host:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, space := range []string{"work", "side"} {
		a, _ := json.Marshal(plain.sessionOfSpace(space).view("host:test"))
		b, _ := json.Marshal(crossed.sessionOfSpace(space).view("host:test"))
		if string(a) != string(b) {
			t.Errorf("%s's view changed:\n%s\n%s", space, a, b)
		}
		if strings.Contains(string(b), "machine") || strings.Contains(string(b), "remote") {
			t.Errorf("%s's view tells of other hosts: %s", space, b)
		}
	}
	w := crossed.sessionOfSpace("work")
	if len(w.windows) != 1 || len(w.windows[0].blocks) != 1 || w.windows[0].blocks[0].sid != "s1" {
		t.Errorf("windows: %+v", w.windows)
	}
}

// TestSpacesRevisionFollowsOtherHostsPanes checks that a change of a
// space's panes of other hosts alone gives the session a new revision,
// so that a client that shows them reads them again.
func TestSpacesRevisionFollowsOtherHostsPanes(t *testing.T) {
	mp := newMapper()
	mp.machines = crossHostMachines
	store := crossHostStore()
	m1, _ := mp.build(layoutOfStore(t, store), "host:test", nil)
	m2, _ := mp.build(layoutOfStore(t, store), "host:test", nil)
	if m1.sessionOfSpace("work").revision != m2.sessionOfSpace("work").revision {
		t.Fatal("the same layout gave a new revision")
	}
	store.Sessions[0].Tabs[1].Root = remotePane("h2", "r4")
	m3, _ := mp.build(layoutOfStore(t, store), "host:test", nil)
	if m3.sessionOfSpace("work").revision != m2.sessionOfSpace("work").revision+1 {
		t.Errorf("revision %d after %d", m3.sessionOfSpace("work").revision, m2.sessionOfSpace("work").revision)
	}
	if m3.sessionOfSpace("side").revision != m2.sessionOfSpace("side").revision {
		t.Error("another space got a new revision")
	}
}

// TestSpacesEndpoint checks /api/spaces end to end: the machine of this
// server, and each space's panes of other hosts by machine and block,
// with nothing of hosts.json's names, addresses or IDs.
func TestSpacesEndpoint(t *testing.T) {
	f := startFixture(t)
	hosts := `{"hosts":[
		{"id":"h1","name":"Secret Studio","address":"tcat://SECRET-ADDRESS-ONE","machine":"machine-one"},
		{"id":"h2","name":"Hidden Box","address":"tailnet:SECRET-PEER-TWO","machine":"machine-two"}]}`
	if err := os.WriteFile(filepath.Join(rex.Dir(), "hosts.json"), []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	f.setLayout(crossHostStore())

	c := f.dialControl()
	c.hello(apiRevision)
	if fs, _ := c.welcome["features"].([]any); !slices.Contains(fs, any(featureSpaces)) {
		t.Errorf("welcome features: %v", c.welcome["features"])
	}
	// The views stay Rex's shape.
	ids := sessionIDs(t, c)
	callChecked(t, c, "session.attach", map[string]any{"session_id": ids["work"]})
	callChecked(t, c, "server.status", nil)

	resp, err := http.Get(f.srv.URL + "/api/spaces")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got spacesResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.MachineID == "" || got.MachineID != rex.MachineID() {
		t.Errorf("machine_id %q, want %q", got.MachineID, rex.MachineID())
	}
	if len(got.Sessions) != 1 || got.Sessions[0].SessionID != ids["work"] || got.Sessions[0].Revision == 0 {
		t.Fatalf("sessions: %+v", got.Sessions)
	}
	want := []remoteBlock{
		{Machine: "machine-one", BlockID: blockID("r1")},
		{Machine: "machine-one", BlockID: blockID("r3")},
		{Machine: "machine-two", BlockID: blockID("r2")},
	}
	if !reflect.DeepEqual(got.Sessions[0].RemoteBlocks, want) {
		t.Errorf("remote blocks %+v", got.Sessions[0].RemoteBlocks)
	}
	for _, secret := range []string{"Secret Studio", "SECRET-ADDRESS-ONE", "Hidden Box", "SECRET-PEER-TWO", `"h1"`, `"h2"`, "r1", "unknown", rex.ClientPublicKey()} {
		if strings.Contains(string(body), secret) {
			t.Errorf("/api/spaces tells %q: %s", secret, body)
		}
	}
	var keys map[string]any
	json.Unmarshal(body, &keys)
	if len(keys) != 2 {
		t.Errorf("fields: %v", keys)
	}

	post, err := http.Post(f.srv.URL+"/api/spaces", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", post.StatusCode)
	}
}

// TestSpacesEndpointRefused checks that the access rules guard
// /api/spaces as they guard the control connection.
func TestSpacesEndpointRefused(t *testing.T) {
	f := startFixture(t)
	f.g.opt.Access = AccessFunc(func(*http.Request) error { return errNoClient })
	rec := httptest.NewRecorder()
	f.g.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/spaces", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d", rec.Code)
	}
}
