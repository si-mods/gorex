//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

func TestGraphicsRewritesFileImage(t *testing.T) {
	dir := t.TempDir()
	data := append(append([]byte(nil), pngHeader...), []byte(strings.Repeat("x", 5000))...)
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	seq := "\x1b_Gi=1,a=T,t=f;image.png\x1b\\"
	var g graphics
	got, recs := g.filter([]byte(seq), dir)
	g.record(recs)
	if bytes.Contains(got, []byte("t=f")) || !bytes.Contains(got, []byte("t=d")) || !bytes.Contains(got, []byte("f=100")) {
		t.Fatalf("rewritten: %q", got)
	}
	if got := decodeChunks(t, got); !bytes.Equal(got, data) {
		t.Fatalf("decoded %d bytes, want %d", len(got), len(data))
	}
	if len(g.history) < 1 || len(g.replay()) == 0 {
		t.Fatalf("history %d", len(g.history))
	}
}

func TestGraphicsLeavesInBandAlone(t *testing.T) {
	in := []byte("before\x1b_Gi=2,a=T,t=d;AAAA\x1b\\after")
	var g graphics
	if got, recs := g.filter(in, ""); !bytes.Equal(got, in) {
		t.Fatalf("changed: %q", got)
	} else {
		g.record(recs)
	}
	if !bytes.Equal(g.replay(), in[6:len(in)-5]) {
		t.Fatalf("replay %q", g.replay())
	}
}

func TestGraphicsKeepsSplitSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	data := append(append([]byte(nil), pngHeader...), []byte("body")...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	seq := []byte("x\x1b_Gi=3,a=T,t=f;a.png\x1b\\y")
	var g graphics
	var out []byte
	for _, p := range [][]byte{seq[:4], seq[4:20], seq[20:]} {
		part, recs := g.filter(p, dir)
		out = append(out, part...)
		g.record(recs)
	}
	if bytes.Contains(out, []byte("t=f")) || out[0] != 'x' || out[len(out)-1] != 'y' {
		t.Fatalf("split output: %q", out)
	}
	if !bytes.Equal(decodeChunks(t, out[1:len(out)-1]), data) {
		t.Fatal("split image lost")
	}
}

func TestGraphicsMissingFileLeavesSequence(t *testing.T) {
	seq := []byte("\x1b_Gi=4,a=T,t=f;missing.png\x1b\\")
	var g graphics
	if got, _ := g.filter(seq, t.TempDir()); !bytes.Equal(got, seq) {
		t.Fatalf("missing file changed: %q", got)
	}
	if len(g.history) != 0 {
		t.Fatal("missing file recorded")
	}
}

func TestGraphicsFastPath(t *testing.T) {
	var g graphics
	in := []byte("plain output\n")
	if got, recs := g.filter(in, ""); !bytes.Equal(got, in) || len(recs) != 0 {
		t.Fatal("plain output changed")
	}
}

// decodeChunks returns the pixels of the image chunks in out.
func decodeChunks(t *testing.T, out []byte) []byte {
	t.Helper()
	var pixels []byte
	for len(out) > 0 {
		i := bytes.Index(out, apcGraphicsStart)
		if i < 0 {
			break
		}
		j := bytes.Index(out[i:], apcEnd)
		if j < 0 {
			break
		}
		body := out[i+len(apcGraphicsStart) : i+j]
		_, payload, ok := bytes.Cut(body, []byte(";"))
		if !ok {
			t.Fatalf("no payload in %q", body)
		}
		p, err := base64.StdEncoding.DecodeString(string(payload))
		if err != nil {
			t.Fatal(err)
		}
		pixels = append(pixels, p...)
		out = out[i+j+len(apcEnd):]
	}
	return pixels
}

func TestSessionRewritesFileImage(t *testing.T) {
	dir := t.TempDir()
	data := append(append([]byte(nil), pngHeader...), []byte("pixels")...)
	if err := os.WriteFile(filepath.Join(dir, "image.png"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newSession("gfx", CreateOptions{
		Command: []string{"/bin/sh", "-c", "printf '\\033_Gi=9,a=T,t=f;image.png\\033\\\\'; sleep 0.1"},
		Dir:     dir, Cols: 40, Rows: 6,
	}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.kill)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
	s.mu.Lock()
	history := s.graphics.replay()
	s.mu.Unlock()
	if bytes.Contains(history, []byte("t=f")) || !bytes.Contains(history, []byte("t=d")) || len(history) == 0 {
		t.Fatalf("session history: %q", history)
	}
}

func TestSessionViewReplaysImages(t *testing.T) {
	dir := t.TempDir()
	data := append(append([]byte(nil), pngHeader...), []byte("pixels")...)
	if err := os.WriteFile(filepath.Join(dir, "image.png"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	term, err := terminal.New(terminal.Options{Conn: client})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	term.Resize(40, 6)
	var g graphics
	rewritten, recs := g.filter([]byte("\x1b_Gi=11,a=T,t=f;image.png\x1b\\"), dir)
	g.record(recs)
	if bytes.Contains(rewritten, []byte("t=f")) {
		t.Fatal("not rewritten")
	}
	term.Feed(rewritten)
	s := &session{vt: term, graphics: g, cols: 40, rows: 6}
	snap, _ := s.view()
	if !bytes.Contains(snap, rewritten) {
		t.Fatalf("snapshot lost image: %q", snap)
	}
}

func TestGraphicsGroupsInBandChunks(t *testing.T) {
	var g graphics
	first := []byte("\x1b_Gi=12,a=T,t=d,m=1;AAAA\x1b\\")
	second := []byte("\x1b_Gm=0;BBBB\x1b\\")
	out, recs := g.filter(first, "")
	if !bytes.Equal(out, first) || len(recs) != 0 {
		t.Fatalf("first %q recs %d", out, len(recs))
	}
	out, recs = g.filter(second, "")
	if !bytes.Equal(out, second) || len(recs) != 1 || !bytes.Equal(recs[0], append(first, second...)) {
		t.Fatalf("second %q recs %q", out, recs)
	}
	g.record(recs)
	if !bytes.Equal(g.replay(), append(first, second...)) {
		t.Fatal("replay lost the chunk group")
	}
}

func TestGraphicsHistoryCopies(t *testing.T) {
	seq := []byte("\x1b_Gi=13,a=d\x1b\\")
	var g graphics
	g.record([][]byte{seq})
	for i := range seq {
		seq[i] = 'x'
	}
	if !bytes.Equal(g.replay(), []byte("\x1b_Gi=13,a=d\x1b\\")) {
		t.Fatal("history aliases its source")
	}
}

func TestGraphicsLeavesOversizeFile(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("x"), maxGraphicsImage+1)
	if err := os.WriteFile(filepath.Join(dir, "big.png"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	seq := []byte("\x1b_Gi=14,a=T,t=f;big.png\x1b\\")
	var g graphics
	out, recs := g.filter(seq, dir)
	if !bytes.Equal(out, seq) || len(recs) != 0 {
		t.Fatal("rewrote an oversize file")
	}
}
