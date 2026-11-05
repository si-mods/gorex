package main

import (
	_ "embed"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
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

	track, trackBorder, tabActive, tabSep ui.Color
	// pillFace and pillShadow are the active tab's pill under the glass
	// setting: its face where the system draws no glass, and its shadow.
	pillFace, pillShadow ui.Color
	// tabHover is a hovered tab's face under the glass setting, as Rex's:
	// lighter in a dark window, darker in a light one, and no glass.
	tabHover ui.Color
	// pillTint tints the system's glass of the active tab's pill.
	pillTint ui.Color
	tileRim  ui.Color

	busy, attention, exited, danger ui.Color

	// panel is the face of popovers, dialogs and the rows that float over
	// a pane: opaque, so that the text they cover shows through nowhere.
	panel, panelBorder, panelSel, backdrop ui.Color
	// panelGlass is panel as the palette lays it over the blurred window,
	// translucent where panel is opaque.
	panelGlass ui.Color
	// field is the face of a text field, which is opaque as panel, and its
	// outline.
	field, fieldBorder ui.Color

	// accent is the color of what is chosen or on; panelSel is its wash.
	accent, accentHover, accentPressed ui.Color
	// surface is the face of controls, opaque as the popovers' panel.
	surface, surfaceHover, surfacePressed ui.Color
}

var lightColors = colors{
	bgTop:    ui.Hex("#f6e7f6"),
	bgMid:    ui.Hex("#efe1e6"),
	bgLow:    ui.Hex("#eee0d3"),
	bgBottom: ui.Hex("#ece1bd"),
	bgTint:   ui.RGBA(242, 214, 222, 0.35),

	card:              ui.RGBA(255, 255, 255, 0.66),
	cardFocused:       ui.RGBA(255, 255, 255, 0.95),
	cardBorder:        ui.RGBA(255, 255, 255, 0.72),
	cardBorderFocused: ui.RGBA(255, 255, 255, 1),
	shadow:            ui.RGBA(60, 40, 50, 0.07),
	shadowFocused:     ui.RGBA(60, 40, 50, 0.12),

	text:      ui.Hex("#1d1d1f"),
	textMuted: ui.Hex("#636366"),
	textFaint: ui.Hex("#8e8e93"),
	iconMuted: ui.Hex("#8a8a8f"),
	hover:     ui.RGBA(0, 0, 0, 0.055),
	pressed:   ui.RGBA(0, 0, 0, 0.1),

	track:       ui.RGBA(255, 255, 255, 0.34),
	trackBorder: ui.RGBA(255, 255, 255, 0.45),
	tabActive:   ui.RGBA(255, 255, 255, 0.97),
	pillFace:    ui.RGBA(255, 255, 255, 0.45),
	pillShadow:  ui.RGBA(255, 255, 255, 0.06),
	tabHover:    ui.RGBA(0, 0, 0, 0.05),
	pillTint:    ui.RGBA(29, 29, 29, 0.25),
	tabSep:      ui.RGBA(0, 0, 0, 0.13),
	tileRim:     ui.RGBA(255, 255, 255, 0.95),

	busy:      ui.Hex("#34a853"),
	danger:    ui.Hex("#ef4444"),
	attention: ui.Hex("#f59e0b"),
	exited:    ui.Hex("#a1a1a6"),

	panel:       ui.Hex("#fcfcfd"),
	panelGlass:  ui.RGBA(252, 252, 253, 0.80),
	panelBorder: ui.RGBA(0, 0, 0, 0.08),
	panelSel:    ui.RGBA(46, 111, 208, 0.12),
	backdrop:    ui.RGBA(40, 20, 40, 0.06),

	accent: ui.Hex("#2e6fd0"), accentHover: ui.Hex("#2a64bd"), accentPressed: ui.Hex("#2459a8"),
	surface: ui.Hex("#fcfcfd"), surfaceHover: ui.Hex("#efeff2"), surfacePressed: ui.Hex("#e3e3e8"),

	field: ui.Hex("#f4f4f5"), fieldBorder: ui.Hex("#d9d9de"),
}

var darkColors = colors{
	bgTop:    ui.Hex("#2c2131"),
	bgMid:    ui.Hex("#231e27"),
	bgLow:    ui.Hex("#221f22"),
	bgBottom: ui.Hex("#2a2619"),
	bgTint:   ui.RGBA(80, 40, 60, 0.25),

	card:              ui.RGBA(20, 20, 24, 0.5),
	cardFocused:       ui.RGBA(36, 36, 41, 0.94),
	cardBorder:        ui.RGBA(255, 255, 255, 0.05),
	cardBorderFocused: ui.RGBA(255, 255, 255, 0.14),
	shadow:            ui.RGBA(0, 0, 0, 0.25),
	shadowFocused:     ui.RGBA(0, 0, 0, 0.4),

	text:      ui.Hex("#f2f2f7"),
	textMuted: ui.Hex("#aeaeb2"),
	textFaint: ui.Hex("#8e8e93"),
	iconMuted: ui.Hex("#98989d"),
	hover:     ui.RGBA(255, 255, 255, 0.08),
	pressed:   ui.RGBA(255, 255, 255, 0.14),

	track:       ui.RGBA(255, 255, 255, 0.06),
	trackBorder: ui.RGBA(255, 255, 255, 0.06),
	tabActive:   ui.RGBA(255, 255, 255, 0.14),
	pillFace:    ui.RGBA(255, 255, 255, 0.14),
	pillShadow:  ui.Transparent,
	tabHover:    ui.RGBA(255, 255, 255, 0.10),
	pillTint:    ui.Transparent,
	tabSep:      ui.RGBA(255, 255, 255, 0.14),
	tileRim:     ui.RGBA(255, 255, 255, 0.22),

	busy:      ui.Hex("#4ade80"),
	danger:    ui.Hex("#ef4444"),
	attention: ui.Hex("#fbbf24"),
	exited:    ui.Hex("#636366"),

	panel:       ui.Hex("#26262a"),
	panelGlass:  ui.RGBA(38, 38, 42, 0.78),
	panelBorder: ui.RGBA(255, 255, 255, 0.1),
	panelSel:    ui.RGBA(108, 178, 255, 0.18),
	backdrop:    ui.RGBA(0, 0, 0, 0.2),

	accent: ui.Hex("#6cb2ff"), accentHover: ui.Hex("#82bfff"), accentPressed: ui.Hex("#5aa2f0"),
	surface: ui.Hex("#262629"), surfaceHover: ui.Hex("#303035"), surfacePressed: ui.Hex("#3a3a40"),

	field: ui.Hex("#2d2d32"), fieldBorder: ui.Hex("#3f3f46"),
}

func colorsOf(c *ui.Context) *colors {
	if c.Theme().Dark {
		return &darkColors
	}
	return &lightColors
}

// isDark reports whether c is darker than the midpoint, as a face is.
func isDark(c ui.Color) bool {
	return int(c.R)*299+int(c.G)*587+int(c.B)*114 < 128*1000
}

// headerColors returns k's colors for a pane header drawn on bg: its own,
// or those of the palette of bg's lightness when the pane takes a terminal
// background of the other appearance, as a light window over a dark
// terminal does. Its title, its buttons and their hover faces then read on
// the header, rather than turning black on a dark one.
func headerColors(k *colors, bg ui.Color) *colors {
	if isDark(bg) == isDark(k.panel) {
		return k
	}
	ref := &lightColors
	if isDark(bg) {
		ref = &darkColors
	}
	h := *k
	h.text, h.textMuted, h.textFaint, h.iconMuted = ref.text, ref.textMuted, ref.textFaint, ref.iconMuted
	h.hover, h.pressed = ref.hover, ref.pressed
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
// gradients of the same colors.
func paintBackground(p *ui.Painter, r ui.Rect, k *colors, alpha float32) {
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

func registerFonts() {
	for _, f := range [][]byte{fontRegular, fontBold, fontItalic, fontBoldItalic} {
		ui.RegisterFont(f, "JetBrains Mono")
	}
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

// accentText is the color of text on the accent: dark on the light accent
// of a dark window, white on the other.
func accentText(dark bool) ui.Color {
	if dark {
		return ui.Hex("#10151c")
	}
	return ui.Hex("#ffffff")
}

// widgetTheme is the stock widgets' theme in GoRex's colors, so that the
// segmented controls, switches, steppers and selects of a window look as
// the rest of the app. Its Background is the face of a segmented control's
// chosen part, not the window's.
func widgetTheme(base *ui.Theme, k *colors) *ui.Theme {
	t := *base
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = k.surface, k.surface, k.surfaceHover, k.surfacePressed
	t.Border, t.Text, t.TextMuted = k.panelBorder, k.text, k.textMuted
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = k.accent, k.accentHover, k.accentPressed, accentText(t.Dark)
	t.Menu = menuStyleOf(k, t.Dark)
	t.Selection, t.Focus = k.accent.Alpha(0.3), k.accent.Alpha(0.55)
	t.Radius = 9
	t.Font = uiFont
	return &t
}
