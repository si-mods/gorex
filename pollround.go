package main

import (
	"time"

	"gorex/internal/rex"
)

// pollRound takes a round of the poll of this machine's server into the
// windows, on the main thread. A round with news, a new list or the
// tailnet's status, gives every window the list (take); one without
// leaves the panes as they are, which hold that list already. Every round
// still saves a layout changed a while ago and retries a notification's
// click, and the badge, counted across all the windows, is counted once.
func (st *store) pollRound(news bool) {
	for _, a := range st.apps {
		if news {
			a.take(nil, st.infos)
		} else {
			a.saveIfDue()
		}
	}
	st.syncBadge()
	st.retryClick()
}

// apply takes what the server of host h, nil for this machine's, said of
// its sessions into the window, then shows the badge and retries a
// notification's click.
func (a *App) apply(h *hostConn, byID map[string]rex.SessionInfo) {
	a.take(h, byID)
	a.syncBadge()
	if a.st != nil {
		a.st.retryClick()
	}
}

// saveIfDue saves the layout once a change waited a second, and no
// gesture moves it.
func (a *App) saveIfDue() {
	if a.saveDue && time.Since(a.lastSave) > time.Second && !a.gesturing() {
		a.save()
	}
}
