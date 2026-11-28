// Command cistats reports how much of the InstaBox sandbox allowance CI uses,
// from GitHub's REST API, for whole UTC days that have ended: starts per day, hour and minute, the starts of each
// job, how long CI's linux job takes, and how many of its steps run.
//
// It reads GITHUB_TOKEN (or GH_TOKEN). The report is Markdown. With -issue it
// posts the report as a comment on that issue, else it prints it.
//
//	go run ./tools/cistats [-days 7] [-repo si-mods/gorex] [-issue N]
//
// docs/REF-015-ci.md says what the numbers mean and what to do about a flag.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	days := flag.Int("days", 7, "days in the window: the N whole UTC days before today (today is not in it)")
	repo := flag.String("repo", "si-mods/gorex", "owner/name")
	issue := flag.Int("issue", 0, "post the report as a comment on this issue")
	flag.Parse()
	if err := run(*days, *repo, *issue); err != nil {
		fmt.Fprintln(os.Stderr, "cistats:", err)
		os.Exit(1)
	}
}

func run(days int, repo string, issue int) error {
	if days < 1 || days > 31 {
		return errors.New("-days must be 1 to 31")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return errors.New("set GITHUB_TOKEN or GH_TOKEN")
	}
	c := newClient("https://api.github.com", token, &http.Client{Timeout: 60 * time.Second})
	window := windowDays(time.Now().UTC(), days)
	data, err := c.collect(repo, window)
	if err != nil {
		return err
	}
	text := render(aggregate(data, window), repo)
	if issue == 0 {
		fmt.Print(text)
		return nil
	}
	return c.comment(repo, issue, text)
}

// windowDays are the window: the N UTC days before
// the day of now, oldest first, as YYYY-MM-DD. The day of now is not in it, so
// every day has ended and its count is final.
func windowDays(now time.Time, days int) []string {
	out := make([]string, days)
	for i := range out {
		out[i] = now.UTC().AddDate(0, 0, i-days).Format("2006-01-02")
	}
	return out
}

// Run is a workflow run, as the list endpoint returns it.
type Run struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Event      string `json:"event"`
	Conclusion string `json:"conclusion"`
}

// Step is a step of a job.
type Step struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// Job is a job of a run.
type Job struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Labels      []string  `json:"labels"`
	Conclusion  string    `json:"conclusion"`
	RunnerName  string    `json:"runner_name"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Steps       []Step    `json:"steps"`
}

// RunJobs is a run with its jobs.
type RunJobs struct {
	Run  Run
	Jobs []Job
}

type client struct {
	base, token string
	http        *http.Client
	sleep       func(time.Duration) // injectable: tests do not sleep
	now         func() time.Time
	logf        func(format string, args ...any)
}

func newClient(base, token string, h *http.Client) *client {
	return &client{base: base, token: token, http: h, sleep: time.Sleep, now: time.Now,
		logf: func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }}
}

const (
	lowRemaining = 20               // wait for the reset below this many requests left
	resetSlack   = 5 * time.Second  // after the reset time
	maxWait      = 65 * time.Minute // GITHUB_TOKEN's limit is per hour
)

// waitReset sleeps until the rate limit resets. A wait that is longer than
// maxWait is an error. Without a reset header there is nothing to wait for.
func (c *client) waitReset(reset string) error {
	sec, err := strconv.ParseInt(reset, 10, 64)
	if err != nil {
		return nil
	}
	until := time.Unix(sec, 0).Add(resetSlack)
	d := until.Sub(c.now())
	if d > maxWait {
		return fmt.Errorf("rate limit resets at %s, more than %s from now", until.UTC().Format(time.RFC3339), maxWait)
	}
	c.logf("rate limit: waiting until %s", until.UTC().Format(time.RFC3339))
	if d > 0 {
		c.sleep(d)
	}
	return nil
}

// get reads one JSON document.
func (c *client) get(path string, q url.Values, out any) error {
	return c.do("GET", c.base+path+"?"+q.Encode(), nil, out)
}

// do sends a request. Every answer's X-RateLimit headers are read: with fewer
// than lowRemaining requests left, it waits for the reset. A 403 or 429 with
// none left waits once and sends the request again. Any other 403 or 429 is
// an error: more calls only make a limit last longer.
func (c *client) do(method, u string, body []byte, out any) error {
	waited := false
	for {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, u, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		remaining, rerr := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
		known := rerr == nil
		reset := resp.Header.Get("X-RateLimit-Reset")
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if known && remaining == 0 && !waited {
				waited = true
				if err := c.waitReset(reset); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("%s %s: HTTP %d (rate limit or missing permission, reset %q): %s",
				method, req.URL.Path, resp.StatusCode, reset, snippet(data))
		}
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("%s %s: HTTP %d: %s", method, req.URL.Path, resp.StatusCode, snippet(data))
		}
		if known && remaining < lowRemaining {
			if err := c.waitReset(reset); err != nil {
				return err
			}
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(data, out)
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// maxPages bounds one run listing: GitHub returns at most 1000 results for a
// query, which is 10 pages of 100.
const maxPages = 10

// listRuns lists the runs for one created= query. capped is true when the
// listing hit the 1000-result limit, so it may be incomplete.
func (c *client) listRuns(repo, created string) (runs []Run, capped bool, err error) {
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			Runs []Run `json:"workflow_runs"`
		}
		q := url.Values{"created": {created}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := c.get("/repos/"+repo+"/actions/runs", q, &resp); err != nil {
			return nil, false, err
		}
		runs = append(runs, resp.Runs...)
		if len(resp.Runs) < 100 {
			return runs, false, nil
		}
	}
	return runs, true, nil
}

// listDay lists the runs created on a day. A day that hits the cap is listed
// again as two 12-hour ranges; a half that hits it too is an error.
func (c *client) listDay(repo, day string) ([]Run, error) {
	runs, capped, err := c.listRuns(repo, day)
	if err != nil || !capped {
		return runs, err
	}
	var all []Run
	for _, r := range []string{day + "T00:00:00Z.." + day + "T11:59:59Z", day + "T12:00:00Z.." + day + "T23:59:59Z"} {
		part, capped, err := c.listRuns(repo, r)
		if err != nil {
			return nil, err
		}
		if capped {
			return nil, fmt.Errorf("%s: more than %d runs created in %s, too many to list", day, maxPages*100, r)
		}
		all = append(all, part...)
	}
	return all, nil
}

// collect lists the runs created on each day of the window and on the day
// before it (a run created before the window can start jobs in it), then the
// jobs of each run.
func (c *client) collect(repo string, window []string) ([]RunJobs, error) {
	first, err := time.Parse("2006-01-02", window[0])
	if err != nil {
		return nil, err
	}
	days := append([]string{first.AddDate(0, 0, -1).Format("2006-01-02")}, window...)
	var runs []Run
	seen := map[int64]bool{}
	for _, day := range days {
		dayRuns, err := c.listDay(repo, day)
		if err != nil {
			return nil, err
		}
		for _, r := range dayRuns {
			if !seen[r.ID] {
				seen[r.ID] = true
				runs = append(runs, r)
			}
		}
	}
	out := make([]RunJobs, len(runs))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		sem      = make(chan struct{}, 6)
	)
	for i, r := range runs {
		out[i].Run = r
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			mu.Lock()
			stop := firstErr != nil
			mu.Unlock()
			if stop {
				return
			}
			jobs, err := c.jobs(repo, r.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out[i].Jobs = jobs
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// jobs lists the jobs of a run. filter=all can list a job once for each
// attempt, so jobs are unique by id.
func (c *client) jobs(repo string, id int64) ([]Job, error) {
	var all []Job
	seen := map[int64]bool{}
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			Jobs []Job `json:"jobs"`
		}
		q := url.Values{"filter": {"all"}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := c.get(fmt.Sprintf("/repos/%s/actions/runs/%d/jobs", repo, id), q, &resp); err != nil {
			return nil, err
		}
		for _, j := range resp.Jobs {
			if j.ID != 0 && seen[j.ID] {
				continue
			}
			seen[j.ID] = true
			all = append(all, j)
		}
		if len(resp.Jobs) < 100 {
			break
		}
	}
	return all, nil
}

// maxComment keeps below GitHub's 65536-character comment limit.
const maxComment = 60000

func (c *client) comment(repo string, issue int, text string) error {
	if len(text) > maxComment {
		text = text[:maxComment] + "\n\n(cut)\n"
	}
	body, err := json.Marshal(map[string]string{"body": text})
	if err != nil {
		return err
	}
	return c.do("POST", fmt.Sprintf("%s/repos/%s/issues/%d/comments", c.base, repo, issue), body, nil)
}
