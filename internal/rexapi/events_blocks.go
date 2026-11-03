package rexapi

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorex/internal/rex"
)

// blockPollEvery is how often the gateway reads the sessions' records for
// the events blocks send.
var blockPollEvery = 500 * time.Millisecond

// blockWatch is what the gateway saw last of a block's session, for the
// events only a change tells of.
type blockWatch struct {
	seen       bool
	title      string
	bells      int
	status     string
	statusIDs  string
	dir        string
	foreground int
	program    string
	generation uint64
	exited     bool
	code       int
}

// watchBlocks tells of the events blocks send: a title, a bell, the
// programs' status, and the end of the program, which the sessions'
// records hold and no event of the layout does.
func (g *Gateway) watchBlocks() {
	defer g.wg.Done()
	ticker := time.NewTicker(blockPollEvery)
	defer ticker.Stop()
	watches := map[string]*blockWatch{}
	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
		}
		list, err := g.opt.Client.List()
		if err != nil {
			continue
		}
		m := g.modelNow()
		live := map[string]bool{}
		for _, info := range list {
			b := m.blockBySID(info.ID)
			if b == nil {
				continue
			}
			live[b.id] = true
			w := watches[b.id]
			if w == nil {
				w = &blockWatch{}
				watches[b.id] = w
			}
			if !w.seen {
				*w = blockWatch{seen: true, title: info.Title, bells: info.Bells, status: statusSignature(info.Statuses),
					statusIDs: statusIDs(info.Statuses), dir: info.Dir, foreground: info.Foreground, program: info.Program,
					exited: info.Exited, code: info.ExitCode}
				continue
			}
			if info.Title != w.title {
				g.emitBlockEvent(b, "title_changed", map[string]any{"title": info.Title})
				w.title = info.Title
			}
			if info.Bells > w.bells {
				g.emitBlockEvent(b, "bell", nil)
				w.bells = info.Bells
			}
			if info.Dir != w.dir {
				g.emitBlockEvent(b, "pwd_changed", map[string]any{"pwd": fileURL(info.Dir), "owner": eventOwner(&info)})
				w.dir = info.Dir
			}
			if info.Foreground != w.foreground || info.Program != w.program {
				w.generation++
				g.emitBlockEvent(b, "process_changed", map[string]any{
					"generation": w.generation,
					"foreground": map[string]any{"name": info.Program, "pid": info.Foreground, "cwd": info.Dir},
					"ancestors":  []any{},
				})
				w.foreground, w.program = info.Foreground, info.Program
			}
			if sig := statusSignature(info.Statuses); sig != w.status {
				g.emitBlockEvent(b, "program_status_changed", map[string]any{"records": statusRecords(&info)})
				if removed := removedStatusIDs(w.statusIDs, info.Statuses); len(removed) > 0 {
					g.emitBlockEvent(b, "program_status_removed", map[string]any{"ids": removed, "reason": "removed"})
				}
				w.status = sig
				w.statusIDs = statusIDs(info.Statuses)
			}
			if info.Exited && !w.exited {
				g.emitBlockEvent(b, "child_exited", map[string]any{"exit_code": info.ExitCode})
			}
			w.exited, w.code = info.Exited, info.ExitCode
		}
		for id := range watches {
			if !live[id] {
				delete(watches, id)
			}
		}
	}
}

// blockBySID returns the model's block of a GoRex session.
func (m *model) blockBySID(sid string) *mBlock {
	for _, b := range m.blocks {
		if b.sid == sid {
			return b
		}
	}
	return nil
}

// statusSignature names the records a program reported, so that a later
// read can tell whether they changed.
func statusSignature(records []rex.ProgramStatus) string {
	var b strings.Builder
	for _, r := range records {
		b.WriteString(r.ID)
		b.WriteByte(0)
		b.WriteString(r.State)
		b.WriteByte(0)
		b.WriteString(r.Kind)
		b.WriteByte(0)
		b.WriteString(r.Msg)
		b.WriteByte(0)
		b.WriteString(r.Updated.Format(time.RFC3339Nano))
		b.WriteByte(1)
	}
	return b.String()
}

// emitBlockEvent sends a block's event to the sessions that receive them.
func (g *Gateway) emitBlockEvent(b *mBlock, name string, payload any) {
	g.emitSession(b.session, "block_event", map[string]any{
		"block_id": b.id, "creator_name": creatorName, "name": name, "payload": payload,
	})
}

// statusIDs is the set of program-status records, for telling which ones a
// later read removed.
func statusIDs(records []rex.ProgramStatus) string {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00")
}

// removedStatusIDs lists the records that were in prev but are not in now.
func removedStatusIDs(prev string, now []rex.ProgramStatus) []string {
	old := map[string]bool{}
	for _, id := range strings.Split(prev, "\x00") {
		if id != "" {
			old[id] = true
		}
	}
	for _, r := range now {
		delete(old, r.ID)
	}
	out := make([]string, 0, len(old))
	for id := range old {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// fileURL is a reported directory in the file:// form Rex's events use.
func fileURL(dir string) string {
	if dir == "" {
		return ""
	}
	host, _ := os.Hostname()
	return (&url.URL{Scheme: "file", Host: host, Path: filepath.ToSlash(dir)}).String()
}

// eventOwner names the process that caused a terminal event.
func eventOwner(info *rex.SessionInfo) map[string]any {
	return map[string]any{"pid": info.Foreground, "name": info.Program}
}
