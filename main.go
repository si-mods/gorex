// GoRex is a terminal of tabs and split panes, after Superlogical's Rex,
// in MyGo's native UI. Its shells run in a session server of their own,
// so that they outlive the app: quit it, open it again, and every tab and
// pane is back as it was, its programs still running.
//
//	go tool mygo dev    the app, rebuilt as the code changes
//	go run . -server    the session server alone, in the foreground
//	go run . control    the bridge rootshell drives as herdr (internal/control)
package main

import (
	"log"
	"os"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"gorex/internal/control"
	"gorex/internal/rex"
	"gorex/locales"
)

func main() {
	// -server runs the session server (rex.ServerMain); other arguments
	// are AppKit's, as -NSWindowResizeTime, which it reads itself.
	if server, err := rex.ServerMain(os.Args[1:]); server {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	// The state of the sessions lives in the app's data directory, which
	// the server it starts takes from GOREX_DIR: "GoRex", or "GoRex Dev"
	// under mygo dev, which keeps the installed app's sessions apart.
	if os.Getenv("GOREX_DIR") == "" {
		if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
			os.Setenv("GOREX_DIR", dir)
		}
	}
	// control bridges rootshell to the server the app runs, in the same
	// directory (internal/control).
	if len(os.Args) > 1 && os.Args[1] == "control" {
		os.Exit(control.Main())
	}
	mygo.Use(native.Plugin)
	registerFonts()
	loadSettings()
	a := &App{}
	mygo.App.SetMenu(a.menu())
	mygo.App.OnLocaleChanged(a.localeChanged)
	// The menu is made again once the app knows its language, which the
	// start of the app may settle after the menu above was made.
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(a.menu())
		a.open()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && a.win == nil {
			a.open()
		}
	})
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" {
			mygo.App.Quit()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.saveNow()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// open opens the window, with the tabs the server kept.
func (a *App) open() {
	client, err := rex.Connect()
	if err != nil {
		log.Print(err)
		mygo.Dialog.Error(locales.ErrorServerStart(), err.Error())
		mygo.App.Quit()
		return
	}
	a.client = client
	a.hello, err = client.Hello()
	if err == nil && staleServer(a.hello) {
		// A server a build before this one started runs that build's
		// code: it runs this build's in its place, keeping its sessions.
		// Only for a newer build: two copies of the app would take the
		// server from each other at every launch. One that cannot upgrade
		// is replaced under mygo dev, ending its sessions, and kept
		// otherwise.
		var next *rex.Client
		if a.hello.Upgrade {
			exe, at := rex.Executable()
			if at.After(a.hello.ExeTime) {
				if next, err = client.Upgrade(exe); err != nil {
					log.Print(err)
					client.Close()
					next, err = rex.Connect()
				}
			}
		} else if mygo.IsDev() {
			next, err = client.Restart(a.hello.PID)
		}
		if err != nil {
			log.Print(err)
			mygo.Dialog.Error(locales.ErrorServerRestart(), err.Error())
			mygo.App.Quit()
			return
		}
		if next != nil {
			client, a.client = next, next
			a.hello, err = client.Hello()
		}
	}
	if err == nil && a.hello.Version != rex.ProtocolVersion {
		a.errMsg = locales.Messages.ErrorServerVersion
	}
	a.reset()
	opts := mygo.WindowOptions{
		Title:                "GoRex",
		Width:                1000,
		Height:               620,
		MinWidth:             560,
		MinHeight:            340,
		StateKey:             "main",
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 15},
		BackgroundColor:      "light-dark(#efe1e6, #231e27)",
		Content:              ui.View(a.view),
	}
	if ownControls {
		// The title bar draws the window's controls (windowControls); the
		// hidden title bar keeps the window's top edge, which resizes it.
		opts.HideWindowButtons = true
	}
	win := mygo.NewWindow(opts)
	a.win = win
	a.loadHosts()
	if !a.restore() {
		home, _ := os.UserHomeDir()
		a.newTab(home)
	}
	a.changed()
	go a.poll(win, client)
	go a.takeCommands(win, client)
	a.debugHook()
	win.OnClosed(func() {
		a.saveNow()
		a.stopHosts()
		a.quitting = true
		for _, t := range a.tabs {
			for _, p := range t.panes() {
				p.closed = true
				if p.term != nil {
					p.term.Close() // detaches: the session goes on
				}
			}
		}
		a.client.Close()
		a.win = nil
	})
}

// localeChanged says again what was set once: the menus, and the
// settings window's title. What the views say follows the language as they
// draw.
func (a *App) localeChanged(string) {
	mygo.App.SetMenu(a.menu())
	if w := settingsUI.win; w != nil {
		w.SetTitle(locales.SettingsTitle())
	}
}

// saveNow sends the layout to the server and waits for it to be kept.
func (a *App) saveNow() {
	if a.client == nil || len(a.tabs) == 0 && !a.quitting {
		return
	}
	b, seq := a.layoutToSave()
	c, done := a.client, make(chan struct{})
	go func() {
		a.sendLayout(c, b, seq)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

// reset forgets the state of a window that closed, keeping the server's
// connection, for the next window.
func (a *App) reset() {
	a.tabs, a.active, a.focusReq, a.errMsg = nil, 0, nil, nil
	a.paletteOpen, a.hostOpen, a.renaming = false, false, nil
	a.saveDue, a.quitting, a.lastSnapshot, a.title = false, false, "", ""
}

// staleServer reports whether the server runs another build of the app
// than this one, as after an update or after mygo dev rebuilt it.
func staleServer(h rex.Hello) bool {
	exe, at := rex.Executable()
	return h.Exe != exe || !h.ExeTime.Equal(at)
}
