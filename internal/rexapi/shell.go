package rexapi

import (
	"os"
	"runtime"
	"strings"
)

// shellOptions are the options of a com.superlogical.terminal.shell
// block, every one Rex's terminal creator lists: a Rex client sends them
// when it makes a block, its theme among them.
type shellOptions struct {
	cwd     string
	command []string
	// theme, size and input apply once the block runs.
	theme *theme
	cols  int
	rows  int
	input string
}

// parseShellOptions reads a shell's options as Rex documents them. GoRex
// starts each pane through its app, which runs a command in place of the
// login shell: shell, login, args and path_prepend become that command.
// exit is accepted and does nothing: a GoRex pane stays when its program
// exits.
func parseShellOptions(o args) (shellOptions, *apiError) {
	var so shellOptions
	for name := range o {
		switch name {
		case "cwd", "command", "args", "shell", "login", "path_prepend", "initial_input", "size", "theme", "exit":
		default:
			return so, errBadRequest("a shell has no option %q", name)
		}
	}
	var aerr *apiError
	if so.cwd, _, aerr = o.str("cwd"); aerr != nil {
		return so, aerr
	}
	command, hasCommand, aerr := o.strs("command")
	if aerr != nil {
		return so, aerr
	}
	argv, hasArgs, aerr := o.strs("args")
	if aerr != nil {
		return so, aerr
	}
	if hasCommand && command != nil && hasArgs && argv != nil {
		return so, errBadRequest("send command or args, not both")
	}
	shell, _, aerr := o.str("shell")
	if aerr != nil {
		return so, aerr
	}
	login, hasLogin, aerr := o.boolean("login")
	if aerr != nil {
		return so, aerr
	}
	if !hasLogin {
		login = true
	}
	prepend, _, aerr := o.strs("path_prepend")
	if aerr != nil {
		return so, aerr
	}
	if so.input, _, aerr = o.str("initial_input"); aerr != nil {
		return so, aerr
	}
	if exit, ok, aerr := o.obj("exit"); aerr != nil {
		return so, aerr
	} else if ok && exit != nil {
		for name := range exit {
			if name != "on_completion" && name != "quick_exit_threshold_ms" {
				return so, errBadRequest("exit has no %q", name)
			}
		}
	}
	if size, ok, aerr := o.obj("size"); aerr != nil {
		return so, aerr
	} else if ok && size != nil {
		cols, okc, aerr := size.number("columns")
		if aerr != nil {
			return so, aerr
		}
		rows, okr, aerr := size.number("rows")
		if aerr != nil {
			return so, aerr
		}
		if !okc || !okr || cols < 1 || rows < 1 || cols > 65535 || rows > 65535 {
			return so, errBadRequest("a size has columns and rows from 1 to 65535")
		}
		so.cols, so.rows = int(cols), int(rows)
	}
	if t, ok, aerr := o.obj("theme"); aerr != nil {
		return so, aerr
	} else if ok && t != nil {
		th, aerr := parseTheme(t)
		if aerr != nil {
			return so, aerr
		}
		so.theme = &th
	}
	if shell == "none" && len(command) == 0 {
		return so, errBadRequest("shell none needs a command")
	}
	so.command = paneCommand(shell, login, command, argv, prepend)
	return so, nil
}

// paneCommand returns what a pane runs for the options, or nil for the
// user's login shell, which is what GoRex runs by default.
func paneCommand(shell string, login bool, command, argv, prepend []string) []string {
	if runtime.GOOS == "windows" {
		// GoRex's Windows panes take a command as given.
		if len(command) > 0 {
			return command
		}
		if shell != "" && shell != "none" {
			return append([]string{shell}, argv...)
		}
		return nil
	}
	sh := shell
	if sh == "" || sh == "none" {
		sh = cmpOr(os.Getenv("SHELL"), "/bin/sh")
	}
	path := ""
	if len(prepend) > 0 {
		path = `PATH="` + strings.Join(prepend, ":") + `:$PATH"; export PATH; `
	}
	switch {
	case len(command) > 0 && shell == "none":
		if path == "" {
			return command
		}
		return append([]string{"/bin/sh", "-c", path + `exec "$@"`, "sh"}, command...)
	case len(command) > 0:
		// The shell reads its startup files, then runs the program with
		// its arguments unchanged.
		c := []string{sh}
		if login {
			c = append(c, "-l")
		}
		return append(append(c, "-c", path+`exec "$@"`, sh), command...)
	case argv != nil:
		return append([]string{sh}, argv...)
	case shell == "" && login && path == "":
		return nil
	default:
		c := []string{sh}
		if login {
			c = append(c, "-l")
		}
		if path == "" {
			return c
		}
		return append([]string{"/bin/sh", "-c", path + `exec "$@"`, "sh"}, c...)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// applyShellOptions applies the options that act on a running block:
// its size, its colors and the text typed into it first.
func (g *Gateway) applyShellOptions(sids []string, leaves []specLeaf) {
	for i, sid := range sids {
		if i >= len(leaves) || sid == "" {
			continue
		}
		so := leaves[i].tree.shell
		if so.cols > 0 && so.rows > 0 {
			g.opt.Client.Resize(sid, so.cols, so.rows)
		}
		if so.theme != nil {
			g.setTheme(blockID(sid), *so.theme)
		}
		if so.input != "" {
			g.opt.Client.Input(sid, []byte(so.input))
		}
	}
}
