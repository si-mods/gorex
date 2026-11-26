package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"gorex/internal/hosts"
	"gorex/internal/layout"
	"gorex/internal/rex"
)

// probeHooks allow unit tests to supply mock local/remote connections
// without requiring a live Tailscale or tailcat network.
var (
	probeDialRemote = func(ctx context.Context, target string) (*rex.Client, error) {
		return rex.ConnectRemote(ctx, target)
	}
	probeTailnetHosts = func() []rex.TailnetHost {
		local, err := rex.ConnectExisting()
		if err != nil {
			return nil
		}
		defer local.Close()
		st, err := local.RemoteStatus()
		if err != nil || st.Tailnet == nil {
			return nil
		}
		return st.Tailnet.Hosts
	}
)

type probedProject struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type probedHost struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Alias    string          `json:"alias,omitempty"`
	Source   string          `json:"source"`
	Online   bool            `json:"online"`
	OS       string          `json:"os,omitempty"`
	Machine  string          `json:"machine,omitempty"`
	Agents   []string        `json:"agents,omitempty"`
	Projects []probedProject `json:"projects,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type probeCandidate struct {
	id      string
	name    string
	alias   string
	source  string
	address string
}

// collectProbeCandidates merges saved hosts from hosts.json with
// Tailscale-discovered hosts, deduplicating by target address and
// case-insensitive name/alias so saved hosts win (matching connect()).
func collectProbeCandidates(saved []hosts.Host, tailnet []rex.TailnetHost) []probeCandidate {
	out := make([]probeCandidate, 0, len(saved)+len(tailnet))
	seenAddr := make(map[string]bool, len(saved))
	seenName := make(map[string]bool, len(saved)*2)
	for _, h := range saved {
		out = append(out, probeCandidate{
			id:      h.ID,
			name:    h.Name,
			alias:   h.Alias,
			source:  "saved",
			address: h.Address,
		})
		if h.Address != "" {
			seenAddr[strings.TrimSpace(h.Address)] = true
		}
		if h.Name != "" {
			seenName[strings.ToLower(h.Name)] = true
		}
		if h.Alias != "" {
			seenName[strings.ToLower(h.Alias)] = true
		}
	}
	for _, th := range tailnet {
		target := strings.TrimSpace(th.Target)
		if target == "" || seenAddr[target] {
			continue
		}
		if th.Name != "" && seenName[strings.ToLower(th.Name)] {
			continue
		}
		seenAddr[target] = true
		if th.Name != "" {
			seenName[strings.ToLower(th.Name)] = true
		}
		out = append(out, probeCandidate{
			name:    th.Name,
			source:  "tailnet",
			address: target,
		})
	}
	return out
}

func probeHosts(candidates []probeCandidate, timeout time.Duration) []probedHost {
	results := make([]probedHost, len(candidates))
	var wg sync.WaitGroup
	for i, cand := range candidates {
		wg.Add(1)
		go func(i int, cand probeCandidate) {
			defer wg.Done()
			results[i] = probeOneHost(cand, timeout)
		}(i, cand)
	}
	wg.Wait()
	return results
}

func probeOneHost(cand probeCandidate, timeout time.Duration) probedHost {
	res := probedHost{
		ID:     cand.id,
		Name:   cand.name,
		Alias:  cand.alias,
		Source: cand.source,
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c, err := probeDialRemote(ctx, cand.address)
	if err != nil {
		res.Error = sanitizeProbeError(err.Error(), cand.address)
		return res
	}
	defer c.Close()

	// Bound post-dial RPCs with the same timeout by closing c when ctx ends.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-done:
		}
	}()
	defer close(done)

	h, err := c.Hello()
	if err != nil {
		res.Error = sanitizeProbeError(err.Error(), cand.address)
		return res
	}
	res.Online = true
	res.OS = h.Host.OS
	res.Machine = h.Machine
	if res.Name == "" {
		res.Name = h.Host.Name
	}

	if h.Can(rex.CapabilityAgents) {
		if ar, err := c.AgentsList(); err == nil && len(ar.Agents) > 0 {
			res.Agents = make([]string, 0, len(ar.Agents))
			for _, a := range ar.Agents {
				res.Agents = append(res.Agents, a.ID)
			}
		}
	}

	if raw, err := c.Layout(); err == nil && len(raw) > 0 {
		st := layout.Parse(raw)
		for _, w := range st.Sessions {
			if w.Dir == "" {
				continue
			}
			res.Projects = append(res.Projects, probedProject{
				Name: w.Name,
				Dir:  w.Dir,
			})
		}
	}
	return res
}

// sanitizeProbeError removes a host's secret address from an error message
// before printing it to stdout/stderr.
func sanitizeProbeError(msg, secretAddr string) string {
	if secretAddr != "" && strings.Contains(msg, secretAddr) {
		msg = strings.ReplaceAll(msg, secretAddr, "<redacted>")
	}
	return msg
}

func writeProbedHostsTable(out io.Writer, list []probedHost) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tALIAS\tSOURCE\tSTATUS\tOS\tAGENTS\tPROJECTS")
	for _, h := range list {
		status := "offline"
		if h.Online {
			status = "online"
		}
		agents := strings.Join(h.Agents, ",")
		projNames := make([]string, 0, len(h.Projects))
		for _, p := range h.Projects {
			projNames = append(projNames, printable(p.Name))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			printable(h.Name),
			printable(h.Alias),
			h.Source,
			status,
			printable(h.OS),
			printable(agents),
			strings.Join(projNames, ","),
		)
	}
	tw.Flush()
}

func agentsCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("agents", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(errOut, "gorex agents: use `gorex [-S HOST] agents [--json]`")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()

	h, err := c.Hello()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	if !h.Can(rex.CapabilityAgents) {
		fmt.Fprintln(errOut, "gorex agents: server does not support agents.list; upgrade GoRex on that host")
		return ExitFailed, true
	}
	ar, err := c.AgentsList()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	agents := ar.Agents
	if agents == nil {
		agents = []rex.AgentInfo{}
	}
	if *asJSON {
		WriteJSON(out, agents)
		return ExitOK, true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME")
	for _, a := range agents {
		fmt.Fprintf(tw, "%s\t%s\n", printable(a.ID), printable(a.Name))
	}
	tw.Flush()
	return ExitOK, true
}
