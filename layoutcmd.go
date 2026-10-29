package main

import (
	"cmp"
	"encoding/json"
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
// a take waits on the server for the next, so each comes at once. Each is
// answered once the server has its layout, the main thread going on
// meanwhile.
func (st *store) takeCommands(client *rex.Client, update func(func())) {
	unavailable := func(id int64, msg string) rex.LayoutCommand {
		return rex.LayoutCommand{ID: id, Code: "desktop_unavailable", Error: msg}
	}
	for {
		cmd, err := client.TakeCommand()
		select {
		case <-client.Closed():
			return
		default:
		}
		if err != nil && strings.Contains(err.Error(), "unknown op") {
			log.Printf("the server takes no layout commands: %v", err)
			return
		}
		if err != nil {
			log.Printf("taking layout commands: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if cmd == nil {
			continue
		}
		started, done := make(chan struct{}), make(chan rex.LayoutCommand, 1)
		update(func() {
			close(started)
			a := st.commandApp()
			if a == nil {
				done <- unavailable(cmd.ID, "no GoRex window is open")
				return
			}
			res := a.command(*cmd)
			if res.Code == "" {
				a.saveDue = false
				st.save()
			}
			done <- res
		})
		// A main thread that runs nothing has no window; one that runs it
		// takes its time, as a session may be slow to start.
		var res rex.LayoutCommand
		select {
		case <-started:
			res = <-done
		case <-time.After(rex.AppCommandTimeout):
			res = unavailable(cmd.ID, "the GoRex window does not answer")
		}
		if res.Code == "" {
			st.writer.wait(rex.CommandTimeout)
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
// window's buttons do, on this machine's panes; the caller saves the
// layout at once, for the client to see it. The active tab and the
// keyboard's focus stay.
func (a *App) command(c rex.LayoutCommand) rex.LayoutCommand {
	res := rex.LayoutCommand{ID: c.ID}
	fail := func(code, msg string) rex.LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	if c.Op == "action" {
		switch c.Action {
		case "app.quit", "app.updates", "config.reload", "session.close", "tab.close", "pane.close", "pane.restart",
			"screen.clear", "scrollback.clear", "tab.move_window":
			return fail("not_allowed", "GoRex does not run "+c.Action+" from a client")
		}
		if c.SID != "" {
			p := a.localPane(c.SID)
			if p == nil {
				return fail("pane_not_found", "no such pane in the GoRex window")
			}
			if i := slices.Index(a.tabs, p.Tab); i >= 0 {
				a.selectTab(i)
			}
			p.Tab.setFocus(p)
			a.focusReq = p
		}
		cmd := lookupCommand(c.Action)
		if cmd == nil {
			return fail("unknown_action", "GoRex does not know "+c.Action)
		}
		var args map[string]any
		if len(c.Args) > 0 {
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return fail("bad_args", err.Error())
			}
		}
		if err := cmd.invoke(a, args); err != nil {
			return fail("action_failed", err.Error())
		}
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
		a.err = ""
		t := a.newTabAfter(nil, dir, after)
		if t == nil {
			return fail("create_failed", "GoRex cannot open a tab now")
		}
		if t.Focus.SID == "" {
			a.dropPane(t.Focus, false) // and its tab
			return fail("create_failed", cmp.Or(a.err, "the session did not start"))
		}
		res.New = t.Focus.SID
	case "pane.split":
		dir := c.Dir
		if dir == "" {
			dir = p.info.Dir
		}
		a.err = ""
		n := a.splitPane(p, c.Vertical, dir)
		if n == nil {
			return fail("create_failed", "GoRex cannot split the pane now")
		}
		if n.SID == "" {
			a.dropPane(n, false)
			return fail("create_failed", cmp.Or(a.err, "the session did not start"))
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
		// Index is the place among this machine's tabs as they are now,
		// counted before the tab leaves its own.
		t := p.Tab
		local := a.localTabs()
		at := min(max(c.Index, 0), len(local))
		if slices.Index(local, t) < at {
			at--
		}
		local = slices.DeleteFunc(local, func(x *Tab) bool { return x == t })
		rest := slices.DeleteFunc(slices.Clone(a.tabs), func(x *Tab) bool { return x == t })
		to := slices.IndexFunc(rest, func(x *Tab) bool { return x.host() == nil })
		if at > 0 {
			to = slices.Index(rest, local[at-1]) + 1
		}
		a.moveTab(t, max(to, 0))
		res.Index = slices.Index(a.localTabs(), t)
	default:
		return fail("unknown_op", "GoRex does not know "+c.Op)
	}
	return res
}

// localTabs returns the tabs with a pane of this machine, in order: those
// the server's other clients see.
func (a *App) localTabs() []*Tab {
	var tabs []*Tab
	for _, t := range a.tabs {
		if slices.ContainsFunc(t.panes(), func(p *Pane) bool { return p.host == nil && p.SID != "" && !p.closed }) {
			tabs = append(tabs, t)
		}
	}
	return tabs
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
