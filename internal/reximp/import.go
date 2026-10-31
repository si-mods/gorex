// Package reximp imports a Rex server's sessions into GoRex: a Rex session
// becomes a space, a window a tab, and a block a pane, which mirrors the
// Rex block (or runs a shell in its directory).
package reximp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gorex/internal/rex"
	"gorex/internal/rexpeer"
)

// Options tell what to import and how.
type Options struct {
	// Sessions are the Rex sessions to import, by label or ID; empty means
	// every session.
	Sessions []string
	// Shell runs a shell in each block's directory instead of mirroring
	// the block.
	Shell bool
	// Mirror is the command that mirrors a block, its own arguments last:
	// --session, the session's ID, --block, the block's ID, --label, the
	// block's label.
	Mirror []string
	// Resize makes a mirror give its Rex block the pane's size.
	Resize bool
	// StatePath is where the imported sessions are recorded, so that a
	// second run does not import them again. Empty records none.
	StatePath string
	// RexSocket names the server, for the record.
	RexSocket string
}

// Result is what a run imported and left.
type Result struct {
	Imported []Imported `json:"imported"`
	Skipped  []Skipped  `json:"skipped"`
}

// Imported is one session made in GoRex.
type Imported struct {
	Session string `json:"session"`
	Label   string `json:"label"`
	Space   string `json:"space"`
	Windows int    `json:"windows"`
	Blocks  int    `json:"blocks"`
}

// Skipped is one session left as it was.
type Skipped struct {
	Session string `json:"session"`
	Label   string `json:"label"`
	Why     string `json:"why"`
}

// state is what a run records: the Rex session of each space it made.
type state struct {
	Rex      string                  `json:"rex,omitempty"`
	Sessions map[string]stateSession `json:"sessions"`
}

type stateSession struct {
	Label string `json:"label,omitempty"`
	Space string `json:"space,omitempty"`
}

// Run imports the sessions into GoRex, through its server's layout
// commands, as the Rex client peer sees them. The GoRex window must be
// open: it makes the spaces, tabs and panes.
func Run(ctx context.Context, gorexClient *rex.Client, peer *rexpeer.Client, opt Options) (Result, error) {
	st := loadState(opt.StatePath)
	st.Rex = opt.RexSocket
	sessions, err := peer.Sessions(ctx)
	if err != nil {
		return Result{}, err
	}
	wanted := selectSessions(sessions, opt.Sessions)
	var res Result
	for _, s := range wanted {
		if prev, ok := st.Sessions[s.SessionID]; ok && prev.Space != "" {
			res.Skipped = append(res.Skipped, Skipped{s.SessionID, s.Label, "already imported"})
			continue
		}
		view, err := peer.Attach(ctx, s.SessionID)
		if err != nil {
			return res, fmt.Errorf("%s: %w", s.Label, err)
		}
		if len(view.Windows) == 0 {
			res.Skipped = append(res.Skipped, Skipped{s.SessionID, s.Label, "it has no windows"})
			continue
		}
		imp, err := importSession(ctx, gorexClient, peer, s, view, opt)
		if err != nil {
			return res, fmt.Errorf("%s: %w", s.Label, err)
		}
		res.Imported = append(res.Imported, imp)
		st.Sessions[s.SessionID] = stateSession{Label: s.Label, Space: imp.Space}
		saveState(opt.StatePath, st)
	}
	return res, nil
}

// importSession makes one Rex session's space, tabs and panes.
func importSession(ctx context.Context, g *rex.Client, peer *rexpeer.Client, s rexpeer.Session, view rexpeer.View, opt Options) (Imported, error) {
	space, err := command(g, rex.LayoutCommand{Op: "space.create", Name: s.Label})
	if err != nil {
		return Imported{}, err
	}
	spaceID := space.Space
	if spaceID == "" {
		return Imported{}, fmt.Errorf("GoRex made no space")
	}
	out := Imported{Session: s.SessionID, Label: s.Label, Space: spaceID}
	fail := func(err error) (Imported, error) {
		_, _ = command(g, rex.LayoutCommand{Op: "space.close", Space: spaceID})
		return Imported{}, err
	}
	for _, w := range view.Windows {
		layer := tiledLayer(w)
		if layer == nil {
			continue
		}
		blocks := map[string]rexpeer.Block{}
		for _, b := range layer.Blocks {
			blocks[b.BlockID] = b
		}
		leaves := leaves(layer.Layout)
		if len(leaves) == 0 {
			continue
		}
		focus := w.WindowID == view.ActiveWindowID
		var prev string
		for i, leaf := range leaves {
			b := blocks[leaf.block]
			if b.BlockID == "" {
				continue
			}
			cmd, dir, err := paneCommand(ctx, peer, s.SessionID, b, opt)
			if err != nil {
				return fail(err)
			}
			if i == 0 {
				res, err := command(g, rex.LayoutCommand{
					Op: "tab.create", Space: spaceID, Name: tabName(w),
					Dir: dir, Command: cmd, Focus: focus,
				})
				if err != nil {
					return fail(err)
				}
				prev = res.New
			} else {
				vertical, _ := splitOf(leaves[i-1], leaf)
				res, err := command(g, rex.LayoutCommand{
					Op: "pane.split", Space: spaceID, SID: prev,
					Vertical: vertical, Dir: dir, Command: cmd,
				})
				if err != nil {
					return fail(err)
				}
				prev = res.New
			}
			if prev == "" {
				return fail(fmt.Errorf("GoRex made no pane"))
			}
			if namedBlock(b.Label) {
				if _, err := command(g, rex.LayoutCommand{Op: "pane.rename", Space: spaceID, SID: prev, Name: b.Label}); err != nil {
					return fail(err)
				}
			}
			out.Blocks++
		}
		out.Windows++
	}
	return out, nil
}

// tabName is the name a tab keeps of a Rex window: its label, unless that
// is Rex's own, made from the window's ID, which says nothing; a tab
// without one follows its block's title, as Rex shows it.
func tabName(w rexpeer.Window) string {
	if w.Label == "" || defaultWindowLabel(w.Label, w.WindowID) {
		return ""
	}
	return w.Label
}

// defaultWindowLabel tells whether a label is the one Rex makes for a
// window, "Window 89abcdef" of its ID's last eight characters.
func defaultWindowLabel(label, windowID string) bool {
	suffix := windowID
	if i := strings.LastIndexByte(suffix, ':'); i >= 0 {
		suffix = suffix[i+1:]
	}
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return strings.EqualFold(strings.TrimSpace(label), "Window "+suffix)
}

// namedBlock tells whether a block's label is one a person gave it: Rex
// calls a terminal it has no name for "shell".
func namedBlock(label string) bool {
	label = strings.TrimSpace(label)
	return label != "" && !strings.EqualFold(label, "shell")
}

// paneCommand is what a new pane runs: the mirror, or the login shell.
func paneCommand(ctx context.Context, peer *rexpeer.Client, session string, b rexpeer.Block, opt Options) (cmd []string, dir string, err error) {
	if opt.Shell {
		return nil, peer.Pwd(ctx, session, b.BlockID), nil
	}
	cmd = append(cmd, opt.Mirror...)
	cmd = append(cmd, "--session", session, "--block", b.BlockID)
	if b.Label != "" {
		cmd = append(cmd, "--label", b.Label)
	}
	if opt.Resize {
		cmd = append(cmd, "--resize")
	}
	return cmd, "", nil
}

// command runs a layout command, failing with what GoRex answered.
func command(g *rex.Client, cmd rex.LayoutCommand) (*rex.LayoutCommand, error) {
	res, err := g.Command(cmd)
	if err != nil {
		return nil, err
	}
	if res.Code != "" {
		return nil, fmt.Errorf("%s: %s", res.Code, res.Error)
	}
	return &res, nil
}

// tiledLayer returns a window's tiled layer.
func tiledLayer(w rexpeer.Window) *rexpeer.Layer {
	for i := range w.Layers {
		if w.Layers[i].Kind == "tiled" {
			return &w.Layers[i]
		}
	}
	return nil
}

// leaf is one block of a layout, and where it sits.
type leaf struct {
	block string
	path  []string
}

// leaves returns a layout's blocks in layout order.
func leaves(l *rexpeer.Layout) []leaf {
	var out []leaf
	var walk func(*rexpeer.Layout, []string)
	walk = func(n *rexpeer.Layout, path []string) {
		if n == nil {
			return
		}
		if n.BlockID != "" {
			out = append(out, leaf{n.BlockID, path})
			return
		}
		if n.Split == nil {
			return
		}
		d := "h"
		if n.Split.Direction == "vertical" {
			d = "v"
		}
		walk(n.Split.Before, append(append([]string(nil), path...), d, "a"))
		walk(n.Split.After, append(append([]string(nil), path...), d, "b"))
	}
	walk(l, nil)
	return out
}

// splitOf returns the direction of the split between two leaves, which is
// their lowest common ancestor's, and whether the second is below it.
func splitOf(a, b leaf) (vertical, after bool) {
	i := 0
	for i < len(a.path) && i < len(b.path) && a.path[i] == b.path[i] {
		i++
	}
	if i >= len(a.path) || i >= len(b.path) {
		return false, true
	}
	return a.path[i] == "v", b.path[i] == "b"
}

// selectSessions returns the sessions wanted, by ID or label, ignoring
// case; all when none are named.
func selectSessions(all []rexpeer.Session, want []string) []rexpeer.Session {
	if len(want) == 0 {
		return all
	}
	var out []rexpeer.Session
	for _, s := range all {
		for _, q := range want {
			if s.SessionID == q || strings.EqualFold(s.Label, q) || strings.HasSuffix(s.SessionID, q) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// loadState reads what earlier runs imported.
func loadState(path string) state {
	st := state{Sessions: map[string]stateSession{}}
	if path == "" {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	if st.Sessions == nil {
		st.Sessions = map[string]stateSession{}
	}
	return st
}

// saveState records what this run imported.
func saveState(path string, st state) {
	if path == "" {
		return
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(b, '\n'), 0o600)
}

// SortedSessions returns sessions by label, for messages.
func SortedSessions(s []rexpeer.Session) []rexpeer.Session {
	out := append([]rexpeer.Session(nil), s...)
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
