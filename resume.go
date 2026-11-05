package main

import (
	"log"
	"path/filepath"
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
		if c, ok := claudeResume(shell, s.AgentSession, s.AgentConfigDir); ok {
			cont = c
		}
	}
	return &resumeHint{cmd: cmd, cont: cont}
}

// claudeResume is the command that resumes Claude Code's conversation id,
// in config dir config when not "", as the shell at path reads it.
func claudeResume(shell, id, config string) (string, bool) {
	q := quoterOf(shell)
	if q == nil {
		return "", false
	}
	w, ok := q(id)
	if !ok {
		return "", false
	}
	cmd := "claude --resume " + w
	if config == "" {
		return cmd, true
	}
	switch strings.ToLower(strings.TrimSuffix(filepath.Base(shell), ".exe")) {
	case "fish", "csh", "tcsh":
		// No NAME=value prefix in these.
		d, _ := q(config)
		return "env CLAUDE_CONFIG_DIR=" + d + " " + cmd, true
	case "pwsh", "powershell":
		return "$env:CLAUDE_CONFIG_DIR=" + powerShellQuoted(config) + "; " + cmd, true
	case "cmd":
		if strings.ContainsAny(config, "%!\"\r\n") {
			return "", false
		}
		return `set "CLAUDE_CONFIG_DIR=` + config + `" && ` + cmd, true
	}
	d, _ := q(config)
	return "CLAUDE_CONFIG_DIR=" + d + " " + cmd, true
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
	name = strings.ToLower(strings.TrimSuffix(filepath.Base(name), ".exe"))
	return agentContinue[name]
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
	q := quoterOf(shell)
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
	if name := strings.ToLower(strings.TrimSuffix(filepath.Base(shell), ".exe")); (name == "pwsh" || name == "powershell") && words[0] != args[0] {
		words[0] = "& " + words[0]
	}
	return strings.Join(words, " "), true
}

// quoter is the word quoting of a shell family.
type quoter func(string) (string, bool)

// quoterOf returns the quoting of the shell at path:
// path/filepath.Base of it, without .exe. It is nil for a shell the quoting
// does not know, which gets no command rather than a wrong one.
func quoterOf(shell string) quoter {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(shell), ".exe"))
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "ash", "yash", "csh", "tcsh":
		return posixWord
	case "fish":
		return fishWord
	case "pwsh", "powershell":
		return powerShellWord
	case "cmd":
		return cmdWord
	}
	return nil
}

// plainWord tells whether s reads as one word of every shell here: letters,
// digits and punctuation none of them takes for syntax. An argument as
// ordinary as an @scope or a comma is quoted: PowerShell takes @name for a
// splat and a comma for an array.
func plainWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_-./:=+", r):
		default:
			return false
		}
	}
	return true
}

// posixWord is s as one word of sh and its kin: in single quotes, where a
// quote is the closing one, an escaped one, and the opening one.
func posixWord(s string) (string, bool) {
	if s == "" {
		return "''", true
	}
	if plainWord(s) {
		return s, true
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", true
}

// fishWord is s as one word of fish: in single quotes, where only a
// backslash and a single quote are escaped.
func fishWord(s string) (string, bool) {
	if s == "" {
		return "''", true
	}
	if plainWord(s) {
		return s, true
	}
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'", true
}

// powerShellWord is s as one word of PowerShell: in single quotes, where a
// quote, ASCII or curly, is doubled. Single quotes take nothing else, $
// included.
func powerShellWord(s string) (string, bool) {
	if s == "" {
		return "''", true
	}
	if plainWord(s) {
		return s, true
	}
	return powerShellQuoted(s), true
}

// powerShellQuote doubles the quotes PowerShell takes for a single one:
// ' and the curly ones, U+2018 to U+201B.
var powerShellQuote = strings.NewReplacer("'", "''",
	"‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛")

// powerShellQuoted is s in PowerShell's single quotes.
func powerShellQuoted(s string) string {
	return "'" + powerShellQuote.Replace(s) + "'"
}

// cmdWord is s as one word of cmd.exe: bare when it reads as one, else in
// double quotes, where & | < > ^ are text. What even quotes do not stop
// from expanding, % and !, gets no command: cmd reads it differently from
// what ran.
func cmdWord(s string) (string, bool) {
	if s == "" {
		return `""`, true
	}
	if plainWord(s) {
		return s, true
	}
	if strings.ContainsAny(s, "%!\"\r\n") {
		return "", false
	}
	return `"` + s + `"`, true
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
