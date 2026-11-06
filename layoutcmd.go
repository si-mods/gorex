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
	"gorex/locales"
)

// takeCommands runs what other clients of this machine's server ask of
// the window (rex.LayoutCommand): an action or config.reload, one at a
// time, on the main thread through update, in the window whose
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
			a := st.commandAppFor(cmd.Space, cmd.SID)
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
// command's space, else the one showing its pane, else the first, as a
// client of before spaces had it.
func (st *store) commandAppFor(space, sid string) *App {
	if space != "" {
		if w := st.workspaceByID(space); w != nil && w.app != nil {
			return w.app
		}
	}
	for _, a := range st.apps {
		if a.localPane(sid) != nil {
			return a
		}
	}
	return st.commandApp()
}

// hiddenSpace is the space no window shows that a command names, by its ID
// or by a pane of session sid in it; nil for none.
func (st *store) hiddenSpace(space, sid string) *Workspace {
	for _, w := range st.workspaces {
		if w.app != nil {
			continue
		}
		if space != "" && w.ID == space {
			return w
		}
		if sid == "" || space != "" {
			continue
		}
		for _, t := range w.layout.Tabs {
			for _, r := range t.Roots() {
				if n := layout.Find(r, sid); n != nil && n.Host == "" {
					return w
				}
			}
		}
	}
	return nil
}

// command runs what another client asks of the window: an action, on
// this machine's panes, or config.reload; the caller saves the layout at
// once, for the client to see it. A server of before it made
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
		// An action on a space no window shows, or on a pane of one, shows
		// it first, in this window, as the action acts on what it shows.
		if a.st != nil {
			if w := a.st.hiddenSpace(c.Space, c.SID); w != nil {
				a.showWorkspace(w)
			}
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
	}
	// A server of before it made the layout's changes queues them here:
	// the app, read-only on it, cannot make them either.
	if a.st != nil && a.st.readOnly {
		return fail("read_only", locales.ServerLayoutReadOnly())
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
// (take), on the main thread: its own changes, as the server made them,
// and other clients' alike. A command's done runs then, on the main
// thread, with its answer. The outbox never waits for the main thread: it
// posts there (update) and goes on. Without update, as in tests, it runs
// on the caller's goroutine.
type outbox struct {
	mu      sync.Mutex
	client  *rex.Client
	queue   []outCmd
	dones   []func()
	running bool
	// rev is the revision of the layout the app shows, told the latest the
	// server told of, and check asks to read the server's, as after a
	// send; force has the app take it even at rev, as after a refusal,
	// which the app's own view of the layout may not show. sending tells
	// a command on its way, and taking a take posted to the main thread,
	// not run yet; sent counts the commands answered, so that a take of a
	// layout read before the last of them does not take it.
	rev, told                     uint64
	check, force, sending, taking bool
	sent                          uint64
	// held tells a take the app held off, as for a gesture, which waits
	// for the next kick, as the save that ends the gesture.
	held bool
	// revs are the revisions of the tabs (layout.Tab.Rev) the app's
	// changes build on: those of the layout it took last, and those its
	// own changes made since. One another client changed it does not
	// know, so that a change built on the tab as it was is refused.
	revs map[string]uint64
	// quiet closes once no command waits nor is on its way.
	quiet chan struct{}
	// update runs a change on the main thread; take shows the server's
	// layout there, and returns false while the app has a change of its
	// own to send first, or a gesture runs.
	update func(func())
	take   func(rex.LayoutState) bool
}

// outCmd is a command to send, and what runs with its answer.
type outCmd struct {
	cmd  rex.LayoutCommand
	done func(rex.LayoutCommand)
}

// baseOf is the tab a change of the app's builds on, whose revision it is
// sent with (rex.LayoutCommand.Base): that of a tab.set, a tab.close or a
// pane.close of a batch; "" for none.
func baseOf(c rex.LayoutCommand) string {
	switch {
	case c.Op == "tab.set" && c.TabLayout != nil:
		return c.TabLayout.ID
	case c.Op == "tab.close" || c.Op == "pane.close":
		return c.Tab
	}
	return ""
}

// lost is the answer of a command of a connection the outbox no longer
// uses.
func lost(c rex.LayoutCommand) rex.LayoutCommand {
	return rex.LayoutCommand{ID: c.ID, Code: "failed", Error: "rex: connection to the server lost"}
}

// use makes c the connection the outbox sends through, a server's whose
// revisions are its own: the app shows none of its layouts yet. What
// waited for the connection before goes: each command's done runs with an
// error, as the app counts on it to, and the one on its way does once it
// answers.
func (o *outbox) use(c *rex.Client, update func(func()), take func(rex.LayoutState) bool) {
	o.mu.Lock()
	dropped, dones := o.queue, o.dones
	o.client, o.update, o.take = c, update, take
	o.queue, o.dones, o.rev, o.told, o.check, o.force, o.revs = nil, nil, 0, 0, false, false, nil
	o.quieten()
	o.mu.Unlock()
	for _, d := range dones {
		d()
	}
	for _, x := range dropped {
		if x.done != nil {
			x.done(lost(x.cmd))
		}
	}
}

// quieten tells who waits that no command waits now. Call it with mu held.
func (o *outbox) quieten() {
	if o.quiet != nil && len(o.queue) == 0 && !o.sending {
		close(o.quiet)
		o.quiet = nil
	}
}

// add queues c after the commands waiting, to send once kicked.
func (o *outbox) add(c rex.LayoutCommand, done func(rex.LayoutCommand)) {
	o.mu.Lock()
	o.queue = append(o.queue, outCmd{c, done})
	if o.quiet == nil {
		o.quiet = make(chan struct{})
	}
	o.mu.Unlock()
}

// kick runs the outbox, unless it runs or has nothing to do.
func (o *outbox) kick() {
	o.mu.Lock()
	o.held = false
	if o.running || o.client == nil || len(o.queue) == 0 && len(o.dones) == 0 && !o.behind() {
		o.mu.Unlock()
		return
	}
	o.running = true
	inline := o.update == nil
	o.mu.Unlock()
	if inline {
		o.run()
	} else {
		go o.run()
	}
}

// behind tells that the server's layout may be past the app's, and no
// take of it waits for the main thread. Call it with mu held.
func (o *outbox) behind() bool {
	return o.take != nil && !o.taking && !o.held && (o.check || o.force || o.told > o.rev)
}

// pending tells that commands wait to be sent.
func (o *outbox) pending() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.queue) > 0
}

// post runs fn on the main thread, without waiting for it.
func (o *outbox) post(update func(func()), fn func()) {
	if update == nil {
		fn()
	} else {
		update(fn)
	}
}

// run sends the commands waiting, then reads the server's layout and posts
// its take, until nothing is left to do.
func (o *outbox) run() {
	for {
		o.mu.Lock()
		c, update := o.client, o.update
		if len(o.queue) > 0 {
			x := o.queue[0]
			o.queue = o.queue[1:]
			x.cmd.Batch = slices.Clone(x.cmd.Batch)
			for i, b := range x.cmd.Batch {
				if tab := baseOf(b); tab != "" {
					x.cmd.Batch[i].Base = o.revs[tab]
				}
			}
			o.sending = true
			o.mu.Unlock()
			res, err := c.Command(x.cmd)
			if err != nil {
				res = rex.LayoutCommand{Code: "failed", Error: err.Error()}
			}
			if res.Code != "" && res.Code != "tab_not_found" {
				log.Printf("gorex: %s: %s", x.cmd.Op, res.Error)
			}
			o.mu.Lock()
			o.sending = false
			o.sent++
			o.quieten()
			if c != o.client {
				res = lost(x.cmd)
			} else {
				o.check = true
				// A refused change leaves the app showing what the server
				// does not have: it takes the server's, as it is.
				o.force = o.force || res.Code != "" && res.Code != "tab_not_found"
				o.count(res.Revs)
			}
			if x.done != nil {
				o.dones = append(o.dones, func() { x.done(res) })
			}
			o.mu.Unlock()
			continue
		}
		if !o.behind() {
			// The dones run once the window shows their changes: after the
			// take waiting, if one does.
			dones := o.dones
			if o.taking {
				dones = nil
			}
			o.dones = o.dones[len(dones):]
			o.running = false
			o.mu.Unlock()
			if len(dones) > 0 {
				o.post(update, func() {
					for _, d := range dones {
						d()
					}
				})
			}
			return
		}
		rev, force, sent := o.rev, o.force, o.sent
		o.check, o.force = false, false
		o.mu.Unlock()
		ls, err := c.LayoutState()
		o.mu.Lock()
		if c != o.client {
			o.mu.Unlock()
			continue // of a connection no longer used
		}
		if err != nil || ls.Revision == rev && !force {
			o.told = min(o.told, o.rev) // nothing to take
			o.mu.Unlock()
			if err != nil {
				log.Printf("gorex: reading the layout: %v", err)
			}
			continue
		}
		o.taking = true
		o.mu.Unlock()
		o.post(update, func() { o.takeOn(c, ls, sent, force) })
	}
}

// takeOn has the app take ls, of c, read once sent commands were answered,
// and makes the revisions the app's changes build on those of ls, at once,
// on the main thread. A layout read before a later answer, or one the app
// holds off (take), it leaves for a read after.
func (o *outbox) takeOn(c *rex.Client, ls rex.LayoutState, sent uint64, force bool) {
	o.mu.Lock()
	current := c == o.client && sent == o.sent
	o.taking = false
	take := o.take
	o.mu.Unlock()
	took := current && take(ls)
	o.mu.Lock()
	if c == o.client {
		if took {
			o.rev, o.revs = ls.Revision, tabRevs(ls.Layout)
		} else {
			o.check, o.force = true, o.force || force
		}
	}
	// Held off by a gesture, with nothing to send, it waits for the save
	// that ends the gesture, which kicks; else it goes on.
	o.held = !took && current && len(o.queue) == 0
	held := o.held
	o.mu.Unlock()
	if !held {
		o.kick()
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

// refresh has the app take the server's layout again, as it is.
func (o *outbox) refresh() {
	o.mu.Lock()
	o.force = true
	o.mu.Unlock()
}

// took notes that the app shows the server's layout raw, at rev, as read.
func (o *outbox) took(rev uint64, raw []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rev, o.revs = rev, tabRevs(raw)
}

// count takes the revisions of the tabs one of the app's changes made: a
// tab one past the revision the app knows is the app's change alone; one
// further on another client changed too, and keeps the revision the app
// knew, so that what the app builds on it is refused. Call it with mu
// held.
func (o *outbox) count(revs map[string]uint64) {
	for id, rev := range revs {
		if o.revs[id]+1 == rev {
			if o.revs == nil {
				o.revs = map[string]uint64{}
			}
			o.revs[id] = rev
		}
	}
}

// tabRevs are the revisions of the tabs of a layout, by their IDs.
func tabRevs(raw []byte) map[string]uint64 {
	out := map[string]uint64{}
	for _, w := range layout.Parse(raw).Sessions {
		for _, t := range w.Tabs {
			out[t.ID] = t.Rev
		}
	}
	return out
}

// revision is the revision of the layout the app shows.
func (o *outbox) revision() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rev
}

// wait waits until the server answered what was sent, for d at most: it
// needs no main thread, which may be the one waiting, as a window that
// closes saves.
func (o *outbox) wait(d time.Duration) {
	o.mu.Lock()
	quiet := o.quiet
	o.mu.Unlock()
	if quiet == nil {
		return
	}
	select {
	case <-quiet:
	case <-time.After(d):
	}
}

// diffLayout returns the changes that make the server's layout, base as
// the app knows it, the app's, now, which the app sends as one batch
// (layout.apply) the server makes whole or not at all:
//   - new spaces first (space.create);
//   - a session of this machine now no longer has ends (pane.close), those
//     of a tab or a space it no longer has with it (tab.close, space.close,
//     after the tabs);
//   - a tab new, changed, or moved among its space's tabs goes as tab.set,
//     after the tab before it (After). A move is of the tabs out of order,
//     the fewest: the others keep their places, and so a close or a move of
//     one tab sends nothing of the tabs after it;
//   - then names and colors, and the windows' spaces.
//
// Sessions start only through commands of their own.
func diffLayout(base, now savedStore) []rex.LayoutCommand {
	var out []rex.LayoutCommand
	tabs, home := map[string]bool{}, map[string]bool{}
	for _, w := range now.Sessions {
		for _, t := range w.Tabs {
			tabs[t.ID] = true
			for sid := range localOf(t) {
				home[sid] = true
			}
		}
	}
	// Where each tab of base is, and its tab.
	type place struct {
		space string
		tab   layout.Tab
	}
	was := map[string]place{}
	for _, w := range base.Sessions {
		for _, t := range w.Tabs {
			was[t.ID] = place{w.ID, t}
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
				if !home[sid] {
					out = append(out, rex.LayoutCommand{Op: "pane.close", Space: bw.ID, Tab: t.ID, SID: sid})
				}
			}
		}
	}
	for _, w := range now.Sessions {
		activeWas := ""
		bw := base.Space(w.ID)
		if bw != nil && bw.Active >= 0 && bw.Active < len(bw.Tabs) {
			activeWas = bw.Tabs[bw.Active].ID
		}
		moved := outOfOrder(bw, w)
		for i, t := range w.Tabs {
			p, ok := was[t.ID]
			focus := i == w.Active && activeWas != t.ID
			if ok && p.space == w.ID && !moved[t.ID] && !focus && sameTab(p.tab, t) {
				// The directories of another host's panes, which no server
				// here stamps, go as the server's tab with them alone:
				// neither its focus nor its arrangement.
				if ft, changed := hostDirs(p.tab, t); changed {
					c := rex.LayoutCommand{Op: "tab.set", Space: w.ID, TabLayout: &ft}
					if i > 0 {
						c.After = w.Tabs[i-1].ID
					}
					out = append(out, c)
				}
				continue
			}
			tab := serverTab(t)
			c := rex.LayoutCommand{Op: "tab.set", Space: w.ID, TabLayout: &tab, Focus: focus}
			if i > 0 {
				c.After = w.Tabs[i-1].ID
			}
			out = append(out, c)
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

// outOfOrder are the tabs of w, of base w was, whose order the fewest
// moves change: those not in the longest run of tabs that kept theirs.
func outOfOrder(was *savedWorkspace, w savedWorkspace) map[string]bool {
	if was == nil {
		return nil
	}
	at := map[string]int{}
	for i, t := range was.Tabs {
		at[t.ID] = i
	}
	var ids []string
	var seq []int
	for _, t := range w.Tabs {
		if i, ok := at[t.ID]; ok {
			ids, seq = append(ids, t.ID), append(seq, i)
		}
	}
	// The longest increasing run of seq, by its ends.
	n := len(seq)
	length, prev := make([]int, n), make([]int, n)
	best := -1
	for i := range seq {
		length[i], prev[i] = 1, -1
		for j := range i {
			if seq[j] < seq[i] && length[j]+1 > length[i] {
				length[i], prev[i] = length[j]+1, j
			}
		}
		if best < 0 || length[i] > length[best] {
			best = i
		}
	}
	keep := map[int]bool{}
	for i := best; i >= 0; i = prev[i] {
		keep[i] = true
	}
	moved := map[string]bool{}
	for i, id := range ids {
		if !keep[i] {
			moved[id] = true
		}
	}
	return moved
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

// hostDirs is base, the server's tab, with the directories of another
// host's panes as the app's tab t has them, and whether one differs.
func hostDirs(base, t layout.Tab) (layout.Tab, bool) {
	dirs := map[[2]string]string{}
	for _, r := range t.Roots() {
		for _, l := range layout.Leaves(r) {
			if l.Host != "" && l.SID != "" {
				dirs[[2]string{l.Host, l.SID}] = l.Dir
			}
		}
	}
	var out layout.Tab
	b, _ := json.Marshal(base)
	json.Unmarshal(b, &out)
	changed := false
	for _, r := range out.Roots() {
		for _, l := range layout.Leaves(r) {
			if d, ok := dirs[[2]string{l.Host, l.SID}]; ok && l.Host != "" && d != l.Dir {
				l.Dir, changed = d, true
			}
		}
	}
	return serverTab(out), changed
}

// sameTab tells whether the tab the app has is the one the server has,
// but for its revision, its panes' facts, which the server keeps of their
// sessions, and a canvas's view, the window's own.
func sameTab(x, y layout.Tab) bool { return bytes.Equal(x.Arrangement(), y.Arrangement()) }

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
			if !bytes.Equal(xw.Tabs[j].Arrangement(), yw.Tabs[j].Arrangement()) {
				return false
			}
		}
	}
	return true
}
