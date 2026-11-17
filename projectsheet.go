package main

import (
	"slices"
	"strings"

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
}

// openSheet opens the sheet: of project space ("" for New Project), on
// host h, nil for this machine, in dir, "" for the home directory.
func (a *App) openSheet(space string, h *hostConn, dir string) {
	// gen goes on from the sheet before, whose listings still on their
	// way are not this one's.
	a.sheet = projectSheet{open: true, space: space, host: h, want: dir, gen: a.sheet.gen + 1}
	a.wsOpen, a.wsColorOnly = false, false
	a.focusReq = nil
	if h != nil && h.current() == nil {
		a.connectTailnetHost(h)
	}
}

// browse lists dir next, "" for the home directory.
func (s *projectSheet) browse(dir string) {
	s.want, s.asked, s.failed = dir, false, ""
	s.gen++
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
// machine: a host that is connected, or one of the tailnet that is online
// and connects as it is picked.
func pickable(h *hostConn) bool {
	return h == nil || h.current() != nil || rex.IsTailnetTarget(h.Address) && h.tailnetOnline
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
		a.connectTailnetHost(h)
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
	if s.loading || s.failed != "" || r.Code != "" || r.Path == "" || r.Path == rex.DirTop && s.windows() {
		return ""
	}
	return r.Path
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

// folderName is the last name of path, the project's name by default.
func folderName(path string) string {
	t := strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(t, `/\`); i >= 0 {
		t = t[i+1:]
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
	if dir == "" {
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
	}
	was := s.open
	ui.DialogBase(c.Key("project-sheet"), &s.open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		winW, winH := c.Size()
		panel.Width(min(540, winW-24)).MaxHeight(winH - 24).Padding(20).Radius(16)
		frostPanel(panel, k, 16)
		panel.Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.06)).Role(ui.RoleDialog).Label(title)
		// What does not fit a short window scrolls; the buttons stay.
		ui.Scroll(c.Key("sheet-body")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(k.text).Margin(0, 0, 6, 0)
				sheetLabel(c, k, locales.ProjectSheetHost())
				a.sheetHosts(c, k)
				sheetLabel(c, k, locales.ProjectSheetFolder())
				// The folders take what room the window leaves, so that the
				// buttons stay in sight in a short window.
				room := winH - 470
				if s.space == "" {
					room -= 70
				}
				a.sheetBrowser(c, k, min(max(room, 84), 220))
				if s.space == "" {
					sheetLabel(c, k, locales.ProjectSheetName())
					textField(c.Key("project-name"), k, &s.name, fieldRegular).Label(locales.ProjectSheetName()).Placeholder(s.auto)
				}
			})
		})
		ui.Row(c).Shrink(0).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
			if ui.Button(c, locales.ProjectSheetCancel()).Clicked() {
				s.open = false
			}
			label := locales.ProjectSheetCreate()
			if s.space != "" {
				label = locales.ProjectSheetUse()
			}
			if ui.PrimaryButton(c, label).Disabled(s.chosen() == "").Clicked() {
				a.later(c, a.finishSheet)
			}
		})
	})
	if was && !s.open {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// sheetLabel is the label over a part of the sheet.
func sheetLabel(c *ui.Context, k *colors, text string) {
	ui.Text(c, text).FontSize(12).FontWeight(600).TextColor(k.textFaint).Margin(4, 0, 0, 0)
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
				Role(ui.RoleRadio).Checked(chosen).Label(name).Border(1, k.panelBorder)
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
		if iconButton(c, k, "house", locales.ProjectSheetHome(), 26, 15).Tooltip(locales.ProjectSheetHome()).Clicked() {
			s.browse(r.Home)
		}
		up := iconButton(c, k, "arrow-up", locales.ProjectSheetUp(), 26, 15).Tooltip(locales.ProjectSheetUp())
		if r.Parent == "" {
			up.Disabled(true)
			dimmed(up, 0.4)
		} else if up.Clicked() {
			s.browse(r.Parent)
		}
		ui.Row(c.Key("crumbs")).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Center).ClipX().Role(ui.RoleNavigation).Label(locales.ProjectSheetFolder()).Children(func() {
			if r.Path == "" {
				return
			}
			glyphIcon(c, "folder-open", 15, k.textMuted).Shrink(0).Margin(0, 4, 0, 6)
			steps := crumbs(r.Path, win)
			for i, step := range steps {
				if i > 0 && !(i == 1 && !win) {
					glyphIcon(c, "chevron-right", 11, k.textFaint).Shrink(0)
				}
				last := i == len(steps)-1
				e := ui.Row(c.Key("crumb-"+step.path)).Height(24).Padding(0, 5).Radius(6).AlignItems(ui.Center).MinWidth(0).
					Role(ui.RoleButton).Label(step.path)
				if !last {
					e.Cursor(ui.CursorPointer)
					if e.Hovered() {
						e.Background(k.hover)
					}
					if e.Clicked() {
						s.browse(step.path)
					}
				} else {
					e.Shrink(0)
				}
				e.Children(func() {
					t := ui.Text(c, step.name).FontSize(12.5).SingleLine().Ellipsis("…").MinWidth(0)
					if last {
						t.FontWeight(600).TextColor(k.text)
					} else {
						t.TextColor(k.textMuted)
					}
				})
			}
		})
		if s.host == nil && a.win != nil {
			if ui.Button(c, locales.ProjectSheetChoose()).Clicked() {
				a.chooseFolder()
			}
		}
	})
	in := textField(c.Key("project-path"), k, &s.path, fieldRegular).Placeholder(locales.ProjectSheetPath()).Label(locales.ProjectSheetPath())
	if in.Submitted() {
		if p := strings.TrimSpace(s.path); p != "" {
			s.browse(p)
		}
	}
	box := ui.Column(c.Key("folders")).Height(listH).Radius(10).Border(1, k.panelBorder).Clip()
	box.Children(func() {
		say := func(text string) {
			ui.Text(c, text).FontSize(12.5).TextColor(k.textFaint).Padding(12)
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
		ui.Scroll(c.Key("folder-list")).Grow(1).MinHeight(0).Padding(4).Children(func() {
			ui.Column(c).Gap(1).Role(ui.RoleList).Label(locales.ProjectSheetFolder()).Children(func() {
				glyph := "folder"
				if win && r.Path == rex.DirTop {
					glyph = "hard-drive"
				}
				for _, name := range r.Dirs {
					row := ui.Row(c.Key("dir-"+name)).Height(28).Padding(0, 8).Gap(8).Radius(concentric(10, 5)).AlignItems(ui.Center).
						Cursor(ui.CursorPointer).Role(ui.RoleListItem).Label(name)
					if row.Hovered() {
						row.Background(k.hover)
					}
					if row.Clicked() {
						s.browse(childPath(r.Path, name, win))
					}
					row.Children(func() {
						glyphIcon(c, glyph, 15, k.textMuted).Shrink(0)
						ui.Text(c, name).FontSize(13).TextColor(k.text).SingleLine().Ellipsis("…").MinWidth(0)
					})
				}
				if r.More {
					ui.Text(c, locales.ProjectSheetMore(rex.DirListMax)).FontSize(11.5).TextColor(k.textFaint).Padding(6, 8)
				}
			})
		})
	})
}
