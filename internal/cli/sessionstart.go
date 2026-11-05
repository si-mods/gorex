package cli

import (
	"fmt"
	"io"
	"os"
)

// internalCmd runs the commands the files `gorex skill setup` installs.
// They are not part of the documented command line.
func internalCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex: unknown internal command")
		return ExitUsage, true
	}
	switch args[0] {
	case "skill":
		return internalSkillCmd(args[1:], out)
	case "session-start":
		return sessionStartCmd(args[1:], out)
	default:
		fmt.Fprintf(errOut, "gorex: unknown internal command %q\n", args[0])
		return ExitUsage, true
	}
}

// internalSkillCmd prints the guide the stub skill tells an agent to read.
// The session-naming flag is accepted for the stub's sake and changes
// nothing here.
func internalSkillCmd(args []string, out io.Writer) (int, bool) {
	fs := flagSet("__internal skill", io.Discard)
	_ = fs.Bool("feature-session-naming", true, "")
	if fs.Parse(args) != nil {
		return ExitOK, true
	}
	fmt.Fprint(out, gorexGuide)
	return ExitOK, true
}

// sessionStartCmd prints what an agent should know at the start of a
// session in a GoRex terminal. Outside GoRex it prints nothing. It exits 0
// whatever happens, so an agent's start never fails for it.
func sessionStartCmd(args []string, out io.Writer) (int, bool) {
	fs := flagSet("__internal session-start", io.Discard)
	_ = fs.String("agent", "", "")
	naming := fs.Bool("feature-session-naming", true, "")
	if fs.Parse(args) != nil {
		return ExitOK, true
	}
	sid := contextValue(os.Getenv("GOREX_SESSION"))
	if sid == "" {
		return ExitOK, true
	}
	pane := contextValue(os.Getenv("GOREX_PANE"))
	server := printable(os.Getenv("GOREX_SERVER"))
	host := contextValue(os.Getenv("GOREX_HOST"))
	client := contextValue(os.Getenv("GOREX_CLIENT"))

	fmt.Fprintln(out, "You're running in a GoRex terminal.")
	fmt.Fprintln(out, "```text")
	if server != "" {
		fmt.Fprintf(out, "GOREX_SERVER=%s\n", server)
	}
	fmt.Fprintf(out, "GOREX_SESSION=%s\n", sid)
	if pane != "" {
		fmt.Fprintf(out, "GOREX_PANE=%s\n", pane)
	}
	if host != "" {
		fmt.Fprintf(out, "GOREX_HOST=%s\n", host)
	} else if client != "" {
		fmt.Fprintf(out, "GOREX_CLIENT=%s\n", client)
	}
	fmt.Fprintln(out, "```")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "`GOREX_SESSION` identifies your session, and `GOREX_PANE` identifies")
	fmt.Fprintln(out, "your terminal pane. Both are set in your shell. Pass")
	fmt.Fprintln(out, "`-s \"$GOREX_SESSION\"` and `-b \"$GOREX_PANE\"` to select them in GoRex")
	fmt.Fprintln(out, "commands. The user may be viewing a different session or pane.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "These inherited IDs haven't been checked against the server. If a")
	fmt.Fprintln(out, "target can't be found, run `gorex ls --json` to see the sessions on")
	fmt.Fprintln(out, "the connected server.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Use the `gorex` command to create sessions, split panes, run commands,")
	fmt.Fprintln(out, "and read terminal output. The gorex skill provides instructions and")
	fmt.Fprintln(out, "examples. If the skill isn't installed, run `gorex skill show` to read")
	fmt.Fprintln(out, "the same guide.")
	if *naming {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Name the session as part of handling the first request that identifies")
		fmt.Fprintln(out, "the work. Run `gorex rename -s \"$GOREX_SESSION\" <label>` without waiting")
		fmt.Fprintln(out, "for the user to ask, before loading the skill. Use two to four lowercase")
		fmt.Fprintln(out, "words joined by hyphens, such as `fix-login-redirect`. Name the task or")
		fmt.Fprintln(out, "project. Keep the existing label if it already describes the same work.")
	}
	return ExitOK, true
}
