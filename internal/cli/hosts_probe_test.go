package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gorex/internal/hosts"
	"gorex/internal/layout"
	"gorex/internal/rex"
)

func TestCollectProbeCandidates(t *testing.T) {
	saved := []hosts.Host{
		{ID: "h1", Name: "build-linux", Alias: "Builder", Address: "secret-addr-1"},
		{ID: "h2", Name: "win-desk", Address: "tailnet:scope:node2"},
	}
	tailnet := []rex.TailnetHost{
		// Same target as h2: deduplicated.
		{Name: "win-desk-renamed", Target: "tailnet:scope:node2"},
		// Same alias as h1 (case-insensitive): deduplicated.
		{Name: "builder", Target: "tailnet:scope:node1"},
		// Unique tailnet peer: kept.
		{Name: "mac-mini", Target: "tailnet:scope:node3"},
		// Duplicate tailnet entry: deduplicated.
		{Name: "mac-mini", Target: "tailnet:scope:node3"},
		// Empty target: ignored.
		{Name: "empty-target", Target: ""},
	}
	got := collectProbeCandidates(saved, tailnet)
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(got), got)
	}
	if got[0].name != "build-linux" || got[0].source != "saved" || got[0].alias != "Builder" {
		t.Fatalf("candidate[0] = %+v", got[0])
	}
	if got[1].name != "win-desk" || got[1].source != "saved" {
		t.Fatalf("candidate[1] = %+v", got[1])
	}
	if got[2].name != "mac-mini" || got[2].source != "tailnet" || got[2].address != "tailnet:scope:node3" {
		t.Fatalf("candidate[2] = %+v", got[2])
	}
}

func TestHostsProbe(t *testing.T) {
	c := startServer(t)
	dir := rex.Dir()

	// Seed a project in the test server's layout so probing reports it.
	projDir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := layout.Store{
		Version: layout.Version,
		Sessions: []layout.Workspace{
			{ID: "p1", Name: "app", Dir: projDir},
			{ID: "np", Name: layout.NoProjectName},
		},
	}
	rawLayout, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetLayout(rawLayout); err != nil {
		t.Fatal(err)
	}

	// Install a fake claude binary on PATH so agents.list returns it.
	binDir := t.TempDir()
	fakeClaude := filepath.Join(binDir, "claude")
	if runtime.GOOS == "windows" {
		fakeClaude += ".cmd"
	}
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	rex.ResetAgentsCacheForTest()
	t.Cleanup(rex.ResetAgentsCacheForTest)

	savedBody := `{"hosts":[` +
		`{"id":"h1","name":"build-linux","alias":"builder","address":"secret-online-addr"},` +
		`{"id":"h2","name":"offline-box","address":"secret-offline-addr"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte(savedBody), 0o600); err != nil {
		t.Fatal(err)
	}

	origDial := probeDialRemote
	origTailnet := probeTailnetHosts
	t.Cleanup(func() {
		probeDialRemote = origDial
		probeTailnetHosts = origTailnet
	})
	probeTailnetHosts = func() []rex.TailnetHost {
		return []rex.TailnetHost{
			{Name: "builder", Target: "tailnet:scope:dup"},
			{Name: "mac-mini", Target: "tailnet:scope:mini"},
		}
	}
	probeDialRemote = func(_ context.Context, target string) (*rex.Client, error) {
		switch target {
		case "secret-online-addr", "tailnet:scope:mini":
			return rex.Connect()
		case "secret-offline-addr":
			return nil, errors.New("dial secret-offline-addr: connection refused")
		default:
			return nil, errors.New("unexpected target: " + target)
		}
	}

	code, handled, out, errOut := runMain(t, "hosts", "--probe", "--json")
	if code != ExitOK || !handled {
		t.Fatalf("hosts --probe --json: %d %v %s", code, handled, errOut)
	}
	if strings.Contains(out, "secret-online-addr") || strings.Contains(out, "secret-offline-addr") {
		t.Fatalf("hosts --probe leaked secret address: %q", out)
	}
	var got []probedHost
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d hosts, want 3: %+v", len(got), got)
	}

	// h1: online saved host with claude and project "app".
	if !got[0].Online || got[0].Name != "build-linux" || got[0].Alias != "builder" || got[0].Source != "saved" || got[0].OS == "" {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if len(got[0].Projects) != 1 || got[0].Projects[0].Name != "app" || got[0].Projects[0].Dir != projDir {
		t.Fatalf("got[0].Projects = %+v, want [app:%s]", got[0].Projects, projDir)
	}
	if !containsString(got[0].Agents, "claude") {
		t.Fatalf("got[0].Agents = %+v, want claude", got[0].Agents)
	}

	// h2: offline saved host with scrubbed error.
	if got[1].Online || got[1].Name != "offline-box" || !strings.Contains(got[1].Error, "<address>") {
		t.Fatalf("got[1] = %+v", got[1])
	}
	if scrubbed := sanitizeProbeError("dial nodekey:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef failed", ""); strings.Contains(scrubbed, "0123456789abcdef") {
		t.Fatalf("sanitizeProbeError leaked key: %q", scrubbed)
	}

	// Tailnet host: online, deduplicated against "builder".
	if !got[2].Online || got[2].Name != "mac-mini" || got[2].Source != "tailnet" {
		t.Fatalf("got[2] = %+v", got[2])
	}

	// Table output sanity check.
	code, _, tableOut, errOut := runMain(t, "hosts", "--probe")
	if code != ExitOK {
		t.Fatalf("hosts --probe table: %d %s", code, errOut)
	}
	if !strings.Contains(tableOut, "STATUS") || !strings.Contains(tableOut, "online") || !strings.Contains(tableOut, "offline") {
		t.Fatalf("unexpected table output:\n%s", tableOut)
	}
	if strings.Contains(tableOut, "secret-online-addr") || strings.Contains(tableOut, "secret-offline-addr") {
		t.Fatalf("table leaked secret address:\n%s", tableOut)
	}

	// Invalid timeout returns ExitUsage.
	if code, _, _, _ := runMain(t, "hosts", "--probe", "--timeout", "0s"); code != ExitUsage {
		t.Fatalf("timeout 0: got %d, want %d", code, ExitUsage)
	}
}

func TestAgentsCmd(t *testing.T) {
	_ = startServer(t)

	binDir := t.TempDir()
	fakeClaude := filepath.Join(binDir, "claude")
	if runtime.GOOS == "windows" {
		fakeClaude += ".cmd"
	}
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	rex.ResetAgentsCacheForTest()
	t.Cleanup(rex.ResetAgentsCacheForTest)

	code, handled, out, errOut := runMain(t, "agents", "--json")
	if code != ExitOK || !handled {
		t.Fatalf("agents --json: %d %v %s", code, handled, errOut)
	}
	var got []rex.AgentInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	found := false
	for _, a := range got {
		if a.ID == "claude" && a.Name == "Claude Code" && len(a.Command) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("claude not found in agents JSON: %+v", got)
	}

	code, _, tableOut, errOut := runMain(t, "agents")
	if code != ExitOK {
		t.Fatalf("agents table: %d %s", code, errOut)
	}
	if !strings.Contains(tableOut, "ID") || !strings.Contains(tableOut, "claude") {
		t.Fatalf("unexpected agents table:\n%s", tableOut)
	}
}

func TestSkillGuidePeerHosts(t *testing.T) {
	code, handled, out, errOut := runMain(t, "skill", "show")
	if code != ExitOK || !handled {
		t.Fatalf("skill show: %d %v %s", code, handled, errOut)
	}
	for _, want := range []string{"gorex hosts --probe --json", "gorex -S build-linux agents --json", "## Peer hosts", "gorex teleport"} {
		if !strings.Contains(out, want) {
			t.Errorf("skill show missing %q", want)
		}
	}
}
