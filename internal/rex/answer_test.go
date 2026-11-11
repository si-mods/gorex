//go:build darwin || linux

package rex

import (
	"bytes"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// answerScript is a program that, once typed a line, prints ready, asks
// its terminal the queries q, and prints what it reads as text until it
// reads nothing for 2 seconds, then END.
func answerScript(q string) []string {
	return []string{"/bin/sh", "-c", `stty -echo; read x; stty raw -echo min 0 time 20; printf 'ready` + q + `'; cat -v; printf '\r\nEND\r\n'; sleep 60`}
}

var (
	cprAnswer  = regexp.MustCompile(`\^\[\[\d+;\d+R`)
	daAnswer   = regexp.MustCompile(`\^\[\[\?[\d;]*c`)
	sizeAnswer = regexp.MustCompile(`\^\[\[8;\d+;\d+t`)
)

// TestOneAnswerPerQuery checks that a program's query gets one answer,
// whichever clients are attached: the authority's terminal answers, and
// the server's emulator, at the session's size, when the authority is
// mute, as the gateway's attach, or when no client is attached. The
// gateway's attach gives no size, so a window beside it is the
// authority, unless the gateway holds the size.
func TestOneAnswerPerQuery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attaches []string
		window   bool   // a window answers, not the emulator
		size     string // the size answer the program reads
	}{
		{"a window", []string{"window"}, true, "^[[8;24;80t"},
		{"no client", nil, false, "^[[8;24;80t"},
		{"the gateway", []string{"gateway"}, false, "^[[8;24;80t"},
		{"a window, then the gateway", []string{"window", "gateway"}, true, "^[[8;24;80t"},
		{"the gateway holding the size", []string{"wide window", "gateway lock"}, false, "^[[8;20;60t"},
		{"two windows", []string{"window", "window"}, true, "^[[8;24;80t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, sid := streamServerCommand(t, nil, answerScript(`\033[6n\033[c\033[18t\033]4;1;?\007`), "/tmp")
			for _, kind := range tc.attaches {
				cl := helloStream(t, s)
				switch kind {
				case "window", "wide window":
					cols, rows := 80, 24
					if kind == "wide window" {
						cols, rows = 100, 30
					}
					conn, err := cl.AttachConn(Attach{SID: sid, Cols: cols, Rows: rows})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					go answer(conn, cols, rows)
				default:
					conn, err := cl.AttachConn(Attach{SID: sid, Snapshot: SnapshotGhostty, Framed: true, Mute: true})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					go io.Copy(io.Discard, conn)
					if kind == "gateway lock" {
						if err := cl.LockSize(sid, "phone", 60, 20); err != nil {
							t.Fatal(err)
						}
						waitSize(t, c, sid, "60x20")
					}
				}
			}
			if err := c.Input(sid, []byte("go\n")); err != nil {
				t.Fatal(err)
			}
			cpr, da, size := answers(t, c, sid)
			if len(cpr) != 1 || len(da) != 1 || len(size) != 1 {
				t.Fatalf("the program read %q, %q and %q, want one of each", cpr, da, size)
			}
			if tc.window != (string(cpr[0]) == "^[[7;7R") {
				t.Errorf("the program read %q, want a window's answer: %v", cpr[0], tc.window)
			}
			if bytes.Contains(must(c.Capture(sid, 0)).Screen, []byte("^[]4;")) {
				t.Error("the emulator answered a color")
			}
			if string(size[0]) != tc.size {
				t.Errorf("the program read the size %q, want %q", size[0], tc.size)
			}
		})
	}
}

// TestAnswerDecidedAsFed checks that who answers a query is decided as
// the query is fed, not as the emulator writes its answer: a window
// attaching in between, which never saw the query, does not take the
// answer from the emulator, and a window that answered, then left, does
// not make the emulator answer too.
func TestAnswerDecidedAsFed(t *testing.T) {
	t.Run("a window attaches", func(t *testing.T) {
		s, c, sid := streamServerCommand(t, nil, answerScript(""), "/tmp")
		ss := s.sessions[sid]
		ready(t, c, sid)
		window := &attached{seq: 1 << 30, out: make(chan []byte, maxQueue)}
		ss.mu.Lock()
		ss.answerLocked()
		ss.vt.Feed([]byte("\x1b[6n"))
		ss.clients[window] = struct{}{}
		ss.mu.Unlock()
		defer ss.leave(window)
		if cpr, _, _ := answers(t, c, sid); len(cpr) != 1 {
			t.Fatalf("the program read %q, want one answer", cpr)
		}
	})
	t.Run("the window leaves", func(t *testing.T) {
		s, c, sid := streamServerCommand(t, nil, answerScript(""), "/tmp")
		ss := s.sessions[sid]
		ready(t, c, sid)
		window := &attached{seq: 1 << 30, out: make(chan []byte, maxQueue)}
		ss.mu.Lock()
		ss.clients[window] = struct{}{}
		ss.answerLocked()
		ss.vt.Feed([]byte("\x1b[6n"))
		ss.mu.Unlock()
		ss.input(window, []byte("\x1b[7;7R"))
		ss.leave(window)
		if cpr, _, _ := answers(t, c, sid); len(cpr) != 1 || string(cpr[0]) != "^[[7;7R" {
			t.Fatalf("the program read %q, want the window's answer alone", cpr)
		}
	})
}

// ready starts answerScript, and waits for it to read.
func ready(t *testing.T, c *Client, sid string) {
	t.Helper()
	if err := c.Input(sid, []byte("go\n")); err != nil {
		t.Fatal(err)
	}
	waitCapture(t, c, sid, "ready")
}

// answers waits for answerScript to end, and returns the answers it read.
func answers(t *testing.T, c *Client, sid string) (cpr, da, size [][]byte) {
	t.Helper()
	waitCapture(t, c, sid, "END")
	screen := must(c.Capture(sid, 0)).Screen
	return cprAnswer.FindAll(screen, -1), daAnswer.FindAll(screen, -1), sizeAnswer.FindAll(screen, -1)
}

// answer answers the queries in what conn reads, as a window's terminal
// of cols×rows does.
func answer(conn net.Conn, cols, rows int) {
	size := "\x1b[8;" + strconv.Itoa(rows) + ";" + strconv.Itoa(cols) + "t"
	var seen []byte
	buf := make([]byte, 32<<10)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		seen = append(seen, buf[:n]...)
		for _, q := range [][2]string{{"\x1b[6n", "\x1b[7;7R"}, {"\x1b[c", "\x1b[?62;22c"}, {"\x1b[18t", size}} {
			for i := bytes.Index(seen, []byte(q[0])); i >= 0; i = bytes.Index(seen, []byte(q[0])) {
				conn.Write([]byte(q[1]))
				seen = append(seen[:i], seen[i+len(q[0]):]...)
			}
		}
	}
}

// TestEmulatorAnswersWhole checks that the emulator's Conn takes its
// answers whole, though a write holds several, or part of one: it types
// all but the colors.
func TestEmulatorAnswersWhole(t *testing.T) {
	s, c, sid := streamServerCommand(t, nil, answerScript(""), "/tmp")
	ready(t, c, sid)
	vc := &vtConn{s: s.sessions[sid]}
	for _, p := range []string{"\x00on", "\x1b[3;4R\x1b]11;rgb:0/0/0\x07\x1b]4;1;rgb:c", "c/6/6\x1b", "\\\x1b[?6", "2;22c\x1b[?997;1n"} {
		vc.Write([]byte(p))
	}
	waitCapture(t, c, sid, "END")
	screen := string(must(c.Capture(sid, 0)).Screen)
	if !strings.Contains(screen, "ready^[[3;4R^[[?62;22c") || strings.Contains(screen, "^[]") || strings.Contains(screen, "997") {
		t.Errorf("the program read %q, want the cursor and the device, not the colors", screen)
	}
}
