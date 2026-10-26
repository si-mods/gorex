package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// debugHook lets a script drive the app while GOREX_DEBUG names a
// directory: each line of its file "do" is run, and "shot" captures the
// window into shot.png.
func (a *App) debugHook() {
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
				a.debug(dir, strings.TrimSpace(line))
			}
		}
	}()
}

func (a *App) debug(dir, line string) {
	verb, arg, _ := strings.Cut(line, " ")
	win := a.win
	if win == nil {
		return
	}
	switch verb {
	case "section": // a section of the settings window
		if w := settingsUI.win; w != nil {
			w.Update(func() { a.showSection(arg) })
		}
	case "remote": // allow NAME KEY, enable, disable, regenerate, reveal, confirm: the Remote page's calls
		op, rest, _ := strings.Cut(arg, " ")
		switch op {
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
