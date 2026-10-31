package rexapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gorex/internal/rex"
)

// blockCall calls a block method and returns its result.
func blockCall(t *testing.T, c *testConn, sid, block, method string, args map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"session_id": sid, "block_id": block}
	if args != nil {
		payload["args"] = args
	}
	return callChecked(t, c, creatorName+"."+method, payload)
}

// blockFailure calls a block method and returns its error.
func blockFailure(t *testing.T, c *testConn, sid, block, method string, args map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"session_id": sid, "block_id": block}
	if args != nil {
		payload["args"] = args
	}
	return failure(t, c.request("bad-"+method, creatorName+"."+method, payload))
}

// TestTerminalQueries checks the block methods that read a terminal.
func TestTerminalQueries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	title := base64.StdEncoding.EncodeToString([]byte("my title"))
	msg := base64.StdEncoding.EncodeToString([]byte("building"))
	script := "printf 'marker\\n'; printf '\\033]0;My Title\\007'; printf '\\033]7501;state=working:progress=42:app=cargo:title=" + title + ":msg=" + msg + "\\033\\\\'; sleep 60"
	c, sid, block := f.sessionWith(t, rex.CreateOptions{
		Command: []string{"/bin/sh", "-c", script},
		Dir:     dir,
	})
	waitFor(t, "the title", func() bool {
		res := blockCall(t, c, sid, block, "title", nil)
		return res["title"] == "My Title"
	})
	// pwd is the foreground program's directory.
	waitFor(t, "pwd", func() bool {
		res := blockCall(t, c, sid, block, "pwd", nil)
		return res["pwd"] == dir
	})
	// format, as text, HTML and escape sequences.
	waitFor(t, "the marker", func() bool {
		return strings.Contains(blockCall(t, c, sid, block, "format", nil)["content"].(string), "marker")
	})
	htmlRes := blockCall(t, c, sid, block, "format", map[string]any{"format": "html"})
	if !strings.HasPrefix(htmlRes["content"].(string), "<pre") {
		t.Errorf("html: %q", htmlRes["content"])
	}
	vtRes := blockCall(t, c, sid, block, "format", map[string]any{"format": "vt"})
	if !strings.Contains(vtRes["content"].(string), "\x1b[") {
		t.Errorf("vt: %q", vtRes["content"])
	}
	if e := blockFailure(t, c, sid, block, "format", map[string]any{"format": "pdf"}); e["code"] != "bad_request" {
		t.Errorf("format pdf: %v", e)
	}
	// process: the shell and the program it runs.
	proc := blockCall(t, c, sid, block, "process", nil)
	child := proc["child"].(map[string]any)
	if child["pid"] == float64(0) || child["name"] == "" {
		t.Errorf("child: %v", child)
	}
	if proc["last_exit"] != nil {
		t.Errorf("last_exit while running: %v", proc["last_exit"])
	}
	if fg, ok := proc["foreground"].(map[string]any); ok {
		if fg["name"] == "" || fg["cwd"] != dir {
			t.Errorf("foreground: %v", fg)
		}
	}
	// program_status: the report the program sent.
	var records []any
	waitFor(t, "the status record", func() bool {
		res := blockCall(t, c, sid, block, "program_status", nil)
		records = res["records"].([]any)
		return len(records) > 0
	})
	r := records[0].(map[string]any)
	if r["state"] != "working" || r["app"] != "cargo" || r["title"] != "my title" ||
		r["msg"] != "building" || r["progress"] != float64(42) || r["updated_at"] == "" {
		t.Errorf("status record: %v", r)
	}
	// input_mode: a shell's pty.
	mode := blockCall(t, c, sid, block, "input_mode", nil)
	if mode["echo"] != true || mode["canonical"] != true || mode["signals"] != true {
		t.Errorf("input mode: %v", mode)
	}
	// modes: a new terminal's defaults.
	modes := blockCall(t, c, sid, block, "modes", nil)
	if modes["screen"] != "primary" {
		t.Errorf("screen: %v", modes["screen"])
	}
	autowrap := false
	for _, raw := range modes["modes"].([]any) {
		m := raw.(map[string]any)
		if m["name"] == "autowrap" {
			autowrap = m["set"] == true
		}
	}
	if !autowrap {
		t.Errorf("modes: %v", modes["modes"])
	}
	// list_dir: a directory this program would see.
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(sub, 0o700)
	os.WriteFile(filepath.Join(sub, "Notes.TXT"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(sub, "data.bin"), []byte("x"), 0o600)
	list := blockCall(t, c, sid, block, "list_dir", map[string]any{
		"path": "sub", "prefix": "n", "files": true,
	})
	if list["path"] != sub || list["status"] != "ok" || list["home"] == "" {
		t.Errorf("list_dir: %v", list)
	}
	entries := list["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["name"] != "Notes.TXT" ||
		entries[0].(map[string]any)["file"] != true {
		t.Errorf("list_dir entries: %v", entries)
	}
	// clear takes the output above the cursor's line away.
	blockCall(t, c, sid, block, "clear", nil)
	waitFor(t, "the cleared screen", func() bool {
		return !strings.Contains(blockCall(t, c, sid, block, "format", nil)["content"].(string), "marker")
	})
	// reset takes everything.
	blockCall(t, c, sid, block, "reset", nil)
	waitFor(t, "the reset screen", func() bool {
		return blockCall(t, c, sid, block, "format", nil)["content"].(string) == ""
	})
	// clipboard_read: GoRex keeps no copies.
	if e := blockFailure(t, c, sid, block, "clipboard_read", nil); e["code"] != "not_found" {
		t.Errorf("clipboard_read: %v", e)
	}
	// A method the block does not have.
	if e := failure(t, c.request("nomethod", "block.method", map[string]any{
		"session_id": sid, "block_id": block, "method": "nope",
	})); e["code"] != "method_not_found" {
		t.Errorf("block.method nope: %v", e)
	}
}

// TestTerminalFormatBlobs: a format reply that does not fit is continued
// through blob_read.
func TestTerminalFormatBlobs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	script := "i=0; while [ $i -lt 80 ]; do echo line-$i; i=$((i+1)); done; sleep 60"
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	waitFor(t, "the output", func() bool {
		return strings.Contains(blockCall(t, c, sid, block, "format", nil)["content"].(string), "line-79")
	})
	whole := blockCall(t, c, sid, block, "format", nil)["content"].(string)
	res := blockCall(t, c, sid, block, "format", map[string]any{"max_bytes": 64})
	if res["eof"] != false || res["total"] == nil || res["id"] == nil {
		t.Fatalf("part: %v", res)
	}
	if total := int(res["total"].(float64)); total != jsonStringLen(whole) {
		t.Errorf("total %d, want %d", total, jsonStringLen(whole))
	}
	got := res["content"].(string)
	if got == "" || len(got) >= len(whole) {
		t.Fatalf("first part %d bytes of %d", len(got), len(whole))
	}
	id := int(res["id"].(float64))
	offset := len(got)
	for i := 0; i < 100; i++ {
		part := blockCall(t, c, sid, block, "blob_read", map[string]any{
			"id": id, "offset": offset,
		})
		if int(part["id"].(float64)) != id || int(part["total"].(float64)) != jsonStringLen(whole) {
			t.Fatalf("blob %v", part)
		}
		got += part["data"].(string)
		offset += len(part["data"].(string))
		if part["eof"] == true {
			break
		}
	}
	if got != whole {
		t.Errorf("read %d bytes of %d", len(got), len(whole))
	}
	// The newest result is read without an ID.
	if e := blockFailure(t, c, sid, block, "blob_read", map[string]any{"offset": -1}); e["code"] != "bad_request" {
		t.Errorf("negative offset: %v", e)
	}
	// Unknown IDs are not found.
	if e := blockFailure(t, c, sid, block, "blob_read", map[string]any{"id": 99999}); e["code"] != "not_found" {
		t.Errorf("unknown blob: %v", e)
	}
}

// TestTerminalTheme: set_theme sends a theme message on the block's data
// stream.
func TestTerminalTheme(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}})
	callChecked(t, c, "com.superlogical.terminal.set_theme", map[string]any{
		"session_id": sid, "block_id": block,
		"args": map[string]any{
			"foreground": "#d6deeb", "background": "011627", "cursor": "#80a4c2",
			"palette": []string{"1=#ef5350"}, "scheme": "dark",
		},
	})
	ticket := dataTicket(t, c, sid, block)
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	_, nonce := dataHello(t, ws, ticket)
	got := readUntil(t, ws, `"type":"theme"`)
	marker := "\x1b_rex1;" + strconv.FormatUint(nonce, 10) + ";"
	i := bytes.LastIndex(got[:bytes.Index(got, []byte(`"type":"theme"`))], []byte(marker))
	if i < 0 {
		t.Fatalf("no theme message: %q", got)
	}
	var msg map[string]any
	rest := got[i+len(marker):]
	end := bytes.Index(rest, []byte("\x1b\\"))
	if end < 0 {
		t.Fatalf("unfinished theme: %q", rest)
	}
	if err := json.Unmarshal(rest[:end], &msg); err != nil {
		t.Fatalf("theme %q: %v", rest[:end], err)
	}
	if msg["type"] != "theme" || msg["foreground"] != "#d6deeb" || msg["background"] != "011627" ||
		msg["cursor"] != "#80a4c2" || msg["scheme"] != "dark" {
		t.Errorf("theme: %v", msg)
	}
	// A color that is not one is refused.
	if e := blockFailure(t, c, sid, block, "set_theme", map[string]any{"foreground": "blue"}); e["code"] != "bad_request" {
		t.Errorf("bad color: %v", e)
	}
	if e := blockFailure(t, c, sid, block, "set_theme", map[string]any{"palette": []string{"300=#ffffff"}}); e["code"] != "bad_request" {
		t.Errorf("bad palette index: %v", e)
	}
}

// TestTerminalEndedSession: a block whose program ended still answers the
// queries of what it showed.
func TestTerminalEndedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: exitCommand("gone")})
	waitFor(t, "the program's end", func() bool {
		res := blockCall(t, c, sid, block, "process", nil)
		return res["last_exit"] != nil
	})
	if !strings.Contains(blockCall(t, c, sid, block, "format", nil)["content"].(string), "gone") {
		t.Error("the ended session's screen is gone")
	}
	last := blockCall(t, c, sid, block, "process", nil)["last_exit"].(map[string]any)
	if last["exit_code"] != float64(0) {
		t.Errorf("last_exit: %v", last)
	}
}
