package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// paneExt is a pane that is not a terminal: a browser, a note, a pane of
// an extension pack (docs/EXT-001-extension-panes.md). It answers for the
// parts of the pane that are not a terminal's: its body and header, its
// fields of the layout, and the acts a pane kind takes part in. Pane.ext
// holds it, nil for a terminal.
type paneExt interface {
	// kind is the layout Kind it answers for: "note", "browser", or a
	// pack's kind name.
	kind() string
	// program is the pane's tile and glyph.
	program() program
	// label is the pane's name and detail in headers and sidebars.
	label(p *Pane) (name, detail string)
	// keepsHeader reports whether the pane keeps its header while the
	// paneHeaders setting is off.
	keepsHeader() bool
	// opaqueTiled reports whether a tiled pane paints an opaque card: a
	// web view does not blend under glass.
	opaqueTiled() bool
	// badge reports whether the activity badge shows in the header.
	badge() bool
	// body draws the pane's content.
	body(a *App, c *ui.Context, k *colors, t *Tab, p *Pane)
	// header draws the pane's header in place of the default one, and
	// reports whether it did; false gives the pane the default header.
	header(a *App, c *ui.Context, k *colors, t *Tab, p *Pane, focused, hovered bool) bool
	// headerActions draws the kind's buttons in the header's action row.
	headerActions(a *App, c *ui.Context, k *colors, t *Tab, p *Pane)
	// save writes the pane's fields into n, its layout node.
	save(p *Pane, n *savedNode)
	// apply reads s, a node of this kind in a layout the window reads,
	// into the live pane p.
	apply(a *App, p *Pane, s *savedNode)
	// clone makes a pane of this kind for a window or a host tab that p
	// moves to.
	clone(a *App, p *Pane) *Pane
	// destroy frees the pane's resources. kill reports that the pane's
	// session dies with it: a note's file goes then too.
	destroy(kill bool)
	// restart answers pane.restart on the pane.
	restart(a *App, p *Pane)
	// zoom answers font.bigger, font.smaller and font.reset, and reports
	// whether it took the command; false leaves it to the terminal.
	zoom(a *App, p *Pane, delta float32) bool
	// layoutSaved runs after the window wrote the pane's layout.
	layoutSaved(p *Pane)
	// webView is the pane's web view, nil for none; the debug hooks ask.
	webView() *mygo.WebView
}

// paneKindDef is a pane kind the app answers for, a built-in today and an
// extension pack's kind tomorrow. kind is the Kind of a layout node it
// serves; match reports whether s is a node of the kind, for the kinds
// whose node carries no Kind, as a browser's url.
type paneKindDef struct {
	kind    string
	match   func(s *savedNode) bool
	spawn   func(a *App) *Pane
	load    func(a *App, s *savedNode, taken func(*savedNode) bool) *Pane
	key     func(s *savedNode) string
	program program
}

// paneKinds are the app's built-in pane kinds; allPaneKinds adds the
// packs' to them.
// paneKinds are the app's built-in pane kinds, none today: every pane
// kind is a pack's.
var paneKinds []paneKindDef

// cmpKey is pre+id, "" when id is empty.
func cmpKey(pre, id string) string {
	if id == "" {
		return ""
	}
	return pre + id
}

// paneKindOf is the def of a node's kind, nil for a kind the build does
// not have. allPaneKinds adds the enabled packs' kinds to the built-ins.
func paneKindOf(s *savedNode) *paneKindDef {
	// A layout an old build wrote has a browser node of no kind: it is
	// the browser pack's kind.
	if s.Kind == "" && s.URL != "" {
		ns := *s
		ns.Kind = "browser"
		s = &ns
	}
	defs := allPaneKinds()
	for i := range defs {
		if defs[i].match(s) {
			return &defs[i]
		}
	}
	return nil
}

// extPaneLoad makes a pane of a layout node's kind, a placeholder for a
// kind the build does not have, or nil for a node that is no extension
// pane's.
func (a *App) extPaneLoad(s *savedNode, taken func(*savedNode) bool) *Pane {
	if d := paneKindOf(s); d != nil {
		return d.load(a, s, taken)
	}
	if s.Kind != "" && s.SID == "" && s.Host == "" {
		return a.newPlaceholderPane(s)
	}
	return nil
}

// newExtPane makes a pane of the pane kind named, nil for a kind the
// build does not have.
func (a *App) newExtPane(kind string) *Pane {
	for _, d := range allPaneKinds() {
		if d.kind == kind {
			return d.spawn(a)
		}
	}
	return nil
}

// newExtTab opens a tab of a new pane of kind after the active tab, and
// shows it.
func (a *App) newExtTab(kind string) *Tab {
	if p := a.newExtPane(kind); p != nil {
		return a.newPaneTab(p)
	}
	return nil
}

// key is the pane's paneKey.
func (p *Pane) key() string {
	n := savedNode{SID: p.SID}
	if p.host != nil {
		n.Host = p.host.ID
	}
	if p.ext != nil {
		p.ext.save(p, &n)
	}
	return savedNodeKey(&n)
}

// savedNodeKey is paneKey: the key of a node of a layout, by its kind,
// or by its session and host for a terminal.
func savedNodeKey(s *savedNode) string {
	if d := paneKindOf(s); d != nil {
		if k := d.key(s); k != "" {
			return k
		}
	}
	if s.SID != "" {
		return s.Host + "/" + s.SID
	}
	return ""
}

// extProgram is the tile of a layout node's pane, by its kind.
func extProgram(s savedNode) (program, bool) {
	if d := paneKindOf(&s); d != nil {
		return d.program, true
	}
	if s.Kind != "" && s.SID == "" {
		return extMissingProgram, true
	}
	return program{}, false
}

// extMissingProgram is the tile of a pane whose kind the build does not
// have.
var extMissingProgram = program{Name: "Extension", Glyph: "app-window", TileGlyph: "app-window", TileBg: ui.Hex("#8a919c"), TileFg: ui.Hex("#ffffff")}

// placeholderExt is a pane whose kind the build does not have: an
// extension pack serves it once installed. It keeps the node's fields to
// write them back, so saving a layout loses none of the pack's state.
type placeholderExt struct {
	kindName string
	keep     savedNode
}

// newPlaceholderPane makes a pane for s, a node of an unknown kind.
func (a *App) newPlaceholderPane(s *savedNode) *Pane {
	keep := *s
	keep.A, keep.B, keep.Cols, keep.Rows = nil, nil, 0, 0
	return &Pane{ID: a.id(), Name: s.Name, ext: &placeholderExt{kindName: s.Kind, keep: keep}}
}

func (e *placeholderExt) kind() string { return e.kindName }
func (e *placeholderExt) program() program {
	return extMissingProgram
}
func (e *placeholderExt) label(p *Pane) (name, detail string) {
	if p.Name != "" {
		return p.Name, ""
	}
	return locales.PaneExtMissing(e.kindName), ""
}
func (e *placeholderExt) keepsHeader() bool { return true }
func (e *placeholderExt) opaqueTiled() bool { return false }
func (e *placeholderExt) badge() bool       { return false }
func (e *placeholderExt) headerActions(*App, *ui.Context, *colors, *Tab, *Pane) {
}
func (e *placeholderExt) header(*App, *ui.Context, *colors, *Tab, *Pane, bool, bool) bool {
	return false
}
func (e *placeholderExt) body(a *App, c *ui.Context, k *colors, t *Tab, p *Pane) {
	draw := func(p *ui.Painter, r ui.Rect, k *colors) {
		glyphIn(p, "app-window", ui.Rect{X: r.X + r.W/2 - 12, Y: r.Y + r.H/2 - 12, W: 24, H: 24}, k.textFaint)
	}
	emptyPage(c, k, draw, locales.PaneExtMissing(e.kindName), locales.PaneExtMissingHint(), emptyAction{}, emptyAction{})
}
func (e *placeholderExt) save(p *Pane, n *savedNode) {
	keep := e.keep
	keep.A, keep.B = nil, nil
	keep.Name = p.Name
	*n = keep
}
func (e *placeholderExt) apply(a *App, p *Pane, s *savedNode) {
	e.keep = *s
	e.keep.A, e.keep.B = nil, nil
}
func (e *placeholderExt) clone(a *App, p *Pane) *Pane {
	return a.newPlaceholderPane(&e.keep)
}
func (e *placeholderExt) destroy(kill bool)                        {}
func (e *placeholderExt) restart(a *App, p *Pane)                  {}
func (e *placeholderExt) zoom(a *App, p *Pane, delta float32) bool { return false }
func (e *placeholderExt) layoutSaved(p *Pane)                      {}
func (e *placeholderExt) webView() *mygo.WebView                   { return nil }
