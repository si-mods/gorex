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

// projectSheet is the New Project sheet: a host, a folder on it chosen by
// browsing its directories (dir.list), and a name. The same sheet, of a
// project, changes the project's folder.
type projectSheet struct {
	open bool
	// space is the project whose folder the sheet changes, "" for New
	// Project.
	space string
	// host is the host browsed, nil for this machine.
	host *hostConn
	// want is the directory to list next, "" for the home directory;
	// asked tells that its listing was asked for, and loading that it is
	// on its way. gen tells the answer of the listing asked last.
	want           string
	asked, loading bool
	gen            int
	// res is the directory listed, failed why it could not be.
	res    rex.DirListResult
	failed string
	// path is the field that takes a path; name the project's name, which
	// follows the folder's (auto) until the user changes it.
	path, name, auto string
	// sel is the folder row the arrow keys highlight, -1 for none; reveal
	// scrolls it into view in the next frame. from is the folder browsed
	// before, highlighted as Up lists its parent.
	sel    int
	reveal bool
	from   string
	// focusList gives the list the focus in the next frame, as the arrow
	// keys highlight a row, so that Enter opens it.
	focusList bool
	// lost is the folder of a project whose home host is unknown, which
	// the sheet shows; held keeps the folder listed from being used until
	// the user browses, as it is this machine's home, not the project's.
	lost string
	held bool
	// pathFocused tells that the path field had the focus last frame.
	pathFocused bool
}

// openSheet opens the sheet: of project space ("" for New Project), on
// host h, nil for this machine, in dir, "" for the home directory.
func (a *App) openSheet(space string, h *hostConn, dir string) {
	// gen goes on from the sheet before, whose listings still on their
	// way are not this one's.
	a.sheet = projectSheet{open: true, space: space, host: h, want: dir, gen: a.sheet.gen + 1, sel: -1}
	a.wsOpen, a.wsColorOnly = false, false
	a.focusReq = nil
	if h != nil && h.current() == nil {
		a.connectHostNow(h)
	}
}

// browse lists dir next, "" for the home directory.
func (s *projectSheet) browse(dir string) {
	s.from = s.res.Path
	s.want, s.asked, s.failed = dir, false, ""
	s.sel, s.held = -1, false
	s.gen++
}

// step moves the highlighted folder row by d, from the first or last row
// when none is.
func (s *projectSheet) step(d int) {
	n := len(s.rows())
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

// rows are the folders the list shows, none while it shows a message.
func (s *projectSheet) rows() []string {
	if s.loading || !s.asked || s.failed != "" || s.res.Code != "" {
		return nil
	}
	return s.res.Dirs
}

// openRow browses the highlighted folder, and tells whether there was one.
func (s *projectSheet) openRow() bool {
	rows := s.rows()
	if s.sel < 0 || s.sel >= len(rows) {
		return false
	}
	s.browse(childPath(s.res.Path, rows[s.sel], s.windows()))
	return true
}

// up browses the parent folder, if there is one.
func (s *projectSheet) up() {
	if p := s.res.Parent; p != "" && !s.loading {
		s.browse(p)
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
	gen, host, want, update := s.gen, s.host, s.want, a.updater()
	go func() {
		r, err := c.DirList(want, false)
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
	if r.Path != "" {
		s.path = r.Path
	}
	if r.Code != "" {
		return
	}
	// Up highlights the folder it came from.
	if i := slices.IndexFunc(r.Dirs, func(d string) bool { return childPath(r.Path, d, s.windows()) == s.from }); i >= 0 {
		s.sel, s.reveal = i, true
	}
	if base := folderName(r.Path); s.name == s.auto {
		s.name, s.auto = base, base
	} else {
		s.auto = base
	}
}

// windows tells whether the host browsed runs Windows, by its paths.
func (s *projectSheet) windows() bool {
	return winPath(s.res.Home) || winPath(s.res.Path)
}

// chosen is the folder the sheet would use, "" for none: the directory
// listed, which is not Windows' list of drives.
func (s *projectSheet) chosen() string {
	r := s.res
	if s.held || s.loading || s.failed != "" || r.Code != "" || r.Path == "" || r.Path == rex.DirTop && s.windows() {
		return ""
	}
	return r.Path
}

// ready tells whether Create, or Use This Folder, can go: a folder is
// chosen and the project has a name, typed or the folder's. A drive's root
// has no name of its own.
func (s *projectSheet) ready() bool {
	dir := s.chosen()
	return dir != "" && (s.space != "" || strings.TrimSpace(s.name) != "" || folderName(dir) != "")
}

// crumb is a step of the breadcrumb: its name and its path.
type crumb struct{ name, path string }

// crumbs are the steps of path from the top of its machine, for the
// breadcrumb: on Windows the list of drives, then the drive.
func crumbs(path string, windows bool) []crumb {
	if !windows {
		out := []crumb{{"/", "/"}}
		at := ""
		for _, part := range strings.Split(path, "/") {
			if part == "" {
				continue
			}
			at += "/" + part
			out = append(out, crumb{part, at})
		}
		return out
	}
	out := []crumb{{locales.ProjectSheetDrives(), rex.DirTop}}
	if path == rex.DirTop || path == "" {
		return out
	}
	vol := path
	switch {
	case strings.HasPrefix(path, `\\`):
		// A share: \\server\share is its root.
		parts := strings.SplitN(strings.TrimPrefix(path, `\\`), `\`, 3)
		vol = `\\` + strings.Join(parts[:min(len(parts), 2)], `\`)
	case len(path) >= 2 && path[1] == ':':
		vol = path[:2]
	}
	root := vol + `\`
	out = append(out, crumb{vol, root})
	at := root
	for _, part := range strings.Split(strings.TrimPrefix(path[len(vol):], `\`), `\`) {
		if part == "" {
			continue
		}
		if !strings.HasSuffix(at, `\`) {
			at += `\`
		}
		at += part
		out = append(out, crumb{part, at})
	}
	return out
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

// finishSheet makes the project, or gives the project its folder, and
// closes the sheet.
func (a *App) finishSheet() {
	s := a.sheet
	dir := s.chosen()
	if !s.open || !s.ready() {
		return
	}
	a.sheet.open = false
	if s.space == "" {
		a.createProject(s.host, dir, s.name)
		return
	}
	if a.st == nil {
		return
	}
	if i := slices.IndexFunc(a.st.workspaces, func(w *Workspace) bool { return w.ID == s.space }); i >= 0 {
		w := a.st.workspaces[i]
		a.setProjectHome(w, s.host, dir, func(ok bool) {
			// The note of its folder gone is answered.
			if ok && a.errNote != nil && a.errNote.run != nil && a.noteShows() {
				a.err, a.errMsg, a.errNote = "", nil, nil
			}
		})
	}
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
		s.browse("")
	}
	a.listSheet()
	title := locales.ProjectSheetTitle()
	var w *Workspace
	if s.space != "" && a.st != nil {
		if i := slices.IndexFunc(a.st.workspaces, func(w *Workspace) bool { return w.ID == s.space }); i >= 0 {
			w = a.st.workspaces[i]
		}
		if w == nil {
			s.open = false
			return
		}
		title = locales.ProjectSheetChangeTitle(w.Name)
		if w.Dir == "" {
			title = locales.ProjectSheetSetTitle(w.Name)
		}
	}
	was := s.open
	ui.DialogBase(c.Key("project-sheet"), &s.open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		winW, winH := c.Size()
		panel.Width(min(540, winW-24)).MaxHeight(winH - 24).Padding(20).Radius(16)
		sheetPanel(c, panel, k)
		panel.Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.06)).Role(ui.RoleDialog).Label(title)
		// What does not fit a short window scrolls; the buttons stay.
		ui.Scroll(c.Key("sheet-body")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(k.text).Margin(0, 0, 6, 0)
				if s.lost != "" {
					// The folder of a home host unknown: not this machine's.
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						glyphIcon(c, "circle-alert", 14, k.attention).Shrink(0)
						ui.Text(c, locales.ProjectSheetHostUnknown(s.lost)).FontSize(12.5).TextColor(k.textMuted).SingleLine().TruncateMiddle().MinWidth(0).Tooltip(s.lost)
					})
				}
				sheetLabel(c, k, locales.ProjectSheetHost())
				a.sheetHosts(c, k)
				sheetLabel(c, k, locales.ProjectSheetFolder())
				// The folders take what room the window leaves, so that the
				// buttons stay in sight in a short window.
				room := winH - 500
				if s.space == "" {
					room -= 70
				}
				a.sheetBrowser(c, k, min(max(room, 84), 220))
				if s.space == "" {
					sheetLabel(c, k, locales.ProjectSheetName())
					placeholder := s.auto
					if placeholder == "" {
						placeholder = locales.ProjectSheetNameHint()
					}
					in := textField(c.Key("project-name"), k, &s.name, fieldRegular).Label(locales.ProjectSheetName()).Placeholder(placeholder)
					if in.Submitted() {
						a.later(c, a.finishSheet)
					}
				}
			})
		})
		a.sheetChoice(c, k)
		ui.Row(c).Shrink(0).Gap(8).Justify(ui.End).Margin(10, 0, 0, 0).Children(func() {
			if ui.Button(c, locales.ProjectSheetCancel()).Clicked() {
				s.open = false
			}
			label := locales.ProjectSheetCreate()
			if s.space != "" {
				label = locales.ProjectSheetUse()
			}
			if ui.PrimaryButton(c, label).Disabled(!s.ready()).Clicked() {
				a.later(c, a.finishSheet)
			}
		})
		a.sheetKeys(panel)
	})
	if was && !s.open {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// sheetPanel gives the sheet's panel its face, as frostPanel does. Under
// the glass setting in a light window, MyGo's glass takes panelGlass over
// it, the tint the frost has without the setting: bare, it shows the panes
// behind it grey, and the sheet's grey text does not read on that.
func sheetPanel(c *ui.Context, panel ui.Element, k *colors) {
	if glassOn() && !c.Theme().Dark {
		panel.Border(1, k.panelBorder).Material(ui.SystemGlass{Fallback: paneGlass{face: k.panelGlass, flat: k.panel}})
		return
	}
	frostPanel(panel, k, 16)
}

// sheetLabel is the label over a part of the sheet.
func sheetLabel(c *ui.Context, k *colors, text string) {
	ui.Text(c, text).FontSize(12).FontWeight(600).TextColor(k.textMuted).Margin(4, 0, 0, 0)
}

// sheetChoice says, over the buttons, the folder Create or Use This Folder
// takes, or why it takes none.
func (a *App) sheetChoice(c *ui.Context, k *colors) {
	s := &a.sheet
	dir := s.chosen()
	glyph, text := "folder", ""
	switch {
	case dir != "" && !s.ready():
		glyph, text = "circle-alert", locales.ProjectSheetNameNeeded()
	case dir != "":
		text = locales.ProjectSheetChosen(dir)
	case s.held:
		glyph, text = "info", locales.ProjectSheetBrowseFirst()
	case s.loading || !s.asked:
	default:
		glyph, text = "info", locales.ProjectSheetNoChoice()
	}
	ui.Row(c.Key("sheet-choice")).Shrink(0).Height(20).Gap(6).AlignItems(ui.Center).Margin(12, 0, 0, 0).Children(func() {
		if text == "" {
			return
		}
		glyphIcon(c, glyph, 14, k.textMuted).Shrink(0)
		t := ui.Text(c, text).FontSize(12.5).TextColor(k.textMuted).SingleLine().TruncateMiddle().MinWidth(0)
		if dir != "" {
			t.Tooltip(dir)
		}
	})
}

// sheetKeys takes the sheet's keys: Up and Down highlight a folder, Enter
// opens it (in the list, sheetBrowser), Backspace and Alt+Up go to the
// parent folder. A text field with the focus takes Backspace first.
func (a *App) sheetKeys(panel ui.Element) {
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
			b := ui.Row(c.Key("sheet-host-"+hostKey(h))).Height(28).Padding(0, 10).Gap(6).Radius(14).AlignItems(ui.Center).
				Role(ui.RoleRadio).Checked(chosen).Label(name).Border(1, k.panelBorder).Focusable()
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
				ui.Text(c, name).FontSize(12.5).TextColor(k.text).SingleLine()
			})
		}
	})
}

// sheetBrowser is the folder browser: Home, Up, the breadcrumb and, for
// this machine, Choose…; the field that takes a path; and the folders in
// the directory, a click going into one, in a list listH high.
func (a *App) sheetBrowser(c *ui.Context, k *colors, listH float32) {
	s := &a.sheet
	r, win := s.res, s.windows()
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		home := iconButton(c, k, "house", locales.ProjectSheetHome(), 26, 15).Tooltip(locales.ProjectSheetHome()).Focusable().Shrink(0)
		if home.Clicked() {
			s.browse(r.Home)
		}
		up := iconButton(c, k, "arrow-up", locales.ProjectSheetUp(), 26, 15).Tooltip(locales.ProjectSheetUp()).Shrink(0)
		if r.Parent == "" {
			up.Disabled(true)
			dimmed(up, 0.4)
		} else {
			up.Focusable()
			if up.Clicked() {
				s.browse(r.Parent)
			}
		}
		a.sheetCrumbs(c, k)
		if s.host == nil && a.win != nil {
			if ui.Button(c, locales.ProjectSheetChoose()).Shrink(0).Clicked() {
				a.chooseFolder()
			}
		}
	})
	a.sheetPath(c, k)
	box := ui.Column(c.Key("folders")).Height(listH).Radius(10).Border(1, k.panelBorder).Clip().Focusable().Label(locales.ProjectSheetFolder())
	if s.focusList {
		box.Focus()
		s.focusList = false
	}
	if box.Shortcut(0, ui.KeyEnter) {
		s.openRow()
	}
	box.Children(func() {
		say := func(text string) {
			ui.Text(c, text).FontSize(12.5).TextColor(k.textMuted).Padding(12)
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
		case len(r.Dirs) == 0:
			say(locales.ProjectSheetEmpty())
			return
		}
		reveal := s.reveal
		s.reveal = false
		ui.Scroll(c.Key("folder-list")).Grow(1).MinHeight(0).Padding(4).Children(func() {
			ui.Column(c).Gap(1).Role(ui.RoleList).Label(locales.ProjectSheetFolder()).Children(func() {
				glyph := "folder"
				if win && r.Path == rex.DirTop {
					glyph = "hard-drive"
				}
				for i, name := range r.Dirs {
					sel := i == s.sel
					row := ui.Row(c.Key("dir-"+name)).Height(28).Padding(0, 8).Gap(8).Radius(concentric(10, 5)).AlignItems(ui.Center).
						Cursor(ui.CursorPointer).Role(ui.RoleListItem).Label(name).Selected(sel)
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
						s.browse(childPath(r.Path, name, win))
					}
					row.Children(func() {
						glyphIcon(c, glyph, 15, k.textMuted).Shrink(0)
						ui.Text(c, name).FontSize(13).TextColor(k.text).SingleLine().TruncateMiddle().MinWidth(0)
					})
				}
				if r.More {
					ui.Text(c, locales.ProjectSheetMore(rex.DirListMax)).FontSize(11.5).TextColor(k.textMuted).Padding(6, 8)
				}
			})
		})
	})
}

// sheetPath is the field that takes a path. Enter browses the path typed,
// or, with the path of the folder listed, opens the folder highlighted.
// Without the focus it shows the end of a path too long for it, the
// folder's name, where a text field shows the start.
func (a *App) sheetPath(c *ui.Context, k *colors) {
	s := &a.sheet
	ui.Box(c.Key("project-path-box")).Children(func() {
		in := textField(c.Key("project-path"), k, &s.path, fieldRegular).Placeholder(locales.ProjectSheetPath()).Label(locales.ProjectSheetPath())
		focused := in.Focused()
		if focused && !s.pathFocused {
			// The caret goes to the end, which the field then shows.
			n := utf8.RuneCountInString(s.path)
			in.SetTextSelection(n, n)
		}
		s.pathFocused = focused
		if in.Submitted() {
			switch p := strings.TrimSpace(s.path); {
			case p == s.res.Path && s.openRow():
			case p != "":
				s.browse(p)
			}
		}
		if focused || s.path == "" {
			return
		}
		in.TextColor(ui.Transparent)
		// Over the field, where its text is: its padding and border.
		ui.Row(c).Absolute().Left(9).Right(9).Top(0).Bottom(0).AlignItems(ui.Center).PassThrough().Children(func() {
			ui.Text(c, s.path).FontSize(13).TextColor(k.text).SingleLine().TruncateStart().MinWidth(0)
		})
	})
}

// crumbGap is the room between steps of the breadcrumb: a chevron and the
// gaps around it.
const crumbGap = 11 + 2

// sheetCrumbs is the breadcrumb of the folder listed. Where its steps do
// not all fit, it shows the top, the last steps that fit and, between,
// a menu of those left out (fitCrumbs).
func (a *App) sheetCrumbs(c *ui.Context, k *colors) {
	s := &a.sheet
	r, win := s.res, s.windows()
	row := ui.Row(c.Key("crumbs")).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Center).ClipX().Role(ui.RoleNavigation).Label(locales.ProjectSheetFolder())
	room := row.Bounds().W
	row.Children(func() {
		if r.Path == "" {
			return
		}
		glyphIcon(c, "folder-open", 15, k.textMuted).Shrink(0).Margin(0, 4, 0, 6)
		steps := crumbs(r.Path, win)
		widths := make([]float32, len(steps))
		for i, step := range steps {
			weight := 400
			if i == len(steps)-1 {
				weight = 600
			}
			w, _ := c.MeasureText(0, ui.Span{Text: step.name, Size: 12.5, Weight: weight})
			widths[i] = w + 10
		}
		if room <= 0 {
			// The first frame, laid out by none before it: the panel's
			// width less its padding, Home, Up and Choose….
			winW, _ := c.Size()
			room = min(540, winW-24) - 40 - 60 - 90
		}
		head := 0
		if win && len(steps) > 1 {
			head = 1 // the drive, not the list of drives
		}
		shown := fitCrumbs(widths, head, room-25-4, 26)
		for j, i := range shown {
			if j > 0 && !(i == 1 && !win && shown[j-1] == 0) {
				glyphIcon(c, "chevron-right", 11, k.textMuted).Shrink(0)
			}
			if i < 0 {
				hidden := hiddenCrumbs(shown, len(steps))
				more := ui.Row(c.Key("crumb-more")).Height(24).Width(26).Radius(6).Center().Shrink(0).Cursor(ui.CursorPointer).
					Role(ui.RoleButton).Label(locales.ProjectSheetMoreFolders()).Tooltip(locales.ProjectSheetMoreFolders()).Focusable()
				if more.Hovered() {
					more.Background(k.hover)
				}
				more.Menu(func(m *ui.Menu) {
					for _, h := range hidden {
						if m.Item(steps[h].name).Chosen() {
							s.browse(steps[h].path)
						}
					}
				})
				more.Children(func() { glyphIcon(c, "ellipsis", 14, k.textMuted) })
				continue
			}
			step, last := steps[i], i == len(steps)-1
			e := ui.Row(c.Key("crumb-"+step.path)).Height(24).Padding(0, 5).Radius(6).AlignItems(ui.Center).
				Role(ui.RoleButton).Label(step.path)
			if !last {
				e.Shrink(0).Cursor(ui.CursorPointer).Focusable()
				if e.Hovered() {
					e.Background(k.hover)
				}
				if e.Clicked() {
					s.browse(step.path)
				}
			} else {
				// Only a name too long for the row shortens, in its middle.
				e.Shrink(1).MinWidth(0)
			}
			e.Children(func() {
				t := ui.Text(c, step.name).FontSize(12.5).SingleLine().MinWidth(0)
				if last {
					t.FontWeight(600).TextColor(k.text).TruncateMiddle()
				} else {
					t.TextColor(k.textMuted)
				}
			})
		}
	})
}

// fitCrumbs picks the steps of a breadcrumb that fit room DIPs, of the
// widths given, each but the first after a chevron: all of them if they
// fit, else the step head (the top) and the last three steps, or two, or
// one, as fit, with -1, a menu more wide, for those left out. At worst
// the menu and the last step, which shortens as it must.
func fitCrumbs(widths []float32, head int, room, more float32) []int {
	n := len(widths)
	width := func(shown []int) float32 {
		w := float32(0)
		for j, i := range shown {
			if j > 0 {
				w += crumbGap
			}
			if i < 0 {
				w += more
			} else {
				w += widths[i]
			}
		}
		return w
	}
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	if width(all) <= room {
		return all
	}
	for tail := 3; tail >= 1; tail-- {
		from := n - tail
		if from <= head || from == head+1 && head == 0 {
			continue // nothing left out, or the top itself
		}
		var shown []int
		if from > head+1 {
			shown = []int{head, -1} // the menu where the steps left out are
		} else {
			shown = []int{-1, head} // only those above the top
		}
		for i := from; i < n; i++ {
			shown = append(shown, i)
		}
		if width(shown) <= room {
			return shown
		}
	}
	return []int{-1, n - 1}
}

// hiddenCrumbs are the steps of n that shown leaves out, top first.
func hiddenCrumbs(shown []int, n int) []int {
	var out []int
	for i := range n {
		if !slices.Contains(shown, i) {
			out = append(out, i)
		}
	}
	return out
}
