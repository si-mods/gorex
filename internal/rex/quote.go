package rex

import (
	"path/filepath"
	"strings"
)

// ClaudeResume is the command that resumes Claude Code's conversation id,
// in config dir config when not "", as the shell at path reads it.
func ClaudeResume(shell, id, config string) (string, bool) {
	q := QuoterOf(shell)
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

// QuoterOf returns the word quoting of the shell at path:
// path/filepath.Base of it, without .exe. It is nil for a shell the quoting
// does not know, which gets no command rather than a wrong one.
func QuoterOf(shell string) func(string) (string, bool) {
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
