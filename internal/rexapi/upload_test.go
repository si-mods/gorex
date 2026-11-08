package rexapi

import (
	"bytes"
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
