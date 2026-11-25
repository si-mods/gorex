package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/ext"
	"gorex/internal/layout"
	"gorex/locales"
)

// extPacks are the extension packs the app loaded at start: the enabled
// ones, whose pane kinds and commands it serves (docs/EXT-001-extension-panes.md).
var extPacks struct {
	packs     []*ext.Pack // enabled, in the order of the exts directory
	errs      []error     // what kept the other directories from loading
	cmds      []*command  // the commands the manifests declare
	tabCmds   []*command  // of them, the ones the New Tab menu shows
	splitCmds []*command  // and the ones the Split menu shows
}

// extsDirFn is the packs' install root: <config>/exts; the tests point
// it at theirs.
var extsDirFn = func() string { return ext.Dir(config.Dir()) }

// loadExtPacks loads the packs of the exts directory and registers their
// pane kinds and commands. It runs after loadSettings, which decides
// which packs are off, and before the menus and the layouts, which ask
// for their kinds; run again when the enabled set changes.
func loadExtPacks() {
	packs, errs := ext.LoadAll(extsDirFn())
	extPacks.packs, extPacks.errs = extPacks.packs[:0], errs
	for _, pk := range packs {
		if !slices.Contains(prefs.DisabledExts, pk.ID()) {
			extPacks.packs = append(extPacks.packs, pk)
		}
	}
	for _, c := range extPacks.cmds {
		delete(commandsByID, c.ID)
	}
	paletteCommands = slices.DeleteFunc(paletteCommands, func(c *command) bool {
		return slices.Contains(extPacks.cmds, c)
	})
	commands = slices.DeleteFunc(commands, func(c *command) bool {
		return slices.Contains(extPacks.cmds, c)
	})
	extPacks.cmds = extPacks.cmds[:0]
	for _, pk := range extPacks.packs {
		extPacks.cmds = append(extPacks.cmds, packCommands(pk)...)
	}
	extPacks.tabCmds, extPacks.splitCmds = nil, nil
	for _, c := range extPacks.cmds {
		if commandsByID[c.ID] != nil {
			log.Printf("extension: command %s collides with a built-in, dropped", c.ID)
			continue
		}
		commandsByID[c.ID] = c
		paletteCommands = append(paletteCommands, c)
		commands = append(commands, c)
		if c.extPlacement == "tab" {
			extPacks.tabCmds = append(extPacks.tabCmds, c)
		} else if c.extPlacement == "split" {
			extPacks.splitCmds = append(extPacks.splitCmds, c)
		}
	}
}

// reloadExtPacks serves the packs again after an install, a remove or a
// toggle: the pane kinds and the menus' commands follow at once.
func (a *App) reloadExtPacks() {
	loadExtPacks()
	if a.st != nil && a.st.refreshMenus != nil {
		a.st.refreshMenus()
	}
}

// packCommands builds the commands of the pack's manifest: pane-spawning
// ones the menus show by their placement, and ones that run a built-in
// action.
func packCommands(pk *ext.Pack) []*command {
	var out []*command
	for _, cd := range pk.Manifest.Commands {
		c := &command{
			ID:           "ext." + pk.ID() + "." + cd.ID,
			Name:         func(locales.Messages) string { return cd.Title },
			extPlacement: cd.Placement,
		}
		switch {
		case cd.Kind != "" && cd.Placement == "split":
			c.Run = func(a *App) { a.splitLocal(false, func() *Pane { return a.newExtPane(cd.Kind) }) }
			c.Args = []actionArg{{Name: "direction", Type: "string", Optional: true, Values: []string{"right", "down"}}}
			c.RunArgs = func(a *App, args map[string]any) error {
				a.splitLocal(argString(args, "direction") == "down", func() *Pane { return a.newExtPane(cd.Kind) })
				return nil
			}
			c.Available = splitsLocal
		case cd.Kind != "":
			c.Run = func(a *App) { a.newExtTab(cd.Kind) }
		case cd.Action != "":
			c.Run = func(a *App) { a.invokeAction(cd.Action, nil) }
			c.RunArgs = func(a *App, args map[string]any) error { return a.invokeAction(cd.Action, args) }
		}
		out = append(out, c)
	}
	return out
}

// invokeAction runs the built-in action id with args, as a pack command
// or the bridge's Invoke does.
func (a *App) invokeAction(id string, args map[string]any) error {
	cmd := lookupCommand(id)
	if cmd == nil {
		return nil
	}
	return cmd.invoke(a, args)
}

// packKindDefs are the pane kind defs of the enabled packs, after the
// built-ins of paneKinds.
func packKindDefs() []paneKindDef {
	var defs []paneKindDef
	for _, pk := range extPacks.packs {
		for i := range pk.Manifest.Panes {
			pk, pd := pk, &pk.Manifest.Panes[i]
			defs = append(defs, paneKindDef{
				kind:  pd.Kind,
				match: func(s *savedNode) bool { return s.Kind == pd.Kind },
				spawn: func(a *App) *Pane { return a.newExtPaneFor(pk, pd, "") },
				load: func(a *App, s *savedNode, taken func(*savedNode) bool) *Pane {
					return a.loadExtPaneFor(pk, pd, s, taken)
				},
				key: func(s *savedNode) string {
					if slices.Contains(pd.Fields, "id") {
						return cmpKey("ext:"+pd.Kind+":", s.ID)
					}
					return ""
				},
				program: packProgram(pd),
			})
		}
	}
	return defs
}

// allPaneKinds are the app's pane kinds, the built-ins and the packs'.
func allPaneKinds() []paneKindDef {
	return append(slices.Clone(paneKinds), packKindDefs()...)
}

// packProgram is the tile of a pack's pane kind: its glyph, or the
// extension glyph.
func packProgram(pd *ext.PaneDef) program {
	glyph := pd.Icon
	if glyph == "" {
		glyph = "app-window"
	}
	return program{Name: pd.Title, Glyph: glyph, TileGlyph: glyph, TileBg: ui.Hex("#8a919c"), TileFg: ui.Hex("#ffffff")}
}

// extCallers resolves the page that called a bound method to the pane it
// is in: the Extension bridge scopes its answers by it.
var (
	extCallersMu sync.RWMutex
	extCallers   = map[*mygo.Page]*extPane{}
)

// extPageURL is the file: URL of the pack's page, which a file origin
// lets call the bound bridge (docs/EXT-001, The pane's page).
func extPageURL(pk *ext.Pack) string {
	return (&url.URL{Scheme: "file", Path: filepath.Join(pk.Dir, "index.html")}).String()
}

// extStateFingerprint is the declared fields of s as JSON, which says
// whether the state a layout carries differs from the pane's.
func extStateFingerprint(s *savedNode, fields []string) string {
	b, _ := json.Marshal(extNodeFields(s, fields))
	return string(b)
}

// extNodeFields reads the declared state fields of s as a JSON object.
func extNodeFields(s *savedNode, fields []string) map[string]any {
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		switch f {
		case "note":
			m[f] = s.Note
		case "noteSize":
			m[f] = float64(s.NoteSize)
		case "notePreview":
			m[f] = s.NotePreview
		case "url":
			m[f] = s.URL
		case "id":
			m[f] = s.ID
		}
	}
	return m
}

// extSetFields writes each member of obj, a declared field of its JSON
// type, into s. "id" is the pane's own and no state of its pack's.
func extSetFields(s *savedNode, fields []string, obj map[string]any) error {
	for name, v := range obj {
		if name == "id" {
			return fmt.Errorf("state field %q is the pane's own", name)
		}
		if !slices.Contains(fields, name) {
			return fmt.Errorf("kind has no state field %q", name)
		}
		switch name {
		case "note", "url":
			str, ok := v.(string)
			if !ok {
				return fmt.Errorf("state field %q wants a string", name)
			}
			if name == "note" {
				s.Note = str
			} else {
				s.URL = str
			}
		case "noteSize":
			num, ok := v.(float64)
			if !ok {
				return fmt.Errorf("state field %q wants a number", name)
			}
			s.NoteSize = float32(num)
		case "notePreview":
			b, ok := v.(bool)
			if !ok {
				return fmt.Errorf("state field %q wants a boolean", name)
			}
			s.NotePreview = b
		}
	}
	return nil
}

// extPane is the pane an extension pack's kind serves: the pack's page
// in a web view, trusted so the Extension bridge answers it, and its web
// slot's untrusted page in another (docs/EXT-001).
type extPane struct {
	a    *App
	pack *ext.Pack
	def  *ext.PaneDef
	prog program
	// keep is the node's fields the pane writes back, Kind among them,
	// which the bridge's state reads and writes; synced is their JSON the
	// layout last wrote or read, so a node that differs is another
	// client's, which the pane takes.
	keep   savedNode
	synced string
	// web is the pack's page's web view; slot its untrusted web slot's,
	// for a pack that declares slots:["web"]; their titles are pageTitle
	// and slotTitle, which name the pane.
	web, slot            *mygo.WebView
	pageTitle, slotTitle string
	// env is the colors the pane's chrome last drew with, what the
	// bridge's Env answers with; a pointer, as the bridge's calls run
	// off the window's thread.
	env atomic.Pointer[map[string]any]
}

// newExtPaneFor makes a pane of the pack's pane kind. startURL is where
// its web slot begins, "" for about:blank.
func (a *App) newExtPaneFor(pk *ext.Pack, pd *ext.PaneDef, startURL string) *Pane {
	e := &extPane{a: a, pack: pk, def: pd, prog: packProgram(pd)}
	e.keep = savedNode{Kind: pd.Kind}
	if slices.Contains(pd.Fields, "id") {
		e.keep.ID = layout.NewTabID()
	}
	p := &Pane{ID: a.id(), ext: e}
	if a.win == nil {
		// A view without a window, as in tests: the pane keeps its
		// fields, and shows no page.
		return p
	}
	v, err := a.win.NewWebView(mygo.WebViewOptions{
		URL:             extPageURL(pk),
		BackgroundColor: "light-dark(#ffffff, #1e1e24)",
		// The page is the pack's own, trusted, so the bridge answers it:
		// what it may call is the manifest's, which the bridge enforces.
	})
	if err != nil {
		log.Printf("extension pane %q: %v", pd.Kind, err)
		a.err, a.errMsg = err.Error(), nil
		return p
	}
	e.web = v
	e.register()
	v.Page().OnPageTitleUpdated(func(ev *mygo.TitleEvent) {
		e.update(func() {
			if p.closed {
				return
			}
			e.pageTitle = strings.TrimSpace(ev.Title)
		})
	})
	if pk.Manifest.HasSlot(ext.SlotWeb) {
		e.newWebSlot(p, startURL)
	}
	return p
}

// newWebSlot makes the pane's untrusted web slot at startURL and wires
// its navigation to the same policy a browser pane's page answers.
func (e *extPane) newWebSlot(p *Pane, startURL string) {
	u := browserURL(startURL)
	if u == "" {
		u = browserStart
	}
	v, err := e.a.win.NewWebView(mygo.WebViewOptions{
		URL:             u,
		BackgroundColor: "light-dark(#ffffff, #1e1e24)",
		// The slot's page is someone else's, whatever its URL: it calls
		// nothing of GoRex's. The handlers below decide where it may go.
		Page: mygo.PageOptions{Untrusted: true, SwipeNavigation: true, PinchZoom: true},
	})
	if err != nil {
		log.Printf("extension pane %q web slot: %v", e.def.Kind, err)
		return
	}
	e.slot = v
	e.keep.URL = u
	page := v.Page()
	page.OnWillNavigate(func(ev *mygo.NavigateEvent) {
		if browserURL(ev.URL) == "" {
			ev.PreventDefault()
		}
	})
	page.OnDidNavigate(func(u string) {
		u = browserCleanURL(u)
		e.update(func() {
			if p.closed || p.ext == nil {
				return
			}
			e.keep.URL, p.title = u, ""
			e.a.changed()
			e.emitWeb(map[string]any{"url": u})
		})
	})
	page.OnPageTitleUpdated(func(ev *mygo.TitleEvent) {
		e.update(func() {
			if p.closed || p.ext == nil {
				return
			}
			e.slotTitle = strings.TrimSpace(ev.Title)
		})
	})
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		if u := browserURL(req.URL); req.UserInitiated && u != "" {
			page.LoadURL(u)
		}
		return nil
	})
}

// loadExtPaneFor makes a pane of s, a node of the pack's kind in a
// layout the window reads, reusing its id while no live pane has it.
func (a *App) loadExtPaneFor(pk *ext.Pack, pd *ext.PaneDef, s *savedNode, taken func(*savedNode) bool) *Pane {
	p := a.newExtPaneFor(pk, pd, s.URL)
	e := p.ext.(*extPane)
	e.keep = *s
	e.keep.A, e.keep.B, e.keep.Cols, e.keep.Rows = nil, nil, 0, 0
	if s.ID != "" && taken(s) {
		e.keep.ID = layout.NewTabID()
	}
	e.synced = extStateFingerprint(&e.keep, pd.Fields)
	p.Name = s.Name
	return p
}

// register marks the pane's page as a caller of the bridge.
func (e *extPane) register() {
	extCallersMu.Lock()
	extCallers[e.web.Page()] = e
	extCallersMu.Unlock()
}

// update runs fn on the window's thread, where the pane's fields change.
func (e *extPane) update(fn func()) {
	if e.a != nil && e.a.win != nil {
		e.a.win.Update(fn)
		return
	}
	fn()
}

// updateSync runs fn as update does, and returns after it ran: the
// bridge's answers need their call's outcome, which Update posts.
func (e *extPane) updateSync(fn func()) {
	done := make(chan struct{})
	e.update(func() {
		defer close(done)
		fn()
	})
	<-done
}

// The paneExt of an extension pack's pane.

func (e *extPane) kind() string     { return e.def.Kind }
func (e *extPane) program() program { return e.prog }

// label is the pane's name, or the page of its web slot, or its own
// page's, or the kind's title in the manifest.
func (e *extPane) label(p *Pane) (name, detail string) {
	name = p.Name
	if name == "" {
		name = e.slotTitle
	}
	if name == "" {
		name = e.pageTitle
	}
	if name == "" {
		name = e.def.Title
	}
	return name, browserHost(e.keep.URL)
}

func (e *extPane) keepsHeader() bool { return true }
func (e *extPane) opaqueTiled() bool { return true }
func (e *extPane) badge() bool       { return false }
func (e *extPane) headerActions(*App, *ui.Context, *colors, *Tab, *Pane) {
}
func (e *extPane) header(*App, *ui.Context, *colors, *Tab, *Pane, bool, bool) bool {
	return false
}

// body shows the pack's page, and its web slot under it when the
// manifest declares one, or a word about why it shows none.
func (e *extPane) body(a *App, c *ui.Context, k *colors, t *Tab, p *Pane) {
	e.captureEnv(k)
	if e.web == nil {
		emptyPage(c, k, func(p *ui.Painter, r ui.Rect, k *colors) {
			glyphIn(p, "app-window", ui.Rect{X: r.X + r.W/2 - 12, Y: r.Y + r.H/2 - 12, W: 24, H: 24}, k.textFaint)
		}, e.def.Title, locales.PaneExtMissingHint(), emptyAction{}, emptyAction{})
		return
	}
	focus := func(wv ui.Element, v *mygo.WebView) {
		if a.focusReq == p && a.tab() == t {
			v.Focus()
			a.focusReq = nil
		}
		if wv.Focused() {
			a.contentFocus = true
			p.attention = false
			if t.Focus != p {
				t.setFocus(p)
				a.changed()
			}
		}
		if wv.Hovered() {
			a.pageHover = p
		}
	}
	wv := ui.WebView(c, e.web)
	if e.slot == nil {
		wv.Grow(1).MinHeight(0)
	} else {
		h := e.def.ChromeHeight
		if h <= 0 {
			h = headerH
		}
		wv.Height(h)
	}
	focus(wv, e.web)
	if e.slot != nil {
		sv := ui.WebView(c, e.slot).Grow(1).MinHeight(0)
		focus(sv, e.slot)
	}
}

// captureEnv keeps the colors the pane's chrome draws with, for the
// bridge's Env to give the page.
func (e *extPane) captureEnv(k *colors) {
	e.env.Store(&map[string]any{
		"dark": cIsDark(k),
		"text": uiHex(k.text), "textMuted": uiHex(k.textMuted),
		"textFaint": uiHex(k.textFaint), "surface": uiHex(k.surface),
		"surfaceHover": uiHex(k.surfaceHover), "accent": uiHex(k.accent),
		"accentText": uiHex(k.accentText), "panel": uiHex(k.panel),
		"panelBorder": uiHex(k.panelBorder),
	})
}

// uiHex is c as "#rrggbb".
func uiHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// cIsDark reads a dark palette off the colors: a surface darker than its
// text is a dark chrome's.
func cIsDark(k *colors) bool {
	lum := func(c ui.Color) float64 {
		return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
	}
	return lum(k.surface) < lum(k.text)
}

func (e *extPane) save(p *Pane, n *savedNode) {
	keep := e.keep
	keep.A, keep.B, keep.Cols, keep.Rows = nil, nil, 0, 0
	keep.Name = p.Name
	*n = keep
}

// apply takes the state of a node another client wrote: the pane's own
// since it last wrote the layout stays.
func (e *extPane) apply(a *App, p *Pane, s *savedNode) {
	fp := extStateFingerprint(s, e.def.Fields)
	if fp == e.synced {
		return
	}
	e.keep = *s
	e.keep.A, e.keep.B, e.keep.Cols, e.keep.Rows = nil, nil, 0, 0
	e.synced = fp
	e.emitState()
}

func (e *extPane) clone(a *App, p *Pane) *Pane {
	np := a.newExtPaneFor(e.pack, e.def, e.keep.URL)
	np.Name = p.Name
	ne := np.ext.(*extPane)
	keep := e.keep
	if ne.keep.ID != "" {
		keep.ID = ne.keep.ID // the clone keeps the id it spawned with
	}
	ne.keep = keep
	return np
}

func (e *extPane) destroy(kill bool) {
	extCallersMu.Lock()
	if e.web != nil {
		delete(extCallers, e.web.Page())
	}
	extCallersMu.Unlock()
	if e.web != nil {
		e.web.Destroy()
		e.web = nil
	}
	if e.slot != nil {
		e.slot.Destroy()
		e.slot = nil
	}
}

func (e *extPane) restart(a *App, p *Pane) {
	if e.web != nil {
		e.web.Page().Reload()
	}
}

func (e *extPane) zoom(a *App, p *Pane, delta float32) bool { return false }

func (e *extPane) layoutSaved(p *Pane) { e.synced = extStateFingerprint(&e.keep, e.def.Fields) }

func (e *extPane) webView() *mygo.WebView { return e.web }
