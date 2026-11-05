package main

import (
	"context"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// hostAt is the window's host of address addr, an error code and message
// when it has none connected, or more than one.
func (a *App) hostAt(addr string) (*hostConn, string, string) {
	var found []*hostConn
	for _, h := range a.hosts {
		if addr != "" && strings.TrimSpace(h.Address) == strings.TrimSpace(addr) && h.current() != nil {
			found = append(found, h)
		}
	}
	switch len(found) {
	case 0:
		return nil, "host_not_found", "no such host connected to the GoRex window"
	case 1:
		return found[0], "", ""
	}
	return nil, "host_ambiguous", "more than one host of the GoRex window has that address"
}

// teleportRun is a teleport of a pane under way: the host it goes to,
// the step it is at, and how to stop it.
type teleportRun struct {
	host   *hostConn
	step   string
	cancel context.CancelFunc
}

// teleportBlocked says why pane p cannot teleport now, "" when it can:
// only a pane of this machine, a shell at its prompt, or Claude Code or
// omp waiting for the user (docs/teleport.md).
func teleportBlocked(p *Pane) string {
	switch {
	case p.teleport != nil:
		return locales.TeleportBusy()
	case p.host != nil:
		return locales.TeleportFromHost()
	}
	switch rex.TeleportRefusal(p.info) {
	case rex.TeleportEnded:
		return locales.TeleportEnded()
	case rex.TeleportProgram:
		return locales.TeleportProgram(p.info.ProgramName())
	case rex.TeleportUnknown:
		return locales.TeleportUnknown(p.info.ProgramName())
	case rex.TeleportWorking:
		return locales.TeleportWorking(p.info.ProgramName())
	}
	return ""
}

// teleportHosts are the hosts a pane may teleport to: those connected.
// why says, for each, why it cannot take one, "" when it can.
func (a *App) teleportHosts() (hosts []*hostConn, why []string) {
	for _, h := range a.hosts {
		if h.current() == nil {
			continue
		}
		reason := ""
		if !h.hello.Can(rex.CapabilityTeleport) {
			reason = locales.TeleportTooOld(h.Name)
		}
		hosts, why = append(hosts, h), append(why, reason)
	}
	return hosts, why
}

// teleportMenu fills the pane menu's Teleport to submenu: the hosts, or
// why the pane or a host cannot.
func (a *App) teleportMenu(m *ui.Menu, p *Pane) {
	hosts, why := a.teleportHosts()
	reason := teleportBlocked(p)
	if reason == "" && len(hosts) == 0 {
		reason = locales.TeleportNoHosts()
	}
	if reason != "" {
		m.Item(reason).Disabled(true)
		return
	}
	for i, h := range hosts {
		label := h.Name
		if why[i] != "" {
			label = why[i]
		}
		if m.Item(label).Disabled(why[i] != "").Chosen() {
			a.teleport(p, h)
		}
	}
}

// teleportItems are the palette's Teleport to a host, for the focused
// pane: disabled, with the reason, when it cannot go.
func (a *App) teleportItems(match func(string) bool) []paletteItem {
	t := a.tab()
	if t == nil || t.Focus == nil || t.Focus.SID == "" || t.Focus.browser != nil || t.Focus.note != nil {
		return nil
	}
	p := t.Focus
	hosts, why := a.teleportHosts()
	reason := teleportBlocked(p)
	var items []paletteItem
	for i, h := range hosts {
		title := locales.CmdTeleportTo(h.Name)
		if !match(title) && !match(locales.In("en").CmdTeleportTo(h.Name)) {
			continue
		}
		detail := reason
		if detail == "" {
			detail = why[i]
		}
		items = append(items, paletteItem{title: title, detail: detail, glyph: "square-arrow-out-up-right", disabled: detail != "",
			run: func() { a.teleport(p, h) }})
	}
	return items
}

// teleport moves pane p's work to host h: its server packs it, h's
// unpacks it and starts it again there, and the pane becomes that
// session, its own ending. Until h answers, the pane is as it was, under
// an overlay that tells the step and cancels it.
func (a *App) teleport(p *Pane, h *hostConn) {
	if teleportBlocked(p) != "" || p.SID == "" {
		return
	}
	dst := h.current()
	if dst == nil {
		a.offline(h)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &teleportRun{host: h, step: rex.TeleportPacking, cancel: cancel}
	p.teleport = run
	a.changed()
	update := a.hostUpdate()
	src, sid := a.client, p.SID
	go func() {
		var expected string
		in, err := rex.Teleport(ctx, src, sid, dst, func(step, newSID string) {
			// Marked before the host can start it: its pane takes it,
			// not a tab of its own.
			if newSID != "" && expected == "" {
				expected = newSID
				h.expecting(newSID, true)
			}
			update(func() {
				run.step = step
				a.changed()
			})
		})
		update(func() {
			if p.teleport == run {
				p.teleport = nil
			}
			cancelled := ctx.Err() != nil
			cancel()
			switch {
			case err == nil:
				a.teleported(p, h, in)
			case !cancelled:
				// A banner as the others, until dismissed or the pane's
				// next teleport goes.
				p.teleportErr = locales.TeleportFailed(h.Name, strings.TrimPrefix(strings.TrimPrefix(err.Error(), "rex: "), "teleport: "))
				a.err, a.errMsg = p.teleportErr, nil
			}
			if expected != "" {
				h.expecting(expected, false)
			}
			a.changed()
		})
	}()
}

// teleported makes pane p, whose work went to host h, the session in
// there: a tab of the host's in the window, as a tab's panes are all on
// one machine, named as p's was, and p's own session ends.
func (a *App) teleported(p *Pane, h *hostConn, in rex.SessionInfo) {
	if p.closed || p.Tab == nil {
		// Closed meanwhile, its session ended: the new one goes too.
		h.kill(in.ID)
		return
	}
	if in.Cols == 0 {
		in.Cols, in.Rows = p.info.Cols, p.info.Rows
	}
	if p.teleportErr != "" && a.err == p.teleportErr {
		a.err = ""
	}
	t := a.sessionTab(h, in)
	np := t.Focus
	np.Name = p.Name
	if len(p.Tab.allPanes()) == 1 {
		t.Name = p.Tab.Name
	}
	at := slices.Index(a.tabs, p.Tab) + 1
	a.tabs = slices.Insert(a.tabs, at, t)
	// p's tab goes with p when it was its last pane; the window, with a
	// tab still, stays open.
	a.closePane(p)
	a.groupTabs()
	a.selectTab(slices.Index(a.tabs, t))
	a.focusReq = np
	a.changed()
}

// teleportOverlay frosts pane p while its teleport runs: where it goes,
// the step it is at, and a Cancel button.
func (a *App) teleportOverlay(c *ui.Context, k *colors, p *Pane) {
	run := p.teleport
	if run == nil {
		return
	}
	step := locales.TeleportPackingStep()
	switch run.step {
	case rex.TeleportSending:
		step = locales.TeleportSendingStep(run.host.Name)
	case rex.TeleportStarting:
		step = locales.TeleportStartingStep(run.host.Name)
	}
	title := locales.TeleportTitle(run.host.Name)
	ui.Column(c.Key("teleport")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Justify(ui.Center).AlignItems(ui.Center).Gap(8).
		Draw(func(pt *ui.Painter, r ui.Rect) { paintFrost(pt, r, 16, 0, k.panelGlass, k.panel) }).Label(title).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(k.text)
		ui.Text(c, step).FontSize(12.5).TextColor(k.textMuted)
		// A cancel stops the target before it starts the session.
		if ui.Button(c.Key("cancel"), locales.TeleportCancel()).Clicked() {
			run.cancel()
		}
	})
}
