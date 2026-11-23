package rex

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func iconOSC(body string) string { return "\x1b]7502;" + body + "\x1b\\" }

func validPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProgramIconParseAndLifecycle(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/></svg>`
	svgB64 := base64.RawURLEncoding.EncodeToString([]byte(svg))
	pngB64 := base64.StdEncoding.EncodeToString(validPNGBytes(t, 16, 16))

	var ic programIcon

	// Query triggers reply without modifying icon state.
	if q := ic.feed([]byte("hello\x1b]7502;?\x07world" + iconOSC("?"))); q != 2 || ic.get() != nil {
		t.Fatalf("query got q=%d icon=%v, want q=2 icon=nil", q, ic.get())
	}

	// Set SVG icon with bg/fg.
	before := ic.updates
	ic.feed([]byte(iconOSC("format=svg:bg=d97757:fg=ffffff:data=" + svgB64)))
	got := ic.get()
	if got == nil || ic.updates != before+1 {
		t.Fatalf("expected svg icon update, got=%+v updates=%d", got, ic.updates)
	}
	if got.Format != "svg" || got.Mode != "mono" || got.Bg != "#d97757" || got.Fg != "#ffffff" || got.Hash == "" {
		t.Fatalf("unexpected svg icon fields: %+v", got)
	}

	// Duplicate icon does not bump updates.
	before = ic.updates
	ic.feed([]byte(iconOSC("format=svg:bg=#D97757:fg=#FFFFFF:data=" + svgB64)))
	if ic.updates != before {
		t.Fatalf("duplicate icon bumped updates from %d to %d", before, ic.updates)
	}

	// Switch to PNG icon.
	ic.feed([]byte(iconOSC("format=png:data=" + pngB64)))
	got = ic.get()
	if got == nil || got.Format != "png" || got.Mode != "color" {
		t.Fatalf("unexpected png icon: %+v", got)
	}

	// Clear via OSC 7502;clear.
	ic.feed([]byte(iconOSC("clear")))
	if ic.get() != nil {
		t.Fatalf("icon not cleared by 7502;clear")
	}

	// Set again, then drop on OSC 133;A.
	ic.feed([]byte(iconOSC("format=svg:mode=color:data=" + svgB64)))
	if ic.get() == nil {
		t.Fatalf("icon not set")
	}
	ic.feed([]byte("\x1b]133;A\x07"))
	if ic.get() != nil {
		t.Fatalf("icon not cleared on 133;A")
	}

	// Set again, then drop on RIS (ESC c).
	ic.feed([]byte(iconOSC("format=svg:data=" + svgB64)))
	ic.feed([]byte("\x1bc"))
	if ic.get() != nil {
		t.Fatalf("icon not cleared on RIS")
	}
}

func TestProgramIconRejectsInvalidAndOversized(t *testing.T) {
	var ic programIcon

	cases := []struct {
		name string
		seq  string
	}{
		{"script tag in svg", iconOSC("format=svg:data=" + base64.StdEncoding.EncodeToString([]byte(`<svg><script>alert(1)</script></svg>`)))},
		{"onload attr in svg", iconOSC("format=svg:data=" + base64.StdEncoding.EncodeToString([]byte(`<svg onload="alert(1)"></svg>`)))},
		{"not svg", iconOSC("format=svg:data=" + base64.StdEncoding.EncodeToString([]byte(`<div>hi</div>`)))},
		{"not png", iconOSC("format=png:data=" + base64.StdEncoding.EncodeToString([]byte(`not a png`)))},
		{"mono png disallowed", iconOSC("format=png:mode=mono:data=" + base64.StdEncoding.EncodeToString(validPNGBytes(t, 8, 8)))},
		{"bad bg hex", iconOSC("format=svg:bg=xyz:data=" + base64.StdEncoding.EncodeToString([]byte(`<svg/>`)))},
		{"oversized payload", iconOSC("format=svg:data=" + base64.StdEncoding.EncodeToString([]byte("<svg>"+strings.Repeat("a", maxIconBytes)+"</svg>")))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ic.feed([]byte(tc.seq))
			if ic.get() != nil {
				t.Fatalf("expected %s to be rejected, got %+v", tc.name, ic.get())
			}
		})
	}
}

func TestProgramIconSplitReads(t *testing.T) {
	svg := `<svg viewBox="0 0 16 16"><path d="M0 0h16v16H0z"/></svg>`
	seq := iconOSC("format=svg:data=" + base64.StdEncoding.EncodeToString([]byte(svg)))
	var ic programIcon
	for i := 0; i < len(seq); i += 7 {
		end := min(i+7, len(seq))
		ic.feed([]byte(seq[i:end]))
	}
	if ic.get() == nil {
		t.Fatalf("split OSC 7502 sequence was not assembled")
	}
}
