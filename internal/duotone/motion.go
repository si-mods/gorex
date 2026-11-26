package duotone

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const (
	baseDuration  = 480 * time.Millisecond
	stepDuration  = 110 * time.Millisecond
	leaveDuration = 360 * time.Millisecond
	motionSteps   = 48
)

var (
	// easeIn overshoots slightly and springs back into the hover pose.
	easeIn = cubicBezier(0.34, 1.56, 0.64, 1)
	// easeBack eases smoothly back to rest without overshoot.
	easeBack = cubicBezier(0.33, 1, 0.68, 1)
)

// Track holds either a single target value or a sequence of keyframes.
type Track []float64

// UnmarshalJSON accepts either a JSON number or an array of numbers.
func (t *Track) UnmarshalJSON(data []byte) error {
	var single float64
	if err := json.Unmarshal(data, &single); err == nil {
		*t = Track{single}
		return nil
	}
	var list []float64
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*t = Track(list)
	return nil
}

// RestValues overrides default rest values for a layer (for example, a: 0 for
// a layer that appears only on hover).
type RestValues struct {
	X  *float64 `json:"x"`
	Y  *float64 `json:"y"`
	R  *float64 `json:"r"`
	S  *float64 `json:"s"`
	SX *float64 `json:"sx"`
	SY *float64 `json:"sy"`
	A  *float64 `json:"a"`
}

// PoseSpec describes how one layer (or "all") animates on hover.
type PoseSpec struct {
	X    Track       `json:"x"`
	Y    Track       `json:"y"`
	R    Track       `json:"r"`
	S    Track       `json:"s"`
	SX   Track       `json:"sx"`
	SY   Track       `json:"sy"`
	A    Track       `json:"a"`
	O    []float64   `json:"o"`
	Rest *RestValues `json:"rest"`
}

type poseValues struct {
	x, y, r, s, sx, sy, a float64
}

func defaultRest(r *RestValues) poseValues {
	v := poseValues{s: 1, sx: 1, sy: 1, a: 1}
	if r == nil {
		return v
	}
	if r.X != nil {
		v.x = *r.X
	}
	if r.Y != nil {
		v.y = *r.Y
	}
	if r.R != nil {
		v.r = *r.R
	}
	if r.S != nil {
		v.s = *r.S
	}
	if r.SX != nil {
		v.sx = *r.SX
	}
	if r.SY != nil {
		v.sy = *r.SY
	}
	if r.A != nil {
		v.a = *r.A
	}
	return v
}

func (p PoseSpec) maxKeyframes() int {
	m := 1
	for _, tr := range []Track{p.X, p.Y, p.R, p.S, p.SX, p.SY, p.A} {
		if len(tr) > m {
			m = len(tr)
		}
	}
	return m
}

func enterDuration(spec Spec) time.Duration {
	if spec.MS > 0 {
		return time.Duration(spec.MS * float64(time.Millisecond))
	}
	maxSteps := 1
	for _, p := range spec.Moves {
		if k := p.maxKeyframes(); k > maxSteps {
			maxSteps = k
		}
	}
	return baseDuration + stepDuration*time.Duration(maxSteps-1)
}

func (p PoseSpec) eval(q float64, entering bool) poseValues {
	q = clamp01(q)
	rest := defaultRest(p.Rest)
	if q == 0 {
		return rest
	}
	if entering {
		return poseValues{
			x:  evalTrack(rest.x, p.X, q, easeIn),
			y:  evalTrack(rest.y, p.Y, q, easeIn),
			r:  evalTrack(rest.r, p.R, q, easeIn),
			s:  evalTrack(rest.s, p.S, q, easeIn),
			sx: evalTrack(rest.sx, p.SX, q, easeIn),
			sy: evalTrack(rest.sy, p.SY, q, easeIn),
			a:  evalTrack(rest.a, p.A, q, easeIn),
		}
	}
	// Returning from the held end pose back to rest as q goes 1 -> 0.
	t := easeBack(1 - q)
	held := func(r float64, tr Track) float64 {
		if len(tr) == 0 {
			return r
		}
		return tr[len(tr)-1]
	}
	lerpBack := func(r float64, tr Track) float64 {
		h := held(r, tr)
		return h + (r-h)*t
	}
	return poseValues{
		x:  lerpBack(rest.x, p.X),
		y:  lerpBack(rest.y, p.Y),
		r:  lerpBack(rest.r, p.R),
		s:  lerpBack(rest.s, p.S),
		sx: lerpBack(rest.sx, p.SX),
		sy: lerpBack(rest.sy, p.SY),
		a:  lerpBack(rest.a, p.A),
	}
}

func evalTrack(from float64, tr Track, q float64, ease func(float64) float64) float64 {
	if len(tr) == 0 {
		return from
	}
	n := len(tr)
	if n == 1 {
		return from + (tr[0]-from)*ease(q)
	}
	k := math.Min(float64(n)-1e-9, q*float64(n))
	i := int(math.Floor(k))
	t := k - float64(i)
	var start float64
	if i == 0 {
		start = from
	} else {
		start = tr[i-1]
	}
	end := tr[i]
	if i == n-1 {
		return start + (end-start)*ease(t)
	}
	return start + (end-start)*cubicInOut(t)
}

func (p PoseSpec) transform(v poseValues) string {
	sx := v.s * v.sx
	sy := v.s * v.sy
	if math.Abs(v.x) < 1e-4 && math.Abs(v.y) < 1e-4 && math.Abs(v.r) < 1e-4 &&
		math.Abs(sx-1) < 1e-4 && math.Abs(sy-1) < 1e-4 {
		return ""
	}
	ox, oy := 8.0, 8.0
	if len(p.O) == 2 {
		ox, oy = p.O[0], p.O[1]
	}
	return fmt.Sprintf("translate(%s %s) rotate(%s) scale(%s %s) translate(%s %s)",
		fmtNum(v.x+ox), fmtNum(v.y+oy), fmtNum(v.r), fmtNum(sx), fmtNum(sy), fmtNum(-ox), fmtNum(-oy))
}

func cubicInOut(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func cubicBezier(x1, y1, x2, y2 float64) func(float64) float64 {
	cx := 3 * x1
	bx := 3*(x2-x1) - cx
	ax := 1 - cx - bx
	cy := 3 * y1
	by := 3*(y2-y1) - cy
	ay := 1 - cy - by
	sx := func(t float64) float64 { return ((ax*t+bx)*t + cx) * t }
	sy := func(t float64) float64 { return ((ay*t+by)*t + cy) * t }
	dx := func(t float64) float64 { return (3*ax*t+2*bx)*t + cx }
	return func(x float64) float64 {
		if x <= 0 {
			return 0
		}
		if x >= 1 {
			return 1
		}
		t := x
		for range 8 {
			e := sx(t) - x
			if math.Abs(e) < 1e-5 {
				break
			}
			d := dx(t)
			if math.Abs(d) < 1e-6 {
				break
			}
			t -= e / d
		}
		return sy(t)
	}
}
