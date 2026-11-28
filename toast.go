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
// takes the place of the one there. MyGo
// keeps a toast's time, stops it while the pointer rests on the toast or
// the focus is in it, closes a focused toast on Escape, and announces its
// text; GoRex draws it, with the banner's look.

// toastKind is what a toast is: a status or an error.
type toastKind int

const (
	toastStatus toastKind = iota
	toastError
)

// A toast shows toastLife, longer for a long text, which reads at
// toastReadRate characters a second.
const (
	toastLife     = 5 * time.Second
	toastReadRate = 15
)

// toastMsg is a toast of the window's slot.
type toastMsg struct {
	kind toastKind
	text string
	// id is MyGo's ID of the toast once it shows.
	id string
}

// lifeOf is how long the toast shows.
func (m *toastMsg) lifeOf() time.Duration {
	return max(toastLife, time.Duration(utf8.RuneCountInString(m.text))*time.Second/toastReadRate)
}

// toastSlot is a window's one toast: now the one it shows, and next the
// one asked for since the last frame, which takes its place.
type toastSlot struct {
	now, next *toastMsg
}

// show puts m in the slot, in place of the toast there. The next frame
// adds it.
func (s *toastSlot) show(m *toastMsg) { s.next = m }

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

// showToast shows m in the window's toast slot, in place of the toast
// there.
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
// or an error's dot, and the text.
func toastView(c *ui.Context, k *colors, t ui.Toast, m *toastMsg) {
	root := ui.ToastBase(c, t).Root
	if anim := motion(c); anim != nil {
		root.Transition(exitIn(ui.ElementTransition{Animation: anim, Position: true, Enter: bannerMotion, Exit: bannerMotion}, durExitMove))
	}
	if m.kind == toastError {
		// Read out at once: what the user asked for did not happen.
		root.Role(ui.RoleAlert)
	}
	toastShadow(root.Row().Gap(10).Padding(8, 10, 8, 14).Radius(k.radius(radiusL)).Background(k.panel).Border(k.borderW(), k.panelBorder), k).AlignItems(ui.Center)
	root.Children(func() {
		switch m.kind {
		case toastError:
			ui.Box(c).Size(8, 8).Radius(k.capsule(8)).Background(k.danger).Shrink(0)
		case toastStatus:
			glyphIcon(c, "check", 14, k.textMuted).Shrink(0)
		}
		ui.Text(c, m.text).FontSize(fontCallout).TextColor(k.text).MaxWidth(520)
	})
}
