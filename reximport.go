package main

import (
	"context"
	"sync/atomic"

	"github.com/egoist/mygo"

	"gorex/internal/rex"
	"gorex/internal/reximp"
	"gorex/internal/rexpeer"
	"gorex/locales"
)

// rexImporting tells that an import runs, so that a second action does
// not make the same spaces again.
var rexImporting atomic.Bool

// importRex makes spaces, tabs and panes of a Rex server's sessions, each
// block mirrored in its pane, as `gorex rex-import` does. The work runs
// in the background; the result comes as a notification, or a dialog when
// it fails.
func (a *App) importRex() {
	client := a.client
	if a.st == nil || client == nil {
		return
	}
	if !rexImporting.CompareAndSwap(false, true) {
		return
	}
	socket := rexpeer.DefaultSocket()
	if socket == "" {
		rexImporting.Store(false)
		mygo.Dialog.Error(locales.CmdImportRex(), locales.RexImportFailed("no Rex server was found"))
		return
	}
	exe, _ := rex.Executable()
	opts := reximp.Options{
		Mirror:    reximp.MirrorCommand(exe, socket),
		StatePath: reximp.StatePath(),
		RexSocket: socket,
	}
	go func() {
		defer rexImporting.Store(false)
		ctx := context.Background()
		peer, err := rexpeer.Dial(ctx, socket)
		if err != nil {
			a.post(func() {
				mygo.Dialog.Error(locales.CmdImportRex(), locales.RexImportFailed(err.Error()))
			})
			return
		}
		defer peer.Close()
		res, err := reximp.Run(ctx, client, peer, opts)
		if err != nil {
			a.post(func() {
				mygo.Dialog.Error(locales.CmdImportRex(), locales.RexImportFailed(err.Error()))
			})
			return
		}
		n := len(res.Imported)
		a.post(func() {
			body := locales.RexImportNone()
			if n > 0 {
				body = locales.RexImportDone(n)
			}
			mygo.NewNotification(mygo.NotificationOptions{
				Title: locales.CmdImportRex(),
				Body:  body,
			}).Show()
		})
	}()
}
