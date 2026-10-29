package control

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"gorex/internal/rex"
)

// commandWait is the longest a change of the layout may take, the app's
// and the bridge's seeing it: a second more than the server waits for the
// app. rootshell gives up on a request after rootshellWait.
var commandWait = rex.CommandTimeout + time.Second

const rootshellWait = 15 * time.Second

// queued is how many changes may wait for the app at once, the one it
// makes too, each answered within rootshellWait: one more is busy.
func queued() int { return max(int((rootshellWait-time.Second)/commandWait), 1) }

// layoutOps are rootshell's methods that the app makes, as the commands
// (rex.LayoutCommand) of the same name.
var layoutOps = map[string]bool{
	"tab.create": true, "pane.split": true, "pane.close": true, "tab.close": true,
	"tab.rename": true, "pane.rename": true, "pane.zoom": true, "tab.move": true,
	"pane.resize": true, "pane.swap": true, "pane.move": true,
}

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
	// before is the layout or shape of the tab the change changes, and
	// beforeTab the tab of the pane a move moves.
	before    string
	beforeTab string
}

// ask asks the app for the change of the layout req asks for, which
// finish answers.
func (b *bridge) ask(req request, p params) (any, error) {
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
		// After the tab rootshell went to last, in its focused pane's
		// directory, as the desktop opens one after the active tab.
		j.cmd.Dir = p.Cwd
		if t := cmp.Or(b.tab(b.anchor), b.tab(b.active)); t != nil {
			j.cmd.After = t.root.sids()[0]
			j.cmd.Dir = cmp.Or(p.Cwd, b.infos[t.focus].Dir)
		}
	case "pane.split":
		if _, err := paneTab(p.Pane); err != nil {
			return nil, err
		}
		j.cmd.Vertical, j.cmd.Dir = p.Dir == "down", p.Cwd
	case "pane.close", "pane.zoom":
		if _, err := paneTab(p.PaneID); err != nil {
			return nil, err
		}
		j.cmd.Mode = p.Mode
		if req.Method == "pane.zoom" && !slices.Contains([]string{"", "on", "off", "toggle"}, p.Mode) {
			return nil, &failure{"invalid_params", "mode is on, off or toggle"}
		}
		if req.Method == "pane.close" {
			j.sids = []string{j.cmd.SID}
		}
	case "pane.rename":
		if p.PaneID == "" {
			return nil, &failure{"invalid_params", "pane_id is required"}
		}
		if _, err := paneTab(p.PaneID); err != nil {
			return nil, err
		}
		// An empty label clears the name, as herdr's pane.rename does.
		j.cmd.Name = strings.TrimSpace(p.Label)
	case "pane.resize":
		if p.PaneID == "" {
			return nil, &failure{"invalid_params", "pane_id is required"}
		}
		t, err := paneTab(p.PaneID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains([]string{"left", "right", "up", "down"}, p.Dir) {
			return nil, &failure{"invalid_params", "direction is left, right, up or down"}
		}
		j.cmd.Direction, j.cmd.Amount = p.Dir, float32(resizeAmount(p.Amount))
		j.before = t.root.ratios()
	case "pane.swap":
		src, dst := sidOf(p.Source), sidOf(p.Pane)
		if src == "" || dst == "" {
			return nil, &failure{"invalid_params", "source_pane_id and target_pane_id are required"}
		}
		st, dt := b.tabOf(src), b.tabOf(dst)
		switch {
		case st == nil || dt == nil:
			return b.swapAnswer("not_found", src, dst), nil
		case src == dst:
			return b.swapAnswer("same_pane", src, dst), nil
		case st != dt:
			return b.swapAnswer("cross_tab", src, dst), nil
		}
		j.cmd.SID, j.cmd.Target = src, dst
		j.before = st.root.shape()
	case "pane.move":
		if p.PaneID == "" {
			return nil, &failure{"invalid_params", "pane_id is required"}
		}
		t, err := paneTab(p.PaneID)
		if err != nil {
			return nil, err
		}
		j.beforeTab = t.id
		if t.zoom {
			return b.moveAnswer(t, j.cmd.SID, "zoomed_tab"), nil
		}
		switch p.Destination.Type {
		case "new_tab":
			j.cmd.NewTab = true
			j.cmd.Name = p.Destination.Label
		case "tab":
			dt := b.tab(p.Destination.TabID)
			if dt == nil {
				return nil, &failure{"tab_not_found", "no tab " + p.Destination.TabID}
			}
			if dt.id == t.id {
				return b.moveAnswer(t, j.cmd.SID, "same_tab"), nil
			}
			if dt.zoom {
				return b.moveAnswer(t, j.cmd.SID, "zoomed_tab"), nil
			}
			target := dt.focus
			if p.Destination.TargetPaneID != "" {
				sid := sidOf(p.Destination.TargetPaneID)
				if !slices.Contains(dt.root.sids(), sid) {
					return nil, &failure{"pane_not_found", "no pane " + p.Destination.TargetPaneID}
				}
				target = sid
			}
			j.cmd.Target = target
			j.cmd.Direction = cmp.Or(p.Destination.Split, "right")
			if j.cmd.Direction != "right" && j.cmd.Direction != "down" {
				return nil, &failure{"invalid_params", "split is right or down"}
			}
			ratio := p.Destination.Ratio
			if ratio <= 0 || ratio > 1 || math.IsNaN(ratio) {
				ratio = 0.5
			}
			j.cmd.Ratio = float32(min(max(ratio, 0.1), 0.9))
		case "new_workspace":
			return nil, &failure{"unsupported_destination", "GoRex has one workspace"}
		default:
			return nil, &failure{"invalid_params", "destination type is tab, new_tab or new_workspace"}
		}
	default: // a tab's
		t := b.tab(p.TabID)
		if t == nil {
			return nil, &failure{"tab_not_found", "no tab " + p.TabID}
		}
		j.cmd.SID = t.root.sids()[0]
		switch req.Method {
		case "tab.close":
			j.sids = t.root.sids()
		case "tab.rename":
			j.cmd.Name = strings.TrimSpace(p.Label)
			if j.cmd.Name == "" {
				return nil, &failure{"invalid_params", "a tab's name cannot be empty"}
			}
		case "tab.move":
			// insert_index is where it goes among the tabs before it moves:
			// the app counts it among its tabs as it makes it.
			j.cmd.Index = p.Index
		}
	}
	b.quiet(j.sids, true)
	select {
	case b.jobs <- j:
		return nil, later
	default:
		b.quiet(j.sids, false)
		return nil, &failure{"busy", "too many layout changes at once"}
	}
}

// resizeAmount is pane.resize's amount: a positive share of the split, at
// most half, herdr's default when none.
func resizeAmount(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0.05
	}
	return min(math.Abs(v), 0.5)
}

// swapAnswer is a pane.swap the bridge makes itself: none, when the panes
// are gone, the same, or in different tabs.
func (b *bridge) swapAnswer(reason, src, dst string) map[string]any {
	swap := map[string]any{
		"changed": false, "reason": reason,
		"source_pane_id": paneID(src), "target_pane_id": paneID(dst),
	}
	if t := cmp.Or(b.tabOf(src), b.tabOf(dst), b.tab(b.active)); t != nil {
		swap["focused_pane_id"], swap["layout"] = paneID(t.focus), b.layoutOf(t)
	}
	return map[string]any{"swap": swap}
}

// moveAnswer is a pane.move the app will not make: from a zoomed tab, or
// into the tab it is in.
func (b *bridge) moveAnswer(t *tab, sid, reason string) map[string]any {
	return map[string]any{"move_result": map[string]any{
		"changed": false, "reason": reason,
		"previous_pane_id": paneID(sid), "previous_tab_id": t.id, "previous_workspace_id": workspace,
		"pane": b.paneInfo(t, sid), "focused_pane_id": paneID(t.focus),
	}}
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
		// A change the app did not make, as a pane that cannot resize,
		// needs no wait for the layout.
		if !j.res.Changed && paneOp(j.cmd.Op) {
			b.complete(j)
			return
		}
		// The new pane has rootshell's focus, as herdr gives it, in what
		// it hears of the layout before the answer too.
		if t := b.tabOf(j.cmd.SID); t != nil && j.cmd.Op == "pane.split" {
			b.picked[t.id] = j.res.New
		}
		b.waiting = j
		b.poll()
		// A move's answer is its new tab, which the app saved before it
		// answered: one pane's tab from before and its new tab look alike,
		// so waiting to see the move would wait forever.
		if j.cmd.Op == "pane.move" {
			b.waiting = nil
			b.complete(j)
			return
		}
		b.check()
		return
	}
	b.complete(j)
}

// paneOp tells that op changes a pane's place in the layout, whose
// answer includes whether it did (Changed).
func paneOp(op string) bool {
	return op == "pane.resize" || op == "pane.swap" || op == "pane.move"
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
// timeout, or at each tick, from a server that tells of no change.
func (b *bridge) recheck(tick <-chan time.Time) <-chan time.Time {
	switch {
	case b.waiting == nil:
		return nil
	case !b.subscribed:
		return tick
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
	case "pane.rename":
		if t := b.tabOf(j.cmd.SID); t != nil {
			result = map[string]any{"pane": b.paneInfo(t, j.cmd.SID)}
		}
	case "pane.resize":
		if t := b.tabOf(j.cmd.SID); t != nil {
			resize := map[string]any{
				"changed": j.res.Changed, "pane_id": paneID(j.cmd.SID),
				"focused_pane_id": paneID(t.focus), "layout": b.layoutOf(t),
			}
			if j.res.Reason != "" {
				resize["reason"] = j.res.Reason
			}
			result = map[string]any{"resize": resize}
		}
	case "pane.swap":
		if t := b.tabOf(j.cmd.SID); t != nil {
			swap := map[string]any{
				"changed": j.res.Changed, "source_pane_id": paneID(j.cmd.SID),
				"target_pane_id": paneID(j.cmd.Target), "focused_pane_id": paneID(t.focus),
				"layout": b.layoutOf(t),
			}
			if j.res.Reason != "" {
				swap["reason"] = j.res.Reason
			}
			result = map[string]any{"swap": swap}
		}
	case "pane.move":
		if t := b.tabOf(j.cmd.SID); t != nil {
			move := map[string]any{
				"changed": j.res.Changed, "previous_pane_id": paneID(j.cmd.SID),
				"previous_tab_id": j.beforeTab, "previous_workspace_id": workspace,
				"pane": b.paneInfo(t, j.cmd.SID), "focused_pane_id": paneID(t.focus),
			}
			if j.res.Reason != "" {
				move["reason"] = j.res.Reason
			}
			if j.cmd.NewTab {
				move["created_tab"] = b.tabInfo(t)
			}
			result = map[string]any{"move_result": move}
		}
	}
	if result == nil && (paneOp(j.cmd.Op) || j.cmd.Op == "pane.rename") {
		b.answer(j.req, nil, &failure{"pane_not_found", "the pane is gone"})
		return
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
	case "tab.move":
		return slices.Index(b.tabs, b.tabOf(j.cmd.SID)) == j.res.Index
	case "pane.rename":
		t := b.tabOf(j.cmd.SID)
		return t != nil && t.root.note(j.cmd.SID) != nil && t.nameOf(j.cmd.SID) == j.res.Name
	case "pane.resize":
		t := b.tabOf(j.cmd.SID)
		return t != nil && t.root.note(j.cmd.SID) != nil && t.root.ratios() != j.before
	case "pane.swap":
		t := b.tabOf(j.cmd.SID)
		return t != nil && t.root.note(j.cmd.SID) != nil && t.root.shape() != j.before
	}
	return true
}

// quiet makes the attaches of sids end without a word, as their panes
// close, or speak again: one that ended meanwhile attaches anew, after a
// gap, its pane staying.
func (b *bridge) quiet(sids []string, on bool) {
	for _, at := range b.attaches {
		if !slices.Contains(sids, at.sid) {
			continue
		}
		at.mu.Lock()
		at.quiet = on
		ended, gen := at.ended && !on, at.gen
		at.ended = false
		at.mu.Unlock()
		if ended {
			at.emit(gen, func(seq uint64) any {
				return map[string]any{"type": "terminal.gap", "attach_id": at.id, "seq": seq, "dropped_bytes": 0}
			})
			at.reattach(b.infos[at.sid])
		}
	}
}
