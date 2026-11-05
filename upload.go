package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
// What is not sent, a folder, pastes its path here, as before.
func (a *App) upload(p *Pane, paths []string) {
	type item struct {
		path string
		x    *fileTransfer
		ctx  context.Context
	}
	items := make([]item, 0, len(paths))
	for _, path := range paths {
		it := item{path: path}
		x := &fileTransfer{name: filepath.Base(path)}
		fi, err := os.Stat(path)
		switch {
		case err != nil:
			x.err = err.Error()
		case fi.IsDir():
			x.err = locales.PaneUploadFolder()
		default:
			x.size = fi.Size()
			it.x = x
			it.ctx, x.cancel = context.WithCancel(context.Background())
		}
		p.transfers = append(p.transfers, x)
		items = append(items, it)
	}
	h, sid, shell := p.host, p.SID, p.info.Shell
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
			if path, err := putFile(it.ctx, h, sid, it.path, it.x, update); err == nil {
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

// putFile sends the file at path into the directory of the session sid's
// program in the foreground on host h, x telling how it goes, and returns
// its path there.
func putFile(ctx context.Context, h *hostConn, sid, path string, x *fileTransfer, update func(func())) (string, error) {
	defer x.cancel()
	var err error
	var there string
	c := h.current()
	if c == nil {
		err = errors.New(locales.HostsNotConnectedTo(h.Name))
	} else if ctx.Err() == nil {
		var f *os.File
		if f, err = os.Open(path); err == nil {
			var last time.Time
			there, _, err = c.PutFile(ctx, rex.FilePut{SID: sid, Name: x.name, Size: x.size, ID: transferID(), Conflict: "rename"}, f, func(done int64) {
				if now := time.Now(); now.Sub(last) >= transferEvery {
					last = now
					update(func() { x.done = done })
				}
			})
			f.Close()
		}
	}
	err = cmp.Or(err, ctx.Err())
	update(func() {
		if err == nil {
			x.done, x.ok = x.size, true
		} else {
			x.err = strings.TrimPrefix(err.Error(), "rex: ")
		}
	})
	return there, err
}

// transferID is a new id of a transfer.
func transferID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
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

// fileDrop takes the files dragged over p, a pane of a host that takes
// them, with where they will go shown over it; over one that does not, it
// says why, and the terminal takes them as before.
func (a *App) fileDrop(c *ui.Context, k *colors, p *Pane, tv ui.Element) {
	over := p.host != nil && (tv.FileDragOver() || p.dropOver)
	p.dropOver = false
	if !over {
		return
	}
	// The box over the terminal takes the drop once it shows: the
	// terminal is under it.
	box := ui.Column(c.Key("file-drop")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Justify(ui.Center).AlignItems(ui.Center)
	label := locales.PaneDropUpdate(p.host.Name)
	if p.host.hello.Can(rex.CapabilityFileTransfer) {
		label = locales.PaneDropUpload(cmp.Or(p.info.Dir, "~"), p.host.Name)
		p.dropOver = tv.FileDragOver() || box.FileDragOver()
		if files := box.DroppedFiles(); len(files) > 0 {
			p.dropOver = false
			a.upload(p, files)
		}
	} else {
		box.PassThrough()
	}
	// The label lets a drop on it through, to the box or the terminal.
	box.Children(func() {
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
	const inset, pad = 6, 4
	r := concentric(cardR, inset)
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
