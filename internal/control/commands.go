package control

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorex/internal/rex"
)

// commandWait is the longest a change of the layout may take, the app's
// and the bridge's seeing it: rootshell gives up on a request after 15
// seconds.
var commandWait = 5 * time.Second

// job is a change of the layout rootshell asked for in req, which the app
// makes (rex.LayoutCommand): sids are the sessions it ends, whose
// attaches end quietly.
type job struct {
	req     request
	cmd     rex.LayoutCommand
	sids    []string
	start   time.Time
	res     rex.LayoutCommand
	err     error
	handled chan struct{}
}

// ask asks the app for the change of the layout req asks for, which
// finish answers.
func (b *bridge) ask(req request, p params) error {
	j := &job{req: req, start: time.Now(), handled: make(chan struct{}), cmd: rex.LayoutCommand{Op: req.Method}}
	paneTab := func(id string) (*tab, error) {
		j.cmd.SID = sidOf(id)
		if t := b.tabOf(j.cmd.SID); t != nil {
			return t, nil
		}
		return nil, &failure{"pane_not_found", "no pane " + id}
	}
	switch req.Method {
	case "tab.create":
		// After the tab rootshell went to last, as the desktop opens one
		// after the active tab.
		j.cmd.Dir = p.Cwd
		if t := cmp.Or(b.tab(b.anchor), b.tab(b.active)); t != nil {
			j.cmd.After = t.root.sids()[0]
		}
	case "pane.split":
		if _, err := paneTab(p.Pane); err != nil {
			return err
		}
		j.cmd.Vertical, j.cmd.Dir = p.Dir == "down", p.Cwd
	case "pane.close", "pane.zoom":
		if _, err := paneTab(p.PaneID); err != nil {
			return err
		}
		j.cmd.Mode = p.Mode
		if req.Method == "pane.close" {
			j.sids = []string{j.cmd.SID}
		}
	default: // a tab's
		t := b.tab(p.TabID)
		if t == nil {
			return &failure{"tab_not_found", "no tab " + p.TabID}
		}
		j.cmd.SID = t.root.sids()[0]
		switch req.Method {
		case "tab.close":
			j.sids = t.root.sids()
		case "tab.rename":
			j.cmd.Name = strings.TrimSpace(p.Label)
			if j.cmd.Name == "" {
				return &failure{"invalid_params", "a tab's name cannot be empty"}
			}
		case "tab.move":
			// insert_index is where it goes among the tabs before it moves.
			from, to := slices.Index(b.tabs, t), min(max(p.Index, 0), len(b.tabs))
			if from < to {
				to--
			}
			order := slices.Insert(slices.Delete(slices.Clone(b.tabs), from, from+1), to, t)
			if to > 0 {
				j.cmd.After = order[to-1].root.sids()[0]
			}
		}
	}
	b.quiet(j.sids, true)
	select {
	case b.jobs <- j:
		return later
	default:
		b.quiet(j.sids, false)
		return &failure{"busy", "too many layout changes at once"}
	}
}

// work asks the app for each change in turn: one finishes before the next
// starts, as a new tab goes after the one before.
func (b *bridge) work() {
	for {
		var j *job
		select {
		case j = <-b.jobs:
		case <-b.quit:
			return
		}
		j.res, j.err = b.c.Command(j.cmd)
		select {
		case b.done <- j:
		case <-b.quit:
			return
		}
		select {
		case <-j.handled:
		case <-b.quit:
			return
		}
	}
}

// finish answers a change the app did not make, and waits to see one it
// made (check).
func (b *bridge) finish(j *job) {
	if j.err == nil && j.res.Code == "" {
		b.waiting = j
		b.poll()
		b.check()
		return
	}
	b.complete(j)
}

// check answers the change waited for once the bridge's tabs show it, as
// the server tells of the layout the app saved, or once it took too long.
func (b *bridge) check() {
	j := b.waiting
	if j == nil {
		return
	}
	if !b.shows(j) {
		if time.Since(j.start) <= commandWait {
			return
		}
		j.res.Code, j.res.Error = "layout_timeout", "GoRex did not apply the change in time; it may still appear"
	}
	b.waiting = nil
	b.complete(j)
}

// recheck fires when to look again at the change waited for: at its
// timeout, or soon, from a server that tells of no change.
func (b *bridge) recheck() <-chan time.Time {
	switch {
	case b.waiting == nil:
		return nil
	case !b.subscribed:
		return time.After(50 * time.Millisecond)
	}
	return time.After(time.Until(b.waiting.start.Add(commandWait)) + time.Millisecond)
}

// complete answers a change: rootshell is told of the new layout first,
// and every session snapshot after has it.
func (b *bridge) complete(j *job) {
	defer close(j.handled)
	res, err := j.res, j.err
	if err == nil && res.Code != "" {
		if res.Code == "desktop_unavailable" {
			res.Error = fmt.Sprintf("Open the GoRex window on %s to change layouts", b.hello.Host.Name)
		}
		err = &failure{res.Code, res.Error}
	}
	if err != nil {
		b.quiet(j.sids, false)
		b.answer(j.req, nil, err)
		return
	}
	for id, at := range b.attaches {
		if slices.Contains(j.sids, at.sid) {
			at.detach()
			delete(b.attaches, id)
		}
	}
	var result any
	switch j.cmd.Op {
	case "tab.create":
		t := b.tabOf(res.New)
		result = map[string]any{"tab": b.tabInfo(t), "root_pane": b.paneInfo(t, res.New)}
	case "pane.split":
		// The new pane has rootshell's focus, as herdr gives it.
		t := b.tabOf(res.New)
		b.picked[t.id], t.focus = res.New, res.New
	case "tab.rename":
		result = map[string]any{"tab": b.tabInfo(b.tabOf(j.cmd.SID))}
	case "tab.move":
		result = map[string]any{"tabs": b.tabInfos()}
	}
	b.tell()
	b.answer(j.req, result, nil)
}

// shows tells whether the bridge's tabs show the change of j.
func (b *bridge) shows(j *job) bool {
	switch j.cmd.Op {
	case "tab.create", "pane.split":
		return b.tabOf(j.res.New) != nil
	case "pane.close", "tab.close":
		return !slices.ContainsFunc(j.sids, func(sid string) bool { return b.tabOf(sid) != nil })
	case "tab.rename":
		t := b.tabOf(j.cmd.SID)
		return t != nil && t.name == j.cmd.Name
	case "pane.zoom":
		t := b.tabOf(j.cmd.SID)
		return t != nil && t.zoom == j.res.Zoomed
	}
	return true
}

// quiet makes the attaches of sids end without a word, as their panes
// close, or speak again.
func (b *bridge) quiet(sids []string, on bool) {
	for _, at := range b.attaches {
		if slices.Contains(sids, at.sid) {
			at.mu.Lock()
			at.quiet = on
			at.mu.Unlock()
		}
	}
}
