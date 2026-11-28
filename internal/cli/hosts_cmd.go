package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"gorex/internal/hosts"
	"gorex/internal/rex"
	"gorex/internal/rexapi"
)

// savedHost is a remote host as hosts.json keeps it. Address is a secret
// and is never printed here.
type savedHost = hosts.Host

func hostsCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) > 0 && args[0] == "ls" {
		args = args[1:]
	}
	fs := flagSet("hosts", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	probe := fs.Bool("probe", false, "dial peers to report reachability, OS, agents, and projects")
	timeout := fs.Duration("timeout", 5*time.Second, "per-host probe timeout")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(errOut, "gorex hosts: use `gorex hosts [ls] [--probe] [--timeout D] [--json]`")
		return ExitUsage, true
	}
	if *probe && *timeout <= 0 {
		fmt.Fprintln(errOut, "gorex hosts: --timeout must be positive")
		return ExitUsage, true
	}
	f, err := hosts.Load(rex.Dir())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	if *probe {
		cands := collectProbeCandidates(f, probeTailnetHosts())
		probed := probeHosts(cands, *timeout)
		if *asJSON {
			WriteJSON(out, probed)
			return ExitOK, true
		}
		writeProbedHostsTable(out, probed)
		return ExitOK, true
	}
	if *asJSON {
		type shown struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Alias string `json:"alias,omitempty"`
		}
		shownHosts := make([]shown, 0, len(f))
		for _, h := range f {
			shownHosts = append(shownHosts, shown{ID: h.ID, Name: h.Name, Alias: h.Alias})
		}
		WriteJSON(out, shownHosts)
		return ExitOK, true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tALIAS\tID")
	for _, h := range f {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", printable(h.Name), printable(h.Alias), h.ID)
	}
	tw.Flush()
	return ExitOK, true
}

// pairCmd is "gorex pair": it prints the QR code another device scans to
// reach a server in Rex, the tailnet address that holds no credential:
// this machine's, or with -S a host's, which its hello carries.
func pairCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("pair", errOut)
	asJSON := fs.Bool("json", false, "print the address as JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(errOut, "gorex pair: use `gorex [-S HOST] pair [--json]`")
		return ExitUsage, true
	}
	url := ""
	if serverTarget == "" {
		// The gateway knows this machine's address; the server's own
		// hello leaves it out, so that it never waits on the gateway.
		found, err := rexapi.PairAddress()
		if err != nil {
			fmt.Fprintln(errOut, "gorex pair:", err)
			return ExitFailed, true
		}
		url = found
	} else {
		c, code := connect(errOut)
		if c == nil {
			return code, true
		}
		defer c.Close()
		h, err := c.Hello()
		if err != nil {
			fmt.Fprintln(errOut, "gorex pair:", err)
			return ExitServer, true
		}
		if h.RexPairURL == "" {
			fmt.Fprintf(errOut, "gorex pair: %s sends no pairing address: its built-in Tailscale is off or not signed in, or its tailnet HTTPS address is not ready\n", serverTarget)
			return ExitFailed, true
		}
		url = h.RexPairURL
	}
	if err := writePair(out, url, *asJSON); err != nil {
		fmt.Fprintln(errOut, "gorex pair:", err)
		return ExitFailed, true
	}
	return ExitOK, true
}

// writePair prints the address as JSON, or as the QR code that holds it
// with the line that says what to scan it with.
func writePair(out io.Writer, url string, asJSON bool) error {
	if asJSON {
		return WriteJSON(out, map[string]string{"url": url})
	}
	art, err := rexapi.RenderQR(url)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(out, art); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "\nScan with Rex on a device on the same tailnet: %s\n", url)
	return err
}

// contextValue keeps an environment value from adding lines or prompt text
// to an agent's context.
func contextValue(s string) string {
	s = printable(s)
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}

// printable keeps a name from moving the cursor or changing the title.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
