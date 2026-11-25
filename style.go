package main

import (
	_ "embed"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"

	"gorex/internal/theme"
)

// colors are the app's own, in light and dark windows.
type colors struct {
	// The window's background: a gradient from top through mid to bottom,
	// and a tint toward the right.
	bgTop, bgMid, bgLow, bgBottom, bgTint ui.Color

	card, cardFocused, cardBorder, cardBorderFocused ui.Color
	shadow, shadowFocused                            ui.Color

	text, textMuted, textFaint, iconMuted ui.Color
	hover, pressed                        ui.Color
	// emptyMark is the slate of an empty state's picture: its placeholder
	// bars and its one accent.
	emptyMark ui.Color

	track, trackBorder, tabActive, tabSep ui.Color
	// pillFace and pillShadow are the active tab's pill under the glass
	// setting: its face where the system draws no glass, and its shadow.
	pillFace, pillShadow ui.Color
	// tabHover is a hovered tab's face under the glass setting, as Rex's:
	// lighter in a dark window, darker in a light one, and no glass.
	tabHover ui.Color
	// pillTint tints the system's glass of the active tab's pill.
	pillTint ui.Color
	// groupFace is the face of the sidebar's current project, its row and
	// tabs; pillLift the active tab's pill on it, lighter than pillFace,
	// and liftShadow its shadow, so that it stands over the group's face.
	groupFace, pillLift, liftShadow ui.Color

	busy, attention, done, exited, danger ui.Color

	// panel is the face of popovers, dialogs and the rows that float over
	// a pane: opaque, so that the text they cover shows through nowhere.
	panel, panelBorder, panelSel, backdrop ui.Color
	// panelGlass is panel as the palette lays it over the blurred window,
	// translucent where panel is opaque, and as a panel lays it over
	// MyGo's glass (glassPanel): dense enough that k.text and k.textMuted
	// read on it whatever is behind it.
	panelGlass ui.Color
	// field is the face of a text field, which is opaque as panel, and its
	// outline.
	field, fieldBorder ui.Color

	// accent is the color of what is chosen or on; panelSel is its wash.
	accent, accentHover, accentPressed ui.Color
	// accentText is the color of text and glyphs on the accent faces.
	accentText ui.Color
	// surface is the face of controls, opaque as the popovers' panel.
	surface, surfaceHover, surfacePressed ui.Color

	// The faces and the line of docs/DSK-012-theme-shaders.md's roles: the
	// title bar, the sidebar column, a pane's header, the accent faces of
	// the buttons, and a pane card's border. The built-in palettes have
	// none (ui.Transparent): where one is unset, the app draws as it did
	// before.
	titleBarFace, sideBarFace, headerFace, buttonFace, paneBorder ui.Color

	// m are the theme's metrics: the corner scale, border width, chrome
	// font and shadows the palette draws with.
	m metrics

	// win is the window's palette when this one is a face's (themedFace):
	// the same faces and panels with the ladder of the face. nil in the
	// window's own palette.
	win *colors
}

// window returns the palette of k's window: k itself, or the palette a
// face's palette came from. A popover or a menu opened from a face draws
// on the window's panel, so it takes the window's ladder, not the face's.
func (k *colors) window() *colors {
	if k.win != nil {
		return k.win
	}
	return k
}

// metrics are the sizes of docs/DSK-012-theme-shaders.md, Metrics, as the
// palette draws with them. builtinMetrics change nothing.
type metrics struct {
	// radius scales the radius tokens, the capsules and the panes' corners.
	radius float32
	// border is the width of a pane card's, a panel's and a field's border.
	border float32
	// font is the family of the chrome's text.
	font string
	// shadowScale scales a raised layer's shadow; shadowHard makes its
	// blur 0.
	shadowScale float32
	shadowHard  bool
}

// builtinMetrics are the sizes with no theme, or a theme with no metrics.
var builtinMetrics = metrics{radius: 1, border: 1, font: uiFont, shadowScale: 1}

// metricsOf are the metrics of a theme's metrics key: the font the first
// family of its list that is installed.
func metricsOf(m *theme.Metrics) metrics {
	return metrics{
		radius: m.Radius, border: m.Border, font: chromeFont(m.Font),
		shadowScale: m.Shadow.Scale, shadowHard: m.Shadow.Hard,
	}
}

// seeds are what a palette is made of (docs/DSK-008-theme.md, Model): the
// colors no rule makes from the others, one set for each appearance. The
// faces and lines of light over a face are ink: derive makes them.
type seeds struct {
	// The window's background, as in colors.
	bgTop, bgMid, bgLow, bgBottom, bgTint ui.Color

	text, textMuted, textFaint, iconMuted, emptyMark ui.Color

	busy, attention, done, exited, danger ui.Color

	accent, accentHover, accentPressed ui.Color
	accentText                         ui.Color

	// panel is the opaque face of popovers and dialogs; its lightness
	// picks the ink steps.
	panel, field, fieldBorder             ui.Color
	surface, surfaceHover, surfacePressed ui.Color

	// Tinted, not black or white ink: the light palette's shadows, backdrop
	// and pill tint, and the dark palette's cards.
	card, cardFocused, shadow, shadowFocused, backdrop, pillTint ui.Color
}

// inkSteps are the alphas of the ink derive lays: contrast ink (black on
// a light palette, white on a dark one), white, black, and the alphas of
// panel and accent. A step of 0 is ui.Transparent.
type inkSteps struct {
	// Contrast ink.
	hover, pressed, tabHover, tabSep, groupFace, panelBorder float32
	// White ink.
	cardBorder, cardBorderFocused, track, trackBorder, tabActive, pillFace, pillShadow, pillLift float32
	// Black ink.
	liftShadow float32
	// panel and accent at an alpha.
	panelGlass, panelSel float32
}

var lightInk = inkSteps{
	hover: 0.055, pressed: 0.1, tabHover: 0.05, tabSep: 0.13, groupFace: 0.045, panelBorder: 0.08,
	cardBorder: 0.72, cardBorderFocused: 1, track: 0.34, trackBorder: 0.45, tabActive: 0.97, pillFace: 0.45, pillShadow: 0.06, pillLift: 0.92,
	liftShadow: 0.06,
	panelGlass: 0.80, panelSel: 0.12,
}

var darkInk = inkSteps{
	hover: 0.08, pressed: 0.14, tabHover: 0.10, tabSep: 0.14, groupFace: 0.05, panelBorder: 0.1,
	cardBorder: 0.05, cardBorderFocused: 0.14, track: 0.06, trackBorder: 0.06, tabActive: 0.14, pillFace: 0.14, pillShadow: 0, pillLift: 0.24,
	liftShadow: 0.18,
	panelGlass: 0.78, panelSel: 0.18,
}

// ink returns c at alpha a, or ui.Transparent when a is 0.
func ink(c ui.Color, a float32) ui.Color {
	if a == 0 {
		return ui.Transparent
	}
	return ui.RGBA(c.R, c.G, c.B, a)
}

// derive makes the palette of s: the seeds as they are, and the ink at
// the steps of the lightness of s.panel.
func derive(s seeds) colors {
	st, contrast := lightInk, ui.RGBA(0, 0, 0, 1)
	if isDark(s.panel) {
		st, contrast = darkInk, ui.RGBA(255, 255, 255, 1)
	}
	white, black := ui.RGBA(255, 255, 255, 1), ui.RGBA(0, 0, 0, 1)
	return colors{
		bgTop: s.bgTop, bgMid: s.bgMid, bgLow: s.bgLow, bgBottom: s.bgBottom, bgTint: s.bgTint,

		card: s.card, cardFocused: s.cardFocused,
		cardBorder: ink(white, st.cardBorder), cardBorderFocused: ink(white, st.cardBorderFocused),
		shadow: s.shadow, shadowFocused: s.shadowFocused,

		text: s.text, textMuted: s.textMuted, textFaint: s.textFaint, iconMuted: s.iconMuted,
		hover: ink(contrast, st.hover), pressed: ink(contrast, st.pressed),
		emptyMark: s.emptyMark,

		track: ink(white, st.track), trackBorder: ink(white, st.trackBorder),
		tabActive: ink(white, st.tabActive), tabSep: ink(contrast, st.tabSep),
		pillFace: ink(white, st.pillFace), pillShadow: ink(white, st.pillShadow),
		tabHover: ink(contrast, st.tabHover), pillTint: s.pillTint,
		groupFace: ink(contrast, st.groupFace), pillLift: ink(white, st.pillLift), liftShadow: ink(black, st.liftShadow),

		busy: s.busy, attention: s.attention, done: s.done, exited: s.exited, danger: s.danger,

		panel: s.panel, panelBorder: ink(contrast, st.panelBorder), panelSel: ink(s.accent, st.panelSel), backdrop: s.backdrop,
		panelGlass: ink(s.panel, st.panelGlass),
		field:      s.field, fieldBorder: s.fieldBorder,

		accent: s.accent, accentHover: s.accentHover, accentPressed: s.accentPressed, accentText: s.accentText,
		surface: s.surface, surfaceHover: s.surfaceHover, surfacePressed: s.surfacePressed,

		m: builtinMetrics,
	}
}

var lightSeeds = seeds{
	bgTop:    ui.Hex("#f6e7f6"),
	bgMid:    ui.Hex("#efe1e6"),
	bgLow:    ui.Hex("#eee0d3"),
	bgBottom: ui.Hex("#ece1bd"),
	bgTint:   ui.RGBA(242, 214, 222, 0.35),

	text:      ui.Hex("#1d1d1f"),
	textMuted: ui.Hex("#636366"),
	textFaint: ui.Hex("#8e8e93"),
	iconMuted: ui.Hex("#8a8a8f"),
	emptyMark: ui.Hex("#7a869d"),

	busy:      ui.Hex("#34a853"),
	danger:    ui.Hex("#ef4444"),
	attention: ui.Hex("#f59e0b"),
	done:      ui.Hex("#7aa0c2"),
	exited:    ui.Hex("#a1a1a6"),

	accent: ui.Hex("#2e6fd0"), accentHover: ui.Hex("#2a64bd"), accentPressed: ui.Hex("#2459a8"), accentText: ui.Hex("#ffffff"),

	panel: ui.Hex("#fcfcfd"), field: ui.Hex("#f4f4f5"), fieldBorder: ui.Hex("#d9d9de"),
	surface: ui.Hex("#fcfcfd"), surfaceHover: ui.Hex("#efeff2"), surfacePressed: ui.Hex("#e3e3e8"),

	card:          ui.RGBA(255, 255, 255, 0.66),
	cardFocused:   ui.RGBA(255, 255, 255, 0.95),
	shadow:        ui.RGBA(60, 40, 50, 0.07),
	shadowFocused: ui.RGBA(60, 40, 50, 0.12),
	backdrop:      ui.RGBA(40, 20, 40, 0.06),
	pillTint:      ui.RGBA(29, 29, 29, 0.25),
}

var darkSeeds = seeds{
	bgTop:    ui.Hex("#2c2131"),
	bgMid:    ui.Hex("#231e27"),
	bgLow:    ui.Hex("#221f22"),
	bgBottom: ui.Hex("#2a2619"),
	bgTint:   ui.RGBA(80, 40, 60, 0.25),

	text:      ui.Hex("#f2f2f7"),
	textMuted: ui.Hex("#aeaeb2"),
	textFaint: ui.Hex("#8e8e93"),
	iconMuted: ui.Hex("#98989d"),
	emptyMark: ui.Hex("#9aa4b8"),

	busy:      ui.Hex("#4ade80"),
	danger:    ui.Hex("#ef4444"),
	attention: ui.Hex("#fbbf24"),
	done:      ui.Hex("#7aa0c2"),
	exited:    ui.Hex("#636366"),

	accent: ui.Hex("#6cb2ff"), accentHover: ui.Hex("#82bfff"), accentPressed: ui.Hex("#5aa2f0"), accentText: ui.Hex("#10151c"),

	panel: ui.Hex("#26262a"), field: ui.Hex("#2d2d32"), fieldBorder: ui.Hex("#3f3f46"),
	surface: ui.Hex("#262629"), surfaceHover: ui.Hex("#303035"), surfacePressed: ui.Hex("#3a3a40"),

	card:          ui.RGBA(20, 20, 24, 0.5),
	cardFocused:   ui.RGBA(36, 36, 41, 0.94),
	shadow:        ui.RGBA(0, 0, 0, 0.25),
	shadowFocused: ui.RGBA(0, 0, 0, 0.4),
	backdrop:      ui.RGBA(0, 0, 0, 0.2),
	pillTint:      ui.Transparent,
}

// The built-in palettes, in light and dark windows.
var (
	lightColors = derive(lightSeeds)
	darkColors  = derive(darkSeeds)
)

// themePalettes are the palettes the windows draw with: the built-in ones,
// or those of the theme appearance.theme names. The user's own
// *.colorCustomizations go over them where they draw (sidebarPalette).
// glass is the theme's glass key, nil without one, and name the theme's
// name, for the Settings page to say what decides the glass.
type themePalettes struct {
	light, dark colors
	glass       *bool
	name        string
	// themed tells that a theme made them, not the built-in seeds alone.
	themed bool
}

// palettes holds the windows' palettes. applyTheme stores a new value, never
// changes the one stored, so a window that draws on its own goroutine
// reads one whole palette while the theme changes; the caches keyed by a
// palette (menuStyleOf, spaceColors) see a new address.
// Until applyTheme stores one, it holds nil, which is builtinPalettes.
var palettes atomic.Pointer[themePalettes]

var builtinPalettes = themePalettes{light: lightColors, dark: darkColors}

// paletteOf returns the windows' palette of an appearance.
func paletteOf(dark bool) *colors {
	p := palettes.Load()
	if p == nil {
		p = &builtinPalettes
	}
	if dark {
		return &p.dark
	}
	return &p.light
}

// colorsOf returns the palette of c's appearance. Reduce Transparency
// changes none of it: every face of the palette lies on the window's own
// opaque gradient, not on a system material (docs/DSK-008-theme.md).
func colorsOf(c *ui.Context) *colors {
	return paletteOf(c.Theme().Dark)
}

// seedField returns the field of s that the seed id names.
func seedField(s *seeds, id theme.Seed) *ui.Color {
	return [theme.NumSeeds]*ui.Color{
		&s.bgTop, &s.bgMid, &s.bgLow, &s.bgBottom, &s.bgTint,
		&s.text, &s.textMuted, &s.textFaint, &s.iconMuted, &s.emptyMark,
		&s.busy, &s.attention, &s.done, &s.exited, &s.danger,
		&s.accent, &s.accentHover, &s.accentPressed, &s.accentText,
		&s.panel, &s.field, &s.fieldBorder, &s.surface, &s.surfaceHover, &s.surfacePressed,
		&s.card, &s.cardFocused, &s.shadow, &s.shadowFocused, &s.backdrop, &s.pillTint,
	}[id]
}

// roleField returns the field of k that the role names.
func roleField(k *colors, r theme.Role) *ui.Color {
	return [theme.NumRoles]*ui.Color{
		theme.ActiveGroupBackground:    &k.groupFace,
		theme.ActiveGroupTabBackground: &k.pillLift,
		theme.ActiveGroupTabShadow:     &k.liftShadow,
		theme.TrackBackground:          &k.track,
		theme.TabSeparator:             &k.tabSep,
		theme.TabHoverBackground:       &k.tabHover,
		theme.TabActiveBackground:      &k.tabActive,
		theme.TitleBarBackground:       &k.titleBarFace,
		theme.SideBarBackground:        &k.sideBarFace,
		theme.PaneHeaderBackground:     &k.headerFace,
		theme.ButtonBackground:         &k.buttonFace,
		theme.PaneBorder:               &k.paneBorder,
	}[r]
}

// themed resolves the first three layers of docs/DSK-008-theme.md for one
// appearance: the built-in seeds, the theme's seeds over them, derive, and
// the theme's roles over the result. i is 0 for light, 1 for dark.
func themed(base seeds, m *theme.Manifest, i int) colors {
	if m == nil {
		return derive(base)
	}
	k := derive(themeSeeds(base, m, i))
	for r, c := range m.Roles[i] {
		*roleField(&k, r) = c
	}
	if m.Metrics != nil {
		k.m = metricsOf(m.Metrics)
	}
	return k
}

// themeSeeds returns base with the theme's seeds of an appearance over
// it: what a theme's shaders read as Seed<Name>.
func themeSeeds(base seeds, m *theme.Manifest, i int) seeds {
	for id, c := range m.Seeds[i] {
		*seedField(&base, id) = c
	}
	return base
}

// themeTerm is the Ghostty theme of the terminals that the theme names for
// each appearance, light then dark, or "" for none. withPrefs applies it
// when the user has no terminal.theme (docs/DSK-008-theme.md, Terminal).
var themeTerm [2]string

// themeSidebar is the sidebar.* of the manifest in use: the sidebar's
// resolvers read it under the user's own sidebar.* keys (docs/DSK-008).
var themeSidebar theme.Sidebar

// applyTheme makes the windows' palettes those of m, or the built-in ones
// when m is nil, and drops what the caches made from the old ones. The
// terminals take m's colors when the caller applies the prefs again.
func applyTheme(m *theme.Manifest) {
	p := &themePalettes{light: themed(lightSeeds, m, 0), dark: themed(darkSeeds, m, 1)}
	if m != nil {
		p.themed = true
		p.glass, p.name = m.Glass, m.Name
		if p.name == "" {
			p.name = prefs.Theme
		}
	}
	palettes.Store(p)
	lookGen.Add(1)
	setThemeShaders(m)
	themeTerm = [2]string{}
	themeSidebar = theme.Sidebar{}
	if m != nil {
		themeTerm = m.Terminal
		themeSidebar = m.Sidebar
	}
	menuStylesMu.Lock()
	clear(menuStyles)
	menuStylesMu.Unlock()
	faceCacheMu.Lock()
	clear(faceCache)
	faceCacheMu.Unlock()
	inkMu.Lock()
	clear(inkLadders)
	inkMu.Unlock()
	spaceColorsMu.Lock()
	clear(spaceColorsCache)
	spaceColorsMu.Unlock()
}

// isDark reports whether c is darker than the midpoint, as a face is.
func isDark(c ui.Color) bool {
	return int(c.R)*299+int(c.G)*587+int(c.B)*114 < 128*1000
}

// ladderOf returns the palette whose text ladder reads on a face of the
// given lightness: k itself when that is k's own lightness, else the
// palette of the other appearance.
func ladderOf(k *colors, dark bool) *colors {
	if dark == isDark(k.panel) {
		return k
	}
	return paletteOf(dark)
}

// faceLadder returns the palette whose text ladder reads on a face of k,
// face its color laid over k's panel (docs/DSK-008-theme.md, Pairing).
// With no theme it is the ladder of the face's lightness (ladderOf), as it
// always was. Under a theme it is the first of these whose text reaches
// 4.5:1 and textMuted 3:1 on the face: the theme's ladder of the face's
// lightness, the theme's other one, the built-in ladder of the face's
// lightness, the built-in other one, and ink, white or black, made for the
// face (inkLadder). A light-only theme, whose dark seeds are its light
// ones, thus gets white text on a dark face rather than its dark text.
//
// same is the caller's palette of the face's lightness, which it took
// before the rule: ladderOf(k, dark), or paletteOf(dark) for themedFace.
func faceLadder(k *colors, face ui.Color, same *colors) *colors {
	if p := palettes.Load(); p == nil || !p.themed {
		return same
	}
	dark := isDark(face)
	return pickLadder(face, same, ladderOf(k, !dark), builtinLadder(dark), builtinLadder(!dark))
}

// builtinLadder returns the built-in palette of an appearance.
func builtinLadder(dark bool) *colors {
	if dark {
		return &darkColors
	}
	return &lightColors
}

// ladderReads reports how well ref's ladder reads on face: its worse margin
// of text over 4.5:1 and textMuted over 3:1, 1 or more where both pass.
func ladderReads(ref *colors, face ui.Color) float64 {
	return min(contrast(ref.text.Over(face), face)/faceTextContrast, contrast(ref.textMuted.Over(face), face)/faceMutedContrast)
}

// pickLadder returns the first of refs whose ladder reads on face, else the
// ink ladder of face (inkLadder), or, should it not read either, the one
// of them that reads best.
func pickLadder(face ui.Color, refs ...*colors) *colors {
	best, score := refs[0], -1.0
	for _, ref := range refs {
		s := ladderReads(ref, face)
		if s >= 1 {
			return ref
		}
		if s > score {
			best, score = ref, s
		}
	}
	ink := inkLadder(face)
	if ladderReads(ink, face) > score {
		return ink
	}
	return best
}

// inkLadders are the ink ladders inkLadder made, by face.
var (
	inkMu      sync.Mutex
	inkLadders = map[ui.Color]*colors{}
)

// inkLadder returns a ladder made for face: text in the ink, white or
// black, that contrasts more with it, and the muted steps that ink laid
// over the face at the least alpha from 0.72 up that keeps textMuted 3:1.
// The palette is the same pointer for the same face.
func inkLadder(face ui.Color) *colors {
	inkMu.Lock()
	defer inkMu.Unlock()
	if k, ok := inkLadders[face]; ok {
		return k
	}
	ink, st := ui.RGBA(0, 0, 0, 1), lightInk
	if contrast(ui.RGBA(255, 255, 255, 1), face) > contrast(ink, face) {
		ink, st = ui.RGBA(255, 255, 255, 1), darkInk
	}
	a := float32(0.72)
	for a < 1 && contrast(ink.Alpha(a).Over(face), face) < faceMutedContrast {
		a += 0.04
	}
	muted := ink.Alpha(min(a, 1)).Over(face)
	k := &colors{
		text: ink, textMuted: muted, textFaint: ink.Alpha(0.55).Over(face), iconMuted: muted,
		hover: ink.Alpha(st.hover), pressed: ink.Alpha(st.pressed),
	}
	inkLadders[face] = k
	return k
}

// withLadder returns k with the text ladder of ref: k itself when ref is k,
// else a copy.
func withLadder(k, ref *colors) *colors {
	if ref == k {
		return k
	}
	h := *k
	h.text, h.textMuted, h.textFaint, h.iconMuted = ref.text, ref.textMuted, ref.textFaint, ref.iconMuted
	h.hover, h.pressed = ref.hover, ref.pressed
	return &h
}

// headerColors returns k's colors for a pane header drawn on bg: its own,
// or those of the palette of bg's lightness when the pane takes a terminal
// background of the other appearance, as a light window over a dark
// terminal does. Its title, its buttons and their hover faces then read on
// the header, rather than turning black on a dark one, and the card's
// edge is not a white ring around a dark face.
func headerColors(k *colors, bg ui.Color) *colors {
	ref := ladderOf(k, isDark(bg))
	if ref == k {
		return k
	}
	h := withLadder(k, ref)
	h.cardBorder, h.cardBorderFocused = ref.cardBorder, ref.cardBorderFocused
	return h
}

// faceRole names a face of the palette that a theme or the user may
// override: a role of internal/theme's allowlist that is a face.
type faceRole = theme.Role

const (
	roleActiveGroupBackground    = theme.ActiveGroupBackground
	roleActiveGroupTabBackground = theme.ActiveGroupTabBackground
	roleTrackBackground          = theme.TrackBackground
	roleTabHoverBackground       = theme.TabHoverBackground
	roleTabActiveBackground      = theme.TabActiveBackground
	roleTitleBarBackground       = theme.TitleBarBackground
	roleSideBarBackground        = theme.SideBarBackground
	rolePaneHeaderBackground     = theme.PaneHeaderBackground
	roleButtonBackground         = theme.ButtonBackground
)

// themedFace returns k with the ladder of the face role sets, and the face,
// when the theme sets one; k and ok false when it does not, so that the
// caller draws as it did before. The ladder is that of the palette of the
// face's lightness, not k's own: k may carry the other appearance's ladder
// already, as a pane header's over a dark terminal in a light window does
// (headerColors).
//
// The palette it makes is the same pointer for the same k and role, until
// applyTheme, so that the caches keyed by a palette (menuStyleOf) do not
// grow each frame. Its window() is k's window.
func themedFace(k *colors, role faceRole) (*colors, ui.Color, bool) {
	face := *roleField(k, role)
	if face.A == 0 {
		return k, face, false
	}
	flat := face.Over(k.panel)
	ref := faceLadder(k, flat, paletteOf(isDark(flat)))
	if k.text == ref.text && k.textMuted == ref.textMuted && k.textFaint == ref.textFaint && k.iconMuted == ref.iconMuted &&
		k.hover == ref.hover && k.pressed == ref.pressed {
		return k, face, true
	}
	key := faceKey{*k, role}
	faceCacheMu.Lock()
	defer faceCacheMu.Unlock()
	if h, ok := faceCache[key]; ok {
		return h, face, true
	}
	h := *withLadder(k, ref)
	h.win = k.window()
	faceCache[key] = &h
	return &h, face, true
}

// faceKey names a face's palette: the palette it is made from, by value,
// as the sidebar's and a space's palettes are copies, and the role.
type faceKey struct {
	k    colors
	role faceRole
}

// The faces' palettes themedFace made, which applyTheme clears. The
// windows' views may run on their own goroutines, so the map has its lock.
var (
	faceCacheMu sync.Mutex
	faceCache   = map[faceKey]*colors{}
)

// buttonColors sets t's accent faces, the primary button's and a switch's
// that is on, to the theme's button.background, with text of the face's
// ladder, or its accentText while the face is the accent. A theme without
// the role leaves t as it is.
func buttonColors(t *ui.Theme, k *colors) {
	face := k.buttonFace
	if face.A == 0 {
		return
	}
	text := k.accentText
	flat := face.Over(k.panel)
	ref := faceLadder(k, flat, ladderOf(k, isDark(flat))) // surface's ladder
	if face != k.accent {
		text = ref.text
	}
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = face, ref.hover.Over(face), ref.pressed.Over(face), text
}

// faceColors is a face with the colors that show on it, as one value: the
// text ladder and the hover faces of the palette whose lightness is the
// face's. Code that draws on the face takes them from here, so that a face
// the user sets cannot keep text that does not read on it.
type faceColors struct {
	face                                  ui.Color
	text, textMuted, textFaint, iconMuted ui.Color
	hover, pressed                        ui.Color
}

// surface resolves role in k: the face k has for it, with the ladder of
// the palette of the face's lightness. A face that is not opaque counts as
// laid over k's panel, so a translucent face of the palette (white at 14%
// in a dark one) has the lightness of what it makes of the panel. A built-in
// face has its own palette's lightness, so it gets the palette's ladder.
func surface(k *colors, role faceRole) faceColors {
	face := *roleField(k, role)
	flat := face.Over(k.panel)
	ref := faceLadder(k, flat, ladderOf(k, isDark(flat)))
	return faceColors{face, ref.text, ref.textMuted, ref.textFaint, ref.iconMuted, ref.hover, ref.pressed}
}

// on returns k with s's ladder: what a surface's content draws with. It
// is k itself when the ladder is k's own.
func (s faceColors) on(k *colors) *colors {
	if s.text == k.text && s.textMuted == k.textMuted && s.textFaint == k.textFaint && s.iconMuted == k.iconMuted &&
		s.hover == k.hover && s.pressed == k.pressed {
		return k
	}
	h := *k
	h.text, h.textMuted, h.textFaint, h.iconMuted = s.text, s.textMuted, s.textFaint, s.iconMuted
	h.hover, h.pressed = s.hover, s.pressed
	return &h
}

// background is the shader of the window's background: the four colors
// as vertical gradients, which meet at 38% and at 70% of the height, mixed
// in sRGB as the gradients are, and the tint over them from 35% of the
// width, as a horizontal gradient from clear. A hash of the pixel moves
// each channel by up to half a step of 8 bits, which hides the bands a
// smooth gradient makes. The colors arrive not premultiplied, and the
// shader returns the color premultiplied, as opaque as Alpha: 1 but over
// the window's glass.
var background = shader.MustCompile(`package main

var Top, Mid, Low, Bottom, Tint vec4
var Alpha float

func hash(v uint) uint {
	s := v*747796405 + 2891336453
	w := ((s >> ((s >> 28) + 4)) ^ s) * 277803737
	return (w >> 22) ^ w
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	y := pos.y / size.y
	c := mix(Top.rgb, Mid.rgb, clamp(y/0.38, 0, 1))
	c = mix(c, Low.rgb, clamp((y-0.38)/0.32, 0, 1))
	c = mix(c, Bottom.rgb, clamp((y-0.7)/0.3, 0, 1))
	a := Tint.a * clamp((pos.x/size.x-0.35)/0.65, 0, 1)
	c = c*(1-a) + Tint.rgb*a
	n := hash(uint(dst.x) + hash(uint(dst.y)))
	c += (float(n)/4294967295.0 - 0.5) / 255.0
	return vec4(clamp(c, 0, 1)*Alpha, Alpha)
}
`)

// The background's uniforms, and the values it draws with, set again for
// each frame.
var (
	bgValues                                   = background.NewValues()
	bgTopU, bgMidU, bgLowU, bgBottomU, bgTintU = background.Uniform("Top"), background.Uniform("Mid"),
		background.Uniform("Low"), background.Uniform("Bottom"), background.Uniform("Tint")
	bgAlphaU = background.Uniform("Alpha")
)

// backgroundOptions returns what the background shader draws with: k's
// colors, as opaque as alpha, in the values made once.
func backgroundOptions(k *colors, alpha float32) shader.Options {
	bgValues.Color(bgTopU, k.bgTop).Color(bgMidU, k.bgMid).Color(bgLowU, k.bgLow).
		Color(bgBottomU, k.bgBottom).Color(bgTintU, k.bgTint).Float(bgAlphaU, alpha)
	return shader.Options{Values: bgValues}
}

// paintBackground paints the window's background, as opaque as alpha: 1,
// or backgroundAlpha's wash over the window's glass. It is one shader, or
// on the CPU renderer, where a shader over the window runs far too slowly,
// gradients of the same colors. A theme's background shader
// (docs/DSK-012-theme-shaders.md) replaces the built-in one on a GPU.
// moves tells that the theme's shaders move in the window
// (themeShaderMoves); sw is the window's shaderWindow (windowShader), nil
// for none kept.
func paintBackground(p *ui.Painter, r ui.Rect, k *colors, alpha float32, dark, moves bool, sw *shaderWindow) {
	f := tickShaders(p, r, moves, sw)
	if sl := themeBackground(); sl != nil && themeShaderGPU(p) {
		sl.draw(p, r, 0, dark, alpha, f)
		return
	}
	if p.Accelerated() {
		background.Draw(p, r, backgroundOptions(k, alpha))
		return
	}
	top, mid, low, bottom, tint := k.bgTop, k.bgMid, k.bgLow, k.bgBottom, k.bgTint
	h1, h2 := r.H*0.38, r.H*0.32
	over := float32(1) // the bands overlap by a DIP, which an opaque one hides
	if alpha < 1 {
		top, mid, low, bottom, tint = top.Alpha(alpha), mid.Alpha(alpha), low.Alpha(alpha), bottom.Alpha(alpha), tint.Alpha(alpha)
		h1, h2, over = float32(int(h1)), float32(int(h2)), 0
	}
	p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: h1 + over}, ui.LinearGradient{From: top, To: mid, Angle: 180}, 0)
	p.FillGradient(ui.Rect{X: r.X, Y: r.Y + h1, W: r.W, H: h2 + over}, ui.LinearGradient{From: mid, To: low, Angle: 180}, 0)
	p.FillGradient(ui.Rect{X: r.X, Y: r.Y + h1 + h2, W: r.W, H: r.H - h1 - h2}, ui.LinearGradient{From: low, To: bottom, Angle: 180}, 0)
	clear := tint
	clear.A = 0
	p.FillGradient(r, ui.LinearGradient{From: clear, To: tint, Angle: 90, Start: 0.35, End: 1}, 0)
}

//go:embed assets/fonts/JetBrainsMono-Regular.ttf
var fontRegular []byte

//go:embed assets/fonts/JetBrainsMono-Bold.ttf
var fontBold []byte

//go:embed assets/fonts/JetBrainsMono-Italic.ttf
var fontItalic []byte

//go:embed assets/fonts/JetBrainsMono-BoldItalic.ttf
var fontBoldItalic []byte

var fontsOnce sync.Once

// registerFonts registers the fonts once: every call to ui.RegisterFont adds
// the fonts to the text system again, which keeps them (DirectWrite on
// Windows) and slows every later layout, as a test process that builds many
// apps shows.
func registerFonts() {
	fontsOnce.Do(func() {
		for _, f := range [][]byte{fontRegular, fontBold, fontItalic, fontBoldItalic} {
			ui.RegisterFont(f, "JetBrains Mono")
		}
	})
}

// defaultFontFamily is the terminals' font family, after the user's own.
const defaultFontFamily = "JetBrains Mono, SF Mono, Menlo, monospace"

// The terminals' colors: soft ink on the panes' paper, and the dark kind.
var lightTerm = &terminal.Theme{
	Foreground: ui.Hex("#2a2d31"),
	Background: ui.Hex("#fbfbfb"),
	Cursor:     ui.Hex("#3a3d42"),
	Selection:  ui.RGBA(46, 111, 208, 0.2),
	Palette: [16]ui.Color{
		ui.Hex("#2a2d31"), ui.Hex("#c2465a"), ui.Hex("#4e8e5f"), ui.Hex("#b07a1e"),
		ui.Hex("#2e6fd0"), ui.Hex("#9050c8"), ui.Hex("#1e8c9c"), ui.Hex("#8a8d93"),
		ui.Hex("#6e7178"), ui.Hex("#d9566b"), ui.Hex("#5ba671"), ui.Hex("#c89026"),
		ui.Hex("#4a8fe0"), ui.Hex("#a56bd8"), ui.Hex("#2ba2b3"), ui.Hex("#b5b8be"),
	},
}

var darkTerm = &terminal.Theme{
	Foreground: ui.Hex("#e6e6ea"),
	Background: ui.Hex("#1e1e22"),
	Cursor:     ui.Hex("#e6e6ea"),
	Selection:  ui.RGBA(108, 178, 255, 0.28),
	Palette: [16]ui.Color{
		ui.Hex("#3a3a40"), ui.Hex("#ff6b7f"), ui.Hex("#7bd88f"), ui.Hex("#e5c07b"),
		ui.Hex("#6cb2ff"), ui.Hex("#c792ea"), ui.Hex("#56d4dd"), ui.Hex("#d0d0d6"),
		ui.Hex("#6e6e78"), ui.Hex("#ff8c9c"), ui.Hex("#9be8a8"), ui.Hex("#f2d28a"),
		ui.Hex("#8ec5ff"), ui.Hex("#d7a8f2"), ui.Hex("#7fe3ea"), ui.Hex("#ffffff"),
	},
}

// widgetTheme is the stock widgets' theme in GoRex's colors, so that the
// segmented controls, switches, steppers and selects of a window look as
// the rest of the app. Its Background is the face of a segmented control's
// chosen part, not the window's.
func widgetTheme(base *ui.Theme, k *colors) *ui.Theme {
	t := *base
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = k.surface, k.surface, k.surfaceHover, k.surfacePressed
	t.Border, t.Text, t.TextMuted = k.panelBorder, k.text, k.textMuted
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = k.accent, k.accentHover, k.accentPressed, k.accentText
	t.Menu = menuStyleOf(k, t.Dark)
	t.Selection, t.Focus = k.accent.Alpha(0.3), k.accent.Alpha(0.55)
	buttonColors(&t, k)
	t.Radius = k.radius(9)
	t.Font = k.m.font
	return &t
}
