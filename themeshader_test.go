package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/theme"
)

// shaderTheme parses a manifest whose assets are the shader sources srcs,
// by name (each its own .kage file), and returns it with its art, as the
// asset load leaves them.
func shaderTheme(t *testing.T, manifest string, srcs map[string]string) (*theme.Manifest, *theme.Art) {
	t.Helper()
	m, warn, err := theme.Parse([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warn {
		t.Logf("parse: %s", w)
	}
	return m, &theme.Art{Bitmaps: map[string]*ui.Bitmap{}, Sources: srcs}
}

// useShaders applies m and makes its checked slots the windows', until the
// test ends.
func useShaders(t *testing.T, m *theme.Manifest, art *theme.Art) *themeShaders {
	t.Helper()
	applyTheme(m)
	set := checkShaders(m, art)
	shaderSet.Store(set)
	t.Cleanup(func() { applyTheme(nil) })
	return set
}

// A gradient in the face's own tones: light grey, as a flat #e4e4e4 face.
const greyGradient = `package main

func Fragment(dst vec4, pos vec2) vec4 {
	y := pos.y / rectSize().y
	v := mix(0.93, 0.86, y)
	return vec4(v, v, v, 1)
}
`

// fakeGPU makes the painters draw theme shaders as on a GPU, in the
// interpreter, until the test ends.
func fakeGPU(t *testing.T) {
	themeShaderGPU = func(*ui.Painter) bool { return true }
	t.Cleanup(func() { themeShaderGPU = (*ui.Painter).Accelerated })
}

// TestThemeShaderChecks: a shader that fails a check at load drops with a
// warning, and its slot draws the flat color; one that passes draws.
func TestThemeShaderChecks(t *testing.T) {
	cases := []struct {
		name, src, param, want string
	}{
		{"passes", greyGradient, "", ""},
		{"bounded loop", "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tv := 0.85\n\tfor i := 0; i < 4; i++ {\n\t\tv += 0.02\n\t}\n\treturn vec4(v, v, v, 1)\n}\n", "", ""},
		{"branch on time", "package main\n\nvar Time float\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tif Time > 4 {\n\t\treturn vec4(0.5, 0.5, 0.5, 1)\n\t}\n\treturn vec4(0.9, 0.9, 0.9, 1)\n}\n", "", "text has"},
		{"branch on window", "package main\n\nvar Window vec2\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tv := 0.0\n\tif Window.x > 200 {\n\t\tfor i := 0; i < 250; i++ {\n\t\t\tv += sin(pos.x*float(i)) * cos(pos.y+float(i)) * 0.0001\n\t\t}\n\t}\n\treturn vec4(0.9+v, 0.9, 0.9, 1)\n}\n", "", "times the built-in background"},
		{"compile", "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\treturn nope\n}\n", "", "does not compile"},
		{"effect", "package main\n\nfunc ColorEffect(pos vec2, c vec4) vec4 {\n\treturn c\n}\n", "", "entry point must be Fragment"},
		{"backdrop", "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\treturn backdropAt(pos)\n}\n", "", "reads its backdrop"},
		{"loop bound", "package main\n\nvar N float\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tv := 0.0\n\tfor i := 0.0; i < N; i++ {\n\t\tv += 0.01\n\t}\n\treturn vec4(v, v, v, 1)\n}\n", `"N": 3`, "not constant"},
		{"loop runs", "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tv := 0.0\n\tfor i := 0; i < 300; i++ {\n\t\tv += 0.001\n\t}\n\treturn vec4(v, v, v, 1)\n}\n", "", "run 300 times"},
		{"uniform type", "package main\n\nvar Time vec2\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\treturn vec4(Time.x, 0, 0, 1)\n}\n", "", "uniform Time"},
		{"param type", "package main\n\nvar Top vec4\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\treturn Top\n}\n", `"Top": 0.5`, "uniform Top"},
		{"cost", "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tv := 0.0\n\tfor i := 0; i < 250; i++ {\n\t\tv += sin(pos.x*float(i)) * cos(pos.y+float(i)) * 0.0001\n\t}\n\treturn vec4(0.9+v, 0.9, 0.9, 1)\n}\n", "", "times the built-in background"},
		{"contrast", "package main\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\treturn vec4(0.5, 0.5, 0.5, 1)\n}\n", "", "text has"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := ""
			if tc.param != "" {
				params = `, "params": {` + tc.param + `}`
			}
			m, art := shaderTheme(t, `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": 1, "faces": { "titleBar.background": { "source": "bar"`+params+` } } }
}`, map[string]string{"bar": tc.src})
			set := useShaders(t, m, art)
			_, drew := faceFill(roleTitleBarBackground, ui.Hex("#e4e4e4"))
			if tc.want == "" {
				if !drew || len(set.warn) > 0 {
					t.Fatalf("a good shader: drawn %v, warnings %q", drew, set.warn)
				}
				return
			}
			if drew {
				t.Fatalf("the slot draws, want it dropped for %q", tc.want)
			}
			if len(set.warn) != 1 || !strings.Contains(set.warn[0], tc.want) || !strings.Contains(set.warn[0], "dropped") {
				t.Fatalf("warnings %q, want one with %q", set.warn, tc.want)
			}
			// The slot draws its flat color.
			img := ui.NewTester(func(c *ui.Context) {
				paintFace(ui.Box(c).Fill(), paletteOf(false), roleTitleBarBackground, ui.Hex("#e4e4e4"))
			}, 20, 10).Image()
			if o := img.PixOffset(5, 5); [3]uint8(img.Pix[o:o+3]) != [3]uint8{0xe4, 0xe4, 0xe4} {
				t.Errorf("a dropped face paints %v, want its flat color", img.Pix[o:o+4])
			}
		})
	}
}

// TestFaceShaderContrast: a light gradient face in a dark window gets the
// dark text of its flat color's ladder, and passes; a face shader that
// crosses the flip point of the ladder fails.
func TestFaceShaderContrast(t *testing.T) {
	const manifest = `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": 1, "faces": { "titleBar.background": { "source": "bar" } } }
}`
	m, art := shaderTheme(t, manifest, map[string]string{"bar": greyGradient})
	set := useShaders(t, m, art)
	if set.faces[roleTitleBarBackground] == nil {
		t.Fatalf("a light gradient on a light face fails: %q", set.warn)
	}
	k, _, _ := themedFace(paletteOf(true), roleTitleBarBackground)
	if k.text != lightColors.text {
		t.Errorf("the light face in a dark window has text %v, want the light ladder's %v", k.text, lightColors.text)
	}
	m, art = shaderTheme(t, manifest, map[string]string{"bar": `package main

func Fragment(dst vec4, pos vec2) vec4 {
	v := 1 - pos.y/rectSize().y
	return vec4(v, v, v, 1)
}
`})
	set = useShaders(t, m, art)
	if set.faces[roleTitleBarBackground] != nil || len(set.warn) == 0 || !strings.Contains(set.warn[0], "below") {
		t.Errorf("a face from white to black passes: %q", set.warn)
	}
}

// solidBackground is a background shader of one color in each
// appearance, under which the window's text reads.
const solidBackground = `package main

var Dark float

func Fragment(dst vec4, pos vec2) vec4 {
	return mix(vec4(0.9, 0.8, 0.7, 1), vec4(0.12, 0.1, 0.1, 1), Dark)
}
`

// TestThemeBackgroundShader: with no background slot, the background is
// the built-in one, byte for byte, as with no theme shader; with one that
// passed, the window's pixel is the shader's color.
func TestThemeBackgroundShader(t *testing.T) {
	fakeGPU(t)
	draw := func() []uint8 {
		return ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, &lightColors, 1, false, false) })
		}, 60, 40).Image().Pix
	}
	before := draw()
	m, art := shaderTheme(t, `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": 1, "faces": { "titleBar.background": { "source": "bar" } } }
}`, map[string]string{"bar": greyGradient})
	useShaders(t, m, art)
	if after := draw(); string(after) != string(before) {
		t.Error("a theme with no background shader changes the background")
	}
	m, art = shaderTheme(t, `{
  "assets": { "bg": "bg.kage" },
  "shaders": { "api": 1, "background": { "source": "bg" } }
}`, map[string]string{"bg": solidBackground})
	if set := useShaders(t, m, art); set.background == nil {
		t.Fatalf("the background slot fails: %q", set.warn)
	}
	img := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, &lightColors, 1, false, false) })
	}, 60, 40).Image()
	if o := img.PixOffset(30, 20); [3]uint8(img.Pix[o:o+3]) != [3]uint8{230, 204, 179} {
		t.Errorf("the theme's background paints %v, want 230 204 179", img.Pix[o:o+4])
	}
}

// TestThemeShaderCPU: on the CPU renderer no theme shader draws: the
// background is the built-in one, and each face its flat color.
func TestThemeShaderCPU(t *testing.T) {
	view := func(c *ui.Context) {
		ui.Column(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, &lightColors, 1, false, false) }).Children(func() {
			paintFace(ui.Box(c).Height(20).Width(40), paletteOf(false), roleTitleBarBackground, ui.Hex("#e4e4e4"))
		})
	}
	plain := ui.NewTester(view, 60, 40).Image().Pix
	m, art := shaderTheme(t, `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bg": "bg.kage", "bar": "bar.kage" },
  "shaders": { "api": 1, "background": { "source": "bg" }, "faces": { "titleBar.background": { "source": "bar" } } }
}`, map[string]string{"bg": solidBackground, "bar": `package main

func Fragment(dst vec4, pos vec2) vec4 {
	return vec4(0.88, 0.88, 0.88, 1)
}
`})
	set := useShaders(t, m, art)
	if set.background == nil || set.faces[roleTitleBarBackground] == nil {
		t.Fatalf("the slots fail: %q", set.warn)
	}
	img := ui.NewTester(view, 60, 40).Image()
	if o := img.PixOffset(10, 10); [3]uint8(img.Pix[o:o+3]) != [3]uint8{0xe4, 0xe4, 0xe4} {
		t.Errorf("the face paints %v on the CPU renderer, want its flat color", img.Pix[o:o+4])
	}
	if o := img.PixOffset(50, 30); string(img.Pix[o:o+4]) != string(plain[o:o+4]) {
		t.Errorf("the background paints %v on the CPU renderer, want the built-in %v", img.Pix[o:o+4], plain[o:o+4])
	}
}

// TestThemeShaderStill: a shader that reads Time asks for frames while
// the theme's shaders move, and for none under each condition of the
// DSK-010 still rule; still, Time stands.
func TestThemeShaderStill(t *testing.T) {
	fakeGPU(t)
	battery := false
	shaderOnBattery = func() bool { return battery }
	ambience, animation := prefs.Ambience, prefs.Animation
	t.Cleanup(func() {
		shaderOnBattery = func() bool { return false }
		prefs.Ambience, prefs.Animation = ambience, animation
	})
	m, art := shaderTheme(t, `{
  "assets": { "bg": "bg.kage" },
  "shaders": { "api": 1, "background": { "source": "bg" } }
}`, map[string]string{"bg": `package main

var Time, Dark float

func Fragment(dst vec4, pos vec2) vec4 {
	v := mix(0.85, 0.12, Dark) + 0.05*sin(Time)
	return vec4(v, v, v, 1)
}
`})
	if set := useShaders(t, m, art); set.background == nil || !set.anyTime {
		t.Fatalf("the slot fails: %q", set.warn)
	}
	cases := []struct {
		name string
		set  func(*ui.Tester)
	}{
		{"ambience still", func(*ui.Tester) { prefs.Ambience = ambienceStill }},
		{"animation off", func(*ui.Tester) { prefs.Animation = "off" }},
		{"less motion", func(tt *ui.Tester) { tt.SetPreferences(ui.Preferences{ReduceMotion: true}) }},
		{"not active", func(tt *ui.Tester) { tt.SetWindowActive(false) }},
		{"hidden", func(tt *ui.Tester) { tt.SetWindowHidden(true) }},
		{"battery", func(*ui.Tester) { battery = true }},
		{"cpu", func(*ui.Tester) { themeShaderGPU = (*ui.Painter).Accelerated }},
	}
	for _, tc := range append([]struct {
		name string
		set  func(*ui.Tester)
	}{{"moving", func(*ui.Tester) {}}}, cases...) {
		t.Run(tc.name, func(t *testing.T) {
			prefs.Ambience, prefs.Animation, battery = "", "", false
			themeShaderGPU = func(*ui.Painter) bool { return true }
			tt := ui.NewTester(func(c *ui.Context) {
				moves := themeShaderMoves(c)
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, &lightColors, 1, false, moves) })
			}, 40, 30)
			tc.set(tt)
			tt.Frame()
			t0 := currentFrame().time
			tt.Advance(500 * 1e6)
			tt.Frame()
			moved := currentFrame().time != t0
			if want := tc.name == "moving"; tt.FramePending() != want || moved != want {
				t.Errorf("asks for a frame %v and Time moves %v, want %v", tt.FramePending(), moved, want)
			}
		})
	}
}

// TestThemeShaderRange: a version 2 shader that reads well at its defaults
// and badly at a number's max or a toggle's other value drops, and the
// warning names the param and the value; the same shader with a range
// that stays readable draws, and version 1 draws no variant.
func TestThemeShaderRange(t *testing.T) {
	src := func(glowMax, dim string) string {
		return "package main\n\n// How much the face darkens.\n//gorex:param number 0 0 " + glowMax + "\nvar Glow float\n\n//gorex:param toggle " + dim + "\nvar Dim float\n\n//gorex:param point 0.5 0.5\nvar Light vec2\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\ty := pos.y / rectSize().y\n\tv := mix(0.93, 0.86, y) - Glow*0.4 - Dim*0.04 + Light.x*0.01\n\treturn vec4(v, v, v, 1)\n}\n"
	}
	cases := []struct {
		name, api, src, params, want string
	}{
		{"readable range", "2", src("0.1", "off"), "", ""},
		{"number max", "2", src("1", "off"), "", "Glow at 1 (max): "},
		{"manifest at max", "2", src("1", "off"), `"Glow": 1`, ": in the light appearance at 0 s"},
		{"toggle", "2", strings.Replace(src("0.1", "off"), "Dim*0.04", "Dim*0.4", 1), "", "Dim on: "},
		{"point", "2", strings.Replace(src("0.1", "off"), "+ Light.x*0.01", "- max(Light.x-0.6, 0)", 1), "", "Light at 1,1: "},
		{"cost", "2", "package main\n\n//gorex:param number 0 0 1\nvar Heavy float\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tv := 0.0\n\tif Heavy > 0.5 {\n\t\tfor i := 0; i < 250; i++ {\n\t\t\tv += sin(pos.x*float(i)) * cos(pos.y+float(i)) * 0.0001\n\t\t}\n\t}\n\treturn vec4(0.9+v, 0.9, 0.9, 1)\n}\n", "", "Heavy at 1 (max): costs"},
		{"version 1", "1", src("1", "off"), `"Glow": 0, "Dim": 0, "Light": [0.5, 0.5]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, art := shaderTheme(t, `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": `+tc.api+`, "faces": { "titleBar.background": { "source": "bar", "params": {`+tc.params+`} } } }
}`, map[string]string{"bar": tc.src})
			set := useShaders(t, m, art)
			_, drew := faceFill(roleTitleBarBackground, ui.Hex("#e4e4e4"))
			if tc.want == "" {
				if !drew || len(set.warn) > 0 {
					t.Fatalf("drawn %v, warnings %q", drew, set.warn)
				}
				return
			}
			if drew || len(set.warn) != 1 || !strings.Contains(set.warn[0], tc.want) || !strings.Contains(set.warn[0], "dropped") {
				t.Fatalf("drawn %v, warnings %q, want one with %q", drew, set.warn, tc.want)
			}
		})
	}
	// A background whose variant hides the sidebar's text fails check 6.
	m, art := shaderTheme(t, `{
  "assets": { "bg": "bg.kage" },
  "shaders": { "api": 2, "background": { "source": "bg" } }
}`, map[string]string{"bg": "package main\n\nvar Dark float\n\n//gorex:param number 0 0 1\nvar Dim float\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tc := mix(vec4(0.9, 0.8, 0.7, 1), vec4(0.12, 0.1, 0.1, 1), Dark)\n\treturn mix(c, vec4(0.5, 0.5, 0.5, 1), Dim)\n}\n"})
	if set := useShaders(t, m, art); set.background != nil || len(set.warn) != 1 || !strings.Contains(set.warn[0], "Dim at 1 (max): behind the sidebar") {
		t.Errorf("a background unreadable at a number's max: drawn %v, warnings %q", set.background != nil, set.warn)
	}
}

// TestThemeShaderBindings: a version 2 color param bound to seed: or
// role: takes the theme's seed or flat color of the role in each
// appearance, not premultiplied; a manifest value of another kind keeps
// the default and warns.
func TestThemeShaderBindings(t *testing.T) {
	m, art := shaderTheme(t, `{
  "seeds": { "light": { "accent": "#336699" }, "dark": { "accent": "#996633" } },
  "roles": { "titleBar.background": { "light": "#e4e4e4", "dark": "#202020" } },
  "assets": { "bg": "bg.kage" },
  "shaders": { "api": 2, "background": { "source": "bg", "params": { "Mix": true, "Ink": "role:titleBar.background" } } }
}`, map[string]string{"bg": "package main\n\n//gorex:param color seed:accent\nvar Ink vec4\n\n//gorex:param color seed:accent\nvar Ink2 vec4\n\n//gorex:param color role:titleBar.background\nvar Ink3 vec4\n\n//gorex:param number 0 0 1\nvar Mix float\n\nfunc Fragment(dst vec4, pos vec2) vec4 {\n\tif pos.x < 2 {\n\t\treturn Ink\n\t}\n\tif pos.x < 4 {\n\t\treturn Ink2\n\t}\n\treturn Ink3\n}\n"})
	pal := [2]colors{themed(lightSeeds, m, 0), themed(darkSeeds, m, 1)}
	e := &slotEnv{api: 2, sd: [2]seeds{themeSeeds(lightSeeds, m, 0), themeSeeds(darkSeeds, m, 1)}, pal: &pal, art: art}
	sl, warn, err := compileSlot(m.Shaders.Background, e, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"params.Mix: not a number, the default 0 applies"}; !slices.Equal(warn, want) {
		t.Errorf("warnings %q, want %q", warn, want)
	}
	for i, want := range [2][3]string{{"#e4e4e4", "#336699", "#e4e4e4"}, {"#202020", "#996633", "#202020"}} {
		img := renderSlot(sl, 6, 1, 1, ui.Point{X: 6, Y: 1}, i == 1, shaderFrame{})
		for j, x := range []int{0, 3, 5} {
			o := img.PixOffset(x, 0)
			if c := ui.Hex(want[j]); [3]uint8(img.Pix[o:o+3]) != [3]uint8{c.R, c.G, c.B} {
				t.Errorf("appearance %d, pixel %d: %v, want %s", i, x, img.Pix[o:o+4], want[j])
			}
		}
	}
}

// BenchmarkThemeShaderChecks measures the checks at load of a pack of one
// background shader and four face shaders, as docs/DSK-012 asks phase 2
// to measure, and the same pack in version 2 with 8 number params in each
// shader, as phase 6 asks: go test . -run '^$' -bench ThemeShaderChecks.
func BenchmarkThemeShaderChecks(b *testing.B) {
	b.Run("v1", func(b *testing.B) { benchShaderChecks(b, "1", packBackground, packBar, packRow) })
	b.Run("v2", func(b *testing.B) {
		benchShaderChecks(b, "2",
			packV2(packBackground, "c += (grain", "c += vec3(0.001) * SUM\n\tc += (grain"),
			packV2(packBar, "c += vec3(0.02", "c += vec3(0.001) * SUM\n\tc += vec3(0.02"),
			packV2(packRow, "y), 1)", "y)+vec3(0.001)*SUM, 1)"))
	})
}

func benchShaderChecks(b *testing.B, api, bg, bar, row string) {
	m, _, err := theme.Parse([]byte(`{
  "roles": {
    "titleBar.background": "#174394", "paneHeader.background": "#174394",
    "sideBar.tabActiveBackground": "#e6eefb", "sideBar.tabHoverBackground": "#eef3fc"
  },
  "assets": { "bg": "bg.kage", "bar": "bar.kage", "row": "row.kage" },
  "shaders": {
    "api": ` + api + `,
    "background": { "source": "bg", "params": { "Opacity": 0.2 } },
    "faces": {
      "titleBar.background": { "source": "bar", "params": { "Top": "#1a469c", "Bottom": "#0f3480" } },
      "paneHeader.background": { "source": "bar", "params": { "Top": "#1a469c", "Bottom": "#0f3480" } },
      "sideBar.tabActiveBackground": { "source": "row", "params": { "Top": "#ffffff", "Bottom": "#d6e4fa" } },
      "sideBar.tabHoverBackground": { "source": "row", "params": { "Top": "#ffffff", "Bottom": "#e4edfb" } }
    }
  }
}`))
	if err != nil {
		b.Fatal(err)
	}
	art := &theme.Art{Bitmaps: map[string]*ui.Bitmap{}, Sources: map[string]string{"bg": bg, "bar": bar, "row": row}}
	builtinCost()
	b.ResetTimer()
	for b.Loop() {
		if set := checkShaders(m, art); set.background == nil || len(set.faces) != 4 {
			b.Fatalf("slots fail: %q", set.warn)
		}
	}
}

// packParams declares 8 number params N1 to N8 for the version 2 case of
// BenchmarkThemeShaderChecks, each at a default inside its range so that
// each draws two variants, and sum is an expression of all of them.
func packParams() (decl, sum string) {
	var terms []string
	for i := 1; i <= 8; i++ {
		decl += fmt.Sprintf("//gorex:param number 0.5 0 1\nvar N%d float\n\n", i)
		terms = append(terms, fmt.Sprintf("N%d", i))
	}
	return decl, "(" + strings.Join(terms, " + ") + ")"
}

// packV2 turns a pack shader of version 1 into one of version 2: its
// params Top, Bottom and Opacity declared, and 8 numbers that shift its
// color a little, where new, with SUM for their sum, replaces old.
func packV2(src, old, new string) string {
	decl, sum := packParams()
	src = strings.Replace(src, "var Top, Bottom vec4\n", "//gorex:param color #ffffff\nvar Top vec4\n\n//gorex:param color #d6e4fa\nvar Bottom vec4\n\n"+decl, 1)
	src = strings.Replace(src, "var Opacity float\n", "\n//gorex:param number 0.2 0 0.3\nvar Opacity float\n\n"+decl, 1)
	return strings.Replace(src, old, strings.Replace(new, "SUM", sum, 1), 1)
}

const packBackground = `package main

// Paper: a soft vertical gradient, a picture laid over it, and grain.
var Dark float
var Opacity float

func grain(p vec2) float {
	return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453)
}

func Fragment(dst vec4, pos vec2) vec4 {
	uv := pos / rectSize()
	light := mix(vec3(0.97, 0.94, 0.88), vec3(0.92, 0.87, 0.78), uv.y)
	dark := mix(vec3(0.15, 0.13, 0.12), vec3(0.10, 0.09, 0.08), uv.y)
	c := mix(light, dark, Dark)
	p := image0Sample(uv * image0Size())
	c = c*(1-p.a*Opacity) + p.rgb*Opacity
	c += (grain(floor(dst.xy)) - 0.5) * 0.04
	return vec4(clamp(c, 0, 1), 1)
}
`

const packBar = `package main

// An XP title bar: a vertical gradient from Top to Bottom with a sheen in
// its upper third.
var Top, Bottom vec4

func Fragment(dst vec4, pos vec2) vec4 {
	y := pos.y / rectSize().y
	c := mix(Top.rgb, Bottom.rgb, y)
	c += vec3(0.02, 0.03, 0.04) * (1 - smoothstep(0.0, 0.35, y))
	return vec4(c, 1)
}
`

const packRow = `package main

// An XP selected row: a light vertical gradient.
var Top, Bottom vec4

func Fragment(dst vec4, pos vec2) vec4 {
	y := pos.y / rectSize().y
	return vec4(mix(Top.rgb, Bottom.rgb, y), 1)
}
`

// TestThemeShaderSwitch: the checks of a theme the user left store
// nothing, a set of another theme than the selected one draws nothing,
// and a reload of the same theme keeps its checked set.
func TestThemeShaderSwitch(t *testing.T) {
	old := prefs.Theme
	t.Cleanup(func() { prefs.Theme = old; applyTheme(nil) })
	m, art := shaderTheme(t, `{
  "roles": { "titleBar.background": "#e4e4e4" },
  "assets": { "bar": "bar.kage" },
  "shaders": { "api": 1, "faces": { "titleBar.background": { "source": "bar" } } }
}`, map[string]string{"bar": greyGradient})
	drawn := func() bool {
		_, ok := faceFill(roleTitleBarBackground, ui.Hex("#e4e4e4"))
		return ok
	}

	prefs.Theme = "a"
	applyTheme(m)
	storeThemeShaders("a", m, art, lookGen.Load())
	set := activeShaders()
	if set == nil || !drawn() {
		t.Fatal("theme a's checked face does not draw")
	}
	// A reload, as loadTheme makes it: applyTheme(nil), then the theme.
	applyTheme(nil)
	applyTheme(m)
	if activeShaders() != set || !drawn() {
		t.Error("a reload of the same theme dropped its checked set")
	}

	// The checks of a start a load, and the user picks b before they end.
	gen := lookGen.Load()
	prefs.Theme = "b"
	applyTheme(m)
	storeThemeShaders("a", m, art, gen)
	if activeShaders() != nil || drawn() {
		t.Error("the checks of a theme the user left stored their set")
	}
	// A set of a, however it got there, draws nothing under b.
	shaderSet.Store(set)
	if activeShaders() != nil || drawn() {
		t.Error("theme a's set draws while b is selected")
	}
}
