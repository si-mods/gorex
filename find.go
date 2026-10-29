package main

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
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
// against the pane's program directory and opens with the system.
func (a *App) openLink(p *Pane, link string) bool {
	path, ok := panePath(p, link)
	if !ok {
		return false
	}
	a.services.OpenURL(fileURL(path))
	return true
}

// panePath turns a printed file link into a path, resolving a relative one
// against the pane's program directory; false for a URL or a missing path.
func panePath(p *Pane, link string) (string, bool) {
	if p != nil && p.host != nil {
		return "", false // the path is on the host, not here
	}
	if strings.Contains(link, "://") {
		return "", false
	}
	path := link
	if m := linkLine.FindStringSubmatch(path); m != nil {
		path = m[1]
	}
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
	path = filepath.Clean(path)
	if st, err := os.Stat(path); err != nil || st == nil {
		return "", false
	}
	return path, true
}

func fileURL(path string) string {
	if filepath.VolumeName(path) != "" {
		path = "/" + filepath.ToSlash(path)
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
