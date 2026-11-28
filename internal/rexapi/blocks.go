package rexapi

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorex/internal/rex"
)

// blockMethods are the methods every GoRex block provides, by their short
// names, under the creator com.superlogical.terminal.
var blockMethods = map[string]func(*control, *mSession, *mBlock, args) (any, *apiError){
	"format":         blockFormat,
	"pwd":            blockPwd,
	"title":          blockTitle,
	"process":        blockProcess,
	"ports":          blockPorts,
	"program_status": blockProgramStatus,
	"modes":          blockModes,
	"clear":          blockClear,
	"reset":          blockReset,
	"input_mode":     blockInputMode,
	"list_dir":       blockListDir,
	"clipboard_read": blockClipboardRead,
	"blob_read":      blockBlobRead,
	"set_theme":      blockSetTheme,
	"resize":         blockResize,
	"size":           blockSize,
	"write":          blockWrite,
	"upload_begin":   blockUploadBegin,
	"download_begin": blockDownloadBegin,
}

// callBlockMethod runs a block method by its full name, such as
// com.superlogical.terminal.write, whose arguments carry session_id,
// block_id and the method's own under args.
func callBlockMethod(c *control, method string, a args) (any, *apiError) {
	i := strings.LastIndexByte(method, '.')
	if i <= 0 {
		return nil, errMethodNotFound("%s is not a block method", method)
	}
	creator, short := method[:i], method[i+1:]
	if creator == tunnelCreatorName {
		return runTunnelMethod(c, short, a)
	}
	if creator != creatorName {
		return nil, errConflict("GoRex makes no blocks of %s", creator)
	}
	return runBlockMethod(c, short, a, false)
}

// methodBlockMethod runs block.method, a block method by its short name.
func methodBlockMethod(c *control, a args) (any, *apiError) {
	short, aerr := a.reqStr("method")
	if aerr != nil {
		return nil, aerr
	}
	if strings.Contains(short, ".") {
		return nil, errBadRequest("block.method takes a method's short name")
	}
	return runBlockMethod(c, short, a, true)
}

// runBlockMethod reads the block a call names and runs the method on it.
func runBlockMethod(c *control, short string, a args, requireBlock bool) (any, *apiError) {
	for name := range a {
		switch name {
		case "session_id", "block_id", "args":
		case "method":
			if !requireBlock {
				return nil, errBadRequest("the method takes no method")
			}
		default:
			return nil, errBadRequest("the method takes no %s", name)
		}
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	id, present, aerr := a.str("block_id")
	if aerr != nil {
		return nil, aerr
	}
	if !present || id == "" {
		return nil, errBadRequest("block_id is required")
	}
	b := s.findBlock(id)
	if b == nil {
		if tb := c.g.tunnel(id); tb != nil && tb.session == s.id {
			argMap, _, aerr := a.obj("args")
			if aerr != nil {
				return nil, aerr
			}
			if argMap == nil {
				argMap = args{}
			}
			return callTunnelMethod(tb, short, argMap)
		}
		return nil, errNotFound("no block %s in %s", id, s.id)
	}
	argMap, _, aerr := a.obj("args")
	if aerr != nil {
		return nil, aerr
	}
	if argMap == nil {
		argMap = args{}
	}
	fn := blockMethods[short]
	if fn == nil {
		return nil, errMethodNotFound("%s.%s is not a method this block has", creatorName, short)
	}
	return fn(c, s, b, argMap)
}

func runTunnelMethod(c *control, short string, a args) (any, *apiError) {
	for name := range a {
		switch name {
		case "session_id", "block_id", "args":
		default:
			return nil, errBadRequest("the method takes no %s", name)
		}
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	id, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	tb := c.g.tunnel(id)
	if tb == nil || tb.session != s.id {
		return nil, errNotFound("no block %s in %s", id, s.id)
	}
	argMap, _, aerr := a.obj("args")
	if aerr != nil {
		return nil, aerr
	}
	if argMap == nil {
		argMap = args{}
	}
	return callTunnelMethod(tb, short, argMap)
}

func callTunnelMethod(tb *tunnelBlock, short string, a args) (any, *apiError) {
	if short != "info" {
		return nil, errMethodNotFound("%s.%s is not a method this block has", tunnelCreatorName, short)
	}
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	return tb.info(), nil
}

// sessionInfo returns the GoRex server's record of a block's session.
func (g *Gateway) sessionInfo(sid string) (*rex.SessionInfo, error) {
	list, err := g.opt.Client.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == sid {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("no session %s", sid)
}

// blockInfo returns the GoRex server's record of a block, failing with
// not_found when its session is gone.
func (g *Gateway) blockInfo(b *mBlock) (*rex.SessionInfo, *apiError) {
	info, err := g.sessionInfo(b.sid)
	if err != nil {
		return nil, errNotFound("block %s has no session", b.id)
	}
	return info, nil
}

// formatResult is com.superlogical.terminal.format's result.
type formatResult struct {
	Content string `json:"content"`
	ID      *int   `json:"id,omitempty"`
	Total   *int   `json:"total,omitempty"`
	EOF     *bool  `json:"eof,omitempty"`
}

// maxBlobBytes is the most a format or blob_read reply carries, once its
// data is a JSON string.
const (
	maxBlobBytes     = 983040
	defaultBlobBytes = 49152
)

// blockFormat returns the screen and scrollback as plain text, HTML or
// escape sequences.
func blockFormat(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	kind := "text"
	formatName, ok, aerr := a.str("format")
	if aerr != nil {
		return nil, aerr
	}
	if ok && formatName != "" {
		kind = formatName
	}
	switch kind {
	case "text", "html", "vt":
	default:
		return nil, errBadRequest("format is text, html or vt")
	}
	for _, name := range []string{"trim", "unwrap", "extra_charsets", "extra_cursor",
		"extra_hyperlink", "extra_keyboard", "extra_kitty_keyboard", "extra_modes",
		"extra_palette", "extra_protection", "extra_pwd", "extra_scrolling_region",
		"extra_style", "extra_tabstops"} {
		if _, _, aerr := a.boolean(name); aerr != nil {
			return nil, aerr
		}
	}
	maxBytes, set, aerr := a.int("max_bytes")
	if aerr != nil {
		return nil, aerr
	}
	if set && maxBytes < 0 {
		return nil, errBadRequest("max_bytes must not be negative")
	}
	wire := "text"
	if kind == "vt" {
		wire = "vt"
	}
	res, err := c.g.opt.Client.Format(b.sid, wire)
	if err != nil {
		return nil, errInternal("reading the screen: %v", err)
	}
	content := string(res.Content)
	if kind == "html" {
		content = renderHTML(content)
	}
	text := content
	out := formatResult{Content: text}
	if set && maxBytes > 0 {
		limit := min(maxBytes, maxBlobBytes)
		part, rest := cutJSONString(text, limit)
		total := jsonStringLen(text)
		out.Content = part
		out.Total = &total
		eof := rest == ""
		out.EOF = &eof
		if rest != "" {
			id := c.g.putBlob(text, total)
			out.ID = &id
		}
		return out, nil
	}
	if len(text) > maxMessage/2 {
		return nil, errTooLarge("the screen is larger than %d bytes; ask for max_bytes", maxMessage)
	}
	return out, nil
}

// renderHTML renders text as a simple preformatted page.
func renderHTML(text string) string {
	var b strings.Builder
	b.WriteString(`<pre style="white-space:pre-wrap;font-family:monospace">`)
	b.WriteString(html.EscapeString(text))
	b.WriteString("</pre>")
	return b.String()
}

// jsonStringLen returns the length of s once encoded as a JSON string.
func jsonStringLen(s string) int {
	b, _ := json.Marshal(s)
	return len(b)
}

// cutJSONString returns the longest part of s whose encoded length is at
// most limit, and the rest, cutting only between characters.
func cutJSONString(s string, limit int) (string, string) {
	if len(s)+3 <= limit {
		return s, ""
	}
	size := 2 // the quotes
	i := 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		e := n
		if n == 1 {
			switch {
			case r < 0x20, r == utf8.RuneError, r == '<', r == '>', r == '&':
				e = 6
			case r == '"', r == '\\':
				e = 2
			}
		}
		if size+e > limit {
			break
		}
		size += e
		i += n
	}
	return s[:i], s[i:]
}

// blockPwd returns the working directory a program reported.
func blockPwd(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	info, aerr := c.g.blockInfo(b)
	if aerr != nil {
		return nil, aerr
	}
	out := map[string]any{}
	if info.Dir != "" {
		out["pwd"] = info.Dir
		if owner := ownerOfInfo(info); owner != nil {
			out["owner"] = owner
		}
	}
	return out, nil
}

// processRef names a process, as pwd, title and program_status do.
type processRef struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// ownerOfInfo returns the foreground process, when there is one that
// differs from the shell.
func ownerOfInfo(info *rex.SessionInfo) *processRef {
	pid := info.Foreground
	if pid <= 0 {
		pid = info.PID
	}
	name := info.Program
	if name == "" {
		name = filepath.Base(info.Shell)
	}
	if name == "" {
		return nil
	}
	return &processRef{PID: pid, Name: name}
}

// blockTitle returns the terminal's title.
func blockTitle(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	info, aerr := c.g.blockInfo(b)
	if aerr != nil {
		return nil, aerr
	}
	out := map[string]any{"title": info.Title}
	return out, nil
}

// processInfo is one process of com.superlogical.terminal.process.
type processInfo struct {
	PID    int    `json:"pid"`
	UserID int    `json:"user_id"`
	User   string `json:"user,omitempty"`
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Argv0  string `json:"argv0,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

// lastExit is com.superlogical.terminal.process's last_exit.
type lastExit struct {
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// blockProcess returns the processes of the terminal and how its program
// exited.
func blockProcess(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	info, aerr := c.g.blockInfo(b)
	if aerr != nil {
		return nil, aerr
	}
	out := map[string]any{"last_exit": nil}
	if info.Exited {
		le := lastExit{ExitCode: info.ExitCode}
		if info.ExitCode != 0 {
			le.Error = fmt.Sprintf("exit status %d", info.ExitCode)
		}
		out["last_exit"] = le
	}
	// The program the session started: the shell it runs.
	child := processInfo{
		PID: info.PID, UserID: currentUID(), User: c.g.user,
		Name: filepath.Base(info.Shell), Path: info.Shell,
	}
	out["child"] = child
	// The process that has the terminal: a program the child runs, or the
	// child itself, which a shell at its prompt and a command session's
	// program both are.
	if info.Foreground > 0 {
		fg := processInfo{
			PID: info.Foreground, UserID: currentUID(), User: c.g.user,
			Name: info.Program, Cwd: info.Dir,
		}
		if len(info.Args) > 0 {
			fg.Argv0 = info.Args[0]
		}
		out["foreground"] = fg
	}
	if home, err := os.UserHomeDir(); err == nil {
		out["home"] = home
	}
	return out, nil
}

// statusRecord is one record of com.superlogical.terminal.program_status.
type statusRecord struct {
	ID           string      `json:"id,omitempty"`
	State        string      `json:"state"`
	Kind         string      `json:"kind,omitempty"`
	Progress     *int        `json:"progress,omitempty"`
	App          string      `json:"app,omitempty"`
	EffectiveApp string      `json:"effective_app,omitempty"`
	Title        string      `json:"title,omitempty"`
	Msg          string      `json:"msg,omitempty"`
	LastMsg      string      `json:"last_msg,omitempty"`
	UpdatedAt    string      `json:"updated_at"`
	Owner        *processRef `json:"owner,omitempty"`
}

// blockProgramStatus lists the status that programs in the terminal
// reported.
func blockProgramStatus(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	info, aerr := c.g.blockInfo(b)
	if aerr != nil {
		return nil, aerr
	}
	return statusPayload(info), nil
}

// statusRecords returns the status programs in the terminal reported, by
// record ID.
func statusRecords(info *rex.SessionInfo) []statusRecord {
	records := make([]statusRecord, 0, len(info.Statuses))
	byID := map[string]string{}
	for _, st := range info.Statuses {
		byID[st.ID] = st.App
	}
	owner := ownerOfInfo(info)
	for _, st := range info.Statuses {
		r := statusRecord{
			ID: st.ID, State: st.State, Kind: st.Kind, App: st.App,
			Title: st.Title, Msg: st.Msg, LastMsg: st.LastMsg, UpdatedAt: connectedAtString(st.Updated),
			EffectiveApp: effectiveApp(st.ID, byID),
		}
		if st.Progress >= 0 {
			p := st.Progress
			r.Progress = &p
		}
		if owner != nil {
			r.Owner = owner
		}
		records = append(records, r)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records
}

// effectiveApp returns the app of a record, or of its nearest ancestor
// that has one.
func effectiveApp(id string, byID map[string]string) string {
	for {
		if app := byID[id]; app != "" {
			return app
		}
		if id == "" {
			return ""
		}
		if i := strings.LastIndexByte(id, '/'); i >= 0 {
			id = id[:i]
		} else {
			id = ""
		}
	}
}

// modeView is one mode of com.superlogical.terminal.modes.
type modeView struct {
	Name    string `json:"name"`
	Number  int    `json:"number"`
	ANSI    bool   `json:"ansi,omitempty"`
	Set     bool   `json:"set"`
	Default bool   `json:"default,omitempty"`
}

// blockModes lists the modes the terminal's programs set.
func blockModes(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	st, err := c.g.opt.Client.Modes(b.sid)
	if err != nil {
		return nil, errNotFound("block %s has no terminal", b.id)
	}
	modes := make([]modeView, 0, len(st.Modes))
	for _, m := range st.Modes {
		modes = append(modes, modeView{Name: m.Name, Number: m.Number, ANSI: m.ANSI, Set: m.Set, Default: m.Default})
	}
	screen := st.Screen
	if screen != "alternate" {
		screen = "primary"
	}
	return map[string]any{"screen": screen, "modes": modes}, nil
}

// blockClear clears the screen and scrollback, keeping the current line
// at the top.
func blockClear(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	if err := c.g.opt.Client.ClearScreen(b.sid); err != nil {
		return nil, errInternal("clearing the screen: %v", err)
	}
	return struct{}{}, nil
}

// blockReset resets the terminal to its starting state.
func blockReset(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	if err := c.g.opt.Client.ResetTerminal(b.sid); err != nil {
		return nil, errInternal("resetting the terminal: %v", err)
	}
	return struct{}{}, nil
}

// blockInputMode reports how the terminal handles typed input.
func blockInputMode(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	mode, err := c.g.opt.Client.InputMode(b.sid)
	if err != nil {
		return nil, errNotFound("GoRex cannot tell how this terminal takes input: %v", err)
	}
	return map[string]any{
		"echo": mode.Echo, "canonical": mode.Canonical, "signals": mode.Signals,
	}, nil
}

// blockClipboardRead reads text a program copied to the clipboard. The
// GoRex server keeps no clipboard.
func blockClipboardRead(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if _, _, aerr := a.int("id"); aerr != nil {
		return nil, aerr
	}
	offset, _, aerr := a.int("offset")
	if aerr != nil {
		return nil, aerr
	}
	if offset < 0 {
		return nil, errBadRequest("offset must not be negative")
	}
	maxBytes, _, aerr := a.int("max_bytes")
	if aerr != nil {
		return nil, aerr
	}
	if maxBytes < 0 {
		return nil, errBadRequest("max_bytes must not be negative")
	}
	return nil, errNotFound("GoRex keeps no clipboard copies")
}

// blockBlobRead reads a result too large for one reply.
func blockBlobRead(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	id, _, aerr := a.int("id")
	if aerr != nil {
		return nil, aerr
	}
	offset, _, aerr := a.int("offset")
	if aerr != nil {
		return nil, aerr
	}
	if offset < 0 {
		return nil, errBadRequest("offset must not be negative")
	}
	maxBytes, set, aerr := a.int("max_bytes")
	if aerr != nil {
		return nil, aerr
	}
	if set && maxBytes < 0 {
		return nil, errBadRequest("max_bytes must not be negative")
	}
	if !set || maxBytes == 0 {
		maxBytes = defaultBlobBytes
	}
	maxBytes = min(maxBytes, maxBlobBytes)
	blob, ok := c.g.getBlob(id)
	if !ok {
		return nil, errNotFound("GoRex no longer has that result")
	}
	text := blob.text
	if offset > len(text) {
		offset = len(text)
	}
	part, rest := cutJSONString(text[offset:], maxBytes)
	id = blob.id
	return map[string]any{
		"id": id, "data": part, "total": blob.total, "eof": rest == "",
	}, nil
}

// blockResize requests a terminal size.
func blockResize(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	cols, ok, aerr := a.int("columns")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || cols <= 0 {
		return nil, errBadRequest("columns must be at least 1")
	}
	rows, ok, aerr := a.int("rows")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || rows <= 0 {
		return nil, errBadRequest("rows must be at least 1")
	}
	cellW, hasW, aerr := a.int("cell_width_px")
	if aerr != nil {
		return nil, aerr
	}
	cellH, hasH, aerr := a.int("cell_height_px")
	if aerr != nil {
		return nil, aerr
	}
	if hasW != hasH {
		return nil, errBadRequest("cell_width_px and cell_height_px go together")
	}
	if hasW && (cellW <= 0 || cellH <= 0) {
		return nil, errBadRequest("a cell is at least one pixel")
	}
	c.g.resizeBlock(c.rec, b, cols, rows)
	return struct{}{}, nil
}

// blockSize reports a terminal's size, its owner, and each client's
// requested size.
func blockSize(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	info, aerr := c.g.blockInfo(b)
	if aerr != nil {
		return nil, aerr
	}
	return c.g.blockSize(b.id, info), nil
}

// blockWrite types text into the terminal.
func blockWrite(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	data, aerr := a.reqStr("data")
	if aerr != nil {
		return nil, aerr
	}
	c.g.takeOnInput(c.rec, s, b)
	if err := c.g.opt.Client.InputHuman(b.sid, []byte(data)); err != nil {
		return nil, errInternal("typing: %v", err)
	}
	return map[string]any{"bytes_written": len(data)}, nil
}

// blob is a result too large for one reply.
type blob struct {
	id    int
	text  string
	total int
	at    time.Time
}

// blobTTL is how long a result stays readable.
const blobTTL = 5 * time.Minute

// putBlob stores a result, and returns the ID it got. text is what
// remains to read; total is the length of the whole result, encoded as a
// JSON string.
func (g *Gateway) putBlob(text string, total int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for id, b := range g.blobs {
		if now.Sub(b.at) > blobTTL {
			delete(g.blobs, id)
		}
	}
	g.blobsNext++
	id := g.blobsNext
	g.blobs[id] = blob{id: id, text: text, total: total, at: now}
	return id
}

// getBlob returns a result by ID, or the newest when ID is 0.
func (g *Gateway) getBlob(id int) (blob, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for k, b := range g.blobs {
		if now.Sub(b.at) > blobTTL {
			delete(g.blobs, k)
		}
	}
	if id != 0 {
		b, ok := g.blobs[id]
		return b, ok
	}
	var newest blob
	found := false
	for _, b := range g.blobs {
		if !found || b.id > newest.id {
			newest, found = b, true
		}
	}
	return newest, found
}
