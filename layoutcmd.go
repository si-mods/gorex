package main

import (
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"gorex/internal/rex"
)

// takeCommands makes the changes of the layout that other clients of this
// machine's server ask for (rex.LayoutCommand), as rootshell's bridge does,
// one at a time, on the main thread through update, in the window whose
// tabs the server's layout shows first (commandApp), until client closes:
// a take waits on the server for the next, so each comes at once.
func (st *store) takeCommands(client *rex.Client, update func(func())) {
	for {
		cmd, err := client.TakeCommand()
		select {
		case <-client.Closed():
			return
		default:
		}
		if err != nil {
			log.Printf("taking layout commands: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if cmd == nil {
			continue
		}
		done := make(chan rex.LayoutCommand, 1)
		update(func() {
			a := st.commandApp()
			if a == nil {
				done <- rex.LayoutCommand{ID: cmd.ID, Code: "desktop_unavailable", Error: "no GoRex window is open"}
				return
			}
			done <- a.command(*cmd)
		})
		var res rex.LayoutCommand
		select {
		case res = <-done:
		case <-time.After(5 * time.Second): // the window went
			res = rex.LayoutCommand{ID: cmd.ID, Code: "desktop_unavailable", Error: "the GoRex window closed"}
		}
		if err := client.CommandDone(res); err != nil {
			log.Printf("answering a layout command: %v", err)
		}
	}
}

// commandApp is the window of the first workspace of the server's layout,
// whose tabs it keeps for clients of one window, as rootshell's bridge:
// that of the first place; nil without windows.
func (st *store) commandApp() *App {
	var first *App
	for _, a := range st.apps {
		if a.ws != nil && (first == nil || a.slot < first.slot) {
			first = a
		}
	}
	return first
}

// command makes a change of the layout another client asks for, as the
// window's buttons do, on this machine's panes, and saves the layout at
// once, for the client to see it. The active tab and the keyboard's focus
// stay.
func (a *App) command(c rex.LayoutCommand) rex.LayoutCommand {
	res := rex.LayoutCommand{ID: c.ID}
	fail := func(code, msg string) rex.LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	p := a.localPane(c.SID)
	if p == nil && c.Op != "tab.create" {
		if strings.HasPrefix(c.Op, "tab.") {
			return fail("tab_not_found", "no such tab in the GoRex window")
		}
		return fail("pane_not_found", "no such pane in the GoRex window")
	}
	// The window closes with its last tab, which only the desktop closes.
	last := len(a.tabs) == 1 && (c.Op == "tab.close" || c.Op == "pane.close" && len(p.Tab.panes()) == 1)
	if last {
		return fail("last_tab", "close GoRex's last tab on the desktop")
	}
	switch c.Op {
	case "tab.create":
		var after *Tab
		dir := c.Dir
		if q := a.localPane(c.After); q != nil {
			after = q.Tab
			if dir == "" {
				dir = q.info.Dir
			}
		}
		if dir == "" {
			dir, _ = os.UserHomeDir()
		}
		t := a.newTabAfter(nil, dir, after)
		if t == nil || t.Focus.SID == "" {
			return fail("create_failed", a.err)
		}
		res.New = t.Focus.SID
	case "pane.split":
		dir := c.Dir
		if dir == "" {
			dir = p.info.Dir
		}
		n := a.splitPane(p, c.Vertical, dir)
		if n == nil || n.SID == "" {
			return fail("create_failed", a.err)
		}
		res.New = n.SID
	case "pane.close":
		a.closePane(p)
	case "tab.close":
		a.closeTab(p.Tab)
	case "tab.rename":
		p.Tab.Name = strings.TrimSpace(c.Name)
		a.changed()
	case "pane.zoom":
		on := c.Mode == "on" || c.Mode != "off" && p.Tab.Zoom == nil
		a.zoom(p, on)
		res.Zoomed = p.Tab.Zoom != nil
	case "tab.move":
		t := p.Tab
		rest := slices.DeleteFunc(slices.Clone(a.tabs), func(x *Tab) bool { return x == t })
		to := slices.IndexFunc(rest, func(x *Tab) bool { return x.host() == nil })
		if q := a.localPane(c.After); q != nil && q.Tab != t {
			to = slices.Index(rest, q.Tab) + 1
		}
		a.moveTab(t, max(to, 0))
	default:
		return fail("unknown_op", "GoRex does not know "+c.Op)
	}
	a.saveNow()
	return res
}

// localPane returns the pane of this machine's session sid, nil for none.
func (a *App) localPane(sid string) *Pane {
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			if p.host == nil && sid != "" && p.SID == sid && !p.closed {
				return p
			}
		}
	}
	return nil
}
