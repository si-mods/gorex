package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gorex/internal/rex"
)

func delegateCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("delegate", errOut)
	agent := fs.String("agent", "auto", "agent CLI ID (auto, claude, omp, codex, agy, devin)")
	dir := fs.String("dir", "", "working directory on the target host")
	name := fs.String("name", "", "tab label on the target host (defaults to a short prompt summary)")
	mode := fs.String("mode", "full", "execution mode hint (full, read-only, or yolo)")
	sessionID := fs.String("session", "", "existing delegated session ID for a warm follow-up turn")
	resume := fs.String("resume", "", "agent conversation ID for a cold follow-up")
	resultSID := fs.String("result", "", "fetch the latest result of a delegated session ID")
	async := fs.Bool("async", false, "return immediately after starting the turn")
	keep := fs.Bool("keep", false, "keep the delegated tab open after clean synchronous exit")
	timeout := fs.Duration("timeout", 10*time.Minute, "synchronous wait timeout (0 waits indefinitely)")
	cancelOnTimeout := fs.Bool("cancel-on-timeout", false, "kill the remote session if --timeout expires")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}

	if *timeout < 0 {
		fmt.Fprintln(errOut, "gorex delegate: --timeout must not be negative")
		return ExitUsage, true
	}
	if *mode != "full" && *mode != "read-only" && *mode != "yolo" {
		fmt.Fprintf(errOut, "gorex delegate: invalid --mode %q (want full, read-only, or yolo)\n", *mode)
		return ExitUsage, true
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *resultSID == "" && prompt == "" {
		fmt.Fprintln(errOut, "gorex delegate: prompt required after -- (or pass --result SESSION)")
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
	if !h.Can(rex.CapabilityDelegate) {
		fmt.Fprintln(errOut, "gorex delegate: server does not support delegate; upgrade GoRex on that host")
		return ExitFailed, true
	}

	hostLabel := serverTarget
	if hostLabel == "" {
		hostLabel = h.Host.Name
	}

	// Fetch result of an existing delegated session.
	if *resultSID != "" {
		res, err := c.DelegateResult(*resultSID)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitFailed, true
		}
		return emitDelegateResult(out, errOut, hostLabel, res, *asJSON), true
	}

	// Handle Ctrl-C during synchronous waits by cancelling the remote session.
	ctx, stop := signal.NotifyContext(onInterrupt(context.Background()), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Always start the remote turn asynchronously so we receive the session
	// handle immediately and never block a single RPC past the server's
	// keepalive timeout. For synchronous mode we then subscribe to OSC 7501
	// session events + periodic DelegateResult checks.
	var started rex.DelegateResult
	closeOnComplete := false
	if *sessionID != "" {
		started, err = c.DelegateSend(rex.DelegateSendOptions{
			SID:    *sessionID,
			Prompt: prompt,
			Async:  true,
		})
	} else {
		closeOnComplete = !*keep && !*async
		started, err = c.Delegate(rex.DelegateOptions{
			Agent:  *agent,
			Dir:    *dir,
			Name:   *name,
			Prompt: prompt,
			Mode:   *mode,
			Resume: *resume,
			From:   delegateOrigin(),
			Keep:   true, // CLI closes cleanly after observing completion when !*keep
			Async:  true,
		})
	}
	if err != nil {
		if *asJSON {
			WriteJSON(out, map[string]any{
				"host":    hostLabel,
				"session": *sessionID,
				"error":   err.Error(),
			})
		} else {
			fmt.Fprintln(errOut, err)
		}
		return ExitFailed, true
	}

	if *async || started.Status != "running" {
		if closeOnComplete && started.Status == "completed" {
			_ = c.Kill(started.Session)
		}
		return emitDelegateResult(out, errOut, hostLabel, started, *asJSON), true
	}

	res, exitCode := waitDelegateSubscribed(ctx, c, hostLabel, started, *timeout, *cancelOnTimeout, closeOnComplete)
	if exitCode == ExitInterrupted {
		return ExitInterrupted, true
	}
	return emitDelegateResult(out, errOut, hostLabel, res, *asJSON), true
}

// waitDelegateSubscribed subscribes to the server's OSC 7501 / session
// event stream and checks DelegateResult on each update (plus a 2s safety
// tick that keeps the control connection alive and handles agents without
// OSC 7501).
func waitDelegateSubscribed(
	ctx context.Context,
	c *rex.Client,
	hostLabel string,
	started rex.DelegateResult,
	timeout time.Duration,
	cancelOnTimeout bool,
	closeOnComplete bool,
) (rex.DelegateResult, int) {
	sid := started.Session
	_ = c.Subscribe()
	events := c.Events()

	var timerCh <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timerCh = t.C
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	check := func() (rex.DelegateResult, bool, error) {
		r, err := c.DelegateResult(sid)
		if err != nil {
			return rex.DelegateResult{}, false, err
		}
		if r.Status != "running" {
			if closeOnComplete && r.Status == "completed" {
				_ = c.Kill(sid)
			}
			return r, true, nil
		}
		return r, false, nil
	}

	// Check once immediately in case the turn already finished before Subscribe.
	if r, done, err := check(); err == nil && done {
		return r, ExitOK
	}

	for {
		select {
		case <-ctx.Done():
			_ = c.Kill(sid)
			return rex.DelegateResult{}, ExitInterrupted
		case <-timerCh:
			if cancelOnTimeout {
				_ = c.Kill(sid)
				r := started
				r.Status = "timeout"
				r.Error = "timed out waiting for delegated turn (session cancelled)"
				return r, ExitFailed
			}
			r := started
			r.Status = "running"
			r.Error = "timed out waiting for delegated turn (session still running)"
			return r, ExitOK
		case _, ok := <-events:
			if !ok {
				// Connection dropped: return the session handle so the caller
				// can reconnect with wait / delegate --result instead of
				// spawning a duplicate task.
				r := started
				r.Status = "running"
				r.Error = fmt.Sprintf("connection to %s lost while waiting (session still running)", hostLabel)
				return r, ExitServer
			}
			if r, done, err := check(); err == nil && done {
				return r, ExitOK
			}
		case <-tick.C:
			r, done, err := check()
			if err != nil {
				out := started
				out.Status = "running"
				out.Error = err.Error()
				return out, ExitServer
			}
			if done {
				return r, ExitOK
			}
		}
	}
}

type delegateOutput struct {
	Host         string `json:"host,omitempty"`
	Session      string `json:"session"`
	Agent        string `json:"agent,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exitCode,omitempty"`
	AgentSession string `json:"agentSession,omitempty"`
	Result       string `json:"result,omitempty"`
	RawCapture   string `json:"rawCapture,omitempty"`
	Error        string `json:"error,omitempty"`
}

func emitDelegateResult(out, errOut io.Writer, host string, r rex.DelegateResult, asJSON bool) int {
	if asJSON {
		WriteJSON(out, delegateOutput{
			Host:         host,
			Session:      r.Session,
			Agent:        r.Agent,
			Mode:         r.Mode,
			Status:       r.Status,
			ExitCode:     r.ExitCode,
			AgentSession: r.AgentSession,
			Result:       r.Result,
			RawCapture:   r.RawCapture,
			Error:        r.Error,
		})
	} else {
		if r.Result != "" {
			fmt.Fprintln(out, r.Result)
		} else if r.RawCapture != "" {
			fmt.Fprintln(out, r.RawCapture)
		} else {
			fmt.Fprintf(out, "%s (%s): %s\n", r.Session, r.Agent, r.Status)
		}
		if r.Error != "" {
			fmt.Fprintln(errOut, r.Error)
		}
	}
	switch r.Status {
	case "completed", "running":
		return ExitOK
	default:
		return ExitFailed
	}
}

func delegateOrigin() string {
	host, _ := os.Hostname()
	sid := os.Getenv("GOREX_SESSION")
	switch {
	case host != "" && sid != "":
		if len(sid) > 8 {
			sid = sid[:8]
		}
		return host + "/" + sid
	case host != "":
		return host
	case sid != "":
		return sid
	default:
		return ""
	}
}
