package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/theme"
)

// lightOnly is a theme whose dark seeds are its light ones, as a light-only
// theme sets them, with a dark blue title bar and a face shader of it.
const lightOnly = `{
  "name": "Light Only",
  "seeds": {
    "light": { "panel": "#fcfcfd", "text": "#1d1d1f", "textMuted": "#636366", "textFaint": "#8e8e93", "iconMuted": "#8a8a8f" },
    "dark": { "panel": "#fcfcfd", "text": "#1d1d1f", "textMuted": "#636366", "textFaint": "#8e8e93", "iconMuted": "#8a8a8f" }
  },
  "roles": { "titleBar.background": "#2876c8" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": 1, "faces": { "titleBar.background": { "source": "bar", "params": { "Top": "#2876c8", "Bottom": "#1d5fae" } } } }
}`

// TestFaceLadderLightOnly: a light-only theme's dark face gets white text
// that reads on it, in both appearances, though the theme's ladders are
// both dark, and its face shader passes the contrast check with it. With
// no theme the pairing is the face's lightness, as before.
func TestFaceLadderLightOnly(t *testing.T) {
	keepTheme(t)
	blue := ui.Hex("#2876c8")
	if got := faceLadder(&lightColors, blue, &darkColors); got != &darkColors {
		t.Fatalf("with no theme a dark face takes %v, want the built-in dark palette", got.text)
	}
	m, art := shaderTheme(t, lightOnly, map[string]string{"bar": `package main

var Top, Bottom vec4

func Fragment(dst vec4, pos vec2) vec4 {
	return vec4(mix(Top.rgb, Bottom.rgb, pos.y/rectSize().y), 1)
}
`})
	set := useShaders(t, m, art)
	for _, dark := range []bool{false, true} {
		k, _, ok := themedFace(paletteOf(dark), roleTitleBarBackground)
		if !ok || k.text != ui.Hex("#ffffff") {
			t.Errorf("dark %v: the title bar's text is %v, want white", dark, k.text)
		}
		if r := contrast(k.text, blue); r < faceTextContrast {
			t.Errorf("dark %v: text %.2f:1", dark, r)
		}
		if r := contrast(k.textMuted, blue); r < faceMutedContrast {
			t.Errorf("dark %v: textMuted %.2f:1", dark, r)
		}
	}
	if set.faces[roleTitleBarBackground] == nil {
		t.Errorf("the title bar's shader fails: %q", set.warn)
	}
}

// TestFaceShaderInset: the contrast check reads a face only where text
// sits, inside faceTextInset: an accent bar 3 px wide at the left edge
// passes, one 10 px in fails.
func TestFaceShaderInset(t *testing.T) {
	bar := func(x0, x1 string) string {
		return "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tif pos.x >= " + x0 + " && pos.x < " + x1 + " {\n\t\treturn vec4(0.2, 0.4, 0.9, 1)\n\t}\n\treturn vec4(0.894, 0.894, 0.894, 1)\n}\n"
	}
	const manifest = `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": 1, "faces": { "titleBar.background": { "source": "bar" } } }
}`
	m, art := shaderTheme(t, manifest, map[string]string{"bar": bar("0.0", "3.0")})
	if set := useShaders(t, m, art); set.faces[roleTitleBarBackground] == nil {
		t.Errorf("an accent bar at the edge fails: %q", set.warn)
	}
	m, art = shaderTheme(t, manifest, map[string]string{"bar": bar("10.0", "13.0")})
	if set := useShaders(t, m, art); set.faces[roleTitleBarBackground] != nil || len(set.warn) == 0 || !strings.Contains(set.warn[0], "text has") {
		t.Errorf("a bar where text sits passes: %q", set.warn)
	}
}

// TestThemeDescription: the Window Theme group shows a theme's
// description under its name.
func TestThemeDescription(t *testing.T) {
	a := &App{}
	tt := ui.NewTester(func(c *ui.Context) {
		a.themesGroup(c, &lightColors, []theme.Summary{{ID: "blue", Name: "Blue Test", Description: "A made-up theme of blue bars"}})
	}, 640, 400)
	tt.Frame()
	if !tt.HasText("A made-up theme of blue bars") {
		t.Errorf("no description; texts %q", tt.Texts())
	}
}
