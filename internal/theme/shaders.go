package theme

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
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

// ShaderAPI is the version of the shader API this build reads.
const ShaderAPI = 1

// The caps of the shaders key.
const (
	maxFaceShaders  = 8
	maxShaderImages = 4
	maxParams       = 16
	maxParamList    = 16
)

// Shaders are the slots of a theme that its shaders fill: the window's
// background and the faces of roles. A nil slot draws as the theme's
// colors do.
type Shaders struct {
	Background *ShaderSlot
	Faces      map[Role]*ShaderSlot
}

// ShaderSlot is one slot: the .kage asset of its source, up to four image
// assets it reads as image0 to image3, and the values of its params.
type ShaderSlot struct {
	Source string
	Images []string
	Params map[string]Param
}

// Param is the value of a uniform the theme sets: numbers, or a color of
// each appearance, light then dark.
type Param struct {
	Nums  []float32
	Color *[2]ui.Color
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
	if json.Unmarshal(m["api"], &api) != nil || api != ShaderAPI {
		w.add("shaders.api: %s is not an API this build knows (shaders dropped)", strings.TrimSpace(string(m["api"])))
		return nil
	}
	s := &Shaders{Faces: map[Role]*ShaderSlot{}}
	if raw, ok := m["background"]; ok {
		s.Background = parseSlot(raw, "shaders.background", assets, w)
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
			if slot := parseSlot(faces[name], where, assets, w); slot != nil {
				s.Faces[r] = slot
			}
		}
	}
	if s.Background == nil && len(s.Faces) == 0 {
		return nil
	}
	return s
}

// parseSlot reads one slot, or returns nil with a warning.
func parseSlot(raw json.RawMessage, where string, assets map[string]Asset, w *warner) *ShaderSlot {
	f, ok := w.fields(raw, where, "source", "images", "params")
	if !ok {
		return nil
	}
	var slot ShaderSlot
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
			if a, ok := assets[n]; !ok || IsKage(a.File) {
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
// exported variables at the package level, which are its uniforms.
func ParseShaderSource(src string) (*ShaderSource, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	s := &ShaderSource{Uniforms: map[string]UniformType{}, Backdrop: readsBackdrop.MatchString(src), fragment: -1}
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
				for _, n := range vs.Names {
					if !n.IsExported() {
						continue
					}
					if !ok {
						return nil, fmt.Errorf("uniform %s: a type GoRex does not set", n.Name)
					}
					s.Uniforms[n.Name] = t
				}
			}
		}
	}
	return s, nil
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
// slot's params: each uniform GoRex sets has its type, each param fits its
// uniform. reserved names uniforms that GoRex adds to the source, which
// the shader may not declare. A uniform neither sets is zero, and a param
// with no uniform does nothing; each gives a warning. It fails on a
// uniform of the wrong type.
func CheckUniforms(src *ShaderSource, slot *ShaderSlot, reserved ...string) (warn []string, err error) {
	for _, name := range slices.Sorted(maps.Keys(src.Uniforms)) {
		t := src.Uniforms[name]
		if slices.Contains(reserved, name) {
			return warn, fmt.Errorf("uniform %s: a name GoRex keeps", name)
		}
		if want, ok := apiType(name); ok {
			if _, seed := SeedUniform(name); strings.HasPrefix(name, "Seed") && !seed {
				return warn, fmt.Errorf("uniform %s: no such seed", name)
			}
			if t != want {
				return warn, fmt.Errorf("uniform %s: a %s, not a %s", name, t, want)
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
		if _, ok := src.Uniforms[name]; !ok {
			warn = append(warn, fmt.Sprintf("params.%s: the shader has no such uniform", name))
		}
	}
	return warn, nil
}
