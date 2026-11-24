package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// Toasts (docs/DSK-013-messages.md): a toast answers what the user did
// just now, and goes by itself. A window has one toast slot: a new toast
// takes the place of the one there, whose action commits first. MyGo
// keeps a toast's time, stops it while the pointer rests on the toast or
// the focus is in it, closes a focused toast on Escape, and announces its
// text; GoRex draws it, with the banner's look.

// toastKind is what a toast is: a status, an error, or a line with an
// action, as Undo.
type toastKind int

const (
	toastStatus toastKind = iota
	toastError
	toastAction
)

// The times a toast shows: a status toastLife, an action toast at least
// toastActionLife, both longer for a long text, which reads at
// toastReadRate characters a second; toastBrief is the short answer to
// Undo.
const (
	toastLife       = 5 * time.Second
	toastActionLife = 6 * time.Second
	toastBrief      = 1500 * time.Millisecond
	toastReadRate   = 15
)

// toastMsg is a toast of the window's slot.
type toastMsg struct {
	kind toastKind
	text string
	// action labels the button of an action toast, and run is what it
	// does.
	action string
	run    func()
	// commit runs once as an action toast goes without its action taken:
	// its time over, closed, replaced, its window closed or the app
	// quitting.
	commit func()
	// life is how long it shows, 0 for the time of its kind and text.
	life time.Duration
	// id is MyGo's ID of the toast once it shows; ended tells that it
	// went, its action taken or committed.
	id    string
	ended bool
}

// lifeOf is how long the toast shows.
func (m *toastMsg) lifeOf() time.Duration {
	if m.life > 0 {
		return m.life
	}
	base := toastLife
	if m.kind == toastAction {
		base = toastActionLife
	}
	return max(base, time.Duration(utf8.RuneCountInString(m.text))*time.Second/toastReadRate)
}

// end ends the toast, once: its action commits unless took tells that the
// user took the action instead.
func (m *toastMsg) end(took bool) {
	if m == nil || m.ended {
		return
	}
	m.ended = true
	if !took && m.commit != nil {
		m.commit()
	}
}

// toastSlot is a window's one toast: now the one it shows, and next the
// one asked for since the last frame, which takes its place.
type toastSlot struct {
	now, next *toastMsg
}

// show puts m in the slot, in place of the toast there, whose action
// commits now. The next frame adds it.
func (s *toastSlot) show(m *toastMsg) {
	s.next.end(false)
	s.now.end(false)
	s.next = m
}

// text is what the slot's toast says, "" while none shows.
func (s *toastSlot) text() string {
	switch {
	case s.next != nil:
		return s.next.text
	case s.now != nil:
		return s.now.text
	}
	return ""
}

// end ends the slot's toast as its window closes or the app quits: the
// action of an action toast commits.
func (s *toastSlot) end() {
	s.next.end(false)
	s.now.end(false)
	s.next, s.now = nil, nil
}

// showToast shows m in the window's toast slot, in place of the toast
// there, whose action commits now.
func (a *App) showToast(m *toastMsg) {
	a.toast.show(m)
	a.updater()(func() {})
}

// toastError tells, in an error toast, that what the user asked for did
// not happen: text is a sentence of the catalog.
func (a *App) toastError(text string) {
	a.showToast(&toastMsg{kind: toastError, text: text})
}

// commandFailed tells that the action cmd failed, for err.
func (a *App) commandFailed(cmd *command, err error) {
	a.toastError(locales.ToastActionFailed(cmd.Title(), reasonOf(err)))
}

// reasonOf is err as the reason of a sentence: without the prefix of the
// session server's package or a full stop of its own.
func reasonOf(err error) string {
	s := strings.TrimSpace(err.Error())
	s = strings.TrimPrefix(s, "rex: ")
	return strings.TrimRight(s, ".")
}

// settingsToastError is toastError in the Settings window, which has a
// toast slot of its own.
func settingsToastError(text string) {
	settingsUI.toast.show(&toastMsg{kind: toastError, text: text})
	if w := settingsUI.win; w != nil {
		w.Update(func() {})
	}
}

// toastText is what the window's toast says, "" while none shows.
func (a *App) toastText() string { return a.toast.text() }

// endToast ends the window's toast as its window closes or the app
// quits: the action of an action toast commits.
func (a *App) endToast() { a.toast.end() }

// toasts shows the window's toast, above the banner while that shows.
func (a *App) toasts(c *ui.Context, k *colors) { a.toast.build(c, k, a.bannerTop) }

// build shows the slot's toast: the one asked for since the last frame,
// in place of the one there. It sits at the bottom centre, as the banner,
// and above bannerTop, where the banner's panel starts, when that is not
// 0.
func (s *toastSlot) build(c *ui.Context, k *colors, bannerTop float32) {
	if m := s.next; m != nil {
		s.next = nil
		if old := s.now; old != nil && old.id != "" {
			c.CloseToast(old.id)
		}
		s.now = m
		m.id = c.AddToast(ui.Toast{Title: m.text, Timeout: m.lifeOf(), OnClose: func() {
			m.end(false)
			if s.now == m {
				s.now = nil
			}
		}})
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, list []ui.Toast) {
		bottom := float32(bannerBottom)
		if bannerTop > 0 {
			_, h := c.Size()
			bottom = h - bannerTop + 8
		}
		viewport.Padding(0, 0, bottom, 0).AlignItems(ui.Center)
		for _, t := range list {
			if m := s.now; m != nil && m.id == t.ID {
				toastView(c, k, t, m)
			}
		}
	})
}

// toastView draws toast t, of m: the banner's panel, with a status's icon
// or an error's dot, the text, and an action toast's button, the ring of
// its time left and its close button.
func toastView(c *ui.Context, k *colors, t ui.Toast, m *toastMsg) {
	p := ui.ToastBase(c, t)
	root := p.Root
	if anim := motion(c); anim != nil {
		root.Transition(exitIn(ui.ElementTransition{Animation: anim, Position: true, Enter: bannerMotion, Exit: bannerMotion}, durExitMove))
	}
	if m.kind == toastError {
		// Read out at once: what the user asked for did not happen.
		root.Role(ui.RoleAlert)
	}
	toastShadow(root.Row().Gap(10).Padding(8, 10, 8, 14).Radius(radiusL).Background(k.panel).Border(1, k.panelBorder), k).AlignItems(ui.Center)
	root.Children(func() {
		switch m.kind {
		case toastError:
			ui.Box(c).Size(8, 8).Radius(capsule(8)).Background(k.danger).Shrink(0)
		case toastStatus:
			glyphIcon(c, "check", 14, k.textMuted).Shrink(0)
		}
		ui.Text(c, m.text).FontSize(fontCallout).TextColor(k.text).MaxWidth(520)
		if m.kind != toastAction {
			return
		}
		if ui.Button(c.Key("toast-action"), m.action).Clicked() {
			m.end(true)
			if m.run != nil {
				m.run()
			}
			c.CloseToast(t.ID)
		}
		toastRing(c, k, p, m, root.Hovered() || root.FocusWithin())
		if iconButton(c, k, "x", locales.ToastClose(), 22, 14).Clicked() {
			c.CloseToast(t.ID)
		}
	})
}

// toastRing draws the time an action toast has left, as a ring that
// empties from the top, clockwise. It moves in every frame, or each
// second with less motion, and holds while held: the pointer on the
// toast, or the focus in it, which stop its time.
func toastRing(c *ui.Context, k *colors, p ui.ToastParts, m *toastMsg, held bool) {
	left := p.Left()
	share := float32(1)
	if life := m.lifeOf(); life > 0 {
		share = max(0, min(1, float32(left)/float32(life)))
	}
	ui.Box(c).Size(14, 14).Shrink(0).AccessHidden(true).Draw(func(pt *ui.Painter, r ui.Rect) {
		cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, r.W/2-1
		var track, arc ui.Path
		ringPath(&track, cx, cy, rad)
		pt.StrokePath(&track, 1.5, k.panelBorder)
		ringPath(&arc, cx, cy, rad).Trim(0, share)
		pt.StrokePath(&arc, 1.5, k.textMuted)
	})
	switch {
	case held || left <= 0:
	case motion(c) == nil:
		c.After(min(left, time.Second))
	default:
		c.AnimationFrame()
	}
}

// ringPath adds to p a circle about (cx, cy) of radius r that starts at
// its top and goes clockwise, and returns p.
func ringPath(p *ui.Path, cx, cy, r float32) *ui.Path {
	const k = 0.5522847498 // a quarter circle's control points, as Path.Circle's
	p.MoveTo(cx, cy-r)
	p.CubeTo(cx+r*k, cy-r, cx+r, cy-r*k, cx+r, cy)
	p.CubeTo(cx+r, cy+r*k, cx+r*k, cy+r, cx, cy+r)
	p.CubeTo(cx-r*k, cy+r, cx-r, cy+r*k, cx-r, cy)
	p.CubeTo(cx-r, cy-r*k, cx-r*k, cy-r, cx, cy-r)
	return p
}
