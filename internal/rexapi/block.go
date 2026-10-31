package rexapi

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/terminal"

	"gorex/internal/rex"
)

// blockMethod serves a terminal block's methods, under block.method and
// under its full name.
func (c *control) blockMethod(sessionID, blockID, name string, raw json.RawMessage) (any, *apiError) {
	if c.s.conn() == nil {
		return nil, errInternal("the server connection is gone")
	}
	s, apiErr := c.sessionFor(sessionID)
	if apiErr != nil {
		return nil, apiErr
	}
	b, apiErr := c.blockIn(s, blockID, "")
	if apiErr != nil {
		return nil, apiErr
	}
	switch name {
	case "format":
		return c.format(b, raw)
	case "write":
		var p struct {
			Data string `json:"data"`
		}
		if apiErr := decode(raw, &p); apiErr != nil {
			return nil, apiErr
		}
		if err := c.s.conn().Input(b.SID, []byte(p.Data)); err != nil {
			return nil, errInternal("%v", err)
		}
		return map[string]any{"bytes_written": len(p.Data)}, nil
	case "resize":
		var p struct {
			Columns int `json:"columns"`
			Rows    int `json:"rows"`
		}
		if apiErr := decode(raw, &p); apiErr != nil {
			return nil, apiErr
		}
		if p.Columns <= 0 || p.Rows <= 0 || p.Columns > 65535 || p.Rows > 65535 {
			return nil, errBadRequest("a size is 1 to 65535 columns and rows")
		}
		if err := c.s.conn().Resize(b.SID, p.Columns, p.Rows); err != nil {
			return nil, errInternal("%v", err)
		}
		return map[string]any{}, nil
	case "size":
		info := c.info(b)
		return map[string]any{"columns": info.Cols, "rows": info.Rows, "owner": nil, "desired": []any{}}, nil
	case "title":
		return map[string]any{"title": c.info(b).Title}, nil
	case "pwd":
		return map[string]any{"pwd": c.info(b).Dir}, nil
	case "program_status":
		out := []map[string]any{}
		for _, st := range c.statuses(b) {
			r := map[string]any{
				"id": st.ID, "state": st.State, "app": st.App,
				"effective_app": st.App, "updated_at": st.Updated,
			}
			if st.Kind != "" {
				r["kind"] = st.Kind
			}
			if st.Title != "" {
				r["title"] = st.Title
			}
			if st.Msg != "" {
				r["msg"] = st.Msg
			}
			if st.Progress >= 0 {
				r["progress"] = st.Progress
			}
			out = append(out, r)
		}
		return map[string]any{"records": out}, nil
	case "process":
		info := c.info(b)
		out := map[string]any{"child": nil, "last_exit": nil}
		if info.Exited {
			out["last_exit"] = map[string]any{"exit_code": info.ExitCode}
		} else if info.Program != "" {
			out["child"] = map[string]any{"pid": 0, "name": info.Program}
		}
		return out, nil
	case "reset":
		if err := c.s.conn().Input(b.SID, []byte("\x1bc")); err != nil {
			return nil, errInternal("%v", err)
		}
		return map[string]any{}, nil
	case "clear":
		if err := c.s.conn().Input(b.SID, []byte("\x1b[2J\x1b[H")); err != nil {
			return nil, errInternal("%v", err)
		}
		return map[string]any{}, nil
	}
	return nil, errUnsupportedMethod(name)
}

// info returns what the server last said of the block's session.
func (c *control) info(b *mBlock) rexInfo {
	c.s.mu.Lock()
	infos := c.s.infos
	c.s.mu.Unlock()
	if in, ok := infos[b.SID]; ok {
		return rexInfo{Cols: in.Cols, Rows: in.Rows, Title: in.Title, Dir: in.Dir, Program: in.Program, Exited: in.Exited, ExitCode: in.ExitCode}
	}
	return rexInfo{}
}

// statuses returns what a block's program reported it is doing.
func (c *control) statuses(b *mBlock) []rex.ProgramStatus {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	in, ok := c.s.infos[b.SID]
	if !ok {
		return nil
	}
	return in.Statuses
}

// rexInfo is the part of a session's record the block methods report.
type rexInfo struct {
	Cols, Rows int
	Title      string
	Dir        string
	Program    string
	Exited     bool
	ExitCode   int
}

// format reads a terminal's screen and scrollback.
func (c *control) format(b *mBlock, raw json.RawMessage) (any, *apiError) {
	var p struct {
		Format   string `json:"format"`
		Trim     bool   `json:"trim"`
		Unwrap   bool   `json:"unwrap"`
		MaxBytes int    `json:"max_bytes"`
	}
	if apiErr := decode(raw, &p); apiErr != nil {
		return nil, apiErr
	}
	if p.Format == "" {
		p.Format = "text"
	}
	res, err := c.s.conn().Capture(b.SID, 0)
	if err != nil {
		return nil, errInternal("%v", err)
	}
	content := ""
	switch p.Format {
	case "vt":
		content = string(res.Screen)
	case "text":
		text, err := plainText(res.Screen, res.Cols, res.Rows)
		if err != nil {
			return nil, errInternal("%v", err)
		}
		content = text
	default:
		return nil, errNotSupported("GoRex does not format a terminal as %s", p.Format)
	}
	if p.Trim {
		content = trimText(content)
	}
	total := len(content)
	// A screen of escape sequences grows when it is JSON encoded: keep
	// the answer well under the message limit.
	const maxFormat = 128 << 10
	limit := p.MaxBytes
	if limit <= 0 || limit > maxFormat {
		limit = maxFormat
	}
	eof := true
	if len(content) > limit {
		content = content[:limit]
		for !utf8.ValidString(content) {
			content = content[:len(content)-1]
		}
		eof = false
	}
	return map[string]any{"content": content, "total": total, "eof": eof}, nil
}

// plainText replays escape sequences in a headless terminal and returns
// its text, as gorex capture does.
func plainText(screen []byte, cols, rows int) (string, error) {
	if err := terminal.Load(); err != nil {
		return "", err
	}
	conn := &nopConn{done: make(chan struct{})}
	defer conn.Close()
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		return "", err
	}
	defer term.Close()
	if cols > 0 && rows > 0 {
		term.Resize(cols, rows)
	}
	term.Feed(screen)
	return term.Text(), nil
}

// trimText removes trailing spaces from each line and trailing empty
// lines.
func trimText(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// nopConn is a terminal connection that only shows what it is fed.
type nopConn struct {
	done chan struct{}
}

func (c *nopConn) Read([]byte) (int, error) {
	<-c.done
	return 0, errClosed
}

func (c *nopConn) Write(p []byte) (int, error) { return len(p), nil }

func (c *nopConn) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return nil
}

// errClosed ends a nopConn's reader.
var errClosed = &closedError{}

type closedError struct{}

func (*closedError) Error() string { return "connection closed" }

// sendEvent passes a block event on, as a key, a paste or a focus change.
func (c *control) sendEvent(b *mBlock, typ string, payload json.RawMessage) *apiError {
	switch typ {
	case "paste":
		var p struct {
			Text string `json:"text"`
		}
		if apiErr := decode(payload, &p); apiErr != nil {
			return apiErr
		}
		if err := c.s.conn().Paste(b.SID, p.Text); err != nil {
			return errInternal("%v", err)
		}
		return nil
	case "key":
		var p struct {
			Key    string `json:"key"`
			Mods   string `json:"mods"`
			Text   string `json:"text"`
			Action string `json:"action"`
		}
		if apiErr := decode(payload, &p); apiErr != nil {
			return apiErr
		}
		if p.Action != "" && p.Action != "press" {
			return nil
		}
		if data := keyBytes(p.Key, p.Mods, p.Text); len(data) > 0 {
			if err := c.s.conn().Input(b.SID, data); err != nil {
				return errInternal("%v", err)
			}
		}
		return nil
	case "focus":
		return nil
	case "mouse", "wheel":
		return errNotSupported("GoRex does not pass %s events to a terminal", typ)
	}
	return errBadRequest("%q is not an event a client sends", typ)
}

// keyBytes encodes a key press as the bytes a terminal receives.
func keyBytes(key, mods, text string) []byte {
	if text != "" {
		return []byte(text)
	}
	ctrl := strings.Contains(mods, "ctrl")
	alt := strings.Contains(mods, "alt")
	seq := ""
	switch strings.ToLower(key) {
	case "enter", "return":
		seq = "\r"
	case "tab":
		seq = "\t"
	case "backspace":
		seq = "\x7f"
	case "escape", "esc":
		seq = "\x1b"
	case "space":
		seq = " "
	case "delete", "del":
		seq = "\x1b[3~"
	case "insert":
		seq = "\x1b[2~"
	case "home":
		seq = "\x1b[H"
	case "end":
		seq = "\x1b[F"
	case "pageup", "page_up":
		seq = "\x1b[5~"
	case "pagedown", "page_down":
		seq = "\x1b[6~"
	case "up", "arrowup":
		seq = "\x1b[A"
	case "down", "arrowdown":
		seq = "\x1b[B"
	case "right", "arrowright":
		seq = "\x1b[C"
	case "left", "arrowleft":
		seq = "\x1b[D"
	}
	if seq == "" && utf8.RuneCountInString(key) == 1 && ctrl {
		r := key[0]
		switch {
		case r >= 'a' && r <= 'z':
			seq = string(r - 'a' + 1)
		case r >= 'A' && r <= 'Z':
			seq = string(r - 'A' + 1)
		case r == '[':
			seq = "\x1b"
		case r == '\\':
			seq = "\x1c"
		case r == ']':
			seq = "\x1d"
		case r == '^':
			seq = "\x1e"
		case r == '_':
			seq = "\x1f"
		case r == '@' || r == ' ':
			seq = "\x00"
		}
	}
	if seq == "" && utf8.RuneCountInString(key) == 1 {
		seq = key
	}
	if alt && seq != "" {
		seq = "\x1b" + seq
	}
	return []byte(seq)
}
