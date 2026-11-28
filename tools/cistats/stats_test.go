package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []RunJobs {
	t.Helper()
	b, err := os.ReadFile("testdata/runs.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw []struct {
		Run  Run   `json:"run"`
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	var out []RunJobs
	for _, r := range raw {
		out = append(out, RunJobs{r.Run, r.Jobs})
	}
	return out
}

var testWindow = []string{"2026-11-25", "2026-11-26", "2026-11-27"}

func TestAggregate(t *testing.T) {
	st := aggregate(fixture(t), testWindow)
	// Starts: 3 CI linux, PR verified once (the skipped one does not count),
	// Maintenance once, test-windows never. Run 7 is outside the window.
	want := []int{0, 1, 4}
	for i, n := range want {
		if st.PerDay[i] != n {
			t.Errorf("day %s: %d starts, want %d", testWindow[i], st.PerDay[i], n)
		}
	}
	if st.PeakHour != 3 || st.PeakHourAt != "2026-11-27 10:00" {
		t.Errorf("peak hour %d at %s", st.PeakHour, st.PeakHourAt)
	}
	if st.PeakMin != 3 || st.PeakMinAt != "2026-11-27 10:00" {
		t.Errorf("peak minute %d at %s", st.PeakMin, st.PeakMinAt)
	}
	if st.PerJob["CI / linux"] != 3 || st.PerJob["PR verified / verified"] != 1 || st.PerJob["CI / test-windows"] != 0 {
		t.Errorf("per job %v", st.PerJob)
	}
	if st.LinuxOK != 2 || st.LinuxFail != 1 {
		t.Errorf("linux ok %d fail %d", st.LinuxOK, st.LinuxFail)
	}
	if got := st.Steps["web test"]; got.ran != 1 || got.skipped != 2 {
		t.Errorf("web test steps %+v", got)
	}
	if _, ok := st.Steps["Set up job"]; ok {
		t.Error("counted a step that is not a ciplan step")
	}
	pr := st.Linux["pull_request"]
	if len(pr) != 2 || pr[0] != 5*time.Minute || pr[1] != 12*time.Minute {
		t.Errorf("pull_request durations %v", pr)
	}
}

func TestPercentile(t *testing.T) {
	d := durations{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if d.percentile(0.5) != 5 || d.percentile(0.9) != 9 || d.percentile(1) != 10 {
		t.Errorf("p50 %v p90 %v p100 %v", d.percentile(0.5), d.percentile(0.9), d.percentile(1))
	}
	if (durations{}).percentile(0.9) != 0 {
		t.Error("empty")
	}
}

func TestFlags(t *testing.T) {
	st := aggregate(fixture(t), testWindow)
	if f := st.Flags(); len(f) != 0 {
		t.Errorf("flags %v, want none", f)
	}
	st.PerDay[2] = maxDay + 1
	st.PeakHour, st.PeakHourAt = maxHour+1, "h"
	st.PeakMin, st.PeakMinAt = maxMinute+1, "m"
	st.LinuxAll = durations{maxP90 + time.Second}
	if f := st.Flags(); len(f) != 4 {
		t.Errorf("flags %v, want 4", f)
	}
	st.PerDay[2] = maxDay
	st.PeakHour, st.PeakMin = maxHour, maxMinute
	st.LinuxAll = durations{maxP90}
	if f := st.Flags(); len(f) != 0 {
		t.Errorf("at the threshold: flags %v, want none", f)
	}
}

func TestRender(t *testing.T) {
	text := render(aggregate(fixture(t), testWindow), "owner/repo")
	for _, s := range []string{"### Flags\n\nnone", "| 2026-11-27 | 4 |", "| `CI / linux` | 3 |", "| pull_request | 2 |", "| web test | 1 | 2 |", "1 of 3 finished jobs failed (33.3%)"} {
		if !strings.Contains(text, s) {
			t.Errorf("report lacks %q:\n%s", s, text)
		}
	}
	empty := render(aggregate(nil, testWindow), "owner/repo")
	if !strings.Contains(empty, "No finished linux job") {
		t.Errorf("empty report:\n%s", empty)
	}
}

func TestWindowDays(t *testing.T) {
	// Today (28th) is not in the window: only days that have ended.
	got := windowDays(time.Date(2026, 11, 28, 5, 0, 0, 0, time.UTC), 3)
	if strings.Join(got, ",") != "2026-11-25,2026-11-26,2026-11-27" {
		t.Errorf("window %v", got)
	}
}

func TestCollectAndComment(t *testing.T) {
	var posted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		switch {
		case r.Method == "POST":
			var m map[string]string
			json.NewDecoder(r.Body).Decode(&m)
			posted = r.URL.Path + " " + m["body"]
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			if r.URL.Query().Get("created") == "2026-11-27" {
				w.Write([]byte(`{"workflow_runs":[{"id":1,"name":"CI","event":"push"}]}`))
			} else {
				w.Write([]byte(`{"workflow_runs":[]}`))
			}
		case strings.HasSuffix(r.URL.Path, "/runs/1/jobs"):
			w.Write([]byte(`{"jobs":[{"name":"linux","labels":["instabox-large"],"conclusion":"success","runner_name":"b","started_at":"2026-11-27T10:00:00Z","completed_at":"2026-11-27T10:01:00Z","steps":[]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newClient(srv.URL, "tok", srv.Client())
	data, err := c.collect("o/r", []string{"2026-11-26", "2026-11-27"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 || len(data[0].Jobs) != 1 || !isStart(data[0].Jobs[0]) {
		t.Fatalf("collected %+v", data)
	}
	if err := c.comment("o/r", 7, "hello"); err != nil || posted != "/repos/o/r/issues/7/comments hello" {
		t.Errorf("comment: %v, posted %q", err, posted)
	}
}

func TestRateLimitStops(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("slow down"))
	}))
	defer srv.Close()
	c := testClient(srv)
	_, err := c.collect("o/r", []string{"2026-11-26", "2026-11-27"})
	if err == nil || !strings.Contains(err.Error(), "429") || calls != 1 {
		t.Errorf("err %v after %d calls, want a 429 error after 1 call", err, calls)
	}
}

func testClient(srv *httptest.Server) *client {
	c := newClient(srv.URL, "tok", srv.Client())
	c.sleep = func(time.Duration) {}
	c.logf = func(string, ...any) {}
	return c
}

func TestWeeklyWindow(t *testing.T) {
	// The Monday run covers Monday to Sunday of the week before.
	got := windowDays(time.Date(2026, 11, 30, 3, 17, 0, 0, time.UTC), 7)
	if got[0] != "2026-11-23" || got[6] != "2026-11-29" {
		t.Errorf("window %v", got)
	}
	if time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC).Weekday() != time.Monday {
		t.Fatal("test date is not a Monday")
	}
}

func TestCollectListsDayBeforeWindow(t *testing.T) {
	var days []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/actions/runs") {
			days = append(days, r.URL.Query().Get("created"))
		}
		w.Write([]byte(`{"workflow_runs":[]}`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).collect("o/r", []string{"2026-11-26", "2026-11-27"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(days, ",") != "2026-11-25,2026-11-26,2026-11-27" {
		t.Errorf("listed %v", days)
	}
}

func TestRateLimitWaitsWhenLow(t *testing.T) {
	now := time.Unix(1000, 0)
	var slept []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "19")
		w.Header().Set("X-RateLimit-Reset", "1060")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	c.now = func() time.Time { return now }
	c.sleep = func(d time.Duration) { slept = append(slept, d) }
	var logged string
	c.logf = func(f string, a ...any) { logged = fmt.Sprintf(f, a...) }
	if err := c.get("/x", url.Values{}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 65*time.Second {
		t.Errorf("slept %v, want 65s (reset in 60s plus 5s)", slept)
	}
	if logged != "rate limit: waiting until 1970-01-01T00:17:45Z" {
		t.Errorf("logged %q", logged)
	}
}

func TestRateLimitNoWaitWhenEnough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "20")
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	c.sleep = func(time.Duration) { t.Error("slept") }
	if err := c.get("/x", url.Values{}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
}

func TestRateLimitTooLongWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1000000")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	c.now = func() time.Time { return time.Unix(1000000-66*60, 0) }
	c.sleep = func(time.Duration) { t.Error("slept") }
	if err := c.get("/x", url.Values{}, &struct{}{}); err == nil || !strings.Contains(err.Error(), "resets at") {
		t.Errorf("err %v", err)
	}
}

func TestRateLimited403WaitsOnceAndRetries(t *testing.T) {
	calls, sleeps := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1060")
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	c.now = func() time.Time { return time.Unix(1000, 0) }
	c.sleep = func(time.Duration) { sleeps++ }
	if err := c.get("/x", url.Values{}, &struct{}{}); err != nil || calls != 2 || sleeps != 1 {
		t.Errorf("err %v, %d calls, %d sleeps", err, calls, sleeps)
	}
	// A second limit on the same request is an error.
	calls = -100
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1060")
		w.WriteHeader(429)
	}))
	defer srv2.Close()
	c2 := testClient(srv2)
	c2.now = c.now
	if err := c2.get("/x", url.Values{}, nil); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("err %v", err)
	}
}

func TestOther403Stops(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "500")
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c := testClient(srv)
	c.sleep = func(time.Duration) { t.Error("slept") }
	if err := c.get("/x", url.Values{}, nil); err == nil || calls != 1 {
		t.Errorf("err %v after %d calls", err, calls)
	}
}

// runsServer answers the run list: full(created) pages of 100 runs for a
// created value that it reports as capped, else the few runs of small.
func runsServer(capped func(created string) bool, listed *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/actions/runs") {
			w.Write([]byte(`{"jobs":[]}`))
			return
		}
		created := r.URL.Query().Get("created")
		*listed = append(*listed, created)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		n := 1
		if capped(created) {
			n = 100
		}
		var runs []string
		for i := 0; i < n; i++ {
			runs = append(runs, fmt.Sprintf(`{"id":%d,"name":"CI"}`, len(created)*100000+page*1000+i+1+int(created[len(created)-2])*10000000))
		}
		fmt.Fprintf(w, `{"workflow_runs":[%s]}`, strings.Join(runs, ","))
	}))
}

func TestDayCapSplitsInHalves(t *testing.T) {
	var listed []string
	srv := runsServer(func(c string) bool { return c == "2026-11-27" }, &listed)
	defer srv.Close()
	runs, err := testClient(srv).listDay("o/r", "2026-11-27")
	if err != nil {
		t.Fatal(err)
	}
	// 10 full pages, then one short page for each half.
	last := listed[len(listed)-2:]
	if last[0] != "2026-11-27T00:00:00Z..2026-11-27T11:59:59Z" || last[1] != "2026-11-27T12:00:00Z..2026-11-27T23:59:59Z" {
		t.Errorf("listed %v", listed)
	}
	if len(listed) != 12 || len(runs) != 2 {
		t.Errorf("%d calls, %d runs", len(listed), len(runs))
	}
}

func TestDayCapErrorsWhenHalfIsCapped(t *testing.T) {
	var listed []string
	srv := runsServer(func(c string) bool { return strings.HasPrefix(c, "2026-11-27T12") || c == "2026-11-27" }, &listed)
	defer srv.Close()
	_, err := testClient(srv).listDay("o/r", "2026-11-27")
	if err == nil || !strings.Contains(err.Error(), "2026-11-27") || !strings.Contains(err.Error(), "T12:00:00Z") {
		t.Errorf("err %v", err)
	}
}

func TestJobsDedupedByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jobs":[{"id":5,"name":"linux"},{"id":6,"name":"web"},{"id":5,"name":"linux"}]}`))
	}))
	defer srv.Close()
	jobs, err := testClient(srv).jobs("o/r", 1)
	if err != nil || len(jobs) != 2 {
		t.Errorf("%v, %d jobs", err, len(jobs))
	}
}

func TestRenderEscapesNames(t *testing.T) {
	st := Stats{Days: testWindow, PerDay: make([]int, 3), PerJob: map[string]int{"evil|x `y` @bob / job": 1},
		Linux: map[string]durations{}, Steps: map[string]*stepCount{}}
	text := render(st, "o/r")
	if !strings.Contains(text, "| `evil\\|x y @bob / job` | 1 |") {
		t.Errorf("report:\n%s", text)
	}
}
