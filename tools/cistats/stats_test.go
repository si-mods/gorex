package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	for _, s := range []string{"### Flags\n\nnone", "| 2026-11-27 | 4 |", "| CI / linux | 3 |", "| pull_request | 2 |", "| web test | 1 | 2 |", "1 of 3 finished jobs failed (33.3%)"} {
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
	got := windowDays(time.Date(2026, 11, 28, 5, 0, 0, 0, time.UTC), 3)
	if strings.Join(got, ",") != "2026-11-26,2026-11-27,2026-11-28" {
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
	c := &client{base: srv.URL, token: "tok", http: srv.Client()}
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
	c := &client{base: srv.URL, token: "tok", http: srv.Client()}
	_, err := c.collect("o/r", []string{"2026-11-26", "2026-11-27"})
	if err == nil || !strings.Contains(err.Error(), "429") || calls != 1 {
		t.Errorf("err %v after %d calls, want a 429 error after 1 call", err, calls)
	}
}
