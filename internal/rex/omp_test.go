//go:build darwin || linux || windows

package rex

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const ompConv = "01a1aab0-a104-70a7-8f7b-bb9d63df27de"

// TestOmpDirs checks where omp keeps its data and state, as its dirs.ts
// says, by its environment and arguments.
func TestOmpDirs(t *testing.T) {
	home, xdg := resolved(t), resolved(t)
	os.MkdirAll(filepath.Join(xdg, "omp"), 0o755)
	agent := filepath.Join(home, ".omp", "agent")
	for _, c := range []struct {
		env         map[string]string
		args        []string
		data, state string
	}{
		{nil, nil, agent, agent},
		{map[string]string{"PI_CONFIG_DIR": ".pi"}, nil, filepath.Join(home, ".pi", "agent"), filepath.Join(home, ".pi", "agent")},
		{map[string]string{"OMP_PROFILE": "work"}, nil, filepath.Join(home, ".omp", "profiles", "work", "agent"), filepath.Join(home, ".omp", "profiles", "work", "agent")},
		{nil, []string{"omp", "--profile=work"}, filepath.Join(home, ".omp", "profiles", "work", "agent"), filepath.Join(home, ".omp", "profiles", "work", "agent")},
		{map[string]string{"OMP_PROFILE": "default"}, nil, agent, agent},
		{map[string]string{"PI_CODING_AGENT_DIR": "/srv/omp"}, nil, "/srv/omp", "/srv/omp"},
		{map[string]string{"XDG_DATA_HOME": xdg}, nil, filepath.Join(xdg, "omp"), agent},
		{map[string]string{"XDG_STATE_HOME": filepath.Join(home, "none")}, nil, agent, agent},
	} {
		data, state := ompDirs(func(k string) string { return c.env[k] }, c.args, home)
		if data != c.data || state != c.state {
			t.Errorf("%v %v: %s %s, want %s %s", c.env, c.args, data, state, c.data, c.state)
		}
	}
}

// TestOmpSlug checks omp's names of its conversations' directories:
// under the temporary directory, the home directory, or neither, of
// POSIX and Windows paths.
func TestOmpSlug(t *testing.T) {
	// A home that is not under the temporary directory, as the tests' are.
	home := filepath.Join(string(filepath.Separator)+"nonexistent", "home")
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	for dir, want := range map[string]string{
		filepath.Join(home, "repos", "gorex"): "-repos-gorex",
		home:                                  "-",
		filepath.Join(tmp, "a", "b"):          "-tmp-a-b",
		tmp:                                   "-tmp",
		"/opt/x/y":                            "--opt-x-y--",
	} {
		if got := ompSlug(dir, home); got != want {
			t.Errorf("ompSlug(%q) = %q, want %q", dir, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if got := ompSlug(`C:\Users\me\x`, home); got != "--C--Users-me-x--" {
			t.Errorf("a Windows path: %q", got)
		}
	}
}

// TestOmpID checks the conversation ids of omp's file names.
func TestOmpID(t *testing.T) {
	for name, want := range map[string]string{
		"2026-11-05T07-51-04-708Z_" + ompConv + ".jsonl": ompConv,
		ompConv + ".jsonl":            "",
		"x_" + ompConv + ".json":      "",
		"../x_" + ompConv + ".jsonl":  "",
		"2026_not-a-uuid.jsonl":       "",
		".2026_" + ompConv + ".jsonl": "",
	} {
		if got := ompID(name); got != want {
			t.Errorf("ompID(%q) = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]bool{
		strings.Repeat("a", 64): true, strings.Repeat("a", 64) + ".png": true, strings.Repeat("a", 63): false,
		strings.Repeat("a", 64) + ".a.b": false, "../" + strings.Repeat("a", 64): false,
	} {
		if got := blobName(name); got != want {
			t.Errorf("blobName(%q) = %v", name, got)
		}
	}
}

// TestOmpSession checks how omp's conversation of a process is found: by
// its terminal's record, else the one of its directory written last.
func TestOmpSession(t *testing.T) {
	home, dir := resolved(t), resolved(t)
	env := func(k string) string { return map[string]string{"HOME": home}[k] }
	agent := filepath.Join(home, ".omp", "agent")
	sessions := filepath.Join(agent, "sessions", ompSlug(dir, home))
	older := filepath.Join(sessions, "2026-11-05T07-00-00-000Z_01a1aab0-0000-70a7-8f7b-bb9d63df27de.jsonl")
	newer := filepath.Join(sessions, "2026-11-05T08-00-00-000Z_"+ompConv+".jsonl")
	writeFile(t, older, "{}\n")
	writeFile(t, newer, "{}\n")
	os.Chtimes(older, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if id, data, file := ompSession(env, dir, nil, "/dev/ttys042"); id != ompConv || data != agent || file != newer {
		t.Errorf("the newest: %q %q %q", id, data, file)
	}
	writeFile(t, filepath.Join(agent, "terminal-sessions", "ttys042"), dir+"\n"+older+"\ncwdstat 1 2\n")
	if id, _, file := ompSession(env, dir, nil, "/dev/ttys042"); file != older || id != "01a1aab0-0000-70a7-8f7b-bb9d63df27de" {
		t.Errorf("the terminal's: %q %q", id, file)
	}
	// Another directory's record is not this one's.
	if _, _, file := ompSession(env, dir, nil, "/dev/ttys043"); file != newer {
		t.Errorf("without a record: %q", file)
	}
	if id, _, _ := ompSession(env, resolved(t), nil, ""); id != "" {
		t.Errorf("a directory with none: %q", id)
	}
}

// ompIn starts omp's stand-in, a script in a bin directory, in a shell of
// c's server in dir, and returns the session.
func ompIn(t *testing.T, c *Client, dir string) (string, SessionInfo) {
	t.Helper()
	cmd, _ := testShell()
	in, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "bin", "omp")
	writeFile(t, fake, "#!/bin/sh\nsleep 60\n")
	os.Chmod(fake, 0o755)
	c.Input(in.ID, []byte("'"+fake+"' 60\n"))
	o := waitInfo(t, c, in.ID, "omp in the foreground", func(in SessionInfo) bool { return in.Program == "omp" })
	t.Cleanup(func() {
		c.Kill(in.ID)
		if p, err := os.FindProcess(o.Foreground); err == nil {
			p.Kill()
		}
	})
	return in.ID, o
}

// ompLines are lines of omp's conversation: its session, and messages by
// id, each after the one before, the first quoting a blob.
func ompLines(cwd, blob string, ids ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{\"type\":\"title\",\"v\":1,\"title\":\"x\"}\n{\"type\":\"session\",\"version\":3,\"id\":%q,\"timestamp\":\"t\",\"cwd\":%s}\n", ompConv, jsonString(cwd))
	parent := "null"
	for i, id := range ids {
		ref := ""
		if i == 0 {
			ref = "blob:sha256:" + blob
		}
		fmt.Fprintf(&b, "{\"type\":\"message\",\"id\":%q,\"parentId\":%s,\"message\":{\"content\":%q}}\n", id, parent, ref)
		parent = fmt.Sprintf("%q", id)
	}
	return b.String()
}

// TestTeleportOmpRoundTrip teleports omp's conversation from A to B, by
// its terminal's record, where it goes on, and back, found as the newest
// of its directory: its file, artifacts and blobs go along, its cwd moves,
// and each side keeps one copy, in its directory's; one gone on at both
// is refused.
func TestTeleportOmpRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for omp is a POSIX program")
	}
	for _, k := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "PI_CONFIG_DIR", "PI_CODING_AGENT_DIR", "OMP_PROFILE", "PI_PROFILE"} {
		t.Setenv(k, "")
	}
	_, work := teleportRepo(t)
	m := newTwoMachines(t, work)
	agentA, agentB := filepath.Join(m.homeA, ".omp", "agent"), filepath.Join(m.homeB, ".omp", "agent")
	blob := strings.Repeat("ab", 32)
	stem := "2026-11-05T07-51-04-708Z_" + ompConv
	copies := func(agent string) []string {
		found, _ := filepath.Glob(filepath.Join(agent, "sessions", "*", "*_"+ompConv+".jsonl"))
		return found
	}

	// A to B, the session found by its terminal's record.
	m.toB(t)
	dirP := filepath.Join(work, "sub")
	sidA, _ := ompIn(t, m.aLocal, dirP)
	fileA := filepath.Join(agentA, "sessions", ompSlug(dirP, m.homeA), stem+".jsonl")
	writeFile(t, fileA, ompLines(dirP, blob, "m1", "m2"))
	writeFile(t, filepath.Join(strings.TrimSuffix(fileA, ".jsonl"), "1.bash.log"), "log")
	writeFile(t, filepath.Join(agentA, "blobs", blob+".png"), "png")
	tty := m.a.sessions[sidA].p.tty()
	writeFile(t, filepath.Join(agentA, "terminal-sessions", strings.ReplaceAll(strings.TrimPrefix(tty, "/dev/"), "/", "-")), dirP+"\n"+fileA+"\n")
	waitInfo(t, m.aLocal, sidA, "omp quiet", func(in SessionInfo) bool { return in.AgentSession == ompConv && TeleportRefusal(in) == "" })
	in, err := Teleport(context.Background(), m.aLocal, sidA, m.bPeer, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	dirW := waitInfo(t, m.bPeer, in.ID, "B's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir
	fileB := filepath.Join(agentB, "sessions", ompSlug(dirW, m.homeB), stem+".jsonl")
	if got := copies(agentB); len(got) != 1 || got[0] != fileB {
		t.Fatalf("B's copies %v, want %s", got, fileB)
	}
	if b, _ := os.ReadFile(fileB); !strings.Contains(string(b), `"cwd":`+jsonString(dirW)) {
		t.Errorf("B's conversation %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(strings.TrimSuffix(fileB, ".jsonl"), "1.bash.log")); string(b) != "log" {
		t.Error("the artifacts did not come")
	}
	if b, _ := os.ReadFile(filepath.Join(agentB, "blobs", blob+".png")); string(b) != "png" {
		t.Error("the blob did not come")
	}
	waitText(t, m.bPeer, in.ID, "stub-omp --resume "+ompConv)

	// The source omp, killed once its pane went, notes it in its copy:
	// no turn of the conversation.
	f, _ := os.OpenFile(fileA, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprintln(f, `{"type":"custom","customType":"session_exit","data":{"reason":"sighup"},"id":"cae291a2","parentId":"m2"}`)
	f.Close()

	// It goes on at B; back to A, found as the newest of its directory.
	f, _ = os.OpenFile(fileB, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprintln(f, `{"type":"message","id":"b1","parentId":"m2","message":{}}`)
	f.Close()
	m.toA(t)
	sidB, _ := ompIn(t, m.bLocal, dirW)
	waitInfo(t, m.bLocal, sidB, "omp quiet", func(in SessionInfo) bool { return in.AgentSession == ompConv && TeleportRefusal(in) == "" })
	in, err = Teleport(context.Background(), m.bLocal, sidB, m.aPeer, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitInfo(t, m.aPeer, in.ID, "A's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir; got != dirP {
		t.Errorf("A's pane is in %s, want %s", got, dirP)
	}
	if got := copies(agentA); len(got) != 1 || got[0] != fileA {
		t.Fatalf("A's copies %v", got)
	}
	if b, _ := os.ReadFile(fileA); !strings.Contains(string(b), `"b1"`) || !strings.Contains(string(b), `"cwd":`+jsonString(dirP)) {
		t.Errorf("A's conversation %s", b)
	}
	if got := tree(t, filepath.Join(agentA, "blobs")); !maps.Equal(got, map[string]string{blob + ".png": "png"}) {
		t.Errorf("A's blobs %v", got)
	}

	// Gone on at A too: B's is refused.
	f, _ = os.OpenFile(fileA, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprintln(f, `{"type":"message","id":"a1","parentId":"b1","message":{}}`)
	f.Close()
	before := tree(t, agentA)
	if _, err := Teleport(context.Background(), m.bLocal, sidB, m.aPeer, func(string, string) {}); err == nil || !strings.Contains(err.Error(), "went on here too") {
		t.Errorf("gone on at both: %v", err)
	}
	if !maps.Equal(tree(t, agentA), before) {
		t.Error("A changed")
	}
}
