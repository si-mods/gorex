package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"gorex/internal/rex"
)

// Exit codes scripts can rely on, as rex's.
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitUsage  = 2
	ExitTarget = 3
	ExitServer = 4
)

// serverTarget is -S, a host label or address; Main sets it for a call.
var serverTarget string

// Main runs the command line. handled is false when args are the app's, as
// an AppKit argument, and the GUI should start.
func Main(args []string, out, errOut io.Writer, version string) (code int, handled bool) {
	serverTarget = ""
	if len(args) == 0 {
		return 0, false
	}
	if args[0] == "-S" || args[0] == "--server" {
		if len(args) < 3 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(errOut, "gorex: -S needs a host and a command")
			return ExitUsage, true
		}
		serverTarget, args = args[1], args[2:]
	}
	name, args := args[0], args[1:]
	switch name {
	case "help", "-h", "--help":
		usage(out)
		return ExitOK, true
	case "version":
		return versionCmd(args, out, errOut, version)
	case "ls", "list":
		return listCmd(args, out, errOut)
	case "clients":
		return clientsCmd(args, out, errOut)
	case "new":
		return newCmd(args, out, errOut)
	case "split":
		return splitCmd(args, out, errOut)
	case "close":
		return closeCmd(args, out, errOut)
	case "send", "type":
		return sendCmd(args, out, errOut)
	case "capture":
		return captureCmd(args, out, errOut)
	case "events":
		return eventsCmd(args, out, errOut)
	case "hosts":
		return hostsCmd(args, out, errOut)
	case "skill":
		return skillCmd(args, out, errOut)
	case "remote":
		return remoteCmd(args, out, errOut)
	case "wait":
		return waitCmd(args, out, errOut)
	case "run":
		return runCmd(args, out, errOut)
	case "action":
		return actionCmd(args, out, errOut)
	}
	if strings.HasPrefix(name, "-") {
		return 0, false
	}
	fmt.Fprintf(errOut, "gorex: unknown command %q\n", name)
	usage(errOut)
	return ExitUsage, true
}

// usage writes what the command line does.
func usage(w io.Writer) {
	fmt.Fprint(w, `gorex — the GoRex command line

Usage:
  gorex [-S HOST] ls [--json]
  gorex clients [--json]
  gorex split [-s SESSION] [--down] [--dir DIR]
  gorex close [-s SESSION] [--tab]
  gorex new [--dir DIR] [--json] [-- COMMAND...]
  gorex send [-s SESSION] [--enter] [--json] TEXT...
  gorex capture [-s SESSION] [--history BYTES] [--raw] [--json]
  gorex events
  gorex hosts [ls] [--json]
  gorex skill show|setup|status|remove [--agent NAME] [--project]
  gorex remote status [--json] | remote control on|off
  gorex wait [-s SESSION] (--exit | --idle | --status STATE | --contains TEXT) [--timeout D]
  gorex run [--dir DIR] -- COMMAND...
  gorex action run [-s SESSION] ACTION [KEY=VALUE...]
  gorex version [--json]

A session defaults to GOREX_SESSION, set in every terminal GoRex opens.
Codes: 0 ok, 1 failed, 2 usage, 3 no such session or target, 4 server
unreachable, 130 interrupted.
`)
}

// flagSet returns the flags of a command.
func flagSet(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "gorex %s\n", name)
		fs.PrintDefaults()
	}
	return fs
}

func parse(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK, true
		}
		return ExitUsage, true
	}
	return 0, false
}

// connect connects to the server, starting one when none runs; -S names a
// saved host or an address.
func connect(errOut io.Writer) (*rex.Client, int) {
	if serverTarget == "" {
		c, err := rex.Connect()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return nil, ExitServer
		}
		return c, ExitOK
	}
	f, err := loadHosts()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return nil, ExitFailed
	}
	{
		var manual, network []savedHost
		for _, h := range f.Hosts {
			if h.ID == "" || h.Address == "" || !strings.EqualFold(h.Name, serverTarget) {
				continue
			}
			if h.Network {
				network = append(network, h)
			} else {
				manual = append(manual, h)
			}
		}
		// A host added by hand wins over one the directory brought; if
		// there is none, a directory host must still be unique.
		found := manual
		if len(found) == 0 {
			found = network
		}
		if len(found) > 1 {
			fmt.Fprintf(errOut, "gorex: host %q is ambiguous\n", serverTarget)
			return nil, ExitTarget
		}
		if len(found) == 1 {
			return dialRemote(found[0], errOut)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := rex.ConnectRemote(ctx, serverTarget)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return nil, ExitServer
	}
	return c, ExitOK
}

func dialRemote(h savedHost, errOut io.Writer) (*rex.Client, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := rex.ConnectRemote(ctx, h.Address)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return nil, ExitServer
	}
	return c, ExitOK
}

// targetFlag adds -s/--session, defaulting to GOREX_SESSION.
func targetFlag(fs *flag.FlagSet) *string {
	def := ""
	if serverTarget == "" {
		def = os.Getenv("GOREX_SESSION")
	}
	return fs.String("s", def, "session ID")
}

func versionCmd(args []string, out, errOut io.Writer, version string) (int, bool) {
	fs := flagSet("version", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if version == "" {
		version = "dev"
	}
	if *asJSON {
		WriteJSON(out, map[string]string{"version": version})
	} else {
		fmt.Fprintln(out, version)
	}
	return ExitOK, true
}

func listCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("ls", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	infos, err := c.List()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	if *asJSON {
		WriteJSON(out, infos)
		return ExitOK, true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROGRAM\tDIR\tATTACHED\tSTATUS")
	for _, in := range infos {
		program := in.Program
		if program == "" {
			program = in.Shell
		}
		status := ""
		if in.Status != nil {
			status = in.Status.State
		}
		if in.Exited {
			status = "exited"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", in.ID, program, in.Dir, in.Attached, status)
	}
	tw.Flush()
	return ExitOK, true
}

func clientsCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("clients", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	clients, err := c.Clients()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	if *asJSON {
		WriteJSON(out, clients)
		return ExitOK, true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tPEER\tREMOTE\tVIA\tSEQ")
	for _, cl := range clients {
		peer := cl.Peer
		if peer == "" {
			peer = "local"
		}
		fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%d\n", cl.SessionID, peer, cl.Remote, cl.Via, cl.Seq)
	}
	tw.Flush()
	return ExitOK, true
}

func splitCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("split", errOut)
	sid := targetFlag(fs)
	down := fs.Bool("down", false, "split below, not right")
	dir := fs.String("dir", "", "directory to start in")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if *sid == "" {
		fmt.Fprintln(errOut, "gorex split: no session; use -s or GOREX_SESSION")
		return ExitTarget, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, *sid)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	res, err := c.Command(rex.LayoutCommand{Op: "pane.split", SID: target, Vertical: *down, Dir: *dir})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if res.Code != "" {
		fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		return codeForResult(res), true
	}
	if *asJSON {
		WriteJSON(out, res)
	} else if res.New != "" {
		fmt.Fprintln(out, res.New)
	}
	return ExitOK, true
}

func closeCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("close", errOut)
	sid := targetFlag(fs)
	tab := fs.Bool("tab", false, "close the tab, not the pane")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if *sid == "" {
		fmt.Fprintln(errOut, "gorex close: no session; use -s or GOREX_SESSION")
		return ExitTarget, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, *sid)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	op := "pane.close"
	if *tab {
		op = "tab.close"
	}
	res, err := c.Command(rex.LayoutCommand{Op: op, SID: target})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if res.Code != "" {
		fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		return codeForResult(res), true
	}
	if *asJSON {
		WriteJSON(out, res)
	}
	return ExitOK, true
}

func newCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("new", errOut)
	dir := fs.String("dir", "", "directory to start in")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	info, err := c.Create(rex.CreateOptions{Command: fs.Args(), Dir: *dir, Cols: 80, Rows: 24})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	if *asJSON {
		WriteJSON(out, info)
	} else {
		fmt.Fprintln(out, info.ID)
	}
	return ExitOK, true
}

func sendCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("send", errOut)
	sid := targetFlag(fs)
	enter := fs.Bool("enter", false, "press Return after the text")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	text := strings.Join(fs.Args(), " ")
	if *enter {
		text += "\r"
	}
	if text == "" {
		fmt.Fprintln(errOut, "gorex send: no text")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, *sid)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if err := c.Input(target, []byte(text)); err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if *asJSON {
		WriteJSON(out, map[string]bool{"ok": true})
	}
	return ExitOK, true
}

func captureCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("capture", errOut)
	sid := targetFlag(fs)
	history := fs.Int("history", 0, "scrollback bytes to keep")
	raw := fs.Bool("raw", false, "print escape sequences")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if *raw && *asJSON {
		fmt.Fprintln(errOut, "gorex capture: --raw and --json cannot be used together")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, *sid)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	res, err := c.Capture(target, *history)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if *raw {
		out.Write(res.Screen)
		return ExitOK, true
	}
	text, err := plainText(res)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	if *asJSON {
		WriteJSON(out, map[string]any{
			"title": res.Title, "cols": res.Cols, "rows": res.Rows,
			"truncated": res.Truncated, "text": text,
		})
	} else {
		fmt.Fprint(out, text)
	}
	return ExitOK, true
}

// plainText replays a capture in a headless terminal and returns its text.
func plainText(res rex.CaptureResult) (string, error) {
	if err := terminal.Load(); err != nil {
		return "", err
	}
	conn := newNopConn()
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		conn.Close()
		return "", err
	}
	defer term.Close()
	defer conn.Close()
	term.Resize(res.Cols, res.Rows)
	term.Feed(res.Screen)
	return term.Text(), nil
}

// nopConn is the Conn of a terminal that only shows what the app feeds it;
// Close ends its reader.
type nopConn struct {
	done chan struct{}
	once sync.Once
}

func newNopConn() *nopConn { return &nopConn{done: make(chan struct{})} }
func (c *nopConn) Read([]byte) (int, error) {
	<-c.done
	return 0, io.EOF
}
func (c *nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *nopConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// savedHost is a remote host as hosts.json keeps it. Address is a secret
// and is never printed here.
type savedHost struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Network bool   `json:"network,omitempty"`
}

type hostsFile struct {
	Hosts []savedHost `json:"hosts"`
}

func hostsCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) > 0 && args[0] == "ls" {
		args = args[1:]
	}
	fs := flagSet("hosts", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(errOut, "gorex hosts: use `gorex hosts [ls]`")
		return ExitUsage, true
	}
	f, err := loadHosts()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	hosts := f.Hosts[:0]
	for _, h := range f.Hosts {
		if h.ID != "" && h.Address != "" {
			hosts = append(hosts, h)
		}
	}
	if *asJSON {
		type shown struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Network bool   `json:"network,omitempty"`
		}
		shownHosts := make([]shown, 0, len(hosts))
		for _, h := range hosts {
			shownHosts = append(shownHosts, shown{ID: h.ID, Name: h.Name, Network: h.Network})
		}
		WriteJSON(out, shownHosts)
		return ExitOK, true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tNETWORK\tID")
	for _, h := range hosts {
		fmt.Fprintf(tw, "%s\t%v\t%s\n", printable(h.Name), h.Network, h.ID)
	}
	tw.Flush()
	return ExitOK, true
}

func loadHosts() (hostsFile, error) {
	b, err := os.ReadFile(filepath.Join(rex.Dir(), "hosts.json"))
	if errors.Is(err, os.ErrNotExist) {
		return hostsFile{}, nil
	}
	if err != nil {
		return hostsFile{}, err
	}
	var f hostsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return hostsFile{}, errors.New("hosts.json is not valid: " + err.Error())
	}
	return f, nil
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

func remoteCmd(args []string, out, errOut io.Writer) (int, bool) {
	if serverTarget != "" {
		fmt.Fprintln(errOut, "gorex remote: this changes the local server only")
		return ExitUsage, true
	}
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex remote: use status or control on|off")
		return ExitUsage, true
	}
	var asJSON *bool
	switch args[0] {
	case "status":
		fs := flagSet("remote status", errOut)
		asJSON = fs.Bool("json", false, "print JSON")
		if code, stop := parse(fs, args[1:]); stop {
			return code, true
		}
	case "control":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			fmt.Fprintln(errOut, "gorex remote control: give on or off")
			return ExitUsage, true
		}
	default:
		fmt.Fprintln(errOut, "gorex remote: use status or control on|off")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	switch args[0] {
	case "status":
		st, err := c.RemoteStatus()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		shown := struct {
			Enabled       bool `json:"enabled"`
			Running       bool `json:"running"`
			ShareSessions bool `json:"shareSessions"`
			RemoteControl bool `json:"remoteControl"`
			Allowed       int  `json:"allowed"`
			Network       bool `json:"network"`
		}{st.Enabled, st.Running, st.ShareSessions, st.RemoteControl, len(st.Allowed), st.Network != nil}
		if *asJSON {
			WriteJSON(out, shown)
			return ExitOK, true
		}
		fmt.Fprintf(out, "enabled=%v running=%v share=%v control=%v allowed=%d network=%v\n",
			shown.Enabled, shown.Running, shown.ShareSessions, shown.RemoteControl, shown.Allowed, shown.Network)
		return ExitOK, true
	case "control":
		on := args[1] == "on"
		if err := c.RemoteControl(on); err != nil {
			fmt.Fprintln(errOut, err)
			return ExitFailed, true
		}
		return ExitOK, true
	}
	fmt.Fprintln(errOut, "gorex remote: use status or control on|off")
	return ExitUsage, true
}

func eventsCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("events", errOut)
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	if err := c.Subscribe(); err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	for ev := range c.Events() {
		b, _ := json.Marshal(ev)
		out.Write(append(b, '\n'))
	}
	return ExitServer, true
}

func waitCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("wait", errOut)
	sid := targetFlag(fs)
	waitExit := fs.Bool("exit", false, "wait for the program to exit")
	idle := fs.Bool("idle", false, "wait for the shell's prompt")
	status := fs.String("status", "", "wait for a program status, as blocked")
	contains := fs.String("contains", "", "wait for the screen to contain text")
	timeout := fs.Duration("timeout", 0, "how long to wait; 0 waits forever")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if !*waitExit && !*idle && *status == "" && *contains == "" {
		fmt.Fprintln(errOut, "gorex wait: give --exit, --idle, --status or --contains")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, *sid)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	var deadline time.Time
	if *timeout > 0 {
		deadline = time.Now().Add(*timeout)
	}
	for {
		infos, err := c.List()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		in, ok := findSession(infos, target)
		if !ok {
			fmt.Fprintf(errOut, "gorex: no session %q\n", target)
			return ExitTarget, true
		}
		ready := true
		if *waitExit && !in.Exited {
			ready = false
		}
		if *idle && !in.Idle {
			ready = false
		}
		if *status != "" && !hasStatus(in, *status) {
			ready = false
		}
		if *contains != "" {
			res, err := c.Capture(target, 64<<10)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return codeFor(err), true
			}
			text, err := plainText(res)
			if err != nil {
				fmt.Fprintln(errOut, err)
				return ExitFailed, true
			}
			if !strings.Contains(text, *contains) {
				ready = false
			}
		}
		if ready {
			if *waitExit {
				return in.ExitCode, true
			}
			return ExitOK, true
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			fmt.Fprintf(errOut, "gorex wait: %s did not become ready in %v\n", target, *timeout)
			return ExitFailed, true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("run", errOut)
	dir := fs.String("dir", "", "directory to start in")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(errOut, "gorex run: no command; put it after --")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	info, err := c.Create(rex.CreateOptions{Command: cmd, Dir: *dir, Cols: 80, Rows: 24})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	defer c.Kill(info.ID)
	in, err := waitExit(c, info.ID, 0)
	if err != nil {
		if errors.Is(err, errNoSession) {
			return ExitTarget, true
		}
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	res, err := c.Capture(info.ID, 0)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	text, err := plainText(res)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	fmt.Fprint(out, text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		fmt.Fprintln(out)
	}
	return in.ExitCode, true
}

func actionCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(errOut, "gorex action: use `gorex action run ACTION [KEY=VALUE...]`")
		return ExitUsage, true
	}
	fs := flagSet("action run", errOut)
	sid := targetFlag(fs)
	if code, stop := parse(fs, args[1:]); stop {
		return code, true
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(errOut, "gorex action run: no action")
		return ExitUsage, true
	}
	id, pairs := rest[0], rest[1:]
	actionArgs := map[string]any{}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			fmt.Fprintf(errOut, "gorex action run: %q is not KEY=VALUE\n", pair)
			return ExitUsage, true
		}
		actionArgs[k] = parseValue(v)
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target := *sid
	if target != "" {
		var err error
		target, err = resolveTarget(c, target)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return codeFor(err), true
		}
	}
	res, err := c.Action(target, id, actionArgs)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if res.Code != "" {
		fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		return ExitFailed, true
	}
	return ExitOK, true
}

// parseValue keeps an action argument a string: the CLI does not know the
// action's schema, and 123 for a name must stay "123".
func parseValue(v string) any { return v }

// errNoSession says a session is no longer listed.
var errNoSession = errors.New("no such session")

func findSession(infos []rex.SessionInfo, sid string) (rex.SessionInfo, bool) {
	for _, in := range infos {
		if in.ID == sid {
			return in, true
		}
	}
	return rex.SessionInfo{}, false
}

func hasStatus(in rex.SessionInfo, want string) bool {
	if in.Status != nil && in.Status.State == want {
		return true
	}
	for _, s := range in.Statuses {
		if s.State == want {
			return true
		}
	}
	return false
}

func waitExit(c *rex.Client, sid string, d time.Duration) (rex.SessionInfo, error) {
	var deadline time.Time
	if d > 0 {
		deadline = time.Now().Add(d)
	}
	for {
		infos, err := c.List()
		if err != nil {
			return rex.SessionInfo{}, err
		}
		in, ok := findSession(infos, sid)
		if !ok {
			return rex.SessionInfo{}, errNoSession
		}
		if in.Exited {
			return in, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return rex.SessionInfo{}, errors.New("timed out waiting for the session to end")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func codeForResult(res rex.LayoutCommand) int {
	switch res.Code {
	case "pane_not_found", "tab_not_found":
		return ExitTarget
	}
	return ExitFailed
}

func codeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	if rex.NotShared(err) || errors.Is(err, errNoSession) || errors.Is(err, errAmbiguous) ||
		strings.Contains(err.Error(), "no session") || strings.Contains(err.Error(), "session has ended") {
		return ExitTarget
	}
	return ExitFailed
}
