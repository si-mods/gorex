package control

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorex/internal/rex"
)

// attach is a terminal rootshell attached to: a session attached to anew
// for each snapshot it asks, with what the session prints after it.
type attach struct {
	id, sid string
	limit   int // history_limit_bytes
	b       *bridge

	mu     sync.Mutex
	conn   net.Conn
	gen    int // the attach to the server that is current
	closed bool
	quiet  bool // its pane closes from rootshell: its end is no news
	ended  bool // it ended while quiet
	// push orders what the attach sends: a snapshot before the output that
	// follows it, and nothing of an attach of before after it.
	push sync.Mutex
	seq  uint64
}

// reattach attaches to the session anew, for a snapshot, then what the
// session prints; what the attach of before still reads is dropped.
func (at *attach) reattach(in rex.SessionInfo) {
	at.push.Lock()
	at.mu.Lock()
	at.gen++
	gen := at.gen
	at.mu.Unlock()
	at.push.Unlock()
	go at.run(gen, in)
}

// The waits between attaches to a session that refuses them while it
// lives.
const (
	retryFirst = 250 * time.Millisecond
	retryMost  = 5 * time.Second
)

func (at *attach) run(gen int, in rex.SessionInfo) {
	var conn net.Conn
	for wait := retryFirst; ; wait = min(wait*2, retryMost) {
		var err error
		conn, err = at.b.c.AttachConn(rex.Attach{SID: at.sid, Framed: true, History: at.limit})
		if err == nil {
			break
		}
		log.Printf("control: attaching %s: %v", at.sid, err)
		if !at.alive(gen) {
			return
		}
		time.Sleep(wait)
	}
	at.mu.Lock()
	if at.gen != gen || at.closed {
		at.mu.Unlock()
		conn.Close()
		return
	}
	old := at.conn
	at.conn = conn
	at.mu.Unlock()
	if old != nil {
		old.Close()
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	var h rex.SnapshotHeader
	line, err := r.ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &h)
	}
	snap := make([]byte, max(h.Snapshot, 0))
	if err == nil {
		_, err = io.ReadFull(r, snap)
	}
	if err != nil {
		at.lost(gen)
		return
	}
	at.emit(gen, func(seq uint64) any {
		x, y := cursor(snap)
		state := map[string]any{"cols": h.Cols, "rows": h.Rows, "title": in.Title}
		if strings.HasPrefix(in.Dir, "/") {
			state["cwd"] = in.Dir
		}
		return map[string]any{"type": "terminal.snapshot", "attach_id": at.id, "snapshot": map[string]any{
			"seq": seq, "active_screen": "primary",
			// rootshell hides the cursor before the snapshot, which shows
			// it again only to hide it.
			"primary": "\x1b[?25h" + string(snap), "alternate": nil,
			"state_ansi": "", "pen_ansi": nil,
			"cursor": map[string]any{"x": x, "y": y, "visible": true, "shape": 0},
			"state":  state, "truncated": h.Truncated,
		}}
	})
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p := append([]byte(nil), buf[:n]...)
			at.emit(gen, func(seq uint64) any {
				return map[string]any{"type": "terminal.output", "attach_id": at.id, "seq": seq, "bytes": p}
			})
		}
		if err != nil {
			at.lost(gen)
			return
		}
	}
}

// emit sends what rec makes of the next seq, while gen is current.
func (at *attach) emit(gen int, rec func(seq uint64) any) {
	at.push.Lock()
	defer at.push.Unlock()
	at.mu.Lock()
	current := at.gen == gen && !at.closed
	at.mu.Unlock()
	if current {
		at.seq++
		at.b.send(rec(at.seq))
	}
}

// lost tells rootshell that output of the session is lost, the server
// having dropped the attach while the session goes on: it asks for a
// snapshot then. A session gone detaches (alive).
func (at *attach) lost(gen int) {
	if at.alive(gen) {
		at.emit(gen, func(seq uint64) any {
			return map[string]any{"type": "terminal.gap", "attach_id": at.id, "seq": seq, "dropped_bytes": 0}
		})
	}
}

// alive tells whether gen is current and its session runs. A session that
// ended, or that the server no longer has, is gone, and its pane with it,
// as the desktop closes it: rootshell is told so, and waits for no
// snapshot of it.
func (at *attach) alive(gen int) bool {
	at.mu.Lock()
	current := at.gen == gen && !at.closed
	at.ended = at.ended || current && at.quiet
	current = current && !at.quiet
	at.mu.Unlock()
	if !current {
		return false
	}
	list, err := at.b.c.List()
	if err != nil {
		return false
	}
	for _, in := range list {
		if in.ID == at.sid && !in.Exited {
			return true
		}
	}
	at.emit(gen, func(uint64) any {
		return map[string]any{"type": "terminal.detached", "attach_id": at.id, "reason": "closed"}
	})
	at.detach()
	return false
}

func (at *attach) input(p []byte) {
	at.mu.Lock()
	conn := at.conn
	at.mu.Unlock()
	if conn != nil && len(p) > 0 {
		conn.Write(p)
	}
}

func (at *attach) detach() {
	at.mu.Lock()
	at.closed = true
	conn := at.conn
	at.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// cup is a cursor position, of which a snapshot's last is the cursor's;
// a row or column left out is 1.
var cup = regexp.MustCompile(`\x1b\[(\d*)(?:;(\d*))?H`)

// cursor returns where a snapshot leaves the cursor, from 0.
func cursor(snap []byte) (x, y int) {
	all := cup.FindAllSubmatch(snap, -1)
	if len(all) == 0 {
		return 0, 0
	}
	m := all[len(all)-1]
	row, _ := strconv.Atoi(string(m[1]))
	col, _ := strconv.Atoi(string(m[2]))
	return max(col-1, 0), max(row-1, 0)
}
