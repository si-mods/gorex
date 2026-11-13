//go:build darwin || linux

package rex

import (
	"slices"
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

// TestStatusSeenOp checks that "seeStatus" raises the session's seen,
// which the list and an event carry, and that seeing it again is no news.
func TestStatusSeenOp(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.Subscribe(); err != nil {
		t.Fatal(err)
	}
	in, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", `printf '\033]7501;state=done\007'; sleep 60`}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	var done ProgramStatus
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if got := sessionInfo(t, c, in.ID); len(got.Statuses) == 1 {
			done = got.Statuses[0]
			if !got.StatusSeen.IsZero() {
				t.Fatalf("seen %v before any client saw it", got.StatusSeen)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no status record")
		}
	}
	for len(c.Events()) > 0 {
		<-c.Events()
	}
	if err := c.SeeStatus(in.ID, done.Updated); err != nil {
		t.Fatal(err)
	}
	if got := sessionInfo(t, c, in.ID); !got.StatusSeen.Equal(done.Updated) || MostUrgent(got.Statuses, got.StatusSeen) != nil {
		t.Fatalf("seen %v, want %v", got.StatusSeen, done.Updated)
	}
	for deadline := time.After(3 * time.Second); ; {
		select {
		case ev := <-c.Events():
			i := slices.IndexFunc(ev.Sessions, func(es EventSession) bool { return es.ID == in.ID })
			if ev.Kind != "sessions" || i < 0 || !ev.Sessions[i].StatusSeen.Equal(done.Updated) {
				continue // one sent before
			}
		case <-deadline:
			t.Fatal("no event of the seen record")
		}
		break
	}
	if err := c.SeeStatus(in.ID, done.Updated.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-c.Events():
		t.Fatalf("an event for no news: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	if err := c.SeeStatus("nope", done.Updated); err == nil {
		t.Error("seeing a session the server lacks succeeded")
	}
}
