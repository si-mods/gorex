package main

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo"
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
func (b *browserPane) destroy() {
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
