package rex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Teleport moves a pane's work to another machine: the Git work tree's
// state, Claude Code's conversation and the scrollback, packed by this
// machine's server ("teleport.pack") and unpacked by the target's
// ("teleport"), which starts the program again there. docs/teleport.md
// tells the whole of it.

// CapabilityTeleport is what a server that takes a teleport says it does:
// the "teleport" op.
const CapabilityTeleport = "teleport"

// The limits of a package: the whole, and its untracked files, so that a
// stray build output does not cross the network.
const (
	teleportMax  = 256 << 20
	untrackedMax = 64 << 20
	// scrollbackMax is how much of the pane's text goes along, its end.
	scrollbackMax = 1 << 20
)

// TeleportManifest describes a package: what teleport.pack answers before
// the package's bytes, and the "teleport" request sends before the same.
type TeleportManifest struct {
	Version int `json:"version"`
	// Host is the source machine's name.
	Host string `json:"host,omitempty"`
	// Program is "claude" or "shell"; Session is Claude Code's
	// conversation.
	Program string `json:"program"`
	Session string `json:"session,omitempty"`
	// Remotes are the repo's remote URLs, origin's first; Branch the branch
	// checked out, "" when detached, and Head its commit.
	Remotes []string `json:"remotes"`
	Branch  string   `json:"branch,omitempty"`
	Head    string   `json:"head"`
	// Root is the work tree's root on the source, as its OS writes it, and
	// Dir the pane's directory under it, slash separated, "" for the root.
	Root string `json:"root"`
	Dir  string `json:"dir,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
	// Size is the length of the package, a tar stream.
	Size int64 `json:"size"`
}

// teleportVersion is the manifest's version this build writes and reads.
const teleportVersion = 1

// The reasons a session cannot teleport (TeleportRefusal).
const (
	TeleportEnded   = "ended"
	TeleportProgram = "program"
	TeleportUnknown = "unknown"
	TeleportWorking = "working"
)

// TeleportRefusal tells why session in cannot teleport, "" when it can: a
// shell at its prompt can, and Claude Code whose conversation is known
// while it waits for the user. It cannot tell whether the directory is a
// Git work tree; the packing does.
func TeleportRefusal(in SessionInfo) string {
	switch {
	case in.Exited:
		return TeleportEnded
	case in.Idle:
		return ""
	case !strings.EqualFold(in.Program, "claude"):
		return TeleportProgram
	case !IsUUID(in.AgentSession):
		return TeleportUnknown
	case in.Status != nil && in.Status.State == "working" || time.Since(in.LastOutput) < 2*time.Second:
		// A conversation still being written; a spinner prints.
		return TeleportWorking
	}
	return ""
}

// teleportReason says a refusal of TeleportRefusal, for an error.
func teleportReason(code, program string) string {
	switch code {
	case TeleportEnded:
		return "the session has ended"
	case TeleportProgram:
		return program + " is running: only a shell at its prompt or Claude Code can teleport"
	case TeleportUnknown:
		return "Claude Code's conversation is not known"
	case TeleportWorking:
		return "Claude Code is working: wait for it to finish, or stop it"
	}
	return code
}

// Teleport steps, as Teleport tells them.
const (
	TeleportPacking  = "pack"
	TeleportSending  = "send"
	TeleportStarting = "start"
)

// Teleport moves session sid of src, this machine's server, to dst's: src
// packs it, and the package goes through this process to dst, which
// unpacks it and starts the program again in a session of its own, which
// it returns. step is told each step as it begins. Cancelling ctx before
// dst starts the session ends it there, and nothing is left; the source
// session is never changed: the caller ends it once it has the new one.
func Teleport(ctx context.Context, src *Client, sid string, dst *Client, step func(string)) (SessionInfo, error) {
	var mu sync.Mutex
	var conns []net.Conn
	keep := func(c net.Conn) bool {
		mu.Lock()
		defer mu.Unlock()
		conns = append(conns, c)
		return ctx.Err() == nil
	}
	defer context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	}()
	fail := func(err error) (SessionInfo, error) {
		if ctx.Err() != nil {
			return SessionInfo{}, ctx.Err()
		}
		return SessionInfo{}, err
	}
	if src.dial == nil || dst.dial == nil {
		return SessionInfo{}, errors.New("rex: no connection to open for a teleport")
	}

	step(TeleportPacking)
	pc, err := src.dial()
	if err != nil {
		return fail(err)
	}
	if !keep(pc) {
		return fail(ctx.Err())
	}
	b, _ := json.Marshal(Request{Op: "teleport.pack", SID: sid})
	if _, err := pc.Write(append(b, '\n')); err != nil {
		return fail(err)
	}
	pr := bufio.NewReaderSize(pc, 64<<10)
	var m TeleportManifest
	if err := readAnswer(pr, &m); err != nil {
		return fail(err)
	}
	if m.Size < 0 || m.Size > teleportMax {
		return fail(errors.New("rex: the package is too large"))
	}

	step(TeleportSending)
	tc, err := dst.dial()
	if err != nil {
		return fail(err)
	}
	if !keep(tc) {
		return fail(ctx.Err())
	}
	b, _ = json.Marshal(Request{Op: "teleport", Teleport: &m})
	if _, err := tc.Write(append(b, '\n')); err != nil {
		return fail(err)
	}
	// The target refuses at once what it would refuse after the package.
	if err := readOK(tc); err != nil {
		return fail(err)
	}
	if _, err := io.CopyN(tc, pr, m.Size); err != nil {
		return fail(fmt.Errorf("rex: sending the package: %w", err))
	}

	step(TeleportStarting)
	var in SessionInfo
	if err := readAnswer(bufio.NewReader(tc), &in); err != nil {
		return fail(err)
	}
	return in, nil
}

// readAnswer reads a Response line of r into out, or its error.
func readAnswer(r *bufio.Reader, out any) error {
	line, err := readLine(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("rex: the server closed the connection")
		}
		return err
	}
	var res Response
	if err := json.Unmarshal(line, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	return json.Unmarshal(res.Data, out)
}

// readLine reads a line of at most maxLine bytes, the newline included.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxLine {
			return nil, errLineTooLong
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return line, nil
	}
}

// maxLine is the longest line of control a connection may send.
const maxLine = 1 << 20

var errLineTooLong = errors.New("rex: the request is too long")

// claudeSlug is the name Claude Code gives the directory of the
// conversations started in dir, under its config dir's projects: every
// character other than an ASCII letter or digit becomes "-".
func claudeSlug(dir string) string {
	out := make([]byte, 0, len(dir))
	for _, r := range dir {
		if r < unicode.MaxASCII && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			out = append(out, byte(r))
		} else {
			// One per UTF-16 unit, as JavaScript's replace counts them.
			out = append(out, '-')
			if r > 0xFFFF {
				out = append(out, '-')
			}
		}
	}
	return string(out)
}

// windowsPath tells whether p, a path of another machine, is Windows':
// a drive letter, or backslashes and no slash.
func windowsPath(p string) bool {
	return len(p) >= 2 && p[1] == ':' || strings.Contains(p, `\`) && !strings.Contains(p, "/")
}

// movePath maps p, a path of the source machine, from under its work tree
// root to the same place under to, a path of this one; false for a path
// outside root. root is written as the source's OS writes it.
func movePath(p, root, to string) (string, bool) {
	win := windowsPath(root)
	sep := "/"
	same := func(a, b string) bool { return a == b }
	if win {
		sep, same = `\`, strings.EqualFold
	}
	if root == "" || len(root) > 1 && strings.HasSuffix(root, sep) {
		root = strings.TrimSuffix(root, sep)
	}
	if root == "" {
		return "", false
	}
	if same(p, root) {
		return to, true
	}
	if len(p) <= len(root) || !same(p[:len(root)], root) || p[len(root):len(root)+1] != sep {
		return "", false
	}
	parts := strings.Split(p[len(root)+1:], sep)
	for _, part := range parts {
		if part == ".." {
			return "", false
		}
	}
	return filepath.Join(append([]string{to}, parts...)...), true
}

// rewriteCwd rewrites the "cwd" of a line of a conversation from under
// root, a path of the source, to under to: the line as it was when it has
// none, or one outside root.
func rewriteCwd(line []byte, root, to string) []byte {
	var f struct {
		Cwd *string `json:"cwd"`
	}
	if json.Unmarshal(line, &f) != nil || f.Cwd == nil {
		return line
	}
	moved, ok := movePath(*f.Cwd, root, to)
	if !ok {
		return line
	}
	// Only the field changes: the rest of the line stays as Claude Code
	// wrote it, which JSON.stringify writes as Go does with HTML left be.
	old, nu := jsonString(*f.Cwd), jsonString(moved)
	field := []byte(`"cwd":` + old)
	if n := strings.Count(string(line), string(field)); n == 1 {
		return []byte(strings.Replace(string(line), string(field), `"cwd":`+nu, 1))
	}
	// Another spelling of the field: write the line anew.
	var all map[string]json.RawMessage
	if json.Unmarshal(line, &all) != nil {
		return line
	}
	all["cwd"] = json.RawMessage(nu)
	b, err := json.Marshal(all)
	if err != nil {
		return line
	}
	return b
}

// jsonString is s as a JSON string, as JavaScript writes it.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// localName tells whether name, a slash-separated path of a package, stays
// under the directory it is unpacked in, here: no "..", no absolute path,
// no drive, and no .git of a work tree.
func localName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || !filepath.IsLocal(filepath.FromSlash(name)) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.FromSlash(name)), "/") {
		if strings.EqualFold(part, ".git") || part == ".." {
			return false
		}
	}
	return true
}

// plainText is s with the control characters a terminal would act on
// left out, but newlines and tabs: text that only shows.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) && r != unicode.ReplacementChar {
			return r
		}
		return -1
	}, s)
}

// claudeConfigDir is Claude Code's config dir of this machine's sessions:
// CLAUDE_CONFIG_DIR of the server's environment, which they inherit, else
// ~/.claude.
func claudeConfigDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// teleportRoots are where a teleport looks for a clone of the repo, and
// clones one into the first: ~/repos, ~/src, ~/code and ~/Projects.
var teleportRoots = func() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var roots []string
	for _, d := range []string{"repos", "src", "code", "Projects"} {
		roots = append(roots, filepath.Join(home, d))
	}
	return roots
}

// repoKey is a remote URL as two spellings of the same repo share it:
// https://host/owner/name, git@host:owner/name and ssh://git@host/owner/name
// all give host/owner/name; a local path stays a path.
func repoKey(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimSuffix(strings.TrimRight(u, `/\`), ".git")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		host, path, _ := strings.Cut(u, "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if h, _, ok := strings.Cut(host, ":"); ok && !strings.Contains(path, ":") {
			host = h // a port
		}
		return strings.ToLower(host + "/" + path)
	}
	// scp's user@host:path, not a Windows drive.
	if host, path, ok := strings.Cut(u, ":"); ok && len(host) > 1 && !strings.ContainsAny(host, `/\`) {
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		return strings.ToLower(host + "/" + strings.TrimPrefix(path, "/"))
	}
	return filepath.Clean(u)
}

// repoName is the directory a clone of URL u gets: the last part of its
// path, without .git.
func repoName(u string) string {
	u = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(u), `/\`), ".git")
	if i := strings.LastIndexAny(u, `/\:`); i >= 0 {
		u = u[i+1:]
	}
	if !localName(u) || strings.ContainsAny(u, `/\`) {
		return "repo"
	}
	return u
}
