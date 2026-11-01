package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gorex/internal/rex"
)

// agentHookTimeout is how long the hook waits for the server: an agent's
// start waits for its hooks.
const agentHookTimeout = 2 * time.Second

// agentHookCmd is what an agent's session-start hook runs (gorex skill
// setup installs it): outside GoRex it does nothing; inside, it tells the
// server that the session runs the agent, with its conversation, so that a
// server that starts the session again resumes it, and prints what the
// agent should know of its session. It exits 0 whatever happens: an
// agent's start never fails for it.
func agentHookCmd(args []string, in io.Reader, out io.Writer) (int, bool) {
	fs := flagSet("agent-hook", io.Discard)
	agent := fs.String("agent", "", "the agent: claude, codex, omp or pi")
	conv := fs.String("conversation", "", "the agent's conversation id; read from the hook's JSON input when not given")
	pid := fs.Int("pid", 0, "the agent's process, else the hook's parent")
	if fs.Parse(args) != nil {
		return ExitOK, true
	}
	sid := contextValue(os.Getenv("GOREX_SESSION"))
	if sid == "" {
		return ExitOK, true
	}
	if *conv == "" && (*agent == "claude" || *agent == "codex") {
		*conv = hookSessionID(in)
	}
	if *pid <= 0 {
		// The server looks up from the hook's shell to the agent.
		*pid = os.Getppid()
	}
	var report *rex.AgentReport
	a := rex.AgentSession{Name: *agent, Conversation: *conv, PID: *pid}
	if rex.ValidAgent(a) == nil {
		report = reportAgent(sid, a)
	}
	host := contextValue(os.Getenv("GOREX_HOST"))
	if host == "" {
		host = "this machine"
	}
	fmt.Fprintf(out, "GoRex session %s on %s. Use gorex ls, gorex send, gorex capture and gorex wait to work in it or in other sessions.\n", sid, host)
	if report != nil && report.Recorded {
		fmt.Fprintln(out, "If the session restarts (a crash, a reboot, gorex server restart), GoRex resumes this conversation in it.")
	}
	const label = "Use two to four lowercase words joined by hyphens, such as `fix-login-redirect`."
	switch {
	case report != nil && report.Pane != "":
		fmt.Fprintf(out, "Its pane is named %q. Keep that name if it already describes the same work; else rename it after your task with `gorex rename -s \"$GOREX_SESSION\" <label>`. %s\n", contextValue(report.Pane), label)
	case report != nil:
		fmt.Fprintf(out, "Name the session: run `gorex rename -s \"$GOREX_SESSION\" <label>` without waiting for the user to ask, as soon as you know the task. %s\n", label)
	default:
		fmt.Fprintf(out, "If the session has no name yet, run `gorex rename -s \"$GOREX_SESSION\" <label>` without waiting for the user to ask, as soon as you know the task. %s Keep the existing name if it already describes the same work.\n", label)
	}
	return ExitOK, true
}

// reportAgent tells the local server that session sid runs agent a, and
// returns its answer, nil when it does not answer in time. It starts no
// server. The server is the session's: GOREX_SERVER, which the session's
// environment names, else this machine's.
func reportAgent(sid string, a rex.AgentSession) *rex.AgentReport {
	sock := os.Getenv("GOREX_SERVER")
	if sock == "" {
		sock = rex.SocketPath()
	}
	done := make(chan *rex.AgentReport, 1)
	go func() {
		c, err := rex.ConnectSocket(sock)
		if err != nil {
			done <- nil
			return
		}
		defer c.Close()
		r, err := c.ReportAgent(sid, a)
		if err != nil {
			done <- nil
			return
		}
		done <- &r
	}()
	select {
	case r := <-done:
		return r
	case <-time.After(agentHookTimeout):
		return nil
	}
}

// hookSessionID reads the session_id of the JSON a Claude Code or Codex
// hook gets on its input, "" when none comes in time.
func hookSessionID(in io.Reader) string {
	if in == nil {
		return ""
	}
	done := make(chan string, 1)
	go func() {
		var v struct {
			SessionID string `json:"session_id"`
		}
		json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&v)
		done <- strings.TrimSpace(v.SessionID)
	}()
	select {
	case id := <-done:
		return id
	case <-time.After(500 * time.Millisecond):
		return ""
	}
}
