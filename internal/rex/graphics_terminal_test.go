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

// TestGraphicsTerminalAcceptsRewrite feeds a rewritten file image to the
// terminal library and reads its answer: an in-band image is OK, the file
// medium it replaced is not.
func TestGraphicsTerminalAcceptsRewrite(t *testing.T) {
	dir := t.TempDir()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var g graphics
	rewritten, recs := g.filter([]byte("\x1b_Gi=7,a=T,t=f;image.png\x1b\\"), dir)
	g.record(recs)
	if bytes.Contains(rewritten, []byte("t=f")) {
		t.Fatalf("not rewritten: %q", rewritten)
	}
	if answer := terminalAnswer(t, rewritten); !strings.Contains(answer, "OK") {
		t.Fatalf("in-band answer %q", answer)
	}
}

func terminalAnswer(t *testing.T, seq []byte) string {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	term, err := terminal.New(terminal.Options{Conn: client})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	term.Resize(80, 24)
	term.Feed(seq)
	server.SetReadDeadline(time.Now().Add(time.Second))
	var got []byte
	buf := make([]byte, 1024)
	for {
		n, err := server.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
		}
		if err != nil || bytes.Contains(got, []byte("\x1b\\")) {
			break
		}
	}
	return string(got)
}
