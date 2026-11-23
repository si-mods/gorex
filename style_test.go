package main

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestHeaderColors checks that a pane header takes the palette of its
// background when the pane's terminal is of the other appearance, so that a
// hovered button does not turn black on a dark header, and keeps its own
// when the two match.
func TestHeaderColors(t *testing.T) {
	dark := headerColors(&lightColors, ui.Hex("#262626"))
	if dark.text != darkColors.text || dark.iconMuted != darkColors.iconMuted || dark.hover != darkColors.hover || dark.pressed != darkColors.pressed {
		t.Fatalf("a dark header kept the light palette: text %v, hover %v", dark.text, dark.hover)
	}
	if dark.accent != lightColors.accent || dark.card != lightColors.card {
		t.Fatal("headerColors changed colors that do not read on the header")
	}
	light := headerColors(&darkColors, ui.Hex("#fbfbfb"))
	if light.text != lightColors.text || light.hover != lightColors.hover {
		t.Fatalf("a light header kept the dark palette: text %v, hover %v", light.text, light.hover)
	}
	if got := headerColors(&lightColors, ui.Hex("#fbfbfb")); got != &lightColors {
		t.Fatal("a header on the window's own background took another palette")
	}
}

// TestHeaderColorsCardBorder checks that a light window over a dark terminal
// face draws the dark palette's card edge, not a white ring, and the reverse.
func TestHeaderColorsCardBorder(t *testing.T) {
	dark := headerColors(&lightColors, ui.Hex("#262626"))
	if dark.cardBorder != darkColors.cardBorder || dark.cardBorderFocused != darkColors.cardBorderFocused {
		t.Fatalf("a dark face kept the light card edge: %v, focused %v", dark.cardBorder, dark.cardBorderFocused)
	}
	light := headerColors(&darkColors, ui.Hex("#fbfbfb"))
	if light.cardBorder != lightColors.cardBorder || light.cardBorderFocused != lightColors.cardBorderFocused {
		t.Fatalf("a light face kept the dark card edge: %v, focused %v", light.cardBorder, light.cardBorderFocused)
	}
}

// TestSurfacePairing: a face that the user sets takes the text ladder of its
// lightness (docs/DSK-008-theme.md). A light tabActive in the dark palette
// resolves the light palette's text, which is dark; a dark one keeps the
// dark palette's; the built-in faces keep their own palette's.
func TestSurfacePairing(t *testing.T) {
	light := darkColors
	light.tabActive = ui.Hex("#e8e8ee")
	s := surface(&light, roleTabActiveBackground)
	if s.face != ui.Hex("#e8e8ee") || s.text != lightColors.text || s.textMuted != lightColors.textMuted ||
		s.textFaint != lightColors.textFaint || s.iconMuted != lightColors.iconMuted {
		t.Errorf("a light face in the dark palette resolved %+v, want the light ladder", s)
	}
	if got := s.on(&light); got.text != lightColors.text || got.accent != darkColors.accent {
		t.Errorf("on(): text %v accent %v", got.text, got.accent)
	}
	dark := darkColors
	dark.tabActive = ui.Hex("#2a2a3d")
	if s := surface(&dark, roleTabActiveBackground); s.text != darkColors.text || s.on(&dark) != &dark {
		t.Errorf("a dark face in the dark palette changed the ladder: %+v", s)
	}
	// A translucent face counts as laid over the panel.
	wash := lightColors
	wash.groupFace = ui.RGBA(0, 0, 0, 0.9)
	if s := surface(&wash, roleActiveGroupBackground); s.text != darkColors.text {
		t.Errorf("a dense dark wash in the light palette kept text %v", s.text)
	}
	// Every built-in face has its palette's own ladder, so no pixel moves.
	for name, k := range map[string]*colors{"light": &lightColors, "dark": &darkColors} {
		for _, role := range []faceRole{roleActiveGroupBackground, roleActiveGroupTabBackground, roleTrackBackground, roleTabHoverBackground, roleTabActiveBackground} {
			if got := surface(k, role).on(k); got != k {
				t.Errorf("%s palette, role %d: the built-in face changed the ladder", name, role)
			}
		}
	}
}

// TestSidebarChosenColors: with sidebar.selection "fill" and a light
// tabActive in the dark window, the chosen row's content draws with the
// light palette's text; with no override it draws with the palette's own.
func TestSidebarChosenColors(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{SidebarSelection: "fill", SidebarColors: map[string]json.RawMessage{
		"sideBar.tabActiveBackground": json.RawMessage(`"#e8e8ee"`),
	}}
	k := sidebarChosenColors(sidebarPalette(&darkColors, true))
	if k.text != lightColors.text || k.textMuted != lightColors.textMuted {
		t.Errorf("chosen row text %v, want the light palette's %v", k.text, lightColors.text)
	}
	prefs.SidebarColors = nil
	if got := sidebarChosenColors(&darkColors); got != &darkColors {
		t.Error("with no override the chosen row left the palette")
	}
}
