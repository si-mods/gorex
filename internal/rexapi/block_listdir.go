package rexapi

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorex/internal/rex"
)

const (
	defaultListDirLimit = 2000
	maxListDirLimit     = 10000
	maxConcurrentLists  = 16
	listDirBatchSize    = 256
	listDirTimeout      = 10 * time.Second
)

var listDirSem = make(chan struct{}, maxConcurrentLists)

// dirEntry is one entry of com.superlogical.terminal.list_dir.
type dirEntry struct {
	Name    string `json:"name"`
	Symlink bool   `json:"symlink,omitempty"`
	File    bool   `json:"file,omitempty"`
}

// blockListDir lists a directory the terminal's program would see.
func blockListDir(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	path, _, aerr := a.str("path")
	if aerr != nil {
		return nil, aerr
	}
	prefix, _, aerr := a.str("prefix")
	if aerr != nil {
		return nil, aerr
	}
	limit, set, aerr := a.int("limit")
	if aerr != nil {
		return nil, aerr
	}
	if !set || limit <= 0 {
		limit = defaultListDirLimit
	}
	limit = min(limit, maxListDirLimit)
	files, _, aerr := a.boolean("files")
	if aerr != nil {
		return nil, aerr
	}
	info, aerr := c.g.blockInfo(b)
	if aerr != nil {
		return nil, aerr
	}
	home, _ := os.UserHomeDir()
	dir, status := resolveDir(path, info.Dir, home)
	out := map[string]any{
		"path": dir, "home": home, "status": status,
		"entries": []dirEntry{}, "truncated": false,
	}
	if status != "ok" {
		return out, nil
	}
	deadline := time.Now().Add(listDirTimeout)
	timer := time.NewTimer(listDirTimeout)
	select {
	case listDirSem <- struct{}{}:
		timer.Stop()
	case <-timer.C:
		return nil, errInternal("too many directory listings under way; try again later")
	}
	defer func() { <-listDirSem }()

	f, err := os.Open(dir)
	if err != nil {
		out["status"] = dirStatus(err)
		return out, nil
	}
	defer f.Close()

	lower := strings.ToLower(prefix)
	kept := []dirEntry{}
	truncated := false
	firstBatch := true
	for !truncated {
		if time.Now().After(deadline) {
			truncated = true
			break
		}
		entries, err := f.ReadDir(listDirBatchSize)
		if err != nil && !errors.Is(err, io.EOF) && firstBatch && len(entries) == 0 {
			out["status"] = dirStatus(err)
			return out, nil
		}
		firstBatch = false
		for _, e := range entries {
			if time.Now().After(deadline) {
				truncated = true
				break
			}
			if lower != "" && !strings.HasPrefix(strings.ToLower(e.Name()), lower) {
				continue
			}
			link := e.Type()&os.ModeSymlink != 0
			entryPath := filepath.Join(dir, e.Name())
			entry := dirEntry{Name: e.Name(), Symlink: link}
			isDir := e.IsDir()
			if link {
				if st, err := os.Stat(entryPath); err == nil {
					isDir = st.IsDir()
				} else {
					// A link that leads nowhere is left out.
					continue
				}
			}
			if files {
				entry.File = !isDir
			} else if !isDir {
				continue
			}
			if len(kept) >= limit {
				truncated = true
				break
			}
			kept = append(kept, entry)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				truncated = true
			}
			break
		}
	}
	out["entries"] = kept
	out["truncated"] = truncated
	return out, nil
}

// resolveDir resolves a list_dir path the way a program in the terminal
// would, and reports it with whether it can be read.
func resolveDir(path, cwd, home string) (string, string) {
	if path == "" {
		if cwd == "" {
			cwd = home
		}
		return cwd, "ok"
	}
	if path == "~" {
		if home == "" {
			return "~", "missing"
		}
		path = home
	} else if rest, ok := strings.CutPrefix(path, "~"); ok && (rest[0] == '/' || rest[0] == filepath.Separator) {
		if home == "" {
			return filepath.Clean(path), "missing"
		}
		path = filepath.Join(home, rest[1:])
	}
	norm, ok := rex.VolumePath(path)
	if !ok {
		// A device, a pipe, or a path rooted with no volume: reading it
		// may wait on it or contact another host, so nothing here reads
		// it, and it names no directory.
		return filepath.Clean(path), "missing"
	}
	if filepath.IsAbs(norm) {
		return norm, "ok"
	}
	if cwd == "" {
		return filepath.Clean(norm), "ok"
	}
	return filepath.Join(cwd, norm), "ok"
}

// dirStatus is what list_dir reports for a directory it could not read.
func dirStatus(err error) string {
	switch {
	case os.IsNotExist(err):
		return "missing"
	case os.IsPermission(err):
		return "denied"
	case strings.Contains(err.Error(), "not a directory"):
		return "not_dir"
	}
	return "denied"
}
