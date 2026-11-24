package theme

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestParseShaders: the shaders key parses with its caps: a slot with a
// bad source or image, a role that is not a face, button.background and a
// new face with no flat color drop; a sidebar face with no flat color takes
// the built-in one; params take numbers, lists and colors; each gives a
// warning.
func TestParseShaders(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "roles": { "titleBar.background": "#2876c8", "button.background": "#2876c8" },
  "assets": { "bg": "bg.kage", "bar": "bar.kage", "wall": "wall.png", "row": "row.kage", "notkage": "x.png" },
  "shaders": {
    "api": 1,
    "background": { "source": "bg", "images": ["wall", "wall", "wall", "wall", "wall"], "params": { "Opacity": 0.2, "Tint": [1, 2, 3], "Ink": { "light": "#000", "dark": "#fff" }, "Time": 1, "bad name": 1, "Odd": "nope" } },
    "ambient": { "source": "bg" },
    "faces": {
      "titleBar.background": { "source": "bar", "params": { "Top": "#73b9ed" } },
      "sideBar.tabActiveBackground": { "source": "row" },
      "paneHeader.background": { "source": "row" },
      "button.background": { "source": "row" },
      "sideBar.tabSeparator": { "source": "row" },
      "editor.background": { "source": "row" },
      "sideBar.trackBackground": { "source": "notkage" },
      "sideBar.background": { "source": "row", "images": ["bg"] }
    }
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	s := m.Shaders
	if s == nil || s.Background == nil {
		t.Fatalf("shaders %+v", s)
	}
	bg := s.Background
	if bg.Source != "bg" || len(bg.Images) != 4 {
		t.Errorf("background %+v", bg)
	}
	if p := bg.Params["Opacity"]; !slices.Equal(p.Nums, []float32{0.2}) {
		t.Errorf("a number param %+v", p)
	}
	if p := bg.Params["Tint"]; !slices.Equal(p.Nums, []float32{1, 2, 3}) {
		t.Errorf("a list param %+v", p)
	}
	if p := bg.Params["Ink"]; p.Color == nil || p.Color[0] != ui.Hex("#000") || p.Color[1] != ui.Hex("#fff") {
		t.Errorf("a color param %+v", p)
	}
	for _, name := range []string{"Time", "bad name", "Odd"} {
		if _, ok := bg.Params[name]; ok {
			t.Errorf("param %q kept", name)
		}
	}
	var faces []string
	for r := range s.Faces {
		faces = append(faces, r.String())
	}
	slices.Sort(faces)
	if want := []string{"sideBar.tabActiveBackground", "titleBar.background"}; !slices.Equal(faces, want) {
		t.Errorf("faces %q, want %q", faces, want)
	}
	want := []string{
		`shaders.ambient: not drawn by this build`,
		`shaders.background.images: more than 4, the rest dropped`,
		`shaders.background.params.Odd: not a number, a list of up to 16 numbers or a color`,
		`shaders.background.params.Time: GoRex sets this uniform (dropped)`,
		`shaders.background.params: bad uniform name "bad name"`,
		`shaders.faces.button.background: the widgets paint this face, which takes no shader (dropped)`,
		`shaders.faces.editor.background: not a face role (dropped)`,
		`shaders.faces.paneHeader.background: needs roles.paneHeader.background as its flat color (dropped)`,
		`shaders.faces.sideBar.background: needs roles.sideBar.background as its flat color (dropped)`,
		`shaders.faces.sideBar.tabActiveBackground: no roles.sideBar.tabActiveBackground, the built-in flat color applies`,
		`shaders.faces.sideBar.tabSeparator: not a face role (dropped)`,
		`shaders.faces.sideBar.trackBackground.source: no .kage asset "notkage" (dropped)`,
		`shaders.faces.sideBar.trackBackground: no roles.sideBar.trackBackground, the built-in flat color applies`,
	}
	if !slices.Equal(warn, want) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, want)
	}

	// An API this build does not know drops the shaders.
	m, warn, _ = Parse([]byte(`{ "assets": { "bg": "bg.kage" }, "shaders": { "api": 2, "background": { "source": "bg" } } }`))
	if m.Shaders != nil || len(warn) != 1 || !strings.Contains(warn[0], "shaders.api") {
		t.Errorf("api 2: %+v %q", m.Shaders, warn)
	}
}

// TestShaderUniforms: the uniform check takes the API's uniforms at their
// types and params that fit, warns of a uniform no param sets and of a
// param with no uniform, and fails on a wrong type.
func TestShaderUniforms(t *testing.T) {
	src, err := ParseShaderSource(`package main

var Time, Dark float
var Origin, Window vec2
var SeedAccent, SeedBgTop vec4
var Top vec4
var Pair vec2
var Weights [3]float
var Unset float
var hidden float

func Fragment(dst vec4, pos vec2) vec4 {
	return Top
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(src.Entries, []string{"Fragment"}) || src.Backdrop {
		t.Errorf("source %+v", src)
	}
	slot := &ShaderSlot{Params: map[string]Param{
		"Top":     {Color: &[2]ui.Color{}},
		"Pair":    {Nums: []float32{1, 2}},
		"Weights": {Nums: []float32{1, 2, 3}},
		"Extra":   {Nums: []float32{1}},
	}}
	warn, err := CheckUniforms(src, slot)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"uniform Unset: no param sets it, so it is zero", "params.Extra: the shader has no such uniform"}
	if !slices.Equal(warn, want) {
		t.Errorf("warnings %q, want %q", warn, want)
	}
	for _, bad := range []string{"var Time vec2", "var SeedNope vec4", "var Pair vec3", "var Kept float"} {
		src, err := ParseShaderSource("package main\n\n" + bad + "\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\treturn vec4(0)\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CheckUniforms(src, slot, "Kept"); err == nil {
			t.Errorf("%s passes", bad)
		}
	}
}

// pngOf encodes a w×h PNG.
func pngOf(t *testing.T, w, h int) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestShaderAssets: a .kage asset loads as a shader's source up to 16 KiB;
// an image only shaders read may be 2048 px, one a still reads keeps the
// 1024 px of DSK-010.
func TestShaderAssets(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"bg.kage":  []byte("package main\n"),
		"big.kage": bytes.Repeat([]byte("/"), maxKageBytes+1),
		"wide.png": pngOf(t, 1500, 4),
		"also.png": pngOf(t, 1500, 4),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err := Parse([]byte(`{
  "assets": { "bg": "bg.kage", "big": "big.kage", "wide": "wide.png", "also": "also.png" },
  "ambience": { "images": [{ "asset": "also", "height": 0.5 }] },
  "shaders": { "api": 1, "background": { "source": "bg", "images": ["wide", "also"] }, "faces": { "sideBar.trackBackground": { "source": "big" } } }
}`))
	if err != nil {
		t.Fatal(err)
	}
	art, warn := LoadAssets(dir, m, 2)
	if art.Sources["bg"] != "package main\n" || art.Sources["big"] != "" {
		t.Errorf("sources %q", art.Sources)
	}
	if art.Bitmaps["wide"] == nil || art.Bitmaps["also"] != nil {
		t.Errorf("bitmaps %v", art.Bitmaps)
	}
	if len(warn) != 2 || !strings.Contains(warn[0], "assets.also") || !strings.Contains(warn[1], "assets.big") {
		t.Errorf("warnings %q", warn)
	}
	// A sprite keeps the caps of DSK-010 when a shader reads it too.
	if (assetUse{shader: true, sprite: 48}).shaderOnly() {
		t.Error("a sprite that a shader reads takes the shaders' caps")
	}
}
