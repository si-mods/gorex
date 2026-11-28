// Command cistats reports how much of the InstaBox sandbox allowance CI uses,
// from GitHub's REST API: starts per day, hour and minute, the starts of each
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
	"strings"
	"sync"
	"time"
)

func main() {
	days := flag.Int("days", 7, "days in the window, today (UTC) included")
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
	c := &client{base: "https://api.github.com", token: token, http: &http.Client{Timeout: 60 * time.Second}}
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

// windowDays are the UTC days from now-(days-1) to now, as YYYY-MM-DD.
func windowDays(now time.Time, days int) []string {
	out := make([]string, days)
	for i := range out {
		out[i] = now.AddDate(0, 0, i-days+1).Format("2006-01-02")
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
}

// get reads one JSON document. A 403 or 429 stops the whole report: more
// calls only make a rate limit last longer.
func (c *client) get(path string, q url.Values, out any) error {
	u := c.base + path + "?" + q.Encode()
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *client) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%s %s: HTTP %d (rate limit or missing permission, reset %q): %s",
			req.Method, req.URL.Path, resp.StatusCode, resp.Header.Get("X-RateLimit-Reset"), snippet(body))
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: HTTP %d: %s", req.Method, req.URL.Path, resp.StatusCode, snippet(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// maxPages bounds one day's run list: GitHub caps a query at 1000 results.
const maxPages = 10

// collect lists the runs created on each day of the window, then the jobs of
// each run.
func (c *client) collect(repo string, window []string) ([]RunJobs, error) {
	var runs []Run
	seen := map[int64]bool{}
	for _, day := range window {
		for page := 1; page <= maxPages; page++ {
			var resp struct {
				Runs []Run `json:"workflow_runs"`
			}
			q := url.Values{"created": {day}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
			if err := c.get("/repos/"+repo+"/actions/runs", q, &resp); err != nil {
				return nil, err
			}
			for _, r := range resp.Runs {
				if !seen[r.ID] {
					seen[r.ID] = true
					runs = append(runs, r)
				}
			}
			if len(resp.Runs) < 100 {
				break
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

func (c *client) jobs(repo string, id int64) ([]Job, error) {
	var all []Job
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			Jobs []Job `json:"jobs"`
		}
		q := url.Values{"filter": {"all"}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := c.get(fmt.Sprintf("/repos/%s/actions/runs/%d/jobs", repo, id), q, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Jobs...)
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
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/repos/%s/issues/%d/comments", c.base, repo, issue), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, nil)
}
