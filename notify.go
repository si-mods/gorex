package main

import (
	"bytes"
	"cmp"
	"image/png"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// notifyStatus tells, with a notification, that a program of pane p
// reported r.
func (a *App) notifyStatus(p *Pane, r *rex.ProgramStatus) {
	prog, icon := r.App, p.program()
	if prog == "" {
		prog, _ = p.label()
	} else {
		icon = programOf(prog)
	}
	title := locales.NotifyFinished(prog)
	switch r.State {
	case "blocked":
		title = locales.NotifyBlocked(prog)
	case "error":
		title = locales.NotifyFailed(prog)
	}
	a.notifyPane(p, prog, icon, title, cmp.Or(r.Message(), r.Title))
}

// notifyDue tells whether a notification of the pane may show, and if so
// marks it shown. One of its status shows once in 10 seconds at most; one
// of its program's own (own) also waits 10 seconds after either, and so
// never keeps a status, which says more, from showing.
func (p *Pane) notifyDue(own bool) bool {
	if time.Since(p.notified) <= 10*time.Second || own && time.Since(p.told) <= 10*time.Second {
		return false
	}
	if own {
		p.told = time.Now()
	} else {
		p.notified = time.Now()
	}
	return true
}

// notifyProgram shows the notification a program of pane p sent (OSC 9
// and 777), unless the pane is gone or the user looks at it.
func (a *App) notifyProgram(p *Pane, title, body string) {
	if !p.closed && !a.shows(p) && p.notifyDue(true) {
		a.notifyPane(p, "", p.program(), cmp.Or(title, "GoRex"), body)
	}
}

// notifyKey is the host and session of pane p, which notifications of it
// keep in their IDs and groups.
func notifyKey(p *Pane) string {
	if p.host == nil {
		return "|" + p.SID
	}
	return p.host.ID + "|" + p.SID
}

// showNotification shows a notification, and returns what removes it.
// Show waits for the system, and the first time for the user to allow
// notifications, so it runs off the main thread; tests, which run no app,
// take its place.
var showNotification = func(o mygo.NotificationOptions) (close func()) {
	n := mygo.NewNotification(o)
	go func() { notificationShown(n.Show()) }()
	return n.Close
}

// notifyPane shows a notification of pane p, whose title names prog, ""
// for none: under the title, the names of its tab and pane and of its
// host, but for those the title says already. The notifications of a
// session stack together till its pane is seen, and the store's
// notificationClicked brings the pane of one clicked to the front, from
// its ID. With no body, the names take its place: Windows and Linux put
// them on a line of the body. Its image is the tile of icon (tileImage):
// of the program that finished, of the program a status names or else
// the pane's, and of the pane's program for one a program sent; none for
// a shell.
func (a *App) notifyPane(p *Pane, prog string, icon program, title, body string) {
	var parts []string
	add := func(s string) {
		if s != "" && s != prog && !slices.Contains(parts, s) {
			parts = append(parts, s)
		}
	}
	if p.Tab != nil {
		tn, _ := p.Tab.label()
		add(tn)
	}
	name, _ := p.label()
	add(name)
	src := strings.Join(parts, " · ")
	if p.host != nil {
		if src == "" {
			src = a.hostName(p.host)
		} else {
			src = locales.NotifyOnHost(src, a.hostName(p.host))
		}
	}
	if body == "" {
		body, src = src, ""
	}
	key := notifyKey(p)
	close := showNotification(mygo.NotificationOptions{
		ID:       "gorex|" + key + "|" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Title:    title,
		Subtitle: src,
		Body:     body,
		Group:    key,
		Image:    tileImage(icon),
	})
	if a.st != nil {
		if a.st.shown == nil {
			a.st.shown = map[string][]func(){}
		}
		a.st.shown[key] = append(a.st.shown[key], close)
	}
}

// tileImage is the tile of prog, as a PNG of 72 pixels square; nil for a
// shell, which has no program to show. Each tile is drawn once, in a
// frame of its own, which ui.Render draws on the main thread: call it
// there, out of a frame the windows build, as the updates that notify
// are. One MyGo refuses to draw gives none, and is drawn again the next
// time.
func tileImage(prog program) []byte {
	if prog.Name == "" || prog.Shell {
		return nil
	}
	key := tileKey{prog.Glyph, prog.TileGlyph, prog.TileBg, prog.TileFg, prog.TileBgGradient, prog.TileBorder, programIconKey(prog.Icon)}
	tileImagesMu.Lock()
	b, ok := tileImages[key]
	tileImagesMu.Unlock()
	if ok {
		return b
	}
	b = drawTileImage(prog)
	if b == nil {
		return nil
	}
	tileImagesMu.Lock()
	if len(tileImages) >= 512 {
		clear(tileImages)
	}
	tileImages[key] = b
	tileImagesMu.Unlock()
	return b
}

// drawTileImage draws the tile of tileImage on a window of no background:
// the tile's corners stay clear. A view MyGo refuses to draw, as its
// headless runtime panics, gives none.
func drawTileImage(prog program) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("gorex: drawing the tile of %s: %v", prog.Name, r)
			out = nil
		}
	}()
	img := ui.Render(func(c *ui.Context) {
		t := *c.Theme()
		t.Background = ui.RGBA(0, 0, 0, 0)
		c.SetTheme(&t)
		ui.Box(c).Size(24, 24).Draw(func(p *ui.Painter, r ui.Rect) {
			drawTile(p, &lightColors, ui.Rect{X: r.X + (r.W-tileW)/2, Y: r.Y + (r.H-tileH)/2, W: tileW, H: tileH}, prog, true)
		})
	}, 24, 24, 3)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// tileKey is what a tile shows: its glyphs and colors.
type tileKey struct {
	glyph, tileGlyph string
	bg, fg           ui.Color
	bgGrad           *ui.LinearGradient
	border           bool
	iconHash         string
}

// tileImages are the PNGs of tileImage, 512 at most.
var (
	tileImagesMu sync.Mutex
	tileImages   = map[tileKey][]byte{}
)

// appearanceDark tells whether the app shows its dark appearance; tests,
// which run no app, take its place.
var appearanceDark = mygo.Theme.IsDark

// closeNotifications removes the notifications of a session, by its
// notifyKey, as its pane is seen.
func (st *store) closeNotifications(key string) {
	for _, close := range st.shown[key] {
		close()
	}
	delete(st.shown, key)
}

// notificationClicked brings to the front the pane of the notification
// clicked, by the host and session of the ID notifyPane gave it: in a
// window, or in a space no window shows, which shows then. One whose
// pane is not there yet, as the click launched the app or its host has
// not connected, waits for the windows' next polls (retryClick).
func (st *store) notificationClicked(id string) {
	st.clicked, st.clickedAt = id, time.Now()
	st.retryClick()
}

// retryClick brings the pane of the notification clicked to the front,
// once it is there; it gives up a minute after the click.
func (st *store) retryClick() {
	if st.clicked == "" || len(st.apps) == 0 {
		return
	}
	f := strings.Split(st.clicked, "|")
	if len(f) != 4 || f[0] != "gorex" || time.Since(st.clickedAt) > time.Minute {
		st.clicked = ""
		return
	}
	n, ok := st.needyOf(f[1], f[2])
	if !ok {
		return
	}
	st.clicked = ""
	// The focused window shows a space no window shows.
	a := cmp.Or(n.app, st.apps[0])
	if a.focusNeedy(n) != nil {
		a.front()
	}
}

// needyOf finds session sid of host, "" for this machine: its pane in a
// window, else a space no window shows that has it.
func (st *store) needyOf(host, sid string) (needy, bool) {
	for _, o := range st.apps {
		for _, t := range o.tabs {
			for _, p := range t.allPanes() {
				if p.SID == sid && (p.host == nil && host == "" || p.host != nil && p.host.ID == host) {
					return needy{app: o, tab: t, pane: p}, true
				}
			}
		}
	}
	var h *hostConn
	if host != "" {
		i := slices.IndexFunc(st.hosts, func(h *hostConn) bool { return h.ID == host })
		if i < 0 {
			return needy{}, false
		}
		h = st.hosts[i]
	}
	for _, w := range st.workspaces {
		found := false
		if w.app == nil {
			st.visitSIDs(w, host, func(s string) { found = found || s == sid })
		}
		if found {
			return needy{ws: w, sid: sid, host: h}, true
		}
	}
	return needy{}, false
}

// laterService queues a menu action without keeping a build context.
func (a *App) laterService(fn func()) { a.post(fn); a.services.Invalidate() }
