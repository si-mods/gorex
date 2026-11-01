//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// silentCommand is a program that prints nothing and stays, so that a
// session's screen is only what a test feeds its emulator.
func silentCommand() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", "ping -n 30 127.0.0.1 >nul"}
	}
	return []string{"/bin/sh", "-c", "sleep 30"}
}

// printAndExit is a program that prints a marker and ends.
func printAndExit() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", "echo ghostty-exit"}
	}
	return []string{"/bin/sh", "-c", "echo ghostty-exit"}
}

// headless returns a terminal no view shows and no program runs, to
// restore a snapshot into.
func headless(t *testing.T) *terminal.Terminal {
	t.Helper()
	pr, pw := io.Pipe()
	term, err := terminal.New(terminal.Options{Conn: discard{pr}})
	if err != nil {
		pw.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pw.Close()
		term.Close()
	})
	return term
}

// framedAttach attaches to sid as a, framed, and returns the header and
// the snapshot it sends first.
func framedAttach(t *testing.T, c *Client, sid string, a Attach) (SnapshotHeader, []byte) {
	t.Helper()
	a.SID, a.Framed = sid, true
	conn, err := c.AttachConn(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	r := bufio.NewReaderSize(conn, 64<<10)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := r.ReadBytes('\n')
	var head SnapshotHeader
	if err == nil {
		err = json.Unmarshal(line, &head)
	}
	var snap []byte
	if err == nil {
		snap = make([]byte, max(head.Snapshot, 0))
		_, err = io.ReadFull(r, snap)
	}
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return head, snap
}

// snapshotTags returns the tags of the records a Ghostty snapshot holds,
// after its 10-byte envelope: each is a u16 tag, a u32 length, a u32
// CRC32C and the payload.
func snapshotTags(t *testing.T, snap []byte) []uint16 {
	t.Helper()
	if len(snap) < 10 || string(snap[:8]) != "GHOSTSNP" {
		t.Fatalf("not a Ghostty snapshot: %q", snap[:min(len(snap), 16)])
	}
	var tags []uint16
	for i := 10; i+10 <= len(snap); {
		tag := binary.LittleEndian.Uint16(snap[i:])
		n := int(binary.LittleEndian.Uint32(snap[i+2:]))
		if n < 0 || i+10+n > len(snap) {
			t.Fatalf("record %d of %d bytes runs past the snapshot", tag, n)
		}
		tags = append(tags, tag)
		i += 10 + n
	}
	return tags
}

// fixtureTags returns the record tags of the snapshot a Rex terminal
// stream starts with, in internal/rexapi's test stream: everything before
// its first Rex APC.
func fixtureTags(t *testing.T) []uint16 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "rexapi", "testdata", "rex-terminal-stream.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if i := bytes.Index(b, []byte("\x1b_rex1;")); i >= 0 {
		b = b[:i]
	}
	return snapshotTags(t, b)
}

// feedSession writes p into the session's emulator, as its program's
// output would.
func feedSession(t *testing.T, s *Server, sid string, p string) {
	t.Helper()
	s.mu.Lock()
	ss := s.sessions[sid]
	s.mu.Unlock()
	if ss == nil {
		t.Fatalf("no session %s", sid)
	}
	ss.mu.Lock()
	ss.vt.Feed([]byte(p))
	ss.mu.Unlock()
}

// TestGhosttyAttach checks that an attach asking for one gets the
// session's screen in Ghostty's binary snapshot format, which restores to
// the screen the VT snapshot replays, and that a plain attach still gets
// the escape sequences.
func TestGhosttyAttach(t *testing.T) {
	var s *Server
	dial := func() (net.Conn, error) {
		a, b := net.Pipe()
		go s.handle(b, "")
		return a, nil
	}
	s, c, sid := streamServerCommand(t, dial, silentCommand(), os.TempDir())
	var out strings.Builder
	out.WriteString("\x1b[1;31mred\x1b[0m and \x1b[4munder\x1b[0m\r\n")
	for i := range 40 {
		fmt.Fprintf(&out, "line %d\r\n", i)
	}
	out.WriteString("sh-3.2$ ")
	feedSession(t, s, sid, out.String())

	head, snap := framedAttach(t, c, sid, Attach{Snapshot: SnapshotGhostty})
	if head.Format != SnapshotGhostty {
		t.Fatalf("format %q, want %q", head.Format, SnapshotGhostty)
	}
	if head.Truncated {
		t.Error("a ghostty snapshot is truncated")
	}
	if head.Cols != 80 || head.Rows != 24 {
		t.Errorf("snapshot of %dx%d, want 80x24", head.Cols, head.Rows)
	}
	if !bytes.HasPrefix(snap, []byte("GHOSTSNP")) {
		t.Fatalf("snapshot starts %q", snap[:min(len(snap), 16)])
	}
	if v := binary.LittleEndian.Uint16(snap[8:10]); v != 1 {
		t.Errorf("snapshot version %d, want 1", v)
	}
	if got, want := snapshotTags(t, snap), fixtureTags(t); !slices.Equal(got, want) {
		t.Errorf("record tags %v, want %v", got, want)
	}
	restored := headless(t)
	if err := restored.RestoreGhosttySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	text := restored.Text()
	for _, want := range []string{"red and under", "line 0", "line 39", "sh-3.2$"} {
		if !strings.Contains(text, want) {
			t.Errorf("restored text lacks %q: %q", want, text)
		}
	}

	// A plain attach still gets the escape sequences, which replay to the
	// same screen.
	vhead, vt := framedAttach(t, c, sid, Attach{})
	if vhead.Format != "" {
		t.Errorf("a plain attach says format %q", vhead.Format)
	}
	if bytes.HasPrefix(vt, []byte("GHOSTSNP")) {
		t.Errorf("a plain attach sends a Ghostty snapshot")
	}
	replayed := headless(t)
	replayed.Resize(head.Cols, head.Rows)
	replayed.Feed(vt)
	if got := replayed.Text(); got != text {
		t.Errorf("the replayed screen differs: %q, want %q", got, text)
	}
}

// TestGhosttyAttachExited checks that a session that ended keeps the
// Ghostty snapshot of its screen, and falls back to the escape sequences
// when it has none.
func TestGhosttyAttachExited(t *testing.T) {
	var s *Server
	dial := func() (net.Conn, error) {
		a, b := net.Pipe()
		go s.handle(b, "")
		return a, nil
	}
	s, c, sid := streamServerCommand(t, dial, printAndExit(), os.TempDir())
	for deadline := time.Now().Add(5 * time.Second); !sessionInfo(t, c, sid).Exited; {
		if time.Now().After(deadline) {
			t.Fatal("the session did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	head, snap := framedAttach(t, c, sid, Attach{Snapshot: SnapshotGhostty})
	if head.Format != SnapshotGhostty {
		t.Fatalf("format %q, want %q", head.Format, SnapshotGhostty)
	}
	restored := headless(t)
	if err := restored.RestoreGhosttySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if text := restored.Text(); !strings.Contains(text, "ghostty-exit") {
		t.Errorf("restored text %q", text)
	}

	// Without one, the attach sends the escape sequences, and the header
	// tells the client which it got.
	s.mu.Lock()
	ss := s.sessions[sid]
	s.mu.Unlock()
	ss.mu.Lock()
	ss.ghostty = nil
	ss.mu.Unlock()
	vhead, vt := framedAttach(t, c, sid, Attach{Snapshot: SnapshotGhostty})
	if vhead.Format != "" {
		t.Errorf("the fallback says format %q", vhead.Format)
	}
	if bytes.HasPrefix(vt, []byte("GHOSTSNP")) {
		t.Errorf("the fallback sends a Ghostty snapshot")
	}
	replayed := headless(t)
	replayed.Feed(vt)
	if text := replayed.Text(); !strings.Contains(text, "ghostty-exit") {
		t.Errorf("replayed text %q", text)
	}
}

// TestGhosttyAttachRemote checks that an attach over the server's remote
// path carries the snapshot it asks for.
func TestGhosttyAttachRemote(t *testing.T) {
	s, _ := testServer(t)
	s.rem.mu.Lock()
	s.rem.cfg.ShareSessions = true
	s.rem.mu.Unlock()
	local := pipe(s, "")
	t.Cleanup(func() { local.Close() })
	info, err := local.Create(CreateOptions{Command: silentCommand(), Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Kill(info.ID) })
	remote := pipe(s, testPeer)
	t.Cleanup(func() { remote.Close() })
	head, snap := framedAttach(t, remote, info.ID, Attach{Snapshot: SnapshotGhostty})
	if head.Format != SnapshotGhostty {
		t.Fatalf("format %q, want %q", head.Format, SnapshotGhostty)
	}
	if !bytes.HasPrefix(snap, []byte("GHOSTSNP")) {
		t.Errorf("snapshot starts %q", snap[:min(len(snap), 16)])
	}
}
