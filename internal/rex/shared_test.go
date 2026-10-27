//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// TestSizeLock takes a session's size from a phone's control connection:
// the desktop's resizes are ignored until the phone unlocks, its
// connection closes, its lease ends, or the desktop takes the size back.
func TestSizeLock(t *testing.T) {
	defer sizeLease.set(sizeLease.set(300 * time.Millisecond))
	s, c, sid := streamServer(t, nil)
	lock := func(owner string, cols, rows int) *Client {
		t.Helper()
		pc := pipe(s, "")
		pc.keepalive = true
		if _, err := pc.Hello(); err != nil {
			t.Fatal(err)
		}
		if err := pc.LockSize(sid, owner, cols, rows); err != nil {
			t.Fatal(err)
		}
		return pc
	}
	info := func() SessionInfo {
		t.Helper()
		for _, in := range must(c.List()) {
			if in.ID == sid {
				return in
			}
		}
		t.Fatal("no session")
		return SessionInfo{}
	}
	// waitUnlocked waits for the lock to go, as the connection closing
	// reaches the server.
	waitUnlocked := func() {
		t.Helper()
		for range 100 {
			if info().SizeLock == "" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("still locked")
	}
	call := func(op, owner string, cols, rows int) error {
		return c.call(Request{Op: op, SID: sid, Owner: owner, Cols: cols, Rows: rows}, nil)
	}

	if err := c.LockSize(sid, "desk", 40, 20); err == nil {
		t.Error("locked on a connection not kept alive")
	}
	phone := lock("phone", 40, 20)
	if in := info(); in.SizeLock != "phone" || in.Cols != 40 || in.Rows != 20 {
		t.Fatalf("locked: %+v", in)
	}
	c.Resize(sid, 100, 30)
	if got := sessionSize(t, c, sid); got != "40x20" {
		t.Errorf("a window resized a locked session to %s", got)
	}
	if err := call("lockedResize", "other", 50, 25); err == nil {
		t.Error("another owner resized")
	}
	if err := call("lockedResize", "phone", 45, 22); err != nil || sessionSize(t, c, sid) != "45x22" {
		t.Errorf("the owner's resize: %v, %s", err, sessionSize(t, c, sid))
	}
	// Another connection takes the lock: the old one closing leaves it,
	// the new one closing ends it.
	phone2 := lock("phone2", 50, 25)
	phone.Close()
	time.Sleep(100 * time.Millisecond)
	if in := info(); in.SizeLock != "phone2" || in.Cols != 50 {
		t.Fatalf("an old connection closing unlocked: %+v", in)
	}
	phone2.Close()
	waitUnlocked()
	c.Resize(sid, 100, 30)
	if got := sessionSize(t, c, sid); got != "100x30" {
		t.Errorf("unlocked, the window's size is %s", got)
	}

	// The desktop takes the size back, at its own; another owner's unlock
	// leaves it.
	phone3 := lock("phone", 40, 20)
	defer phone3.Close()
	if err := c.UnlockSize(sid, "other", 0, 0); err != nil || info().SizeLock != "phone" {
		t.Errorf("unlocked by another owner: %v, %+v", err, info())
	}
	if err := c.UnlockSize(sid, "", 90, 30); err != nil || info().SizeLock != "" || sessionSize(t, c, sid) != "90x30" {
		t.Errorf("unlocked by the desktop: %v, %+v", err, info())
	}

	// A lease ends unless renewed, the connection still open.
	phone4 := lock("phone", 40, 20)
	defer phone4.Close()
	for range 3 {
		time.Sleep(150 * time.Millisecond)
		if err := call("renewSize", "phone", 0, 0); err != nil {
			t.Fatalf("renewing: %v", err)
		}
	}
	waitUnlocked()
	if err := call("renewSize", "phone", 0, 0); err == nil {
		t.Error("renewed a lease that ended")
	}
}

// TestBindSamePeer checks that an attach binds only to a control
// connection of its own peer: one bound answers the program's queries
// while that connection holds the size.
func TestBindSamePeer(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	local.keepalive = true
	h, err := local.Hello()
	if err != nil || h.Conn == "" {
		t.Fatalf("hello %+v %v", h, err)
	}
	b := pipe(s, "")
	if _, ok := s.bind(h.Conn, must(b.dial()), "nodekey:other"); ok {
		t.Error("a remote peer bound to a local connection")
	}
	if _, ok := s.bind(h.Conn, must(b.dial()), ""); !ok {
		t.Error("a local attach did not bind")
	}
}

// TestReplyAuthority checks that only one client's terminal answers the
// program's queries, while every client types.
func TestReplyAuthority(t *testing.T) {
	s := &session{clients: map[*attached]struct{}{}}
	deskConn, phoneConn := net.Pipe()
	defer deskConn.Close()
	desk, phone := &attached{conn: deskConn, seq: 1}, &attached{conn: phoneConn, seq: 2}
	s.clients[desk], s.clients[phone] = struct{}{}, struct{}{}
	reply := "\x1b[12;40R\x1b[?62;22c\x1b[0n\x1b]11;rgb:0000/0000/0000\x1b\\\x1b]10;rgb:ffff/ffff/ffff\x07\x1bP>|GoRex\x1b\\\x1b[?2026;2$y\x1b[?1u"
	check := func(what string, a *attached, in, want string) {
		t.Helper()
		if got := string(s.admit(a, []byte(in))); got != want {
			t.Errorf("%s: %q, want %q", what, got, want)
		}
	}
	check("nobody typed: the newest answers", phone, reply, reply)
	check("nobody typed: the others' answers go", desk, reply, "")
	check("the desktop types", desk, "ls\r", "ls\r")
	check("the desktop typed last, and answers", desk, reply, reply)
	check("the phone's answers go", phone, reply, "")
	// The phone holds the size: it answers, whoever types.
	phone.via = phoneConn
	s.sizeLock, s.sizeLockConn = "phone", phoneConn
	check("typed on the desktop with answers", desk, "a"+reply+"\x1b[A\x1b[1;5D", "a\x1b[A\x1b[1;5D")
	check("the owner answers", phone, reply, reply)
	check("a part of an answer stays", desk, "\x1b[12;4", "\x1b[12;4")
}

// TestListETag polls the list with its ETag: it comes again only once
// it changed.
func TestListETag(t *testing.T) {
	_, c, sid := streamServer(t, nil)
	// The list changes as the shell starts, printing.
	etag := ""
	for deadline := time.Now().Add(5 * time.Second); ; {
		list, next, changed, err := c.ListSince(etag)
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			if list != nil || next != etag {
				t.Fatalf("unchanged: %v %q", list, next)
			}
			break
		}
		if next == "" || len(list) != 1 {
			t.Fatalf("list %v, etag %q", list, next)
		}
		if time.Now().After(deadline) {
			t.Fatal("the list never settled")
		}
		etag = next
		time.Sleep(100 * time.Millisecond)
	}
	c.Resize(sid, 90, 30)
	list, next, changed, err := c.ListSince(etag)
	if err != nil || !changed || next == etag || len(list) != 1 || list[0].Cols != 90 {
		t.Errorf("resized: %v %q %v %v", list, next, changed, err)
	}
}

// TestBatchWait checks that an attach waits for the synchronized update
// the output is in to end, and not much longer than batchWait for one
// that does not.
func TestBatchWait(t *testing.T) {
	s := &session{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch([]byte("a\x1b[?20"))
	s.batch([]byte("26hframe"))
	if s.batchEnd == nil {
		t.Fatal("missed the start of an update cut in two")
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		s.mu.Lock()
		s.batch([]byte("end\x1b[?2026l"))
		s.mu.Unlock()
	}()
	start := time.Now()
	s.waitBatchLocked()
	if d := time.Since(start); s.batchEnd != nil || d < 20*time.Millisecond || d >= batchWait {
		t.Errorf("waited %v for an update that ended", d)
	}
	s.batch([]byte("\x1b[?2026h"))
	start = time.Now()
	s.waitBatchLocked()
	if d := time.Since(start); d < batchWait || d > batchWait+time.Second {
		t.Errorf("waited %v for an update that did not end", d)
	}
}

// TestTrimSnapshot cuts the scrollback of a real snapshot: the modes at
// its start, the screen and the cursor at its end stay, as a terminal fed
// the cut snapshot shows.
func TestTrimSnapshot(t *testing.T) {
	vt := func() *terminal.Terminal {
		pr, _ := io.Pipe()
		v, err := terminal.New(terminal.Options{Conn: discard{pr}, Scrollback: scrollback})
		if err != nil {
			t.Fatal(err)
		}
		v.Resize(40, 6)
		t.Cleanup(func() { v.Close() })
		return v
	}
	src := vt()
	var out strings.Builder
	out.WriteString("\x1b[?2004h\x1b[?1h\x1b[?25l")
	for i := range 500 {
		fmt.Fprintf(&out, "\x1b[3%dmline %d\x1b[0m\r\n", i%7, i)
	}
	out.WriteString("last\x1b[3;5H\x1b[1m")
	src.Feed([]byte(out.String()))
	snap := src.Snapshot()
	cut, ok := trimSnapshot(snap, 6, 1000)
	if !ok || len(cut) > 1000 || len(cut) < 800 {
		t.Fatalf("cut %d of %d bytes, %v", len(cut), len(snap), ok)
	}
	home := bytes.Index(snap, []byte("\x1b[H")) + 3
	if !bytes.HasPrefix(cut, snap[:home]) || !bytes.Contains(cut[:home], []byte("\x1b[?2004h")) || !bytes.Contains(cut[:home], []byte("\x1b[?25l")) {
		t.Errorf("modes lost: %q", cut[:min(len(cut), 120)])
	}
	if !bytes.HasSuffix(snap, cut[home:]) || !bytes.HasPrefix(cut[home:], []byte("\x1b[0m")) {
		t.Errorf("not cut at a row: %q", cut[home:min(len(cut), home+40)])
	}
	// Fed to a terminal, it shows what the session shows, with less above.
	dst := vt()
	dst.Feed(cut)
	if got := dst.Snapshot(); !bytes.Equal(got, cut) {
		t.Errorf("the cut snapshot shows otherwise:\n%q\n%q", got, cut)
	}
	if want := src.Text()[len(src.Text())-200:]; !strings.HasSuffix(dst.Text(), want) {
		t.Errorf("screen %q, want %q", dst.Text(), want)
	}
	// The screen alone is never cut, nor what fits.
	if _, ok := trimSnapshot(snap, 1000, 10); ok {
		t.Error("cut the screen")
	}
	if _, ok := trimSnapshot(snap, 6, len(snap)); ok {
		t.Error("cut what fits")
	}
}

func TestConnectExistingStartsNone(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	if _, err := ConnectExisting(); err != errNotRunning {
		t.Fatalf("connected: %v", err)
	}
	if _, err := os.Stat(SocketPath()); err == nil {
		t.Error("a server started")
	}
}
