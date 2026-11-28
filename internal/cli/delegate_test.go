package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gorex/internal/rex"
)

func TestDelegateCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX TUI script")
	}
	_ = startServer(t)

	// Usage validation errors.
	if code, _, _, _ := runMain(t, "delegate"); code != ExitUsage {
		t.Fatalf("delegate without prompt: got %d, want %d", code, ExitUsage)
	}
	if code, _, _, _ := runMain(t, "delegate", "--mode", "bogus", "--", "hi"); code != ExitUsage {
		t.Fatalf("delegate bad mode: got %d, want %d", code, ExitUsage)
	}
	if code, _, _, _ := runMain(t, "delegate", "--ready-timeout", "-1s", "--", "hi"); code != ExitUsage {
		t.Fatalf("delegate negative ready-timeout: got %d, want %d", code, ExitUsage)
	}

	claudeCfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeCfg)
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	// Compute Claude's project slug (replace non-alnum with '-').
	slug := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, workDir)
	script := fmt.Sprintf(`#!/bin/sh
sid=""
while [ $# -gt 0 ]; do
  case "$1" in
    --session-id|--resume) sid="$2"; shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "%s/projects/%s"
out="%s/projects/%s/${sid}.jsonl"
printf '> '
while IFS= read -r line; do
  clean=$(printf '%%s' "$line" | tr -d '\r' | sed 's/\x1b\[200~//g; s/\x1b\[201~//g')
  [ -z "$clean" ] && continue
  printf '{"type":"user","sessionId":"%%s","message":{"role":"user","content":"%%s"}}\n' "$sid" "$clean" >> "$out"
  printf '{"type":"assistant","sessionId":"%%s","message":{"model":"claude-sonnet","content":[{"type":"text","text":"cli:%%s"}]}}\n' "$sid" "$clean" >> "$out"
  printf '{"type":"system","subtype":"turn_duration"}\n' >> "$out"
  printf 'cli:%%s\n> ' "$clean"
done
`, claudeCfg, slug, claudeCfg, slug)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/sh")
	rex.ResetAgentsCacheForTest()
	t.Cleanup(rex.ResetAgentsCacheForTest)

	// 1. Sync delegate with --keep --json.
	code, handled, out, errOut := runMain(t, "delegate", "--agent", "claude", "--dir", workDir, "--keep", "--timeout", "10s", "--json", "--", "ping one")
	if code != ExitOK || !handled {
		t.Fatalf("delegate: code=%d handled=%v out=%q err=%q", code, handled, out, errOut)
	}
	var got delegateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got.Status != "completed" || got.Result != "cli:ping one" || got.Session == "" || !rex.IsUUID(got.AgentSession) {
		t.Fatalf("delegate output = %+v", got)
	}

	// 2. Warm follow-up with --session.
	code, _, out2, errOut2 := runMain(t, "delegate", "--session", got.Session, "--timeout", "10s", "--json", "--", "ping two")
	if code != ExitOK {
		t.Fatalf("delegate --session: code=%d out=%q err=%q", code, out2, errOut2)
	}
	var got2 delegateOutput
	if err := json.Unmarshal([]byte(out2), &got2); err != nil || got2.Result != "cli:ping two" {
		t.Fatalf("delegate --session output = %q (%v)", out2, err)
	}

	// 3. Fetch result with --result.
	code, _, out3, errOut3 := runMain(t, "delegate", "--result", got.Session, "--json")
	if code != ExitOK {
		t.Fatalf("delegate --result: code=%d out=%q err=%q", code, out3, errOut3)
	}
	var got3 delegateOutput
	if err := json.Unmarshal([]byte(out3), &got3); err != nil || got3.Result != "cli:ping two" {
		t.Fatalf("delegate --result output = %q (%v)", out3, err)
	}

	// Clean up kept session.
	_, _, _, _ = runMain(t, "close", "-s", got.Session)

	// 4. Async delegate must NOT kill the session even if the turn finishes quickly.
	code, _, out4, errOut4 := runMain(t, "delegate", "--agent", "claude", "--dir", workDir, "--async", "--json", "--", "async ping")
	if code != ExitOK {
		t.Fatalf("delegate --async: code=%d out=%q err=%q", code, out4, errOut4)
	}
	var got4 delegateOutput
	if err := json.Unmarshal([]byte(out4), &got4); err != nil || got4.Session == "" {
		t.Fatalf("delegate --async output = %q (%v)", out4, err)
	}
	code, _, resOut, resErr := runMain(t, "delegate", "--result", got4.Session, "--json")
	if code != ExitOK {
		t.Fatalf("delegate --result after --async: code=%d out=%q err=%q", code, resOut, resErr)
	}
	_, _, _, _ = runMain(t, "close", "-s", got4.Session)
}
