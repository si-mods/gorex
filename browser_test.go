package main

import (
	"testing"

	"gorex/internal/layout"
)

// extPaneOf is the pane's extension-pack pane, nil for one that is not
// a pack's.
func extPaneOf(p *Pane) *extPane {
	e, _ := p.ext.(*extPane)
	return e
}

func TestBrowserURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"example.com", "https://example.com"},
		{"  example.com ", "https://example.com"},
		{"https://example.com/a", "https://example.com/a"},
		{"HTTP://example.com", "HTTP://example.com"},
		{"http://example.com", "http://example.com"},
		{"localhost:3000", "https://localhost:3000"},
		{"example.com:8080/a", "https://example.com:8080/a"},
		{"about:blank", "about:blank"},
		{"ABOUT:BLANK", "about:blank"},
		// Only web pages: another scheme is no URL for a browser pane.
		{"file:///tmp/page.html", ""},
		{"data:text/html,hi", ""},
		{"javascript:alert(1)", ""},
		{"JavaScript:alert(1)", ""},
		{"javascript:123", ""},
		{"mailto:a@example.com", ""},
		{"blob:https://example.com/x", ""},
		{"mygo://localhost/", ""},
	} {
		if got := browserURL(c.in); got != c.want {
			t.Errorf("browserURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// What a pane keeps is the page's URL without its user info.
	if got := browserCleanURL("https://me:hunter2@example.com/a?q=1"); got != "https://example.com/a?q=1" {
		t.Errorf("browserCleanURL keeps user info: %q", got)
	}
	if got := browserCleanURL("https://example.com/a"); got != "https://example.com/a" {
		t.Errorf("browserCleanURL changed a URL with no user info: %q", got)
	}
	for _, c := range []struct{ in, want string }{
		{"https://example.com/a?b", "example.com"},
		{"https://example.com:8080/", "example.com:8080"},
		{"about:blank", ""},
		{"", ""},
	} {
		if got := browserHost(c.in); got != c.want {
			t.Errorf("browserHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

const browserExtManifest = `{
	"id": "gorex-browser", "name": "Browser", "engine": "webview", "api": 1,
	"panes": [{"kind": "browser", "title": "Browser", "fields": ["url", "id"]}],
	"permissions": ["state", "web"],
	"slots": ["web"],
	"commands": [
		{"id": "new", "title": "New Browser", "kind": "browser", "placement": "tab"},
		{"id": "split", "title": "Split Browser", "kind": "browser", "placement": "split"}
	]
}`

// browserPack installs the test browser pack for the test's duration.
func browserPack(t *testing.T) {
	dir := t.TempDir()
	writeExtPack(t, dir, "gorex-browser", browserExtManifest)
	loadTestPacks(t, dir)
}

func TestBrowserTab(t *testing.T) {
	browserPack(t)
	a, tt := newTestApp(t)
	first := a.tab()
	a.newExtTab("browser")
	tt.Frame()
	if len(a.tabs) != 2 {
		t.Fatalf("%d tabs", len(a.tabs))
	}
	tab := a.tab()
	if tab == first {
		t.Fatal("the browser tab is not the active one")
	}
	p := tab.Focus
	e := extPaneOf(p)
	if p == nil || e == nil || e.def.Kind != "browser" || p.SID != "" {
		t.Fatalf("focus is not a browser pane: %+v", p)
	}
	if name, _ := p.label(); name != "Browser" {
		t.Errorf("name %q", name)
	}
	if a.findable() {
		t.Error("find is offered in a browser pane")
	}
}

func TestSplitBrowser(t *testing.T) {
	browserPack(t)
	a, tt := newTestApp(t)
	tab := a.tab()
	a.splitLocal(false, func() *Pane { return a.newExtPane("browser") })
	tt.Frame()
	ps := tab.panes()
	if len(ps) != 2 {
		t.Fatalf("%d panes", len(ps))
	}
	// Headless web views take no keyboard; the split's focus request is
	// the pane it made.
	p := ps[1]
	if extPaneOf(p) == nil {
		t.Fatalf("the new pane is not a browser: %+v", p)
	}
	if a.focusReq != p {
		t.Error("the new pane did not take the focus request")
	}
	if extPaneOf(ps[0]) != nil || ps[0].term == nil {
		t.Error("the shell pane is gone")
	}
}

// findNode returns the saved node whose URL is url, nil for none.
func findNode(n *layout.Node, url string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.URL == url {
		return n
	}
	if got := findNode(n.A, url); got != nil {
		return got
	}
	return findNode(n.B, url)
}

func TestBrowserLayout(t *testing.T) {
	browserPack(t)
	a, tt := newTestApp(t)
	a.newExtTab("browser")
	tt.Frame()
	p := a.tab().Focus
	p.Name = "Docs"
	extPaneOf(p).keep.URL = "https://example.com/docs"
	a.saveNow()

	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	n := findNode(l.Tabs[len(l.Tabs)-1].Root, "https://example.com/docs")
	if n == nil {
		t.Fatalf("the layout keeps no browser pane: %+v", l.Tabs[len(l.Tabs)-1].Root)
	}
	if n.SID != "" || n.Name != "Docs" || n.Kind != "browser" {
		t.Errorf("saved node %+v", n)
	}

	// The next launch shows the page again, with the pane's name.
	b := &App{client: a.client}
	if !b.reconcile(l) {
		t.Fatal("nothing restored")
	}
	var got *Pane
	for _, tab := range b.tabs {
		for _, p := range tab.panes() {
			if extPaneOf(p) != nil {
				got = p
			}
		}
	}
	if got == nil {
		t.Fatal("the browser pane is not restored")
	}
	if extPaneOf(got).keep.URL != "https://example.com/docs" || got.Name != "Docs" {
		t.Errorf("restored %q %q", extPaneOf(got).keep.URL, got.Name)
	}
	if got.SID != "" || got.term != nil {
		t.Errorf("a browser pane restored with a session %q", got.SID)
	}
}
