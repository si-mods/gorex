package main

import (
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/locales"
)

// signInWait is how long the Sign in button waits for the built-in node's
// link; signInEvery is the time between two asks.
const (
	signInWait  = 10 * time.Second
	signInEvery = 500 * time.Millisecond
)

// remoteTailnet draws what follows the Tailscale switch: on the built-in
// side, a line for the node's state, with Sign in while it needs one;
// on the system side, the broker's error while it does not run.
func (a *App) remoteTailnet(c *ui.Context, k *colors, tn *rex.TailnetStatus) {
	if !tn.Enabled {
		return
	}
	if tn.Side != rex.TailnetBuiltin {
		if !tn.Running {
			hint := locales.TailnetUnavailable()
			if tn.Error != "" {
				hint = tn.Error
			}
			groupRow(c, k, nil).Children(func() { rowHint(c, k, hint) })
		}
		return
	}
	page := &remoteUI
	switch tn.Node {
	case rex.NodeNeedsLogin:
		groupRow(c, k, "ts-login").Children(func() {
			rowLine(c, k, locales.TailnetSignInHint(), func() {
				if ui.Button(c.Key("ts-signin"), locales.TailnetSignIn()).Disabled(page.signing).Clicked() {
					a.tailnetSignIn(c.Services())
				}
			})
			if page.signErr != "" {
				rowError(c, k, page.signErr)
			}
		})
	case rex.NodeNeedsApproval:
		groupRow(c, k, nil).Children(func() { rowHint(c, k, locales.TailnetApproval()) })
	case rex.NodeStarting:
		groupRow(c, k, nil).Children(func() { rowHint(c, k, locales.TailnetStarting()) })
	case rex.NodeOff:
		// Off until the user turns it on, as after an upgrade with the
		// mode on from before, or after a sign-out or gorex rexapi
		// tailscale off: Turn on starts it, as the switch does. Error is
		// why turning it on failed.
		groupRow(c, k, "ts-off").Children(func() {
			rowLine(c, k, locales.TailnetOff(), func() {
				if ui.Button(c.Key("ts-on"), locales.TailnetTurnOn()).Disabled(page.unreachable || page.busy).Clicked() {
					a.remoteDo(func(srv *rex.Client) error { return srv.SetTailnet(true) }, nil)
				}
			})
			if tn.Error != "" {
				rowError(c, k, tn.Error)
			}
		})
	case rex.NodeRunning:
		// A tagged node has no account; one that runs may still not
		// carry discovery, and Error says why.
		groupRow(c, k, nil).Children(func() {
			if tn.Account != "" {
				ui.Text(c, locales.TailnetSignedIn(tn.Account)).FontSize(13).FontWeight(500).TextColor(k.text)
			}
			rowHint(c, k, tn.Device)
			if !tn.Running && tn.Error != "" {
				rowError(c, k, tailnetReason(tn))
			}
		})
	default:
		if tn.Error != "" {
			groupRow(c, k, nil).Children(func() { rowHint(c, k, tn.Error) })
		}
	}
}

// tailnetReason is why a running node does not carry discovery, in the
// app's words where the broker names the cause.
func tailnetReason(tn *rex.TailnetStatus) string {
	switch tn.Reason {
	case rex.ReasonTagged:
		return locales.TailnetTagged()
	case rex.ReasonExpired:
		return locales.TailnetExpired()
	}
	return tn.Error
}

// tailnetSignIn asks the built-in node for a sign-in, off the UI thread,
// waits a little for its link and opens it in the browser. The link binds
// the device to whoever opens it first: it is never drawn or logged.
func (a *App) tailnetSignIn(svc ui.Services) {
	page := &remoteUI
	if page.signing {
		return
	}
	page.signing, page.signErr = true, ""
	update := a.post // no window in tests: the app's posts
	if win := settingsUI.win; win != nil {
		update = func(fn func()) { win.Update(fn) }
	}
	go func() {
		link, err := signInLink(signInWait, signInEvery)
		update(func() {
			page.signing = false
			switch {
			case err != nil:
				msg := serverMessage(err)
				page.signErr = locales.TailnetSignInFailed(msg)
			case link == "":
				page.signErr = locales.TailnetSignInNoLink()
			default:
				svc.OpenURL(link)
			}
		})
	}()
}

// signInLink starts a sign-in and polls for its link up to wait. It
// returns "" when the node gave none in time.
func signInLink(wait, every time.Duration) (string, error) {
	link, err := rexapi.StartTailscaleLogin()
	deadline := time.Now().Add(wait)
	for err == nil && link == "" && time.Now().Before(deadline) {
		time.Sleep(every)
		link, err = rexapi.TailscaleAuthURL()
	}
	if err != nil {
		return "", err
	}
	link = strings.TrimSpace(link)
	if link != "" && !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
		return "", errors.New("the link is not a web address")
	}
	return link, nil
}
