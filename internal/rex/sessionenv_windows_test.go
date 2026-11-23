//go:build windows

package rex

import (
	"slices"
	"strings"
	"testing"
)

func fakeRegistryEnv(t *testing.T, env []string) {
	t.Helper()
	old := registryEnvFunc
	registryEnvFunc = func() []string { return env }
	t.Cleanup(func() { registryEnvFunc = old })
}

func TestFillRegistryEnv(t *testing.T) {
	fakeRegistryEnv(t, []string{
		`CLAUDE_CONFIG_DIR=C:\Users\alice\.claude-team`,
		`Path=C:\reg\bin`,
		"FOO=registry",
		"TERM=dumb",
		`GOREX_DIR=C:\reg-gorex`,
		"lower=1",
	})
	env := []string{`Path=C:\server\bin`, "FOO=server", "TERM=xterm-256color"}
	got := fillRegistryEnv(slices.Clone(env), false)
	m := envMap(got)
	if m[`CLAUDE_CONFIG_DIR`] != `C:\Users\alice\.claude-team` {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want the registry's", m["CLAUDE_CONFIG_DIR"])
	}
	if m["FOO"] != "server" {
		t.Errorf("FOO = %q, want the server's, not the registry's", m["FOO"])
	}
	if m["TERM"] != "xterm-256color" {
		t.Errorf("TERM = %q, want the session's", m["TERM"])
	}
	if m["lower"] != "1" {
		t.Errorf("lower = %q, want 1", m["lower"])
	}
	for _, kv := range got {
		k, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(env, kv) && k != "CLAUDE_CONFIG_DIR" && k != "lower" {
			t.Errorf("unexpected fill %q", kv)
		}
	}
}

func TestFillRegistryEnvOtherUser(t *testing.T) {
	fakeRegistryEnv(t, []string{"FOO=registry"})
	env := []string{"A=1"}
	got := fillRegistryEnv(slices.Clone(env), true)
	if !slices.Equal(got, env) {
		t.Errorf("fillRegistryEnv(runAs) = %v, want %v unchanged", got, env)
	}
}
