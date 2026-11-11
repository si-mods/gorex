package rex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParseClaudeSession(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	dir, other := filepath.Join(os.TempDir(), "proj"), filepath.Join(os.TempDir(), "other")
	file := func(pid int, sid, cwd string) string {
		b, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": sid, "cwd": cwd})
		return string(b)
	}
	for _, tt := range []struct {
		label, file, dir string
		want             string
	}{
		{"own", file(42, id, dir), dir, id},
		{"cwd unknown", file(42, id, ""), "", id},
		{"trailing separator", file(42, id, dir+string(filepath.Separator)), dir, id},
		{"a crashed process's, in another dir", file(42, id, other), dir, ""},
		{"no cwd", file(42, id, ""), dir, ""},
		{"another process's", file(7, id, dir), dir, ""},
		{"no id", `{"pid":42}`, "", ""},
		{"not an id", file(42, "x; rm -rf ~", ""), "", ""},
		{"an option", file(42, "--add", ""), "", ""},
		{"dashes", file(42, "-----", ""), "", ""},
		{"not json", `{`, "", ""},
	} {
		if got := parseClaudeSession([]byte(tt.file), 42, tt.dir); got != tt.want {
			t.Errorf("%s: parseClaudeSession = %q, want %q", tt.label, got, tt.want)
		}
	}
	if runtime.GOOS == "windows" && !samePath(`C:\Repos\X\`, `c:\repos\x`) {
		t.Error("Windows paths differ by case")
	}
}

func TestIsUUID(t *testing.T) {
	for s, want := range map[string]bool{
		"5d454f1b-0c2e-4b8a-9f00-0123456789ab": true,
		"5D454F1B-0C2E-4B8A-9F00-0123456789AB": true,
		"5d454f1b0c2e-4b8a-9f00-0123456789ab-": false,
		"5d454f1b-0c2e-4b8a-9f00-0123456789a":  false,
		"5d454f1b-0c2e-4b8a-9f00-0123456789ag": false,
		"--add":                                false,
		"":                                     false,
	} {
		if IsUUID(s) != want {
			t.Errorf("IsUUID(%q) = %v", s, !want)
		}
	}
}

// TestClaudeSession reads the config dir of a process of its own, which
// the test binary is, and the conversation its file there names.
func TestClaudeSession(t *testing.T) {
	if os.Getenv("GOREX_TEST_SLEEP") != "" {
		time.Sleep(30 * time.Second)
		return
	}
	config := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestClaudeSession$")
	cmd.Env = append(os.Environ(), "GOREX_TEST_SLEEP=1", "CLAUDE_CONFIG_DIR="+config)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()
	pid := cmd.Process.Pid
	if got := procEnv(pid, "CLAUDE_CONFIG_DIR"); got != config {
		t.Fatalf("procEnv = %q, want %q", got, config)
	}
	dir := inspect(pid).dir
	if id, cfg := claudeSession(pid, dir); id != "" || cfg != "" {
		t.Errorf("without a file: claudeSession = %q, %q", id, cfg)
	}
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	os.MkdirAll(filepath.Join(config, "sessions"), 0o755)
	file := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q}`, pid, id, dir)
	if err := os.WriteFile(filepath.Join(config, "sessions", fmt.Sprint(pid)+".json"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if gotID, gotCfg := claudeSession(pid, dir); gotID != id || gotCfg != config {
		t.Errorf("claudeSession = %q, %q; want %q, %q", gotID, gotCfg, id, config)
	}
	if gotID, _ := claudeSession(pid, t.TempDir()); gotID != "" {
		t.Errorf("in another dir: claudeSession = %q", gotID)
	}
}

// TestEnvReader checks that a lookup reads the environment at its first
// call only, and not at all when none is made.
func TestEnvReader(t *testing.T) {
	reads := 0
	get := envReader(func() []string {
		reads++
		return []string{"HOME=/home/x", "PI_PROFILE=work"}
	})
	if reads != 0 {
		t.Fatalf("read before a lookup: %d", reads)
	}
	if get("PI_PROFILE") != "work" || get("HOME") != "/home/x" || get("OMP_PROFILE") != "" {
		t.Error("wrong values")
	}
	if reads != 1 {
		t.Errorf("reads = %d, want 1", reads)
	}
	// A whole look at an omp process takes one read.
	reads = 0
	get = envReader(func() []string {
		reads++
		return []string{"HOME=" + t.TempDir(), "PI_PROFILE=work", "XDG_DATA_HOME=/x"}
	})
	ompSession(get, t.TempDir(), nil, "/dev/ttys042")
	if reads != 1 {
		t.Errorf("reads for ompSession = %d, want 1", reads)
	}
}
