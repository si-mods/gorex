package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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

// Main runs the command line. handled is false when args are the app's, as
// an AppKit argument, and the GUI should start.
func Main(args []string, out, errOut io.Writer, version string) (code int, handled bool) {
	if len(args) == 0 {
		return 0, false
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
	case "new":
		return newCmd(args, out, errOut)
	case "send", "type":
		return sendCmd(args, out, errOut)
	case "capture":
		return captureCmd(args, out, errOut)
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
	return 0, false
}

// usage writes what the command line does.
func usage(w io.Writer) {
	fmt.Fprint(w, `gorex — the GoRex command line

Usage:
  gorex ls [--json]
  gorex new [--dir DIR] [--json] [-- COMMAND...]
  gorex send [-s SESSION] [--enter] [--json] TEXT...
  gorex capture [-s SESSION] [--history BYTES] [--raw] [--json]
  gorex wait [-s SESSION] (--exit | --idle | --status STATE) [--timeout D]
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
	fs.Usage = func() {}
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

// connect connects to the server, starting one when none runs.
func connect(errOut io.Writer) (*rex.Client, int) {
	c, err := rex.Connect()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return nil, ExitServer
	}
	return c, ExitOK
}

// targetFlag adds -s/--session, defaulting to GOREX_SESSION.
func targetFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("GOREX_SESSION")
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
	if err := c.Input(*sid, []byte(text)); err != nil {
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
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	res, err := c.Capture(*sid, *history)
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
	term, err := terminal.New(terminal.Options{Conn: nopConn{}})
	if err != nil {
		return "", err
	}
	defer term.Close()
	term.Resize(res.Cols, res.Rows)
	term.Feed(res.Screen)
	return term.Text(), nil
}

// nopConn is the Conn of a terminal that only shows what the app feeds it.
type nopConn struct{}

func (nopConn) Read([]byte) (int, error)    { select {} }
func (nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (nopConn) Close() error                { return nil }

func waitCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("wait", errOut)
	sid := targetFlag(fs)
	waitExit := fs.Bool("exit", false, "wait for the program to exit")
	idle := fs.Bool("idle", false, "wait for the shell's prompt")
	status := fs.String("status", "", "wait for a program status, as blocked")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if !*waitExit && !*idle && *status == "" {
		fmt.Fprintln(errOut, "gorex wait: give --exit, --idle or --status")
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	deadline := time.Now().Add(*timeout)
	for {
		infos, err := c.List()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitServer, true
		}
		in, ok := findSession(infos, *sid)
		if !ok {
			fmt.Fprintf(errOut, "gorex: no session %q\n", *sid)
			return ExitTarget, true
		}
		if *waitExit && in.Exited {
			return in.ExitCode, true
		}
		if *idle && in.Idle {
			return ExitOK, true
		}
		if *status != "" && hasStatus(in, *status) {
			return ExitOK, true
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(errOut, "gorex wait: %s did not become ready in %v\n", *sid, *timeout)
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
	in, ok := waitExit(c, info.ID, 0)
	if !ok {
		fmt.Fprintf(errOut, "gorex run: %s did not end\n", info.ID)
		return ExitFailed, true
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
	res, err := c.Action(*sid, id, actionArgs)
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

func parseValue(v string) any {
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return n
	}
	return v
}

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

func waitExit(c *rex.Client, sid string, d time.Duration) (rex.SessionInfo, bool) {
	deadline := time.Now().Add(d)
	for {
		infos, err := c.List()
		if err == nil {
			if in, ok := findSession(infos, sid); ok && in.Exited {
				return in, true
			}
		}
		if d > 0 && time.Now().After(deadline) {
			return rex.SessionInfo{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func codeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	if rex.NotShared(err) || strings.Contains(err.Error(), "no session") {
		return ExitTarget
	}
	return ExitFailed
}
