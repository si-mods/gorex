//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/base64"
	"fmt"
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
	if !bytes.Equal(out, second) || len(recs) != 1 || !bytes.Equal(recs[0].seq, append(first, second...)) {
		t.Fatalf("second %q recs %+v", out, recs)
	}
	g.record(recs)
	if !bytes.Equal(g.replay(), append(first, second...)) {
		t.Fatal("replay lost the chunk group")
	}
}

func TestGraphicsHistoryCopies(t *testing.T) {
	seq := []byte("\x1b_Gi=13,a=d\x1b\\")
	var g graphics
	g.restore([][]byte{seq})
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

// TestGraphicsRecordsNoOrphanChunks: the rest of a transfer whose start
// went before the session read, as across an upgrade, is no image to
// replay; the rest of one the session read the start of stays as it was.
func TestGraphicsRecordsNoOrphanChunks(t *testing.T) {
	g := graphics{cut: true}
	out, recs := g.filter([]byte("\x1b_Gm=1;AAAA\x1b\\\x1b_Gm=0;AAAA\x1b\\"), "")
	if len(recs) != 0 || !bytes.Contains(out, []byte("m=0;AAAA")) {
		t.Fatalf("recorded %+v, passed %q", recs, out)
	}
	_, recs = g.filter([]byte("\x1b_Ga=T,f=100,m=1;AAAA\x1b\\\x1b_Gm=0;AAAA\x1b\\"), "")
	if len(recs) != 1 || !bytes.HasPrefix(recs[0].seq, []byte("\x1b_Ga=T")) {
		t.Fatalf("a whole transfer recorded %+v", recs)
	}

	// A file image's first chunk is rewritten whole; its last chunk is
	// recorded after it, as without a cut.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.png"), append(append([]byte(nil), pngHeader...), "pixels"...), 0o600); err != nil {
		t.Fatal(err)
	}
	g = graphics{}
	_, recs = g.filter([]byte("\x1b_Ga=T,t=f,m=1;image.png\x1b\\\x1b_Gm=0;\x1b\\"), dir)
	if len(recs) != 2 || !bytes.Contains(recs[0].seq, []byte("t=d")) || string(recs[1].seq) != "\x1b_Gm=0;\x1b\\" {
		t.Fatalf("a chunked file image recorded %+v", recs)
	}
}

// recorded feeds the output of a program to a session's graphics, as its
// reads do, and returns what an attach replays.
func recorded(out ...string) string {
	var g graphics
	for _, p := range out {
		_, recs := g.filter([]byte(p), "")
		g.record(recs)
	}
	return string(g.replay())
}

// TestGraphicsHistoryKeepsImagesAsTheyAre: an image a program sends again
// by its id, as omp does at each resize, replays once, the last one; one
// it deleted, with its data, does not replay; what names no image by its
// id stays as it was sent.
func TestGraphicsHistoryKeepsImagesAsTheyAre(t *testing.T) {
	image := func(id int, payload string) string {
		return fmt.Sprintf("\x1b_Ga=T,f=100,i=%d,q=2;%s\x1b\\", id, payload)
	}
	var out []string
	for n := range 20 {
		out = append(out, image(7, fmt.Sprint("AAA", n)))
	}
	out = append(out, image(8, "BBBB"))
	if got, want := recorded(out...), image(7, "AAA19")+image(8, "BBBB"); got != want {
		t.Errorf("after 20 transmits of image 7 the replay is\n%q, want\n%q", got, want)
	}

	place := "\x1b_Ga=p,i=7,p=2,q=2\x1b\\"
	unplace := "\x1b_Ga=d,d=i,i=7,p=2,q=2\x1b\\"
	free := "\x1b_Ga=d,d=I,i=7,q=2\x1b\\"
	if got, want := recorded(image(7, "AAAA"), place, unplace, image(8, "BBBB"), free), image(8, "BBBB"); got != want {
		t.Errorf("after image 7 was deleted the replay is\n%q, want\n%q", got, want)
	}
	// A delete of one placement leaves the image: it stays recorded.
	freeOne := "\x1b_Ga=d,d=I,i=7,p=2,q=2\x1b\\"
	if got, want := recorded(image(7, "AAAA"), place, freeOne), image(7, "AAAA")+place+freeOne; got != want {
		t.Errorf("after a placement of image 7 was deleted the replay is\n%q, want\n%q", got, want)
	}

	// A transfer in chunks, sent again, replays once, whole.
	chunked := "\x1b_Ga=T,f=100,i=9,q=2,m=1;AAAA\x1b\\\x1b_Gm=1;BBBB\x1b\\\x1b_Gm=0;CCCC\x1b\\"
	if got := recorded(chunked, chunked, chunked); got != chunked {
		t.Errorf("after a chunked image sent 3 times the replay is\n%q, want\n%q", got, chunked)
	}

	// No id, or a number, which makes another image each time: as sent.
	noID := "\x1b_Ga=T,f=100;AAAA\x1b\\"
	number := "\x1b_Ga=T,f=100,I=3;AAAA\x1b\\"
	if got, want := recorded(noID, noID, number, number), noID+noID+number+number; got != want {
		t.Errorf("images with no id replay\n%q, want\n%q", got, want)
	}
}

// TestGraphicsHistoryReplacesByID: an id is a number however spelled; a
// transmit too large to keep still replaces what the history had of its
// image; what does not transmit or free an image by its id leaves it.
func TestGraphicsHistoryReplacesByID(t *testing.T) {
	image := func(id, payload string) string {
		return "\x1b_Ga=T,f=100,i=" + id + ",q=2;" + payload + "\x1b\\"
	}
	if got, want := recorded(image("7", "AAAA"), image("07", "BBBB")), image("07", "BBBB"); got != want {
		t.Errorf("after i=7 then i=07 the replay is\n%q, want\n%q", got, want)
	}

	// Too large for one read's rewrite, for the history in chunks, and
	// for the history whole.
	big := strings.Repeat("A", maxGraphicsRewrite+4)
	chunk := strings.Repeat("A", 3<<20)
	before := []string{image("7", "AAAA"), image("8", "BBBB")}
	for name, out := range map[string][]string{
		"in one read": {image("7", big)},
		"in chunks": {"\x1b_Ga=T,f=100,i=7,q=2,m=1;" + chunk + "\x1b\\", "\x1b_Gm=1;" + chunk + "\x1b\\",
			"\x1b_Gm=1;" + chunk + "\x1b\\", "\x1b_Gm=0;AAAA\x1b\\"},
	} {
		if got := recorded(append(before, out...)...); got != image("8", "BBBB") {
			t.Errorf("after a transmit of image 7 too large to keep %s, the replay is %.80q", name, got)
		}
	}
	var g graphics
	g.record([]graphicsSeq{{seq: []byte(before[0]), id: 7, op: graphicsTransmit}})
	g.record([]graphicsSeq{{seq: []byte(image("7", strings.Repeat("A", maxGraphicsHistory))), id: 7, op: graphicsTransmit}})
	if len(g.history) != 0 {
		t.Errorf("after a transmit of image 7 too large for the history, it has %d sequences", len(g.history))
	}

	// A free that frees nothing recorded, as of an image sent by number,
	// stays; a header with no a=d, or a transmit with no payload, does
	// nothing to the image.
	for _, out := range [][]string{
		{"\x1b_Ga=T,f=100,I=5;AAAA\x1b\\", "\x1b_Ga=d,d=I,i=5,q=2\x1b\\"},
		{image("7", "AAAA"), "\x1b_Gd=I,i=7\x1b\\"},
		{image("7", "AAAA"), "\x1b_Ga=T,i=7;\x1b\\"},
	} {
		if got, want := recorded(out...), strings.Join(out, ""); got != want {
			t.Errorf("the replay is\n%q, want\n%q", got, want)
		}
	}
}

// TestSessionReplaysImageOnce: a window attaching to a session whose
// program sent its image twenty times gets it once.
func TestSessionReplaysImageOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.png"), append(append([]byte(nil), pngHeader...), "pixels"...), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newSession("gfx", CreateOptions{
		Command: []string{"/bin/sh", "-c", "for n in $(seq 20); do printf '\\033_Gi=7,a=T,t=f;image.png\\033\\\\'; done; sleep 0.1"},
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
	snap, _ := s.view()
	s.mu.Unlock()
	if n := bytes.Count(snap, []byte("\x1b_Gi=7,")); n != 1 {
		t.Errorf("the attach sends image 7 %d times, want once", n)
	}
}
