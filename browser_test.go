package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/locales"
)

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

func TestBrowserTab(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.tab()
	a.invokeCommand(&cmdNewBrowser, nil)
	tt.Frame()
	if len(a.tabs) != 2 {
		t.Fatalf("%d tabs", len(a.tabs))
	}
	tab := a.tab()
	if tab == first {
		t.Fatal("the browser tab is not the active one")
	}
	p := tab.Focus
	if p == nil || p.browser == nil || p.SID != "" {
		t.Fatalf("focus is not a browser pane: %+v", p)
	}
	if name, _ := p.label(); name != locales.BrowserName() {
		t.Errorf("name %q", name)
	}
	if _, ok := tt.Find(locales.BrowserAddress()); !ok {
		t.Errorf("no address field; texts %q", tt.Texts())
	}
	if !tt.HasText(locales.BrowserUnavailable()) {
		t.Errorf("no word about the page; texts %q", tt.Texts())
	}
	if a.findable() {
		t.Error("find is offered in a browser pane")
	}
	// The field takes the keyboard of the new pane: its URL is typed, and
	// Enter loads it.
	tt.Type("example.com")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if p.browser.addr != "https://example.com" {
		t.Errorf("address %q after typing", p.browser.addr)
	}
	// A URL a browser pane does not open keeps the field, and says why.
	a.navigateBrowser(p, "file:///tmp/page.html")
	if p.browser.addr != "https://example.com" {
		t.Errorf("address %q after a file URL", p.browser.addr)
	}
	a.err, a.errMsg = "", nil

	// The address field's URL is a page's, and the pane keeps it.
	p.title = "Example Domain"
	p.browser.url, p.browser.addr = "https://example.com/", "https://example.com/"
	if name, detail := p.label(); name != "Example Domain" || detail != "example.com" {
		t.Errorf("label %q %q", name, detail)
	}
	a.navigateBrowser(p, "example.com/next")
	if p.browser.addr != "https://example.com/next" {
		t.Errorf("address %q", p.browser.addr)
	}
}

func TestSplitBrowser(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	a.invokeCommand(&cmdSplitBrowser, nil)
	tt.Frame()
	ps := tab.panes()
	if len(ps) != 2 {
		t.Fatalf("%d panes", len(ps))
	}
	p := tab.Focus
	if p == nil || p.browser == nil {
		t.Fatalf("the new pane is not a browser: %+v", p)
	}
	if p == ps[0] {
		t.Error("the browser is not beside the shell")
	}
	if ps[0].browser != nil || ps[0].term == nil {
		t.Error("the shell pane is gone")
	}
	if _, ok := tt.Find(locales.BrowserAddress()); !ok {
		t.Errorf("no address field; texts %q", tt.Texts())
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
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewBrowser, nil)
	tt.Frame()
	p := a.tab().Focus
	p.Name = "Docs"
	p.browser.url, p.browser.addr = "https://example.com/docs", "https://example.com/docs"
	a.saveNow()

	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	n := findNode(l.Tabs[len(l.Tabs)-1].Root, "https://example.com/docs")
	if n == nil {
		t.Fatalf("the layout keeps no browser pane: %+v", l.Tabs[len(l.Tabs)-1].Root)
	}
	if n.SID != "" || n.Name != "Docs" {
		t.Errorf("saved node %+v", n)
	}

	// The next launch shows the page again, with the pane's name.
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	var got *Pane
	for _, tab := range b.tabs {
		for _, p := range tab.panes() {
			if p.browser != nil {
				got = p
			}
		}
	}
	if got == nil {
		t.Fatal("the browser pane is not restored")
	}
	if got.browser.url != "https://example.com/docs" || got.Name != "Docs" {
		t.Errorf("restored %q %q", got.browser.url, got.Name)
	}
	if got.SID != "" || got.term != nil {
		t.Errorf("a browser pane restored with a session %q", got.SID)
	}
}
