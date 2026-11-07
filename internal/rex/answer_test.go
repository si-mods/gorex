//go:build darwin || linux

package rex

import (
	"bytes"
	"io"
	"net"
	"regexp"
	"testing"
)

// TestOneAnswerPerQuery checks that a program's query gets one answer,
// whichever clients are attached: a window's terminal answers, the
// gateway's attach (a GHOSTSNP one) answers nothing, and the server's
// emulator answers when no attached terminal does.
func TestOneAnswerPerQuery(t *testing.T) {
	script := `stty -echo; read x; stty raw -echo min 0 time 20; printf '\033[6n\033[c'; cat -v; printf '\r\nEND\r\n'; sleep 60`
	for _, tc := range []struct {
		name     string
		attaches []string
	}{
		{"a window", []string{"window"}},
		{"the gateway", []string{"gateway"}},
		{"a window, then the gateway", []string{"window", "gateway"}},
		{"the gateway holding the size", []string{"window", "gateway lock"}},
		{"two windows", []string{"window", "window"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, sid := streamServerCommand(t, nil, []string{"/bin/sh", "-c", script}, "/tmp")
			for _, kind := range tc.attaches {
				cl := helloStream(t, s)
				if kind == "window" {
					conn, err := cl.AttachConn(Attach{SID: sid, Cols: 80, Rows: 24})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					go answer(conn)
					continue
				}
				conn, err := cl.AttachConn(Attach{SID: sid, Snapshot: SnapshotGhostty, Framed: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				go io.Copy(io.Discard, conn)
				if kind == "gateway lock" {
					if err := cl.LockSize(sid, "phone", 60, 20); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := c.Input(sid, []byte("go\n")); err != nil {
				t.Fatal(err)
			}
			waitCapture(t, c, sid, "END")
			screen := must(c.Capture(sid, 0)).Screen
			cpr := regexp.MustCompile(`\^\[\[\d+;\d+R`).FindAll(screen, -1)
			da := regexp.MustCompile(`\^\[\[\?[\d;]*c`).FindAll(screen, -1)
			if len(cpr) != 1 || len(da) != 1 {
				t.Fatalf("the program read %q and %q, want one of each", cpr, da)
			}
			// A window answers whenever one is attached.
			if window := tc.attaches[0] == "window"; window != (string(cpr[0]) == "^[[7;7R") {
				t.Errorf("the program read %q, with a window attached: %v", cpr[0], window)
			}
		})
	}
}

// answer answers the queries in what conn reads, as a window's terminal
// does.
func answer(conn net.Conn) {
	var seen []byte
	buf := make([]byte, 32<<10)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		seen = append(seen, buf[:n]...)
		for _, q := range [][2]string{{"\x1b[6n", "\x1b[7;7R"}, {"\x1b[c", "\x1b[?62;22c"}} {
			for i := bytes.Index(seen, []byte(q[0])); i >= 0; i = bytes.Index(seen, []byte(q[0])) {
				conn.Write([]byte(q[1]))
				seen = append(seen[:i], seen[i+len(q[0]):]...)
			}
		}
	}
}
