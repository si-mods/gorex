package main

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The palettes as literals, before derive made them (docs/DSK-008-theme.md,
// phase 2). TestDeriveKeepsPalettes holds derive to them byte for byte.
var wantLightColors = colors{
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
	emptyMark: ui.Hex("#7a869d"),
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
	groupFace:   ui.RGBA(0, 0, 0, 0.045),
	pillLift:    ui.RGBA(255, 255, 255, 0.92),
	liftShadow:  ui.RGBA(0, 0, 0, 0.06),

	busy:      ui.Hex("#34a853"),
	danger:    ui.Hex("#ef4444"),
	attention: ui.Hex("#f59e0b"),
	done:      ui.Hex("#7aa0c2"),
	exited:    ui.Hex("#a1a1a6"),

	panel:       ui.Hex("#fcfcfd"),
	panelGlass:  ui.RGBA(252, 252, 253, 0.80),
	panelBorder: ui.RGBA(0, 0, 0, 0.08),
	panelSel:    ui.RGBA(46, 111, 208, 0.12),
	backdrop:    ui.RGBA(40, 20, 40, 0.06),

	accent: ui.Hex("#2e6fd0"), accentHover: ui.Hex("#2a64bd"), accentPressed: ui.Hex("#2459a8"), accentText: ui.Hex("#ffffff"),
	surface: ui.Hex("#fcfcfd"), surfaceHover: ui.Hex("#efeff2"), surfacePressed: ui.Hex("#e3e3e8"),

	field: ui.Hex("#f4f4f5"), fieldBorder: ui.Hex("#d9d9de"),
}

var wantDarkColors = colors{
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
	emptyMark: ui.Hex("#9aa4b8"),
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
	groupFace:   ui.RGBA(255, 255, 255, 0.05),
	pillLift:    ui.RGBA(255, 255, 255, 0.24),
	liftShadow:  ui.RGBA(0, 0, 0, 0.18),

	busy:      ui.Hex("#4ade80"),
	danger:    ui.Hex("#ef4444"),
	attention: ui.Hex("#fbbf24"),
	done:      ui.Hex("#7aa0c2"),
	exited:    ui.Hex("#636366"),

	panel:       ui.Hex("#26262a"),
	panelGlass:  ui.RGBA(38, 38, 42, 0.78),
	panelBorder: ui.RGBA(255, 255, 255, 0.1),
	panelSel:    ui.RGBA(108, 178, 255, 0.18),
	backdrop:    ui.RGBA(0, 0, 0, 0.2),

	accent: ui.Hex("#6cb2ff"), accentHover: ui.Hex("#82bfff"), accentPressed: ui.Hex("#5aa2f0"), accentText: ui.Hex("#10151c"),
	surface: ui.Hex("#262629"), surfaceHover: ui.Hex("#303035"), surfacePressed: ui.Hex("#3a3a40"),

	field: ui.Hex("#2d2d32"), fieldBorder: ui.Hex("#3f3f46"),
}

// TestDeriveKeepsPalettes checks that derive on the built-in seeds gives
// every field of the literal palettes, each channel byte-exact: the seeds
// change where the values come from, not one pixel.
func TestDeriveKeepsPalettes(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  colors
		want colors
	}{
		{"light", derive(lightSeeds), wantLightColors},
		{"dark", derive(darkSeeds), wantDarkColors},
		{"lightColors", lightColors, wantLightColors},
		{"darkColors", darkColors, wantDarkColors},
	} {
		if tc.got.m != builtinMetrics {
			t.Errorf("%s: metrics %+v, want the built-in ones", tc.name, tc.got.m)
		}
		got, want := reflect.ValueOf(tc.got), reflect.ValueOf(tc.want)
		for i := range got.NumField() {
			g, w := got.Field(i), want.Field(i)
			if g.Type() != reflect.TypeFor[ui.Color]() {
				continue // the metrics, checked above
			}
			gc := ui.Color{R: uint8(g.Field(0).Uint()), G: uint8(g.Field(1).Uint()), B: uint8(g.Field(2).Uint()), A: uint8(g.Field(3).Uint())}
			wc := ui.Color{R: uint8(w.Field(0).Uint()), G: uint8(w.Field(1).Uint()), B: uint8(w.Field(2).Uint()), A: uint8(w.Field(3).Uint())}
			if gc != wc {
				t.Errorf("%s.%s = %v, want %v", tc.name, got.Type().Field(i).Name, gc, wc)
			}
		}
	}
}
