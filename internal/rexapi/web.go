package rexapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WebConfig is the web pages the gateway trusts, kept in web.json in the
// gateway's state directory. Native clients send no Origin header and
// are not judged by it. A browser sends the page's origin with every
// request, and the gateway serves a page only when its origin is in
// Origins: the standalone web client (docs/web-client.md) wherever it is
// hosted. Without the list, any page a browser on an allowed network
// opens could use the API as that browser, since the access rule judges
// the caller's address, not the page. The list is empty by default.
type WebConfig struct {
	// Origins are the pages let in, normalized by NormalizeOrigin:
	// scheme://host[:port], lower case, without a default port.
	Origins []string `json:"origins,omitempty"`
}

func webConfPath(dir string) string { return filepath.Join(dir, "web.json") }

// LoadWeb reads the web origins in dir; a missing file trusts none.
func LoadWeb(dir string) (WebConfig, error) {
	var c WebConfig
	b, err := os.ReadFile(webConfPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", webConfPath(dir), err)
	}
	return c, nil
}

// SaveWeb checks c and writes it in dir, where a gateway running picks it
// up within a second.
func SaveWeb(dir string, c WebConfig) error {
	for _, o := range c.Origins {
		if n, err := NormalizeOrigin(o); err != nil {
			return err
		} else if n != o {
			return fmt.Errorf("origin %q is not normalized (%s)", o, n)
		}
	}
	return writeJSON(webConfPath(dir), c)
}

// NormalizeOrigin reads a web page's origin, as a browser sends it in
// Origin or a user types it: http or https, a host and maybe a port,
// and nothing else (a lone trailing / is let through). It returns it as
// scheme://host[:port], scheme and host in lower case, the scheme's
// default port left out. Wildcards, paths, queries, user names and the
// opaque origin "null" are errors.
func NormalizeOrigin(s string) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("%q is not a web origin such as https://gorex.example.com or http://127.0.0.1:5173: %s", s, why)
	}
	t := strings.TrimSpace(s)
	if strings.ContainsAny(t, "*?#\\ ") {
		return bad("no wildcards, queries or fragments")
	}
	u, err := url.Parse(t)
	if err != nil {
		return bad("not a URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return bad("the scheme is http or https")
	}
	if u.Opaque != "" || u.User != nil || u.Host == "" {
		return bad("it has a host and no user name")
	}
	if u.Path != "" && u.Path != "/" {
		return bad("an origin has no path")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return bad("the port is empty")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return bad("the port is a number from 1 to 65535")
		}
	}
	if strings.HasPrefix(u.Host, "[") {
		a, err := netip.ParseAddr(host)
		if err != nil || !a.Is6() || a.Zone() != "" {
			return bad("not an IPv6 address")
		}
		host = "[" + a.String() + "]"
	} else if !validHostname(host) {
		return bad("not a host name")
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

// validHostname reports whether h is a DNS name or an IPv4 address:
// labels of letters, digits, - and _, none empty.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

// webOrigins is the gateway's list of trusted origins, read from
// web.json again when it changed, at most once a second.
type webOrigins struct {
	dir string

	mu      sync.Mutex
	checked time.Time
	watch   confWatch
	set     map[string]bool
}

func newWebOrigins(dir string) *webOrigins {
	return &webOrigins{dir: dir, watch: confWatch{path: webConfPath(dir)}}
}

// allowed reports whether a page of origin, as its Origin header has it,
// may use the API. A nil list trusts no page.
func (o *webOrigins) allowed(origin string) bool {
	if o == nil {
		return false
	}
	n, err := NormalizeOrigin(origin)
	if err != nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if time.Since(o.checked) >= time.Second {
		o.checked = time.Now()
		if o.watch.changed() {
			c, err := LoadWeb(o.dir)
			if err != nil {
				logf("web: %v", err)
			}
			o.set = map[string]bool{}
			for _, s := range c.Origins {
				if n, err := NormalizeOrigin(s); err == nil {
					o.set[n] = true
				}
			}
		}
	}
	return o.set[n]
}

// checkOrigin lets a request go on as far as its Origin header tells.
// Native clients send none and go on. A page of a trusted origin goes on
// and gets CORS headers, so that it can read the gateway's plain HTTP
// answers (uploads, refusals); a preflight of such a page is answered
// after the access rule (preflight). Any other page, and the opaque
// origin "null", gets 403. It reports whether the request may go on.
func (g *Gateway) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !g.web.allowed(origin) {
		tracef("refused %s: origin %s", r.URL.Path, origin)
		hint := "allow it with gorex rexapi web allow ORIGIN"
		if n, err := NormalizeOrigin(origin); err == nil {
			hint = "allow it with gorex rexapi web allow " + n
		}
		writeRefusal(w, fmt.Sprintf("this GoRex gateway does not trust web pages from %s (%s)", origin, hint))
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Add("Vary", "Origin")
	return true
}

// preflight answers a trusted page's CORS preflight, which a browser
// sends before an upload (a POST of application/octet-stream), and
// reports whether it did.
func preflight(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodOptions || r.Header.Get("Origin") == "" || r.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Methods", "GET, POST")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
	return true
}

// webMain is "gorex rexapi web allow ORIGIN | deny ORIGIN | status": the
// web pages the gateway trusts. The gateway reads web.json again within
// a second; nothing needs to restart.
func webMain(args []string) int {
	usage := "usage: gorex rexapi web allow ORIGIN | deny ORIGIN | status"
	dir := TailnetDir()
	conf, err := LoadWeb(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorex rexapi web: %v\n", err)
		return 1
	}
	switch {
	case len(args) == 1 && args[0] == "status":
	case len(args) == 2 && (args[0] == "allow" || args[0] == "deny"):
		o, err := NormalizeOrigin(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gorex rexapi web: %v\n", err)
			return 2
		}
		if args[0] == "allow" {
			if !slices.Contains(conf.Origins, o) {
				conf.Origins = append(conf.Origins, o)
			}
		} else {
			conf.Origins = slices.DeleteFunc(conf.Origins, func(s string) bool {
				n, err := NormalizeOrigin(s)
				return err != nil || n == o
			})
		}
		if err := SaveWeb(dir, conf); err != nil {
			fmt.Fprintf(os.Stderr, "gorex rexapi web: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	fmt.Println("web origins trusted:")
	if len(conf.Origins) == 0 {
		fmt.Println("  none")
	}
	for _, o := range conf.Origins {
		fmt.Printf("  %s\n", o)
	}
	return 0
}
