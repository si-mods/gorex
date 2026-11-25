package main

import (
	"regexp"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// projectSheet is the New Project sheet: a host, and T3 Code's add-project
// browser copied — a path field that is the browser
// (apps/web/src/components/CommandPalette.tsx in pingdotgg/t3code). The
// text to its last separator is the directory listed under "Directories";
// the leaf after it filters the rows, names the folder a row opens, and
// creates a folder no row names. ".." goes up, Tab completes or enters a
// row, Enter adds the folder — ⌘/Ctrl+Enter even over a highlighted row —
// and the system's folder panel is the Choose… button. A project's folder
// does not change: another folder is another project.
type projectSheet struct {
	open bool
	// host is the host browsed, nil for this machine.
	host *hostConn
	// want is the directory to list next — the path field's directory
	// part — and hidden whether the listing is to show hidden folders.
	// asked tells that its listing was asked for, and loading that it is
	// on its way. gen tells the answer of the listing asked last.
	want           string
	hidden         bool
	asked, loading bool
	gen            int
	// res is the directory listed, failed why it could not be.
	res    rex.DirListResult
	failed string
	// path is the field that browses; prevPath its text last frame, which
	// a pasted path is told from.
	path, prevPath string
	// sel is the row the arrow keys highlight, -1 for none; reveal
	// scrolls it into view in the next frame. from is the folder browsed
	// before, highlighted as Up lists its parent. seenLeaf is the leaf
	// of the last frame: a new leaf drops the highlight.
	sel      int
	reveal   bool
	from     string
	seenLeaf string
	// pathFocused tells that the path field had the focus last frame;
	// caretEnd asks for the caret at the field's end, as a write of the
	// field leaves it where it was; focusPath asks for the focus back on
	// the field, as a row clicked takes it.
	pathFocused, caretEnd, focusPath bool
	// making is a dir.mkdir on its way for the leaf; makeErr its error.
	making  bool
	makeErr string
}

// sheetEntry is a row of the folder list: ".." for the parent folder, or
// a folder by name.
type sheetEntry struct {
	name string
	up   bool
}

// openSheet opens the sheet on host h, nil for this machine, in dir, "~"
// for the home directory.
func (a *App) openSheet(h *hostConn, dir string) {
	// gen goes on from the sheet before, whose listings still on their
	// way are not this one's.
	a.sheet = projectSheet{open: true, host: h, gen: a.sheet.gen + 1, sel: -1}
	a.sheet.browse(dir)
	a.sheet.want = a.sheet.dir()
	a.closeMenus()
	a.focusReq = nil
	if h != nil && h.current() == nil {
		a.connectHostNow(h)
	}
}

// split reads the path field as the directory to list and the filter of
// its folders: dir is the text to the last separator, leaf what is left.
// "~" is the home directory and "C:" its root. A backslash is a
// separator on a Windows host, or in a path that looks like Windows' —
// a share or a drive — before the host's first listing says so.
func (s *projectSheet) split() (dir, leaf string) {
	p := s.path
	seps := "/"
	if win := s.windows() || looksWindows(p); win {
		seps = `/\`
	}
	if i := strings.LastIndexAny(p, seps); i >= 0 {
		return p[:i+1], p[i+1:]
	}
	switch {
	case p == "~":
		return "~", ""
	case len(p) == 2 && p[1] == ':' && seps == `/\`:
		return p + `\`, ""
	}
	return "", p
}

// dir is the directory the path field names, leaf the filter after it.
func (s *projectSheet) dir() string  { d, _ := s.split(); return d }
func (s *projectSheet) leaf() string { _, l := s.split(); return l }

// looksWindows tells a Windows path apart on a host not yet listed: a
// share's \\ or a drive's letter.
func looksWindows(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 2 && p[1] == ':' && (len(p) == 2 || p[2] == '\\' || p[2] == '/')
}

// pathSet writes the field's text from the sheet's navigation: dir ends
// in a separator, which lists it.
func (s *projectSheet) pathSet(dir string) {
	if dir != "" && !strings.HasSuffix(dir, "/") && !strings.HasSuffix(dir, `\`) {
		dir += s.sep()
	}
	s.path = dir
	s.caretEnd = true
}

// sep is the separator the path field writes: the host's.
func (s *projectSheet) sep() string {
	if s.windows() {
		return `\`
	}
	return "/"
}

// sync has the listing follow the path field: a pasted path replaces the
// text as resolvePastedPath says; a new directory part, or a leaf that
// now names hidden folders, asks for a new listing; a new leaf drops the
// highlight.
func (s *projectSheet) sync() {
	if p := resolvePastedPath(s.prevPath, s.path); p != s.path {
		s.path, s.caretEnd = p, true
	}
	s.prevPath = s.path
	d, l := s.split()
	hidden := strings.HasPrefix(l, ".")
	if d != s.want || hidden != s.hidden {
		s.want, s.hidden, s.asked, s.failed = d, hidden, false, ""
		s.sel = -1
		s.gen++
	}
	if l != s.seenLeaf {
		s.seenLeaf = l
		s.sel = -1
	}
}

// browse points the path field at dir, "" for the home directory.
func (s *projectSheet) browse(dir string) {
	s.from = ""
	if dir == "" {
		s.path, s.caretEnd = "~/", true
		return
	}
	s.pathSet(dir)
}

// entries are the rows of the folder list: ".." for the parent when
// there is one, then the folders the leaf keeps, ordered as T3 Code's
// browse scores them (leafScore).
func (s *projectSheet) entries() []sheetEntry {
	r := s.res
	if !s.asked || s.failed != "" || r.Code != "" || s.loading && len(r.Dirs) == 0 {
		return nil
	}
	var out []sheetEntry
	if r.Parent != "" {
		out = append(out, sheetEntry{up: true})
	}
	leaf := s.leaf()
	type scored struct {
		e     sheetEntry
		score int
	}
	var rest []scored
	for _, d := range r.Dirs {
		score := 0
		if leaf != "" {
			if score = leafScore(d, leaf); score < 0 {
				continue
			}
		}
		rest = append(rest, scored{sheetEntry{name: d}, score})
	}
	slices.SortStableFunc(rest, func(x, y scored) int { return x.score - y.score })
	for _, m := range rest {
		out = append(out, m.e)
	}
	return out
}

// leafScore ranks a folder for the leaf, as T3 Code's scoreQueryMatch
// ranks a folder of the directory listed (searchRanking.ts in
// pingdotgg/t3code): an exact name, then a prefix, one at a boundary,
// one inside, and a fuzzy one, each tier of 1000 apart, better inside
// its tier the sooner it matches and the less longer the name is. -1
// says it does not match.
func leafScore(name, leaf string) int {
	v := []rune(strings.ToLower(name))
	q := []rune(strings.ToLower(leaf))
	if len(v) == 0 || len(q) == 0 {
		return -1
	}
	penalty := min(64, max(0, len(v)-len(q)))
	if string(v) == string(q) {
		return 0
	}
	if strings.HasPrefix(string(v), string(q)) {
		return 1000 + penalty
	}
	// A boundary marker (" ", "-", "_", ".") right before the leaf.
	best := -1
	for _, m := range []rune{' ', '-', '_', '.'} {
		if i := runesIndex(v, append([]rune{m}, q...)); i >= 0 {
			if at := i + 1; best < 0 || at < best {
				best = at
			}
		}
	}
	if best >= 0 {
		return 2000 + best*2 + penalty
	}
	if i := runesIndex(v, q); i >= 0 {
		return 3000 + i*2 + penalty
	}
	if fs := fuzzyScore(v, q); fs >= 0 {
		return 4000 + fs
	}
	return -1
}

// runesIndex is strings.Index for runes: q's place in v, -1 for none.
func runesIndex(v, q []rune) int {
	for i := 0; i+len(q) <= len(v); i++ {
		if slices.Equal(v[i:i+len(q)], q) {
			return i
		}
	}
	return -1
}

// fuzzyScore is T3 Code's scoreSubsequenceMatch: the leaf's letters in
// order in the name, scored on where it starts, the gaps it skips, the
// span it ends and the name's extra length.
func fuzzyScore(v, q []rune) int {
	if len(q) == 0 {
		return 0
	}
	qi, first, prev, gap := 0, -1, -1, 0
	for vi := 0; vi < len(v); vi++ {
		if v[vi] != q[qi] {
			continue
		}
		if first == -1 {
			first = vi
		}
		if prev != -1 {
			gap += vi - prev - 1
		}
		prev = vi
		qi++
		if qi == len(q) {
			span := vi - first + 1 - len(q)
			return first*2 + gap*3 + span + min(64, len(v)-len(q))
		}
	}
	return -1
}

// step moves the highlighted row by d, from the first or last row when
// none is.
func (s *projectSheet) step(d int) {
	n := len(s.entries())
	if n == 0 {
		return
	}
	switch {
	case s.sel < 0 && d > 0:
		s.sel = 0
	case s.sel < 0:
		s.sel = n - 1
	default:
		s.sel = min(max(s.sel+d, 0), n-1)
	}
	s.reveal = true
}

// activate opens a row: ".." lists the parent, a folder goes into it.
func (s *projectSheet) activate(e sheetEntry) {
	s.focusPath = true
	if e.up {
		s.up()
		return
	}
	s.from = ""
	s.pathSet(s.dir() + e.name)
}

// openSel opens the highlighted row, and tells whether there was one.
func (s *projectSheet) openSel() bool {
	es := s.entries()
	if s.sel < 0 || s.sel >= len(es) {
		return false
	}
	s.activate(es[s.sel])
	return true
}

// tab is the shell's Tab on the path field, as T3 Code's add-project
// does it: the highlighted row opens — ".." goes up, a folder goes in —
// and with none highlighted the folders that start with the leaf open
// their only one, or extend the leaf to the start they share; none do
// nothing.
func (s *projectSheet) tab() {
	if s.openSel() {
		return
	}
	var matches []string
	for _, e := range s.entries() {
		if !e.up && strings.HasPrefix(strings.ToLower(e.name), strings.ToLower(s.leaf())) {
			matches = append(matches, e.name)
		}
	}
	switch len(matches) {
	case 0:
	case 1:
		s.activate(sheetEntry{name: matches[0]})
	default:
		p := matches[0]
		for _, m := range matches[1:] {
			p = commonStart(p, m)
		}
		if utf8.RuneCountInString(p) > utf8.RuneCountInString(s.leaf()) {
			s.path = s.dir() + p
			s.caretEnd = true
		}
	}
}

// commonStart is the runes a and b start with, in a's case.
func commonStart(a, b string) string {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	n := min(len(ra), len(rb))
	i := 0
	for i < n && ra[i] == rb[i] {
		i++
	}
	return string([]rune(a)[:i])
}

// up lists the parent folder, if there is one.
func (s *projectSheet) up() {
	if p := s.res.Parent; p != "" && !s.loading {
		s.from = s.res.Path
		s.pathSet(p)
	}
}

// sheetClient is the client of the host the sheet browses, nil while it
// is not connected.
func (a *App) sheetClient() *rex.Client {
	if a.sheet.host == nil {
		return a.client
	}
	return a.sheet.host.current()
}

// pickable tells whether the sheet can browse host h, nil for this
// machine: a host that is connected.
func pickable(h *hostConn) bool {
	return h == nil || h.current() != nil
}

// pickHost has the sheet browse host h from its home directory.
func (a *App) pickHost(h *hostConn) {
	s := &a.sheet
	if s.host == h || !pickable(h) {
		return
	}
	s.host, s.res = h, rex.DirListResult{}
	s.asked = false
	s.browse("")
	if h != nil && h.current() == nil {
		a.connectHostNow(h)
	}
}

// listSheet asks the host for the directory the sheet wants, once its
// client is there; the answer comes on the main thread.
func (a *App) listSheet() {
	s := &a.sheet
	if !s.open || s.asked {
		return
	}
	c := a.sheetClient()
	if c == nil {
		return
	}
	s.asked, s.loading = true, true
	gen, host, want, hidden, update := s.gen, s.host, s.want, s.hidden, a.updater()
	go func() {
		r, err := c.DirList(want, hidden)
		update(func() {
			if !a.sheet.open || a.sheet.gen != gen || a.sheet.host != host {
				return
			}
			a.sheet.listed(r, err)
		})
	}()
}

// listed takes the answer of a listing.
func (s *projectSheet) listed(r rex.DirListResult, err error) {
	s.loading = false
	if err != nil {
		s.failed = strings.TrimPrefix(err.Error(), "rex: ")
		return
	}
	s.res = r
	if r.Code != "" {
		return
	}
	// Up highlights the folder it came from.
	if s.from != "" {
		for i, e := range s.entries() {
			if !e.up && childPath(r.Path, e.name, s.windows()) == s.from {
				s.sel, s.reveal = i, true
				break
			}
		}
		s.from = ""
	}
}

// windows tells whether the host browsed runs Windows, by its paths.
func (s *projectSheet) windows() bool {
	return winPath(s.res.Home) || winPath(s.res.Path)
}

// resolved is the folder the sheet's Enter and Add button take, and
// whether it must be made first: the directory listed for an empty leaf,
// the folder a leaf names exactly, or the folder under it the leaf asks
// to create (dir.mkdir, then project.create — the folder it made is not
// made twice).
func (s *projectSheet) resolved() (dir string, create bool) {
	r := s.res
	if leaf := s.leaf(); leaf != "" {
		for _, d := range r.Dirs {
			if d == leaf || s.windows() && strings.EqualFold(d, leaf) {
				return childPath(r.Path, d, s.windows()), false
			}
		}
		return childPath(r.Path, leaf, s.windows()), true
	}
	return r.Path, false
}

// canSubmit tells whether Enter and the Add button can go: a directory
// is listed and no creation is under way. Windows' list of drives is not
// a folder.
func (s *projectSheet) canSubmit() bool {
	r := s.res
	return s.asked && !s.loading && !s.making && s.failed == "" && r.Code == "" &&
		r.Path != "" && !(r.Path == rex.DirTop && s.windows())
}

// willCreate tells whether submitting makes the folder first — the leaf
// names no folder of the directory listed — which the button says with
// "Create & Add".
func (s *projectSheet) willCreate() bool {
	if !s.canSubmit() {
		return false
	}
	_, create := s.resolved()
	return create
}

// sheetSubmit takes the field's path: the folder resolved(), making it
// first when the leaf asks for one.
func (a *App) sheetSubmit() {
	s := &a.sheet
	if !s.open || !s.canSubmit() {
		return
	}
	dir, create := s.resolved()
	if !create {
		s.open = false
		a.createProject(s.host, dir, "")
		return
	}
	c := a.sheetClient()
	if c == nil {
		return
	}
	s.making, s.makeErr = true, ""
	gen, host, update := s.gen, s.host, a.updater()
	go func() {
		err := c.DirMkdir(dir)
		update(func() {
			s := &a.sheet
			if !s.open || s.gen != gen || s.host != host {
				return
			}
			s.making = false
			if err != nil {
				s.makeErr = strings.TrimPrefix(err.Error(), "rex: ")
				return
			}
			s.open = false
			a.createProject(host, dir, "")
		})
	}()
}

// childPath is the directory name in dir.
func childPath(dir, name string, windows bool) string {
	switch {
	case windows && dir == rex.DirTop:
		return name // a drive's root, as C:\
	case windows:
		return strings.TrimSuffix(dir, `\`) + `\` + name
	case dir == "/":
		return "/" + name
	}
	return dir + "/" + name
}

// folderName is the last name of path, the project's name by default: ""
// for a root, as / or a drive's C:\, which has none.
func folderName(path string) string {
	t := strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(t, `/\`); i >= 0 {
		t = t[i+1:]
	}
	if len(t) == 2 && t[1] == ':' {
		return "" // C:, a drive
	}
	return t
}

// chooseFolder opens the system's folder picker, for this machine, and
// browses the folder chosen.
func (a *App) chooseFolder() {
	win, from, update := a.win, a.sheet.res.Path, a.updater()
	if win == nil {
		return
	}
	gen := a.sheet.gen
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Directory: true, DefaultPath: from})
		if err != nil || len(paths) == 0 {
			return
		}
		update(func() {
			if s := &a.sheet; s.open && s.host == nil && s.gen == gen {
				s.browse(paths[0])
				s.focusPath = true
			}
		})
	}()
}

// quotedPathAtEnd ends pasted text that holds a path ready for a shell:
// '/a b', /a\'s\ b, or "C:\a b" — as T3 Code's resolvePastedPath reads
// it.
var quotedPathAtEnd = regexp.MustCompile(`(?:'((?:[^']|'\\'')+)'|"([^"]+)")\s*$`)

var shellEscape = regexp.MustCompile(`\\([\s'"\\()[\]{}<>|&;$` + "`" + `!*?#~^=])`)

// isPastedFolderPath tells whether pasted text names a folder: an
// absolute path, or "~/" of home; a "~\" the browser does not take.
func isPastedFolderPath(v string) bool {
	return strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") ||
		strings.HasPrefix(v, `\\`) ||
		len(v) >= 2 && v[1] == ':' && (len(v) == 2 || v[2] == '/' || v[2] == '\\')
}

// resolvePastedPath is what the field holds after a change, as T3 Code's
// resolvePastedPath says (packages/shared/src/path.ts in pingdotgg/
// t3code): a full path pasted replaces the text, even after what is
// there, since appending it would create a folder under it.
func resolvePastedPath(prev, next string) string {
	typing := strings.HasPrefix(next, prev) && len(next) == len(prev)+1 || strings.HasPrefix(prev, next)
	if typing {
		return next
	}
	m := quotedPathAtEnd.FindStringSubmatchIndex(next)
	// Text before the quote that wasn't in the field is not a paste to
	// put after it.
	if m != nil && strings.HasPrefix(prev, strings.TrimRight(next[:m[0]], " ")) {
		quoted := ""
		if m[2] >= 0 {
			quoted = strings.ReplaceAll(next[m[2]:m[3]], `'\''`, "'")
		} else if m[4] >= 0 {
			quoted = next[m[4]:m[5]]
		}
		if isPastedFolderPath(quoted) {
			return quoted
		}
	}
	// Without a trailing separator, /a then /a/b is a longer path, not
	// one pasted after a folder.
	path := next
	if (strings.HasSuffix(prev, "/") || strings.HasSuffix(prev, `\`)) && strings.HasPrefix(next, prev) {
		path = next[len(prev):]
	}
	if !isPastedFolderPath(path) {
		return next
	}
	// "\" separates folders in Windows paths, so it only escapes in "/"
	// and "~/" paths.
	if looksWindows(path) {
		return path
	}
	return shellEscape.ReplaceAllString(path, "$1")
}

// projectSheetView draws the sheet while it is open.
func (a *App) projectSheetView(c *ui.Context, k *colors) {
	s := &a.sheet
	if !s.open {
		return
	}
	if s.host != nil && !slices.Contains(a.hosts, s.host) {
		// Its host removed meanwhile.
		s.host, s.res = nil, rex.DirListResult{}
		s.path, s.want, s.asked = "", "", false
	}
	s.sync()
	a.listSheet()
	title := locales.ProjectSheetTitleOn(a.hostName(s.host))
	was := s.open
	ui.DialogBase(c.Key("project-sheet"), &s.open, func(backdrop, panel ui.Element) {
		dialogEnter(c, backdrop, panel)
		backdrop.Background(k.backdrop)
		winW, winH := c.Size()
		panel.Width(min(540, winW-24)).MaxHeight(winH - 24).Padding(20).Radius(k.radius(radiusXXL))
		frostPanel(panel, k, k.radius(radiusXXL))
		dialogShadow(panel, k).Role(ui.RoleDialog).Label(title)
		// What does not fit a short window scrolls.
		ui.Scroll(c.Key("sheet-body")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, title).FontSize(fontTitle).FontWeight(700).TextColor(k.text).Margin(0, 0, 6, 0)
				sheetLabel(c, k, locales.ProjectSheetHost())
				a.sheetHosts(c, k)
				// The folders take what room the window leaves.
				room := winH - 420
				a.sheetBrowser(c, k, min(max(room, 84), 220))
			})
		})
		// As the palette's "Open in Finder": the system's folder panel.
		if s.host == nil && a.win != nil {
			ui.Row(c).Shrink(0).Justify(ui.End).Margin(10, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.ProjectSheetChoose()).Clicked() {
					a.chooseFolder()
				}
			})
		}
	})
	if was && !s.open {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// sheetLabel is the label over a part of the sheet.
func sheetLabel(c *ui.Context, k *colors, text string) {
	ui.Text(c, text).FontSize(fontSubhead).FontWeight(600).TextColor(k.textMuted).Margin(4, 0, 0, 0)
}

// sheetHosts are the hosts the sheet can browse: this machine and the
// saved hosts, one chosen. One that is offline shows, dimmed, and cannot
// be chosen.
func (a *App) sheetHosts(c *ui.Context, k *colors) {
	ui.Row(c).Gap(6).Wrap().Role(ui.RoleRadioGroup).Label(locales.ProjectSheetHost()).Children(func() {
		for _, h := range a.allHosts() {
			name, on := a.hostName(h), pickable(h)
			glyph := ""
			if h == nil {
				_, _, glyph = a.hostIdentity()
			} else {
				glyph = h.remoteGlyph()
			}
			chosen := a.sheet.host == h
			b := ui.Row(c.Key("sheet-host-"+hostKey(h))).Height(28).Padding(0, 10).Gap(6).Radius(k.capsule(28)).AlignItems(ui.Center).
				Role(ui.RoleRadio).Checked(chosen).Label(name).Border(k.borderW(), k.panelBorder).Focusable()
			switch {
			case chosen:
				b.Background(k.hover).Border(1, k.accent)
			case on && b.Hovered():
				b.Background(k.hover)
			}
			if on {
				b.Cursor(ui.CursorPointer)
				if b.Clicked() {
					a.pickHost(h)
				}
			} else {
				label, _, _ := hostStatus(h)
				b.Disabled(true).Tooltip(label)
				dimmed(b, 0.45)
			}
			b.Children(func() {
				glyphIcon(c, glyph, 14, k.textMuted).Shrink(0)
				ui.Text(c, name).FontSize(fontCallout).TextColor(k.text).SingleLine()
			})
		}
	})
}

// sheetBrowser is the folder browser, T3 Code's add-project view: the
// path field — a folder's glyph, the text, and the Add button inside it —
// over the directory it lists under "Directories": ".." for the parent
// first, then the folders the leaf keeps. A click opens a folder, Enter
// takes the field's path, and a leaf that names no folder creates it.
func (a *App) sheetBrowser(c *ui.Context, k *colors, listH float32) {
	s := &a.sheet
	r, win := s.res, s.windows()
	var in ui.Element
	box := ui.Column(c.Key("browser")).Radius(k.radius(radiusM)).Border(k.borderW(), k.panelBorder).Background(k.field).Clip().Shrink(0)
	box.Children(func() {
		ui.Row(c).Height(34).AlignItems(ui.Center).Children(func() {
			glyphIcon(c, "folder-plus", 16, k.iconMuted).Shrink(0).Margin(0, 0, 0, 10)
			// The cell the field and its path overlay share.
			ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
				in = styledField(c, k, func(c *ui.Context) ui.Element {
					return ui.TextInputBase(c.Key("project-path"), &s.path)
				}).Grow(1).MinWidth(0).FontSize(fontBody).Padding(0, 4).Background(ui.Transparent).Border(0, ui.Transparent).
					Label(locales.ProjectSheetPath()).Placeholder(locales.ProjectSheetPathHint()).AutoFocus()
				focused := in.Focused()
				if focused && (!s.pathFocused || s.caretEnd) {
					// The caret goes to the end, which the field then shows.
					n := utf8.RuneCountInString(s.path)
					in.SetTextSelection(n, n)
				}
				s.pathFocused, s.caretEnd = focused, false
				if s.focusPath && !focused {
					in.Focus()
				}
				s.focusPath = false
				in.OnTextKey(func(k ui.TextKey) bool {
					switch {
					case k.Key == ui.KeyEnter:
						if k.Mods&(ui.Cmd|ui.Ctrl) != 0 || !s.openSel() {
							// ⌘/Ctrl+Enter submits over a highlighted row.
							a.later(c, a.sheetSubmit)
						}
						return true
					case k.Key == ui.KeyTab && k.Mods == 0:
						s.tab()
						return true
					case k.Key == ui.KeyDown && k.Mods == 0:
						s.step(1)
						return true
					case k.Key == ui.KeyUp && k.Mods == 0:
						s.step(-1)
						return true
					}
					return false
				})
				if focused || s.path == "" {
					return
				}
				in.TextColor(ui.Transparent)
				// Over the field, where its text is: its padding and border.
				ui.Row(c).Absolute().Left(4).Right(4).Top(0).Bottom(0).AlignItems(ui.Center).PassThrough().Children(func() {
					ui.Text(c, s.path).FontSize(fontBody).TextColor(k.text).SingleLine().TruncateStart().MinWidth(0)
				})
			})
			a.sheetAddButton(c, k)
		})
		if in.Focused() {
			box.BorderColor(k.accent)
		}
		ui.Divider(c).Background(k.panelBorder)
		listBox := ui.Box(c.Key("folders")).Height(listH).Label(locales.ProjectSheetDirectories())
		listBox.Children(func() {
			say := func(text string) {
				emptyMenu(c, k, "sheet-"+text, text)
			}
			switch {
			case s.host != nil && s.host.current() == nil:
				say(locales.ProjectSheetConnecting(a.hostName(s.host)))
				return
			case s.failed != "":
				say(s.failed)
				return
			case s.loading && len(r.Dirs) == 0 || !s.asked:
				say(locales.ProjectSheetLoading())
				return
			case r.Code == rex.DirNotFound:
				say(locales.ProjectSheetNotFound())
				return
			case r.Code == rex.DirDenied:
				say(locales.ProjectSheetDenied())
				return
			}
			es := s.entries()
			reveal := s.reveal
			s.reveal = false
			ui.Scroll(c.Key("folder-list")).Grow(1).MinHeight(0).Padding(4).Children(func() {
				ui.Column(c).Gap(2).Role(ui.RoleList).Label(locales.ProjectSheetDirectories()).Children(func() {
					ui.Text(c, locales.ProjectSheetDirectories()).FontSize(fontFootnote).TextColor(k.textMuted).Padding(2, 8, 0, 8)
					glyph := "folder"
					if win && r.Path == rex.DirTop {
						glyph = "hard-drive"
					}
					for i, e := range es {
						sel := i == s.sel
						key, icon, name, label := "dir-"+e.name, glyph, e.name, e.name
						if e.up {
							key, icon, name, label = "dir-up", "corner-left-up", "..", locales.ProjectSheetUp()
						}
						row := ui.Row(c.Key(key)).Height(28).Padding(0, 8).Gap(8).Radius(concentric(k.radius(radiusM), 5)).AlignItems(ui.Center).
							Cursor(ui.CursorPointer).Role(ui.RoleListItem).Label(label).AccessSelected(sel)
						switch {
						case sel:
							row.Background(k.panelSel)
							if reveal {
								row.ScrollIntoView()
							}
						case row.Hovered():
							row.Background(k.hover)
						}
						if row.Clicked() {
							s.activate(e)
						}
						row.Children(func() {
							glyphIcon(c, icon, 15, k.textMuted).Shrink(0)
							ui.Text(c, name).FontSize(fontBody).TextColor(k.text).SingleLine().TruncateMiddle().MinWidth(0)
						})
					}
					// A leaf that names no folder creates it on Enter.
					if s.willCreate() {
						ui.Text(c, locales.ProjectSheetCreateHint()).FontSize(fontFootnote).TextColor(k.textMuted).Padding(6, 8, 2, 8)
					}
					if s.makeErr != "" {
						ui.Text(c, s.makeErr).FontSize(fontFootnote).TextColor(k.textMuted).Padding(0, 8, 2, 8)
					}
					if r.More {
						ui.Text(c, locales.ProjectSheetMore(rex.DirListMax)).FontSize(fontFootnote).TextColor(k.textMuted).Padding(6, 8)
					}
				})
			})
		})
	})
}

// sheetAddButton is the submit the path field holds at its end, as T3
// Code's CommandPaletteSubmitButton: "Add" for the folder the path
// names, "Create & Add" for one it creates, and the key that submits —
// Enter, or ⌘/Ctrl+Enter while a row is highlighted.
func (a *App) sheetAddButton(c *ui.Context, k *colors) {
	s := &a.sheet
	label := locales.ProjectSheetAdd()
	if s.making {
		label = locales.ProjectSheetWorking()
	} else if s.willCreate() {
		label = locales.ProjectSheetCreateAdd()
	}
	mod := "⌘"
	if runtime.GOOS != "darwin" {
		mod = "Ctrl+"
	}
	kbd := "↩"
	if s.sel >= 0 {
		kbd = mod + "↩"
	}
	b := ui.Row(c.Key("add")).Shrink(0).Margin(0, 6, 0, 0).Height(22).Padding(0, 7).Gap(4).AlignItems(ui.Center).
		Radius(concentric(k.radius(radiusM), 3)).Border(k.borderW(), k.panelBorder).Label(label)
	switch {
	case s.canSubmit() && b.Hovered():
		b.Background(k.hover)
		b.Cursor(ui.CursorPointer)
	case s.canSubmit():
		b.Cursor(ui.CursorPointer)
	default:
		b.Disabled(true)
		dimmed(b, 0.45)
	}
	if s.canSubmit() && b.Clicked() {
		a.later(c, a.sheetSubmit)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(fontCallout).FontWeight(500).TextColor(k.text).SingleLine()
		ui.Text(c, kbd).FontSize(fontFootnote).TextColor(k.textMuted).SingleLine()
	})
}
