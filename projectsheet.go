package main

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// projectSheet is the New Project sheet: a host and a folder on it, chosen
// by a path field that is the browser (docs/MOD-002-projects.md, "New
// Project"): the text to its last separator is the directory listed, the
// leaf after it filters the folders shown, as T3 Code's add-project input
// works. A project's folder does not change: another folder is another
// project.
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
	// path is the field that browses; name the project's name, which
	// follows the chosen folder's (auto) until the user changes it.
	path, name, auto string
	// sel is the row the arrow keys highlight, -1 for none; reveal
	// scrolls it into view in the next frame. from is the folder browsed
	// before, highlighted as Up lists its parent. seenLeaf is the leaf
	// of the last frame: a new leaf drops the highlight.
	sel      int
	reveal   bool
	from     string
	seenLeaf string
	// focusList gives the list the focus in the next frame, as the arrow
	// keys highlight a row, so that Enter opens it.
	focusList bool
	// pathFocused tells that the path field had the focus last frame;
	// caretEnd asks for the caret at the field's end, as a write of the
	// field leaves it where it was.
	pathFocused, caretEnd bool
}

// sheetEntry is a row of the folder list: ".." for the parent folder, or
// a folder by name.
type sheetEntry struct {
	name string
	up   bool
}

// openSheet opens the sheet on host h, nil for this machine, in dir, ""
// for the home directory.
func (a *App) openSheet(h *hostConn, dir string) {
	// gen goes on from the sheet before, whose listings still on their
	// way are not this one's.
	a.sheet = projectSheet{open: true, host: h, gen: a.sheet.gen + 1, sel: -1}
	a.sheet.pathSet(dir)
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

// sync has the listing follow the path field: a new directory part, or a
// leaf that now names hidden folders, asks for a new listing; a new leaf
// drops the highlight; and the name follows the folder chosen till it is
// typed.
func (s *projectSheet) sync() {
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
	if dir := s.chosen(); dir != "" {
		if base := folderName(dir); s.name == s.auto {
			s.name, s.auto = base, base
		} else {
			s.auto = base
		}
	}
}

// browse points the path field at dir, "" for the home directory.
func (s *projectSheet) browse(dir string) {
	s.from = ""
	s.pathSet(dir)
}

// entries are the rows of the folder list: ".." for the parent when
// there is one, then the folders the leaf keeps, those it starts first.
func (s *projectSheet) entries() []sheetEntry {
	r := s.res
	if !s.asked || s.failed != "" || r.Code != "" || s.loading && len(r.Dirs) == 0 {
		return nil
	}
	var out, rest []sheetEntry
	if r.Parent != "" {
		out = append(out, sheetEntry{up: true})
	}
	leaf := strings.ToLower(s.leaf())
	for _, d := range r.Dirs {
		if leaf != "" && !fuzzy(strings.ToLower(d), leaf) {
			continue
		}
		e := sheetEntry{name: d}
		if strings.HasPrefix(strings.ToLower(d), leaf) {
			out = append(out, e)
		} else {
			rest = append(rest, e)
		}
	}
	return append(out, rest...)
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
	s.reveal, s.focusList = true, true
}

// activate opens a row: ".." lists the parent, a folder goes into it.
func (s *projectSheet) activate(e sheetEntry) {
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

// openLeaf opens the folder the leaf names: its exact match, else the
// first that matches; it tells whether there was one.
func (s *projectSheet) openLeaf() bool {
	es := s.entries()
	leaf := s.leaf()
	if i := slices.IndexFunc(es, func(e sheetEntry) bool {
		return !e.up && strings.EqualFold(e.name, leaf)
	}); i >= 0 {
		s.activate(es[i])
		return true
	}
	if i := slices.IndexFunc(es, func(e sheetEntry) bool { return !e.up }); i >= 0 {
		s.activate(es[i])
		return true
	}
	return false
}

// complete is the shell's Tab on the path field: the only folder that
// starts with the leaf opens it, several extend the leaf to their common
// start, none do nothing.
func (s *projectSheet) complete() {
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

// chosen is the folder the sheet would use, "" for none: the directory
// listed, or the folder a leaf names exactly — which is not Windows'
// list of drives.
func (s *projectSheet) chosen() string {
	r := s.res
	if s.loading || s.failed != "" || r.Code != "" || r.Path == "" || r.Path == rex.DirTop && s.windows() {
		return ""
	}
	if leaf := s.leaf(); leaf != "" {
		if i := slices.IndexFunc(r.Dirs, func(d string) bool { return strings.EqualFold(d, leaf) }); i >= 0 {
			return childPath(r.Path, r.Dirs[i], s.windows())
		}
		return ""
	}
	return r.Path
}

// ready tells whether Create can go: a folder is chosen and the project
// has a name, typed or the folder's. A drive's root has no name of its
// own.
func (s *projectSheet) ready() bool {
	dir := s.chosen()
	return dir != "" && (strings.TrimSpace(s.name) != "" || folderName(dir) != "")
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
			}
		})
	}()
}

// finishSheet makes the project and closes the sheet.
func (a *App) finishSheet() {
	s := a.sheet
	dir := s.chosen()
	if !s.open || !s.ready() {
		return
	}
	a.sheet.open = false
	a.createProject(s.host, dir, s.name)
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
		var pathIn ui.Element
		// What does not fit a short window scrolls; the buttons stay.
		ui.Scroll(c.Key("sheet-body")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, title).FontSize(fontTitle).FontWeight(700).TextColor(k.text).Margin(0, 0, 6, 0)
				sheetLabel(c, k, locales.ProjectSheetHost())
				a.sheetHosts(c, k)
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					sheetLabel(c, k, locales.ProjectSheetFolder())
					ui.Box(c).Grow(1)
					if s.host == nil && a.win != nil {
						if ui.Button(c, locales.ProjectSheetChoose()).Shrink(0).Clicked() {
							a.chooseFolder()
						}
					}
				})
				// The folders take what room the window leaves, so that
				// the buttons stay in sight in a short window.
				room := winH - 500
				pathIn = a.sheetBrowser(c, k, min(max(room, 84), 220))
				sheetLabel(c, k, locales.ProjectSheetName())
				placeholder := s.auto
				if placeholder == "" {
					placeholder = locales.ProjectSheetNameHint()
				}
				in := textField(c.Key("project-name"), k, &s.name, fieldRegular).Label(locales.ProjectSheetName()).Placeholder(placeholder)
				if in.Submitted() {
					a.later(c, a.finishSheet)
				}
			})
		})
		a.sheetChoice(c, k)
		ui.Row(c).Shrink(0).Gap(8).Justify(ui.End).Margin(10, 0, 0, 0).Children(func() {
			if ui.Button(c, locales.ProjectSheetCancel()).Clicked() {
				s.open = false
			}
			if ui.PrimaryButton(c, locales.ProjectSheetCreate()).Disabled(!s.ready()).Clicked() {
				a.later(c, a.finishSheet)
			}
		})
		a.sheetKeys(panel, pathIn.Focused())
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

// sheetChoice says, over the buttons, the folder Create takes, or why it
// takes none.
func (a *App) sheetChoice(c *ui.Context, k *colors) {
	s := &a.sheet
	dir := s.chosen()
	glyph, text := "folder", ""
	switch {
	case dir != "" && !s.ready():
		glyph, text = "circle-alert", locales.ProjectSheetNameNeeded()
	case dir != "":
		text = locales.ProjectSheetChosen(dir)
	case s.loading || !s.asked:
	default:
		glyph, text = "info", locales.ProjectSheetNoChoice()
	}
	ui.Row(c.Key("sheet-choice")).Shrink(0).Height(20).Gap(6).AlignItems(ui.Center).Margin(12, 0, 0, 0).Children(func() {
		if text == "" {
			return
		}
		glyphIcon(c, glyph, 14, k.textMuted).Shrink(0)
		t := ui.Text(c, text).FontSize(fontCallout).TextColor(k.textMuted).SingleLine().TruncateMiddle().MinWidth(0)
		if dir != "" {
			t.Tooltip(dir)
		}
	})
}

// sheetKeys takes the sheet's keys: Up and Down highlight a folder, Enter
// opens it (in the list, sheetBrowser) or, in the field, the folder the
// leaf names; Backspace and Alt+Up list the parent, and Tab completes
// the leaf while the field has the focus. A text field with the focus
// takes Backspace first.
func (a *App) sheetKeys(panel ui.Element, pathFocused bool) {
	s := &a.sheet
	down, up := panel.Shortcut(0, ui.KeyDown), panel.Shortcut(0, ui.KeyUp)
	altUp, back := panel.Shortcut(ui.Alt, ui.KeyUp), panel.Shortcut(0, ui.KeyBackspace)
	switch {
	case down:
		s.step(1)
	case up:
		s.step(-1)
	case altUp || back:
		s.up()
	case pathFocused && panel.Shortcut(0, ui.KeyTab):
		s.complete()
	}
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

// sheetBrowser is the folder browser: the path field is the browser, its
// directory part listed and its leaf the filter of the folder rows under
// it, in one box. A click opens a folder, ".." the parent; on this
// machine Choose… opens the system's folder panel.
func (a *App) sheetBrowser(c *ui.Context, k *colors, listH float32) ui.Element {
	s := &a.sheet
	r, win := s.res, s.windows()
	var in ui.Element
	box := ui.Column(c.Key("browser")).Radius(k.radius(radiusM)).Border(k.borderW(), k.panelBorder).Background(k.field).Clip().Shrink(0)
	box.Children(func() {
		ui.Row(c).Height(34).AlignItems(ui.Center).Children(func() {
			in = styledField(c, k, func(c *ui.Context) ui.Element {
				return ui.TextInputBase(c.Key("project-path"), &s.path)
			}).Grow(1).MinWidth(0).FontSize(fontBody).Padding(0, 10).Background(ui.Transparent).Border(0, ui.Transparent).
				Label(locales.ProjectSheetPath()).Placeholder(locales.ProjectSheetPath()).AutoFocus()
			focused := in.Focused()
			if focused && (!s.pathFocused || s.caretEnd) {
				// The caret goes to the end, which the field then shows.
				n := utf8.RuneCountInString(s.path)
				in.SetTextSelection(n, n)
			}
			s.pathFocused, s.caretEnd = focused, false
			if in.Submitted() {
				switch {
				case s.openSel():
				case s.leaf() != "" && s.openLeaf():
				default:
					a.later(c, a.finishSheet)
				}
			}
			if focused || s.path == "" {
				return
			}
			in.TextColor(ui.Transparent)
			// Over the field, where its text is: its padding and border.
			ui.Row(c).Absolute().Left(10).Right(10).Top(0).Bottom(0).AlignItems(ui.Center).PassThrough().Children(func() {
				ui.Text(c, s.path).FontSize(fontBody).TextColor(k.text).SingleLine().TruncateStart().MinWidth(0)
			})
		})
		if in.Focused() {
			box.BorderColor(k.accent)
		}
		ui.Divider(c).Background(k.panelBorder)
		listBox := ui.Box(c.Key("folders")).Height(listH).Focusable().Label(locales.ProjectSheetFolder())
		if s.focusList {
			listBox.Focus()
			s.focusList = false
		}
		if listBox.Shortcut(0, ui.KeyEnter) {
			s.openSel()
		}
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
			dirs := 0
			for _, e := range es {
				if !e.up {
					dirs++
				}
			}
			if len(es) == 0 {
				if s.leaf() != "" {
					say(locales.ProjectSheetNoMatch())
				} else {
					say(locales.ProjectSheetEmpty())
				}
				return
			}
			reveal := s.reveal
			s.reveal = false
			ui.Scroll(c.Key("folder-list")).Grow(1).MinHeight(0).Padding(4).Children(func() {
				ui.Column(c).Gap(2).Role(ui.RoleList).Label(locales.ProjectSheetFolder()).Children(func() {
					glyph := "folder"
					if win && r.Path == rex.DirTop {
						glyph = "hard-drive"
					}
					for i, e := range es {
						sel := i == s.sel
						key, icon, name, label := "dir-"+e.name, glyph, e.name, e.name
						if e.up {
							key, icon, name, label = "dir-up", "arrow-up", "..", locales.ProjectSheetUp()
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
					// ".." alone says what an empty folder or a filter
					// that keeps nothing says.
					if dirs == 0 {
						if s.leaf() != "" {
							say(locales.ProjectSheetNoMatch())
						} else {
							say(locales.ProjectSheetEmpty())
						}
					}
					if r.More {
						ui.Text(c, locales.ProjectSheetMore(rex.DirListMax)).FontSize(fontFootnote).TextColor(k.textMuted).Padding(6, 8)
					}
				})
			})
		})
	})
	return in
}
