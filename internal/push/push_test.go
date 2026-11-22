package push

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gorex/internal/rex"
)

// TestKeypairPersists: a host's key, once made, is the one the data dir
// keeps; the file is readable by the user only.
func TestKeypairPersists(t *testing.T) {
	dir := t.TempDir()
	k, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.PublicB64() != k.PublicB64() {
		t.Error("a second LoadOrCreate made a new key")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "push-key.json")); !strings.Contains(string(b), `"key"`) {
		t.Errorf("push-key.json: %s", b)
	}
	fi, err := os.Stat(filepath.Join(dir, "push-key.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("push-key.json mode %o, want 600", fi.Mode().Perm())
	}
}

// TestKeypairSigns: the relay checks a request with the public part of
// the key the phone bound, PKIX DER in base64.
func TestKeypairSigns(t *testing.T) {
	k, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"device":"d","event":"update"}`)
	sig, err := k.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	der, err := base64.StdEncoding.DecodeString(k.PublicB64())
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if !ecdsa.VerifyASN1(pub.(*ecdsa.PublicKey), sum[:], sig) {
		t.Error("the signature does not verify")
	}
}

// TestStateForMaps: the content state is TerminalLive.state's on the
// host: the window's title first, the record's program, the most urgent
// record — none when idle — and its message as the line.
func TestStateForMaps(t *testing.T) {
	updated := time.Unix(ReferenceDate+3600, 0)
	info := &rex.SessionInfo{
		Program: "zsh",
		Statuses: []rex.ProgramStatus{
			{ID: "a", State: "working", App: "cargo", Title: "build", Msg: "compiling\nfoo", Progress: 40, Updated: updated},
		},
	}
	st := StateFor(info, "editor", "zsh", "shell", "work")
	if st.Title != "editor" || st.Program != "cargo" || st.Line != "compiling foo" || !st.Connected {
		t.Errorf("state: %+v", st)
	}
	if st.Record == nil || st.Record.State != "working" || *st.Record.Progress != 40 || st.Record.Title != "build" {
		t.Errorf("record: %+v", st.Record)
	}
	if st.Record.Updated == nil || *st.Record.Updated != 3600 {
		t.Errorf("updated: %v, want 3600 seconds since the reference date", st.Record.Updated)
	}

	// Without a window title the block's label wins, then the session's.
	st = StateFor(info, "", "zsh", "shell", "work")
	if st.Title != "shell" {
		t.Errorf("title: %q, want the block's label", st.Title)
	}
	st = StateFor(info, "", "zsh", " ", "work")
	if st.Title != "work" {
		t.Errorf("title: %q, want the session's label", st.Title)
	}

	// An idle record is no record, but its message is still the line,
	// and the block's program stays.
	info.Statuses = []rex.ProgramStatus{{State: "idle", Msg: "at the prompt"}}
	st = StateFor(info, "editor", "zsh", "", "")
	if st.Record != nil || st.Line != "at the prompt" || st.Program != "zsh" {
		t.Errorf("idle: %+v", st)
	}

	// A done record's line is its last message when it reports none.
	info.Statuses = []rex.ProgramStatus{{State: "done", LastMsg: "tests passed", Updated: updated}}
	st = StateFor(info, "editor", "zsh", "", "")
	if st.Record == nil || st.Record.State != "done" || st.Line != "tests passed" {
		t.Errorf("done: %+v", st)
	}

	// A done record the user saw does not count: the most urgent is nil,
	// and so are the record and the line.
	info.StatusSeen = updated.Add(time.Minute)
	st = StateFor(info, "editor", "zsh", "", "")
	if st.Record != nil || st.Line != "" {
		t.Errorf("seen: %+v", st)
	}
}

// TestCutLimits: cut holds both limits, whole characters only, counting
// a cluster — an accented letter, an emoji family, a flag — as one
// character, as TerminalActivity.cut does.
func TestCutLimits(t *testing.T) {
	if got := cut("hello", 3, 400); got != "hel" {
		t.Errorf("cut by chars: %q", got)
	}
	if got := cut("hello", 200, 4); got != "hell" {
		t.Errorf("cut by bytes: %q", got)
	}
	// "é" as e + a combining acute is one character of three bytes.
	if got := cut("e\u0301abc", 2, 400); got != "e\u0301a" {
		t.Errorf("combining mark: %q", got)
	}
	// A family emoji (ZWJ-joined) is one character; three of them fill
	// three places but not the bytes of a fourth.
	fam := "👨\u200d👩\u200d👧\u200d👦"
	if got := cut(fam+fam+fam+fam, 3, 400); got != fam+fam+fam {
		t.Errorf("emoji family: %q", got)
	}
	if got := cut(fam+fam, 200, len(fam)+1); got != fam {
		t.Errorf("emoji family by bytes: %q", got)
	}
	// A flag is a pair of regional indicators, one character.
	flag := "🇬🇧"
	if got := cut(flag+flag+"ab", 3, 400); got != flag+flag+"a" {
		t.Errorf("flags: %q", got)
	}
	if cut("", 1, 1) != "" {
		t.Error("empty")
	}
}

// TestPushRequestSize: the longest state stays within the relay's 4 KB
// bodies.
func TestPushRequestSize(t *testing.T) {
	long := strings.Repeat("界", MaxText)     // 600 bytes of CJK
	longLine := strings.Repeat("界", MaxLine) // 240 bytes
	updated := 1.5
	progress := 42
	st := ContentState{
		Title:   long,
		Program: "claude-code",
		Record: &Record{
			State: "blocked", Kind: "permission", Title: long,
			Progress: &progress, Updated: &updated,
		},
		Line:      longLine,
		Connected: true,
	}
	body, err := json.Marshal(PushRequest{
		Device:        base64.StdEncoding.EncodeToString(make([]byte, 200)),
		Event:         "update",
		ContentState:  st,
		StaleDate:     1,
		DismissalDate: 1,
		Alert:         &Alert{Title: long, Body: long},
		Priority:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 4096 {
		t.Errorf("a worst-case request is %d bytes, over the relay's 4 KB", len(body))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"device", "event", "content_state", "stale_date", "dismissal_date", "alert", "priority"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("no %s in %s", name, body)
		}
	}
}

// fakeRelay records what the sender posts, request by request.
type fakeRelay struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	sig  string
	body []byte
}

func newFakeRelay(t *testing.T) *fakeRelay {
	f := &fakeRelay{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body [4096]byte
		n, _ := r.Body.Read(body[:])
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{sig: r.Header.Get("X-Gorex-Sig"), body: body[:n]})
		f.mu.Unlock()
		w.Write([]byte(`{"sent":1}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// requests returns the pushes posted so far, as JSON.
func (f *fakeRelay) requests(t *testing.T) []map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.reqs))
	for _, r := range f.reqs {
		var m map[string]any
		if err := json.Unmarshal(r.body, &m); err != nil {
			t.Fatalf("the request is not JSON: %s", r.body)
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeRelay) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// last returns the newest push's JSON once one arrived.
func (f *fakeRelay) last(t *testing.T) map[string]any {
	t.Helper()
	waitPush(t, "a push", func() bool { return f.count() > 0 })
	rs := f.requests(t)
	return rs[len(rs)-1]
}

func waitPush(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// testSender is a sender on the fake relay with a manual clock: after
// collects the waits, run fires them.
type testSender struct {
	*Sender
	relay *fakeRelay
	now   time.Time
	tasks []func()
}

func newTestSender(t *testing.T) *testSender {
	relay := newFakeRelay(t)
	s := &testSender{relay: relay, now: time.Unix(1000, 0)}
	s.Sender = NewSender(relay.srv.URL, t.TempDir(), func(string, ...any) {})
	s.Sender.now = func() time.Time { return s.now }
	s.Sender.after = func(_ time.Duration, f func()) { s.tasks = append(s.tasks, f) }
	return s
}

// advance moves the clock and runs the waits that came due.
func (s *testSender) advance(d time.Duration) {
	s.now = s.now.Add(d)
	tasks := s.tasks
	s.tasks = nil
	for _, f := range tasks {
		f()
	}
}

func deviceKey(t *testing.T) string {
	k, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return k.PublicB64()
}

// TestSenderCoalesces: updates within the coalescing window join to one,
// the newest state.
func TestSenderCoalesces(t *testing.T) {
	ts := newTestSender(t)
	dev := deviceKey(t)
	if _, err := ts.Register("conn", dev, "s", "b"); err != nil {
		t.Fatal(err)
	}
	info := &rex.SessionInfo{Program: "zsh", Statuses: []rex.ProgramStatus{
		{State: "working", Msg: "one", Updated: ts.now},
	}}
	ts.Changed("b", info, "w", "zsh", "", "s")
	waitPush(t, "the first push", func() bool { return ts.relay.count() == 1 })

	// Two changes inside the window post one update, the newest state.
	info.Statuses[0].Msg = "two"
	ts.Changed("b", info, "w", "zsh", "", "s")
	info.Statuses[0].Msg = "three"
	ts.Changed("b", info, "w", "zsh", "", "s")
	if len(ts.tasks) != 1 {
		t.Fatalf("no coalesced send waits: %d", len(ts.tasks))
	}
	ts.advance(3 * time.Second)
	waitPush(t, "the coalesced push", func() bool { return ts.relay.count() == 2 })
	last := ts.relay.last(t)
	cs := last["content_state"].(map[string]any)
	if cs["line"] != "three" || last["device"] != dev || last["event"] != "update" || last["priority"] != float64(5) {
		t.Errorf("coalesced push: %v", last)
	}
	if stale := last["stale_date"].(float64); stale != float64(ts.now.Add(StaleAfter).Unix()) {
		t.Errorf("stale_date %v, want now + 3min", stale)
	}
}

// TestSenderAlerts: a record that newly turns blocked goes at once, at
// priority 10, inside the coalescing window too.
func TestSenderAlerts(t *testing.T) {
	ts := newTestSender(t)
	ts.Register("conn", deviceKey(t), "s", "b")
	info := &rex.SessionInfo{Statuses: []rex.ProgramStatus{
		{ID: "r", State: "working", Msg: "one"},
	}}
	ts.Changed("b", info, "w", "", "", "")
	waitPush(t, "the working push", func() bool { return ts.relay.count() == 1 })

	info.Statuses[0].State = "blocked"
	info.Statuses[0].Msg = "needs input"
	ts.Changed("b", info, "w", "", "", "")
	waitPush(t, "the alert", func() bool { return ts.relay.count() == 2 })
	last := ts.relay.last(t)
	if last["priority"] != float64(10) || last["content_state"].(map[string]any)["record"].(map[string]any)["state"] != "blocked" {
		t.Errorf("alert push: %v", last)
	}

	// The same (id, state) again is no new alert.
	ts.advance(3 * time.Second)
	info.Statuses[0].Msg = "still waiting"
	ts.Changed("b", info, "w", "", "", "")
	waitPush(t, "the coalesced push", func() bool { return ts.relay.count() == 3 })
	if got := ts.relay.last(t)["priority"]; got != float64(5) {
		t.Errorf("a repeated blocked is coalesced, priority %v", got)
	}
}

// TestDetachedPush: a connection's end sends a last update, connected
// false, stale now.
func TestDetachedPush(t *testing.T) {
	ts := newTestSender(t)
	dev := deviceKey(t)
	ts.Register("conn", dev, "s", "b")
	info := &rex.SessionInfo{Statuses: []rex.ProgramStatus{{State: "working", Msg: "one"}}}
	ts.Changed("b", info, "w", "", "", "")
	waitPush(t, "the first push", func() bool { return ts.relay.count() == 1 })

	ts.Detached("conn")
	waitPush(t, "the last push", func() bool { return ts.relay.count() == 2 })
	last := ts.relay.last(t)
	cs := last["content_state"].(map[string]any)
	if cs["connected"] != false || cs["line"] != "one" {
		t.Errorf("the last push: %v", last)
	}
	if stale := last["stale_date"].(float64); stale != float64(ts.now.Unix()) {
		t.Errorf("stale_date %v, want now", stale)
	}

	// A change after the end goes nowhere.
	info.Statuses[0].Msg = "two"
	ts.Changed("b", info, "w", "", "", "")
	ts.advance(3 * time.Second)
	if n := ts.relay.count(); n != 2 {
		t.Errorf("a change after detach pushed, %d requests", n)
	}
}

// TestUnregisterPush: an unregistered connection gets no more updates.
func TestUnregisterPush(t *testing.T) {
	ts := newTestSender(t)
	ts.Register("conn", deviceKey(t), "s", "b")
	ts.Unregister("conn")
	ts.Changed("b", &rex.SessionInfo{Statuses: []rex.ProgramStatus{{State: "blocked"}}}, "w", "", "", "")
	ts.advance(3 * time.Second)
	if n := ts.relay.count(); n != 0 {
		t.Errorf("an unregistered connection pushed, %d requests", n)
	}
}

// TestNoRelay: without a relay the sender registers and does nothing.
func TestNoRelay(t *testing.T) {
	s := NewSender("", t.TempDir(), nil)
	if s.Relay() != "" {
		t.Error("relay URL with no relay")
	}
	if _, err := s.Register("conn", "device", "s", "b"); err != nil {
		t.Fatal(err)
	}
	s.Changed("b", &rex.SessionInfo{Statuses: []rex.ProgramStatus{{State: "blocked"}}}, "w", "", "", "")
	s.Detached("conn")
}

// TestSenderSigns: the request's signature verifies against the host
// key register answers with.
func TestSenderSigns(t *testing.T) {
	ts := newTestSender(t)
	host, err := ts.Register("conn", deviceKey(t), "s", "b")
	if err != nil {
		t.Fatal(err)
	}
	ts.Changed("b", &rex.SessionInfo{Statuses: []rex.ProgramStatus{{State: "blocked"}}}, "w", "", "", "")
	waitPush(t, "the push", func() bool { return ts.relay.count() == 1 })

	ts.relay.mu.Lock()
	r := ts.relay.reqs[0]
	ts.relay.mu.Unlock()
	sig, err := base64.StdEncoding.DecodeString(r.sig)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := base64.StdEncoding.DecodeString(host)
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(r.body)
	if !ecdsa.VerifyASN1(pub.(*ecdsa.PublicKey), sum[:], sig) {
		t.Error("the push's signature does not verify")
	}
}
