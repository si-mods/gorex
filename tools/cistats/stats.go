package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// The InstaBox Pro allowance and the flag thresholds. The thresholds sit well
// below the allowance (500 a day, 200 an hour, 30 a minute) so a flag comes
// before a block.
const (
	maxDay    = 350
	maxHour   = 150
	maxMinute = 25
	maxP90    = 20 * time.Minute

	limitHour   = 200
	limitMinute = 30
)

// ciSteps are the step names of the linux job in .github/workflows/ci.yml
// that tools/ciplan can skip.
var ciSteps = []string{"gofmt", "vet", "build", "lint", "web test", "test", "build and upload to R2"}

// isStart reports whether a job started an instabox sandbox: it ran on an
// instabox label, was not skipped, and got a runner.
func isStart(j Job) bool {
	if j.Conclusion == "skipped" || j.RunnerName == "" {
		return false
	}
	for _, l := range j.Labels {
		if strings.HasPrefix(l, "instabox") {
			return true
		}
	}
	return false
}

// durations are sorted job durations.
type durations []time.Duration

func (d durations) percentile(p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	i := int(p*float64(len(d))+0.999999) - 1 // nearest rank
	return d[min(max(i, 0), len(d)-1)]
}

// stepCount is how often a step ran and was skipped.
type stepCount struct{ ran, skipped int }

// Stats is everything the report shows.
type Stats struct {
	Days       []string
	PerDay     []int
	PeakHour   int
	PeakHourAt string
	PeakMin    int
	PeakMinAt  string
	PerJob     map[string]int
	Linux      map[string]durations // by event
	LinuxAll   durations
	LinuxOK    int // linux jobs that succeeded
	LinuxFail  int
	Steps      map[string]*stepCount
	Starts     int
}

// aggregate counts the jobs that started on the window's days (by started_at).
func aggregate(data []RunJobs, window []string) Stats {
	st := Stats{Days: window, PerDay: make([]int, len(window)), PerJob: map[string]int{},
		Linux: map[string]durations{}, Steps: map[string]*stepCount{}}
	hours, mins := map[string]int{}, map[string]int{}
	for _, rj := range data {
		for _, j := range rj.Jobs {
			i := slices.Index(window, j.StartedAt.UTC().Format("2006-01-02"))
			if i < 0 {
				continue
			}
			if isStart(j) {
				at := j.StartedAt.UTC()
				st.PerDay[i]++
				st.Starts++
				st.PerJob[rj.Run.Name+" / "+j.Name]++
				hours[at.Format("2006-01-02 15:00")]++
				mins[at.Format("2006-01-02 15:04")]++
			}
			if rj.Run.Name == "CI" && j.Name == "linux" {
				addLinux(&st, rj.Run.Event, j)
			}
		}
	}
	st.PeakHourAt, st.PeakHour = peak(hours)
	st.PeakMinAt, st.PeakMin = peak(mins)
	for _, d := range st.Linux {
		sort.Slice(d, func(i, k int) bool { return d[i] < d[k] })
	}
	sort.Slice(st.LinuxAll, func(i, k int) bool { return st.LinuxAll[i] < st.LinuxAll[k] })
	return st
}

func addLinux(st *Stats, event string, j Job) {
	switch j.Conclusion {
	case "success":
		st.LinuxOK++
	case "failure":
		st.LinuxFail++
	default:
		return // cancelled, skipped or running: no verdict, no duration
	}
	if !j.StartedAt.IsZero() && j.CompletedAt.After(j.StartedAt) {
		d := j.CompletedAt.Sub(j.StartedAt)
		st.Linux[event] = append(st.Linux[event], d)
		st.LinuxAll = append(st.LinuxAll, d)
	}
	for _, s := range j.Steps {
		if !slices.Contains(ciSteps, s.Name) {
			continue
		}
		c := st.Steps[s.Name]
		if c == nil {
			c = &stepCount{}
			st.Steps[s.Name] = c
		}
		switch s.Conclusion {
		case "success", "failure":
			c.ran++
		case "skipped":
			c.skipped++
		}
	}
}

// peak returns the key with the largest count; the earliest on a tie.
func peak(m map[string]int) (string, int) {
	best, n := "", 0
	for k, v := range m {
		if v > n || v == n && n > 0 && k < best {
			best, n = k, v
		}
	}
	return best, n
}

// Flags are the thresholds that the window crosses.
func (st Stats) Flags() []string {
	var out []string
	for i, n := range st.PerDay {
		if n > maxDay {
			out = append(out, fmt.Sprintf("%s: %d starts (more than %d a day)", st.Days[i], n, maxDay))
		}
	}
	if st.PeakHour > maxHour {
		out = append(out, fmt.Sprintf("%s UTC: %d starts in one hour (more than %d)", st.PeakHourAt, st.PeakHour, maxHour))
	}
	if st.PeakMin > maxMinute {
		out = append(out, fmt.Sprintf("%s UTC: %d starts in one minute (more than %d)", st.PeakMinAt, st.PeakMin, maxMinute))
	}
	if p := st.LinuxAll.percentile(0.9); p > maxP90 {
		out = append(out, fmt.Sprintf("CI linux p90 is %s (more than %s)", dur(p), dur(maxP90)))
	}
	return out
}

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// render writes the Markdown report.
func render(st Stats, repo string) string {
	var b strings.Builder
	last := st.Days[len(st.Days)-1]
	fmt.Fprintf(&b, "## CI health: %s to %s UTC (%s)\n\n", st.Days[0], last, repo)
	b.WriteString("### Flags\n\n")
	if f := st.Flags(); len(f) == 0 {
		b.WriteString("none\n")
	} else {
		for _, l := range f {
			b.WriteString("- " + l + "\n")
		}
	}

	b.WriteString("\n### Sandbox starts per day (UTC)\n\n| Day | Starts |\n| --- | ---: |\n")
	top := 0
	for i, d := range st.Days {
		fmt.Fprintf(&b, "| %s | %d |\n", d, st.PerDay[i])
		top = max(top, st.PerDay[i])
	}
	fmt.Fprintf(&b, "\nMax %d a day, average %.1f a day, %d in all. The allowance is 500 a day.\n", top, float64(st.Starts)/float64(len(st.Days)), st.Starts)
	if st.Starts > 0 {
		fmt.Fprintf(&b, "\nBusiest hour: %d starts at %s UTC (limit %d). Busiest minute: %d starts at %s UTC (limit %d).\n",
			st.PeakHour, st.PeakHourAt, limitHour, st.PeakMin, st.PeakMinAt, limitMinute)
	}

	b.WriteString("\n### Starts per workflow and job (top 10)\n\n| Workflow / job | Starts |\n| --- | ---: |\n")
	keys := make([]string, 0, len(st.PerJob))
	for k := range st.PerJob {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, k int) bool {
		if st.PerJob[keys[i]] != st.PerJob[keys[k]] {
			return st.PerJob[keys[i]] > st.PerJob[keys[k]]
		}
		return keys[i] < keys[k]
	})
	for _, k := range keys[:min(10, len(keys))] {
		fmt.Fprintf(&b, "| %s | %d |\n", k, st.PerJob[k])
	}

	b.WriteString("\n### CI linux job duration\n\n| Event | Jobs | p50 | p90 | Max |\n| --- | ---: | ---: | ---: | ---: |\n")
	events := make([]string, 0, len(st.Linux))
	for e := range st.Linux {
		events = append(events, e)
	}
	order := map[string]int{"pull_request": 0, "push": 1, "workflow_dispatch": 2}
	sort.Slice(events, func(i, k int) bool {
		oi, iok := order[events[i]]
		ok2, kok := order[events[k]]
		switch {
		case iok && kok:
			return oi < ok2
		case iok != kok:
			return iok
		}
		return events[i] < events[k]
	})
	row := func(name string, d durations) {
		if len(d) == 0 {
			return
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", name, len(d), dur(d.percentile(0.5)), dur(d.percentile(0.9)), dur(d[len(d)-1]))
	}
	for _, e := range events {
		row(e, st.Linux[e])
	}
	row("all", st.LinuxAll)

	b.WriteString("\n### CI linux steps\n\n| Step | Ran | Skipped |\n| --- | ---: | ---: |\n")
	for _, s := range ciSteps {
		if c := st.Steps[s]; c != nil {
			fmt.Fprintf(&b, "| %s | %d | %d |\n", s, c.ran, c.skipped)
		}
	}

	total := st.LinuxOK + st.LinuxFail
	b.WriteString("\n### CI linux failure rate\n\n")
	if total == 0 {
		b.WriteString("No finished linux job in the window.\n")
	} else {
		fmt.Fprintf(&b, "%d of %d finished jobs failed (%.1f%%). Cancelled jobs are not counted.\n", st.LinuxFail, total, 100*float64(st.LinuxFail)/float64(total))
	}
	return b.String()
}
