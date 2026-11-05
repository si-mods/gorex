//go:build darwin || linux || windows

package rex

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// omp, oh-my-pi, keeps its conversations in its data dir's sessions, a
// directory per cwd (ompSlug), each a jsonl named <ISO time>_<id>.jsonl
// with a directory of the same name beside it, and the files they quote
// in its blobs, content addressed. As omp's own packages/utils/src/dirs.ts
// and session-paths.ts (v18) place them.

// ompDirs are omp's data dir, which has sessions and blobs, and its state
// dir, which has terminal-sessions, for a process whose environment env
// tells and whose arguments are args, home its home directory:
// ~/.omp/agent, PI_CONFIG_DIR for .omp, a profile's (OMP_PROFILE,
// PI_PROFILE, --profile) under profiles/, PI_CODING_AGENT_DIR without a
// profile, and XDG_DATA_HOME/omp and XDG_STATE_HOME/omp when there.
func ompDirs(env func(string) string, args []string, home string) (data, state string) {
	profile := env("OMP_PROFILE")
	if profile == "" {
		profile = env("PI_PROFILE")
	}
	for i, a := range args {
		if a == "--profile" && i+1 < len(args) {
			profile = args[i+1]
		} else if v, ok := strings.CutPrefix(a, "--profile="); ok {
			profile = v
		}
	}
	if profile == "default" || !localName(profile) || strings.ContainsAny(profile, `/\`) {
		profile = ""
	}
	cfg := ".omp"
	if v := env("PI_CONFIG_DIR"); v != "" {
		cfg = v
	}
	root := filepath.Join(home, cfg)
	if profile != "" {
		root = filepath.Join(root, "profiles", profile)
	}
	agent := filepath.Join(root, "agent")
	if d := env("PI_CODING_AGENT_DIR"); d != "" && profile == "" && filepath.IsAbs(d) {
		return d, d
	}
	xdg := func(name string) string {
		v := env(name)
		if v == "" {
			return ""
		}
		d := filepath.Join(v, "omp")
		if profile != "" {
			d = filepath.Join(d, "profiles", profile)
		}
		if exists(d) {
			return d
		}
		return ""
	}
	data, state = agent, agent
	if d := xdg("XDG_DATA_HOME"); d != "" {
		data = d
	}
	if d := xdg("XDG_STATE_HOME"); d != "" {
		state = d
	}
	return data, state
}

// ompSlug is the name of omp's directory of the conversations started in
// dir: under the temporary directory, -tmp and the path from it; under
// home, - and the path from it; else the path between -- and --, every
// separator and colon a -.
func ompSlug(dir, home string) string {
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	u := real(dir)
	under := func(base string) (string, bool) {
		rel, err := filepath.Rel(real(base), u)
		if err != nil {
			return "", false
		}
		if rel == "." {
			rel = ""
		}
		return rel, rel == "" || !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
	}
	dashes := strings.NewReplacer("/", "-", `\`, "-", ":", "-")
	join := func(prefix, rel string) string {
		r := dashes.Replace(rel)
		switch {
		case r == "":
			return prefix
		case strings.HasSuffix(prefix, "-"):
			return prefix + r
		}
		return prefix + "-" + r
	}
	if rel, ok := under(os.TempDir()); ok {
		return join("-tmp", rel)
	}
	if rel, ok := under(home); ok {
		return join("-", rel)
	}
	if strings.HasPrefix(u, "/") || strings.HasPrefix(u, `\`) {
		u = u[1:]
	}
	return "--" + dashes.Replace(u) + "--"
}

// ompSession returns the conversation of omp's process whose environment
// env tells, whose directory is dir, arguments args and terminal tty: its
// id, omp's data dir, and the file. omp records each terminal's in its state dir's
// terminal-sessions, by the tty's name; where it has no tty, as on
// Windows, the conversation of dir written last stands for it. All ""
// when none is found.
func ompSession(env func(string) string, dir string, args []string, tty string) (id, data, file string) {
	home := env("HOME")
	if home == "" {
		home = env("USERPROFILE")
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	data, state := ompDirs(env, args, home)
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	if name, ok := strings.CutPrefix(tty, "/dev/"); ok {
		b, err := os.ReadFile(filepath.Join(state, "terminal-sessions", strings.ReplaceAll(name, "/", "-")))
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if err == nil && len(lines) >= 2 && samePath(real(lines[0]), real(dir)) && exists(lines[1]) {
			file = lines[1]
		}
	}
	if file == "" {
		sessions := filepath.Join(data, "sessions", ompSlug(dir, home))
		for i, a := range args {
			if a == "--session-dir" && i+1 < len(args) {
				sessions = args[i+1]
			} else if v, ok := strings.CutPrefix(a, "--session-dir="); ok {
				sessions = v
			}
		}
		found, _ := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
		var newest os.FileInfo
		for _, f := range found {
			if st, err := os.Stat(f); err == nil && (newest == nil || st.ModTime().After(newest.ModTime())) {
				newest, file = st, f
			}
		}
	}
	if id = ompID(filepath.Base(file)); id == "" {
		return "", "", ""
	}
	return id, data, file
}

// ompID is the conversation id of omp's file name, <time>_<id>.jsonl,
// "" for another name.
func ompID(name string) string {
	stem, ok := strings.CutSuffix(name, ".jsonl")
	i := strings.LastIndexByte(stem, '_')
	if !ok || i <= 0 || !IsUUID(stem[i+1:]) || slices.ContainsFunc([]rune(stem[:i]), func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-')
	}) {
		return ""
	}
	return stem[i+1:]
}

// ompDataDir is omp's data dir of this machine's sessions, which inherit
// the server's environment.
func (s *Server) ompDataDir() (string, error) {
	home, err := s.homeDir()
	if err != nil {
		return "", err
	}
	data, _ := ompDirs(os.Getenv, nil, home)
	return data, nil
}

// ompResume is the command that resumes omp's conversation id, as the
// shell at path reads it.
func ompResume(shell, id string) (string, bool) {
	q := QuoterOf(shell)
	if q == nil {
		return "", false
	}
	w, ok := q(id)
	return "omp --resume " + w, ok
}

// blobName tells whether name is one of omp's blobs: a SHA-256 in hex,
// and an extension.
func blobName(name string) bool {
	h, ext, _ := strings.Cut(name, ".")
	return hexHead(h) && len(h) == 64 && !strings.ContainsAny(ext, `/\.`) && (ext == "" || localName(ext))
}
