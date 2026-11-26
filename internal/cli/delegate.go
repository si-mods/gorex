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
	mode := fs.String("mode", "full", "execution mode hint (full or read-only)")
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
	if *mode != "full" && *mode != "read-only" {
		fmt.Fprintf(errOut, "gorex delegate: invalid --mode %q (want full or read-only)\n", *mode)
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

	timeoutMs := 0
	if *timeout > 0 {
		timeoutMs = int((*timeout) / time.Millisecond)
	}

	// Handle Ctrl-C during synchronous waits by cancelling the remote session
	// once known.
	ctx, stop := signal.NotifyContext(onInterrupt(context.Background()), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Warm follow-up turn on an already-running delegated session.
	if *sessionID != "" {
		resCh := make(chan delegateCallRes, 1)
		go func() {
			r, err := c.DelegateSend(rex.DelegateSendOptions{
				SID:       *sessionID,
				Prompt:    prompt,
				Async:     *async,
				TimeoutMs: timeoutMs,
			})
			resCh <- delegateCallRes{res: r, err: err}
		}()
		select {
		case <-ctx.Done():
			_ = c.Kill(*sessionID)
			return ExitInterrupted, true
		case got := <-resCh:
			if got.err != nil {
				if *asJSON {
					WriteJSON(out, map[string]any{
						"host":    hostLabel,
						"session": *sessionID,
						"status":  "running",
						"error":   got.err.Error(),
					})
				} else {
					fmt.Fprintln(errOut, got.err)
				}
				return ExitServer, true
			}
			return emitDelegateResult(out, errOut, hostLabel, got.res, *asJSON), true
		}
	}

	from := delegateOrigin()
	resCh := make(chan delegateCallRes, 1)
	go func() {
		r, err := c.Delegate(rex.DelegateOptions{
			Agent:           *agent,
			Dir:             *dir,
			Prompt:          prompt,
			Mode:            *mode,
			Resume:          *resume,
			From:            from,
			Keep:            *keep,
			Async:           *async,
			TimeoutMs:       timeoutMs,
			CancelOnTimeout: *cancelOnTimeout,
		})
		resCh <- delegateCallRes{res: r, err: err}
	}()

	select {
	case <-ctx.Done():
		c.Close()
		return ExitInterrupted, true
	case got := <-resCh:
		if got.err != nil {
			if *asJSON {
				WriteJSON(out, map[string]any{
					"host":  hostLabel,
					"error": got.err.Error(),
				})
			} else {
				fmt.Fprintln(errOut, got.err)
			}
			return ExitFailed, true
		}
		return emitDelegateResult(out, errOut, hostLabel, got.res, *asJSON), true
	}
}

type delegateCallRes struct {
	res rex.DelegateResult
	err error
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
