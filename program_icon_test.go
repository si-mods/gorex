package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorex/internal/rex"
)

func testPNGData(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, G: 80, B: 40, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestPaneProgramWithCustomIcon(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M4 4h16v16H4z"/></svg>`
	ic := &rex.ProgramIcon{
		Format: "svg",
		Mode:   "mono",
		Data:   base64.StdEncoding.EncodeToString([]byte(svg)),
		Bg:     "#f7f7f7",
		Fg:     "#1a1a1a",
		Hash:   "deadbeef00000001",
	}
	p := &Pane{
		info: rex.SessionInfo{
			Shell:       "bash",
			Program:     "my-custom-cli",
			Idle:        false,
			ProgramIcon: ic,
		},
	}
	got := p.program()
	if got.Icon != ic {
		t.Fatalf("program().Icon = %v, want %v", got.Icon, ic)
	}
	if got.Shell {
		t.Fatalf("program().Shell = true, want false when custom icon is active")
	}
	if got.TileBg != ui.Hex("#f7f7f7") || got.TileFg != ui.Hex("#1a1a1a") {
		t.Fatalf("tile colors = (%v, %v), want (#f7f7f7, #1a1a1a)", got.TileBg, got.TileFg)
	}
	if !got.TileBorder {
		t.Fatalf("TileBorder = false for light bg #f7f7f7, want true")
	}
	if s := customIconSVG(ic); s == nil {
		t.Fatalf("customIconSVG returned nil")
	}
}

func TestCustomIconBitmapAndTileImage(t *testing.T) {
	ic := &rex.ProgramIcon{
		Format: "png",
		Mode:   "color",
		Data:   testPNGData(t),
		Bg:     "#112233",
		Hash:   "deadbeef00000002",
	}
	if src := customIconBitmap(ic); src == nil {
		t.Fatalf("customIconBitmap returned nil")
	}
	prog := programWithIcon(programOf("custom-tool"), ic)
	if b := tileImage(prog); len(b) == 0 {
		t.Fatalf("tileImage returned empty for custom PNG icon")
	}
}
