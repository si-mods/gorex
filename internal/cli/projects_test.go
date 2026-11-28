package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// openDesktop simulates an open GoRex desktop window taking commands from the
// server (s.desks > 0), which is the runtime state where raw c.Create leaves
// local sessions unplaced in the layout.
func openDesktop(t *testing.T) {
	t.Helper()
	app, err := rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	go func() {
		for {
			if _, err := app.TakeCommand(); err != nil {
				return
			}
		}
	}()
	// Give the server a moment to register the command.take reader (s.desks > 0).
	time.Sleep(50 * time.Millisecond)
}

func seedProjects(t *testing.T, c *rex.Client, names ...string) map[string]string {
	t.Helper()
	ids := make(map[string]string, len(names))
	for _, name := range names {
		dir := t.TempDir()
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		res, err := c.Command(rex.LayoutCommand{Op: "project.create", Name: name, Dir: dir, Command: []string{"sh"}})
		if err != nil || res.Code != "" {
			t.Fatalf("project.create %s: %v %+v", name, err, res)
		}
		ids[name] = res.Space
	}
	return ids
}

func projectHasSID(st *layout.Store, projectID, sid string) bool {
	w := st.Space(projectID)
	if w == nil {
		return false
	}
	return workspaceHasSID(w, sid)
}

// TestNewPlacesInActiveProjectAndCloses reproduces issue #1276: with multiple
// projects and an open GoRex desktop window, `gorex new --dir DIR` must place
// the session in the active project (never an unplaced orphan), expose its
// project in `gorex ls --json`, allow `gorex close -s` to close it cleanly,
// and prune/forget sessions killed externally instead of leaving stale zombies.
func TestNewPlacesInActiveProjectAndCloses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and SIGKILL")
	}
	c := startServer(t)
	openDesktop(t)

	ids := seedProjects(t, c, "alpha", "beta")
	if res, err := c.Command(rex.LayoutCommand{Op: "windows.set", Spaces: []string{ids["beta"]}}); err != nil || res.Code != "" {
		t.Fatalf("windows.set: %v %+v", err, res)
	}

	otherDir := t.TempDir()
	code, _, out, errOut := runMain(t, "new", "--json", "--dir", otherDir, "--", "sh")
	if code != ExitOK {
		t.Fatalf("new: %d %s", code, errOut)
	}
	var created sessionJSON
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("new JSON %q: %v", out, err)
	}

	raw, err := c.Layout()
	if err != nil {
		t.Fatal(err)
	}
	st := layout.Parse(raw)
	if !projectHasSID(&st, ids["beta"], created.ID) {
		t.Fatalf("session %s not placed in active project beta; layout=%s", created.ID, raw)
	}
	if created.ProjectID != ids["beta"] || created.Project != "beta" || created.TabID == "" {
		t.Fatalf("new --json project metadata = %+v, want project beta (%s)", created, ids["beta"])
	}

	// `gorex ls --json` exposes the project of the session.
	code, _, out, errOut = runMain(t, "ls", "--json")
	if code != ExitOK {
		t.Fatalf("ls: %d %s", code, errOut)
	}
	if !strings.Contains(out, `"projectId": "`+ids["beta"]+`"`) || !strings.Contains(out, `"project": "beta"`) {
		t.Fatalf("ls --json missing project metadata for %s: %s", created.ID, out)
	}

	// `gorex close -s` closes the session created by `gorex new` and removes it from layout and ls.
	code, _, _, errOut = runMain(t, "close", "-s", created.ID)
	if code != ExitOK {
		t.Fatalf("close -s %s: code=%d err=%s", created.ID, code, errOut)
	}
	raw, _ = c.Layout()
	if strings.Contains(string(raw), created.ID) {
		t.Fatalf("closed session %s still in layout: %s", created.ID, raw)
	}
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == created.ID {
			t.Fatalf("closed session %s still listed in ls: %+v", created.ID, in)
		}
	}

	// Killing a `gorex new` session's PID externally prunes its tab immediately
	// and removes it from `gorex ls` after refreshWait (no stale zombie).
	code, _, out, errOut = runMain(t, "new", "--json", "--", "sleep", "60")
	if code != ExitOK {
		t.Fatalf("new sleep: %d %s", code, errOut)
	}
	var killed sessionJSON
	if err := json.Unmarshal([]byte(out), &killed); err != nil || killed.PID <= 0 {
		t.Fatalf("new sleep JSON %q: %v", out, err)
	}
	proc, err := os.FindProcess(killed.PID)
	if err != nil {
		t.Fatalf("find process %d: %v", killed.PID, err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill %d: %v", killed.PID, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		infos, err := c.List()
		if err != nil {
			t.Fatal(err)
		}
		stillListed := false
		for _, in := range infos {
			if in.ID == killed.ID {
				stillListed = true
				break
			}
		}
		raw, _ := c.Layout()
		if !stillListed && !strings.Contains(string(raw), killed.ID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("killed session %s remained stale: listed=%v layout=%s", killed.ID, stillListed, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestNewProjectSelectionAndErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	c := startServer(t)
	openDesktop(t)

	// 1. Server has no projects at all -> clear error, zero sessions created.
	code, _, _, errOut := runMain(t, "new", "--dir", t.TempDir(), "--", "sh")
	if code != ExitTarget || !strings.Contains(errOut, "no project") {
		t.Fatalf("new with no project: code=%d err=%q", code, errOut)
	}
	if infos, _ := c.List(); len(infos) != 0 {
		t.Fatalf("new with no project leaked sessions: %+v", infos)
	}

	// 2. Server only has "Tabs with no project" (w.Dir == "") -> still fails clearly.
	noProjLayout := layout.Store{
		Version:  layout.Version,
		Sessions: []layout.Workspace{{ID: "np1", Name: layout.NoProjectName, Layout: layout.Layout{Tabs: []layout.Tab{}}}},
		Windows:  []string{"np1"},
	}
	b, _ := json.Marshal(noProjLayout)
	if err := c.SetLayout(b); err != nil {
		t.Fatal(err)
	}
	code, _, _, errOut = runMain(t, "new", "--", "sh")
	if code != ExitTarget || !strings.Contains(errOut, "no project") {
		t.Fatalf("new with only NoProject workspace: code=%d err=%q", code, errOut)
	}

	// 3. Seed projects alpha and beta; make beta the active window.
	ids := seedProjects(t, c, "alpha", "beta")
	if res, err := c.Command(rex.LayoutCommand{Op: "windows.set", Spaces: []string{ids["beta"]}}); err != nil || res.Code != "" {
		t.Fatalf("windows.set: %v %+v", err, res)
	}

	// 4. Caller running inside an alpha pane (GOREX_SESSION) defaults to alpha,
	// even while beta is shown in the window.
	raw, _ := c.Layout()
	st := layout.Parse(raw)
	alphaPaneSID := st.Space(ids["alpha"]).Tabs[0].Root.SID
	t.Setenv("GOREX_SESSION", alphaPaneSID)
	code, _, out, errOut := runMain(t, "new", "--", "sh")
	if code != ExitOK {
		t.Fatalf("new from GOREX_SESSION: %d %s", code, errOut)
	}
	sidFromEnv := strings.TrimSpace(out)
	raw, _ = c.Layout()
	st = layout.Parse(raw)
	if !projectHasSID(&st, ids["alpha"], sidFromEnv) {
		t.Fatalf("expected session %s in caller's project alpha, layout=%s", sidFromEnv, raw)
	}
	t.Setenv("GOREX_SESSION", "")

	// 5. Explicit --project by name and by ID overrides active project beta.
	code, _, out, errOut = runMain(t, "new", "--project", "alpha", "--name", "custom-tab", "--", "sh")
	if code != ExitOK {
		t.Fatalf("new --project alpha: %d %s", code, errOut)
	}
	sidExplicitName := strings.TrimSpace(out)
	code, _, out, errOut = runMain(t, "new", "--project", ids["alpha"], "--", "sh")
	if code != ExitOK {
		t.Fatalf("new --project <id>: %d %s", code, errOut)
	}
	sidExplicitID := strings.TrimSpace(out)
	raw, _ = c.Layout()
	st = layout.Parse(raw)
	if !projectHasSID(&st, ids["alpha"], sidExplicitName) || !projectHasSID(&st, ids["alpha"], sidExplicitID) {
		t.Fatalf("explicit --project sessions not in alpha: %s", raw)
	}

	// 6. Unknown or empty --project fails without creating any session.
	beforeInfos, _ := c.List()
	if code, _, _, errOut := runMain(t, "new", "--project", "nonexistent", "--", "sh"); code != ExitTarget || !strings.Contains(errOut, "no such project") {
		t.Fatalf("new --project nonexistent: code=%d err=%q", code, errOut)
	}
	if code, _, _, _ := runMain(t, "new", "--project", "", "--", "sh"); code != ExitUsage {
		t.Fatalf("new --project empty: code=%d, want %d", code, ExitUsage)
	}
	if afterInfos, _ := c.List(); len(afterInfos) != len(beforeInfos) {
		t.Fatalf("failed --project leaked a session: before=%d after=%d", len(beforeInfos), len(afterInfos))
	}

	// 7. Ambiguous project name fails with ExitTarget.
	if res, err := c.Command(rex.LayoutCommand{Op: "project.update", Space: ids["beta"], Name: "alpha"}); err != nil || res.Code != "" {
		t.Fatalf("project.update: %v %+v", err, res)
	}
	if code, _, _, errOut := runMain(t, "new", "--project", "alpha", "--", "sh"); code != ExitTarget || !strings.Contains(errOut, "ambiguous") {
		t.Fatalf("new --project ambiguous: code=%d err=%q", code, errOut)
	}
}

func TestCloseOrphanSessionFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	c := startServer(t)
	openDesktop(t)

	// Simulate a pre-existing unplaced session created via raw c.Create.
	orphan, err := c.Create(rex.CreateOptions{Command: []string{"sh"}, Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	code, _, _, errOut := runMain(t, "close", "-s", orphan.ID)
	if code != ExitOK {
		t.Fatalf("close orphan %s: code=%d err=%s", orphan.ID, code, errOut)
	}
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == orphan.ID {
			t.Fatalf("orphan %s still listed after close", orphan.ID)
		}
	}
}

func TestProjectCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	c := startServer(t)
	openDesktop(t)

	dirA := t.TempDir()
	dirB := t.TempDir()
	if real, err := filepath.EvalSymlinks(dirA); err == nil {
		dirA = real
	}
	if real, err := filepath.EvalSymlinks(dirB); err == nil {
		dirB = real
	}

	// 1. `gorex project new` with explicit name and --dir (flags after positional arg).
	code, _, out, errOut := runMain(t, "project", "new", "service-a", "--dir", dirA, "--space", "Work", "--json")
	if code != ExitOK {
		t.Fatalf("project new service-a: %d %s", code, errOut)
	}
	var createdA struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Dir     string `json:"dir"`
		Space   string `json:"space"`
		TabID   string `json:"tabId"`
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &createdA); err != nil {
		t.Fatalf("project new JSON %q: %v", out, err)
	}
	if createdA.ID == "" || createdA.Name != "service-a" || createdA.Dir != dirA || createdA.Space != "Work" || createdA.Session == "" {
		t.Fatalf("unexpected createdA: %+v", createdA)
	}

	// Duplicate folder returns exists error.
	if code, _, _, errOut := runMain(t, "project", "new", "dup", "--dir", dirA); code != ExitFailed || !strings.Contains(errOut, "exists") {
		t.Fatalf("duplicate project new: code=%d err=%q", code, errOut)
	}

	// 2. `gorex project new --dir <dir>` defaults name to folder basename.
	code, _, out, errOut = runMain(t, "project", "new", "--dir", dirB)
	if code != ExitOK {
		t.Fatalf("project new dirB: %d %s", code, errOut)
	}
	projBID := strings.TrimSpace(out)
	if projBID == "" {
		t.Fatal("project new returned empty ID")
	}

	// 3. `gorex project rename` changes only the project's name, keeping tabs/shells intact.
	infosBefore, _ := c.List()
	pidsBefore := map[string]int{}
	for _, in := range infosBefore {
		pidsBefore[in.ID] = in.PID
	}
	code, _, out, errOut = runMain(t, "project", "rename", projBID, "Service B", "--json")
	if code != ExitOK || !strings.Contains(out, `"name": "Service B"`) {
		t.Fatalf("project rename: %d %q %s", code, out, errOut)
	}

	// 4. `gorex projects --json` and plain `gorex projects` list id/name/tabs/panes.
	code, _, out, errOut = runMain(t, "projects", "--json")
	if code != ExitOK {
		t.Fatalf("projects --json: %d %s", code, errOut)
	}
	var listed []projectJSON
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("projects --json %q: %v", out, err)
	}
	if listed[0].ID != createdA.ID || listed[0].Name != "service-a" || len(listed[0].Tabs) != 1 || len(listed[0].Tabs[0].Panes) != 1 || listed[0].Tabs[0].Panes[0].SID != createdA.Session {
		t.Fatalf("listed[0] = %+v", listed[0])
	}
	if listed[1].ID != projBID || listed[1].Name != "Service B" || len(listed[1].Tabs) != 1 || len(listed[1].Tabs[0].Panes) != 1 {
		t.Fatalf("listed[1] = %+v", listed[1])
	}

	code, _, tableOut, errOut := runMain(t, "projects")
	if code != ExitOK || !strings.Contains(tableOut, "service-a") || !strings.Contains(tableOut, "Service B") || !strings.Contains(tableOut, createdA.Session) {
		t.Fatalf("projects table: %d %q %s", code, tableOut, errOut)
	}

	// 5. `gorex project move <session> <project>` moves the tab & live process across projects.
	code, _, out, errOut = runMain(t, "project", "move", createdA.Session, "Service B", "--json")
	if code != ExitOK || !strings.Contains(out, `"project": "Service B"`) {
		t.Fatalf("project move: %d %q %s", code, out, errOut)
	}
	raw, _ := c.Layout()
	st := layout.Parse(raw)
	if projectHasSID(&st, createdA.ID, createdA.Session) || !projectHasSID(&st, projBID, createdA.Session) {
		t.Fatalf("session %s did not move from service-a to Service B: %s", createdA.Session, raw)
	}

	// 6. `gorex project move` also rescues an unplaced session into a project.
	orphan, err := c.Create(rex.CreateOptions{Command: []string{"sh"}, Dir: dirA, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	pidsBefore[orphan.ID] = orphan.PID
	code, _, _, errOut = runMain(t, "project", "move", orphan.ID, "service-a")
	if code != ExitOK {
		t.Fatalf("project move orphan: %d %s", code, errOut)
	}
	raw, _ = c.Layout()
	st = layout.Parse(raw)
	if !projectHasSID(&st, createdA.ID, orphan.ID) {
		t.Fatalf("orphan %s was not adopted into service-a: %s", orphan.ID, raw)
	}

	// Shell PIDs did not restart across rename and moves.
	infosAfter, _ := c.List()
	for _, in := range infosAfter {
		if wantPID, ok := pidsBefore[in.ID]; ok && in.PID != wantPID {
			t.Fatalf("session %s changed PID from %d to %d", in.ID, wantPID, in.PID)
		}
	}
}
