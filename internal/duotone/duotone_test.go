package duotone

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestEmbeddedIconsParseCleanly(t *testing.T) {
	names := Names()
	if len(names) < 8 {
		t.Fatalf("expected at least 8 embedded icons, got %d", len(names))
	}
	primary := ui.Hex("#007AFF")
	for _, dark := range []bool{false, true} {
		secondary := Secondary(primary, dark)
		for _, name := range names {
			ic, ok := Lookup(name)
			if !ok {
				t.Fatalf("Lookup(%q) failed", name)
			}
			if ic.Name() != name {
				t.Errorf("icon %q has Name()=%q", name, ic.Name())
			}
			for _, tc := range []struct {
				q        float64
				entering bool
			}{
				{0, true},
				{0.5, true},
				{1, true},
				{0.5, false},
			} {
				s := ic.SVG(primary, secondary, tc.q, tc.entering)
				if unsup := s.Unsupported(); len(unsup) > 0 {
					t.Errorf("icon %q (dark=%v q=%v in=%v) has unsupported SVG features: %v",
						name, dark, tc.q, tc.entering, unsup)
				}
			}
		}
	}
}

func TestSecondaryColorDerivation(t *testing.T) {
	// Upstream Kubai default: #5A76F2 at 40% over white = #BDC8FA.
	got := Secondary(ui.Hex("#5A76F2"), false)
	want := ui.Hex("#BDC8FA")
	if got != want {
		t.Fatalf("light Secondary(#5A76F2) = %v, want %v", got, want)
	}

	dark := Secondary(ui.Hex("#3B9EFF"), true)
	if dark.R != 0x3B || dark.G != 0x9E || dark.B != 0xFF {
		t.Fatalf("dark Secondary RGB changed: %v", dark)
	}
	if dark.A == 255 || dark.A == 0 {
		t.Fatalf("dark Secondary alpha should be translucent, got %d", dark.A)
	}
}

func TestMotionInterpolation(t *testing.T) {
	// monitor has hover-only layer a0 with rest.a = 0 and a = 1.
	mon := Must("monitor")
	restSVG := mon.RenderSVG(ui.Hex("#28A745"), Secondary(ui.Hex("#28A745"), false), 0, true)
	if !strings.Contains(restSVG, `opacity="0"`) {
		t.Errorf("monitor rest SVG should hide hover-only layer a0, got:\n%s", restSVG)
	}
	hoverSVG := mon.RenderSVG(ui.Hex("#28A745"), Secondary(ui.Hex("#28A745"), false), 1, true)
	if strings.Contains(hoverSVG, `opacity="0"`) {
		t.Errorf("monitor hovered SVG should reveal layer a0, got:\n%s", hoverSVG)
	}

	// sun rotates rays 45 degrees on hover.
	sun := Must("sun")
	sunHover := sun.RenderSVG(ui.Hex("#007AFF"), Secondary(ui.Hex("#007AFF"), false), 1, true)
	if !strings.Contains(sunHover, "rotate(45)") {
		t.Errorf("sun hovered SVG should rotate rays by 45 deg, got:\n%s", sunHover)
	}

	// sparkles is a square icon and applies FIT scaling (13/12).
	sp := Must("sparkles")
	spRest := sp.RenderSVG(ui.Hex("#AF52DE"), Secondary(ui.Hex("#AF52DE"), false), 0, true)
	if !strings.Contains(spRest, "scale(1.083)") {
		t.Errorf("square icon sparkles should apply 13/12 FIT scale, got:\n%s", spRest)
	}
}

func TestHoverStateTransitions(t *testing.T) {
	t0 := time.Unix(1000, 0)
	st := hoverState{enterDur: 480 * time.Millisecond, start: t0}

	st.update(true, t0, false)
	q, entering, moving := st.progress(t0.Add(240 * time.Millisecond))
	if !entering || !moving || math.Abs(q-0.5) > 0.01 {
		t.Fatalf("mid-enter: got q=%v entering=%v moving=%v", q, entering, moving)
	}

	q, entering, moving = st.progress(t0.Add(500 * time.Millisecond))
	if !entering || moving || q != 1 {
		t.Fatalf("end-enter: got q=%v entering=%v moving=%v", q, entering, moving)
	}

	// Leave back to rest.
	t1 := t0.Add(600 * time.Millisecond)
	st.update(false, t1, false)
	q, entering, moving = st.progress(t1.Add(180 * time.Millisecond))
	if entering || !moving || math.Abs(q-0.5) > 0.01 {
		t.Fatalf("mid-leave: got q=%v entering=%v moving=%v", q, entering, moving)
	}

	q, entering, moving = st.progress(t1.Add(400 * time.Millisecond))
	if entering || moving || q != 0 {
		t.Fatalf("end-leave: got q=%v entering=%v moving=%v", q, entering, moving)
	}

	// ReduceMotion snaps immediately with no moving frames.
	st.update(true, t1.Add(500*time.Millisecond), true)
	q, _, moving = st.progress(t1.Add(500 * time.Millisecond))
	if moving || q != 1 {
		t.Fatalf("reduce motion: got q=%v moving=%v", q, moving)
	}
}
