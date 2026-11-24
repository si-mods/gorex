package theme

import (
	"slices"
	"testing"
)

// TestParseMetrics: the metrics and glass keys of
// docs/DSK-012-theme-shaders.md parse; a value out of range clamps, a value
// of the wrong kind and an unknown key drop to the built-in value, each
// with a warning; a manifest without the keys has none.
func TestParseMetrics(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "metrics": { "radius": 0.2, "border": 2, "font": ["Tahoma", "Segoe UI", "system"], "shadow": { "scale": 0.5, "hard": true } },
  "glass": false
}`))
	if err != nil || len(warn) != 0 {
		t.Fatal(err, warn)
	}
	want := Metrics{Radius: 0.2, Border: 2, Font: []string{"Tahoma", "Segoe UI", "system"}, Shadow: ShadowMetric{Scale: 0.5, Hard: true}}
	if got := m.Metrics; got == nil || got.Radius != want.Radius || got.Border != want.Border || !slices.Equal(got.Font, want.Font) || got.Shadow != want.Shadow {
		t.Errorf("metrics %+v, want %+v", got, want)
	}
	if m.Glass == nil || *m.Glass {
		t.Errorf("glass %v, want false", m.Glass)
	}

	m, warn, err = Parse([]byte(`{
  "metrics": { "radius": 3, "border": 1.4, "font": ["A", "B,C", "", 7, "D", "E", "F"], "shadow": { "scale": -1, "hard": "yes", "blur": 2 }, "gap": 4 },
  "glass": "off"
}`))
	if err != nil {
		t.Fatal(err)
	}
	got := m.Metrics
	if got == nil || got.Radius != 1 || got.Border != 1 || !slices.Equal(got.Font, []string{"A", "D", "E", "F"}) || got.Shadow != (ShadowMetric{Scale: 0}) {
		t.Errorf("clamped metrics %+v", got)
	}
	if m.Glass != nil {
		t.Errorf("a glass that is not a bool kept %v", *m.Glass)
	}
	wantWarn := []string{
		`glass: not true or false`,
		`metrics.border: 1.4 rounded to 1`,
		`metrics.font[1]: bad family name`,
		`metrics.font[2]: bad family name`,
		`metrics.font[3]: bad family name`,
		`metrics.radius: 3 clamped to 0–1`,
		`metrics.shadow.hard: not true or false`,
		`metrics.shadow.scale: -1 clamped to 0–2`,
		`metrics.shadow: unknown key "blur"`,
		`metrics: unknown key "gap"`,
	}
	if !slices.Equal(warn, wantWarn) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, wantWarn)
	}

	m, _, _ = Parse([]byte(`{"metrics": {"font": ["A", "B", "C", "D", "E"]}}`))
	if !slices.Equal(m.Metrics.Font, []string{"A", "B", "C", "D"}) || m.Metrics.Radius != 1 || m.Metrics.Shadow.Scale != 1 {
		t.Errorf("a fifth name or a default: %+v", m.Metrics)
	}
	m, warn, _ = Parse([]byte(`{"metrics": 3}`))
	if m.Metrics != nil || !slices.Equal(warn, []string{"metrics: not an object"}) {
		t.Errorf("metrics that is not an object: %+v, %q", m.Metrics, warn)
	}
	m, _, _ = Parse([]byte(`{"name": "plain"}`))
	if m.Metrics != nil || m.Glass != nil {
		t.Error("a manifest without the keys has metrics or glass")
	}
}
