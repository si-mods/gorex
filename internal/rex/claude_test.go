package rex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseClaudeSession(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	for _, tt := range []struct {
		label, file string
		want        string
	}{
		{"own", `{"pid":42,"sessionId":"` + id + `","cwd":"/x"}`, id},
		{"another process's", `{"pid":7,"sessionId":"` + id + `"}`, ""},
		{"no id", `{"pid":42}`, ""},
		{"not an id", `{"pid":42,"sessionId":"x; rm -rf ~"}`, ""},
		{"not json", `{`, ""},
	} {
		if got := parseClaudeSession([]byte(tt.file), 42); got != tt.want {
			t.Errorf("%s: parseClaudeSession = %q, want %q", tt.label, got, tt.want)
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
	if id, dir := claudeSession(pid, ""); id != "" || dir != "" {
		t.Errorf("without a file: claudeSession = %q, %q", id, dir)
	}
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	os.MkdirAll(filepath.Join(config, "sessions"), 0o755)
	file := fmt.Sprintf(`{"pid":%d,"sessionId":%q}`, pid, id)
	if err := os.WriteFile(filepath.Join(config, "sessions", fmt.Sprint(pid)+".json"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if gotID, gotDir := claudeSession(pid, ""); gotID != id || gotDir != config {
		t.Errorf("claudeSession = %q, %q; want %q, %q", gotID, gotDir, id, config)
	}
}
