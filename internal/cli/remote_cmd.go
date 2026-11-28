package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gorex/internal/rex"
)

// serverCmd restarts the local server, so that a new one takes its place: it
// starts the sessions of the layout again, as the server after a crash does.
// It starts no server itself, which one run from an SSH session would end
// with it: the app, or the next command, starts it.
func serverCmd(args []string, out, errOut io.Writer) (int, bool) {
	if serverTarget != "" {
		fmt.Fprintln(errOut, "gorex server: this changes the local server only")
		return ExitUsage, true
	}
	usage := func() (int, bool) {
		fmt.Fprintln(errOut, "gorex server: restart | upgrade")
		return ExitUsage, true
	}
	if len(args) != 1 || (args[0] != "restart" && args[0] != "upgrade") {
		return usage()
	}
	c, err := rex.ConnectExisting()
	if err != nil {
		fmt.Fprintln(errOut, "gorex server: no server runs")
		return ExitServer, true
	}
	h, err := c.Hello()
	if err != nil {
		c.Close()
		fmt.Fprintln(errOut, "gorex server: no server runs")
		return ExitServer, true
	}
	if args[0] == "upgrade" {
		defer c.Close()
		exe, _ := rex.ServerExecutable()
		if err := upgradeServer(c, h, exe, out); err != nil {
			fmt.Fprintln(errOut, "gorex server:", err)
			return ExitFailed, true
		}
		return ExitOK, true
	}
	if !h.Can(rex.CapabilityShutdownRestart) {
		c.Close()
		fmt.Fprintln(errOut, "gorex server: the running server is too old to bring its sessions back: quit it once with Quit and End All, or kill it")
		return ExitFailed, true
	}
	// Run in a pane, this shell ends with the server: say so while it can.
	if os.Getenv("GOREX_SESSION") != "" {
		fmt.Fprintln(out, "restarting: this terminal ends, and comes back as a new shell")
	}
	if err := c.StopToRestart(); err != nil {
		c.Close()
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	c.Close()
	deadline := time.Now().Add(15 * time.Second)
	for rex.ProcessAlive(h.PID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if rex.ProcessAlive(h.PID) {
		fmt.Fprintln(errOut, "gorex server: the server did not stop")
		return ExitServer, true
	}
	// The app reconnects and starts the next server; without one, the next
	// command or launch does, which brings the sessions back then.
	deadline = time.Now().Add(5 * time.Second)
	for {
		if next, err := rex.ConnectExisting(); err == nil {
			nh, herr := next.Hello()
			next.Close()
			if herr == nil && nh.PID != h.PID {
				fmt.Fprintf(out, "restarted (pid %d)\n", nh.PID)
				return ExitOK, true
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Fprintln(out, "stopped; the next server brings the sessions back")
	return ExitOK, true
}

func remoteCmd(args []string, out, errOut io.Writer) (int, bool) {
	if serverTarget != "" {
		fmt.Fprintln(errOut, "gorex remote: this changes the local server only")
		return ExitUsage, true
	}
	usage := func() (int, bool) {
		fmt.Fprintln(errOut, "gorex remote: status | tailscale on|off | address | key | relay HOST|default | reachable IP|off | enable | disable | control on|off | pair start|stop | pending | approve KEY|CODE | reject KEY|CODE | allow NAME KEY | revoke KEY")
		return ExitUsage, true
	}
	if len(args) == 0 {
		return usage()
	}
	asJSON := false
	if args[0] == "status" && len(args) > 1 && args[1] == "--json" {
		asJSON = true
		args = append(args[:1], args[2:]...)
	}
	if len(args) > 1 && args[0] == "status" {
		return usage()
	}
	need := func(n int) bool {
		if len(args) != n {
			usage()
			return false
		}
		return true
	}
	onOff := func(what string) (bool, bool) {
		if !need(2) || (args[1] != "on" && args[1] != "off") {
			fmt.Fprintf(errOut, "gorex remote %s: give on or off\n", what)
			return false, false
		}
		return args[1] == "on", true
	}
	var run func(*rex.Client) error
	// after runs on a change that worked, as the relay's enrollment hint.
	var after func(*rex.Client)
	switch args[0] {
	case "status":
	case "address":
	case "key":
		// The key of this machine as a remote client, for a host's
		// allowlist or a relay operator's: no server needed.
		if !need(1) {
			return ExitUsage, true
		}
		fmt.Fprintln(out, rex.ClientPublicKey())
		return ExitOK, true
	case "enable":
		if !need(1) {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.EnableRemote() }
	case "disable":
		if !need(1) {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.DisableRemote() }
	case "relay":
		if !need(2) {
			return ExitUsage, true
		}
		relay := args[1]
		if relay == "default" {
			relay = ""
		}
		var err error
		if relay, err = rex.NormalizeRelay(relay); err != nil {
			fmt.Fprintln(errOut, err)
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.SetRelay(relay) }
		if relay != "" {
			// The relay's list admits the host by its node key: show the
			// one to send its operator, once the host has keys.
			after = func(c *rex.Client) {
				if st, err := c.RemoteStatus(); err == nil && st.PublicKey != "" {
					fmt.Fprintf(out, "key=%s\n", st.PublicKey)
					fmt.Fprintln(out, "send it to the relay's operator, whose list lets the host on")
				}
			}
		}
	case "reachable":
		if !need(2) {
			return ExitUsage, true
		}
		ip := args[1]
		if ip == "off" {
			ip = ""
		}
		if _, err := rex.NormalizeReachable(ip); err != nil {
			fmt.Fprintln(errOut, err)
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.SetReachable(ip) }
	case "tailscale":
		on, ok := onOff("tailscale")
		if !ok {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.SetTailnet(on) }
	case "control":
		on, ok := onOff("control")
		if !ok {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.RemoteControl(on) }
	case "pair":
		if !need(2) || (args[1] != "start" && args[1] != "stop") {
			return usage()
		}
		if args[1] == "start" {
			run = func(c *rex.Client) error { return c.StartPairing() }
		} else {
			run = func(c *rex.Client) error { return c.StopPairing() }
		}
	case "pending":
		if !need(1) {
			return ExitUsage, true
		}
	case "approve", "reject", "revoke":
		if !need(2) {
			return ExitUsage, true
		}
		key := args[1]
		// Six digits are the code of a request by address; a key is longer.
		byCode := len(key) == 6 && strings.Trim(key, "0123456789") == ""
		switch {
		case args[0] == "approve" && byCode:
			run = func(c *rex.Client) error { return c.ApprovePairingCode(key) }
		case args[0] == "approve":
			run = func(c *rex.Client) error { return c.ApprovePairing(key) }
		case args[0] == "reject" && byCode:
			run = func(c *rex.Client) error { return c.RejectPairingCode(key) }
		case args[0] == "reject":
			run = func(c *rex.Client) error { return c.RejectPairing(key) }
		default:
			run = func(c *rex.Client) error { return c.RevokeRemote(key) }
		}
	case "allow":
		if !need(3) {
			return ExitUsage, true
		}
		name, key := args[1], args[2]
		run = func(c *rex.Client) error { return c.AllowRemote(name, key) }
	default:
		return usage()
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	if args[0] == "status" {
		st, err := c.RemoteStatus()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		shown := struct {
			Enabled       bool               `json:"enabled"`
			Running       bool               `json:"running"`
			RemoteControl bool               `json:"remoteControl"`
			Allowed       int                `json:"allowed"`
			Relay         string             `json:"relay"`
			PublicKey     string             `json:"publicKey,omitempty"`
			ReachableAt   string             `json:"reachableAt"`
			Reachable     bool               `json:"reachable"`
			ReachableErr  string             `json:"reachableError,omitempty"`
			Firewall      string             `json:"reachableFirewall,omitempty"`
			Tailnet       *rex.TailnetStatus `json:"tailnet,omitempty"`
		}{st.Enabled, st.Running, st.RemoteControl, len(st.Allowed), st.Relay,
			st.PublicKey, st.ReachableAt, st.ReachableAddress != "", st.ReachableError, st.ReachableFirewall, st.Tailnet}
		if asJSON {
			WriteJSON(out, shown)
			return ExitOK, true
		}
		relay := shown.Relay
		if relay == "" {
			relay = "default"
		}
		fmt.Fprintf(out, "enabled=%v running=%v control=%v allowed=%d relay=%s\n",
			shown.Enabled, shown.Running, shown.RemoteControl, shown.Allowed, relay)
		if st.PublicKey != "" {
			fmt.Fprintf(out, "key=%s\n", st.PublicKey)
		}
		if shown.ReachableAt != "" {
			fmt.Fprintf(out, "reachable=%s running=%v\n", shown.ReachableAt, shown.Reachable)
			if shown.ReachableErr != "" {
				fmt.Fprintf(out, "reachableError=%s\n", shown.ReachableErr)
			}
			switch shown.Firewall {
			case "blocked":
				fmt.Fprintf(out, "reachableFirewall=blocked: Windows Firewall blocks port %d; to allow it, run as administrator:\n", rex.RemotePort)
				for _, c := range rex.FirewallRules("netsh") {
					fmt.Fprintf(out, "  %s\n", c)
				}
			case "blockRule":
				fmt.Fprintln(out, "reachableFirewall=blockRule: a Windows Firewall rule blocks GoRex; remove it in Windows Defender Firewall, Inbound Rules")
			}
		}
		if st.Tailnet != nil {
			side := st.Tailnet.Side
			if side == "" {
				side = "none"
			}
			fmt.Fprintf(out, "tailscale=%v running=%v hosts=%d side=%s\n", st.Tailnet.Enabled, st.Tailnet.Running, len(st.Tailnet.Hosts), side)
			if st.Tailnet.Node != "" {
				fmt.Fprintf(out, "tailscaleNode=%s", st.Tailnet.Node)
				if st.Tailnet.Account != "" {
					fmt.Fprintf(out, " account=%s", st.Tailnet.Account)
				}
				if st.Tailnet.Device != "" {
					fmt.Fprintf(out, " device=%s", st.Tailnet.Device)
				}
				fmt.Fprintln(out)
			}
			if st.Tailnet.Error != "" {
				fmt.Fprintf(out, "tailscaleError=%s\n", st.Tailnet.Error)
			}
		}
		return ExitOK, true
	}
	if args[0] == "address" {
		st, err := c.RemoteStatus()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		fmt.Fprintln(out, st.Address)
		// The second, of Reachable at, while its relay runs.
		if st.ReachableAddress != "" {
			fmt.Fprintln(out, st.ReachableAddress)
		}
		return ExitOK, true
	}
	if args[0] == "pending" {
		st, err := c.RemoteStatus()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		// The requests by address first, each by its code; then those over
		// tailcat, by key.
		all := []any{}
		for _, p := range st.PendingByAddress {
			all = append(all, p)
		}
		for _, p := range st.Pending {
			all = append(all, p)
		}
		WriteJSON(out, all)
		return ExitOK, true
	}
	if err := run(c); err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if after != nil {
		after(c)
	}
	return ExitOK, true
}
