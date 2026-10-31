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
	"bufio"
	"log"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"

	"gorex/internal/cli"
	"gorex/internal/control"
	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/locales"
)

func main() {
	// Rex's API shares the session server's socket, so that Rex.app and
	// the rex CLI can use GoRex as they use Rex (internal/rexapi).
	rex.SetHTTPServe(serveRexAPI)
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
	ownDir := os.Getenv("GOREX_DIR") != ""
	if !ownDir {
		if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
			os.Setenv("GOREX_DIR", dir)
		}
	}
	// control bridges rootshell to the server the app runs, in the same
	// directory (internal/control).
	if len(os.Args) > 1 && os.Args[1] == "control" {
		os.Exit(control.Main())
	}
	// The command line shares the binary: it runs before the app, and
	// leaves the app's own arguments, as AppKit's, alone.
	cli.GhosttyWarnings = ghosttyWarnings
	if code, handled := cli.Main(os.Args[1:], os.Stdout, os.Stderr, mygo.App.Version()); handled {
		os.Exit(code)
	}
	// The app runs once: launched again, it shows the window focused last.
	// MyGo keys the lock by the app's identifier and name, which mygo dev's
	// GoRex Dev has of its own; an app given a GOREX_DIR of its own, as
	// tests and trials are, has other sessions, and runs beside it.
	if !ownDir && !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.Use(native.Plugin)
	registerFonts()
	loadSettings()
	st := &store{}
	mygo.App.SetMenu(st.menu())
	mygo.App.OnLocaleChanged(st.localeChanged)
	// The menu is made again once the app knows its language, which the
	// start of the app may settle after the menu above was made.
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(st.menu())
		st.start()
	})
	mygo.App.OnSecondInstance(func([]string, string) { st.secondInstance() })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && len(st.apps) == 0 {
			st.reopen()
		}
	})
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" {
			mygo.App.Quit()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		st.quitting = true
		st.saveNow()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// localeChanged says again what was set once: the menus, and the
// settings window's title. What the views say follows the language as they
// draw.
func (st *store) localeChanged(string) {
	mygo.App.SetMenu(st.menu())
	if w := settingsUI.win; w != nil {
		w.SetTitle(locales.SettingsTitle())
	}
}

// saveNow sends the layout to the server and waits for it to be kept.
func (a *App) saveNow() {
	a.saveDue = false
	if a.st != nil {
		a.st.saveNow()
	}
}

// staleServer reports whether the server runs another build of the app
// than this one, as after an update or after mygo dev rebuilt it.
func staleServer(h rex.Hello) bool {
	exe, at := rex.Executable()
	return h.Exe != exe || !h.ExeTime.Equal(at)
}

// serveRexAPI serves a Rex client's connection, starting Rex's API over
// this machine's server the first time one comes.
func serveRexAPI(conn net.Conn, r *bufio.Reader, peer string) {
	if peer != "" {
		// A remote connection has no Rex API: it has no notion of the
		// remote permissions the server enforces, and would act as the
		// local user.
		conn.Close()
		return
	}
	rexAPIMu.Lock()
	if rexAPIServer == nil {
		s, err := rexapi.New(rexapi.Options{
			Socket:  rex.SocketPath(),
			Version: version(),
			Commit:  commit(),
		})
		if err != nil {
			log.Printf("gorex: serving Rex's API: %v", err)
		} else {
			rexAPIServer = s
		}
	}
	server := rexAPIServer
	rexAPIMu.Unlock()
	if server == nil {
		conn.Close()
		return
	}
	rexapi.ServeConn(conn, r, server.Handler())
}

var (
	rexAPIMu     sync.Mutex
	rexAPIServer *rexapi.Server
)

// version is GoRex's version, as server.version reports it.
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return "gorex " + info.Main.Version
	}
	return "gorex"
}

// commit is the revision GoRex was built from, when the build knows it.
func commit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return ""
}
