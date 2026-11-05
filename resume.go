package main

import (
	"log"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// resumeHint is what a pane's session ran when the layout was saved, for a
// pane whose session is lost: the command, as the pane's shell reads it,
// which one click types again, and for a coding agent, the command that
// continues its last conversation there.
type resumeHint struct {
	// cmd is the command line, quoted for the shell.
	cmd string
	// cont continues the agent's last conversation, "" for a program that
	// is no agent GoRex knows: the agent finds the conversation itself, or
	// for Claude Code, resumes the one the layout kept.
	cont string
}

// resumeHintFor is the hint for the program the layout saved of pane s,
// nil when no shell can run it again as it was: an unknown shell, or
// arguments one cannot take unquoted. shell is the pane's shell, its path.
func resumeHintFor(shell string, s *savedNode) *resumeHint {
	cmd, ok := resumeCommand(shell, s.Prog, s.Args)
	if !ok {
		return nil
	}
	cont := continueCommand(s.Prog)
	// Claude Code's own conversation, not the last one of the directory,
	// which another pane there may have had.
	// The layout is a file: an id that is no UUID is not typed.
	if cont == agentContinue["claude"] && rex.IsUUID(s.AgentSession) {
		if c, ok := rex.ClaudeResume(shell, s.AgentSession, s.AgentConfigDir); ok {
			cont = c
		}
	}
	return &resumeHint{cmd: cmd, cont: cont}
}

// agentContinue is the command that continues a coding agent's last
// conversation, by the name of its program: the agent itself resolves which
// one that was, in the directory the pane starts in, so nothing has to know
// a conversation id.
var agentContinue = map[string]string{
	"claude": "claude --continue",
	"codex":  "codex resume --last",
	"omp":    "omp --continue",
	"pi":     "pi --continue",
}

// continueCommand is the continue command of the agent named name, "" for a
// program that is no agent here.
func continueCommand(name string) string {
	return agentContinue[rex.ShellName(name)]
}

// resumeCommand is argv as the shell at path reads it, "" for a shell the
// quoting does not know. prog names the program when the layout kept no
// arguments, as an inspection that found none.
func resumeCommand(shell, prog string, args []string) (string, bool) {
	if len(args) == 0 {
		if prog == "" {
			return "", false
		}
		args = []string{prog}
	}
	q := rex.QuoterOf(shell)
	if q == nil {
		return "", false
	}
	words := make([]string, 0, len(args))
	for _, a := range args {
		w, ok := q(a)
		if !ok {
			return "", false
		}
		words = append(words, w)
	}
	// PowerShell reads a quoted first word as a string, not a command: the
	// call operator runs it.
	if name := rex.ShellName(shell); (name == "pwsh" || name == "powershell") && words[0] != args[0] {
		words[0] = "& " + words[0]
	}
	return strings.Join(words, " "), true
}

// runResume types the pane's command into its shell, as the user asked by
// the click: the strip goes, and the command runs where the pane's session
// started.
func (a *App) runResume(p *Pane, cmd string) {
	if p.resume == nil || p.SID == "" || cmd == "" {
		return
	}
	p.resume = nil
	if c := a.clientOf(p.host); c != nil {
		if err := c.Input(p.SID, []byte(cmd+"\r")); err != nil {
			log.Printf("running %q again: %v", cmd, err)
		}
	}
	a.focusReq = p
	a.changed()
}

// resumeBar is the strip over a restored pane's terminal: what the pane ran
// before its session was lost, and the button that runs it again.
func (a *App) resumeBar(c *ui.Context, k *colors, p *Pane) ui.Element {
	h := p.resume
	row := ui.Row(c.Key("resume")).Height(30).Shrink(0).Padding(0, 8).Gap(8).AlignItems(ui.Center).
		Background(k.hover).Radius(7)
	row.Children(func() {
		ui.Text(c.Key("what"), locales.ResumeLastRun()).FontSize(12).TextColor(k.textMuted).
			Shrink(0).SingleLine()
		ui.Text(c.Key("cmd"), h.cmd).Font(monoFamily()).FontSize(11.5).TextColor(k.text.Alpha(0.8)).
			Grow(1).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(h.cmd)
		if h.cont != "" {
			if ui.Button(c.Key("continue"), locales.ResumeContinue()).Tooltip(locales.ResumeContinueTip()).Clicked() {
				a.runResume(p, h.cont)
			}
		}
		if ui.Button(c.Key("run"), locales.ResumeRun()).Tooltip(locales.ResumeRunTip()).Clicked() {
			a.runResume(p, h.cmd)
		}
		if ui.Button(c.Key("close"), "×").Tooltip(locales.ResumeDismiss()).Clicked() {
			p.resume = nil
			a.changed()
		}
	})
	return row
}
