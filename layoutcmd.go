package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// takeCommands runs what other clients of this machine's server ask of
// the window (rex.LayoutCommand): an action, config.reload or a teleport,
// one at a time, on the main thread through update, in the window whose
// tabs the server's layout shows first (commandApp), until client closes:
// a take waits on the server for the next, so each comes at once. The
// server makes every change of the layout itself.
func (st *store) takeCommands(client *rex.Client, update func(func())) {
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
		started, done := make(chan struct{}), make(chan *rex.LayoutCommand, 1)
		update(func() {
			close(started)
			a := st.commandAppFor(cmd.Space)
			if a == nil {
				// The server makes it itself, before the main thread goes
				// on: a window that opens next reads the layout after it.
				if err := client.CommandDone(rex.LayoutCommand{ID: cmd.ID, Code: "desktop_unavailable", Error: "no GoRex window is open"}); err != nil {
					log.Printf("answering a layout command: %v", err)
				}
				done <- nil
				return
			}
			res := a.command(*cmd)
			if res.Code == "" {
				st.save()
			}
			done <- &res
		})
		// A main thread that runs nothing does not answer, and may make the
		// command still; one that runs it takes its time, as a session may
		// be slow to start.
		var res rex.LayoutCommand
		select {
		case <-started:
			answer := <-done
			if answer == nil {
				continue // answered
			}
			res = *answer
		case <-time.After(rex.AppCommandTimeout):
			res = rex.LayoutCommand{ID: cmd.ID, Code: "layout_timeout", Error: "the GoRex window does not answer; the change may still appear"}
		}
		if res.Code == "" {
			st.out.wait(rex.CommandTimeout)
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

// commandAppFor is the window that makes a command: the one showing the
// command's space, else the first, as a client of before spaces had it.
func (st *store) commandAppFor(space string) *App {
	if space != "" {
		if w := st.workspaceByID(space); w != nil && w.app != nil {
			return w.app
		}
	}
	return st.commandApp()
}

// command runs what another client asks of the window: an action, on
// this machine's panes, config.reload or a teleport; the caller saves the
// layout at once, for the client to see it. A server of before it made
// the layout's changes itself (rex.CapabilityLayoutServer) makes them
// once the window says it does not.
func (a *App) command(c rex.LayoutCommand) rex.LayoutCommand {
	res := rex.LayoutCommand{ID: c.ID}
	fail := func(code, msg string) rex.LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	switch c.Op {
	case "config.reload":
		a.err = ""
		a.reloadLuaConfig()
		if a.err != "" {
			return fail("config_failed", a.err)
		}
		return res
	case "action":
		if blockedFromClient(c.Action) {
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
			a.revealPane(p)
			p.Tab.setFocus(p)
			a.focusReq = p
		}
		cmd := lookupCommand(c.Action)
		if cmd == nil {
			return fail("unknown_action", "GoRex does not know "+c.Action)
		}
		if c.Remote && cmd.Custom {
			return fail("not_allowed", "GoRex does not run a custom action from a remote client")
		}
		var args map[string]any
		if len(c.Args) > 0 {
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return fail("bad_args", err.Error())
			}
		}
		args = coerceArgs(cmd, args)
		if err := cmd.invoke(a, args); err != nil {
			return fail("action_failed", err.Error())
		}
		return res
	case "pane.teleporting", "pane.teleported":
		// The CLI teleports the pane's work (gorex teleport): the window
		// waits for the session on the host, then the pane becomes it, as
		// the menu's teleport does.
		p := a.localPane(c.SID)
		h, code, msg := a.hostAt(c.Host)
		switch {
		case p == nil:
			return fail("pane_not_found", "no such pane in the GoRex window")
		case h == nil || c.Target == "":
			return fail(cmp.Or(code, "bad_args"), cmp.Or(msg, "no session"))
		case c.Op == "pane.teleporting":
			h.expecting(c.Target, true)
			return res
		}
		a.teleported(p, h, rex.SessionInfo{ID: c.Target})
		h.expecting(c.Target, false)
		return res
	}
	return fail("desktop_unavailable", "the session server makes this change")
}

// coerceArgs turns the CLI's strings into the types an action declares.
func coerceArgs(cmd *command, args map[string]any) map[string]any {
	for _, spec := range cmd.Args {
		s, ok := args[spec.Name].(string)
		if !ok {
			continue
		}
		switch spec.Type {
		case "number":
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				args[spec.Name] = n
			}
		case "integer":
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				args[spec.Name] = n
			}
		case "boolean":
			if b, err := strconv.ParseBool(s); err == nil {
				args[spec.Name] = b
			}
		}
	}
	return args
}

// localTabs returns the tabs with a pane of this machine, in order: those
// the server's other clients see.
func (a *App) localTabs() []*Tab {
	var tabs []*Tab
	for _, t := range a.tabs {
		if slices.ContainsFunc(t.allPanes(), func(p *Pane) bool { return p.host == nil && p.SID != "" && !p.closed }) {
			tabs = append(tabs, t)
		}
	}
	return tabs
}

// localPane returns the pane of this machine's session sid, nil for none.
func (a *App) localPane(sid string) *Pane {
	for _, t := range a.tabs {
		for _, p := range t.allPanes() {
			if p.host == nil && sid != "" && p.SID == sid && !p.closed {
				return p
			}
		}
	}
	return nil
}

// outbox sends the app's changes of the layout to the server, as commands
// (rex.LayoutCommand), in order and one at a time; once none waits, and
// the server's layout is past the one the app shows, the app takes it
// (take): its own changes, as the server made them, and other clients'
// alike. A command's done runs then, on the main thread, with its answer.
// Without update, as in tests, it sends on the caller's goroutine.
type outbox struct {
	mu      sync.Mutex
	client  *rex.Client
	queue   []outCmd
	dones   []func()
	running bool
	idle    chan struct{} // closed once nothing waits
	// rev is the revision of the layout the app shows, told the latest the
	// server told of, and check asks to read the server's, as after a
	// send.
	rev, told uint64
	check     bool
	// update runs a change on the main thread; take shows the server's
	// layout there, and returns false while the app has a change of its
	// own to send first.
	update func(func())
	take   func(rex.LayoutState) bool
}

// outCmd is a command to send, and what runs with its answer.
type outCmd struct {
	cmd  rex.LayoutCommand
	done func(rex.LayoutCommand)
}

// use makes c the connection the outbox sends through, a server's whose
// revisions are its own: the app shows none of its layouts yet.
func (o *outbox) use(c *rex.Client, update func(func()), take func(rex.LayoutState) bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.client, o.update, o.take = c, update, take
	o.queue, o.dones, o.rev, o.told, o.check = nil, nil, 0, 0, false
}

// add queues c after the commands waiting, to send once kicked.
func (o *outbox) add(c rex.LayoutCommand, done func(rex.LayoutCommand)) {
	o.mu.Lock()
	o.queue = append(o.queue, outCmd{c, done})
	o.mu.Unlock()
}

// kick runs the outbox, unless it runs or has nothing to do.
func (o *outbox) kick() {
	o.mu.Lock()
	if o.running || o.client == nil || len(o.queue) == 0 && len(o.dones) == 0 && !o.behind() {
		o.mu.Unlock()
		return
	}
	o.running, o.idle = true, make(chan struct{})
	inline := o.update == nil
	o.mu.Unlock()
	if inline {
		o.run()
	} else {
		go o.run()
	}
}

// behind tells that the server's layout may be past the app's. Call it
// with mu held.
func (o *outbox) behind() bool { return o.take != nil && (o.check || o.told > o.rev) }

// pending tells that commands wait to be sent.
func (o *outbox) pending() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.queue) > 0
}

// onMain runs fn on the main thread, and returns what it does; false once
// c closes.
func (o *outbox) onMain(c *rex.Client, update func(func()), fn func() bool) bool {
	if update == nil {
		return fn()
	}
	ch := make(chan bool, 1)
	update(func() { ch <- fn() })
	select {
	case ok := <-ch:
		return ok
	case <-c.Closed():
		return false
	}
}

// run sends the commands waiting, then takes the server's layout, until
// nothing is left to do or the app holds the take off.
func (o *outbox) run() {
	for {
		o.mu.Lock()
		c, update, take := o.client, o.update, o.take
		if len(o.queue) > 0 {
			x := o.queue[0]
			o.queue = o.queue[1:]
			o.mu.Unlock()
			res, err := c.Command(x.cmd)
			if err != nil {
				res = rex.LayoutCommand{Code: "failed", Error: err.Error()}
			}
			if res.Code != "" && res.Code != "tab_not_found" {
				log.Printf("gorex: %s: %s", x.cmd.Op, res.Error)
			}
			o.mu.Lock()
			if c == o.client {
				o.check = true
				if x.done != nil {
					o.dones = append(o.dones, func() { x.done(res) })
				}
			}
			o.mu.Unlock()
			continue
		}
		if !o.behind() {
			dones := o.dones
			o.dones = nil
			if len(dones) == 0 {
				o.running = false
				close(o.idle)
				o.mu.Unlock()
				return
			}
			o.mu.Unlock()
			o.onMain(c, update, func() bool {
				for _, d := range dones {
					d()
				}
				return true
			})
			continue
		}
		rev := o.rev
		o.check = false
		o.mu.Unlock()
		ls, err := c.LayoutState()
		o.mu.Lock()
		if c != o.client {
			o.mu.Unlock()
			continue // of a connection no longer used
		}
		if err != nil || ls.Revision == rev {
			o.told = min(o.told, o.rev) // nothing to take
		}
		o.mu.Unlock()
		if err != nil {
			log.Printf("gorex: reading the layout: %v", err)
			continue
		}
		if ls.Revision == rev {
			continue
		}
		took := o.onMain(c, update, func() bool {
			// Not of a connection the app no longer uses.
			o.mu.Lock()
			current := c == o.client
			o.mu.Unlock()
			return current && take(ls)
		})
		o.mu.Lock()
		if c == o.client {
			if took {
				o.rev = ls.Revision
			} else if o.check = true; len(o.queue) == 0 {
				// Held off by a gesture: the save that ends it kicks.
				o.running = false
				close(o.idle)
				o.mu.Unlock()
				return
			}
		}
		o.mu.Unlock()
	}
}

// tell notes that the server's layout, of c, is at rev: past the app's, the
// app takes it.
func (o *outbox) tell(c *rex.Client, rev uint64) {
	o.mu.Lock()
	if c != o.client {
		o.mu.Unlock()
		return
	}
	o.told = max(o.told, rev)
	o.mu.Unlock()
	o.kick()
}

// follow has the app check once for a layout the server did not tell of,
// as one made before c subscribed.
func (o *outbox) follow(c *rex.Client) {
	o.mu.Lock()
	if c != o.client {
		o.mu.Unlock()
		return
	}
	o.check = true
	o.mu.Unlock()
	o.kick()
}

// took notes that the app shows the server's layout at rev, as read.
func (o *outbox) took(rev uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rev = rev
}

// revision is the revision of the layout the app shows.
func (o *outbox) revision() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rev
}

// wait waits until what was sent is made, for d at most.
func (o *outbox) wait(d time.Duration) {
	o.mu.Lock()
	idle, running := o.idle, o.running
	o.mu.Unlock()
	if !running {
		return
	}
	select {
	case <-idle:
	case <-time.After(d):
	}
}

// diffLayout returns the commands that make the server's layout, base as
// the app knows it, the app's, now. A session of this machine now no
// longer has ends (pane.close), those of a tab or a space it no longer
// has with it (tab.close, space.close); new spaces come first, and the
// tabs that take a session from another tab before the others, so that
// the session moves before that tab leaves it out (tab.set). Sessions
// start only through commands of their own.
func diffLayout(base, now savedStore) []rex.LayoutCommand {
	var out []rex.LayoutCommand
	tabs, sids := map[string]bool{}, map[string]bool{}
	for _, w := range now.Sessions {
		for _, t := range w.Tabs {
			tabs[t.ID] = true
			for sid := range localOf(t) {
				sids[sid] = true
			}
		}
	}
	// Where each tab of base is, and its tab.
	type place struct {
		space string
		at    int
		tab   layout.Tab
	}
	was := map[string]place{}
	for _, w := range base.Sessions {
		for i, t := range w.Tabs {
			was[t.ID] = place{w.ID, i, t}
		}
	}
	for _, w := range now.Sessions {
		if base.Space(w.ID) == nil {
			out = append(out, rex.LayoutCommand{Op: "space.create", Space: w.ID, Name: w.Name, Color: colorArg(w.Color)})
		}
	}
	for _, bw := range base.Sessions {
		if now.Space(bw.ID) == nil {
			continue
		}
		for _, t := range bw.Tabs {
			if !tabs[t.ID] {
				continue
			}
			for sid := range localOf(t) {
				if !sids[sid] {
					out = append(out, rex.LayoutCommand{Op: "pane.close", Space: bw.ID, SID: sid})
				}
			}
		}
	}
	for _, takes := range []bool{true, false} {
		for _, w := range now.Sessions {
			activeWas := ""
			if bw := base.Space(w.ID); bw != nil && bw.Active >= 0 && bw.Active < len(bw.Tabs) {
				activeWas = bw.Tabs[bw.Active].ID
			}
			for i, t := range w.Tabs {
				p, ok := was[t.ID]
				focus := i == w.Active && activeWas != t.ID
				if ok && p.space == w.ID && p.at == i && !focus && sameTab(p.tab, t) {
					continue
				}
				gains := false
				for sid := range localOf(t) {
					if _, in := localOf(p.tab)[sid]; !in {
						gains = true
					}
				}
				if gains != takes {
					continue
				}
				tab := serverTab(t)
				out = append(out, rex.LayoutCommand{Op: "tab.set", Space: w.ID, Index: i, TabLayout: &tab, Focus: focus})
			}
		}
	}
	for _, bw := range base.Sessions {
		if now.Space(bw.ID) == nil {
			out = append(out, rex.LayoutCommand{Op: "space.close", Space: bw.ID})
			continue
		}
		for _, t := range bw.Tabs {
			if !tabs[t.ID] {
				out = append(out, rex.LayoutCommand{Op: "tab.close", Space: bw.ID, Tab: t.ID})
			}
		}
	}
	for _, w := range now.Sessions {
		if bw := base.Space(w.ID); bw != nil && (bw.Name != w.Name || bw.Color != w.Color) {
			out = append(out, rex.LayoutCommand{Op: "space.rename", Space: w.ID, Name: w.Name, Color: colorArg(w.Color)})
		}
	}
	if !slices.Equal(base.Windows, now.Windows) && len(now.Windows) > 0 {
		out = append(out, rex.LayoutCommand{Op: "windows.set", Spaces: now.Windows})
	}
	return out
}

// colorArg is a space's color as a command gives it: "default" for none.
func colorArg(color string) string { return cmp.Or(color, "default") }

// localOf are the sessions of this machine in a tab.
func localOf(t layout.Tab) map[string]bool {
	out := map[string]bool{}
	for _, r := range t.Roots() {
		for _, l := range layout.Leaves(r) {
			if l.Host == "" && l.SID != "" {
				out[l.SID] = true
			}
		}
	}
	return out
}

// serverTab is a tab as the app sends it: a canvas's pan and zoom are the
// window's own (they show the canvas, as a scroll does a page).
func serverTab(t layout.Tab) layout.Tab {
	if t.Canvas != nil {
		cv := *t.Canvas
		cv.X, cv.Y, cv.Scale = 0, 0, 0
		t.Canvas = &cv
	}
	return t
}

// sameTab tells whether the tab the app has is the one the server has, but
// for the size of its panes' sessions, which is theirs.
func sameTab(x, y layout.Tab) bool {
	return bytes.Equal(tabJSON(x, false), tabJSON(y, false))
}

// tabJSON is a tab as sameTab and sameArrangement compare it: without its
// panes' sizes and canvas view, and with facts, their directories and
// programs, too.
func tabJSON(t layout.Tab, facts bool) []byte {
	t = serverTab(t)
	var strip func(n *layout.Node) *layout.Node
	strip = func(n *layout.Node) *layout.Node {
		if n == nil {
			return nil
		}
		c := *n
		c.Cols, c.Rows = 0, 0
		if facts {
			c.Dir, c.Prog, c.Args, c.AgentSession, c.AgentConfigDir = "", "", nil, "", ""
		}
		c.A, c.B = strip(n.A), strip(n.B)
		return &c
	}
	t.Root = strip(t.Root)
	t.Floats = slices.Clone(t.Floats)
	for i := range t.Floats {
		t.Floats[i].Pane = strip(t.Floats[i].Pane)
	}
	b, _ := json.Marshal(t)
	return b
}

// sameArrangement tells whether two layouts are the same but for their
// panes' facts, which the server keeps: what the app sent coming back.
func sameArrangement(x, y savedStore) bool {
	if !slices.Equal(x.Windows, y.Windows) || len(x.Sessions) != len(y.Sessions) {
		return false
	}
	for i, xw := range x.Sessions {
		yw := y.Sessions[i]
		if xw.ID != yw.ID || xw.Name != yw.Name || xw.Color != yw.Color || xw.Active != yw.Active || len(xw.Tabs) != len(yw.Tabs) {
			return false
		}
		for j := range xw.Tabs {
			if !bytes.Equal(tabJSON(xw.Tabs[j], true), tabJSON(yw.Tabs[j], true)) {
				return false
			}
		}
	}
	return true
}
