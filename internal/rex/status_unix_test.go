//go:build darwin || linux

package rex

import (
	"strings"
	"testing"
	"time"
)

// TestStatusSession checks that the server answers a program's query
// once, and lists what it reported.
func TestStatusSession(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	// cat shows what the program reads, until nothing comes for 2s.
	script := `stty -echo -icanon min 0 time 20; printf '\033]7501;?\033\\'; ` +
		`printf '\033]7501;state=done:msg=b2s=\007'; cat -v; echo END`
	info, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", script}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	st := c.Stream(info.ID, 80, 24)
	defer st.Close()
	out := readUntil(t, st, `END`)
	if n := strings.Count(out, `^[]7501;?^[\`); n != 1 {
		t.Fatalf("%d answers in %q", n, out)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if l, _ := c.List(); len(l) == 1 && l[0].Exited {
			if p := l[0].Status; p == nil || p.State != "done" || p.Msg != "ok" {
				t.Fatalf("status %+v", p)
			}
			return
		}
	}
	t.Fatal("the session did not exit")
}
