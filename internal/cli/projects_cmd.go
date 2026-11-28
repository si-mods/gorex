package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

var (
	errNoProject          = errors.New("no such project")
	errNoAvailableProject = errors.New("no project on this server; create one with `gorex project new <name> --dir <dir>`")
)

type sessionPlace struct {
	projectID string
	project   string
	tabID     string
}

type sessionJSON struct {
	rex.SessionInfo
	ProjectID string `json:"projectId,omitempty"`
	Project   string `json:"project,omitempty"`
	TabID     string `json:"tabId,omitempty"`
}

type projectPaneJSON struct {
	SID     string `json:"sid,omitempty"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Program string `json:"program,omitempty"`
	Title   string `json:"title,omitempty"`
	Float   bool   `json:"float,omitempty"`
}

type projectTabJSON struct {
	ID     string            `json:"id"`
	Name   string            `json:"name,omitempty"`
	Active bool              `json:"active,omitempty"`
	Panes  []projectPaneJSON `json:"panes"`
}

type projectJSON struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Dir    string           `json:"dir"`
	Space  string           `json:"space,omitempty"`
	Color  string           `json:"color,omitempty"`
	Active bool             `json:"active,omitempty"`
	Used   *time.Time       `json:"used,omitempty"`
	Tabs   []projectTabJSON `json:"tabs"`
}

// localDir expands ~ and relative paths against this process's working
// directory when targeting the local server; remote paths stay untouched so
// the remote host resolves them.
func localDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || serverTarget != "" {
		return dir
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if dir == "~" {
				return home
			}
			return filepath.Join(home, dir[2:])
		}
	}
	if !filepath.IsAbs(dir) {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
	}
	return filepath.Clean(dir)
}

func sessionPlaces(raw json.RawMessage) map[string]sessionPlace {
	out := map[string]sessionPlace{}
	if len(raw) == 0 {
		return out
	}
	st := layout.Parse(raw)
	for _, w := range st.Sessions {
		projID, projName := "", ""
		if w.Dir != "" {
			projID, projName = w.ID, w.Name
		}
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, l := range layout.Leaves(r) {
					if l.Host == "" && l.SID != "" {
						out[l.SID] = sessionPlace{
							projectID: projID,
							project:   projName,
							tabID:     t.ID,
						}
					}
				}
			}
		}
	}
	return out
}

func projectsOf(st *layout.Store) []*layout.Workspace {
	var out []*layout.Workspace
	for i := range st.Sessions {
		if st.Sessions[i].Dir != "" {
			out = append(out, &st.Sessions[i])
		}
	}
	return out
}

// resolveProject resolves a project by exact ID (with or without Rex API's
// "session:" prefix), exact name, case-insensitive name, folder path, or ID
// suffix (at least 4 characters).
func resolveProject(st *layout.Store, target string, local bool) (*layout.Workspace, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errNoProject
	}
	trimmed := strings.TrimPrefix(target, "session:")
	projects := projectsOf(st)

	for _, w := range projects {
		if w.ID == target || w.ID == trimmed {
			return w, nil
		}
	}

	var exactName []*layout.Workspace
	for _, w := range projects {
		if w.Name == target {
			exactName = append(exactName, w)
		}
	}
	if len(exactName) == 1 {
		return exactName[0], nil
	}
	if len(exactName) > 1 {
		return nil, fmt.Errorf("%w: project %q", errAmbiguous, target)
	}

	var foldName []*layout.Workspace
	for _, w := range projects {
		if strings.EqualFold(w.Name, target) {
			foldName = append(foldName, w)
		}
	}
	if len(foldName) == 1 {
		return foldName[0], nil
	}
	if len(foldName) > 1 {
		return nil, fmt.Errorf("%w: project %q", errAmbiguous, target)
	}

	dirCand := target
	if local {
		dirCand = localDir(dirCand)
		if real, err := filepath.EvalSymlinks(dirCand); err == nil {
			dirCand = real
		}
	}
	if dirCand != "" {
		for _, w := range projects {
			if layout.SameDir(w.Dir, dirCand) {
				return w, nil
			}
		}
	}

	if len(trimmed) >= 4 {
		low := strings.ToLower(trimmed)
		var suffix []*layout.Workspace
		for _, w := range projects {
			if strings.HasSuffix(strings.ToLower(w.ID), low) {
				suffix = append(suffix, w)
			}
		}
		if len(suffix) == 1 {
			return suffix[0], nil
		}
		if len(suffix) > 1 {
			ids := make([]string, len(suffix))
			for i, w := range suffix {
				ids[i] = w.ID
			}
			return nil, fmt.Errorf("%w: %s", errAmbiguous, strings.Join(ids, ", "))
		}
	}

	return nil, fmt.Errorf("%w: %s", errNoProject, target)
}

// defaultProject selects the current or active project when --project is
// omitted:
//  1. the project holding the caller's own GOREX_SESSION (when local);
//  2. the active project shown by a GoRex window (Store.Windows);
//  3. the project whose folder equals or contains dir (when headless/remote);
//  4. the most recently used project, else the first project.
//
// When the server has no projects at all, it returns errNoAvailableProject
// instead of creating an orphan session.
func defaultProject(st *layout.Store, currentSID, dir string, local bool) (*layout.Workspace, error) {
	projects := projectsOf(st)
	if len(projects) == 0 {
		return nil, errNoAvailableProject
	}
	if currentSID != "" {
		for _, w := range projects {
			if workspaceHasSID(w, currentSID) {
				return w, nil
			}
		}
	}
	for _, id := range st.Windows {
		if w := st.Space(id); w != nil && w.Dir != "" {
			return w, nil
		}
	}
	if dir != "" {
		cand := dir
		if local {
			cand = localDir(cand)
			if real, err := filepath.EvalSymlinks(cand); err == nil {
				cand = real
			}
		}
		if w := matchProjectPath(projects, cand); w != nil {
			return w, nil
		}
	}
	var latest *layout.Workspace
	for _, w := range projects {
		if !w.Used.IsZero() && (latest == nil || !w.Used.Before(latest.Used)) {
			latest = w
		}
	}
	if latest != nil {
		return latest, nil
	}
	return projects[0], nil
}

func workspaceHasSID(w *layout.Workspace, sid string) bool {
	if w == nil || sid == "" {
		return false
	}
	for _, t := range w.Tabs {
		for _, r := range t.Roots() {
			for _, l := range layout.Leaves(r) {
				if l.Host == "" && l.SID == sid {
					return true
				}
			}
		}
	}
	return false
}

func matchProjectPath(projects []*layout.Workspace, dir string) *layout.Workspace {
	if dir == "" {
		return nil
	}
	clean := filepath.Clean(dir)
	var best *layout.Workspace
	for _, w := range projects {
		if layout.SameDir(w.Dir, clean) {
			return w
		}
		rel, err := filepath.Rel(filepath.Clean(w.Dir), clean)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if best == nil || len(w.Dir) > len(best.Dir) {
				best = w
			}
		}
	}
	return best
}

// reorderFlags moves known flags ahead of positional arguments so subcommands
// like `gorex project new myproj --dir /path --json` accept flags either
// before or after positional arguments.
func reorderFlags(args []string, boolFlags []string, valFlags []string) []string {
	isBool := make(map[string]bool, len(boolFlags))
	for _, f := range boolFlags {
		isBool["-"+f] = true
		isBool["--"+f] = true
	}
	isVal := make(map[string]bool, len(valFlags))
	for _, f := range valFlags {
		isVal["-"+f] = true
		isVal["--"+f] = true
	}
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if a == "-h" || a == "--help" || isBool[a] {
			flags = append(flags, a)
			continue
		}
		if isVal[a] {
			flags = append(flags, a)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		if k, _, ok := strings.Cut(a, "="); ok && (isBool[k] || isVal[k]) {
			flags = append(flags, a)
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			// Unknown flags stay with flags so flag.FlagSet reports usage.
			flags = append(flags, a)
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func projectsCmd(args []string, out, errOut io.Writer) (int, bool) {
	return projectsListCmd("projects", args, out, errOut)
}

func projectCmd(args []string, out, errOut io.Writer) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "gorex project: use `gorex [-S HOST] project ls|new|rename|move`")
		return ExitUsage, true
	}
	switch args[0] {
	case "ls", "list":
		return projectsListCmd("project ls", args[1:], out, errOut)
	case "new", "create":
		return projectNewCmd(args[1:], out, errOut)
	case "rename":
		return projectRenameCmd(args[1:], out, errOut)
	case "move":
		return projectMoveCmd(args[1:], out, errOut)
	case "-h", "--help", "help":
		fmt.Fprint(out, `gorex project — manage GoRex projects

Usage:
  gorex [-S HOST] projects [--json]
  gorex [-S HOST] project ls [--json]
  gorex [-S HOST] project new [NAME] [--dir DIR] [--space SPACE] [--color COLOR] [--json]
  gorex [-S HOST] project rename PROJECT NAME [--json]
  gorex [-S HOST] project move [--no-project] SESSION [PROJECT] [--json]
`)
		return ExitOK, true
	default:
		fmt.Fprintf(errOut, "gorex project: unknown subcommand %q (use ls, new, rename or move)\n", args[0])
		return ExitUsage, true
	}
}

func projectsListCmd(cmdName string, args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet(cmdName, errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(errOut, "gorex %s: unexpected arguments\n", cmdName)
		return ExitUsage, true
	}
	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()

	raw, err := c.Layout()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	infos, _ := c.List()
	bySID := make(map[string]rex.SessionInfo, len(infos))
	for _, in := range infos {
		bySID[in.ID] = in
	}

	st := layout.Parse(raw)
	activeID := ""
	if cur, err := defaultProject(&st, currentLocalSession(), "", serverTarget == ""); err == nil && cur != nil {
		activeID = cur.ID
	}

	projects := projectsOf(&st)
	list := make([]projectJSON, 0, len(projects))
	for _, w := range projects {
		pj := projectJSON{
			ID:     w.ID,
			Name:   w.Name,
			Dir:    w.Dir,
			Space:  w.Space,
			Color:  w.Color,
			Active: w.ID == activeID || slices.Contains(st.Windows, w.ID),
			Tabs:   make([]projectTabJSON, 0, len(w.Tabs)),
		}
		if !w.Used.IsZero() {
			u := w.Used
			pj.Used = &u
		}
		for ti, t := range w.Tabs {
			tj := projectTabJSON{
				ID:     t.ID,
				Name:   t.Name,
				Active: ti == w.Active,
				Panes:  []projectPaneJSON{},
			}
			for _, l := range layout.Leaves(t.Root) {
				tj.Panes = append(tj.Panes, buildPaneJSON(l, false, bySID))
			}
			for _, fl := range t.Floats {
				for _, l := range layout.Leaves(fl.Pane) {
					tj.Panes = append(tj.Panes, buildPaneJSON(l, true, bySID))
				}
			}
			pj.Tabs = append(pj.Tabs, tj)
		}
		list = append(list, pj)
	}

	if *asJSON {
		WriteJSON(out, list)
		return ExitOK, true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tACTIVE\tTABS\tPANES\tSESSIONS\tDIR")
	for _, p := range list {
		paneCount := 0
		var sids []string
		for _, t := range p.Tabs {
			paneCount += len(t.Panes)
			for _, pn := range t.Panes {
				if pn.SID != "" {
					sids = append(sids, pn.SID)
				}
			}
		}
		act := ""
		if p.Active {
			act = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			p.ID,
			printable(p.Name),
			act,
			len(p.Tabs),
			paneCount,
			strings.Join(sids, ","),
			printable(p.Dir),
		)
	}
	tw.Flush()
	return ExitOK, true
}

func buildPaneJSON(l *layout.Node, isFloat bool, bySID map[string]rex.SessionInfo) projectPaneJSON {
	p := projectPaneJSON{
		SID:   l.SID,
		ID:    l.ID,
		Name:  l.Name,
		Dir:   l.Dir,
		Float: isFloat,
	}
	if l.Host == "" && l.SID != "" {
		if in, ok := bySID[l.SID]; ok {
			if in.Dir != "" {
				p.Dir = in.Dir
			}
			prog := in.Program
			if prog == "" {
				prog = in.Shell
			}
			p.Program = prog
			p.Title = in.Title
		}
	}
	if p.Program == "" && l.Prog != "" {
		p.Program = l.Prog
	}
	return p
}

func currentLocalSession() string {
	if serverTarget != "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv("GOREX_SESSION"))
}

func requireProjectsCapability(c *rex.Client, cmd string, errOut io.Writer) int {
	h, err := c.Hello()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer
	}
	if !h.Can(rex.CapabilityProjects) {
		fmt.Fprintf(errOut, "gorex %s: server does not support projects; upgrade GoRex on that host\n", cmd)
		return ExitFailed
	}
	return ExitOK
}

func projectNewCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("project new", errOut)
	dir := fs.String("dir", "", "project folder (defaults to current directory)")
	space := fs.String("space", "", "space name (defaults to Default)")
	color := fs.String("color", "", "project color preset or #rrggbb")
	asJSON := fs.Bool("json", false, "print JSON")
	reordered := reorderFlags(args, []string{"json"}, []string{"dir", "space", "color"})
	if code, stop := parse(fs, reordered); stop {
		return code, true
	}
	dirSet := false
	spaceSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "dir":
			dirSet = true
		case "space":
			spaceSet = true
		}
	})
	name := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if name == "" && !dirSet {
		fmt.Fprintln(errOut, "gorex project new: use `gorex [-S HOST] project new [NAME] --dir DIR`")
		return ExitUsage, true
	}
	folder := strings.TrimSpace(*dir)
	if folder == "" {
		if serverTarget != "" {
			fmt.Fprintln(errOut, "gorex project new: --dir is required with -S")
			return ExitUsage, true
		}
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return ExitFailed, true
		}
		folder = wd
	} else {
		folder = localDir(folder)
	}

	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	if code := requireProjectsCapability(c, "project new", errOut); code != ExitOK {
		return code, true
	}

	cmd := rex.LayoutCommand{
		Op:    "project.create",
		Name:  name,
		Dir:   folder,
		Color: strings.TrimSpace(*color),
	}
	if spaceSet {
		sp := *space
		cmd.SpaceName = &sp
	}
	res, err := c.Command(cmd)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if res.Code != "" {
		if res.Code == "exists" && res.Space != "" {
			fmt.Fprintf(errOut, "gorex: %s: %s (project %s)\n", res.Code, res.Error, res.Space)
		} else {
			fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		}
		return codeForResult(res), true
	}

	if *asJSON {
		outName, outDir, outSpace := name, folder, *space
		if raw, err := c.Layout(); err == nil && len(raw) > 0 {
			st := layout.Parse(raw)
			if w := st.Space(res.Space); w != nil {
				outName, outDir, outSpace = w.Name, w.Dir, w.Space
			}
		}
		WriteJSON(out, map[string]any{
			"id":      res.Space,
			"name":    outName,
			"dir":     outDir,
			"space":   outSpace,
			"tabId":   res.Tab,
			"session": res.New,
		})
	} else {
		fmt.Fprintln(out, res.Space)
	}
	return ExitOK, true
}

func projectRenameCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("project rename", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	reordered := reorderFlags(args, []string{"json"}, nil)
	if code, stop := parse(fs, reordered); stop {
		return code, true
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(errOut, "gorex project rename: use `gorex [-S HOST] project rename PROJECT NAME`")
		return ExitUsage, true
	}
	target := rest[0]
	newName := layout.Label(strings.Join(rest[1:], " "))
	if newName == "" {
		fmt.Fprintln(errOut, "gorex project rename: empty name")
		return ExitUsage, true
	}

	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	if code := requireProjectsCapability(c, "project rename", errOut); code != ExitOK {
		return code, true
	}

	raw, err := c.Layout()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	st := layout.Parse(raw)
	proj, err := resolveProject(&st, target, serverTarget == "")
	if err != nil {
		fmt.Fprintln(errOut, "gorex project rename:", err)
		return codeFor(err), true
	}

	res, err := c.Command(rex.LayoutCommand{Op: "project.update", Space: proj.ID, Name: newName})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if res.Code != "" {
		fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		return codeForResult(res), true
	}
	if *asJSON {
		WriteJSON(out, map[string]any{
			"id":   proj.ID,
			"name": newName,
			"dir":  proj.Dir,
		})
	}
	return ExitOK, true
}

func projectMoveCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("project move", errOut)
	noProj := fs.Bool("no-project", false, "move out of any project (into Tabs with no project)")
	asJSON := fs.Bool("json", false, "print JSON")
	reordered := reorderFlags(args, []string{"no-project", "json"}, nil)
	if code, stop := parse(fs, reordered); stop {
		return code, true
	}
	rest := fs.Args()
	if (*noProj && len(rest) != 1) || (!*noProj && len(rest) != 2) {
		fmt.Fprintln(errOut, "gorex project move: use `gorex [-S HOST] project move SESSION PROJECT` (or `--no-project SESSION`)")
		return ExitUsage, true
	}

	c, code := connect(errOut)
	if c == nil {
		return code, true
	}
	defer c.Close()
	if code := requireProjectsCapability(c, "project move", errOut); code != ExitOK {
		return code, true
	}

	raw, err := c.Layout()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	st := layout.Parse(raw)

	var dst *layout.Workspace
	toSpace := ""
	if !*noProj {
		dst, err = resolveProject(&st, rest[1], serverTarget == "")
		if err != nil {
			fmt.Fprintln(errOut, "gorex project move:", err)
			return codeFor(err), true
		}
		toSpace = dst.ID
	}

	// Accept either an explicit tab ID (from `gorex projects --json` / Rex API)
	// or any session/pane/tab target supported by resolveTarget.
	tabID, sid := findTabByID(&st, rest[0])
	if tabID == "" {
		sid, err = resolveTarget(c, rest[0])
		if err != nil {
			fmt.Fprintln(errOut, "gorex project move:", err)
			return codeFor(err), true
		}
		tabID = findTabOfSID(&st, sid)
	}

	var res rex.LayoutCommand
	if tabID != "" {
		res, err = c.Command(rex.LayoutCommand{Op: "project.move", Tab: tabID, ToSpace: toSpace})
	} else {
		// Rescue an unplaced session (e.g. created by raw c.Create) into a tab
		// in the destination project without restarting its shell.
		ad := layout.Adoption{
			Tabs: []layout.AdoptTab{{Split: &layout.Node{SID: sid}}},
		}
		if dst != nil {
			ad.Project = &layout.AdoptProject{Name: dst.Name, Dir: dst.Dir, Space: dst.Space}
		}
		res, err = c.Command(rex.LayoutCommand{Op: "project.adopt", Adopt: &ad})
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	if res.Code != "" {
		fmt.Fprintf(errOut, "gorex: %s: %s\n", res.Code, res.Error)
		return codeForResult(res), true
	}
	if *asJSON {
		outMap := map[string]any{
			"projectId": res.Space,
			"tabId":     res.Tab,
		}
		if dst != nil {
			outMap["project"] = dst.Name
		}
		if sid != "" {
			outMap["session"] = sid
		}
		WriteJSON(out, outMap)
	}
	return ExitOK, true
}

func findTabByID(st *layout.Store, candidate string) (tabID, firstSID string) {
	cand := strings.TrimPrefix(strings.TrimSpace(candidate), "window:")
	if cand == "" {
		return "", ""
	}
	for _, w := range st.Sessions {
		for _, t := range w.Tabs {
			if t.ID != "" && t.ID == cand {
				return t.ID, tabSession(t)
			}
		}
	}
	return "", ""
}

func findTabOfSID(st *layout.Store, sid string) string {
	if sid == "" {
		return ""
	}
	for _, w := range st.Sessions {
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, l := range layout.Leaves(r) {
					if l.Host == "" && l.SID == sid {
						return t.ID
					}
				}
			}
		}
	}
	return ""
}
