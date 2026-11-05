//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// homeDir is the home directory of the server's user.
func (s *Server) homeDir() (string, error) {
	if s.home != "" {
		return s.home, nil
	}
	return os.UserHomeDir()
}

// claudeConfigDir is Claude Code's config dir of this machine's sessions:
// CLAUDE_CONFIG_DIR of the server's environment, which they inherit, else
// ~/.claude.
func (s *Server) claudeConfigDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		return d, nil
	}
	home, err := s.homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// homeRel is root relative to the home directory, slash separated, ""
// when outside it.
func (s *Server) homeRel(root string) string {
	home, err := s.homeDir()
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(home, root)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// gitTop finds the work tree dir is in, walking up, and the common dir of
// its repo: its .git directory, or the one a linked work tree's .git file
// points to, through that one's commondir. Read from the files, as
// asking git for every directory would be slow.
func gitTop(dir string) (top, common string, ok bool) {
	for d := filepath.Clean(dir); ; {
		g := filepath.Join(d, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				return d, g, true
			}
			b, err := os.ReadFile(g)
			gd, found := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if err != nil || !found {
				return "", "", false
			}
			gd = filepath.FromSlash(strings.TrimSpace(gd))
			if !filepath.IsAbs(gd) {
				gd = filepath.Join(d, gd)
			}
			common = gd
			if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
				if common = filepath.FromSlash(strings.TrimSpace(string(c))); !filepath.IsAbs(common) {
					common = filepath.Join(gd, common)
				}
			}
			return d, filepath.Clean(common), true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", false
		}
		d = parent
	}
}

// configRemotes reads the remotes of a Git config file, in its order: the
// names and their URLs. Includes and insteadOf are not followed.
func configRemotes(path string) (names, urls []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	cur := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[':
			cur = ""
			sec := strings.TrimSpace(strings.Trim(line, "[]"))
			if rest, ok := strings.CutPrefix(sec, "remote "); ok {
				cur = strings.Trim(strings.TrimSpace(rest), `"`)
			}
		case cur != "":
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "url") {
				names, urls = append(names, cur), append(urls, strings.Trim(strings.TrimSpace(v), `"`))
			}
		}
	}
	return names, urls
}

// remoteURLs are the URLs of the remotes of the repo of the work tree at
// root, origin's first.
func remoteURLs(root string) []string {
	_, common, ok := gitTop(root)
	if !ok {
		return nil
	}
	names, urls := configRemotes(filepath.Join(common, "config"))
	if i := slices.Index(names, "origin"); i > 0 {
		urls = append([]string{urls[i]}, slices.Delete(urls, i, i+1)...)
	}
	return urls
}

// matchRemote is the name of the remote of the repo whose common dir is
// common whose URL is one of urls, "" for none.
func matchRemote(common string, urls []string) string {
	want := map[string]bool{}
	for _, u := range urls {
		want[repoKey(u)] = true
	}
	names, have := configRemotes(filepath.Join(common, "config"))
	for i, u := range have {
		if want[repoKey(u)] {
			return names[i]
		}
	}
	return ""
}

// workDirs are the directories the user works in on this machine, as far
// as GoRex knows: its sessions', live and those the journal recorded, and
// the cwd of each of Claude Code's projects.
func (s *Server) workDirs() []string {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		all = append(all, ss)
	}
	s.mu.Unlock()
	var dirs []string
	for _, ss := range all {
		dirs = append(dirs, ss.dir)
		if in := s.info(ss); in.Dir != "" {
			dirs = append(dirs, in.Dir)
		}
	}
	for _, e := range readJournal() {
		if e.Session != nil {
			dirs = append(dirs, e.Session.Dir)
		}
		for _, js := range e.Sessions {
			dirs = append(dirs, js.Dir)
		}
	}
	if config, err := s.claudeConfigDir(); err == nil {
		projects, _ := filepath.Glob(filepath.Join(config, "projects", "*"))
		for _, p := range projects {
			if d := projectCwd(p); d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	return dirs
}

// projectCwd is the cwd of a conversation of Claude Code's project at
// dir: the first line of one that has one.
func projectCwd(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) == 0 {
		return ""
	}
	f, err := os.Open(files[0])
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, maxLine)
	for sc.Scan() {
		var l struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Cwd != "" {
			return l.Cwd
		}
	}
	return ""
}

// findClone finds a clone of the repo of one of urls among the
// directories the user works in (workDirs), and the name of its remote;
// "" for none. A clone is the main work tree of its repo, not a
// teleport's.
func (s *Server) findClone(urls []string) (clone, remote string) {
	seen := map[string]bool{}
	for _, d := range s.workDirs() {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		top, common, ok := gitTop(d)
		if !ok || seen[common] {
			continue
		}
		seen[common] = true
		if r := matchRemote(common, urls); r != "" {
			if filepath.Base(common) == ".git" {
				top = filepath.Dir(common)
			}
			return top, r
		}
	}
	return "", ""
}

// cloneRepo clones the first of m's remotes where the source has it,
// relative to the home directory; else, when that is another directory,
// or the source's is outside its home, in the home directory under the
// repo's name, a number after it when taken.
func (s *Server) cloneRepo(m *TeleportManifest) (clone, remote string, err error) {
	home, err := s.homeDir()
	if err != nil {
		return "", "", err
	}
	if m.HomeRel != "" {
		clone = filepath.Join(home, filepath.FromSlash(m.HomeRel))
		// A clone there that GoRex did not know of.
		if top, common, ok := gitTop(clone); ok && samePath(top, clone) {
			if r := matchRemote(common, m.Remotes); r != "" {
				return clone, r, nil
			}
		}
		if exists(clone) {
			clone = ""
		}
	}
	if clone == "" {
		name := repoName(m.Remotes[0])
		clone = filepath.Join(home, name)
		for i := 2; exists(clone); i++ {
			clone = filepath.Join(home, fmt.Sprintf("%s-%d", name, i))
		}
	}
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		return "", "", err
	}
	if _, err := gitOut(filepath.Dir(clone), "clone", "-q", "--", m.Remotes[0], clone); err != nil {
		return "", "", err
	}
	return clone, "origin", nil
}
