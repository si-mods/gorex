package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gorex/internal/rex"
)

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
	// Go exits on a signal without running defers: end the session here.
	ctx, stop := signal.NotifyContext(onInterrupt(context.Background()), os.Interrupt, syscall.SIGTERM)
	defer stop()
	in, err := waitExit(ctx, c, info.ID, 0)
	if ctx.Err() != nil {
		return ExitInterrupted, true
	}
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

// floatAction maps `gorex float` arguments to those of `gorex action run`:
// its flags first, then the action its subcommand names, then its

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

func waitExit(ctx context.Context, c *rex.Client, sid string, d time.Duration) (rex.SessionInfo, error) {
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
		select {
		case <-ctx.Done():
			return rex.SessionInfo{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
