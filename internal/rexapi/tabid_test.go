package rexapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestWindowIDsFromTabIDs: a window is named by its tab's lasting ID,
// the same for any mapper, wherever the tab is and whatever it holds.
func TestWindowIDsFromTabIDs(t *testing.T) {
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{
			{ID: "t-one", Name: "one", Root: pane("a")},
			{ID: "t-two", Name: "two", Root: pane("b")},
		}},
	}}}
	m1, err := newMapper().build(layoutOfStore(t, store), "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	one, two := m1.sessions[0].windows[0].id, m1.sessions[0].windows[1].id
	if one == two || !strings.HasPrefix(one, "window:") {
		t.Fatalf("IDs %q %q", one, two)
	}
	// Reordered, its panes all replaced: the tab is the same window.
	store.Sessions[0].Tabs[0], store.Sessions[0].Tabs[1] = store.Sessions[0].Tabs[1], store.Sessions[0].Tabs[0]
	store.Sessions[0].Tabs[1].Root = split(pane("x"), pane("y"), false, 0.5)
	m2, err := newMapper().build(layoutOfStore(t, store), "h", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m2.sessions[0].windows[0].id != two || m2.sessions[0].windows[1].id != one {
		t.Errorf("after reorder: %q %q, want %q %q", m2.sessions[0].windows[0].id, m2.sessions[0].windows[1].id, two, one)
	}
	if m2.sessions[0].windows[1].tab != "t-one" {
		t.Errorf("tab %q", m2.sessions[0].windows[1].tab)
	}
}

// TestWindowIDsSurviveGateway: a gateway started again on the same
// layout names the windows as the first did, so a client's IDs still
// work.
func TestWindowIDsSurviveGateway(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{
			{ID: "tab-a", Name: "a", Root: pane("s1")},
			{ID: "tab-b", Name: "b", Root: split(pane("s2"), pane("s3"), false, 0.5)},
		}}}}})
	windows := func(c *testConn) []string {
		t.Helper()
		boot := callChecked(t, c, "session.bootstrap", map[string]any{"event_scope": "session"})
		view := boot["selected_session"].(map[string]any)["view"].(map[string]any)
		var ids []string
		for _, w := range view["windows"].([]any) {
			ids = append(ids, w.(map[string]any)["window_id"].(string))
		}
		return ids
	}
	c := f.dialControl()
	c.hello(apiRevision)
	first := windows(c)

	kc, err := rex.ConnectSocketKeepalive(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	g2, err := New(Options{Client: kc})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(g2.Handler())
	defer func() { srv.Close(); g2.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, srv.URL+"/api/control", &websocket.DialOptions{Subprotocols: []string{controlProtocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	c2 := newTestConn(t, ws)
	c2.hello(apiRevision)
	second := windows(c2)
	if len(first) != 2 || strings.Join(first, " ") != strings.Join(second, " ") {
		t.Fatalf("window IDs changed: %v to %v", first, second)
	}
}
