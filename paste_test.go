package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// writeImages counts the images the tests wrote, so that each differs.
var writeImages int

// writeImage writes an image of the test's own to dir, named name: each
// call writes other bytes.
func writeImage(t *testing.T, dir, name string, png0 bool) (string, []byte) {
	t.Helper()
	writeImages++
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(1, 1, color.RGBA{R: byte(1 + writeImages%200), A: 0xff})
	var buf bytes.Buffer
	var err error
	if png0 {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, buf.Bytes()
}

// fileData returns the paste a file manager's copy of paths makes.
func fileData(t *testing.T, paths ...string) transfer.Data {
	t.Helper()
	d, err := transfer.FileData(paths...)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestPasteOffersAFileAsItsBytes: a paste of a copied image file offers
// the file's bytes as its image type, ahead of its file URLs, which stay
// for a program that wants a file to open.
func TestPasteOffersAFileAsItsBytes(t *testing.T) {
	path, want := writeImage(t, t.TempDir(), "shot.png", true)
	fd := fileData(t, path)
	d := transfer.New(append(fd.Items(), transfer.NewItem(transfer.Bytes(transfer.Text, []byte("shot.png"))))...)
	got, ok := pasteImageData(d)
	if !ok {
		t.Fatal("no image offered of a copied image file")
	}
	if f := got.Formats(); len(f) == 0 || f[0] != transfer.PNG {
		t.Errorf("formats %v, want image/png first", f)
	}
	if b, err := got.Read(transfer.PNG); err != nil || !bytes.Equal(b, want) {
		t.Errorf("offered %d bytes, %d want (%v)", len(b), len(want), err)
	}
	if b, _ := got.Read(transfer.Text); string(b) != "shot.png" {
		t.Errorf("text %q, want the paste's own", b)
	}
	if b, _ := got.Read(transfer.URIList); len(b) == 0 {
		t.Error("the file URLs are gone")
	}
}

// TestPasteOffersAPathTextAsItsImage: a paste of text that is one
// absolute path to an image here, quoted as a file manager's copy of a
// path pastes it, offers the image and keeps the text.
func TestPasteOffersAPathTextAsItsImage(t *testing.T) {
	path, want := writeImage(t, t.TempDir(), "shot.jpg", false)
	quoted := `"` + path + `"`
	got, ok := pasteImageData(transfer.TextData(quoted))
	if !ok {
		t.Fatal("no image offered of a path pasted as text")
	}
	if f := got.Formats(); f[0] != transfer.Format("image/jpeg") {
		t.Errorf("formats %v, want image/jpeg first", f)
	}
	if b, _ := got.Read(transfer.Format("image/jpeg")); !bytes.Equal(b, want) {
		t.Errorf("offered %d bytes, %d want", len(b), len(want))
	}
	if b, _ := got.Read(transfer.Text); string(b) != quoted {
		t.Errorf("text %q, want the paste's own", b)
	}
	// A clipboard holds other formats too, which a paste keeps.
	withHTML := transfer.New(
		transfer.NewItem(transfer.Bytes(transfer.Text, []byte(path))),
		transfer.NewItem(transfer.Bytes(transfer.HTML, []byte("<p>shot</p>"))),
	)
	if got, ok := pasteImageData(withHTML); !ok {
		t.Error("no image offered beside HTML")
	} else if b, err := got.Read(transfer.HTML); err != nil || string(b) != "<p>shot</p>" {
		t.Errorf("HTML %q (%v), want the paste's own", b, err)
	}
}

// TestPasteReadsTheImageWhenTheProgramAsks: the file is read as the
// program asks for it, not as the paste is made, so a paste into a shell
// costs nothing.
func TestPasteReadsTheImageWhenTheProgramAsks(t *testing.T) {
	dir := t.TempDir()
	path, first := writeImage(t, dir, "shot.png", true)
	got, ok := pasteImageData(fileData(t, path))
	if !ok {
		t.Fatal("no image offered")
	}
	_, later := writeImage(t, dir, "shot.png", true)
	if bytes.Equal(first, later) {
		t.Fatal("the test's two writes are the same bytes")
	}
	if b, err := got.Read(transfer.PNG); err != nil || !bytes.Equal(b, later) {
		t.Errorf("offered %d bytes (%v), want the file's as the paste is read", len(b), err)
	}
}

// TestPasteOffersTheImageOfManyFiles: the image among the files a paste
// lists is the one offered, whatever its place, though its name has
// spaces.
func TestPasteOffersTheImageOfManyFiles(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, want := writeImage(t, dir, "a shot.png", true)
	got, ok := pasteImageData(fileData(t, other, path))
	if !ok {
		t.Fatal("no image offered among many files")
	}
	if b, err := got.Read(transfer.PNG); err != nil || !bytes.Equal(b, want) {
		t.Errorf("offered %d bytes (%v), want the second file's image", len(b), err)
	}
}

// TestPasteLeavesWhatIsNoImage: a paste of text, of a file that is not an
// image, of a path that is not here, is left as it is.
func TestPasteLeavesWhatIsNoImage(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file of more bytes than a paste offers, whatever its head says.
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, maxPasteImage+1); err != nil {
		t.Fatal(err)
	}
	// A directory a paste names as an image.
	if err := os.Mkdir(filepath.Join(dir, "folder.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		d    transfer.Data
	}{
		{"text", transfer.TextData("hello")},
		{"a file of text", fileData(t, text)},
		{"a path of a file of text", transfer.TextData(text)},
		{"a path that is not here", transfer.TextData(filepath.Join(dir, "gone.png"))},
		{"a relative path", transfer.TextData("shot.png")},
		{"two lines", transfer.TextData(text + "\nmore")},
		{"an image already", transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("png")), transfer.Bytes(transfer.Text, []byte(text))))},
		{"a file over the limit", fileData(t, big)},
		{"a path over the limit", transfer.TextData(big)},
		{"a directory named as an image", fileData(t, filepath.Join(dir, "folder.png"))},
	} {
		got, ok := pasteImageData(c.d)
		if ok {
			t.Errorf("%s: offered %v", c.name, got.Formats())
		}
	}
}

// captureConn is a terminal's Conn that keeps what it is sent and reads
// nothing, as one over a link that has nothing to say.
type captureConn struct{ out bytes.Buffer }

func (c *captureConn) Read([]byte) (int, error) { select {} }
func (c *captureConn) Write(p []byte) (int, error) {
	return c.out.Write(p)
}
func (c *captureConn) Close() error { return nil }

// TestPasteImageReachesAProgramReadingTheClipboard: the bytes a paste
// offers reach a program that reads the clipboard itself (mode 5522) as a
// paste event, which is how they cross SSH and hosts.
func TestPasteImageReachesAProgramReadingTheClipboard(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Skipf("libghostty-vt: %v", err)
	}
	conn := &captureConn{}
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	// The program a paste goes to, which takes images by the clipboard.
	term.Feed([]byte("\x1b[?5522h"))
	path, _ := writeImage(t, t.TempDir(), "shot.png", true)
	d, ok := pasteImageData(fileData(t, path))
	if !ok {
		t.Fatal("no image offered")
	}
	term.PasteData(d)
	want := base64.StdEncoding.EncodeToString([]byte("image/png"))
	deadline := time.Now().Add(10 * time.Second)
	for {
		event := conn.out.String()
		if strings.Contains(event, "\x1b]5522;type=read:status=OK") && strings.Contains(event, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the program was told %q, want a paste event offering image/png (%s)", event, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pasteInto pastes d into a pane of a new app, whose program runs cat
// into a file, with mode 5522 set first when mode, and returns what the
// program read once the screen shows until, the end of what cat reads
// sent to flush it.
func pasteInto(t *testing.T, mode bool, d transfer.Data, until string) (string, string) {
	t.Helper()
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	got := filepath.Join(t.TempDir(), "pasted")
	if mode {
		// The program takes images by the clipboard.
		p.term.Send([]byte("printf 'READY\\033[?5522h\\n'\n"))
		waitFor(t, tt, "the mode", func() bool { return strings.Contains(p.term.Text(), "READY") })
	}
	p.term.Send([]byte("echo GO; cat > '" + got + "'\n"))
	waitFor(t, tt, "cat", func() bool { return strings.Contains(p.term.Text(), "GO") })
	tt.SetClipboardData(d)
	tt.Frame()
	key := ui.Modifiers(ui.Super)
	if runtime.GOOS != "darwin" {
		key = ui.Ctrl | ui.Shift
	}
	tt.Key(key, ui.KeyV)
	waitFor(t, tt, "the paste", func() bool { return strings.Contains(p.term.Text(), until) })
	p.term.Send([]byte{0x04}) // the end of what cat reads
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(got)
		if err == nil && len(b) > 0 {
			return string(b), p.term.Text()
		}
		if time.Now().After(deadline) {
			return "", p.term.Text()
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
}

// TestPasteOfAnImageFileOverTheSession: a paste of a copied image file in
// a pane whose program reads the clipboard itself (mode 5522) reaches it
// as a paste event with the file's bytes.
func TestPasteOfAnImageFileOverTheSession(t *testing.T) {
	path, _ := writeImage(t, t.TempDir(), "shot.png", true)
	want := base64.StdEncoding.EncodeToString([]byte("image/png"))
	got, text := pasteInto(t, true, fileData(t, path), "status=OK")
	if !strings.Contains(got, "\x1b]5522;type=read:status=OK") || !strings.Contains(got, want) {
		t.Errorf("the program got %q, want a paste event offering image/png (%s); screen %q", got, want, text)
	}
}

// TestPasteOfAPathOverTheSession: without the mode a paste of a path as
// text still types the path, as it always did.
func TestPasteOfAPathOverTheSession(t *testing.T) {
	path, _ := writeImage(t, t.TempDir(), "shot.png", true)
	got, text := pasteInto(t, false, transfer.TextData(path), path)
	if got != path {
		t.Errorf("a paste of a path sent %q, want the path; screen %q", got, text)
	}
}
