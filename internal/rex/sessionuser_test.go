//go:build darwin || linux || windows

package rex

import (
	"os"
	"os/user"
	"runtime"
	"testing"
)

func TestSessionUserFromEnvUnset(t *testing.T) {
	t.Setenv(EnvSessionUser, " ")
	if u, err := sessionUserFromEnv(); u != nil || err != nil {
		t.Fatalf("unset: %+v, %v", u, err)
	}
}

// The server's own user is no change, on every system: the sessions were
// its already. Root is refused, as a server that runs as root is not the
// same user but the one the setting exists to leave.
func TestSessionUserFromEnvIsTheServerUser(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip("no user for this process")
	}
	if me.Uid == "0" {
		t.Skip("the server's user is root")
	}
	t.Setenv(EnvSessionUser, me.Username)
	if u, err := sessionUserFromEnv(); u != nil || err != nil {
		t.Fatalf("the server's user: %+v, %v", u, err)
	}
}

func TestSessionUserFromEnvUnknownUser(t *testing.T) {
	t.Setenv(EnvSessionUser, "gorex-no-such-user")
	if _, err := sessionUserFromEnv(); err == nil {
		t.Fatal("an unknown user was accepted")
	}
}

// A session as root is what the setting exists to avoid.
func TestSessionUserFromEnvRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the login shell and the capabilities are Linux's")
	}
	t.Setenv(EnvSessionUser, "root")
	if _, err := sessionUserFromEnv(); err == nil {
		t.Fatal("root was accepted")
	}
}

// The server must run as root to start a session as another user: as a
// user, it cannot, and says so instead of starting it as itself.
func TestSessionUserFromEnvNeedsRoot(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("needs a server that is not root")
	}
	t.Setenv(EnvSessionUser, "nobody")
	if _, err := sessionUserFromEnv(); err == nil {
		t.Fatal("another user was accepted below root")
	}
}

func TestLookupSessionUser(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the login shell is Linux's")
	}
	u, err := lookupSessionUser("root")
	if err != nil {
		t.Fatal(err)
	}
	if u.uid != 0 || u.gid != 0 || u.home != "/root" {
		t.Errorf("root = %+v", u)
	}
	if u.shell == "" {
		t.Error("root has no login shell")
	}
	if _, err := lookupSessionUser("gorex-no-such-user"); err == nil {
		t.Error("an unknown user was found")
	}
}

// A session of another user is that user's: its home, name and shell, and
// no way to the server's socket, as one a remote client made.
func TestSessionEnvAsUser(t *testing.T) {
	t.Setenv("HOME", "/root")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("USER", "root")
	t.Setenv("LOGNAME", "root")
	t.Setenv("PAGER", "less")
	u := &sessionUser{name: "me", uid: 1001, gid: 1001, home: "/home/me", shell: "/bin/zsh"}
	got := envMap(sessionEnv("abc", nil, true, u))
	want := map[string]string{"HOME": "/home/me", "USER": "me", "LOGNAME": "me", "SHELL": "/bin/zsh"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got["PAGER"] != "less" {
		t.Errorf("PAGER = %q, want less", got["PAGER"])
	}
	for _, k := range []string{"GOREX_SERVER", "GOREX_BIN_DIR"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s reached a session of another user", k)
		}
	}
	if got["GOREX_SESSION"] != "abc" {
		t.Errorf("GOREX_SESSION = %q, want abc", got["GOREX_SESSION"])
	}
	if got["PATH"] != os.Getenv("PATH") {
		t.Errorf("PATH %q is not the server's %q", got["PATH"], os.Getenv("PATH"))
	}
}

func TestSessionDir(t *testing.T) {
	dir := t.TempDir()
	if got := sessionDir(dir, "/home/me"); got != dir {
		t.Errorf("a directory: %q, want %q", got, dir)
	}
	if got := sessionDir("/nonexistent/gorex", "/home/me"); got != "/home/me" {
		t.Errorf("a gone directory: %q, want /home/me", got)
	}
	if got := sessionDir("", "/home/me"); got != "/home/me" {
		t.Errorf("no directory: %q, want /home/me", got)
	}
}

func TestSessionExecArgs(t *testing.T) {
	if _, _, err := sessionExecArgs(nil); err == nil {
		t.Error("no program was accepted")
	}
	path, argv, err := sessionExecArgs([]string{"/bin/sh"})
	if err != nil || path != "/bin/sh" || len(argv) != 1 || argv[0] != "/bin/sh" {
		t.Errorf("one argument: %q %q %v", path, argv, err)
	}
	path, argv, err = sessionExecArgs([]string{"/bin/bash", "-bash"})
	if err != nil || path != "/bin/bash" || len(argv) != 1 || argv[0] != "-bash" {
		t.Errorf("a login shell: %q %q %v", path, argv, err)
	}
}

func TestSessionExecMainIsNotOurs(t *testing.T) {
	if ours, err := SessionExecMain([]string{"-server"}); ours || err != nil {
		t.Errorf("-server: %v, %v", ours, err)
	}
	if ours, err := SessionExecMain(nil); ours || err != nil {
		t.Errorf("nothing: %v, %v", ours, err)
	}
}
