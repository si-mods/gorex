//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	got := g.filter([]byte(seq), dir)
	if bytes.Contains(got, []byte("t=f")) || !bytes.Contains(got, []byte("t=d")) || !bytes.Contains(got, []byte("f=100")) {
		t.Fatalf("rewritten: %q", got)
	}
	if got := decodeChunks(t, got); !bytes.Equal(got, data) {
		t.Fatalf("decoded %d bytes, want %d", len(got), len(data))
	}
}

func TestGraphicsLeavesInBandAlone(t *testing.T) {
	in := []byte("before\x1b_Gi=2,a=T,t=d;AAAA\x1b\\after")
	var g graphics
	if got := g.filter(in, ""); !bytes.Equal(got, in) {
		t.Fatalf("changed: %q", got)
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
		out = append(out, g.filter(p, dir)...)
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
	if got := g.filter(seq, t.TempDir()); !bytes.Equal(got, seq) {
		t.Fatalf("missing file changed: %q", got)
	}
}

func TestGraphicsFastPath(t *testing.T) {
	var g graphics
	in := []byte("plain output\n")
	if got := g.filter(in, ""); !bytes.Equal(got, in) {
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

// kittyRGBA returns the Kitty graphics sequence that transmits a w×h
// image of one byte value, as RGBA, with more keys.
func kittyRGBA(id, w, h int, keys string) string {
	px := bytes.Repeat([]byte{byte(id), 0x40, 0x80, 0xff}, w*h)
	return fmt.Sprintf("\x1b_Ga=T,f=32,s=%d,v=%d,i=%d,q=2%s;%s\x1b\\", w, h, id, keys, base64.StdEncoding.EncodeToString(px))
}

// transmits counts the sequences in b that start sending image id's
// pixels.
func transmits(b []byte, id int) int {
	n := 0
	for {
		i := bytes.Index(b, apcGraphicsStart)
		if i < 0 {
			return n
		}
		b = b[i+len(apcGraphicsStart):]
		header, _, _ := strings.Cut(string(b[:max(bytes.Index(b, apcEnd), 0)]), ";")
		keys := "," + header + ","
		if (strings.Contains(keys, ",a=t,") || strings.Contains(keys, ",a=T,")) && strings.Contains(keys, fmt.Sprintf(",i=%d,", id)) {
			n++
		}
	}
}

// tinyPNG returns a PNG of 4×4 gray pixels.
func tinyPNG() []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 4, 4)))
	return b.Bytes()
}

// TestSessionRewritesFileImage: an image a program names by its file
// reaches the session's emulator, which the attaches take it from.
func TestSessionRewritesFileImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.png"), tinyPNG(), 0o600); err != nil {
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
	screen, images := s.screen, s.images
	s.mu.Unlock()
	if transmits(images, 9) != 1 || !bytes.HasSuffix(screen, images) {
		t.Fatalf("the exited session's images %q, its screen ends %q", images, screen[max(len(screen)-40, 0):])
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
	if out := g.filter(seq, dir); !bytes.Equal(out, seq) {
		t.Fatal("rewrote an oversize file")
	}
}
