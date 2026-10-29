package main

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
)

// debugHook lets a script drive the app while GOREX_DEBUG names a
// directory: each line of its file "do" is run, against the window the
// lines name, by default the one focused last; "shot" captures the window
// into shot.png. It runs once for the app, from its first window, a.
func (st *store) debugHook(a *App) {
	if os.Getenv("GOREX_OPEN_SETTINGS") == "1" {
		a.openSettings()
	}
	dir := os.Getenv("GOREX_DEBUG")
	if dir == "" {
		return
	}
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	go func() {
		for range time.Tick(150 * time.Millisecond) {
			req := filepath.Join(dir, "do")
			b, err := os.ReadFile(req)
			if err != nil {
				continue
			}
			os.Remove(req)
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				st.debug(dir, strings.TrimSpace(line))
			}
		}
	}()
}

// debugMain runs a debug line's change on the main thread, and waits for
// it; tests, which have no main loop, run it at once.
var debugMain = mygo.RunOnMain

// debugState is what "state" writes: the windows in the order of their
// places, every workspace, and the app's process.
type debugState struct {
	PID      int            `json:"pid"`
	Windows  []debugWindow  `json:"windows"`
	Sessions []debugSession `json:"sessions"`
}

// debugWindow is a window as "state" describes it.
type debugWindow struct {
	Index     int      `json:"index"` // from 1, as "win" takes it
	StateKey  string   `json:"stateKey"`
	Session   debugRef `json:"session"`
	Tabs      []string `json:"tabs"`
	ActiveTab int      `json:"activeTab"` // from 1
	Focused   bool     `json:"focused"`   // focused last
	Minimized bool     `json:"minimized"`
}

// debugRef names a workspace.
type debugRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// debugSession is a workspace as "state" describes it.
type debugSession struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Tabs   int    `json:"tabs"`
	Window int    `json:"window"` // the index of the window that shows it, 0 for none
}

// debugPanes is what "panes" writes: every pane of the window.
type debugPanes struct {
	Panes []debugPane `json:"panes"`
}

// debugPane is a pane as "panes" describes it: the terminal's text and
// size, and the screen the session server's snapshot of it renders, so
// that a duplication in the scrollback can be told from a fault of the
// server.
type debugPane struct {
	Tab          int    `json:"tab"`                // from 1
	Pane         int    `json:"pane"`               // the pane's ID
	Session      string `json:"session,omitempty"`  // the id that state's sessions join with
	TabTitle     string `json:"tabTitle,omitempty"` // the tab's label, shared by its panes
	Cols         int    `json:"cols"`
	Rows         int    `json:"rows"`
	Text         string `json:"text"` // the terminal's Text()
	ScreenCols   int    `json:"screenCols,omitempty"`
	ScreenRows   int    `json:"screenRows,omitempty"`
	Screen       string `json:"screen,omitempty"` // the server's snapshot, rendered
	CaptureError string `json:"captureError,omitempty"`
}

// byPlace returns the windows in the order of their places.
func (st *store) byPlace() []*App {
	apps := slices.Clone(st.apps)
	slices.SortFunc(apps, func(x, y *App) int { return x.slot - y.slot })
	return apps
}

// state describes the windows and the workspaces. Call it on the main
// thread.
func (st *store) state() debugState {
	s := debugState{PID: os.Getpid(), Windows: []debugWindow{}, Sessions: []debugSession{}}
	shown := map[*Workspace]int{}
	for i, a := range st.byPlace() {
		w := debugWindow{Index: i + 1, StateKey: stateKey(a.slot), Tabs: []string{}, ActiveTab: a.active + 1, Focused: len(st.apps) > 0 && st.apps[0] == a}
		if a.ws != nil {
			w.Session = debugRef{a.ws.ID, a.ws.Name}
			shown[a.ws] = i + 1
		}
		for _, t := range a.tabs {
			name, detail := t.label()
			w.Tabs = append(w.Tabs, strings.TrimSpace(name+" "+detail))
		}
		if a.win != nil {
			w.Minimized = a.win.IsMinimized()
		}
		s.Windows = append(s.Windows, w)
	}
	for _, w := range st.workspaces {
		s.Sessions = append(s.Sessions, debugSession{ID: w.ID, Name: w.Name, Tabs: w.tabCount(), Window: shown[w]})
	}
	return s
}

// debug runs a line: the verbs of the windows and the workspaces here,
// the others in the window the lines target.
func (st *store) debug(dir, line string) {
	verb, arg, _ := strings.Cut(line, " ")
	var a *App
	debugMain(func() {
		apps := st.byPlace()
		switch {
		case st.debugWin == 0 && len(st.apps) > 0:
			a = st.apps[0]
		case st.debugWin > 0 && st.debugWin <= len(apps):
			a = apps[st.debugWin-1]
		}
	})
	switch verb {
	case "win": // N: the next lines target the Nth window, by place, from 1; 0, the one focused last
		n := atoi(arg)
		ok := true
		debugMain(func() { ok = n <= len(st.apps) })
		if !ok {
			log.Printf("gorex: debug: no window %d", n)
			return
		}
		st.debugWin = n
		return
	case "focus-win": // N: the Nth window counts as focused last, as if it had the focus
		debugMain(func() {
			if apps := st.byPlace(); atoi(arg) >= 1 && atoi(arg) <= len(apps) {
				st.raise(apps[atoi(arg)-1])
			} else {
				log.Printf("gorex: debug: no window %s", arg)
			}
		})
		return
	case "state": // FILE: the windows and the workspaces, as JSON, into FILE
		var b []byte
		debugMain(func() { b, _ = json.MarshalIndent(st.state(), "", "  ") })
		path := filepath.Join(dir, arg)
		if os.WriteFile(path+".tmp", b, 0o644) == nil {
			os.Rename(path+".tmp", path)
		}
		return
	}
	if a == nil {
		log.Printf("gorex: debug: no window for %q", line)
		return
	}
	switch verb {
	case "session": // NAME: the target window shows the workspace of that name
		debugMain(func() {
			for _, w := range st.workspaces {
				if w.Name == arg {
					a.showWorkspace(w)
					return
				}
			}
			log.Printf("gorex: debug: no session %s", arg)
		})
	case "rename-session": // NAME: names the target window's workspace
		debugMain(func() { a.renameWorkspace(arg) })
	case "confirm": // closes the workspace Close Session asks about
		debugMain(func() {
			if a.wsConfirm {
				a.wsConfirm = false
				a.closeWorkspace()
			}
		})
	case "cancel": // keeps it
		debugMain(func() { a.wsConfirm = false })
	default:
		a.debug(dir, line)
	}
	debugMain(func() {
		if a.win != nil {
			a.win.Update(func() {})
		}
	})
}

// panesDump describes every pane of the window, in tab order: the
// terminal's text and size, and the screen the session server sends for
// it, rendered. It reads the window's panes on the main thread, then
// waits for the server, and renders, off it: the capture round trip is
// never held on the UI thread.
func (a *App) panesDump() debugPanes {
	type paneRef struct {
		tab      int
		tabTitle string
		pane     *Pane
		term     *terminal.Terminal
	}
	var refs []paneRef
	debugMain(func() {
		for i, t := range a.tabs {
			name, detail := t.label()
			title := strings.TrimSpace(name + " " + detail)
			for _, p := range t.panes() {
				refs = append(refs, paneRef{i + 1, title, p, p.term})
			}
		}
	})
	d := debugPanes{Panes: []debugPane{}}
	for _, r := range refs {
		dp := debugPane{Tab: r.tab, Pane: r.pane.ID, Session: r.pane.SID, TabTitle: r.tabTitle}
		if r.term != nil {
			dp.Cols, dp.Rows = r.term.Size()
			dp.Text = r.term.Text()
		}
		switch {
		case r.pane.host != nil:
			dp.CaptureError = "remote session"
		case r.pane.SID == "":
			dp.CaptureError = "no session"
		case r.term == nil:
			dp.CaptureError = "no terminal"
		default:
			res, err := a.client.Capture(r.pane.SID, 0)
			if err != nil {
				dp.CaptureError = err.Error()
				break
			}
			dp.ScreenCols, dp.ScreenRows = res.Cols, res.Rows
			screen, err := renderScreen(res.Screen, res.Cols, res.Rows)
			if err != nil {
				dp.CaptureError = err.Error()
				break
			}
			dp.Screen = screen
		}
		d.Panes = append(d.Panes, dp)
	}
	return d
}

// renderScreen feeds a session's snapshot into a terminal of its size, and
// returns the text that terminal shows.
func renderScreen(snapshot []byte, cols, rows int) (string, error) {
	pr, _ := io.Pipe()
	v, err := terminal.New(terminal.Options{Conn: dumpConn{pr}, Scrollback: termOptions().Scrollback})
	if err != nil {
		return "", err
	}
	defer v.Close()
	v.Resize(cols, rows)
	v.Feed(snapshot)
	return v.Text(), nil
}

// dumpConn is the Conn of a terminal that only replays a snapshot: it
// reads from an empty pipe, and drops what the terminal answers. It is
// not nopConn, whose Read parks on select{} and whose Close is nil, so
// closing the terminal would leave that reader parked.
type dumpConn struct{ r *io.PipeReader }

func (c dumpConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c dumpConn) Write(p []byte) (int, error) { return len(p), nil }
func (c dumpConn) Close() error                { return c.r.Close() }

func (a *App) debug(dir, line string) {
	verb, arg, _ := strings.Cut(line, " ")
	switch verb {
	case "panes": // FILE: each pane's text and size, and the session server's snapshot, as JSON, into FILE
		name := "panes.json"
		if arg != "" {
			name = arg
		}
		b, _ := json.MarshalIndent(a.panesDump(), "", "  ")
		path := filepath.Join(dir, name)
		if os.WriteFile(path+".tmp", b, 0o644) == nil {
			os.Rename(path+".tmp", path)
		}
		return
	}
	win := a.win
	if win == nil {
		return
	}
	switch verb {
	case "section": // a section of the settings window
		if w := settingsUI.win; w != nil {
			w.Update(func() { a.showSection(arg) })
		}
	case "remote": // allow NAME KEY, enable, disable, share on|off, regenerate, reveal, confirm: the Remote page's calls
		op, rest, _ := strings.Cut(arg, " ")
		switch op {
		case "share":
			a.client.ShareSessions(rest == "on")
		case "allow":
			name, key, _ := strings.Cut(rest, " ")
			a.client.AllowRemote(name, key)
		case "enable":
			a.client.EnableRemote()
		case "disable":
			a.client.DisableRemote()
		case "regenerate":
			a.client.RegenerateRemote()
		case "reveal", "confirm":
			if w := settingsUI.win; w != nil {
				w.Update(func() {
					remoteUI.reveal, remoteUI.confirm = op == "reveal", op == "confirm"
					remoteUI.st.Address = ""
					a.fetchRemote()
				})
			}
		}
	case "hosts": // add NAME ADDRESS [PAIR-AS], retry NAME, remove NAME: the Remote Hosts page's
		op, rest, _ := strings.Cut(arg, " ")
		win.Update(func() {
			if op == "add" {
				a.openSettingsAt("hosts")
				a.openAddHost()
				f := strings.Fields(rest)
				if len(f) > 0 {
					hostsUI.name = f[0]
				}
				if len(f) > 1 {
					hostsUI.address = f[1]
				}
				if len(f) > 2 {
					hostsUI.pairAs = strings.Join(f[2:], " ")
				}
				a.submitHost()
				return
			}
			for _, h := range a.hosts {
				if h.Name != rest {
					continue
				}
				if op == "retry" {
					h.poke()
				} else if op == "remove" {
					a.removeHost(h)
				}
				break
			}
		})
	case "newtab-on": // NAME, or "local": a new tab on that host
		win.Update(func() {
			var on *hostConn
			for _, h := range a.hosts {
				if h.Name == arg {
					on = h
				}
			}
			if on != nil || arg == "local" {
				a.newTabOn(on, a.dirOn(on))
			}
		})
	case "host": // opens or closes the host chip's panel
		win.Update(func() { a.hostOpen = !a.hostOpen })
	case "tabs": // above, left or right: where the tabs go
		win.Update(func() {
			setTabPlacement(&prefs, map[string]string{"left": "left", "right": "right"}[arg])
			a.syncSidebarItem()
		})
	case "headers": // show or hide: the pane headers beside a left sidebar
		win.Update(func() { prefs.HidePaneHeaders = arg == "hide" })
	case "appearance": // system, light or dark
		a.setAppearance(map[string]string{"light": "light", "dark": "dark"}[arg])
	case "sshot": // the settings window, into the file named by arg
		time.Sleep(300 * time.Millisecond)
		if w := settingsUI.win; w != nil {
			if png, err := w.CapturePage(); err == nil {
				os.WriteFile(filepath.Join(dir, arg), png, 0o644)
			}
		}
	case "shot":
		time.Sleep(100 * time.Millisecond)
		if png, err := win.CapturePage(); err == nil {
			name := "shot.png"
			if arg != "" {
				name = arg
			}
			os.WriteFile(filepath.Join(dir, name), png, 0o644)
		}
	case "sleep":
		d, _ := time.ParseDuration(arg)
		time.Sleep(d)
	case "type":
		arg = strings.ReplaceAll(arg, `\r`, "\r")
		arg = strings.ReplaceAll(arg, `\e`, "\x1b")
		win.Update(func() {
			if t := a.tab(); t != nil && t.Focus != nil && t.Focus.term != nil {
				t.Focus.term.Send([]byte(arg))
			}
		})
	case "size":
		var w, h int
		if _, err := fmtSscan(arg, &w, &h); err == nil {
			win.SetSize(w, h)
		}
	default:
		for _, cmd := range commands {
			if strings.EqualFold(strings.ReplaceAll(cmd.English(), " ", "-"), verb) {
				a.run(cmd)
			}
		}
		switch verb {
		case "tab":
			win.Update(func() { a.selectTab(atoi(arg) - 1) })
		case "focus":
			win.Update(func() {
				if t := a.tab(); t != nil {
					if ps := t.panes(); atoi(arg)-1 < len(ps) {
						t.setFocus(ps[atoi(arg)-1])
						a.focusReq = t.Focus
					}
				}
			})
		case "name":
			win.Update(func() {
				if t := a.tab(); t != nil {
					t.Name = arg
				}
			})
		case "close":
			win.Close()
		case "zoom":
			win.Update(func() {
				if win.IsMaximized() {
					win.Unmaximize()
				} else {
					win.Maximize()
				}
			})
		}
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

func fmtSscan(s string, w, h *int) (int, error) {
	a, b, _ := strings.Cut(s, " ")
	*w, *h = atoi(a), atoi(b)
	return 2, nil
}
