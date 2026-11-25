package main

import (
	"log"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/locales"
)

// browserPane is a pane that shows a web page in a web view of its
// window, with an address field in its header.
type browserPane struct {
	web *mygo.WebView
	// url is the page the pane shows, as the layout keeps it, and addr
	// the text of the address field; id names the pane in the layout
	// (layout.Node.ID), and synced is the URL the layout had when the
	// window last wrote or read it: one the server has past it is another
	// client's, which the pane follows.
	url, addr, id, synced string
	// auto gives the address field the keyboard when the pane opens, in
	// place of the page: a new pane has the URL of the page to type.
	auto bool
	// editing tells that the address field had the focus as it was drawn:
	// a page's own navigation then leaves the text being typed.
	editing bool
}

// browserStart is the page a browser pane opens at, until a URL is typed.
const browserStart = "about:blank"

// destroy closes the pane's web view, and forgets it.
func (b *browserPane) destroy(kill bool) {
	if b != nil && b.web != nil {
		b.web.Destroy()
		b.web = nil
	}
}

// browserURL turns what was typed into a URL to load: one without a
// scheme is a site, https. Only web pages open in a browser pane: any
// other scheme, such as file:, data: or javascript:, is "".
func browserURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return s
	}
	if lower == browserStart {
		return browserStart
	}
	if ref, err := url.Parse(lower); err == nil && ref.Scheme != "" {
		switch ref.Scheme {
		case "javascript", "file", "data", "blob", "mailto", "ftp", "mygo", "chrome", "view-source":
			return ""
		}
		// A host and port, as localhost:3000, is a site, not a scheme.
		if rest, _ := strings.CutPrefix(lower, ref.Scheme+":"); rest != "" {
			port, _, _ := strings.Cut(rest, "/")
			if port != "" && strings.Trim(port, "0123456789") == "" {
				return "https://" + s
			}
		}
		return ""
	}
	return "https://" + s
}

// browserCleanURL is the URL a browser pane keeps and shows: without the
// user info, which a layout file should not hold.
func browserCleanURL(u string) string {
	if ref, err := url.Parse(u); err == nil && ref.User != nil {
		ref.User = nil
		return ref.String()
	}
	return u
}

// browserHost is the host of a URL, "" when it has none.
func browserHost(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Host
}

// newBrowserPane makes a pane that shows url in a web view of the
// window's, with the address field given the keyboard.
func (a *App) newBrowserPane(url string) *Pane {
	if url = browserURL(url); url == "" {
		url = browserStart
	}
	addr := url
	if url == browserStart {
		addr = "" // the field says its placeholder, and the URL is typed
	}
	b := &browserPane{url: url, addr: addr, id: layout.NewTabID(), auto: true}
	p := &Pane{ID: a.id(), ext: b}
	if a.win == nil {
		// A view without a window, as in tests: the pane keeps its URL,
		// and shows no page.
		return p
	}
	v, err := a.win.NewWebView(mygo.WebViewOptions{
		URL:             url,
		BackgroundColor: "light-dark(#ffffff, #1e1e24)",
		// A page is someone else's, whatever its URL: it calls no Go
		// method, asks no permission, opens no window of its own and
		// downloads nothing. The handler below decides where it may go.
		Page: mygo.PageOptions{Untrusted: true, SwipeNavigation: true, PinchZoom: true},
	})
	if err != nil {
		log.Printf("browser pane: %v", err)
		a.err, a.errMsg = err.Error(), nil
		return p
	}
	b.web = v
	update := func(fn func()) {
		if a.win != nil {
			a.win.Update(fn)
		}
	}
	page := v.Page()
	// An untrusted page may not go to another origin unless this decides:
	// a browser pane follows the web, and nothing else.
	page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if browserURL(e.URL) == "" {
			e.PreventDefault()
		}
	})
	page.OnDidNavigate(func(u string) {
		u = browserCleanURL(u)
		update(func() {
			if p.closed || p.ext == nil {
				return
			}
			b.url = u
			// The page's title is the new document's, until it says one.
			p.title = ""
			// A new pane's first page, about:blank, leaves the field
			// saying its placeholder, for the URL to type; and a page that
			// goes on by itself leaves the URL being typed.
			if !b.editing && (b.addr != "" || u != browserStart) {
				b.addr = u
			}
			a.changed()
		})
	})
	page.OnPageTitleUpdated(func(e *mygo.TitleEvent) {
		update(func() {
			if p.closed || p.ext == nil {
				return
			}
			p.title = strings.TrimSpace(e.Title)
		})
	})
	// A page that asks for a window shows it in this pane: GoRex has no
	// other window to give a site, and a script's pop-up is not wanted.
	// The URL is the pane's to load, not the page's to choose.
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		if u := browserURL(req.URL); req.UserInitiated && u != "" {
			page.LoadURL(u)
		}
		return nil
	})
	return p
}

// loadBrowserPane makes a browser pane of s, a browser node of a layout.
func (a *App) loadBrowserPane(s *savedNode, taken func(*savedNode) bool) *Pane {
	p := a.newBrowserPane(s.URL)
	p.Name = s.Name
	b := p.ext.(*browserPane)
	b.auto, b.synced = false, s.URL
	if s.ID != "" && !taken(s) {
		b.id = s.ID
	}
	return p
}

// navigateBrowser loads the URL in a browser pane's address field.
func (a *App) navigateBrowser(p *Pane, text string) {
	b, ok := p.ext.(*browserPane)
	if !ok {
		return
	}
	u := browserURL(text)
	if u == "" {
		a.err, a.errMsg = "", locales.Messages.BrowserOnlyWeb
		return
	}
	a.err, a.errMsg = "", nil
	// The page keeps what was typed, credentials and all; the field and
	// the layout keep it without them.
	b.addr, b.url = browserCleanURL(u), browserCleanURL(u)
	if b.web == nil {
		return
	}
	if err := b.web.Page().LoadURL(u); err != nil {
		a.err, a.errMsg = err.Error(), nil
	}
}

// The paneExt of a browser pane.

func (b *browserPane) kind() string     { return "browser" }
func (b *browserPane) program() program { return browserProgram }

// label is the page's title, or the browser's name, and the page's host.
func (b *browserPane) label(p *Pane) (name, detail string) {
	name = p.Name
	if name == "" {
		name = strings.TrimSpace(p.title)
	}
	if name == "" {
		name = locales.BrowserName()
	}
	return name, browserHost(b.url)
}

func (b *browserPane) keepsHeader() bool { return true }

// opaqueTiled: a tiled browser pane paints an opaque card, as its web
// view does not blend under glass; a float's keeps the glass.
func (b *browserPane) opaqueTiled() bool { return true }

func (b *browserPane) badge() bool { return true }

func (b *browserPane) body(a *App, c *ui.Context, k *colors, t *Tab, p *Pane) {
	a.browserBody(c, k, t, p)
}

func (b *browserPane) header(a *App, c *ui.Context, k *colors, t *Tab, p *Pane, focused, hovered bool) bool {
	a.browserHeader(c, k, t, p, focused, hovered)
	return true
}

func (b *browserPane) headerActions(*App, *ui.Context, *colors, *Tab, *Pane) {}

func (b *browserPane) save(p *Pane, n *savedNode) {
	n.Kind, n.URL, n.ID = "browser", b.url, b.id
}

// apply takes a page another client navigated the pane to: the window's
// own navigation since it last wrote the layout stays.
func (b *browserPane) apply(a *App, p *Pane, s *savedNode) {
	if s.URL != b.synced {
		b.url, b.addr, b.synced = s.URL, s.URL, s.URL
		if b.web != nil {
			b.web.Page().LoadURL(s.URL)
		}
	}
}

func (b *browserPane) clone(a *App, p *Pane) *Pane {
	np := a.newBrowserPane(b.url)
	np.Name = p.Name
	np.ext.(*browserPane).auto = false
	return np
}

func (b *browserPane) restart(a *App, p *Pane) {
	if b.web != nil {
		b.web.Page().Reload()
	}
}

func (b *browserPane) zoom(a *App, p *Pane, delta float32) bool { return false }

func (b *browserPane) layoutSaved(p *Pane) { b.synced = b.url }

func (b *browserPane) webView() *mygo.WebView { return b.web }
