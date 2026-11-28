package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// TestMain keeps the tests from asking the app its appearance: no app runs,
// and mygo.Theme.IsDark would wait for its main loop.
func TestMain(m *testing.M) {
	bindAppCommands()
	appearanceDark = func() bool { return false }
	os.Exit(m.Run())
}

// replyConn is a terminal's Conn that keeps the replies it is sent and
// reads nothing.
type replyConn struct {
	mu  sync.Mutex
	out strings.Builder
}

func (c *replyConn) Read([]byte) (int, error) { select {} }
func (c *replyConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.Write(p)
}
func (c *replyConn) Close() error { return nil }
func (c *replyConn) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

// TestTermAnswersBackgroundOfAppearanceBeforeView: a program asking the
// background (OSC 11) before a view shows its terminal gets the one of the
// app's appearance, not the light theme's in a dark app (#656), and the
// terminal keeps both themes for its view to pick from.
func TestTermAnswersBackgroundOfAppearanceBeforeView(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Skipf("libghostty-vt: %v", err)
	}
	old := appearanceDark
	t.Cleanup(func() { appearanceDark = old })
	for _, dark := range []bool{false, true} {
		appearanceDark = func() bool { return dark }
		conn := &replyConn{}
		opts := termOptions()
		opts.Conn = conn
		term, err := newTerm(opts)
		if err != nil {
			t.Fatal(err)
		}
		term.Feed([]byte("\x1b]11;?\x1b\\"))
		want := termLight.Background
		if dark {
			want = termDark.Background
		}
		hex := fmt.Sprintf("rgb:%02x%02x/%02x%02x/%02x%02x", want.R, want.R, want.G, want.G, want.B, want.B)
		deadline := time.Now().Add(2 * time.Second)
		for !strings.Contains(conn.String(), "]11;") && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := conn.String(); !strings.Contains(got, hex) {
			t.Errorf("dark=%v: OSC 11 reply %q, want %s", dark, got, hex)
		}
		term.Close()
	}
}
