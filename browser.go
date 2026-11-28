package main

import (
	"net/url"
	"strings"
)

// The URL rules a browser-kind pane answers, shared by the extension
// bridge's web slot and the gorex-browser pack's address field: the
// policy of where a page may go stays in the app (docs/EXT-001).

const browserStart = "about:blank"

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
