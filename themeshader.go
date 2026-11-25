package main

import (
	"errors"
	"fmt"
	"image"
	"log"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"

	"gorex/internal/theme"
	"gorex/locales"
)

// themeshader.go compiles, checks and draws the shaders of a theme pack
// (docs/DSK-012-theme-shaders.md, Shader API, Slots, Checks at load): the
// window's background and the faces of roles. A slot whose shader fails a
// check, or any slot on the CPU renderer, draws the theme's flat colors.

// themeSlot is a slot whose layers passed the checks at load, drawn from
// the first to the last (docs/DSK-012, Layers).
type themeSlot struct {
	layers []*themeLayer
	// flat tells a face of version 2, whose layers draw over its flat
	// color; under is that color in each appearance, which the checks lay
	// under the layers.
	flat  bool
	under [2]ui.Color
}

// readsTime reports whether a layer of the slot reads Time.
func (sl *themeSlot) readsTime() bool {
	return slices.ContainsFunc(sl.layers, func(l *themeLayer) bool { return l.time })
}

// readsPointer reports whether a layer of the slot reads Pointer.
func (sl *themeSlot) readsPointer() bool {
	return slices.ContainsFunc(sl.layers, func(l *themeLayer) bool { return l.pointer })
}

// The places of the pointer that the checks draw a layer reading Pointer
// at (docs/DSK-012, Checks of the param range, item 5), besides the frame's
// own, which the checks leave off the window: pressed at the center of the
// drawn rectangle, and over its top left corner.
const (
	pointerOwn = iota
	pointerCenter
	pointerCorner
	// pointerSidebar presses the pointer at the center of the sidebar's
	// column of the window, where check 6 reads the background.
	pointerSidebar
)

// themeLayer is a layer of a slot: a shader that passed checks 1 to 3.
type themeLayer struct {
	s      *shader.Shader
	images [4]*ui.Bitmap
	// values hold what does not change while the theme stands: Dark, the
	// seeds and the params, light then dark.
	values [2]*shader.Values
	// The uniforms of the API that change from draw to draw, which the
	// shader reads; alpha is GorexAlpha, of the background's wrapper or of
	// a layer's opacity.
	time, origin, window, alpha, pointer, state bool
	// pointerAt, set by the checks, puts Pointer at a place of the drawn
	// rectangle in place of the frame's (pointerCenter, pointerCorner).
	pointerAt int
	// opacity multiplies the layer's color: 1 in version 1.
	opacity float32
	// src and params are what the checks vary in version 2: the parsed
	// source, with its declarations, and the value of each param.
	src    *theme.ShaderSource
	params map[string]theme.Param
}

// themeShaders are the selected theme's slots that passed the checks, and
// the warnings of the checks, which the Extensions page shows.
type themeShaders struct {
	id         string
	spec       *theme.Shaders
	assets     map[string]theme.Asset
	background *themeSlot
	faces      map[theme.Role]*themeSlot
	warn       []string
	notes      []shaderNote
	// anyTime tells that a slot reads Time, so that frames move it;
	// anyPointer that a slot reads Pointer, so that the pointer's moves
	// paint the window.
	anyTime, anyPointer bool
}

// shaderSet holds the selected theme's checked slots, nil with none or
// until the checks are done; lastShaders the last set checked, which a
// reload of the same theme keeps rather than drawing flat while it checks
// again. shaderThemeID is the theme whose slots may draw: a set of another
// theme draws nothing (activeShaders).
var (
	shaderSet, lastShaders atomic.Pointer[themeShaders]
	shaderThemeID          atomic.Pointer[string]
)

// shaderMu orders the stores of the sets: a theme's change (setThemeShaders)
// and the end of a check (storeThemeShaders), so that a check of a theme
// the user left never stores its set after the change.
var shaderMu sync.Mutex

// activeShaders returns the set that draws: shaderSet when it is the
// selected theme's.
func activeShaders() *themeShaders {
	s := shaderSet.Load()
	if s == nil {
		return nil
	}
	if id := shaderThemeID.Load(); id == nil || *id != s.id {
		return nil
	}
	return s
}

// setThemeShaders makes m's slots the windows', when they were checked for
// the same theme and files, or none until the checks at load are done.
func setThemeShaders(m *theme.Manifest) {
	shaderMu.Lock()
	defer shaderMu.Unlock()
	id := prefs.Theme
	shaderThemeID.Store(&id)
	if m == nil || m.Shaders == nil {
		shaderSet.Store(nil)
		if prefs.Theme == "" || m != nil {
			lastShaders.Store(nil) // loadTheme's applyTheme(nil) on the way to a theme keeps it
		}
		return
	}
	if old := lastShaders.Load(); old != nil && old.id == prefs.Theme && reflect.DeepEqual(old.spec, m.Shaders) && maps.Equal(old.assets, m.Assets) {
		shaderSet.Store(old)
		return
	}
	shaderSet.Store(nil)
	lastShaders.Store(nil)
	shaderClock.reset()
}

// shaderNote is a warning of the checks as the Extensions page shows it:
// the slot, the English detail, and whether the slot was dropped.
type shaderNote struct {
	slot, detail string
	dropped      bool
}

// themeShaderWarnings returns the warnings of the selected theme's shader
// checks, for the Extensions page: a localized line that names the slot,
// and the check's detail after it.
func themeShaderWarnings() []string {
	s := activeShaders()
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.notes))
	for _, n := range s.notes {
		head := locales.SettingsThemeShaderNote(n.slot)
		if n.dropped {
			head = locales.SettingsThemeShaderDropped(n.slot)
		}
		out = append(out, head+" — "+n.detail)
	}
	return out
}

// storeThemeShaders checks the shaders of m, the theme name, with its
// decoded assets, and makes the slots that pass the windows', unless the
// user left the theme meanwhile (gen). It runs on loadArt's goroutine.
func storeThemeShaders(name string, m *theme.Manifest, art *theme.Art, gen int64) {
	if m.Shaders == nil {
		return
	}
	set := checkShaders(m, art)
	set.id = name
	for _, w := range set.warn {
		log.Printf("gorex settings: appearance.theme %q: %s", name, w)
	}
	shaderMu.Lock()
	defer shaderMu.Unlock()
	if id := shaderThemeID.Load(); lookGen.Load() != gen || id == nil || *id != name {
		return
	}
	shaderSet.Store(set)
	lastShaders.Store(set)
}

// themeShaderGPU reports whether p draws theme shaders: on a GPU only, as
// the CPU renderer runs a shader pixel by pixel. A test sets it.
var themeShaderGPU = (*ui.Painter).Accelerated

// The limits of the checks at load.
const (
	maxShaderCost     = 8   // times the built-in background, at 64×64 px
	faceTextContrast  = 4.5 // text on a face, at each pixel
	faceMutedContrast = 3.0 // textMuted on a face
	themeShaderFPS    = 30  // the frames a second a shader that reads Time asks for
)

// checkShaders compiles and checks each slot of m (docs/DSK-012, Checks
// at load): a slot that fails drops with a warning, and draws its flat
// color.
func checkShaders(m *theme.Manifest, art *theme.Art) *themeShaders {
	set := &themeShaders{spec: m.Shaders, assets: m.Assets, faces: map[theme.Role]*themeSlot{}}
	pal := [2]colors{themed(lightSeeds, m, 0), themed(darkSeeds, m, 1)}
	env := &slotEnv{
		api: m.Shaders.API,
		sd:  [2]seeds{themeSeeds(lightSeeds, m, 0), themeSeeds(darkSeeds, m, 1)},
		pal: &pal,
		art: art,
	}
	check := func(where string, slot *theme.ShaderSlot, bg bool, under *[2]ui.Color, contrast func(*themeSlot, *checkSamples) error) *themeSlot {
		sl, warn, err := checkSlot(slot, env, bg, under, contrast)
		for _, w := range warn {
			at, w := layerNote(where, w)
			set.warn = append(set.warn, fmt.Sprintf("%s: %s", at, w))
			set.notes = append(set.notes, shaderNote{at, w, false})
		}
		if err != nil {
			at, e := layerNote(where, err.Error())
			set.warn = append(set.warn, fmt.Sprintf("%s: %s (dropped: the flat colors draw)", at, e))
			set.notes = append(set.notes, shaderNote{at, e, true})
			return nil
		}
		set.anyTime = set.anyTime || sl.readsTime()
		set.anyPointer = set.anyPointer || sl.readsPointer()
		return sl
	}
	if bg := m.Shaders.Background; bg != nil {
		set.background = check("shaders.background", bg, true, nil, func(sl *themeSlot, s *checkSamples) error { return bandContrast(sl, &pal, s.band) })
	}
	for _, role := range theme.FaceRoles {
		if f := m.Shaders.Faces[role]; f != nil {
			var under *[2]ui.Color
			if env.api == theme.ShaderAPI2 {
				under = &[2]ui.Color{*roleField(&pal[0], role), *roleField(&pal[1], role)}
			}
			if sl := check("shaders.faces."+role.String(), f, false, under, func(sl *themeSlot, s *checkSamples) error { return faceContrast(sl, &pal, role, s.face) }); sl != nil {
				set.faces[role] = sl
			}
		}
	}
	return set
}

// layerNote splits a warning of checkSlot that names a layer ("layer 2:
// ...") into the layer's place, "shaders.background layer 2", and the
// rest, so that check warnings name a layer as parse warnings do
// (theme.LayerWhere).
func layerNote(where, w string) (string, string) {
	var i int
	if n, _ := fmt.Sscanf(w, "layer %d:", &i); n == 1 {
		if _, rest, ok := strings.Cut(w, ": "); ok {
			return theme.LayerWhere(where, i-1), rest
		}
	}
	return where, w
}

// slotEnv is what the checks of a theme's slots read: the version of its
// shader API, its seeds and palettes of each appearance (themeSeeds,
// themed), and its decoded assets.
type slotEnv struct {
	api int
	sd  [2]seeds
	pal *[2]colors
	art *theme.Art
}

// color resolves a binding of version 2 in the appearance i: the theme's
// seed, or its flat color of a role. The user's sidebar.colorCustomizations
// do not feed it (docs/DSK-012, Declared params).
func (e *slotEnv) color(b theme.Binding, i int) ui.Color {
	if b.IsRole {
		return *roleField(&e.pal[i], b.Role)
	}
	return *seedField(&e.sd[i], b.Seed)
}

// checkSlot runs the checks of one slot: compile, loops and uniforms on
// each layer, which drop the whole slot when one fails; then cost and
// contrast on each variant of its params (rangeVariants), one layer at a
// time with the others at the manifest's values. The first variant draws
// allSamples, the others fewSamples. under, for a face of version 2, is
// its flat color, which its layers draw over. A panic in any check fails
// the slot. With more than one layer, a warning names the layer.
func checkSlot(slot *theme.ShaderSlot, e *slotEnv, bg bool, under *[2]ui.Color, contrast func(*themeSlot, *checkSamples) error) (sl *themeSlot, warn []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			sl, err = nil, fmt.Errorf("a check failed: %v", r)
		}
	}()
	sl = &themeSlot{}
	if under != nil {
		sl.flat, sl.under = true, *under
	}
	label := func(i int, s string) string {
		if len(slot.Layers) > 1 {
			return fmt.Sprintf("layer %d: %s", i+1, s)
		}
		return s
	}
	for i := range slot.Layers {
		l, lw, err := compileLayer(&slot.Layers[i], e, bg)
		for _, w := range lw {
			warn = append(warn, label(i, w))
		}
		if err != nil {
			return nil, warn, errors.New(label(i, err.Error()))
		}
		sl.layers = append(sl.layers, l)
	}
	// Variant 1: the manifest's values, with all samples.
	costs := make([]float64, len(sl.layers))
	var total float64
	var slow shaderFrame
	for i, l := range sl.layers {
		costs[i], slow = layerCost(l, allSamples.cost, false, maxShaderCost)
		total += costs[i]
	}
	if err := costLimit(total, len(sl.layers), slow); err != nil {
		return nil, warn, err
	}
	if err := contrast(sl, &allSamples); err != nil {
		return nil, warn, err
	}
	for i, l := range sl.layers {
		others := total - costs[i]
		for _, v := range rangeVariants(l, e, bg) {
			c, slow := layerCost(v.l, fewSamples.cost, fewSamples.costOnce, maxShaderCost-others)
			err := costLimit(others+c, len(sl.layers), slow)
			if err == nil {
				vs := *sl
				vs.layers = slices.Clone(sl.layers)
				vs.layers[i] = v.l
				err = contrast(&vs, &fewSamples)
			}
			if err != nil {
				return nil, warn, fmt.Errorf("%s: %w", label(i, v.label), err)
			}
		}
	}
	return sl, warn, nil
}

// rangeVariant is a set of param values the checks draw a layer with, and
// the label that names it in a warning.
type rangeVariant struct {
	label string
	l     *themeLayer
}

// rangeVariants are the variants of a layer of version 2 past the first,
// the manifest's values (docs/DSK-012, Checks of the param range): each
// number at its min, then its max; each toggle at its other value; each
// point at 0,0, then 1,1; the other params as the manifest sets them. A
// variant equal to the first is not drawn again. Colors are not varied.
// A layer that reads Pointer adds item 5: the first variant draws the
// pointer off the window (z = 0); then pressed at the center of the drawn
// rectangle, and over its top left corner; for the background, pressed at
// the center of the sidebar's column too, where check 6 reads.
func rangeVariants(l *themeLayer, e *slotEnv, bg bool) []rangeVariant {
	if e.api != theme.ShaderAPI2 {
		return nil
	}
	var out []rangeVariant
	add := func(name, label string, nums ...float32) {
		if slices.Equal(l.params[name].Nums, nums) {
			return
		}
		params := maps.Clone(l.params)
		params[name] = theme.Param{Nums: nums}
		v := *l
		v.params, v.values = params, slotValues(l.s, l.src, params, &e.sd)
		out = append(out, rangeVariant{label, &v})
	}
	for _, name := range slices.Sorted(maps.Keys(l.src.Params)) {
		switch d := l.src.Params[name]; d.Kind {
		case theme.ParamNumber:
			add(name, fmt.Sprintf("%s at %g (min)", name, d.Min), d.Min)
			add(name, fmt.Sprintf("%s at %g (max)", name, d.Max), d.Max)
		case theme.ParamToggle:
			if on := l.params[name].Nums[0] != 0; on {
				add(name, name+" off", 0)
			} else {
				add(name, name+" on", 1)
			}
		case theme.ParamPoint:
			add(name, name+" at 0,0", 0, 0)
			add(name, name+" at 1,1", 1, 1)
		}
	}
	if l.pointer {
		// Item 5: variant 1 draws the pointer off the window (z = 0).
		for _, at := range []struct {
			at    int
			label string
		}{{pointerCenter, "Pointer pressed at the center"}, {pointerCorner, "Pointer at the top left corner"}, {pointerSidebar, "Pointer pressed in the sidebar"}} {
			if at.at == pointerSidebar && !bg {
				continue
			}
			v := *l
			v.pointerAt = at.at
			out = append(out, rangeVariant{at.label, &v})
		}
	}
	return out
}

// gorexAlpha is the uniform of the wrapper: the alpha of the background's
// wash under the glass setting (backgroundAlpha), times a layer's opacity.
const gorexAlpha = "GorexAlpha"

// backgroundWrapper calls the theme's Fragment, renamed, and fades its
// color as the built-in background fades.
const backgroundWrapper = `

var GorexAlpha float

func Fragment(dst vec4, pos vec2) vec4 {
	return themeFragment(dst, pos) * GorexAlpha
}
`

// compileLayer runs checks 1 to 3 on a layer: it parses and compiles its
// source, refuses its loops and checks its uniforms; it then sets what the
// layer draws with. A background, or a layer whose opacity is under 1,
// wraps the source with backgroundWrapper. In version 2 the params are the
// shader's declared defaults with the manifest's values over them
// (theme.ResolveParams), whose warnings it returns.
func compileLayer(slot *theme.ShaderLayer, e *slotEnv, bg bool) (*themeLayer, []string, error) {
	var src string
	art := e.art
	if art != nil {
		src = art.Sources[slot.Source]
	}
	if src == "" {
		return nil, nil, fmt.Errorf("no source: the asset %q did not load", slot.Source)
	}
	info, err := theme.ParseShaderSource(src)
	if err != nil {
		return nil, nil, fmt.Errorf("does not parse: %v", err)
	}
	if len(info.Entries) != 1 || info.Entries[0] != "Fragment" {
		return nil, nil, errors.New("the entry point must be Fragment alone, not an effect's")
	}
	if info.Backdrop {
		return nil, nil, errors.New("reads its backdrop (backdropAt, backdropSample)")
	}
	wrap := bg || slot.Opacity != 1
	if wrap {
		src = info.RenameFragment(src, "themeFragment") + backgroundWrapper
	}
	s, err := compileTheme(src)
	if err != nil {
		return nil, nil, fmt.Errorf("does not compile: %v", err)
	}
	if err := themeShaderLoops(s); err != nil {
		return nil, nil, err
	}
	warn, err := theme.CheckUniforms(info, slot, e.api, gorexAlpha)
	if err != nil {
		return nil, warn, err
	}
	params := slot.Params
	if e.api == theme.ShaderAPI2 {
		var pw []string
		params, pw = theme.ResolveParams(info, slot, e.color)
		warn = append(warn, pw...)
	}
	sl := &themeLayer{s: s, alpha: wrap, opacity: slot.Opacity, src: info, params: params}
	_, sl.time = info.Uniforms["Time"]
	_, sl.origin = info.Uniforms["Origin"]
	_, sl.window = info.Uniforms["Window"]
	if e.api == theme.ShaderAPI2 {
		// In version 1 a uniform named Pointer is an ordinary param.
		_, sl.pointer = info.Uniforms["Pointer"]
		_, sl.state = info.Uniforms["State"]
	}
	for i, name := range slot.Images {
		if sl.images[i] = art.Bitmaps[name]; sl.images[i] == nil {
			warn = append(warn, fmt.Sprintf("image%d: the asset %q did not load, so it reads as transparent", i, name))
		}
	}
	sl.values = slotValues(s, info, params, &e.sd)
	return sl, warn, nil
}

// slotValues returns the values a slot draws with that do not change
// while the theme stands, light then dark: Dark, the seeds and params.
func slotValues(s *shader.Shader, info *theme.ShaderSource, params map[string]theme.Param, sd *[2]seeds) [2]*shader.Values {
	var out [2]*shader.Values
	for i := range out {
		v := s.NewValues()
		if _, ok := info.Uniforms["Dark"]; ok {
			v.Float(s.Uniform("Dark"), float32(i))
		}
		for name, t := range info.Uniforms {
			if id, ok := theme.SeedUniform(name); ok {
				v.Color(s.Uniform(name), *seedField(&sd[i], id))
				continue
			}
			p, ok := params[name]
			switch {
			case !ok:
			case p.Color != nil:
				v.Color(s.Uniform(name), p.Color[i])
			case strings.HasPrefix(t.Base, "i"):
				ints := make([]int32, len(p.Nums))
				for j, x := range p.Nums {
					ints[j] = int32(x)
				}
				v.Ints(s.Uniform(name), ints...)
			default:
				v.Floats(s.Uniform(name), p.Nums...)
			}
		}
		out[i] = v
	}
	return out
}

// compileTheme compiles a theme's shader, and turns a panic of the
// compiler into an error.
func compileTheme(src string) (s *shader.Shader, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, fmt.Errorf("the compiler failed: %v", r)
		}
	}()
	return shader.Compile(src)
}

// maxLoopRuns is how many times the loops of a theme's shader may run in
// all for a pixel.
const maxLoopRuns = 256

// themeShaderLoops is check 2: every loop of a theme's shader has
// constant bounds, and its loops run at most maxLoopRuns times in all for
// a pixel (MyGo's Shader.LoopRuns), so that the cost check never meets a
// loop without end.
func themeShaderLoops(s *shader.Shader) error {
	runs, bounded := s.LoopRuns()
	switch {
	case !bounded:
		return errors.New("has a loop whose runs are not constant (a bound that is not a constant, a counter its body writes, or recursion)")
	case runs > maxLoopRuns:
		return fmt.Errorf("its loops run %d times a pixel, more than %d", runs, maxLoopRuns)
	}
	return nil
}

// shaderFrame is what a window's frame draws the slots with: the time of
// the theme's shaders and the window's size, in DIPs.
type shaderFrame struct {
	time   float32
	window ui.Point
	// pointer is the Pointer uniform: x and y in the window, z 1 while
	// the pointer is over it, w 1 while its primary button is down; watch
	// tells that the window follows its moves (tickShaders).
	pointer [4]float32
	state   [4]float32
	watch   bool
	// moving tells that Time passes in the window's frames (tickShaders).
	moving bool
	// origin, set by the checks, is the Origin the slot reads in place of
	// the drawn rectangle's corner.
	origin *ui.Point
}

// frameNow holds the frame paintBackground set last, which the faces of
// the frame read; the faces paint after the background.
var frameNow atomic.Pointer[shaderFrame]

func currentFrame() shaderFrame {
	if f := frameNow.Load(); f != nil {
		return *f
	}
	return shaderFrame{window: ui.Point{X: 1280, Y: 800}}
}

// draw fills r with the slot's layers in order, rounded by radius, in the
// appearance, faded to alpha (the background's wash), at frame f; each
// layer fades by its opacity too.
func (sl *themeSlot) draw(p *ui.Painter, r ui.Rect, radius float32, dark bool, alpha float32, f shaderFrame) {
	for _, l := range sl.layers {
		l.draw(p, r, radius, dark, alpha*l.opacity, f)
	}
}

// draw fills r with the layer's shader, rounded by radius, in the
// appearance, faded to alpha, at frame f.
func (sl *themeLayer) draw(p *ui.Painter, r ui.Rect, radius float32, dark bool, alpha float32, f shaderFrame) {
	i := 0
	if dark {
		i = 1
	}
	o := shader.Options{Values: sl.values[i], Images: sl.images, Radius: radius}
	if sl.time || sl.origin || sl.window || sl.alpha || sl.pointer || sl.state {
		o.Uniforms = make(map[string]any, 6)
		if sl.time {
			o.Uniforms["Time"] = f.time
		}
		origin := ui.Point{X: r.X, Y: r.Y}
		if f.origin != nil {
			origin = *f.origin
		}
		if sl.origin {
			o.Uniforms["Origin"] = [2]float32{origin.X, origin.Y}
		}
		if sl.pointer {
			ptr := f.pointer
			switch sl.pointerAt {
			case pointerCenter:
				ptr = [4]float32{origin.X + r.W/2, origin.Y + r.H/2, 1, 1}
			case pointerCorner:
				ptr = [4]float32{origin.X, origin.Y, 1, 0}
			case pointerSidebar:
				ptr = [4]float32{sidebarWidthIn(f.window.X) / 2, f.window.Y / 2, 1, 1}
			}
			o.Uniforms["Pointer"] = ptr
		}
		if sl.window {
			o.Uniforms["Window"] = [2]float32{f.window.X, f.window.Y}
		}
		if sl.alpha {
			o.Uniforms[gorexAlpha] = alpha
		}
		if sl.state {
			o.Uniforms["State"] = f.state
		}
	}
	sl.s.Draw(p, r, o)
}

// themeBackground returns the background slot of the selected theme, nil
// with none or with Ambience Off, as its other layers.
func themeBackground() *themeSlot {
	if s := activeShaders(); s != nil && ambienceShown() {
		return s.background
	}
	return nil
}

// tickShaders advances the theme shaders' clock for a frame of a window r
// big, in which they move when moves (themeShaderMoves) and p draws them,
// and asks for the next frame while a slot reads Time. While a slot reads
// Pointer it reads the pointer, and has its moves paint the window at most
// themeShaderFPS a second (Painter.WatchPointer). Still, Time and the
// pointer's place stand, z and w of Pointer are 0, and it asks for no
// frame (docs/DSK-012, Pointer).
func tickShaders(p *ui.Painter, r ui.Rect, moves bool, sw *shaderWindow) shaderFrame {
	set := activeShaders()
	if set == nil {
		return currentFrame()
	}
	if sw == nil {
		sw = &shaderWindow{}
	}
	moving := moves && set.anyTime && themeShaderGPU(p)
	watch := moves && set.anyPointer && themeShaderGPU(p)
	ptr := sw.pointer
	ptr[2], ptr[3] = 0, 0
	if watch {
		x, y, over, primary := p.Pointer()
		ptr = [4]float32{x, y, boolFloat(over), boolFloat(primary)}
		p.WatchPointer(themeShaderFPS)
	}
	sw.pointer = ptr
	f := &shaderFrame{time: shaderClock.at(p.Now(), moving), window: ui.Point{X: r.W, Y: r.H}, pointer: ptr, watch: watch, moving: moving}
	frameNow.Store(f)
	if moving {
		p.AnimationFrameRate(themeShaderFPS)
	}
	return *f
}

// shaderWindow is what the theme's shaders keep of one window from frame
// to frame: the Pointer it drew last, whose place a still window keeps.
// Each window has its own (windowShader), so that the pointer of one
// never shows in another.
type shaderWindow struct {
	pointer [4]float32
}

type shaderWindowKey struct{}

// windowShader returns the shaderWindow of the window whose root element
// is e.
func windowShader(e ui.Element) *shaderWindow {
	return ui.Local(e, shaderWindowKey{}, func() shaderWindow { return shaderWindow{} })
}

// boolFloat is 1 for true, 0 for false, as a uniform holds a flag.
func boolFloat(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// themeShaderMoves reports whether the theme's shaders move in the window
// being built, by the still rule of docs/DSK-010-theme-ambience.md (Motion
// and the budget): not with Ambience Still or Off, with Animation Off or
// less motion, in a window that is not active or is hidden, nor on
// battery. The painter adds the CPU renderer (tickShaders). It asks
// nothing of the system when no slot of the theme reads Time or Pointer.
func themeShaderMoves(c *ui.Context) bool {
	s := activeShaders()
	return s != nil && (s.anyTime || s.anyPointer) && prefs.Ambience == "" && motion(c) != nil && c.WindowActive() && !c.WindowHidden() && !shaderOnBattery()
}

// shaderOnBattery reports whether the computer runs on its battery, asked
// of the system at most every 10 s; a test sets it.
var shaderOnBattery = func() func() bool {
	var mu sync.Mutex
	var at time.Time
	var on bool
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		if now := time.Now(); now.Sub(at) > 10*time.Second {
			at, on = now, mygo.Power.IsOnBattery()
		}
		return on
	}
}()

// clock is the time of the theme's shaders: seconds since the theme
// applied, which pass only while a window moves them, modulo 3600.
type clock struct {
	mu   sync.Mutex
	t    float64
	last time.Time
}

var shaderClock clock

// reset starts the clock at 0, as a theme applies.
func (k *clock) reset() {
	k.mu.Lock()
	k.t, k.last = 0, time.Time{}
	k.mu.Unlock()
}

// at returns the time at now: moving, it passes by the time since the last
// moving frame, at most a tenth of a second, so that it goes on from where
// it stopped; still, it stands.
func (k *clock) at(now time.Time, moving bool) float32 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if moving {
		if !k.last.IsZero() {
			k.t += min(max(now.Sub(k.last).Seconds(), 0), 0.1)
		}
		k.last = now
	} else {
		k.last = time.Time{}
	}
	return float32(math.Mod(k.t, 3600))
}

// paintFace gives e the face of role, face its flat color: the theme's
// face shader where one passed the checks and face is the theme's flat
// color of the role, as a material that paints the shader on a GPU and
// the flat color elsewhere; else face as the background. A color the user
// or a space set over the role draws flat.
func paintFace(e ui.Element, k *colors, role faceRole, face ui.Color) ui.Element {
	if m, ok := faceFill(role, face); ok {
		return e.Material(m)
	}
	return e.Background(face)
}

// faceMaterialOf is the material of the face of role where the system
// draws no glass: the theme's face shader, or face's color.
func faceMaterialOf(role faceRole, face ui.Color) ui.Material {
	if m, ok := faceFill(role, face); ok {
		return m
	}
	return faceMaterial{face}
}

// faceFill returns the face shader of role for the flat color face.
func faceFill(role faceRole, face ui.Color) (faceShader, bool) {
	set := activeShaders()
	if set == nil || face.A == 0 {
		return faceShader{}, false
	}
	sl := set.faces[role]
	if sl == nil || face != *roleField(paletteOf(false), role) && face != *roleField(paletteOf(true), role) {
		return faceShader{}, false
	}
	return faceShader{sl: sl, face: face}, true
}

// faceShader is a face that a theme's shader paints, on a GPU; its flat
// color on the CPU renderer.
type faceShader struct {
	sl      *themeSlot
	face    ui.Color
	el      ui.Element
	primary bool
	hasEl   bool
}

// statefulFaceMaterial returns a face shader material bound to el's live
// hovered/pressed/focused/disabled state and primary flag.
func statefulFaceMaterial(role faceRole, face ui.Color, el ui.Element, primary bool) (ui.Material, bool) {
	f, ok := faceFill(role, face)
	if !ok {
		return nil, false
	}
	f.el = el
	f.primary = primary
	f.hasEl = true
	return f, true
}

func (f faceShader) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	if !themeShaderGPU(p) || f.sl.flat {
		p.FillRadii(box, radii, f.face)
	}
	if !themeShaderGPU(p) {
		return
	}
	frame := currentFrame()
	if f.hasEl {
		var hov, prs, foc, dis float32
		if f.el.Hovered() {
			hov = 1
		}
		if f.el.Pressed() {
			prs = 1
		}
		if f.primary || f.el.Focused() {
			foc = 1
		}
		if f.el.IsDisabled() {
			dis = 1
		}
		frame.state = [4]float32{hov, prs, foc, dis}
	}
	f.sl.draw(p, box, radii[0], p.Theme().Dark, 1, frame)
	// The background's Draw asks for its own part; a face, a material
	// MyGo paints as a part of its own, asks again, so that the frames
	// that only paint move it too.
	if f.sl.readsTime() && frame.moving {
		p.AnimationFrameRate(themeShaderFPS)
	}
	if f.sl.readsPointer() && frame.watch {
		p.WatchPointer(themeShaderFPS)
	}
}

// renderSlot draws the slot alone in the CPU's interpreter, as the checks
// read it: filling a rectangle of size, scaled by scale, of which the
// render shows the top left w×h px, at frame f. It scales with a
// transform, not the render's scale, which would run the shader for each
// pixel at scale 1.
func renderSlot(sl *themeSlot, w, h int, scale float32, size ui.Point, dark bool, f shaderFrame) *image.RGBA {
	return ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			p.Transform(ui.Scaling(scale, scale), 0, 0, func() {
				if sl.flat {
					c := sl.under[0]
					if dark {
						c = sl.under[1]
					}
					p.FillRadii(ui.Rect{W: size.X, H: size.Y}, [4]float32{}, c)
				}
				sl.draw(p, ui.Rect{W: size.X, H: size.Y}, 0, dark, 1, f)
			})
		})
	}, w, h, 1)
}

// The uniforms the checks draw a slot with: a shader may branch on Time,
// Origin and Window, so each check draws several values of each, not one.
// Sampling does not prove every input (docs/DSK-012, Checks at load).
var (
	sampleTimes   = []float32{0, 1.7, 37, 600, 3599}
	sampleWindows = []ui.Point{{X: 64, Y: 64}, {X: 1280, Y: 800}, {X: 3840, Y: 2160}}
	sampleOrigins = []ui.Point{{}, {X: 900, Y: 500}}
)

// costFrames are the frames the cost check draws: each time with each
// window, the origins in turn.
var costFrames = func() []shaderFrame {
	var out []shaderFrame
	for _, t := range sampleTimes {
		for _, w := range sampleWindows {
			o := sampleOrigins[len(out)%len(sampleOrigins)]
			out = append(out, shaderFrame{time: t, window: w, origin: &o})
		}
	}
	return out
}()

// facePlaces are the origins and window sizes a face has, which the face
// check draws in turn.
var facePlaces = []struct{ origin, window ui.Point }{
	{ui.Point{}, ui.Point{X: 1280, Y: 800}},
	{ui.Point{X: 900, Y: 500}, ui.Point{X: 1280, Y: 800}},
	{ui.Point{X: 200, Y: 40}, ui.Point{X: 3840, Y: 2160}},
}

// faceFrames are the frames the face check draws: Time 0 to 3 s and large
// ones, at facePlaces in turn.
var faceFrames = func() []shaderFrame {
	var out []shaderFrame
	for i, t := range []float32{0, 1, 2, 3, 37, 600, 3599} {
		p := facePlaces[i%len(facePlaces)]
		out = append(out, shaderFrame{time: t, window: p.window, origin: &p.origin})
	}
	return out
}()

// bandTimes are the times the sidebar band check draws.
var bandTimes = []float32{0, 37, 3599}

// checkSamples are the frames checks 4 to 6 draw a variant of a slot with.
type checkSamples struct {
	cost, face []shaderFrame
	band       []float32
	// costOnce times the cost frame once, with no fastest of three.
	costOnce bool
}

// allSamples are the frames of version 1, which the first variant of a
// slot, the manifest's values, draws. fewSamples are those of the other
// variants of version 2, as the checks of every variant with every sample
// take seconds (docs/DSK-012, Checks of the param range): the cost at Time
// 0, Window 1280×800 and Origin 0,0, timed once; the face at Time 0 and
// Window 1280×800 at each of facePlaces' origins; the band at Time 0.
var (
	allSamples = checkSamples{cost: costFrames, face: faceFrames, band: bandTimes}
	fewSamples = func() checkSamples {
		win := ui.Point{X: 1280, Y: 800}
		s := checkSamples{cost: []shaderFrame{{window: win, origin: &ui.Point{}}}, band: []float32{0}, costOnce: true}
		for _, p := range facePlaces {
			s.face = append(s.face, shaderFrame{window: win, origin: &p.origin})
		}
		return s
	}()
)

// fastest returns the shortest of three runs of fn.
func fastest(fn func()) time.Duration {
	best := time.Duration(math.MaxInt64)
	for range 3 {
		t := time.Now()
		fn()
		best = min(best, time.Since(t))
	}
	return max(best, 1)
}

// costReference is the built-in background as it was when check 4 was
// set: with the loop of four glows of docs/DSK-010-theme-ambience.md,
// which the background has no more. It fixes the cost a theme shader
// compares with, so that removing the glows did not make the check
// stricter. Only builtinCost draws it.
var costReference = shader.MustCompile(`package main

var Top, Mid, Low, Bottom, Tint vec4
var Alpha float
var Glow, GlowColor [4]vec4

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
	for i := 0; i < 4; i++ {
		g := GlowColor[i].a * clamp(1-length((pos/size-Glow[i].xy)/Glow[i].zw), 0, 1)
		c = c*(1-g) + GlowColor[i].rgb*g
	}
	n := hash(uint(dst.x) + hash(uint(dst.y)))
	c += (float(n)/4294967295.0 - 0.5) / 255.0
	return vec4(clamp(c, 0, 1)*Alpha, Alpha)
}
`)

// builtinCost is the time of the built-in background at 64×64 px, which
// a theme shader's cost compares with, measured once (costReference).
var builtinCost = sync.OnceValue(func() time.Duration {
	k := &lightColors
	o := shader.Options{Uniforms: map[string]any{
		"Top": k.bgTop, "Mid": k.bgMid, "Low": k.bgLow, "Bottom": k.bgBottom, "Tint": k.bgTint, "Alpha": 1,
		"Glow": [16]float32{2: 1, 3: 1, 6: 1, 7: 1, 10: 1, 11: 1, 14: 1, 15: 1},
	}}
	return fastest(func() {
		ui.Render(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { costReference.Draw(p, r, o) })
		}, 64, 64, 1)
	})
})

// layerCost is check 4 for a layer: its time drawn at 64×64 px in the
// interpreter, as a ratio to the built-in background, with each of frames
// (costFrames, or fewer), and the frame that took longest. It draws each
// once, then the slowest the fastest of three, unless once, which keeps
// the time of the one draw when its ratio is under limit, so that only a
// layer over it is timed again. A test sets it to fixed ratios.
var layerCost = timeLayer

// timeLayer is layerCost as it runs: it times the layer in the interpreter.
func timeLayer(l *themeLayer, frames []shaderFrame, once bool, limit float64) (float64, shaderFrame) {
	sl := &themeSlot{layers: []*themeLayer{l}}
	size := ui.Point{X: 64, Y: 64}
	slow, worst := frames[0], time.Duration(0)
	for _, f := range frames {
		t := time.Now()
		renderSlot(sl, 64, 64, 1, size, false, f)
		if d := time.Since(t); d > worst {
			slow, worst = f, d
		}
	}
	ratio := func(t time.Duration) float64 { return float64(t) / float64(builtinCost()) }
	r := ratio(max(worst, 1))
	if !once || r > limit {
		r = ratio(fastest(func() { renderSlot(sl, 64, 64, 1, size, false, slow) }))
	}
	return r, slow
}

// costLimit fails a slot whose layers cost more than maxShaderCost times
// the built-in background in all: cost, the sum of their ratios. A slot of
// one layer names its slowest frame, slow.
func costLimit(cost float64, layers int, slow shaderFrame) error {
	switch {
	case cost <= maxShaderCost:
		return nil
	case layers == 1:
		return fmt.Errorf("costs %.1f times the built-in background at Time %g, Window %gx%g, more than %d", cost, slow.time, slow.window.X, slow.window.Y, maxShaderCost)
	}
	return fmt.Errorf("its %d layers cost %.1f times the built-in background in all, more than %d", layers, cost, maxShaderCost)
}

// overOpaque returns the premultiplied pixel px laid over the opaque
// color bg.
func overOpaque(px []uint8, bg ui.Color) ui.Color {
	a := 1 - float32(px[3])/255
	ch := func(v, b uint8) uint8 { return uint8(min(float32(v)+float32(b)*a+0.5, 255)) }
	return ui.RGB(ch(px[0], bg.R), ch(px[1], bg.G), ch(px[2], bg.B))
}

// readable checks that text and muted keep their contrast on each pixel
// of img in area, laid over bg.
func readable(img *image.RGBA, area image.Rectangle, bg, text, muted ui.Color) error {
	b := img.Bounds().Intersect(area)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			c := overOpaque(img.Pix[o:o+4], bg)
			if r := contrast(text, c); r < faceTextContrast {
				return fmt.Errorf("text has %.1f:1 at %d,%d, below %g:1", r, x, y, faceTextContrast)
			}
			if r := contrast(muted, c); r < faceMutedContrast {
				return fmt.Errorf("textMuted has %.1f:1 at %d,%d, below %g:1", r, x, y, faceMutedContrast)
			}
		}
	}
	return nil
}

// faceTextInset is the edge of a face where no text sits, which check 5
// does not read: 4 DIPs at the left and right, 2 at the top and bottom.
// A face shader may draw an accent bar, an inner border, a stitched edge
// or a bottom line there in any color.
var faceTextInset = ui.Point{X: 4, Y: 2}

// faceContrast is check 5: the face drawn at 96×32 px with each of
// frames (faceFrames, or fewer) keeps the text of the ladder its flat color selects
// (pickLadder, as faceLadder picks it) readable at each pixel inside
// faceTextInset, in both appearances; a pixel not opaque counts as laid
// over the panel, as surface counts a face.
func faceContrast(sl *themeSlot, pal *[2]colors, role faceRole, frames []shaderFrame) error {
	const w, h = 96, 32
	area := image.Rect(int(faceTextInset.X), int(faceTextInset.Y), w-int(faceTextInset.X), h-int(faceTextInset.Y))
	for i, name := range [2]string{"light", "dark"} {
		k := &pal[i]
		flat := roleField(k, role).Over(k.panel)
		d := 0
		if isDark(flat) {
			d = 1
		}
		ref := pickLadder(flat, &pal[d], &pal[1-d], builtinLadder(d == 1), builtinLadder(d == 0))
		for _, f := range frames {
			img := renderSlot(sl, w, h, 1, ui.Point{X: w, Y: h}, i == 1, f)
			if err := readable(img, area, k.panel, ref.text, ref.textMuted); err != nil {
				return fmt.Errorf("in the %s appearance at %g s, %v", name, f.time, err)
			}
		}
	}
	return nil
}

// bandScale is the scale check 6 draws the window at.
const bandScale = 0.125

// bandContrast is check 6: the background drawn for a window of 1280×800
// DIPs at 1/8 scale, at times (bandTimes, or fewer), keeps the window's text readable in
// the sidebar's columns, in both appearances, unless the theme's sideBar.background is
// an opaque face there, which the text reads on instead.
func bandContrast(sl *themeSlot, pal *[2]colors, times []float32) error {
	const w, h = 1280, 800
	side := int(sidebarWidthIn(w) * bandScale)
	for i, name := range [2]string{"light", "dark"} {
		k := &pal[i]
		if k.sideBarFace.A == 255 {
			continue
		}
		for _, t := range times {
			img := renderSlot(sl, side, h*bandScale, bandScale, ui.Point{X: w, Y: h}, i == 1, shaderFrame{time: t, window: ui.Point{X: w, Y: h}})
			if err := readable(img, image.Rect(0, 0, side, h), k.bgBottom, k.text, k.textMuted); err != nil {
				return fmt.Errorf("behind the sidebar in the %s appearance at %g s, %v", name, t, err)
			}
		}
	}
	return nil
}
