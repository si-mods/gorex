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

// writeImage writes an image of the test's own to dir, named name.
func writeImage(t *testing.T, dir, name string, png0 bool) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(1, 1, color.RGBA{R: 0xff, A: 0xff})
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

// TestPasteOffersAFileAsItsBytes: a paste of a copied image file offers
// the file's bytes as its image type, ahead of its file URLs, which stay
// for a program that wants a file to open.
func TestPasteOffersAFileAsItsBytes(t *testing.T) {
	path, want := writeImage(t, t.TempDir(), "shot.png", true)
	fd, err := transfer.FileData(path)
	if err != nil {
		t.Fatal(err)
	}
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
// absolute path to an image here offers the image, and keeps the path as
// its text.
func TestPasteOffersAPathTextAsItsImage(t *testing.T) {
	path, want := writeImage(t, t.TempDir(), "shot.jpg", false)
	got, ok := pasteImageData(transfer.TextData(path))
	if !ok {
		t.Fatal("no image offered of a path pasted as text")
	}
	if f := got.Formats(); f[0] != transfer.Format("image/jpeg") {
		t.Errorf("formats %v, want image/jpeg first", f)
	}
	if b, _ := got.Read(transfer.Format("image/jpeg")); !bytes.Equal(b, want) {
		t.Errorf("offered %d bytes, %d want", len(b), len(want))
	}
	if b, _ := got.Read(transfer.Text); string(b) != path {
		t.Errorf("text %q, want the path", b)
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
	for _, c := range []struct {
		name string
		d    transfer.Data
	}{
		{"text", transfer.TextData("hello")},
		{"a file of text", func() transfer.Data {
			fd, err := transfer.FileData(text)
			if err != nil {
				t.Fatal(err)
			}
			return fd
		}()},
		{"a path of a file of text", transfer.TextData(text)},
		{"a path that is not here", transfer.TextData(filepath.Join(dir, "gone.png"))},
		{"a relative path", transfer.TextData("shot.png")},
		{"two lines", transfer.TextData(text + "\nmore")},
		{"an image already", transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("png")), transfer.Bytes(transfer.Text, []byte(text))))},
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
	fd, err := transfer.FileData(path)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := pasteImageData(fd)
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

// TestPasteOfAnImageFileOverTheSession: a paste of a copied image file in
// a pane whose program reads the clipboard itself (mode 5522) reaches it
// as a paste event with the file's bytes; a paste of a path as text still
// types the path.
func TestPasteOfAnImageFileOverTheSession(t *testing.T) {
	paste := func(t *testing.T, mode bool, d transfer.Data) (string, string) {
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
		time.Sleep(300 * time.Millisecond)
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

	path, _ := writeImage(t, t.TempDir(), "shot.png", true)
	fd, err := transfer.FileData(path)
	if err != nil {
		t.Fatal(err)
	}
	got, text := paste(t, true, fd)
	want := base64.StdEncoding.EncodeToString([]byte("image/png"))
	if !strings.Contains(got, "\x1b]5522;type=read:status=OK") || !strings.Contains(got, want) {
		t.Errorf("the program got %q, want a paste event offering image/png (%s); screen %q", got, want, text)
	}

	// Without the mode the paste is the text it always was.
	got, text = paste(t, false, transfer.TextData(path))
	if got != path {
		t.Errorf("a paste of a path sent %q, want the path; screen %q", got, text)
	}
}
