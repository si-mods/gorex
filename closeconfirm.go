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

// running reports whether closing the pane ends a program: one in the
// foreground of its shell, or one that reported it works or needs the
// user. A shell at its prompt runs none.
func (p *Pane) running() bool {
	if p.browser != nil || p.note != nil || p.info.Exited {
		return false
	}
	if s := p.status(); s != nil && (s.State == "working" || needsUser(s)) {
		return true
	}
	return !p.info.Idle && p.info.Program != ""
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

// endAsked closes what the popconfirm asked about, if it is still there.
func (a *App) endAsked() {
	ask := a.closeAsk
	a.closeAsk = closeAsk{}
	switch {
	case ask.pane != nil:
		if !ask.pane.closed {
			a.closePane(ask.pane)
		}
	case ask.tab != nil && slices.Contains(a.tabs, ask.tab):
		a.closeTab(ask.tab)
	}
}

// closeConfirm draws the popconfirm from anchor, the close button of t,
// or of p when p is set, while it asks about closing it.
func (a *App) closeConfirm(c *ui.Context, k *colors, anchor ui.Element, t *Tab, p *Pane) {
	if !a.asking(t, p) {
		return
	}
	a.closeAsk.shown = true
	ui.PopoverBase(c, anchor, &a.closeAsk.open, func(panel ui.Element) {
		popoverShadow(glassPanel(panel.Margin(8, 0, 0, 0).Width(280).Padding(14).Radius(radiusXL), k.panel).Border(1, k.panelBorder), k)
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
	if ask.pane != nil && ask.pane.closed || ask.pane == nil && !slices.Contains(a.tabs, ask.tab) {
		*ask = closeAsk{}
		return
	}
	ui.DialogBase(c, &ask.open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(320).Padding(20).Radius(radiusXXL), k.panel).Border(1, k.panelBorder))
		a.closeAskBody(c, k, panel)
	})
	a.closeAskDone()
}

// closeAskBody is the question: what runs, what closing ends, and the
// buttons. Enter ends the session, Escape keeps it.
func (a *App) closeAskBody(c *ui.Context, k *colors, panel ui.Element) {
	ask := a.closeAsk
	p := ask.pane
	if p == nil {
		p = ask.tab.runningPane()
	}
	name := ""
	if p != nil {
		name, _ = p.label()
	}
	body := locales.CloseRunningPane()
	if ask.pane == nil {
		body = locales.CloseRunningTab(len(ask.tab.allPanes()))
	}
	panel.Role(ui.RoleDialog).Label(locales.CloseRunningTitle(name))
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, locales.CloseRunningTitle(name)).FontSize(fontTitle).FontWeight(700).TextColor(k.text).SingleLine().Ellipsis("…")
		ui.Text(c, body).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			if ui.Button(c, locales.SessionCancel()).Clicked() {
				a.closeAsk.open = false
			}
			end := dangerButton(c, k, locales.CloseEndSession()).Focusable().AutoFocus()
			if end.Clicked() || end.Shortcut(0, ui.KeyEnter) {
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
	tabs, panes := 0, 0
	for _, o := range a.tabs {
		if o != keep {
			tabs++
			panes += len(o.allPanes())
		}
	}
	title := locales.CloseOthersTitle(tabs)
	ui.DialogBase(c, &a.closeOthersOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(380).Padding(20).Radius(radiusXXL), k.panel).Border(1, k.panelBorder)).Role(ui.RoleDialog).Label(title)
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, title).FontSize(fontTitle).FontWeight(700).TextColor(k.text)
			ui.Text(c, locales.CloseOthersBody(panes)).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
			ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.SessionCancel()).Clicked() {
					a.closeOthersOpen = false
				}
				end := dangerButton(c, k, locales.TabCloseOthers()).Focusable().AutoFocus()
				if end.Clicked() || end.Shortcut(0, ui.KeyEnter) {
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
