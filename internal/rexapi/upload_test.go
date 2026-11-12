package rexapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// postUpload posts body to /api/upload for block as name, with the type
// typ, and returns the status and the decoded answer.
func postUpload(t *testing.T, f *fixture, block, name, typ string, body []byte) (int, map[string]string) {
	t.Helper()
	q := url.Values{"block_id": {block}, "name": {name}}
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/upload?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", typ)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("not JSON: %s %s", resp.Status, data)
	}
	return resp.StatusCode, m
}

// TestUpload: the welcome lists the upload feature, and a file posted for
// a block lands in the server user's temporary folder, a second of the
// same name beside it, with its path answered as a word of the block's
// shell.
func TestUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the session runs /bin/sh")
	}
	f := startFixture(t)
	c, _, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-i"}})
	features, _ := c.welcome["features"].([]any)
	if !slices.Contains(features, any(featureUpload)) {
		t.Fatalf("welcome features: %v", c.welcome["features"])
	}
	body := bytes.Repeat([]byte("photo bytes "), 100_000) // over a megabyte
	var paths []string
	for range 2 {
		status, res := postUpload(t, f, block, "IMG 0001.HEIC", "application/octet-stream", body)
		if status != http.StatusOK {
			t.Fatalf("upload: %d %v", status, res)
		}
		path := res["path"]
		t.Cleanup(func() { os.Remove(path) })
		if !filepath.IsAbs(path) || !strings.HasPrefix(filepath.Base(path), "IMG 0001") {
			t.Fatalf("path %q", path)
		}
		if res["word"] != "'"+path+"'" {
			t.Errorf("word %q for %q", res["word"], path)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("the file there: %d bytes, %v", len(got), err)
		}
		paths = append(paths, path)
	}
	if paths[0] == paths[1] || filepath.Dir(paths[0]) != filepath.Dir(paths[1]) {
		t.Errorf("two uploads of a name: %v", paths)
	}
}

// uploadBegin calls com.superlogical.terminal.upload_begin for block.
// It does not check the result against the help document, whose snapshot
// predates the method.
func uploadBegin(t *testing.T, c *testConn, session, block string, m map[string]any) map[string]any {
	t.Helper()
	return result(t, c.request("upload_begin", "com.superlogical.terminal.upload_begin", map[string]any{
		"session_id": session, "block_id": block, "args": m,
	}))
}

// newUploadRef makes a ref: upload: followed by a new UUID.
func newUploadRef(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return "upload:" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// uploadHelloBytes opens a rex.data.bytes.v1 stream for the ticket the
// reservation got, and returns the WELCOME's nonce.
func uploadHelloBytes(t *testing.T, ws *websocket.Conn, res map[string]any) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(res["ticket"].(string))
	if err != nil || len(raw) != 16 {
		t.Fatalf("ticket %q: %v", res["ticket"], err)
	}
	_, nonce := dataHello(t, ws, raw)
	_ = nonce
	return raw
}

// expectCloseOk reads the stream to its end, which the terminal closes
// with status when the batch's paths were typed or the upload failed.
func expectUploadClose(t *testing.T, ws *websocket.Conn, status websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if got := websocket.CloseStatus(err); got != status {
				t.Fatalf("stream closed %v, want %v", got, status)
			}
			return
		}
		if ctx.Err() != nil {
			t.Fatal("the stream never closed")
		}
	}
}

// captureOf reads a session's screen as text, with its newlines taken
// out, since a wrapped line breaks a long name across rows.
func captureOf(t *testing.T, f *fixture, sid string) string {
	t.Helper()
	res, err := f.desk.Capture(sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(string(res.Screen))
}

// TestUploadBegin: revision 10's clients do not know the method, a
// reservation answers a ticket, and the arguments Rex documents fail
// with the codes it names.
func TestUploadBegin(t *testing.T) {
	f := startFixture(t)
	session, block, _ := inputSession(t, f)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})

	ref := newUploadRef(t)
	res := uploadBegin(t, c, session, block, map[string]any{"ref": ref, "name": "notes.txt", "size": 3})
	raw, err := base64.RawURLEncoding.DecodeString(res["ticket"].(string))
	if err != nil || len(raw) != 16 {
		t.Fatalf("ticket %q: %v", res["ticket"], err)
	}
	if res["expires_in_ms"] != float64(60000) {
		t.Errorf("expires_in_ms %v", res["expires_in_ms"])
	}

	t.Run("revision 10 does not know it", func(t *testing.T) {
		if methodAvailable(10, "com.superlogical.terminal.upload_begin") {
			t.Fatal("revision 10 knows upload_begin")
		}
		old := f.dialControl()
		old.hello(10)
		e := failure(t, old.request("old", "com.superlogical.terminal.upload_begin", map[string]any{
			"session_id": session, "block_id": block,
			"args": map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": 1},
		}))
		if e["code"] != "method_not_found" {
			t.Errorf("revision 10 calling upload_begin: %v", e)
		}
	})

	cases := []struct {
		name string
		args map[string]any
		code string
	}{
		{"a malformed ref", map[string]any{"ref": "upload:nope", "name": "a.txt", "size": 1}, "bad_request"},
		{"a path as the name", map[string]any{"ref": newUploadRef(t), "name": "a/b.txt", "size": 1}, "bad_request"},
		{"no size", map[string]any{"ref": newUploadRef(t), "name": "a.txt"}, "bad_request"},
		{"a negative size", map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": -1}, "bad_request"},
		{"an index without a batch", map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": 1, "index": 0}, "bad_request"},
		{"a count without a batch", map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": 1, "count": 1}, "bad_request"},
		{"a batch without a count", map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": 1, "batch": "b1"}, "bad_request"},
		{"an index outside the batch", map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": 1, "batch": "b2", "index": 2, "count": 2}, "bad_request"},
		{"too many files", map[string]any{"ref": newUploadRef(t), "name": "a.txt", "size": 1, "batch": "b3", "index": 0, "count": maxUploadBatchFiles + 1}, "bad_request"},
		{"a name of more than 255 bytes", map[string]any{"ref": newUploadRef(t), "name": strings.Repeat("a", 252) + ".txt", "size": 1}, "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := failure(t, c.request("bad", "com.superlogical.terminal.upload_begin", map[string]any{
				"session_id": session, "block_id": block, "args": tc.args,
			}))
			if e["code"] != tc.code {
				t.Errorf("code %v, want %s (%v)", e["code"], tc.code, e)
			}
		})
	}

	t.Run("a colon is refused on Windows only", func(t *testing.T) {
		// A ":" names a stream of a file on Windows, where the server
		// refuses it; elsewhere it is a file name's character.
		args := map[string]any{"ref": newUploadRef(t), "name": "a:b.txt", "size": 1}
		if runtime.GOOS != "windows" {
			uploadBegin(t, c, session, block, args)
			return
		}
		e := failure(t, c.request("colon", "com.superlogical.terminal.upload_begin", map[string]any{
			"session_id": session, "block_id": block, "args": args,
		}))
		if e["code"] != "bad_request" {
			t.Errorf("code %v (%v)", e["code"], e)
		}
	})

	t.Run("an index twice is conflict", func(t *testing.T) {
		// The first file of the batch takes its place; a second file of
		// the same place is refused.
		uploadBegin(t, c, session, block, map[string]any{"ref": newUploadRef(t), "name": "b.txt", "size": 1, "batch": "dup", "index": 0, "count": 2})
		e := failure(t, c.request("dup", "com.superlogical.terminal.upload_begin", map[string]any{
			"session_id": session, "block_id": block,
			"args": map[string]any{"ref": newUploadRef(t), "name": "b.txt", "size": 1, "batch": "dup", "index": 0, "count": 2},
		}))
		if e["code"] != "conflict" {
			t.Fatalf("code %v (%v)", e["code"], e)
		}
	})

	t.Run("the same ref twice is conflict", func(t *testing.T) {
		if e := failure(t, c.request("dupref", "com.superlogical.terminal.upload_begin", map[string]any{
			"session_id": session, "block_id": block,
			"args": map[string]any{"ref": ref, "name": "c.txt", "size": 1},
		})); e["code"] != "conflict" {
			t.Errorf("code %v (%v)", e["code"], e)
		}
	})
}

// TestUploadStream: the file the stream carries lands in the server
// user's temporary folder, whole, and its path is typed into the shell.
func TestUploadStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the session runs /bin/sh")
	}
	f := startFixture(t)
	session, block, sid := inputSession(t, f)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})

	body := bytes.Repeat([]byte("upload bytes "), 100_000) // over a megabyte
	res := uploadBegin(t, c, session, block, map[string]any{
		"ref": newUploadRef(t), "name": "IMG 0001.HEIC", "size": len(body),
	})
	ws := f.dial("/api/data", bytesProtocol)
	ws.SetReadLimit(maxMessage)
	uploadHelloBytes(t, ws, res)
	for at := 0; at < len(body); at += 64 << 10 {
		end := min(at+64<<10, len(body))
		wsWrite(t, ws, websocket.MessageBinary, body[at:end])
	}
	// An empty message is the half-close that ends the file.
	wsWrite(t, ws, websocket.MessageBinary, nil)
	expectUploadClose(t, ws, websocket.StatusNormalClosure)

	// The terminal typed the path, which the shell echoes.
	deadline := time.Now().Add(15 * time.Second)
	screen := ""
	for !strings.Contains(screen, "IMG 0001") {
		if time.Now().After(deadline) {
			t.Fatalf("no path on the screen: %q", screen)
		}
		screen = captureOf(t, f, sid)
		time.Sleep(50 * time.Millisecond)
	}
}

// TestUploadBatch: a batch's paths are typed together, once every file
// has arrived, and a file that over-sends aborts the batch.
func TestUploadBatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the session runs /bin/sh")
	}
	f := startFixture(t)
	session, block, sid := inputSession(t, f)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})

	batch := "batch-" + hex.EncodeToString(randomBytes(4))
	files := []struct {
		name string
		body []byte
	}{{"first-" + batch + ".txt", []byte("first file")}, {"second-" + batch + ".txt", []byte("second file")}}
	for i, file := range files {
		res := uploadBegin(t, c, session, block, map[string]any{
			"ref": newUploadRef(t), "name": file.name, "size": len(file.body),
			"batch": batch, "index": i, "count": len(files),
		})
		ws := f.dial("/api/data", bytesProtocol)
		ws.SetReadLimit(maxMessage)
		uploadHelloBytes(t, ws, res)
		wsWrite(t, ws, websocket.MessageBinary, file.body)
		wsWrite(t, ws, websocket.MessageBinary, nil)
		expectUploadClose(t, ws, websocket.StatusNormalClosure)
	}
	deadline := time.Now().Add(15 * time.Second)
	screen := ""
	for {
		if strings.Contains(screen, files[0].name) && strings.Contains(screen, files[1].name) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the batch's paths are not on the screen: %q", screen)
		}
		screen = captureOf(t, f, sid)
		time.Sleep(50 * time.Millisecond)
	}

	// Over-sending fails the stream and aborts the batch, so a later
	// file of it is refused with conflict.
	res := uploadBegin(t, c, session, block, map[string]any{
		"ref": newUploadRef(t), "name": "third.txt", "size": 5,
		"batch": "batch-2", "index": 0, "count": 2,
	})
	ws := f.dial("/api/data", bytesProtocol)
	ws.SetReadLimit(maxMessage)
	uploadHelloBytes(t, ws, res)
	wsWrite(t, ws, websocket.MessageBinary, []byte("more bytes than its size"))
	expectUploadClose(t, ws, websocket.StatusPolicyViolation)
	e := failure(t, c.request("abort", "com.superlogical.terminal.upload_begin", map[string]any{
		"session_id": session, "block_id": block,
		"args": map[string]any{"ref": newUploadRef(t), "name": "fourth.txt", "size": 1, "batch": "batch-2", "index": 1, "count": 2},
	}))
	if e["code"] != "conflict" {
		t.Errorf("the aborted batch: %v", e)
	}
}

// TestUploadDisconnect: a client that reserves and goes away leaves
// nothing behind — the reservation and its batch are gone, and another
// client may take the ref.
func TestUploadDisconnect(t *testing.T) {
	f := startFixture(t)
	session, block, _ := inputSession(t, f)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})

	ref := newUploadRef(t)
	batch := "gone-" + hex.EncodeToString(randomBytes(4))
	uploadBegin(t, c, session, block, map[string]any{
		"ref": ref, "name": "gone.txt", "size": 1, "batch": batch, "index": 0, "count": 2,
	})
	// The client goes away without opening the stream.
	c.ws.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		g := f.g
		g.mu.Lock()
		_, live := g.uploads[ref]
		g.mu.Unlock()
		if !live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reservation outlived the client")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Another client takes the ref, and a fresh batch, without conflict.
	c2 := f.dialControl()
	c2.hello(apiRevision)
	callChecked(t, c2, "session.attach", map[string]any{"session_id": session})
	uploadBegin(t, c2, session, block, map[string]any{"ref": ref, "name": "gone.txt", "size": 1})
	e := failure(t, c2.request("aborted", "com.superlogical.terminal.upload_begin", map[string]any{
		"session_id": session, "block_id": block,
		"args": map[string]any{"ref": newUploadRef(t), "name": "b.txt", "size": 1, "batch": batch, "index": 1, "count": 2},
	}))
	if e["code"] != "conflict" {
		t.Errorf("the dropped batch: %v", e)
	}
}

// TestUploadRefused: an upload that is not a POST of
// application/octet-stream, or names no block, is refused.
func TestUploadRefused(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	if status, _ := postUpload(t, f, blockID("s1"), "a.txt", "text/plain", []byte("x")); status != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", status)
	}
	if status, res := postUpload(t, f, "block:nope", "a.txt", "application/octet-stream", []byte("x")); status != http.StatusNotFound || res["error"] == "" {
		t.Errorf("no block: %d %v", status, res)
	}
	if status, _ := postUpload(t, f, blockID("s1"), "", "application/octet-stream", []byte("x")); status != http.StatusBadRequest {
		t.Errorf("no name: %d", status)
	}
	resp, err := http.Get(f.srv.URL + "/api/upload")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %s", resp.Status)
	}
}

// TestUploadForShell posts files for a block whose shell is cmd.exe (a
// program of its name): a name it cannot take is changed so its path
// pastes; a temporary folder whose path it cannot take keeps the file,
// answered with no word and why.
func TestUploadForShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the session runs /bin/sh")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	cmd := filepath.Join(t.TempDir(), "cmd")
	if err := os.Symlink("/bin/sh", cmd); err != nil {
		t.Fatal(err)
	}
	f := startFixture(t)
	_, _, block := f.sessionWith(t, rex.CreateOptions{Command: []string{cmd, "-i"}})
	status, res := postUpload(t, f, block, "50%!.txt", "application/octet-stream", []byte("x"))
	if status != http.StatusOK || filepath.Base(res["path"]) != "50__.txt" || res["word"] == "" || res["reason"] != "" {
		t.Fatalf("upload: %d %v", status, res)
	}

	odd := filepath.Join(tmp, "a%b")
	os.Mkdir(odd, 0o755)
	t.Setenv("TMPDIR", odd)
	status, res = postUpload(t, f, block, "c.txt", "application/octet-stream", []byte("x"))
	if status != http.StatusOK || res["word"] != "" || !strings.Contains(res["reason"], "can't take this path") {
		t.Fatalf("upload into a folder the shell refuses: %d %v", status, res)
	}
	if _, err := os.Stat(res["path"]); err != nil {
		t.Errorf("the file there: %v", err)
	}
}
