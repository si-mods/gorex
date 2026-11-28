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

// TestStatusQueryBeforeDA1 checks that the server's OSC 7501 and OSC 7502
// query replies reach the program before the trailing DA1 (CSI c) sentinel,
// whether the server's headless emulator or an attached window answers DA1.
func TestStatusQueryBeforeDA1(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		reply string
	}{
		{"osc7501", `\033]7501;?\033\\`, `^[]7501;?^[\`},
		{"osc7502", `\033]7502;?\033\\`, `^[]7502;?^[\`},
	} {
		for _, withWindow := range []bool{false, true} {
			sub := tc.name + "/no-window"
			if withWindow {
				sub = tc.name + "/window"
			}
			t.Run(sub, func(t *testing.T) {
				s, c, sid := streamServerCommand(t, nil, answerScript(tc.query+`\033[c`), "/tmp")
				if withWindow {
					cl := helloStream(t, s)
					conn, err := cl.AttachConn(Attach{SID: sid, Cols: 80, Rows: 24})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					go answer(conn, 80, 24)
				}
				if err := c.Input(sid, []byte("go\n")); err != nil {
					t.Fatal(err)
				}
				ready(t, c, sid)
				var out string
				for deadline := time.Now().Add(8 * time.Second); ; time.Sleep(20 * time.Millisecond) {
					out = string(must(c.Capture(sid, 0)).Screen)
					if strings.Contains(out, "END") {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("no END in %q", out)
					}
				}
				qi := strings.Index(out, tc.reply)
				di := daAnswer.FindStringIndex(out)
				if qi < 0 || di == nil {
					t.Fatalf("missing reply (osc=%d, da=%v) in %q", qi, di, out)
				}
				if qi > di[0] {
					t.Fatalf("DA1 reply arrived before %s reply (osc=%d, da=%d) in %q", tc.name, qi, di[0], out)
				}
			})
		}
	}
}

// TestStatusSeenOp checks that "seeStatus" raises the session's seen,
// which the list and an event carry, and that seeing it again is no news.
// The session prints nothing, and the test gives it its record, so that
// nothing else tells of a change on its own: neither its output (and the
// sampler it asks) nor the notify of a record the list already shows
// (#1160). Each step reads the events up to a fence, so none from before
// is still on its way.
func TestStatusSeenOp(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.SubscribeTransfers(); err != nil {
		t.Fatal(err)
	}
	in, err := c.Create(CreateOptions{Command: []string{"sleep", "60"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	ss := must(s.session(in.ID))
	ss.mu.Lock()
	ss.status.feed([]byte("\x1b]7501;state=done\x07"))
	ss.mu.Unlock()
	got := sessionInfo(t, c, in.ID)
	if len(got.Statuses) != 1 || !got.StatusSeen.IsZero() {
		t.Fatalf("statuses %+v, seen %v before any client saw them", got.Statuses, got.StatusSeen)
	}
	done := got.Statuses[0]
	fence(t, s, c)

	if err := c.SeeStatus(in.ID, done.Updated); err != nil {
		t.Fatal(err)
	}
	if got := sessionInfo(t, c, in.ID); !got.StatusSeen.Equal(done.Updated) || MostUrgent(got.Statuses, got.StatusSeen) != nil {
		t.Fatalf("seen %v, want %v", got.StatusSeen, done.Updated)
	}
	told := slices.ContainsFunc(fence(t, s, c), func(ev Event) bool {
		i := slices.IndexFunc(ev.Sessions, func(es EventSession) bool { return es.ID == in.ID })
		return ev.Kind == "sessions" && i >= 0 && ev.Sessions[i].StatusSeen.Equal(done.Updated)
	})
	if !told {
		t.Fatal("no event of the seen record")
	}

	if err := c.SeeStatus(in.ID, done.Updated.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, ev := range fence(t, s, c) {
		if ev.Kind == "sessions" {
			t.Fatalf("an event for no news: %+v", ev)
		}
	}
	if err := c.SeeStatus("nope", done.Updated); err == nil {
		t.Error("seeing a session the server lacks succeeded")
	}
}

// fence returns the events c, which subscribed to the transfers, reads
// before a transfer event of its own: the server writes a subscriber's
// events in the order of their changes, a burst's transfers last, so
// none of the changes before the fence is still on its way after it.
func fence(t *testing.T, s *Server, c *Client) []Event {
	t.Helper()
	id := newID()
	s.transferred(Transfer{ID: id, SID: "fence", Direction: "put", State: "done"})
	var evs []Event
	for deadline := time.After(5 * time.Second); ; {
		select {
		case ev := <-c.Events():
			if ev.Kind == "transfer" && ev.Transfer != nil && ev.Transfer.ID == id {
				return evs
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatal("no fence")
		}
	}
}
