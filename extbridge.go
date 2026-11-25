package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"

	"gorex/internal/ext"
	"gorex/internal/rex"
)

// extensionBridge is the bound service a pack's page calls as
// window.mygo.Extension (docs/EXT-001-extension-panes.md): each method
// resolves the calling page to the pane it is in, and answers only what
// the pack's manifest permits. Calls run on their own goroutines; the
// pane's fields change on the window's thread, through e.update.
type extensionBridge struct{}

var (
	// extStateEvent is "ext.state", which a pane's page hears when a
	// layout another client wrote changes its state.
	extStateEvent = mygo.NewEvent[map[string]any]("ext.state")
	// extWebEvent is "ext.web", which reports the web slot's page: where
	// it went.
	extWebEvent = mygo.NewEvent[map[string]any]("ext.web")
	// extCommandEvent is "ext.command": the event a pack command of
	// manifest "event" sends to the focused pane's page.
	extCommandEvent = mygo.NewEvent[string]("ext.command")
)

// emitState sends the pane's state to its page.
func (e *extPane) emitState() {
	if e.web != nil {
		extStateEvent.EmitPage(e.web.Page(), extNodeFields(&e.keep, e.def.Fields))
	}
}

// emitWeb sends the web slot's page, where it went, to the pane's page.
func (e *extPane) emitWeb(v map[string]any) {
	if e.web != nil {
		extWebEvent.EmitPage(e.web.Page(), v)
	}
}

// caller resolves the calling page to the pane whose page it is, and
// checks that the pack holds the permission the call needs.
func (extensionBridge) caller(ctx context.Context, perm string) (*extPane, error) {
	page := mygo.CallerPage(ctx)
	if page == nil {
		return nil, errors.New("no calling page")
	}
	extCallersMu.RLock()
	e := extCallers[page]
	extCallersMu.RUnlock()
	if e == nil {
		return nil, errors.New("the page is no extension pane's")
	}
	if perm != "" && !e.pack.Manifest.Has(perm) {
		return nil, fmt.Errorf("pack %q holds no %q permission", e.pack.ID(), perm)
	}
	return e, nil
}

// maxStateBytes caps what SetState writes into a layout node.
const maxStateBytes = 64 << 10

// maxStorageFileBytes caps what ReadFile reads of the pack's storage.
const maxStorageFileBytes = 4 << 20

// State is the pane's state: the fields its kind declares, from the
// node the layout keeps.
func (extensionBridge) State(ctx context.Context) (map[string]any, error) {
	e, err := extensionBridge{}.caller(ctx, ext.PermState)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	e.updateSync(func() { out = extNodeFields(&e.keep, e.def.Fields) })
	return out, nil
}

// SetState writes the fields of obj into the pane's state, each a
// declared field of its JSON type; the layout keeps them from the next
// save.
func (extensionBridge) SetState(ctx context.Context, obj map[string]any) error {
	e, err := extensionBridge{}.caller(ctx, ext.PermState)
	if err != nil {
		return err
	}
	if raw, err := json.Marshal(obj); err != nil || len(raw) > maxStateBytes {
		return fmt.Errorf("state exceeds %d bytes", maxStateBytes)
	}
	e.updateSync(func() {
		if err = extSetFields(&e.keep, e.def.Fields, obj); err == nil {
			e.a.changed()
		}
	})
	return err
}

// storageRoot is the pack's storage directory, made on use.
func (e *extPane) storageRoot() (string, error) {
	root := e.pack.Manifest.StorageDir(rex.Dir())
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

// jail is the file name inside the pack's storage, or its error.
func (e *extPane) jail(name string) (string, error) {
	root, err := e.storageRoot()
	if err != nil {
		return "", err
	}
	return ext.JailPath(root, name)
}

// ReadFile reads the file name inside the pack's storage directory.
func (extensionBridge) ReadFile(ctx context.Context, name string) (string, error) {
	e, err := extensionBridge{}.caller(ctx, ext.PermStorage)
	if err != nil {
		return "", err
	}
	p, err := e.jail(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if len(b) > maxStorageFileBytes {
		return "", fmt.Errorf("file %q exceeds %d bytes", name, maxStorageFileBytes)
	}
	return string(b), nil
}

// WriteFile writes data to the file name inside the pack's storage
// directory, making the folders it names.
func (extensionBridge) WriteFile(ctx context.Context, name, data string) error {
	e, err := extensionBridge{}.caller(ctx, ext.PermStorage)
	if err != nil {
		return err
	}
	if len(data) > maxStorageFileBytes {
		return fmt.Errorf("file %q exceeds %d bytes", name, maxStorageFileBytes)
	}
	p, err := e.jail(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(data), 0o644)
}

// DeleteFile removes the file name inside the pack's storage directory.
func (extensionBridge) DeleteFile(ctx context.Context, name string) error {
	e, err := extensionBridge{}.caller(ctx, ext.PermStorage)
	if err != nil {
		return err
	}
	p, err := e.jail(name)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return err
}

// ListFiles names the files inside the pack's storage directory,
// relative to it.
func (extensionBridge) ListFiles(ctx context.Context) ([]string, error) {
	e, err := extensionBridge{}.caller(ctx, ext.PermStorage)
	if err != nil {
		return nil, err
	}
	root, err := e.storageRoot()
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// Invoke runs the built-in action the manifest's commands name.
func (extensionBridge) Invoke(ctx context.Context, action string, args map[string]any) error {
	e, err := extensionBridge{}.caller(ctx, ext.PermInvoke)
	if err != nil {
		return err
	}
	ok := slices.ContainsFunc(e.pack.Manifest.Commands, func(c ext.CommandDef) bool {
		return c.Action == action
	})
	if !ok {
		return fmt.Errorf("pack %q declares no action %q", e.pack.ID(), action)
	}
	var ierr error
	e.updateSync(func() { ierr = e.a.invokeAction(action, args) })
	return ierr
}

// Env is what the page shows of the app's look: its language and
// appearance settings, its theme's name, and the colors it last drew
// the pane's chrome with (the page's draw caches them on the pane).
func (extensionBridge) Env(ctx context.Context) (map[string]any, error) {
	e, err := extensionBridge{}.caller(ctx, ext.PermEnv)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"locale":     prefs.Language,
		"appearance": prefs.Appearance,
		"theme":      prefs.Theme,
	}
	if cs := e.env.Load(); cs != nil {
		out["colors"] = *cs
	}
	return out, nil
}

// WebNavigate takes the pane's web slot to the URL, under the same
// policy a browser pane's page answers (docs/EXT-001, The web slot).
func (extensionBridge) WebNavigate(ctx context.Context, raw string) error {
	e, err := extensionBridge{}.caller(ctx, ext.PermWeb)
	if err != nil {
		return err
	}
	u := browserURL(raw)
	if u == "" {
		return fmt.Errorf("%q is no web page", raw)
	}
	e.update(func() {
		e.keep.URL = browserCleanURL(u)
		e.a.changed()
		if e.slot != nil {
			e.slot.Page().LoadURL(u)
		}
	})
	return nil
}

// WebURL is where the pane's web slot is.
func (extensionBridge) WebURL(ctx context.Context) (string, error) {
	e, err := extensionBridge{}.caller(ctx, ext.PermWeb)
	if err != nil {
		return "", err
	}
	var u string
	e.updateSync(func() { u = e.keep.URL })
	return u, nil
}

// WebBack takes the pane's web slot a page back.
func (extensionBridge) WebBack(ctx context.Context) error {
	return extensionBridge{}.webAct(ctx, func(p *mygo.Page) { p.GoBack() })
}

// WebForward takes the pane's web slot a page forward.
func (extensionBridge) WebForward(ctx context.Context) error {
	return extensionBridge{}.webAct(ctx, func(p *mygo.Page) { p.GoForward() })
}

// WebReload loads the pane's web slot's page again.
func (extensionBridge) WebReload(ctx context.Context) error {
	return extensionBridge{}.webAct(ctx, func(p *mygo.Page) { p.Reload() })
}

// webAct runs fn on the pane's web slot's page, while it has one.
func (extensionBridge) webAct(ctx context.Context, fn func(*mygo.Page)) error {
	e, err := extensionBridge{}.caller(ctx, ext.PermWeb)
	if err != nil {
		return err
	}
	e.update(func() {
		if e.slot != nil {
			fn(e.slot.Page())
		}
	})
	return nil
}
