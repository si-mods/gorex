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
	"log"
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
// and its path there is pasted, a space after each, as a drop on a pane
// of this machine pastes the paths here. A drop on a pane of this machine
// is the terminal's own. A file path Command-clicked in a pane of a host
// comes here, into Downloads.

// fileTransfer is a file on its way to or from a pane's machine, as the
// pane's transfer list shows it.
type fileTransfer struct {
	name string
	// from is the path there of a download, absolute when the pane's
	// directory is known, "" for an upload.
	from       string
	size, done int64
	// ok tells that the file is there, and err why it is not; note says
	// what else of one there is to know, shown in place of Done.
	ok        bool
	err, note string
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

// sessionTransfers are the files on their way to or from a session of a
// host, as the transfer list of a pane that shows it lists them: they
// are the session's, not a pane's, and go on as its pane moves to
// another window or its window closes. uploading closes once the last
// drop's are sent; ctx is done once a pane of the session closes for
// good, which stops them.
type sessionTransfers struct {
	list      []*fileTransfer
	uploading chan struct{}
	ctx       context.Context
	stop      context.CancelFunc
}

// transfersOf returns the transfers of session sid of the host, made when
// it has none. Call it on the main thread.
func (h *hostConn) transfersOf(sid string) *sessionTransfers {
	ts := h.transfers[sid]
	if ts == nil {
		if h.transfers == nil {
			h.transfers = map[string]*sessionTransfers{}
		}
		ts = &sessionTransfers{}
		ts.ctx, ts.stop = context.WithCancel(context.Background())
		h.transfers[sid] = ts
	}
	return ts
}

// stopTransfers stops the transfers of session sid of the host, as a pane
// of it closes for good: what they would paste is not pasted.
func (h *hostConn) stopTransfers(sid string) {
	if ts := h.transfers[sid]; ts != nil {
		ts.stop()
		delete(h.transfers, sid)
	}
}

// upload sends the files dropped on p, a pane of a host, one after another,
// after those of the drops before, then pastes their paths there into its
// session, through the host's server: whichever pane shows it then, in
// whichever window, shows them. A folder is not sent: its path here is
// pasted, as before.
func (a *App) upload(p *Pane, paths []string) {
	h, sid := p.host, p.SID
	ts := h.transfersOf(sid)
	type item struct {
		path string
		x    *fileTransfer
		put  rex.FilePut
		ctx  context.Context
	}
	var items []item
	for _, path := range paths {
		x := &fileTransfer{name: filepath.Base(path)}
		ts.list = append(ts.list, x)
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
			it := item{path: path, x: x, put: rex.FilePut{SID: sid, Name: x.name, Size: x.size, ID: uploadID(sid, path, fi), Conflict: "rename"}}
			it.ctx, x.cancel = context.WithCancel(ts.ctx)
			items = append(items, it)
		}
	}
	offline, shell := locales.HostsNotConnectedTo(h.Name), p.info.Shell
	temp := h.hello.Can(rex.CapabilityFileTemp)
	update := h.update
	prev, done := ts.uploading, make(chan struct{})
	ts.uploading = done
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
			if path, word, err := putFile(it.ctx, c, shell, it.put, it.path, it.x, update); err == nil && word != "" {
				pasted = append(pasted, path)
			}
		}
		if c := h.current(); c != nil && len(pasted) > 0 && ts.ctx.Err() == nil {
			if err := c.Paste(sid, pastedPaths(shell, pasted)); err != nil {
				log.Printf("upload: pasting into %s: %v", sid, err)
			}
		}
	}()
}

// uploadFile is a file an upload reads.
type uploadFile interface {
	io.ReaderAt
	io.Closer
}

// openUpload opens the file at path to send it.
var openUpload = func(path string) (uploadFile, error) { return os.Open(path) }

// putFile sends the file at path as f says, for a pane whose shell is at
// shell (PutForShell), x telling how it goes, and returns its path there
// and that path as a word of the shell, "" when not to be pasted.
// Cancelled under way, PutFile removes what the host has of the file.
func putFile(ctx context.Context, c *rex.Client, shell string, f rex.FilePut, path string, x *fileTransfer, update func(func())) (there, word string, err error) {
	defer x.cancel()
	err = ctx.Err()
	if err == nil {
		var src uploadFile
		if src, err = openUpload(path); err == nil {
			var last time.Time
			there, word, err = c.PutForShell(ctx, shell, f, src, func(done int64) {
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
		switch {
		case err != nil:
			x.err = strings.TrimPrefix(err.Error(), "rex: ")
		case word == "":
			// There, in a folder or by a name the shell refuses: not typed.
			x.done, x.ok, x.note = x.size, true, locales.PaneUploadNotPasted()
		default:
			x.done, x.ok = x.size, true
		}
	})
	return there, word, err
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
	ts := p.host.transfersOf(p.SID)
	for _, x := range ts.list {
		if x.from == path && !x.ok && x.err == "" {
			return true
		}
	}
	name := strings.TrimRight(path, `/\`)
	x := &fileTransfer{name: name[strings.LastIndexAny(name, `/\`)+1:], from: path}
	ts.list = append(ts.list, x)
	dir, err := downloadsDir()
	if err != nil {
		x.err = err.Error()
		return true
	}
	ctx, cancel := context.WithCancel(ts.ctx)
	x.cancel = cancel
	c, sid, update := p.host.current(), p.SID, p.host.update
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

// pastedPaths is paths as a drop pastes them into a pane whose shell is at
// shell, a path of its machine: each a word of the shell followed by a
// space, so that the paths of the next drop, or what is typed, are
// another word.
func pastedPaths(shell string, paths []string) string {
	var words []string
	for _, path := range paths {
		// A path the shell's quoting refuses is left out.
		if w, ok := rex.PathWord(shell, path); ok {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ") + " "
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
		frostPanel(ui.Row(c).Padding(8, 14).Radius(r).Shadow(0, 8, 24, 0, k.shadowFocused).MaxWidth(420).PassThrough(), k, r).
			Children(func() {
				ui.Text(c, label).FontSize(12.5).TextColor(k.text).PassThrough()
			})
	})
}

// transfers lists the transfers of p's session in its bottom-right
// corner: a file there for transferShown, one that failed until clicked.
func (a *App) transfers(c *ui.Context, k *colors, p *Pane) {
	if p.host == nil || p.host.transfers[p.SID] == nil {
		return
	}
	ts, now := p.host.transfers[p.SID], c.Now()
	ts.list = slices.DeleteFunc(ts.list, func(x *fileTransfer) bool {
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
	if len(ts.list) == 0 {
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
		Radius(r).Shadow(0, 8, 24, 0, k.shadowFocused)
	frostPanel(list, k, r)
	anim := motion(c)
	if anim != nil {
		list.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: bannerMotion, Exit: bannerMotion})
	}
	// A row clicked away goes once the list is built.
	var gone *fileTransfer
	defer func() {
		ts.list = slices.DeleteFunc(ts.list, func(x *fileTransfer) bool { return x == gone })
	}()
	list.Children(func() {
		for _, x := range ts.list {
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
					// The track is a pressed face of the panel's: k.track is
					// the window background's, white on a light panel.
					ui.Box(c).Height(3).Radius(1.5).Background(k.pressed).Draw(func(pt *ui.Painter, b ui.Rect) {
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
					text = cmp.Or(x.note, locales.PaneTransferDone())
				case x.size == 0:
					text = "…"
				}
				ui.Text(c, text).FontSize(11.5).TextColor(col).LineHeight(1.3)
			})
		}
	})
}
