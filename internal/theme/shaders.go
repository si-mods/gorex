package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

// The shaders key of docs/DSK-012-theme-shaders.md: the shader API this
// build reads, the slots a theme fills, the param parser and the uniform
// check. The app compiles, checks and draws them (themeshader.go).

// The versions of the shader API this build reads (docs/DSK-012, Shader
// API version 2): version 1 sets params from the manifest alone; version 2
// reads the params a shader declares, and the manifest overrides their
// defaults.
const (
	ShaderAPI1 = 1
	ShaderAPI2 = 2
)

// The caps of the shaders key.
const (
	maxFaceShaders  = 8
	maxShaderImages = 4
	maxParams       = 16
	maxParamList    = 16
	// Version 2: the layers of a slot, and of a theme over all its slots.
	maxSlotLayers  = 4
	maxThemeLayers = 16
)

// Shaders are the slots of a theme that its shaders fill: the window's
// background and the faces of roles. A nil slot draws as the theme's
// colors do.
type Shaders struct {
	// API is the version of the shader API: ShaderAPI1 or ShaderAPI2.
	API        int
	Background *ShaderSlot
	Faces      map[Role]*ShaderSlot
}

// ShaderSlot is one slot: its layers, drawn from the first to the last.
// Version 1 has one layer; version 2 up to maxSlotLayers (docs/DSK-012,
// Layers).
type ShaderSlot struct {
	Layers []ShaderLayer
}

// ShaderLayer is one shader of a slot: the .kage asset of its source, up
// to four image assets it reads as image0 to image3, the values of its
// params, and its opacity from 0 to 1 (1 in version 1).
type ShaderLayer struct {
	Source  string
	Images  []string
	Params  map[string]Param
	Opacity float32
}

// Param is the value of a uniform the theme sets: numbers, or a color of
// each appearance, light then dark. Version 2 adds a toggle's value and a
// color bound to the theme's seed or role, which the app resolves in each
// appearance (ResolveParams).
type Param struct {
	Nums  []float32
	Color *[2]ui.Color
	On    *bool
	Bind  *Binding
}

// Binding is a color of version 2 that follows the theme: seed:<name>, its
// resolved seed, or role:<name>, its flat color of a role.
type Binding struct {
	IsRole bool
	Seed   Seed
	Role   Role
}

// FaceRoles are the roles a face shader may fill: the five faces that
// surface resolves and the four faces of DSK-012's new roles.
var FaceRoles = []Role{
	ActiveGroupBackground, ActiveGroupTabBackground, TrackBackground, TabHoverBackground, TabActiveBackground,
	TitleBarBackground, SideBarBackground, PaneHeaderBackground, ButtonBackground,
}

// builtinFace reports whether the built-in palette has a flat color for
// role: the five sidebar faces have one, the four new roles none.
func builtinFace(r Role) bool { return r < TitleBarBackground }

// IsKage reports whether file is a shader source: a .kage file.
func IsKage(file string) bool { return strings.EqualFold(pathExt(file), ".kage") }

func pathExt(file string) string {
	if i := strings.LastIndexByte(file, '.'); i >= 0 {
		return file[i:]
	}
	return ""
}

// parseShaders reads the shaders key. An unknown api drops it all; a slot
// with a bad source, an image that is not an image asset, a face role that
// is not a face, a face past the cap or a face with no flat color drops,
// each with a warning. roles are the theme's roles, light then dark.
func parseShaders(raw json.RawMessage, assets map[string]Asset, roles [2]map[Role]ui.Color, w *warner) *Shaders {
	m, ok := w.fields(raw, "shaders", "api", "background", "ambient", "faces")
	if !ok {
		return nil
	}
	var api float64
	if json.Unmarshal(m["api"], &api) != nil || api != ShaderAPI1 && api != ShaderAPI2 {
		w.add("shaders.api: %s is not an API this build knows (shaders dropped)", strings.TrimSpace(string(m["api"])))
		return nil
	}
	s := &Shaders{API: int(api), Faces: map[Role]*ShaderSlot{}}
	layers := 0 // over all slots, for maxThemeLayers
	if raw, ok := m["background"]; ok {
		s.Background = parseSlot(raw, "shaders.background", s.API, assets, &layers, w)
	}
	if _, ok := m["ambient"]; ok {
		w.add("shaders.ambient: not drawn by this build")
	}
	if raw, ok := m["faces"]; ok {
		var faces map[string]json.RawMessage
		if json.Unmarshal(raw, &faces) != nil {
			w.add("shaders.faces: not an object")
		}
		for _, name := range slices.Sorted(maps.Keys(faces)) {
			where := "shaders.faces." + name
			r, ok := RoleByName(name)
			switch {
			case !ok || !slices.Contains(FaceRoles, r):
				w.add("%s: not a face role (dropped)", where)
				continue
			case r == ButtonBackground:
				// MyGo's widgets paint this face in the theme's accent; no
				// shader reaches it.
				w.add("%s: the widgets paint this face, which takes no shader (dropped)", where)
				continue
			case len(s.Faces) == maxFaceShaders:
				w.add("%s: more than %d face shaders (dropped)", where, maxFaceShaders)
				continue
			}
			_, light := roles[0][r]
			_, dark := roles[1][r]
			if !light || !dark {
				if !builtinFace(r) {
					w.add("%s: needs roles.%s as its flat color (dropped)", where, name)
					continue
				}
				w.add("%s: no roles.%s, the built-in flat color applies", where, name)
			}
			if slot := parseSlot(faces[name], where, s.API, assets, &layers, w); slot != nil {
				s.Faces[r] = slot
			}
		}
	}
	if s.Background == nil && len(s.Faces) == 0 {
		return nil
	}
	return s
}

// parseSlot reads one slot of the API version api, or returns nil with a
// warning. In version 2 a slot is a layer or a list of 1 to maxSlotLayers
// layers. A layer that does not parse, a list past maxSlotLayers, and a
// slot whose layers would pass maxThemeLayers, counted in layers over the
// theme so far, drop the whole slot, so that it never draws half its
// look. A warning of a layer of a list of more than one names the layer:
// "shaders.background layer 2: ...".
func parseSlot(raw json.RawMessage, where string, api int, assets map[string]Asset, layers *int, w *warner) *ShaderSlot {
	var list []json.RawMessage
	if api != ShaderAPI2 || json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	} else {
		switch {
		case len(list) == 0:
			w.add("%s: no layers (dropped)", where)
			return nil
		case len(list) > maxSlotLayers:
			w.add("%s: more than %d layers (dropped)", where, maxSlotLayers)
			return nil
		}
	}
	slot := &ShaderSlot{}
	for i, raw := range list {
		var lw warner
		l := parseLayer(raw, where, api, assets, &lw)
		for _, m := range lw {
			if len(list) > 1 {
				m = LayerWhere(where, i) + ": " + strings.TrimLeft(strings.TrimPrefix(m, where), ".: ")
			}
			w.add("%s", m)
		}
		if l == nil {
			if len(list) > 1 {
				w.add("%s: a layer did not parse (dropped)", where)
			}
			return nil
		}
		slot.Layers = append(slot.Layers, *l)
	}
	if *layers+len(slot.Layers) > maxThemeLayers {
		w.add("%s: more than %d layers in the theme (dropped)", where, maxThemeLayers)
		return nil
	}
	*layers += len(slot.Layers)
	return slot
}

// LayerWhere names the layer i of a slot where in a warning: "shaders.background layer 2".
func LayerWhere(where string, i int) string { return fmt.Sprintf("%s layer %d", where, i+1) }

// parseLayer reads one layer of the API version api, or returns nil with
// a warning. Version 2 adds opacity, from 0 to 1, which clamps.
func parseLayer(raw json.RawMessage, where string, api int, assets map[string]Asset, w *warner) *ShaderLayer {
	keys := []string{"source", "images", "params"}
	if api == ShaderAPI2 {
		keys = append(keys, "opacity")
	}
	f, ok := w.fields(raw, where, keys...)
	if !ok {
		return nil
	}
	slot := ShaderLayer{Opacity: 1}
	if api == ShaderAPI2 {
		if o, ok := w.num(f, "opacity", where, 0, 1, 1); ok {
			slot.Opacity = o
		}
	}
	if json.Unmarshal(f["source"], &slot.Source) != nil {
		w.add("%s.source: not a string (dropped)", where)
		return nil
	}
	if a, ok := assets[slot.Source]; !ok || !IsKage(a.File) {
		w.add("%s.source: no .kage asset %q (dropped)", where, slot.Source)
		return nil
	}
	if raw, ok := f["images"]; ok {
		var names []string
		if json.Unmarshal(raw, &names) != nil {
			w.add("%s.images: not a list of names (dropped)", where)
			return nil
		}
		if len(names) > maxShaderImages {
			w.add("%s.images: more than %d, the rest dropped", where, maxShaderImages)
			names = names[:maxShaderImages]
		}
		for _, n := range names {
			if a, ok := assets[n]; !ok || a.Frames > 0 || IsKage(a.File) {
				w.add("%s.images: no image asset %q (dropped)", where, n)
				return nil
			}
		}
		slot.Images = names
	}
	if raw, ok := f["params"]; ok {
		var ps map[string]json.RawMessage
		if json.Unmarshal(raw, &ps) != nil {
			w.add("%s.params: not an object", where)
		}
		slot.Params = map[string]Param{}
		for _, name := range slices.Sorted(maps.Keys(ps)) {
			if len(slot.Params) == maxParams {
				w.add("%s.params: more than %d, the rest dropped", where, maxParams)
				break
			}
			if !exportedIdent(name) {
				w.add("%s.params: bad uniform name %q", where, name)
				continue
			}
			if _, ok := apiType(name); ok {
				w.add("%s.params.%s: GoRex sets this uniform (dropped)", where, name)
				continue
			}
			if api == ShaderAPI2 {
				// The kind of a value is checked against the shader's
				// declaration where the shader is read (ResolveParams).
				p, ok := parseParamV2(ps[name])
				if !ok {
					w.add("%s.params.%s: not a number, true or false, [x, y] or a color value", where, name)
					continue
				}
				slot.Params[name] = p
				continue
			}
			p, ok := parseParam(ps[name])
			if !ok {
				w.add("%s.params.%s: not a number, a list of up to %d numbers or a color", where, name, maxParamList)
				continue
			}
			slot.Params[name] = p
		}
	}
	return &slot
}

// parseParam reads a param: a number, a list of numbers, or a color as a
// role value.
func parseParam(raw json.RawMessage) (Param, bool) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return Param{Nums: []float32{float32(f)}}, true
	}
	var list []float64
	if json.Unmarshal(raw, &list) == nil {
		if len(list) == 0 || len(list) > maxParamList {
			return Param{}, false
		}
		p := Param{Nums: make([]float32, len(list))}
		for i, v := range list {
			p.Nums[i] = float32(v)
		}
		return p, true
	}
	light, lok := ColorFor(raw, false)
	dark, dok := ColorFor(raw, true)
	if !lok || !dok {
		return Param{}, false
	}
	return Param{Color: &[2]ui.Color{light, dark}}, true
}

// parseParamV2 reads a param of version 2: a number, true or false, a
// point [x, y], or a color value (parseColorValue, or a {"light", "dark"}
// pair as a role value).
func parseParamV2(raw json.RawMessage) (Param, bool) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return Param{}, false // json.Unmarshal leaves a bool false on null
	}
	var on bool
	if json.Unmarshal(raw, &on) == nil {
		return Param{On: &on}, true
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return Param{Nums: []float32{float32(f)}}, true
	}
	var xy []float64
	if json.Unmarshal(raw, &xy) == nil {
		if len(xy) != 2 {
			return Param{}, false
		}
		return Param{Nums: []float32{float32(xy[0]), float32(xy[1])}}, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return parseColorValue(s)
	}
	return parseParam(raw)
}

// parseColorValue reads a color value of version 2: a hex color for both
// appearances, two hex colors light/dark, seed:<name> or role:<name>.
func parseColorValue(s string) (Param, bool) {
	if name, ok := strings.CutPrefix(s, "seed:"); ok {
		id, ok := SeedByName(name)
		return Param{Bind: &Binding{Seed: id}}, ok
	}
	if name, ok := strings.CutPrefix(s, "role:"); ok {
		r, ok := RoleByName(name)
		return Param{Bind: &Binding{IsRole: true, Role: r}}, ok
	}
	light, dark, pair := strings.Cut(s, "/")
	if !pair {
		dark = light
	}
	l, lok := Hex(light)
	d, dok := Hex(dark)
	if !lok || !dok {
		return Param{}, false
	}
	return Param{Color: &[2]ui.Color{l, d}}, true
}

// kind returns the kind of declaration a manifest value of version 2
// fits, 0 for none.
func (p Param) kind() ParamKind {
	switch {
	case p.On != nil:
		return ParamToggle
	case p.Color != nil || p.Bind != nil:
		return ParamColor
	case len(p.Nums) == 1:
		return ParamNumber
	case len(p.Nums) == 2:
		return ParamPoint
	}
	return 0
}

// exportedIdent reports whether s is an exported Go identifier of at most
// 64 bytes: what names a uniform.
func exportedIdent(s string) bool {
	if s == "" || len(s) > 64 || !unicode.IsUpper(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !(r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')) {
			return false
		}
	}
	return true
}

// UniformType is the type of a uniform as its source declares it: a
// basic type (float, vec2, int, ...) and, for an array, its length.
type UniformType struct {
	Base string
	Len  int // 0 for no array
}

func (t UniformType) String() string {
	if t.Len > 0 {
		return fmt.Sprintf("[%d]%s", t.Len, t.Base)
	}
	return t.Base
}

// The uniforms of API version 1 that GoRex sets, besides Seed<Name>.
var apiUniforms = map[string]UniformType{
	"Time":   {Base: "float"},
	"Dark":   {Base: "float"},
	"Origin": {Base: "vec2"},
	"Window": {Base: "vec2"},
}

// SeedUniform returns the seed the uniform name Seed<Name> stands for:
// "SeedAccent" for accent, "SeedBgTop" for bgTop.
func SeedUniform(name string) (Seed, bool) {
	rest, ok := strings.CutPrefix(name, "Seed")
	if !ok || rest == "" {
		return 0, false
	}
	return SeedByName(strings.ToLower(rest[:1]) + rest[1:])
}

// apiType returns the type of a uniform GoRex sets, and whether it sets it.
func apiType(name string) (UniformType, bool) {
	if t, ok := apiUniforms[name]; ok {
		return t, true
	}
	if strings.HasPrefix(name, "Seed") {
		return UniformType{Base: "vec4"}, true // SeedUniform tells whether the seed is known
	}
	return UniformType{}, false
}

// ShaderSource is what the checks read of a shader's source before it
// compiles: its entry points, its uniforms and their types, and whether
// it names its backdrop. MyGo counts its loops (Shader.LoopRuns).
type ShaderSource struct {
	Entries  []string
	Uniforms map[string]UniformType
	Backdrop bool
	// Params are the //gorex:param declarations of its uniforms, which
	// version 2 reads; badParams the declarations that do not parse.
	Params    map[string]ParamDecl
	badParams map[string]error
	// fragment is the offset of the name of the function Fragment.
	fragment int
}

// readsBackdrop finds a shader that names its backdrop, as MyGo's
// shader package does.
var readsBackdrop = regexp.MustCompile(`\bbackdrop(At|Sample)\b`)

// entryPoints are the entry points of MyGo's shaders: a fill's and the
// effects'.
var entryPoints = []string{"Fragment", "ColorEffect", "DistortionEffect", "LayerEffect"}

// ParseShaderSource reads src, a Kage file, as Go: its entry points, the
// exported variables at the package level, which are its uniforms, and
// the //gorex:param declarations in the comments above them.
func ParseShaderSource(src string) (*ShaderSource, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return nil, err
	}
	s := &ShaderSource{
		Uniforms: map[string]UniformType{}, Backdrop: readsBackdrop.MatchString(src), fragment: -1,
		Params: map[string]ParamDecl{}, badParams: map[string]error{},
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && slices.Contains(entryPoints, d.Name.Name) {
				s.Entries = append(s.Entries, d.Name.Name)
				if d.Name.Name == "Fragment" {
					s.fragment = int(d.Name.Pos()) - 1
				}
			}
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs := spec.(*ast.ValueSpec)
				t, ok := uniformType(vs.Type)
				doc := vs.Doc
				if doc == nil && !d.Lparen.IsValid() {
					doc = d.Doc
				}
				for _, n := range vs.Names {
					if !n.IsExported() {
						continue
					}
					if !ok {
						return nil, fmt.Errorf("uniform %s: a type GoRex does not set", n.Name)
					}
					s.Uniforms[n.Name] = t
					if decl, found, err := parseDecl(doc); err != nil {
						s.badParams[n.Name] = err
					} else if found {
						s.Params[n.Name] = decl
					}
				}
			}
		}
	}
	return s, nil
}

// ParamKind is the kind of a declared param (docs/DSK-012, Declared
// params).
type ParamKind int

// The kinds of a declared param.
const (
	ParamNumber ParamKind = iota + 1
	ParamToggle
	ParamPoint
	ParamColor
)

var paramKinds = []string{ParamNumber: "number", ParamToggle: "toggle", ParamPoint: "point", ParamColor: "color"}

func (k ParamKind) String() string {
	if k <= 0 || int(k) >= len(paramKinds) {
		return "value"
	}
	return paramKinds[k]
}

// ParamDecl is a //gorex:param declaration: the kind of the param, its
// default, a number's range, and the other lines of its comment, which
// describe it for a later Settings control.
type ParamDecl struct {
	Kind     ParamKind
	Default  Param
	Min, Max float32
	About    string
	// def is the default as the declaration writes it.
	def string
}

// The marker of a declaration, and the most characters of a description.
const (
	paramMarker = "//gorex:param"
	maxAbout    = 80
)

// parseDecl reads the declaration in doc, the comment above a uniform:
// found is false when it has none, err tells a declaration that does not
// parse.
func parseDecl(doc *ast.CommentGroup) (d ParamDecl, found bool, err error) {
	if doc == nil {
		return d, false, nil
	}
	var about []string
	for _, c := range doc.List {
		args, ok := strings.CutPrefix(c.Text, paramMarker)
		if !ok || args != "" && args[0] != ' ' && args[0] != '\t' {
			about = append(about, strings.TrimSpace(strings.TrimPrefix(c.Text, "//")))
			continue
		}
		if found {
			return d, true, errors.New(paramMarker + ": more than one in the comment")
		}
		found = true
		if d, err = parseDeclArgs(strings.Fields(args)); err != nil {
			return d, true, err
		}
	}
	if !found {
		return d, false, nil
	}
	d.About = strings.Join(strings.Fields(strings.Join(about, " ")), " ")
	if r := []rune(d.About); len(r) > maxAbout {
		d.About = string(r[:maxAbout])
	}
	return d, true, nil
}

// parseDeclArgs reads the words after //gorex:param: a kind and its
// arguments, by the table of docs/DSK-012, Declared params.
func parseDeclArgs(f []string) (ParamDecl, error) {
	bad := func(format string, a ...any) (ParamDecl, error) {
		return ParamDecl{}, fmt.Errorf(paramMarker+": "+format, a...)
	}
	if len(f) == 0 {
		return bad("no kind")
	}
	nums := func(args []string) ([]float32, bool) {
		out := make([]float32, len(args))
		for i, a := range args {
			v, err := strconv.ParseFloat(a, 32)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, false
			}
			out[i] = float32(v)
		}
		return out, true
	}
	d := ParamDecl{def: strings.Join(f[1:], " ")}
	if f[0] == "number" && len(f) > 1 {
		d.def = f[1]
	}
	switch f[0] {
	case "number":
		v, ok := nums(f[1:])
		if !ok || len(v) != 3 {
			return bad("number takes a default, a min and a max")
		}
		if !(v[1] < v[2] && v[1] <= v[0] && v[0] <= v[2]) {
			return bad("number %s: needs min < max and min <= default <= max", d.def)
		}
		d.Kind, d.Default, d.Min, d.Max = ParamNumber, Param{Nums: v[:1]}, v[1], v[2]
	case "toggle":
		if len(f) != 2 || f[1] != "on" && f[1] != "off" {
			return bad("toggle takes on or off")
		}
		d.Kind, d.Default = ParamToggle, Param{Nums: []float32{0}}
		if f[1] == "on" {
			d.Default.Nums[0] = 1
		}
	case "point":
		v, ok := nums(f[1:])
		if !ok || len(v) != 2 || v[0] < 0 || v[0] > 1 || v[1] < 0 || v[1] > 1 {
			return bad("point takes x and y, each from 0 to 1")
		}
		d.Kind, d.Default, d.Min, d.Max = ParamPoint, Param{Nums: v}, 0, 1
	case "color":
		if len(f) != 2 {
			return bad("color takes one color value")
		}
		p, ok := parseColorValue(f[1])
		if !ok {
			return bad("color %q: not a hex color, light/dark, seed:<name> or role:<name>", f[1])
		}
		d.Kind, d.Default = ParamColor, p
	default:
		return bad("%q is not a kind (number, toggle, point, color)", f[0])
	}
	return d, nil
}

// fitsDecl checks that a declaration of kind k fits a uniform of type t.
func fitsDecl(k ParamKind, t UniformType) error {
	switch {
	case t.Len != 0:
	case (k == ParamNumber || k == ParamToggle) && t.Base == "float":
		return nil
	case k == ParamPoint && t.Base == "vec2":
		return nil
	case k == ParamColor && (t.Base == "vec3" || t.Base == "vec4"):
		return nil
	}
	want := map[ParamKind]string{ParamNumber: "a float", ParamToggle: "a float", ParamPoint: "a vec2", ParamColor: "a vec3 or a vec4"}[k]
	return fmt.Errorf("a %s param needs %s, not a %s", k, want, t)
}

// ResolveParams returns the value of each declared param of src for slot,
// a slot of version 2: the manifest's value over the default. A value of
// another kind, or for no declaration, drops, and a number or a point out
// of its range clamps, each with a warning. color resolves a binding in
// an appearance (0 light, 1 dark), so that each color param holds a color
// of each appearance.
func ResolveParams(src *ShaderSource, slot *ShaderLayer, color func(b Binding, dark int) ui.Color) (map[string]Param, []string) {
	var warn []string
	out := map[string]Param{}
	names := slices.Sorted(maps.Keys(src.Params))
	for name := range slot.Params {
		if _, ok := src.Params[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		d, ok := src.Params[name]
		if !ok {
			warn = append(warn, fmt.Sprintf("params.%s: the shader declares no such param (dropped)", name))
			continue
		}
		v := d.Default
		if p, set := slot.Params[name]; set {
			switch {
			case p.kind() != d.Kind:
				warn = append(warn, fmt.Sprintf("params.%s: not a %s, the default %s applies", name, d.Kind, d.def))
			case d.Kind == ParamToggle:
				v = Param{Nums: []float32{0}}
				if *p.On {
					v.Nums[0] = 1
				}
			case d.Kind == ParamNumber:
				x := p.Nums[0]
				v = Param{Nums: []float32{min(max(x, d.Min), d.Max)}}
				if x < d.Min {
					warn = append(warn, fmt.Sprintf("params.%s: %g is below the min %g, so it is %g", name, x, d.Min, d.Min))
				} else if x > d.Max {
					warn = append(warn, fmt.Sprintf("params.%s: %g is above the max %g, so it is %g", name, x, d.Max, d.Max))
				}
			case d.Kind == ParamPoint:
				v = Param{Nums: []float32{min(max(p.Nums[0], 0), 1), min(max(p.Nums[1], 0), 1)}}
				if !slices.Equal(v.Nums, p.Nums) {
					warn = append(warn, fmt.Sprintf("params.%s: %g,%g is outside 0 to 1, so it is %g,%g", name, p.Nums[0], p.Nums[1], v.Nums[0], v.Nums[1]))
				}
			default:
				v = p
			}
		}
		if v.Bind != nil {
			v = Param{Color: &[2]ui.Color{color(*v.Bind, 0), color(*v.Bind, 1)}}
		}
		out[name] = v
	}
	return out, warn
}

// uniformType reads a uniform's type: an identifier, or an array of one
// with a constant length.
func uniformType(e ast.Expr) (UniformType, bool) {
	switch e := e.(type) {
	case *ast.Ident:
		return UniformType{Base: e.Name}, true
	case *ast.ArrayType:
		lit, ok := e.Len.(*ast.BasicLit)
		id, ok2 := e.Elt.(*ast.Ident)
		if !ok || !ok2 || lit.Kind != token.INT {
			return UniformType{}, false
		}
		n, err := strconv.Atoi(lit.Value)
		if err != nil || n <= 0 {
			return UniformType{}, false
		}
		return UniformType{Base: id.Name, Len: n}, true
	}
	return UniformType{}, false
}

// RenameFragment returns src with its function Fragment named name, so
// that a wrapper can call it.
func (s *ShaderSource) RenameFragment(src, name string) string {
	if s.fragment < 0 || !strings.HasPrefix(src[s.fragment:], "Fragment") {
		return src
	}
	return src[:s.fragment] + name + src[s.fragment+len("Fragment"):]
}

// lanes returns the numbers a basic type holds, and whether they are
// numbers: 1 for float and int, 2 to 4 for the vectors, 4, 9 and 16 for
// the matrices.
func lanes(base string) (int, bool) {
	switch base {
	case "float", "int":
		return 1, true
	case "vec2", "ivec2":
		return 2, true
	case "vec3", "ivec3":
		return 3, true
	case "vec4", "ivec4", "mat2":
		return 4, true
	case "mat3":
		return 9, true
	case "mat4":
		return 16, true
	}
	return 0, false
}

// fits reports whether p can set a uniform of type t: a color a vec3 or a
// vec4, numbers a uniform that holds as many.
func (p Param) fits(t UniformType) bool {
	if p.Color != nil {
		return t.Len == 0 && (t.Base == "vec3" || t.Base == "vec4")
	}
	n, ok := lanes(t.Base)
	if !ok {
		return false
	}
	if t.Len > 0 {
		n *= t.Len
	}
	return n == len(p.Nums)
}

// CheckUniforms checks the uniforms of src against the API table and the
// slot's params, by the API version api: each uniform GoRex sets has its
// type, each param fits its uniform. reserved names uniforms that GoRex
// adds to the source, which the shader may not declare. In version 1 a
// uniform neither sets is zero, and a param with no uniform does nothing;
// each gives a warning. It fails on a uniform of the wrong type. In
// version 2 each other uniform has a declaration that parses and fits its
// type, at most maxParams of them, and no API uniform has one
// (docs/DSK-012, Declared params); ResolveParams warns of the manifest's
// values.
func CheckUniforms(src *ShaderSource, slot *ShaderLayer, api int, reserved ...string) (warn []string, err error) {
	if api == ShaderAPI2 && len(src.Params)+len(src.badParams) > maxParams {
		return nil, fmt.Errorf("declares more than %d params", maxParams)
	}
	for _, name := range slices.Sorted(maps.Keys(src.Uniforms)) {
		t := src.Uniforms[name]
		if slices.Contains(reserved, name) {
			return warn, fmt.Errorf("uniform %s: a name GoRex keeps", name)
		}
		_, declared := src.Params[name]
		bad, badDecl := src.badParams[name]
		if want, ok := apiType(name); ok {
			if api == ShaderAPI2 && (declared || badDecl) {
				return warn, fmt.Errorf("uniform %s: GoRex sets this uniform, which takes no %s", name, paramMarker)
			}
			if _, seed := SeedUniform(name); strings.HasPrefix(name, "Seed") && !seed {
				return warn, fmt.Errorf("uniform %s: no such seed", name)
			}
			if t != want {
				return warn, fmt.Errorf("uniform %s: a %s, not a %s", name, t, want)
			}
			continue
		}
		if api == ShaderAPI2 {
			switch {
			case badDecl:
				return warn, fmt.Errorf("uniform %s: %v", name, bad)
			case !declared:
				return warn, fmt.Errorf("uniform %s: no %s declares it", name, paramMarker)
			}
			if err := fitsDecl(src.Params[name].Kind, t); err != nil {
				return warn, fmt.Errorf("uniform %s: %v", name, err)
			}
			continue
		}
		p, ok := slot.Params[name]
		if !ok {
			warn = append(warn, fmt.Sprintf("uniform %s: no param sets it, so it is zero", name))
			continue
		}
		if !p.fits(t) {
			return warn, fmt.Errorf("uniform %s: a %s, which the param does not fit", name, t)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(slot.Params)) {
		if _, ok := src.Uniforms[name]; !ok && api != ShaderAPI2 {
			warn = append(warn, fmt.Sprintf("params.%s: the shader has no such uniform", name))
		}
	}
	return warn, nil
}
