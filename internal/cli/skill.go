package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// skillMarker marks the skill this command owns; a file without it is the
// user's and is not replaced.
const skillMarker = "<!-- gorex skill -->"

// skillGuide is what an agent reads.
const skillGuide = `---
name: gorex
description: Drive GoRex sessions: open, type, wait, read and run commands.
---
` + skillMarker + `

# GoRex

You are in a GoRex terminal. Every local session has the ` + "`gorex`" + ` command on
` + "`PATH`" + ` and ` + "`GOREX_SESSION`" + ` set to its session. Use it to work in other
sessions or panes instead of guessing at the UI.

- ` + "`gorex ls`" + ` lists sessions; ` + "`gorex new --dir DIR`" + ` makes one and prints its ID.
- ` + "`gorex send -s ID --enter 'make test'`" + ` types into it.
- ` + "`gorex capture -s ID`" + ` prints its screen as text; ` + "`--raw`" + ` keeps escapes.
- ` + "`gorex wait -s ID --exit`" + ` waits for the program; ` + "`--contains TEXT`" + `,
  ` + "`--status blocked`" + ` and ` + "`--idle`" + ` wait for other conditions.
- ` + "`gorex run -- CMD...`" + ` runs a command in a new session and prints its screen.
- ` + "`gorex action run ACTION [KEY=VALUE...]`" + ` runs a built-in action.
- ` + "`gorex clients`" + ` shows what is attached.

Commands that change the window's layout (` + "`split`" + `, ` + "`close`" + `, ` + "`action`" + `)
need the GoRex window open on that machine.
`

// agentSkill is one agent's skill directory.
type agentSkill struct {
	Name string
	Dir  func(project bool) (string, error)
}

func homeDir() (string, error) { return os.UserHomeDir() }

func skillAgents() []agentSkill {
	return []agentSkill{
		{Name: "claude", Dir: func(project bool) (string, error) {
			if project {
				wd, err := os.Getwd()
				return filepath.Join(wd, ".claude", "skills", "gorex"), err
			}
			base := os.Getenv("CLAUDE_CONFIG_DIR")
			if base == "" {
				h, err := homeDir()
				if err != nil {
					return "", err
				}
				base = filepath.Join(h, ".claude")
			}
			return filepath.Join(base, "skills", "gorex"), nil
		}},
		{Name: "codex", Dir: agentsDir},
		{Name: "opencode", Dir: agentsDir},
		{Name: "pi", Dir: agentsDir},
	}
}

func agentsDir(project bool) (string, error) {
	if project {
		wd, err := os.Getwd()
		return filepath.Join(wd, ".agents", "skills", "gorex"), err
	}
	h, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".agents", "skills", "gorex"), nil
}

// skillState is one agent's result.
type skillState struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	State  string `json:"state"` // missing, installed, current, conflict, failed, removed
	Hook   string `json:"hook,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func skillFile(dir string) string { return filepath.Join(dir, "SKILL.md") }

func readSkill(dir string) (string, error) {
	b, err := os.ReadFile(skillFile(dir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// writeSkill writes the guide, refusing a file it does not own.
func writeSkill(dir string, force bool) (string, error) {
	if st, err := os.Lstat(dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("the skill directory is a symlink")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := skillFile(dir)
	old, err := readSkill(dir)
	if err != nil {
		return "", err
	}
	if old != "" && !strings.Contains(old, skillMarker) && !force {
		return "conflict", errors.New("a skill named gorex already exists and is not GoRex's")
	}
	if old == skillGuide {
		return "current", nil
	}
	tmp, err := os.CreateTemp(dir, ".SKILL.md-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(skillGuide); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return "", err
	}
	return "installed", nil
}

func skillCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex skill: use show, setup, status or remove")
		return ExitUsage, true
	}
	action, args := args[0], args[1:]
	switch action {
	case "show":
		fmt.Fprint(out, skillGuide)
		return ExitOK, true
	case "setup", "status", "remove":
		return skillRun(action, args, out, errOut)
	default:
		fmt.Fprintf(errOut, "gorex skill: unknown action %q\n", action)
		return ExitUsage, true
	}
}

func skillRun(action string, args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("skill "+action, errOut)
	var names agentList
	fs.Var(&names, "agent", "agent to set up (claude, codex, opencode, pi); repeatable")
	project := fs.Bool("project", false, "use the current repository")
	dry := fs.Bool("dry-run", false, "report without changing")
	force := fs.Bool("force", false, "replace a skill GoRex did not install")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintln(errOut, "gorex skill: unexpected arguments")
		return ExitUsage, true
	}
	agents := skillAgents()
	if len(names) > 0 {
		byName := map[string]agentSkill{}
		for _, a := range agents {
			byName[a.Name] = a
		}
		agents = nil
		for _, n := range names {
			a, ok := byName[n]
			if !ok {
				fmt.Fprintf(errOut, "gorex skill: unknown agent %q\n", n)
				return ExitUsage, true
			}
			agents = append(agents, a)
		}
	}
	var states []skillState
	for _, a := range agents {
		dir, err := a.Dir(*project)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitFailed, true
		}
		st := skillState{Agent: a.Name, Path: skillFile(dir)}
		switch action {
		case "setup":
			if *dry {
				st.State = "would install"
				if old, err := readSkill(dir); err == nil && old != "" && !strings.Contains(old, skillMarker) && !*force {
					st.State = "conflict"
				}
				break
			}
			st.State, err = writeSkill(dir, *force)
			if err != nil {
				if st.State == "" {
					st.State = "failed"
				}
				st.Reason = err.Error()
			}
		case "status":
			old, err := readSkill(dir)
			switch {
			case err != nil:
				st.State, st.Reason = "conflict", err.Error()
			case old == "":
				st.State = "missing"
			case old == skillGuide:
				st.State = "current"
			case strings.Contains(old, skillMarker):
				st.State = "installed"
			default:
				st.State = "conflict"
			}
		case "remove":
			old, err := readSkill(dir)
			switch {
			case err != nil:
				st.State, st.Reason = "conflict", err.Error()
			case old == "":
				st.State = "missing"
			case !strings.Contains(old, skillMarker):
				st.State = "conflict"
			default:
				if *dry {
					st.State = "would remove"
					break
				}
				if err := os.Remove(skillFile(dir)); err != nil {
					st.State, st.Reason = "conflict", err.Error()
				} else {
					os.Remove(dir)
					st.State = "removed"
				}
			}
		}
		if !*project {
			switch action {
			case "setup":
				if st.State == "installed" || st.State == "current" || st.State == "would install" {
					st.Hook, err = setHook(a.Name, true, *dry)
				}
			case "remove":
				st.Hook, err = setHook(a.Name, false, *dry)
			case "status":
				st.Hook, err = hookState(a.Name)
			}
			if err != nil {
				st.Reason = err.Error()
				if st.State != "conflict" {
					st.State = "failed"
				}
			}
		}
		states = append(states, st)
	}
	if *asJSON {
		WriteJSON(out, states)
	} else {
		for _, st := range states {
			fmt.Fprintf(out, "%s\t%s\t%s", st.Agent, st.State, st.Path)
			if st.Reason != "" {
				fmt.Fprintf(out, "\t%s", st.Reason)
			}
			fmt.Fprintln(out)
		}
	}
	for _, st := range states {
		if st.State == "conflict" || st.State == "failed" || st.State == "" {
			return ExitFailed, true
		}
	}
	return ExitOK, true
}

// agentList is a repeatable -agent flag.
type agentList []string

func (a *agentList) String() string { return strings.Join(*a, ",") }
func (a *agentList) Set(v string) error {
	*a = append(*a, v)
	return nil
}
