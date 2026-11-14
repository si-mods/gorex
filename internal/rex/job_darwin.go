//go:build darwin

package rex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The server as a launchd job.
//
// macOS ends a dying app's subordinate processes: when an app exits while
// it still has children and Background Task Management has no answer on
// whether the app may run in the background, loginwindow "schedules its
// subordinates' termination" and force quits them ("askBTM: Error -98...
// not allowed to run in the background"). The session server is such a
// child of the app, so an update's relaunch — the app quits, the updater
// swaps the bundle, the new app opens — ended every session with the app,
// and the conversations in its panes with them.
//
// A job of launchd's is nobody's child: the app that loaded it may quit,
// crash or be replaced without touching the sessions. Spawn loads the job
// in the user's session and starts it; the server then serves the socket
// as it always has, and Connect dials it as it always has.

// errNoJob is what spawnJob returns when this platform has no job of its
// own to start the server as: Spawn starts it as a detached process then.
var errNoJob = errors.New("rex: no job to start the server as")

// jobLabel is the label of the server's job in dir: one per data
// directory, so that a dev build's server and the installed app's are
// jobs of their own. Rex names its server's job the same way, with the
// SHA-256 of the server's directory ("com.superlogical.rex.server.ebba…").
func jobLabel(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return "dev.gorex.app.server." + hex.EncodeToString(sum[:])
}

// inBundle reports whether exe runs from an app bundle. The app's own
// binary does; a command started from a pane is the same file through the
// shim; a test binary or `go run . -server`'s does not, and those start
// the server as a process of their own.
func inBundle(exe string) bool {
	return strings.Contains(filepath.ToSlash(exe), ".app/Contents/MacOS/")
}

// spawnJob starts the session server as a launchd job of the user's
// session, replacing whatever runs in dir at once. It runs the copy of
// this build (serverCopy), not the app: an update replaces the app's
// bundle, not the copy.
func spawnJob(appExe string) error {
	if !inBundle(appExe) {
		return errNoJob
	}
	program, _, err := serverCopy(appExe)
	if err != nil {
		return err
	}
	uid := strconv.Itoa(os.Getuid())
	domain := "gui/" + uid
	// No GUI session (an ssh login, a test runner): an agent of the
	// user's would not run there either.
	if exec.Command("launchctl", "print", domain).Run() != nil {
		return errNoJob
	}
	dir := Dir()
	label := jobLabel(dir)
	path := filepath.Join(dir, "server.plist")
	if err := writePlist(path, jobPlist(label, program, appExe)); err != nil {
		return fmt.Errorf("rex: writing the server's job: %w", err)
	}
	if exec.Command("launchctl", "print", domain+"/"+label).Run() != nil {
		if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
			return fmt.Errorf("rex: loading the server's job: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	out, err := exec.Command("launchctl", "kickstart", domain+"/"+label).CombinedOutput()
	if err == nil {
		return nil
	}
	// A job of a binary that has moved since it was loaded: load this
	// definition in its place.
	_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run()
	if bout, berr := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); berr != nil {
		return fmt.Errorf("rex: loading the server's job: %w: %s; %s", berr, strings.TrimSpace(string(bout)), strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("launchctl", "kickstart", domain+"/"+label).CombinedOutput(); err != nil {
		return fmt.Errorf("rex: starting the server's job: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// jobPlist is the definition of the job label that runs program, the
// copy of this build, as the server of this machine: a few of the app's
// variables, so that a pane's shell starts as it does now (and the app's
// own path, which the server's shim keeps pointing at), the log beside
// the data directory, and no start of its own (RunAtLoad): Spawn starts
// it, and it stays down while no app uses it, as the idle timeout leaves
// it.
func jobPlist(label, program, appExe string) []byte {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	keyString(&b, "Label", label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	text(&b, "\t\t<string>", program)
	b.WriteString("</string>\n")
	text(&b, "\t\t<string>", "-server")
	b.WriteString("</string>\n\t</array>\n")
	b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	for _, kv := range jobEnv() {
		k, v, _ := strings.Cut(kv, "=")
		keyString(&b, k, v)
	}
	keyString(&b, EnvAppExe, appExe)
	b.WriteString("\t</dict>\n")
	keyString(&b, "StandardOutPath", filepath.Join(Dir(), "server.log"))
	keyString(&b, "StandardErrorPath", filepath.Join(Dir(), "server.log"))
	b.WriteString("\t<key>LimitLoadToSessionType</key>\n\t<string>Aqua</string>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<false/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<false/>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// jobEnv is the environment of the server's job: what a pane's shell and
// the server need of the app's, and not the app's whole environment,
// which the plist would keep on disk. Rex passes its server the same few
// (its job's environment is PATH, HOME, USER, LOGNAME, SHELL, TMPDIR,
// SSH_AUTH_SOCK and its own REX_* variables).
func jobEnv() []string {
	keep := map[string]bool{
		"GOREX_DIR": true, // the server's directory: the socket and the layout
		"PATH":      true, // a pane's programs keep the app's, not the launchd default's
		"HOME":      true, "USER": true, "LOGNAME": true,
		"SHELL":         true,
		"TMPDIR":        true,
		"SSH_AUTH_SOCK": true,
		"LANG":          true, "LC_ALL": true, "LC_CTYPE": true,
	}
	var env []string
	has := map[string]bool{}
	for _, kv := range serverEnv() {
		k, _, _ := strings.Cut(kv, "=")
		if keep[k] && !has[k] {
			has[k] = true
			env = append(env, kv)
		}
	}
	if !has["PATH"] {
		env = append(env, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
	}
	return env
}

// keyString writes a key of the plist and its string value.
func keyString(b *bytes.Buffer, key, value string) {
	text(b, "\t<key>", key)
	b.WriteString("</key>\n")
	text(b, "\t<string>", value)
	b.WriteString("</string>\n")
}

// text writes prefix, s escaped for XML, and nothing after it.
func text(b *bytes.Buffer, prefix, s string) {
	b.WriteString(prefix)
	xml.EscapeText(b, []byte(s))
}

// writePlist writes b to path, readable by the user only, atomically.
func writePlist(path string, b []byte) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
