package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
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

// Files dropped on a pane of a host go there: each is sent into a
// temporary folder of the host's, not to litter the directory the pane
// works in (into that directory, of a host before CapabilityFileTemp),
// and its path there is pasted, as a drop on a pane of this
// machine pastes the paths here. A drop on a pane of this machine is the
// terminal's own. A file path
// Command-clicked in a pane of a host comes here, into Downloads.

// fileTransfer is a file on its way to or from a pane's machine, as the
// pane's transfer list shows it.
type fileTransfer struct {
	name string
	// from is the path there of a download, absolute when the pane's
	// directory is known, "" for an upload.
	from       string
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
	temp := h.hello.Can(rex.CapabilityFileTemp)
	update := a.paneUpdater(p)
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
			it.put.Temp = temp
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
			if _, q := a.paneNow(p); len(pasted) > 0 && !q.closed && q.term != nil {
				q.term.Paste(pastedPaths(shell, pasted))
			}
		})
	}()
}

// uploadFile is a file an upload reads.
type uploadFile interface {
	io.ReaderAt
	io.Closer
}

// openUpload opens the file at path to send it.
var openUpload = func(path string) (uploadFile, error) { return os.Open(path) }

// putFile sends the file at path as f says, x telling how it goes, and
// returns its path there. Cancelled under way, PutFile removes what the
// host has of the file.
func putFile(ctx context.Context, c *rex.Client, f rex.FilePut, path string, x *fileTransfer, update func(func())) (string, error) {
	defer x.cancel()
	var there string
	err := ctx.Err()
	if err == nil {
		var src uploadFile
		if src, err = openUpload(path); err == nil {
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

// downloadsDir is where a file Command-clicked in a pane of a host goes.
var downloadsDir = func() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Downloads"), err
}

// download takes the file at link, a path printed in p, a pane of a host
// that transfers files, or a file:// hyperlink, into the Downloads folder
// here, quarantined as a browser's download, then opens it as a
// Command-click on a pane of this machine opens a file. A path with
// :line:col is the file's; a relative one is of the pane's directory
// there, and one of "~/" of the home there. It reports whether it took
// the link: a path, not another URL, of a host that transfers files. A
// click on a file being downloaded does nothing more.
func (a *App) download(p *Pane, link string) bool {
	path, ok := linkPath(link)
	if u, err := url.Parse(link); err == nil && u.Scheme == "file" {
		// A Windows host's is file:///C:/dir/name.
		path, ok = u.Path, u.Path != ""
		if len(path) > 2 && path[2] == ':' {
			path = path[1:]
		}
	}
	if !ok || !transfersFiles(p) {
		return false
	}
	path = hostPath(p.info.Dir, path)
	for _, x := range p.transfers {
		if x.from == path && !x.ok && x.err == "" {
			return true
		}
	}
	name := strings.TrimRight(path, `/\`)
	x := &fileTransfer{name: name[strings.LastIndexAny(name, `/\`)+1:], from: path}
	p.transfers = append(p.transfers, x)
	dir, err := downloadsDir()
	if err != nil {
		x.err = err.Error()
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	x.cancel = cancel
	c, sid, update := p.host.current(), p.SID, a.paneUpdater(p)
	go func() {
		defer cancel()
		var last time.Time
		got, err := c.DownloadFile(ctx, sid, path, dir, "rename", func(done, size int64) {
			if now := time.Now(); now.Sub(last) >= transferEvery {
				last = now
				update(func() { x.done, x.size = done, size })
			}
		})
		if err == nil {
			err = quarantine(got)
		}
		update(func() {
			switch {
			case rex.Folder(err):
				x.err = locales.PaneDownloadFolder()
			case err != nil:
				x.err = strings.TrimPrefix(err.Error(), "rex: ")
			default:
				x.done, x.ok = x.size, true
				a.services.OpenURL(fileURL(got))
			}
		})
	}()
	return true
}

// hostPath is path, printed in a pane of a host whose directory there is
// dir, made absolute against dir when it is relative and dir is known.
func hostPath(dir, path string) string {
	if dir == "" || path == "" || strings.ContainsRune(`~/\`, rune(path[0])) || len(path) > 1 && path[1] == ':' {
		return path
	}
	sep := "/"
	if !strings.HasPrefix(dir, "/") {
		sep = `\` // a Windows host's
	}
	return strings.TrimRight(dir, `/\`) + sep + path
}

// uploadID is the transfer id of an upload of the file at path, as fi
// tells of it, into the session sid: the same for each try of the same
// file, which goes on from what the last left there.
func uploadID(sid, path string, fi os.FileInfo) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", sid, path, fi.Size(), fi.ModTime().UnixNano()))
	return hex.EncodeToString(sum[:8])
}

// paneNow returns p and a, its window, or, p having moved to another
// window, the pane that shows its session there and that window.
func (a *App) paneNow(p *Pane) (*App, *Pane) {
	if p.closed && a.st != nil && p.host != nil {
		if n, ok := a.st.needyOf(p.host.ID, p.SID); ok && n.pane != nil {
			return n.app, n.pane
		}
	}
	return a, p
}

// paneUpdater is a.updater for p's transfers: each change draws the window
// that shows p's session now.
func (a *App) paneUpdater(p *Pane) func(func()) {
	update := a.updater()
	return func(fn func()) {
		update(func() {
			fn()
			if o, _ := a.paneNow(p); o != a && o.win != nil {
				o.win.Update(func() {})
			}
		})
	}
}

// handTransfers gives p's transfers to the pane that shows its session
// now, p having moved to another window.
func (a *App) handTransfers(p *Pane) {
	if _, q := a.paneNow(p); q != p {
		q.transfers, q.uploading = p.transfers, p.uploading
	}
}

// stopTransfers cancels the pane's transfers, as it closes.
func (p *Pane) stopTransfers() {
	for _, x := range p.transfers {
		if x.cancel != nil {
			x.cancel()
		}
	}
}

// pastedPaths is paths as a drop pastes them into a pane whose shell is at
// shell, a path of its machine: each a word of the shell, spaces between.
func pastedPaths(shell string, paths []string) string {
	words := make([]string, len(paths))
	for i, path := range paths {
		words[i] = rex.PathWord(shell, path)
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

// transfersFiles tells whether p's host takes files dropped on p and gives
// those Command-clicked in it: a host connected now that transfers them.
func transfersFiles(p *Pane) bool {
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
	case transfersFiles(p):
		// The card is an ancestor of all the pane shows, and the native
		// drag asks for its data targets before the terminal's files.
		if d, ok := ui.DropData(card, fileDrops); ok {
			if paths, err := d.Data.Files(); err == nil && len(paths) > 0 {
				a.upload(p, paths)
			}
		}
		if _, over := ui.DataDragOver(card, fileDrops); over {
			label = locales.PaneDropUpload(cmp.Or(p.info.Dir, "~"), h.Name)
			if h.hello.Can(rex.CapabilityFileTemp) {
				label = locales.PaneDropUploadTemp(h.Name)
			}
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
						// A download's size is not known before it begins.
						if x.size > 0 {
							b.W *= float32(x.done) / float32(x.size)
							pt.Fill(b, k.accent, 1.5)
						}
					})
				}
				text, col := locales.PaneTransferProgress(byteSize(x.done), byteSize(x.size)), k.textFaint
				switch {
				case failed:
					text, col = x.err, k.danger
				case x.ok:
					text = locales.PaneTransferDone()
				case x.size == 0:
					text = "…"
				}
				ui.Text(c, text).FontSize(11.5).TextColor(col).LineHeight(1.3)
			})
		}
	})
}
