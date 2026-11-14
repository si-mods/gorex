package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
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
// The pane-naming flag is accepted for the stub's sake and changes
// nothing here.
func internalSkillCmd(args []string, out io.Writer) (int, bool) {
	fs := flagSet("__internal skill", io.Discard)
	_ = fs.Bool("feature-pane-naming", true, "")
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
	agent := fs.String("agent", "", "")
	naming := fs.Bool("feature-pane-naming", true, "")
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

	var msg strings.Builder
	fmt.Fprintln(&msg, "You're running in a GoRex terminal.")
	fmt.Fprintln(&msg, "```text")
	if server != "" {
		fmt.Fprintf(&msg, "GOREX_SERVER=%s\n", server)
	}
	fmt.Fprintf(&msg, "GOREX_SESSION=%s\n", sid)
	if pane != "" {
		fmt.Fprintf(&msg, "GOREX_PANE=%s\n", pane)
	}
	if host != "" {
		fmt.Fprintf(&msg, "GOREX_HOST=%s\n", host)
	} else if client != "" {
		fmt.Fprintf(&msg, "GOREX_CLIENT=%s\n", client)
	}
	fmt.Fprintln(&msg, "```")
	fmt.Fprintln(&msg)
	fmt.Fprintln(&msg, "`GOREX_SESSION` identifies your session, and `GOREX_PANE` numbers")
	fmt.Fprintln(&msg, "your terminal pane; both are set in your shell. Commands select the")
	fmt.Fprintln(&msg, "pane with `-s \"$GOREX_SESSION\"`. The user may be viewing a different")
	fmt.Fprintln(&msg, "session or pane.")
	fmt.Fprintln(&msg)
	fmt.Fprintln(&msg, "These inherited IDs haven't been checked against the server. If a")
	fmt.Fprintln(&msg, "target can't be found, run `gorex ls --json` to see the sessions on")
	fmt.Fprintln(&msg, "the connected server.")
	fmt.Fprintln(&msg)
	fmt.Fprintln(&msg, "Use the `gorex` command to create sessions, split panes, run commands,")
	fmt.Fprintln(&msg, "and read terminal output. The gorex skill provides instructions and")
	fmt.Fprintln(&msg, "examples. If the skill isn't installed, run `gorex skill show` to read")
	fmt.Fprintln(&msg, "the same guide.")
	if *naming {
		fmt.Fprintln(&msg)
		fmt.Fprintln(&msg, "Label the pane as part of handling the first request that identifies")
		fmt.Fprintln(&msg, "the work. Run `gorex rename -s \"$GOREX_SESSION\" \"<label>\"`")
		fmt.Fprintln(&msg, "without waiting for the user to ask, before loading the skill. Use a short")
		fmt.Fprintln(&msg, "natural-language phrase in the user's language (for example, `Fix login")
		fmt.Fprintln(&msg, "redirect` or `修复登录重定向`), not a hyphenated slug. Name the task or")
		fmt.Fprintln(&msg, "project. Keep the existing label if it already describes the same work.")
	}
	if *agent == "agy" {
		WriteJSON(out, map[string]any{
			"injectSteps": []any{
				map[string]any{
					"systemMessage": map[string]any{
						"systemMessage": strings.TrimRight(msg.String(), "\n"),
					},
				},
			},
		})
		return ExitOK, true
	}
	fmt.Fprint(out, msg.String())
	return ExitOK, true
}
