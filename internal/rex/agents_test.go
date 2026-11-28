//go:build darwin || linux

package rex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDetectAgentsInDirs(t *testing.T) {
	dir := t.TempDir()
	writeBin := func(name string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	writeBin("claude", 0o755)
	writeBin("codex", 0o755)
	writeBin("agy", 0o644) // not executable, must be skipped
	if err := os.Mkdir(filepath.Join(dir, "omp"), 0o755); err != nil {
		t.Fatal(err) // directory, must be skipped
	}

	got := detectAgentsInDirs([]string{dir}, "/bin/zsh")
	if len(got) != 2 {
		t.Fatalf("got %d agents (%+v), want 2", len(got), got)
	}
	if got[0].ID != "claude" || got[0].Name != "Claude Code" {
		t.Errorf("agent 0 = %+v, want claude", got[0])
	}
	qClaude, _ := PathWord("/bin/zsh", filepath.Join(dir, "claude"))
	wantClaude := []string{"/bin/zsh", "-lic", "exec " + qClaude}
	if !slices.Equal(got[0].Command, wantClaude) {
		t.Errorf("claude command = %v, want %v", got[0].Command, wantClaude)
	}
	if got[1].ID != "codex" || got[1].Name != "Codex" {
		t.Errorf("agent 1 = %+v, want codex", got[1])
	}

	// Unknown shell falls back to the resolved binary path.
	gotOther := detectAgentsInDirs([]string{dir}, "/usr/bin/customsh")
	if len(gotOther) != 2 {
		t.Fatalf("got %d agents for custom shell, want 2", len(gotOther))
	}
	wantPath := []string{filepath.Join(dir, "claude")}
	if !slices.Equal(gotOther[0].Command, wantPath) {
		t.Errorf("custom shell command = %v, want %v", gotOther[0].Command, wantPath)
	}

	// POSIX and fish each quote special characters in resolved paths with their own rules.
	specialBin := "/opt/alice's tools/bin\\claude"
	gotPosix := agentCommand("/bin/zsh", "claude", specialBin)
	wantPosix := []string{"/bin/zsh", "-lic", `exec '/opt/alice'\''s tools/bin\claude'`}
	if !slices.Equal(gotPosix, wantPosix) {
		t.Errorf("posix command = %q, want %q", gotPosix, wantPosix)
	}
	gotFish := agentCommandWithArgs("/opt/homebrew/bin/fish", "claude", specialBin, []string{"-p", "hi"})
	wantFish := []string{"/opt/homebrew/bin/fish", "-lic", `exec '/opt/alice\'s tools/bin\\claude' $argv`, "--", "-p", "hi"}
	if !slices.Equal(gotFish, wantFish) {
		t.Errorf("fish command = %q, want %q", gotFish, wantFish)
	}
}

func TestAgentsListRPC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prevProbe := probeShellPathFunc
	probeShellPathFunc = func(string) string { return dir }
	t.Cleanup(func() {
		probeShellPathFunc = prevProbe
		ResetAgentsCacheForTest()
	})
	ResetAgentsCacheForTest()

	s, _ := testServer(t)
	c := pipe(s, "")
	t.Cleanup(func() { c.Close() })
	h, err := c.Hello()
	if err != nil || !h.Can(CapabilityAgents) {
		t.Fatalf("hello %v %v: missing %s", h.Capabilities, err, CapabilityAgents)
	}
	res, err := c.AgentsList()
	if err != nil {
		t.Fatalf("AgentsList: %v", err)
	}
	if !slices.ContainsFunc(res.Agents, func(a AgentInfo) bool { return a.ID == "claude" }) {
		t.Fatalf("AgentsList = %+v, want claude present", res.Agents)
	}
}
