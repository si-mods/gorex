package rex

import (
	"errors"
	"regexp"
)

// AgentSession is a coding agent that a session runs, as the agent's
// session-start hook reports it (gorex agent-hook): a server that starts
// the session again after a crash, a reboot or a restart types the
// command that resumes the conversation (ResumeCommand).
type AgentSession struct {
	// Name is the agent: claude, omp or pi.
	Name string `json:"name"`
	// Conversation is the agent's own id of the conversation.
	Conversation string `json:"conversation"`
	// PID is the agent's process, which has the session's terminal in the
	// foreground; the record ends with it. 0 is an agent a restart typed
	// the resume command of, until its process is known. In a report
	// ("agent") it is a process of the agent or of its hook, from which
	// the server looks up to the agent.
	PID int `json:"pid,omitempty"`
}

// AgentReport is the server's answer to "agent".
type AgentReport struct {
	// Recorded tells that the server keeps the agent; Reason, why not.
	Recorded bool   `json:"recorded"`
	Reason   string `json:"reason,omitempty"`
	// Pane is the name the layout gives the session's pane, "" for none.
	Pane string `json:"pane,omitempty"`
}

// agentCommands are the agents a session resumes, by name: the command
// that resumes a conversation, and the separator before its id.
var agentCommands = map[string]struct{ prefix, sep string }{
	"claude": {"claude --resume", " "},
	"omp":    {"omp --resume", "="},
	"pi":     {"pi --session", " "},
}

// conversationID is what a conversation id may be: letters, digits, dots,
// hyphens and underscores, which no shell GoRex starts (sh, zsh, bash,
// fish, nu, PowerShell, cmd) reads as anything but a word, so that a
// resume command types the same everywhere, unquoted.
var conversationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidAgent tells whether a is an agent a session can resume.
func ValidAgent(a AgentSession) error {
	if _, ok := agentCommands[a.Name]; !ok {
		return errors.New("rex: not an agent GoRex resumes: " + a.Name)
	}
	if !conversationID.MatchString(a.Conversation) {
		return errors.New("rex: not a conversation id GoRex resumes")
	}
	return nil
}

// ResumeCommand is the command line that resumes a's conversation, ""
// for an agent ValidAgent refuses.
func ResumeCommand(a AgentSession) string {
	if ValidAgent(a) != nil {
		return ""
	}
	c := agentCommands[a.Name]
	return c.prefix + c.sep + a.Conversation
}
