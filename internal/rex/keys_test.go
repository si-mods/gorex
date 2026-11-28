package rex

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEncodeKeySequence(t *testing.T) {
	cases := []struct {
		specs []string
		want  []byte
	}{
		{[]string{"alt+m"}, []byte("\x1bm")},
		{[]string{"opt+m"}, []byte("\x1bm")},
		{[]string{"meta+m"}, []byte("\x1bm")},
		{[]string{"esc>m"}, []byte("\x1bm")},
		{[]string{"esc", "m"}, []byte("\x1bm")},
		{[]string{"esc m"}, []byte("\x1bm")},
		{[]string{"alt+shift+m"}, []byte("\x1bM")},
		{[]string{"ctrl+c"}, []byte{0x03}},
		{[]string{"ctrl+alt+c"}, []byte{0x1b, 0x03}},
		{[]string{"ctrl+space"}, []byte{0x00}},
		{[]string{"enter"}, []byte{'\r'}},
		{[]string{"return"}, []byte{'\r'}},
		{[]string{"esc"}, []byte{0x1b}},
		{[]string{"tab"}, []byte{'\t'}},
		{[]string{"shift+tab"}, []byte("\x1b[Z")},
		{[]string{"btab"}, []byte("\x1b[Z")},
		{[]string{"backspace"}, []byte{0x7f}},
		{[]string{"alt+backspace"}, []byte{0x1b, 0x7f}},
		{[]string{"delete"}, []byte("\x1b[3~")},
		{[]string{"up"}, []byte("\x1b[A")},
		{[]string{"ctrl+up"}, []byte("\x1b[1;5A")},
		{[]string{"f1"}, []byte("\x1bOP")},
		{[]string{"shift+f1"}, []byte("\x1b[1;2P")},
		{[]string{"f5"}, []byte("\x1b[15~")},
		{[]string{"+"}, []byte{'+'}},
		{[]string{"alt++"}, []byte("\x1b+")},
	}
	for _, tc := range cases {
		got, err := EncodeKeySequence(tc.specs...)
		if err != nil {
			t.Errorf("EncodeKeySequence(%q) unexpected error: %v", tc.specs, err)
			continue
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("EncodeKeySequence(%q) = %q, want %q", tc.specs, got, tc.want)
		}
	}

	bad := [][]string{
		nil,
		{""},
		{"\t\n"},
		{"hyper+m"},
		{"unknownkey"},
		{"alt+"},
	}
	for _, specs := range bad {
		if got, err := EncodeKeySequence(specs...); err == nil {
			t.Errorf("EncodeKeySequence(%q) = %q, want error", specs, got)
		}
	}
}

func TestAgentRegistryFlushAndSplash(t *testing.T) {
	idCases := []struct {
		in   string
		want string
	}{
		{"agy", "agy"},
		{"antigravity", "agy"},
		{"/opt/homebrew/bin/antigravity-cli", "agy"},
		{`C:\Users\alice\AppData\Roaming\npm\agy.cmd`, "agy"},
		{"claude", "claude"},
		{"claude-code", "claude"},
		{"omp.exe.12345.bak", "omp"},
		{"codex", "codex"},
		{"devin", "devin"},
		{"zsh", ""},
		{"", ""},
	}
	for _, tc := range idCases {
		if got := CanonicalAgentID(tc.in); got != tc.want {
			t.Errorf("CanonicalAgentID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if got := SessionAgentID(SessionInfo{DelegatedAgent: "agy", Program: "node"}); got != "agy" {
		t.Fatalf("DelegatedAgent resolution = %q, want agy", got)
	}
	if got := SessionAgentID(SessionInfo{Program: "node", Status: &ProgramStatus{App: "antigravity"}}); got != "agy" {
		t.Fatalf("Status.App resolution = %q, want agy", got)
	}
	if got := SessionAgentID(SessionInfo{Program: "node", Args: []string{"node", "/usr/local/lib/antigravity-cli.js"}}); got != "agy" {
		t.Fatalf("Args resolution = %q, want agy", got)
	}
	if got := SessionAgentID(SessionInfo{Program: "zsh"}); got != "" {
		t.Fatalf("plain shell resolution = %q, want empty", got)
	}

	if got := AgentFlushInput("agy"); !bytes.Equal(got, []byte("\x1bm")) {
		t.Fatalf("AgentFlushInput(agy) = %q, want \\x1bm", got)
	}
	if got := AgentFlushInput("antigravity"); !bytes.Equal(got, []byte("\x1bm")) {
		t.Fatalf("AgentFlushInput(antigravity) = %q, want \\x1bm", got)
	}
	for _, unconfigured := range []string{"claude", "codex", "omp", "devin", "zsh", ""} {
		if got := AgentFlushInput(unconfigured); got != nil {
			t.Errorf("AgentFlushInput(%q) = %q, want nil", unconfigured, got)
		}
	}

	if got := agentSplashDismiss("agy", "Introducing Gemini 4 Neon — esc to dismiss"); !bytes.Equal(got, []byte{0x1b}) {
		t.Fatalf("agentSplashDismiss splash = %q, want ESC", got)
	}
	if got := agentSplashDismiss("agy", "Antigravity 1.5.18\n❯ "); got != nil {
		t.Fatalf("agentSplashDismiss normal prompt = %q, want nil", got)
	}
}

func TestActionSendKeysServerOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sh and cat -v")
	}
	s, _ := testServer(t)
	local := pipe(s, "")
	t.Cleanup(func() { local.Close() })
	remote := pipe(s, "nodekey:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Cleanup(func() { remote.Close() })

	info, err := local.Create(CreateOptions{
		Command: []string{"/bin/sh", "-c", "stty raw -echo; exec cat -v"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Kill(info.ID) })

	// Works with no GoRex window open (desks == 0) and on remote connections
	// even when remoteControl is off.
	res, err := remote.Action(info.ID, "send-keys", map[string]any{"keys": "alt+m"})
	if err != nil || res.Code != "" {
		t.Fatalf("remote Action send-keys: res=%+v err=%v", res, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		capRes, capErr := local.Capture(info.ID, 0)
		if capErr == nil && strings.Contains(string(capRes.Screen), "^[m") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never showed ^[m: %v", capErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	badRes, err := local.Action(info.ID, "send-key", map[string]any{"keys": "hyper+z"})
	if err != nil || badRes.Code != "bad_args" {
		t.Fatalf("bad Action send-key: res=%+v err=%v, want Code=bad_args", badRes, err)
	}
}
