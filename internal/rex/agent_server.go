//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"gorex/internal/layout"
)

// A session keeps the coding agent it runs while the agent's process
// does: its session-start hook reports it ("agent"), and the server
// journals it, so that a server that starts the session again after a
// crash, a reboot or a restart types the command that resumes its
// conversation. The record ends with the agent's process, so that a pane
// back at its shell is not resumed later.

const (
	// agentGrace is how long an agent a restart resumed may take to show
	// in the session's foreground before the server gives it up.
	agentGrace = 30 * time.Second
	// resumeQuiet is how long the shell of a restarted session is quiet,
	// having printed its prompt, before the resume command is typed;
	// resumeWaitMost is how long that is waited for at most.
	resumeQuiet    = 300 * time.Millisecond
	resumeWaitMost = 10 * time.Second
)

// notAgents are programs between an agent and its hook, or that start the
// agent: the hook's shell, and gorex itself.
var notAgents = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true,
	"tcsh": true, "csh": true, "nu": true, "pwsh": true, "powershell": true, "cmd": true,
	"gorex": true,
}

// agentProcess finds the agent a report of session ss comes from: from,
// a process of the agent or of its hook, and its parents up to the first
// that is not a shell nor gorex, which must be the program in the
// session's foreground. An agent another runs (a subagent, through a
// tool's shell) is not, and is not recorded.
func agentProcess(ss *session, from int) (int, error) {
	shell := ss.p.pid()
	fg := ss.p.foreground()
	if fg <= 0 || fg == shell {
		return 0, errors.New("no program runs in the session's foreground")
	}
	pid := from
	for range 16 {
		if pid <= 1 || pid == shell {
			break
		}
		name := strings.ToLower(strings.TrimSuffix(inspect(pid).programName(), ".exe"))
		if !notAgents[name] {
			if pid == fg {
				return pid, nil
			}
			return 0, errors.New("the agent is not the session's foreground program")
		}
		pid = parentPID(pid)
	}
	return 0, errors.New("the agent is not the session's foreground program")
}

// reportAgent records the agent a session's hook reports.
func (s *Server) reportAgent(req Request, peer string) (AgentReport, error) {
	if req.Agent == nil {
		return AgentReport{}, errors.New("rex: no agent")
	}
	a := *req.Agent
	if err := ValidAgent(a); err != nil {
		return AgentReport{}, err
	}
	ss, err := s.use(req.SID, peer)
	if err != nil {
		return AgentReport{}, err
	}
	s.mu.Lock()
	l := s.layout
	s.mu.Unlock()
	r := AgentReport{}
	walkPanes(l, func(n *layout.Node) {
		if n.SID == req.SID && n.Host == "" {
			r.Pane = n.Name
		}
	})
	a.PID, err = agentProcess(ss, a.PID)
	if err != nil {
		r.Reason = err.Error()
		return r, nil
	}
	ss.setAgent(a)
	s.journal.agent(ss.id, &a)
	s.notify("sessions")
	r.Recorded = true
	return r, nil
}

// agentEnded journals that session id's agent ended.
func (s *Server) agentEnded(id string) { s.journal.agent(id, nil) }

// watch has the server journal the end of ss's agent.
func (s *Server) watch(ss *session) {
	ss.mu.Lock()
	ss.agentGone = s.agentEnded
	ss.mu.Unlock()
}

// setAgent records the agent the session runs.
func (s *session) setAgent(a AgentSession) {
	s.mu.Lock()
	s.agent, s.agentTyped = &a, time.Time{}
	s.mu.Unlock()
}

// takeAgent forgets the session's agent, and tells whether it had one.
func (s *session) takeAgent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	had := s.agent != nil
	s.agent = nil
	return had
}

// watchAgent ends the session's agent once its process ended, and learns
// the process of one a restart resumed: the program in the foreground
// that the resume command started, which names the conversation. in is
// what runs in the session now. It returns the agent that runs still.
func (s *session) watchAgent(in *SessionInfo) *AgentSession {
	s.mu.Lock()
	a := s.agent
	if a == nil {
		s.mu.Unlock()
		return nil
	}
	gone := false
	switch {
	case in.Exited:
		gone = true
	case a.PID > 0:
		gone = !processAlive(a.PID)
	case s.agentTyped.IsZero():
		// The resume command is still to type.
	case !in.Idle && in.Foreground > 0 && slices.ContainsFunc(in.Args, func(arg string) bool {
		return strings.Contains(arg, a.Conversation)
	}):
		a = &AgentSession{Name: a.Name, Conversation: a.Conversation, PID: in.Foreground}
		s.agent = a
	case time.Since(s.agentTyped) > agentGrace:
		gone = true
	}
	if !gone {
		s.mu.Unlock()
		c := *a
		return &c
	}
	s.agent = nil
	done := s.agentGone
	s.mu.Unlock()
	if done != nil {
		done(s.id)
	}
	s.notify()
	return nil
}

// refreshAgent looks whether the session's agent runs still.
func (s *session) refreshAgent() {
	s.mu.Lock()
	in := SessionInfo{Exited: s.exited}
	s.mu.Unlock()
	if !in.Exited {
		in.PID = s.p.pid()
		s.foreground(&in)
	}
	s.watchAgent(&in)
}

// resumeAgent types the command that resumes a's conversation into the
// shell the session just started, once the shell printed its prompt and
// went quiet: when the agent exits, the pane is back at its shell.
func (s *session) resumeAgent(a AgentSession) {
	cmd := ResumeCommand(a)
	if cmd == "" {
		return
	}
	s.mu.Lock()
	s.agent = &AgentSession{Name: a.Name, Conversation: a.Conversation}
	s.mu.Unlock()
	go func() {
		for start := time.Now(); time.Since(start) < resumeWaitMost; {
			time.Sleep(50 * time.Millisecond)
			s.mu.Lock()
			out, last, exited := s.output, s.lastOutput, s.exited
			s.mu.Unlock()
			if exited {
				return
			}
			if out > 0 && time.Since(last) >= resumeQuiet {
				break
			}
		}
		s.mu.Lock()
		if s.agent != nil && s.agent.PID == 0 {
			s.agentTyped = time.Now()
		}
		s.mu.Unlock()
		if err := s.inputProgram([]byte(cmd + "\r")); err != nil {
			return
		}
		time.AfterFunc(agentGrace+time.Second, s.refreshAgent)
	}()
}

// walkPanes calls f with each pane of layout l.
func walkPanes(l json.RawMessage, f func(n *layout.Node)) {
	if len(l) == 0 {
		return
	}
	st := layout.Store{}
	if json.Unmarshal(l, &st) != nil {
		return
	}
	if st.Version < 2 {
		st.Sessions = []layout.Workspace{{Layout: st.Layout}}
	}
	var walk func(n *layout.Node)
	walk = func(n *layout.Node) {
		if n == nil {
			return
		}
		if n.A == nil || n.B == nil {
			f(n)
			return
		}
		walk(n.A)
		walk(n.B)
	}
	for _, w := range st.Sessions {
		for _, t := range w.Layout.Tabs {
			for _, n := range t.Roots() {
				walk(n)
			}
		}
	}
}
