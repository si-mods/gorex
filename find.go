package main

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"golang.org/x/net/publicsuffix"
	"gorex/locales"
)

// findable tells whether the focused pane can be searched.
func (a *App) findable() bool {
	t := a.tab()
	return t != nil && t.Focus != nil && t.Focus.term != nil
}

// openFind shows the find bar of the focused pane, with no query.
func (a *App) openFind() {
	t := a.tab()
	if t == nil || t.Focus == nil || t.Focus.term == nil {
		return
	}
	p := t.Focus
	if a.findPane != nil && a.findPane != p && a.findPane.term != nil {
		a.findPane.term.EndSearch()
	}
	a.findPane, a.findText, a.findFocus = p, "", true
	a.lastFind = ""
	a.findStatus = terminal.SearchStatus{}
	a.focusReq = nil
	a.changed()
}

// closeFind ends the search and gives the pane its keyboard back.
func (a *App) closeFind() {
	p := a.findPane
	if p != nil && p.term != nil {
		p.term.EndSearch()
	}
	if a.findText != "" {
		a.lastFind = a.findText
	}
	a.findPane, a.findText, a.findFocus = nil, "", false
	a.findStatus = terminal.SearchStatus{}
	if p != nil && !p.closed {
		a.focusReq = p
	}
	a.changed()
}

// findQuery searches the pane for the bar's text.
func (a *App) findQuery() {
	p := a.findPane
	if p == nil || p.term == nil {
		return
	}
	p.term.Search(a.findText)
	a.changed()
}

// findNext selects the next match.
func (a *App) findNext() {
	if a.findPane == nil && !a.resumeFind() {
		return
	}
	if p := a.findPane; p != nil && p.term != nil && a.findText != "" {
		p.term.SearchNext()
		a.changed()
	}
}

// findPrevious selects the previous match.
func (a *App) findPrevious() {
	if a.findPane == nil && !a.resumeFind() {
		return
	}
	if p := a.findPane; p != nil && p.term != nil && a.findText != "" {
		p.term.SearchPrevious()
		a.changed()
	}
}

// resumeFind reopens the bar on the last query, reporting whether there
// was one.
func (a *App) resumeFind() bool {
	if a.lastFind == "" {
		return false
	}
	last := a.lastFind
	a.openFind()
	if a.findPane == nil {
		return false
	}
	a.findText, a.lastFind = last, last
	a.findQuery()
	return true
}

// findCount says how the search goes.
func (a *App) findCount() string {
	if a.findText == "" {
		return ""
	}
	return locales.FindCount(strconv.Itoa(a.findStatus.Selected), strconv.Itoa(a.findStatus.Total))
}

var linkLine = regexp.MustCompile(`^(.*?)(:\d+)(?::\d+)?$`)

// openLink opens a link the terminal hands over: a URL goes to the system
// as the terminal would; a file path, with :line:col trimmed, resolves
// against the pane's program directory and opens with the system. One
// printed in a pane of a host is downloaded first (download).
func (a *App) openLink(p *Pane, link string) bool {
	if p != nil && p.host != nil {
		return a.download(p, link)
	}
	path, ok := panePath(p, link)
	if !ok {
		return false
	}
	a.services.OpenURL(fileURL(path))
	return true
}

// pathStat is what a stat found of a path printed in a pane of this
// machine: whether it is there, and a directory, as of at; pending while a
// stat runs.
type pathStat struct {
	there, dir, pending bool
	at                  time.Time
}

const (
	// pathStatsMax bounds App.pathStats: once full it starts over.
	pathStatsMax = 256
	// pathStatAge is how long a stat holds before another checks it again,
	// in the background, a path that came or went meanwhile.
	pathStatAge = 5 * time.Second
)

// isLink reports whether link, a file path printed in the pane, is one the
// terminal shows as a link: in a pane of this machine, one that is there,
// so that prose such as and/or or w/ is none; in a pane of a host, any but
// a directory, as a folder is not downloaded. It runs on every frame of a
// Command hover with the terminal locked, so it never waits on the disk,
// which a dead mount can hang: it answers from App.pathStats and on a miss
// stats the path on another goroutine, update storing what it found and
// building a frame again.
func (a *App) isLink(p *Pane, link string, update func(func())) bool {
	path, ok := linkPath(link)
	if !ok || p == nil {
		return false
	}
	dir := strings.HasSuffix(path, "/")
	if p.host != nil {
		return !dir && !webPath(path)
	}
	path, ok = resolvePath(p, path)
	if !ok {
		return false
	}
	s, known := a.pathStats[path]
	if !s.pending && (!known || time.Since(s.at) > pathStatAge) {
		a.setPathStat(path, pathStat{there: s.there, dir: s.dir, at: s.at, pending: true})
		go func() {
			st, err := os.Stat(path)
			found := pathStat{there: err == nil, dir: err == nil && st.IsDir(), at: time.Now()}
			update(func() { a.setPathStat(path, found) })
		}()
	}
	return s.there && (!dir || s.dir)
}

// setPathStat records s of path, starting over once App.pathStats is full.
func (a *App) setPathStat(path string, s pathStat) {
	if a.pathStats == nil || len(a.pathStats) >= pathStatsMax {
		a.pathStats = make(map[string]pathStat)
	}
	a.pathStats[path] = s
}

// linkPath is the file path a printed link names, :line:col trimmed;
// false for a URL.
func linkPath(link string) (string, bool) {
	if strings.Contains(link, "://") {
		return "", false
	}
	if m := linkLine.FindStringSubmatch(link); m != nil {
		return m[1], true
	}
	return link, true
}

// webPath reports whether path, printed in a pane of a host, where no stat
// can tell, reads as a URL without its scheme, as support.github.com/tickets:
// a relative path whose first part is a host name under a public suffix.
func webPath(path string) bool {
	name, _, ok := strings.Cut(path, "/")
	if !ok || !strings.Contains(name, ".") {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(strings.ToLower(name))
	return icann && suffix != strings.ToLower(name)
}

// panePath turns a printed file link into a path, resolving a relative one
// against the pane's program directory; false for a URL or a missing path.
func panePath(p *Pane, link string) (string, bool) {
	path, ok := linkPath(link)
	if !ok {
		return "", false
	}
	if path, ok = resolvePath(p, path); !ok {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// resolvePath is path, printed in the pane without :line:col, made
// absolute: from ~/ in the home directory, a relative one in the pane's
// program directory; false when there is none.
func resolvePath(p *Pane, path string) (string, bool) {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		dir := ""
		if p != nil {
			dir = p.info.Dir
			if dir == "" {
				dir = p.startDir
			}
		}
		if dir == "" {
			return "", false
		}
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), true
}

func fileURL(path string) string {
	if filepath.VolumeName(path) != "" {
		path = "/" + filepath.ToSlash(path)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
