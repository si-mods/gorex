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

	"gorex/internal/config"
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

// GhosttyWarnings, when set by the app, lists the Ghostty options the app
// ignores, for config check. It is nil in tests and tools.
var GhosttyWarnings func() []string

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
	case "layout":
		return layoutCmd(args, out, errOut)
	case "new":
		return newCmd(args, out, errOut)
	case "split":
		return splitCmd(args, out, errOut)
	case "close":
		return closeCmd(args, out, errOut)
	case "rename":
		return renameCmd(args, out, errOut)
	case "send", "type":
		return sendCmd(args, out, errOut)
	case "paste":
		return pasteCmd(args, out, errOut)
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
	case "server":
		return serverCmd(args, out, errOut)
	case "config":
		return configCmd(args, out, errOut)
	case "__internal":
		return internalCmd(args, out, errOut)
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
  gorex [-S HOST] layout [--json]
  gorex split [-s SESSION] [--down] [--dir DIR]
  gorex close [-s SESSION] [--tab]
  gorex rename [-s SESSION] NAME
  gorex new [--dir DIR] [--json] [-- COMMAND...]
  gorex send [-s SESSION] [-w TAB] [-b PANE] [--enter] [--json] TEXT...
  gorex paste [-s SESSION] [-w TAB] [-b PANE] [--json] TEXT...
  gorex capture [-s SESSION] [--history BYTES] [--raw] [--json]
  gorex events
  gorex hosts [ls] [--json]
  gorex skill show|setup|status|remove [--agent NAME] [--project]
  gorex remote status [--json] | address | enable | disable | control on|off | share on|off | layout on|off | pair start|stop | pending | approve KEY | reject KEY | allow NAME KEY | revoke KEY
  gorex server restart
  gorex config path | config check [FILE] | config reload
  gorex wait [-s SESSION] [--json] (--exit | --idle | --status STATE | --contains TEXT) [--timeout D]
  gorex run [--json] [--dir DIR] -- COMMAND...
  gorex action run [-s SESSION] [--json] ACTION [KEY=VALUE...]
  gorex version [--json]

Targets: -s SESSION, -w TAB or -b PANE; -b wins, then -w, then -s.
The session defaults to GOREX_SESSION, set in every terminal GoRex opens.
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

// targetFlags are -s, -w and -b; the pane wins, then the tab, then the
// session. The session defaults to GOREX_SESSION.
type targetFlags struct{ s, w, b *string }

func targetFlag(fs *flag.FlagSet) *targetFlags {
	def := ""
	if serverTarget == "" {
		def = os.Getenv("GOREX_SESSION")
	}
	return &targetFlags{
		s: fs.String("s", def, "session ID"),
		w: fs.String("w", "", "tab label or ID"),
		b: fs.String("b", "", "pane label or ID"),
	}
}

func (t *targetFlags) value() string {
	if *t.b != "" {
		return *t.b
	}
	if *t.w != "" {
		return *t.w
	}
	return *t.s
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
	fmt.Fprintln(tw, "ID\tPROGRAM\tOWNER\tDIR\tATTACHED\tSTATUS")
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
		owner := in.OwnerName
		if owner == "" && in.Owner != "" {
			owner = "remote"
		}
		if owner == "" {
			owner = "local"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", in.ID, program, owner, in.Dir, in.Attached, status)
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
	fmt.Fprintln(tw, "SESSION\tPRINCIPAL\tPEER\tREMOTE\tVIA\tAGE")
	for _, cl := range clients {
		peer := cl.Peer
		if peer == "" {
			peer = "local"
		}
		name := cl.Principal
		if name == "" && !cl.Remote {
			name = "local"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%v\t%ds\n", cl.SessionID, name, peer, cl.Remote, cl.Via, cl.Age)
	}
	tw.Flush()
	return ExitOK, true
}

func layoutCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("layout", errOut)
	asJSON := fs.Bool("json", false, "print the raw JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	raw, err := c.Layout()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if *asJSON || len(raw) == 0 {
		fmt.Fprintln(out, string(raw))
		return ExitOK, true
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		fmt.Fprintln(out, string(raw))
		return ExitOK, true
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(out, string(b))
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
	if sid.value() == "" {
		fmt.Fprintln(errOut, "gorex split: no session; use -s or GOREX_SESSION")
		return ExitTarget, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, sid.value())
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
	if sid.value() == "" {
		fmt.Fprintln(errOut, "gorex close: no session; use -s or GOREX_SESSION")
		return ExitTarget, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, sid.value())
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

func renameCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("rename", errOut)
	sid := targetFlag(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) == 0 {
		fmt.Fprintln(errOut, "gorex rename: no name")
		return ExitUsage, true
	}
	name := strings.Join(fs.Args(), " ")
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, sid.value())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	res, err := c.Command(rex.LayoutCommand{Op: "pane.rename", SID: target, Name: name})
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
	target, err := resolveTarget(c, sid.value())
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

func pasteCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("paste", errOut)
	sid := targetFlag(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	text := strings.Join(fs.Args(), " ")
	if text == "" {
		fmt.Fprintln(errOut, "gorex paste: no text")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	target, err := resolveTarget(c, sid.value())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if err := c.Paste(target, text); err != nil {
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
	target, err := resolveTarget(c, sid.value())
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

func configCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex config: use path, check or reload")
		return ExitUsage, true
	}
	if serverTarget != "" && args[0] != "reload" {
		fmt.Fprintln(errOut, "gorex config: path and check read this machine's file")
		return ExitUsage, true
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(out, config.Path())
		return ExitOK, true
	case "check":
		path := config.Path()
		if len(args) > 1 {
			path = args[1]
		}
		cfg, err := config.LoadCheck(path)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitFailed, true
		}
		defer cfg.Close()
		fmt.Fprintf(out, "path: %s\nbindings: %d\nactions: %d\nmodes: %d\n", path, len(cfg.Bindings), len(cfg.Actions), len(cfg.Modes))
		for _, w := range cfg.Warnings {
			fmt.Fprintln(out, "warning:", w)
		}
		if GhosttyWarnings != nil {
			for _, w := range GhosttyWarnings() {
				fmt.Fprintln(out, "warning:", w)
			}
		}
		return ExitOK, true
	case "reload":
		c, code := connect(errOut)
		if c == nil {
			return code, true
		}
		defer c.Close()
		res, err := c.Command(rex.LayoutCommand{Op: "config.reload"})
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
	fmt.Fprintln(errOut, "gorex config: use path, check or reload")
	return ExitUsage, true
}

func internalCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) > 0 && args[0] == "session-start" {
		id := contextValue(os.Getenv("GOREX_SESSION"))
		if id == "" {
			return ExitOK, true
		}
		host := contextValue(os.Getenv("GOREX_HOST"))
		if host == "" {
			host = "this machine"
		}
		fmt.Fprintf(out, "GoRex session %s on %s. Use gorex ls, gorex send, gorex capture and gorex wait to work in it or in other sessions.\n", id, host)
		return ExitOK, true
	}
	fmt.Fprintln(errOut, "gorex: unknown internal command")
	return ExitUsage, true
}

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
		fmt.Fprintln(errOut, "gorex server: restart")
		return ExitUsage, true
	}
	if len(args) != 1 || args[0] != "restart" {
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
		fmt.Fprintln(errOut, "gorex remote: status | address | enable | disable | control on|off | share on|off | layout on|off | pair start|stop | pending | approve KEY | reject KEY | allow NAME KEY | revoke KEY")
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
	switch args[0] {
	case "status":
	case "address":
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
	case "control":
		on, ok := onOff("control")
		if !ok {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.RemoteControl(on) }
	case "share":
		on, ok := onOff("share")
		if !ok {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.ShareSessions(on) }
	case "layout":
		on, ok := onOff("layout")
		if !ok {
			return ExitUsage, true
		}
		run = func(c *rex.Client) error { return c.ShareLayout(on) }
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
		switch args[0] {
		case "approve":
			run = func(c *rex.Client) error { return c.ApprovePairing(key) }
		case "reject":
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
			Enabled       bool `json:"enabled"`
			Running       bool `json:"running"`
			ShareSessions bool `json:"shareSessions"`
			ShareLayout   bool `json:"shareLayout"`
			RemoteControl bool `json:"remoteControl"`
			Allowed       int  `json:"allowed"`
			Network       bool `json:"network"`
		}{st.Enabled, st.Running, st.ShareSessions, st.ShareLayout, st.RemoteControl, len(st.Allowed), st.Network != nil}
		if asJSON {
			WriteJSON(out, shown)
			return ExitOK, true
		}
		fmt.Fprintf(out, "enabled=%v running=%v share=%v layout=%v control=%v allowed=%d network=%v\n",
			shown.Enabled, shown.Running, shown.ShareSessions, shown.ShareLayout, shown.RemoteControl, shown.Allowed, shown.Network)
		return ExitOK, true
	}
	if args[0] == "address" {
		st, err := c.RemoteStatus()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		fmt.Fprintln(out, st.Address)
		return ExitOK, true
	}
	if args[0] == "pending" {
		st, err := c.RemoteStatus()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		WriteJSON(out, st.Pending)
		return ExitOK, true
	}
	if err := run(c); err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	return ExitOK, true
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
	asJSON := fs.Bool("json", false, "print JSON when ready")
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
	target, err := resolveTarget(c, sid.value())
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
			if *asJSON {
				WriteJSON(out, map[string]any{"session": target, "exitCode": in.ExitCode})
			}
			if *waitExit {
				return in.ExitCode, true
			}
			return ExitOK, true
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			msg := fmt.Sprintf("%s did not become ready in %v", target, *timeout)
			if *asJSON {
				WriteJSON(out, map[string]any{"session": target, "error": msg})
			}
			fmt.Fprintln(errOut, "gorex wait:", msg)
			return ExitFailed, true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("run", errOut)
	dir := fs.String("dir", "", "directory to start in")
	asJSON := fs.Bool("json", false, "print JSON")
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
		if *asJSON {
			WriteJSON(out, map[string]any{"error": err.Error()})
		}
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	defer c.Kill(info.ID)
	in, err := waitExit(c, info.ID, 0)
	if err != nil {
		if *asJSON {
			WriteJSON(out, map[string]any{"session": info.ID, "error": err.Error()})
		}
		if errors.Is(err, errNoSession) {
			return ExitTarget, true
		}
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	}
	res, err := c.Capture(info.ID, 0)
	if err != nil {
		if *asJSON {
			WriteJSON(out, map[string]any{"session": info.ID, "error": err.Error()})
		}
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	text, err := plainText(res)
	if err != nil {
		if *asJSON {
			WriteJSON(out, map[string]any{"session": info.ID, "error": err.Error()})
		}
		fmt.Fprintln(errOut, err)
		return ExitFailed, true
	}
	if *asJSON {
		WriteJSON(out, map[string]any{"session": info.ID, "exitCode": in.ExitCode, "text": text})
	} else {
		fmt.Fprint(out, text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			fmt.Fprintln(out)
		}
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
	asJSON := fs.Bool("json", false, "print JSON")
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
	target := sid.value()
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
		if *asJSON {
			WriteJSON(out, res)
		}
		fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		return ExitFailed, true
	}
	if *asJSON {
		WriteJSON(out, res)
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
