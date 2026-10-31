package rexapi

import (
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
	seen   bool
	title  string
	bells  int
	status string
	exited bool
	code   int
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
				*w = blockWatch{seen: true, title: info.Title, bells: info.Bells, status: statusSignature(info.Statuses), exited: info.Exited, code: info.ExitCode}
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
			if sig := statusSignature(info.Statuses); sig != w.status {
				g.emitBlockEvent(b, "program_status_changed", map[string]any{"records": statusRecords(&info)})
				w.status = sig
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
