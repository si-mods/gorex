package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// Files dropped on a pane of a host go there: each is sent into the
// directory of the session's program in the foreground, and its path there
// is pasted, as a drop on a pane of this machine pastes the paths here. A
// drop on a pane of this machine is the terminal's own.

// fileTransfer is a file on its way to a pane's machine, as the pane's
// transfer list shows it.
type fileTransfer struct {
	name       string
	size, done int64
	// ok tells that the file is there, and err why it is not.
	ok  bool
	err string
	// ended is when the list first showed the file there.
	ended  time.Time
	cancel context.CancelFunc
}

const (
	// transferEvery is how often at most a transfer's progress is shown.
	transferEvery = 150 * time.Millisecond
	// transferShown is how long a file there stays in the list.
	transferShown = 2 * time.Second
)

// upload sends the files dropped on p, a pane of a host, one after another,
// after those of the drops before, then pastes their paths there into it.
// A folder is not sent: its path here is pasted, as before.
func (a *App) upload(p *Pane, paths []string) {
	type item struct {
		path string
		x    *fileTransfer
		put  rex.FilePut
		ctx  context.Context
	}
	var items []item
	for _, path := range paths {
		x := &fileTransfer{name: filepath.Base(path)}
		p.transfers = append(p.transfers, x)
		fi, err := os.Stat(path)
		switch {
		case err != nil:
			// A file gone, or a broken link: nothing to paste.
			if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
				err = pe.Err
			}
			x.err = err.Error()
		case fi.IsDir():
			x.err = locales.PaneUploadFolder()
			items = append(items, item{path: path})
		default:
			x.size = fi.Size()
			it := item{path: path, x: x, put: rex.FilePut{SID: p.SID, Name: x.name, Size: x.size, ID: uploadID(p.SID, path, fi), Conflict: "rename"}}
			it.ctx, x.cancel = context.WithCancel(context.Background())
			items = append(items, it)
		}
	}
	h, offline, shell := p.host, locales.HostsNotConnectedTo(p.host.Name), p.info.Shell
	update := a.updater()
	prev, done := p.uploading, make(chan struct{})
	p.uploading = done
	go func() {
		defer close(done)
		if prev != nil {
			<-prev
		}
		var pasted []string
		for _, it := range items {
			if it.x == nil {
				pasted = append(pasted, it.path)
				continue
			}
			c := h.current()
			if c == nil {
				it.x.cancel()
				update(func() { it.x.err = offline })
				continue
			}
			if path, err := putFile(it.ctx, c, it.put, it.path, it.x, update); err == nil {
				pasted = append(pasted, path)
			}
		}
		update(func() {
			if len(pasted) > 0 && !p.closed && p.term != nil {
				p.term.Paste(pastedPaths(shell, pasted))
			}
		})
	}()
}

// putFile sends the file at path as f says, x telling how it goes, and
// returns its path there. Cancelled under way, PutFile removes what the
// host has of the file.
func putFile(ctx context.Context, c *rex.Client, f rex.FilePut, path string, x *fileTransfer, update func(func())) (string, error) {
	defer x.cancel()
	var there string
	err := ctx.Err()
	if err == nil {
		var src *os.File
		if src, err = os.Open(path); err == nil {
			var last time.Time
			there, _, err = c.PutFile(ctx, f, src, func(done int64) {
				if now := time.Now(); now.Sub(last) >= transferEvery {
					last = now
					update(func() { x.done = done })
				}
			})
			src.Close()
		}
		if err != nil && ctx.Err() != nil {
			err = ctx.Err()
		}
	}
	update(func() {
		if err == nil {
			x.done, x.ok = x.size, true
		} else {
			x.err = strings.TrimPrefix(err.Error(), "rex: ")
		}
	})
	return there, err
}

// uploadID is the transfer id of an upload of the file at path, as fi
// tells of it, into the session sid: the same for each try of the same
// file, which goes on from what the last left there.
func uploadID(sid, path string, fi os.FileInfo) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", sid, path, fi.Size(), fi.ModTime().UnixNano()))
	return hex.EncodeToString(sum[:8])
}

// stopUploads cancels the pane's uploads, as it closes.
func (p *Pane) stopUploads() {
	for _, x := range p.transfers {
		if x.cancel != nil {
			x.cancel()
		}
	}
}

// pastedPaths is paths as a drop pastes them into a pane whose shell is at
// shell, a path of its machine: each a word of the shell, spaces between.
func pastedPaths(shell string, paths []string) string {
	q := quoterOf(shell[strings.LastIndexAny(shell, `/\`)+1:])
	if q == nil {
		q = posixWord
	}
	words := make([]string, len(paths))
	for i, path := range paths {
		w, ok := q(path)
		if !ok {
			w = path
		}
		words[i] = w
	}
	return strings.Join(words, " ")
}

// byteSize is n bytes as Finder writes them: 1000 to the kB.
func byteSize(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	f, i := float64(n)/1000, 0
	for f >= 1000 && i < 4 {
		f /= 1000
		i++
	}
	return fmt.Sprintf("%.1f %s", f, []string{"kB", "MB", "GB", "TB", "PB"}[i])
}

// uploads tells whether files dropped on p go to its host: a host
// connected now that takes them.
func uploads(p *Pane) bool {
	h := p.host
	return h != nil && h.current() != nil && h.hello.Can(rex.CapabilityFileTransfer)
}

// fileDrops are what the card of a pane of a host takes: files, copied.
var fileDrops = transfer.DropOptions{Formats: []transfer.Format{transfer.FileList}, Operations: transfer.Copy}

// fileDrop makes card, of p, a pane of a host that takes files, take those
// dropped on it, the terminal's included, and show where they go while
// they are dragged over. Over one that does not take them, it says why
// while the terminal, tv, takes them as before.
func (a *App) fileDrop(c *ui.Context, k *colors, p *Pane, card, tv ui.Element) {
	var label string
	switch h := p.host; {
	case uploads(p):
		// The card is an ancestor of all the pane shows, and the native
		// drag asks for its data targets before the terminal's files.
		if d, ok := ui.DropData(card, fileDrops); ok {
			if paths, err := d.Data.Files(); err == nil && len(paths) > 0 {
				a.upload(p, paths)
			}
		}
		if _, over := ui.DataDragOver(card, fileDrops); over {
			label = locales.PaneDropUpload(cmp.Or(p.info.Dir, "~"), h.Name)
			drawPaneDrop(card, k, dropCenter)
		}
	case h != nil && h.current() != nil && tv.FileDragOver():
		label = locales.PaneDropUpdate(h.Name)
	}
	if label == "" {
		return
	}
	// What it says lets the drag through, to the terminal under it.
	ui.Column(c.Key("file-drop")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Justify(ui.Center).AlignItems(ui.Center).PassThrough().Children(func() {
		const r = 12
		frostPanel(ui.Row(c).Padding(8, 14).Radius(r).Border(1, k.panelBorder).Shadow(0, 8, 24, 0, k.shadowFocused).MaxWidth(420).PassThrough(), k, r).
			Children(func() {
				ui.Text(c, label).FontSize(12.5).TextColor(k.text).PassThrough()
			})
	})
}

// transfers lists p's transfers in its bottom-right corner: a file there
// for transferShown, one that failed until clicked.
func (a *App) transfers(c *ui.Context, k *colors, p *Pane) {
	now := c.Now()
	p.transfers = slices.DeleteFunc(p.transfers, func(x *fileTransfer) bool {
		if !x.ok {
			return false
		}
		if x.ended.IsZero() {
			x.ended = now
		}
		left := transferShown - now.Sub(x.ended)
		if left > 0 {
			c.After(left)
		}
		return left <= 0
	})
	if len(p.transfers) == 0 {
		return
	}
	// The list sits in the body's padding, which is inside the card's
	// border, of a tiled pane: its corner is concentric with the card's.
	const inset, pad = 6, 4
	edge := float32(inset)
	if p.float == nil {
		edge += 1
	}
	r := concentric(cardR, edge)
	list := ui.Column(c.Key("transfers")).Absolute().Right(inset).Bottom(inset).Width(260).Padding(pad).Gap(2).
		Radius(r).Border(1, k.panelBorder).Shadow(0, 8, 24, 0, k.shadowFocused)
	frostPanel(list, k, r)
	anim := motion(c)
	if anim != nil {
		list.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: bannerMotion, Exit: bannerMotion})
	}
	// A row clicked away goes once the list is built.
	var gone *fileTransfer
	defer func() {
		p.transfers = slices.DeleteFunc(p.transfers, func(x *fileTransfer) bool { return x == gone })
	}()
	list.Children(func() {
		for _, x := range p.transfers {
			row := ui.Column(c.Key(x)).Padding(6, 8).Gap(4).Radius(concentric(r, pad))
			if anim != nil {
				row.Transition(ui.ElementTransition{Animation: anim, Position: true, Size: true, Enter: bannerMotion, Exit: bannerMotion})
			}
			failed := x.err != ""
			if failed {
				row.Cursor(ui.CursorPointer)
				if row.Hovered() {
					row.Background(k.hover)
				}
				if row.Clicked() {
					gone = x
				}
			}
			row.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, x.name).FontSize(12.5).TextColor(k.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					if x.cancel != nil && !x.ok && !failed &&
						iconButton(c, k, "x", locales.PaneTransferCancel(), 18, 11).Clicked() {
						x.cancel()
						gone = x
					}
				})
				if !failed && !x.ok {
					ui.Box(c).Height(3).Radius(1.5).Background(k.track).Draw(func(pt *ui.Painter, b ui.Rect) {
						if x.size > 0 {
							b.W *= float32(x.done) / float32(x.size)
						}
						pt.Fill(b, k.accent, 1.5)
					})
				}
				text, col := locales.PaneTransferProgress(byteSize(x.done), byteSize(x.size)), k.textFaint
				switch {
				case failed:
					text, col = x.err, k.danger
				case x.ok:
					text = locales.PaneTransferDone()
				}
				ui.Text(c, text).FontSize(11.5).TextColor(col).LineHeight(1.3)
			})
		}
	})
}
