package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Quit and End All Sessions asks first (docs/DSK-013-messages.md, G1): it
// ends every session of this machine, and the programs in them, which
// nothing brings back.

// askQuitAndEnd is Quit and End All Sessions: a question while this
// machine has sessions, else the quit at once.
func (a *App) askQuitAndEnd() {
	if a.localSessions() == 0 {
		a.quitAndEnd()
		return
	}
	a.quitAsk = true
	a.focusReq = nil // the question takes the focus
}

// localSessions counts the sessions of this machine that the layout
// holds: those Quit and End All Sessions ends.
func (a *App) localSessions() int {
	n := 0
	if a.st == nil {
		for _, t := range a.tabs {
			for _, p := range t.allPanes() {
				if p.host == nil && p.SID != "" {
					n++
				}
			}
		}
		return n
	}
	for _, w := range a.st.workspaces {
		if w.host != nil {
			continue
		}
		for _, t := range w.savedLayout().Tabs {
			for _, r := range t.Roots() {
				for _, l := range layout.Leaves(r) {
					if l.SID != "" && l.Host == "" {
						n++
					}
				}
			}
		}
	}
	return n
}

// quitConfirm asks before Quit and End All Sessions, as Close Space asks
// (workspaceConfirm). Cancel has the focus, so that keys typed on for
// the terminal keep the sessions.
func (a *App) quitConfirm(c *ui.Context, k *colors) {
	if !a.quitAsk {
		return
	}
	n := a.localSessions()
	title := locales.QuitEndTitle(n)
	ui.DialogBase(c.Key("quit-confirm"), &a.quitAsk, func(backdrop, panel ui.Element) {
		dialogEnter(c, backdrop, panel)
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(380).Padding(20).Radius(k.radius(radiusXXL)), k).Border(k.borderW(), k.panelBorder), k).Role(ui.RoleDialog).Label(title)
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, title).FontSize(fontTitle).FontWeight(700).TextColor(k.text)
			ui.Text(c, locales.QuitEndBody(n)).FontSize(fontBody).TextColor(k.textMuted).LineHeight(1.35)
			ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
				if cancelButton(c, locales.SessionCancel()).AutoFocus().Clicked() {
					a.quitAsk = false
				}
				if dangerButton(c, k, locales.QuitEndConfirm()).Focusable().Clicked() {
					a.quitAsk = false
					a.later(c, a.quitAndEnd)
				}
			})
		})
	})
	if !a.quitAsk {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// endAndQuit ends this machine's sessions through c, and quits the app;
// tests, which run no app, take its place.
var endAndQuit = func(c *rex.Client) {
	c.Shutdown()
	mygo.App.Quit()
}
