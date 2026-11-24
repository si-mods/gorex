package main

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// closeAsk is the close a popconfirm asks about: of a tab, or of a pane
// when pane is set. The close button that asked draws it, anchored to it
// (closeConfirm); where no button shows, as with pane headers off, it is
// a small dialog (closeAskFallback).
type closeAsk struct {
	tab  *Tab
	pane *Pane
	open bool
	// shown tells that a close button drew it in this frame.
	shown bool
}

// running reports whether closing the pane ends a program: the command
// the session was started with, one in the foreground of its shell, or
// one that reported it works or waits for the user. A shell at its
// prompt runs none, also with a done or error record not seen yet.
func (p *Pane) running() bool {
	if !p.terminal() || p.info.Exited {
		return false
	}
	if p.info.Command {
		return true
	}
	for _, s := range p.info.Statuses {
		if s.State == "working" || s.State == "blocked" {
			return true
		}
	}
	return !p.info.Idle && p.info.Program != ""
}

// terminal reports whether the pane is a terminal: not a browser or a note.
func (p *Pane) terminal() bool { return p.browser == nil && p.note == nil }

// terminals counts the terminals among the tabs' panes, and those of them
// that run a program.
func terminals(tabs ...*Tab) (n, running int) {
	for _, t := range tabs {
		for _, p := range t.allPanes() {
			if p.terminal() {
				n++
				if p.running() {
					running++
				}
			}
		}
	}
	return
}

// runningPane is the first of the tab's panes that runs a program, nil
// for none.
func (t *Tab) runningPane() *Pane {
	for _, p := range t.allPanes() {
		if p.running() {
			return p
		}
	}
	return nil
}

// askCloseTab closes the tab, or asks first while a program runs in it.
func (a *App) askCloseTab(t *Tab) {
	if t.runningPane() == nil {
		a.closeTab(t)
		return
	}
	a.closeAsk = closeAsk{tab: t, open: true}
}

// askClosePane closes the pane, or asks first while a program runs in it.
func (a *App) askClosePane(p *Pane) {
	if !p.running() {
		a.closePane(p)
		return
	}
	a.closeAsk = closeAsk{tab: p.Tab, pane: p, open: true}
}

// asking reports whether the popconfirm asks about closing t, or p when
// p is set.
func (a *App) asking(t *Tab, p *Pane) bool {
	return a.closeAsk.open && a.closeAsk.pane == p && (p != nil || a.closeAsk.tab == t)
}

// askedThere reports whether what the question asks about is still in
// the window: not closed, and in a tab of the space it shows.
func (a *App) askedThere() bool {
	ask := a.closeAsk
	if ask.pane != nil {
		return !ask.pane.closed && slices.Contains(a.tabs, ask.pane.Tab)
	}
	return ask.tab != nil && slices.Contains(a.tabs, ask.tab)
}

// endAsked closes what the popconfirm asked about, if it is still there;
// gone, or in a space the window no longer shows, it closes nothing.
func (a *App) endAsked() {
	if !a.askedThere() {
		a.closeAsk = closeAsk{}
		return
	}
	ask := a.closeAsk
	a.closeAsk = closeAsk{}
	if ask.pane != nil {
		a.closePane(ask.pane)
	} else {
		a.closeTab(ask.tab)
	}
}

// closeConfirm draws the popconfirm from anchor, the close button of t,
// or of p when p is set, while it asks about closing it.
func (a *App) closeConfirm(c *ui.Context, k *colors, anchor ui.Element, t *Tab, p *Pane) {
	if !a.asking(t, p) {
		popoverIdle(c, anchor)
		return
	}
	// A tab's row scrolled out of the sidebar's list leaves the question
	// to the dialog: its button is clipped.
	if r, list := anchor.Bounds(), a.sideList; p == nil && prefs.TabPlacement != "" && r.H > 0 && list.H > 0 &&
		(r.Y+r.H/2 < list.Y || r.Y+r.H/2 > list.Y+list.H) {
		popoverIdle(c, anchor)
		return
	}
	a.closeAsk.shown = true
	popoverBase(c, anchor, &a.closeAsk.open, func(panel ui.Element, _ bool) {
		popoverShadow(glassPanel(panel.Margin(8, 0, 0, 0).Width(280).Padding(14).Radius(k.radius(radiusXL)), k).Border(k.borderW(), k.panelBorder), k)
		a.closeAskBody(c, k, panel)
	})
	a.closeAskDone()
}

// closeAskFallback shows the question as a dialog while no close button
// drew it, as for the close shortcut on a pane without a header, and
// lets it go once what it asks about is gone.
func (a *App) closeAskFallback(c *ui.Context, k *colors) {
	ask := &a.closeAsk
	shown := ask.shown
	ask.shown = false
	if !ask.open || shown {
		return
	}
	if !a.askedThere() {
		*ask = closeAsk{}
		return
	}
	ui.DialogBase(c, &ask.open, func(backdrop, panel ui.Element) {
		dialogEnter(c, backdrop, panel)
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(320).Padding(20).Radius(k.radius(radiusXXL)), k).Border(k.borderW(), k.panelBorder), k)
		a.closeAskBody(c, k, panel)
	})
	a.closeAskDone()
}

// closeAskBody is the question: what runs, what closing ends, and the
// buttons. Cancel has the focus, so that keys typed on for the terminal
// keep the session: Enter or Space cancel, and so does Escape; End
// Session takes a click, or Tab then Enter.
func (a *App) closeAskBody(c *ui.Context, k *colors, panel ui.Element) {
	ask := a.closeAsk
	p := ask.pane
	if p == nil {
		p = ask.tab.runningPane()
	}
	name := ""
	if p != nil {
		if name = p.info.ProgramName(); name == "" {
			name = p.program().Name
		}
	}
	body := locales.CloseRunningPane()
	if ask.pane == nil {
		n, _ := terminals(ask.tab)
		body = locales.CloseRunningTab(n)
	}
	panel.Role(ui.RoleDialog).Label(locales.CloseRunningTitle(name))
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, locales.CloseRunningTitle(name)).FontSize(fontTitle).FontWeight(700).TextColor(k.text).SingleLine().Ellipsis("…")
		ui.Text(c, body).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			if cancelButton(c, locales.SessionCancel()).AutoFocus().Clicked() {
				a.closeAsk.open = false
			}
			if dangerButton(c, k, locales.CloseEndSession()).Focusable().Clicked() {
				a.closeAsk.open = false
				a.later(c, a.endAsked)
			}
		})
	})
}

// closeAskDone gives the focus back to the tab's pane once the question
// is answered or dismissed. What it asked about stays in closeAsk for
// endAsked, which runs after the frame.
func (a *App) closeAskDone() {
	if t := a.tab(); !a.closeAsk.open && t != nil && a.focusReq == nil {
		a.focusReq = t.Focus
	}
}

// askCloseOthers asks before Close Other Tabs closes every tab but t.
func (a *App) askCloseOthers(t *Tab) {
	if len(a.tabs) > 1 {
		a.closeOthers, a.closeOthersOpen = t, true
		a.focusReq = nil
	}
}

// closeOthersConfirm asks before Close Other Tabs ends the sessions of
// the other tabs, as Close Space asks (workspaceConfirm).
func (a *App) closeOthersConfirm(c *ui.Context, k *colors) {
	keep := a.closeOthers
	if !a.closeOthersOpen || !slices.Contains(a.tabs, keep) {
		a.closeOthers, a.closeOthersOpen = nil, false
		return
	}
	others := slices.DeleteFunc(slices.Clone(a.tabs), func(o *Tab) bool { return o == keep })
	n, running := terminals(others...)
	title := locales.CloseOthersTitle(len(others))
	body := locales.CloseOthersBody(n)
	if running == 0 {
		body = locales.CloseOthersBodyIdle(n)
	}
	ui.DialogBase(c, &a.closeOthersOpen, func(backdrop, panel ui.Element) {
		dialogEnter(c, backdrop, panel)
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(380).Padding(20).Radius(k.radius(radiusXXL)), k).Border(k.borderW(), k.panelBorder), k).Role(ui.RoleDialog).Label(title)
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, title).FontSize(fontTitle).FontWeight(700).TextColor(k.text)
			ui.Text(c, body).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
			ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
				if cancelButton(c, locales.SessionCancel()).AutoFocus().Clicked() {
					a.closeOthersOpen = false
				}
				if dangerButton(c, k, locales.TabCloseOthers()).Focusable().Clicked() {
					a.closeOthersOpen = false
					a.later(c, func() { a.closeOtherTabs(keep) })
				}
			})
		})
	})
	if !a.closeOthersOpen {
		a.closeOthers = nil
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// closeOtherTabs closes every tab but keep and ends their sessions.
func (a *App) closeOtherTabs(keep *Tab) {
	for _, o := range slices.Clone(a.tabs) {
		if o != keep {
			a.closeTab(o)
		}
	}
}
