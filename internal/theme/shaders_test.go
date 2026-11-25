package theme

import (
	"bytes"
	"fmt"
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

	// An API this build does not know drops the shaders, as a build of
	// version 1 drops a pack of version 2.
	m, warn, _ = Parse([]byte(`{ "assets": { "bg": "bg.kage" }, "shaders": { "api": 3, "background": { "source": "bg" } } }`))
	if m.Shaders != nil || len(warn) != 1 || !strings.Contains(warn[0], "shaders.api") {
		t.Errorf("api 3: %+v %q", m.Shaders, warn)
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
	warn, err := CheckUniforms(src, slot, 1)
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
		if _, err := CheckUniforms(src, slot, 1, "Kept"); err == nil {
			t.Errorf("%s passes", bad)
		}
	}
}

// declSource is a version 2 shader with a declaration of each kind.
const declSource = `package main

var Time float
var SeedAccent vec4

// Grain strength.
// It rises to one.
//gorex:param number 0.3 0 1
var Grain float

//gorex:param color #73b9ed/#2a5d8f
var Top vec4

//gorex:param color seed:accent
var Ink vec3

//gorex:param toggle on
var Stitch float

// Where the light sits, in the drawn rectangle.
//gorex:param point 0.5 0.2
var Light vec2

var (
	// A grouped declaration.
	//gorex:param number 2 1 4
	Scale float
)

func Fragment(dst vec4, pos vec2) vec4 {
	return Top
}
`

// TestShaderDeclarations: a version 2 shader declares its params with
// //gorex:param; each bad declaration, an undeclared uniform, a kind that
// does not fit its uniform, more than 16 params and a declaration on an
// API uniform fail check 3. Version 1 reads none of it.
func TestShaderDeclarations(t *testing.T) {
	src, err := ParseShaderSource(declSource)
	if err != nil {
		t.Fatal(err)
	}
	d := src.Params
	if len(d) != 6 {
		t.Fatalf("declarations %+v", d)
	}
	if g := d["Grain"]; g.Kind != ParamNumber || !slices.Equal(g.Default.Nums, []float32{0.3}) || g.Min != 0 || g.Max != 1 || g.About != "Grain strength. It rises to one." {
		t.Errorf("Grain %+v", g)
	}
	if c := d["Top"]; c.Kind != ParamColor || c.Default.Color == nil || c.Default.Color[0] != ui.Hex("#73b9ed") || c.Default.Color[1] != ui.Hex("#2a5d8f") {
		t.Errorf("Top %+v", c)
	}
	if c := d["Ink"]; c.Kind != ParamColor || c.Default.Bind == nil || c.Default.Bind.Seed != Accent || c.Default.Bind.IsRole {
		t.Errorf("Ink %+v", c)
	}
	if s := d["Stitch"]; s.Kind != ParamToggle || !slices.Equal(s.Default.Nums, []float32{1}) {
		t.Errorf("Stitch %+v", s)
	}
	if l := d["Light"]; l.Kind != ParamPoint || !slices.Equal(l.Default.Nums, []float32{0.5, 0.2}) || l.About != "Where the light sits, in the drawn rectangle." {
		t.Errorf("Light %+v", l)
	}
	if s := d["Scale"]; s.Kind != ParamNumber || s.Min != 1 || s.Max != 4 {
		t.Errorf("Scale %+v", s)
	}
	if warn, err := CheckUniforms(src, &ShaderSlot{}, 2); err != nil || len(warn) != 0 {
		t.Errorf("a good version 2 shader: %q, %v", warn, err)
	}
	long, _ := ParseShaderSource("package main\n\n// " + strings.Repeat("a", 100) + "\n//gorex:param number 0 0 1\nvar A float\n")
	if a := long.Params["A"].About; len(a) != 80 {
		t.Errorf("a description of %d characters, want 80", len(a))
	}

	many := "package main\n\n"
	for i := range 17 {
		many += fmt.Sprintf("//gorex:param number 0 0 1\nvar P%d float\n", i)
	}
	bad := []struct{ name, src, want string }{
		{"undeclared", "var Glow float", "uniform Glow: no //gorex:param"},
		{"unknown kind", "//gorex:param slider 0 0 1\nvar Glow float", "uniform Glow: //gorex:param"},
		{"number args", "//gorex:param number 0 1\nvar Glow float", "uniform Glow: //gorex:param"},
		{"number order", "//gorex:param number 0.5 1 0\nvar Glow float", "uniform Glow: //gorex:param"},
		{"number default", "//gorex:param number 2 0 1\nvar Glow float", "uniform Glow: //gorex:param"},
		{"toggle word", "//gorex:param toggle yes\nvar Glow float", "uniform Glow: //gorex:param"},
		{"point range", "//gorex:param point 0.5 1.5\nvar Glow vec2", "uniform Glow: //gorex:param"},
		{"color value", "//gorex:param color seed:nope\nvar Glow vec4", "uniform Glow: //gorex:param"},
		{"two lines", "//gorex:param number 0 0 1\n//gorex:param number 0 0 1\nvar Glow float", "uniform Glow: //gorex:param"},
		{"number type", "//gorex:param number 0 0 1\nvar Glow vec2", "uniform Glow: a number param needs a float"},
		{"point type", "//gorex:param point 0 0\nvar Glow float", "uniform Glow: a point param needs a vec2"},
		{"color type", "//gorex:param color #fff\nvar Glow float", "uniform Glow: a color param needs a vec3 or a vec4"},
		{"api uniform", "//gorex:param number 0 0 1\nvar Time float", "uniform Time: GoRex sets this uniform"},
		{"too many", strings.TrimPrefix(many, "package main\n\n"), "more than 16"},
	}
	for _, tc := range bad {
		src, err := ParseShaderSource("package main\n\n" + tc.src + "\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n	return vec4(0)\n}\n")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, err := CheckUniforms(src, &ShaderSlot{}, 2); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
		// Version 1 reads no declaration: a bad one does not fail it.
		if tc.name != "number type" && tc.name != "point type" && tc.name != "color type" && tc.name != "api uniform" {
			if _, err := CheckUniforms(src, &ShaderSlot{}, 1); err != nil {
				t.Errorf("%s under version 1: %v", tc.name, err)
			}
		}
	}
}

// TestParseShadersV2: a version 2 manifest's params override the declared
// defaults; a value of another kind or with no declaration drops to the
// default, and a number out of range clamps, each with a warning; seed:
// and role: bindings resolve in each appearance.
func TestParseShadersV2(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "roles": { "titleBar.background": "#2876c8" },
  "assets": { "bg": "bg.kage" },
  "shaders": {
    "api": 2,
    "background": { "source": "bg", "params": {
      "Grain": 7, "Top": "role:titleBar.background", "Ink": "#112233/#445566", "Stitch": false,
      "Light": [1.5, 0.25], "Scale": true, "Extra": 1, "Bad": "seed:nope", "Odd": [1, 2, 3], "Stitch2": null
    } }
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Shaders == nil || m.Shaders.API != 2 || m.Shaders.Background == nil {
		t.Fatalf("shaders %+v", m.Shaders)
	}
	wantParse := []string{
		`shaders.background.params.Bad: not a number, true or false, [x, y] or a color value`,
		`shaders.background.params.Odd: not a number, true or false, [x, y] or a color value`,
		`shaders.background.params.Stitch2: not a number, true or false, [x, y] or a color value`,
	}
	if !slices.Equal(warn, wantParse) {
		t.Errorf("parse warnings\n%q\nwant\n%q", warn, wantParse)
	}
	src, err := ParseShaderSource(declSource)
	if err != nil {
		t.Fatal(err)
	}
	color := func(b Binding, dark int) ui.Color {
		if b.IsRole {
			return [2]ui.Color{ui.Hex("#010101"), ui.Hex("#020202")}[dark]
		}
		return [2]ui.Color{ui.Hex("#030303"), ui.Hex("#040404")}[dark]
	}
	got, rwarn := ResolveParams(src, m.Shaders.Background, color)
	wantResolve := []string{
		`params.Extra: the shader declares no such param (dropped)`,
		`params.Grain: 7 is above the max 1, so it is 1`,
		`params.Light: 1.5,0.25 is outside 0 to 1, so it is 1,0.25`,
		`params.Scale: not a number, the default 2 applies`,
	}
	if !slices.Equal(rwarn, wantResolve) {
		t.Errorf("resolve warnings\n%q\nwant\n%q", rwarn, wantResolve)
	}
	checks := []struct {
		name string
		nums []float32
		col  [2]string
	}{
		{"Grain", []float32{1}, [2]string{}},
		{"Stitch", []float32{0}, [2]string{}},
		{"Light", []float32{1, 0.25}, [2]string{}},
		{"Scale", []float32{2}, [2]string{}},
		{"Top", nil, [2]string{"#010101", "#020202"}},
		{"Ink", nil, [2]string{"#112233", "#445566"}},
	}
	for _, c := range checks {
		p, ok := got[c.name]
		switch {
		case !ok:
			t.Errorf("%s: no value", c.name)
		case c.nums != nil && !slices.Equal(p.Nums, c.nums):
			t.Errorf("%s: %v, want %v", c.name, p.Nums, c.nums)
		case c.nums == nil && (p.Color == nil || p.Color[0] != ui.Hex(c.col[0]) || p.Color[1] != ui.Hex(c.col[1])):
			t.Errorf("%s: %+v, want %v", c.name, p, c.col)
		}
	}
	// A default bound to a seed resolves in each appearance.
	slot := &ShaderSlot{}
	got, _ = ResolveParams(src, slot, color)
	if p := got["Ink"]; p.Color == nil || p.Color[0] != ui.Hex("#030303") || p.Color[1] != ui.Hex("#040404") {
		t.Errorf("a seed default %+v", p)
	}
	if p := got["Top"]; p.Color == nil || p.Color[0] != ui.Hex("#73b9ed") || p.Color[1] != ui.Hex("#2a5d8f") {
		t.Errorf("a light/dark default %+v", p)
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
