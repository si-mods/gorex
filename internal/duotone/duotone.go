// Package duotone renders Kubai two-tone (duotone) solid icons natively with
// MyGo's SVG renderer and plays their per-layer hover micro-interactions.
//
// Icon layer data under icons/ comes from kubai087/kubai-icons (MIT License;
// see LICENSE-ICONS).
package duotone

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

//go:embed icons/*.json
var iconFS embed.FS

// Spec is the upstream Kubai duotone JSON definition for one icon.
type Spec struct {
	Name   string              `json:"name"`
	Key    string              `json:"key"`
	Layers []Layer             `json:"layers"`
	Moves  map[string]PoseSpec `json:"moves"`
	MS     float64             `json:"ms"`
}

// Layer is one vector shape in a duotone icon.
type Layer struct {
	N    json.RawMessage `json:"n"`
	D    string          `json:"d"`
	Tone string          `json:"tone"`
	SW   float64         `json:"sw"`
	Gap  float64         `json:"gap"`
	Clip string          `json:"clip"`
	Cut  []CutShape      `json:"cut"`
	Part string          `json:"part"`
}

// CutShape is a knockout shape subtracted from a single layer.
type CutShape struct {
	D  string  `json:"d"`
	SW float64 `json:"sw"`
}

func (l Layer) id(index int) string {
	s := strings.TrimSpace(string(l.N))
	if s == "" || s == "null" {
		return strconv.Itoa(index)
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// Icon is a parsed Kubai duotone icon with an internal SVG frame cache.
// Icons are safe for concurrent use.
type Icon struct {
	spec     Spec
	enterDur time.Duration

	mu    sync.Mutex
	cache map[frameKey]*ui.SVG
}

type frameKey struct {
	primary   uint32
	secondary uint32
	step      uint16
	entering  bool
}

const maxCachedFrames = 256

var (
	regMu sync.Mutex
	reg   = map[string]*Icon{}
)

// Lookup returns the embedded duotone icon with the given name.
func Lookup(name string) (*Icon, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	if ic, ok := reg[name]; ok {
		return ic, true
	}
	data, err := iconFS.ReadFile("icons/" + name + ".json")
	if err != nil {
		return nil, false
	}
	ic, err := Parse(data)
	if err != nil {
		return nil, false
	}
	reg[name] = ic
	return ic, true
}

// Must returns the embedded duotone icon or panics if it is missing.
func Must(name string) *Icon {
	ic, ok := Lookup(name)
	if !ok {
		panic("duotone: unknown icon " + strconv.Quote(name))
	}
	return ic
}

// Names returns all embedded duotone icon names.
func Names() []string {
	entries, err := iconFS.ReadDir("icons")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			out = append(out, name)
		}
	}
	return out
}

// Parse decodes a Kubai duotone JSON icon definition.
func Parse(data []byte) (*Icon, error) {
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &Icon{
		spec:     spec,
		enterDur: enterDuration(spec),
	}, nil
}

// Name returns the icon's upstream identifier.
func (ic *Icon) Name() string { return ic.spec.Name }

// Secondary derives the body (secondary) color from a primary color.
// On light surfaces it blends primary at 40% over white, matching Kubai's
// secondary(primary, 0.4). On dark surfaces it uses primary at 34% alpha so
// the body stays muted and translucent over dark glass.
func Secondary(primary ui.Color, dark bool) ui.Color {
	if dark {
		return primary.Alpha(0.34)
	}
	const alpha = 0.4
	blend := func(v uint8) uint8 {
		return uint8(math.Round(255 - float64(255-int(v))*alpha))
	}
	return ui.RGB(blend(primary.R), blend(primary.G), blend(primary.B))
}

func packColor(c ui.Color) uint32 {
	return uint32(c.R)<<24 | uint32(c.G)<<16 | uint32(c.B)<<8 | uint32(c.A)
}

func fmtNum(v float64) string {
	v = math.Round(v*1000) / 1000
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func hexColor(c ui.Color) string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

func alphaAttr(attr string, a uint8) string {
	if a == 255 {
		return ""
	}
	return fmt.Sprintf(` %s="%s"`, attr, fmtNum(float64(a)/255.0))
}

func shapeSVG(d string, sw float64, c ui.Color, extra, scale float64) string {
	col := hexColor(c)
	if sw > 0 {
		return fmt.Sprintf(`<path d="%s" fill="none" stroke="%s"%s stroke-width="%s" stroke-linecap="round" stroke-linejoin="round"/>`,
			d, col, alphaAttr("stroke-opacity", c.A), fmtNum((sw+extra)/scale))
	}
	if extra > 0 {
		return fmt.Sprintf(`<path d="%s" fill="%s"%s stroke="%s"%s stroke-width="%s" stroke-linejoin="round"/>`,
			d, col, alphaAttr("fill-opacity", c.A), col, alphaAttr("stroke-opacity", c.A), fmtNum(extra/scale))
	}
	return fmt.Sprintf(`<path d="%s" fill="%s"%s/>`, d, col, alphaAttr("fill-opacity", c.A))
}

// RenderSVG builds the SVG markup for the icon at progress q in [0, 1].
// When entering is true, q advances along the hover-in curve; when false, q
// advances along the return-to-rest curve from the hovered pose back to rest.
func (ic *Icon) RenderSVG(primary, secondary ui.Color, q float64, entering bool) string {
	k := 1.0
	if ic.spec.Key == "square" {
		k = 13.0 / 12.0
	}

	type piece struct {
		part string
		svg  string
	}
	var defs []string
	pieces := make([]piece, 0, len(ic.spec.Layers))
	maskBox := `maskUnits="userSpaceOnUse" x="-2" y="-2" width="20" height="20"><rect x="-2" y="-2" width="20" height="20" fill="#fff"/>`
	black := ui.RGB(0, 0, 0)

	wrapPose := func(id, inner string) string {
		pose, ok := ic.spec.Moves[id]
		if !ok {
			return inner
		}
		v := pose.eval(q, entering)
		tr := pose.transform(v)
		op := ""
		if v.a < 0.999 {
			op = fmt.Sprintf(` opacity="%s"`, fmtNum(clamp01(v.a)))
		}
		if tr == "" && op == "" {
			return inner
		}
		if tr != "" {
			tr = fmt.Sprintf(` transform="%s"`, tr)
		}
		return "<g" + tr + op + ">" + inner + "</g>"
	}

	for i, l := range ic.spec.Layers {
		lid := l.id(i)
		part := l.Part
		if part == "" {
			part = "#" + strconv.Itoa(i)
		}
		ink := primary
		if l.Tone == "b" {
			ink = secondary
		}

		if l.Gap > 0 {
			mid := fmt.Sprintf("g%d", i)
			knockout := wrapPose(lid, shapeSVG(l.D, l.SW, black, l.Gap*2, k))
			defs = append(defs, fmt.Sprintf(`<mask id="%s" %s%s</mask>`, mid, maskBox, knockout))
			for j := range pieces {
				if pieces[j].part != part {
					pieces[j].svg = fmt.Sprintf(`<g mask="url(#%s)">%s</g>`, mid, pieces[j].svg)
				}
			}
		}

		p := shapeSVG(l.D, l.SW, ink, 0, k)
		if l.Clip != "" {
			cid := fmt.Sprintf("c%d", i)
			defs = append(defs, fmt.Sprintf(`<clipPath id="%s" clipPathUnits="userSpaceOnUse"><path d="%s"/></clipPath>`, cid, l.Clip))
			p = fmt.Sprintf(`<g clip-path="url(#%s)">%s</g>`, cid, p)
		}
		if len(l.Cut) > 0 {
			kid := fmt.Sprintf("k%d", i)
			var cuts strings.Builder
			for _, cs := range l.Cut {
				cuts.WriteString(shapeSVG(cs.D, cs.SW, black, 0, k))
			}
			defs = append(defs, fmt.Sprintf(`<mask id="%s" %s%s</mask>`, kid, maskBox, cuts.String()))
			p = fmt.Sprintf(`<g mask="url(#%s)">%s</g>`, kid, p)
		}

		pieces = append(pieces, piece{part: part, svg: wrapPose(lid, p)})
	}

	var body strings.Builder
	for _, p := range pieces {
		body.WriteString(p.svg)
	}
	out := wrapPose("all", body.String())
	if k != 1 {
		out = fmt.Sprintf(`<g transform="translate(8 8) scale(%s) translate(-8 -8)">%s</g>`, fmtNum(k), out)
	}

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16">`)
	if len(defs) > 0 {
		b.WriteString("<defs>")
		for _, d := range defs {
			b.WriteString(d)
		}
		b.WriteString("</defs>")
	}
	b.WriteString(out)
	b.WriteString("</svg>")
	return b.String()
}

// SVG returns a cached parsed *ui.SVG for the icon at progress q in [0, 1].
func (ic *Icon) SVG(primary, secondary ui.Color, q float64, entering bool) *ui.SVG {
	q = clamp01(q)
	if q == 0 {
		entering = true
	}
	key := frameKey{
		primary:   packColor(primary),
		secondary: packColor(secondary),
		step:      uint16(math.Round(q * motionSteps)),
		entering:  entering,
	}
	ic.mu.Lock()
	if s := ic.cache[key]; s != nil {
		ic.mu.Unlock()
		return s
	}
	ic.mu.Unlock()

	quantQ := float64(key.step) / float64(motionSteps)
	s := ui.MustParseSVG([]byte(ic.RenderSVG(primary, secondary, quantQ, entering)))

	ic.mu.Lock()
	if ic.cache == nil || len(ic.cache) >= maxCachedFrames {
		ic.cache = make(map[frameKey]*ui.SVG)
	}
	ic.cache[key] = s
	ic.mu.Unlock()
	return s
}
