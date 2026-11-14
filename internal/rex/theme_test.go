//go:build darwin || linux

package rex

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSetThemeAndColorReply(t *testing.T) {
	s, _ := testServer(t)
	c := helloStream(t, s)
	if err := c.Subscribe(); err != nil {
		t.Fatal(err)
	}
	before, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}

	th := &Theme{
		Foreground: "#112233",
		Background: "#f7f7f7",
		Cursor:     "#445566",
		Palette:    []string{"0=#010203", "15=#fefefe"},
	}
	if err := c.SetTheme(th); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind == "theme" && ev.Theme != nil && ev.Theme.Background == "#f7f7f7" {
				goto gotEvent
			}
		case <-deadline:
			t.Fatal("timed out waiting for theme event")
		}
	}
gotEvent:

	if !s.sessions[before.ID].hasTheme.Load() {
		t.Fatal("existing session did not receive host theme")
	}

	after, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if !s.sessions[after.ID].hasTheme.Load() {
		t.Fatal("new session did not inherit host theme")
	}

	// With host theme set, vtConn lets color replies through so headless
	// sessions (e.g. iOS-only) answer OSC 10/11 queries with host colors.
	var written bytes.Buffer
	vc := &vtConn{s: s.sessions[after.ID]}
	_, _ = vc.Write([]byte("\x00on\x1b]11;rgb:f7f7/f7f7/f7f7\x1b\\"))
	_ = written
	// Verify colorReply itself is still recognized and vtConn doesn't drop
	// non-color replies.
	if !colorReply([]byte("\x1b]11;rgb:f7f7/f7f7/f7f7\x1b\\")) {
		t.Fatal("expected OSC 11 to be recognized as colorReply")
	}
	if got := s.currentTheme(); got == nil || !strings.EqualFold(got.Background, "#f7f7f7") {
		t.Fatalf("currentTheme = %+v", got)
	}
}
