package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gorex/internal/config"
	"gorex/internal/iconpack"
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

func TestPaneProgramWithIconPack(t *testing.T) {
	packsDir := t.TempDir()
	packDir := filepath.Join(packsDir, "community")
	if err := os.MkdirAll(filepath.Join(packDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M2 2h20v20H2z"/></svg>`
	if err := os.WriteFile(filepath.Join(packDir, "icons", "tf.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema":1,"id":"community","name":"Community","programs":{"terraform":{"icon":"icons/tf.svg","bg":"#7b42bc","fg":"#ffffff"}}}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, warns := iconpack.LoadRegistry(packsDir, "", []string{"community"}, []config.ProgramIconRule(nil), "")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	setActiveIconPacks(reg)
	t.Cleanup(func() { setActiveIconPacks(nil) })

	osc := &rex.ProgramIcon{
		Format: "svg",
		Mode:   "mono",
		Data:   base64.StdEncoding.EncodeToString([]byte(svg)),
		Bg:     "#000000",
		Hash:   "osc99999",
	}
	p := &Pane{
		info: rex.SessionInfo{
			Shell:       "bash",
			Program:     "terraform",
			Idle:        false,
			ProgramIcon: osc,
		},
	}
	got := p.program()
	if got.Icon == nil || got.TileBg != ui.Hex("#7b42bc") {
		t.Fatalf("expected pack icon (#7b42bc) to override OSC 7502 by default, got %+v", got)
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

func TestProgramIconRender(t *testing.T) {
	a, tt := newTestApp(t)
	a.newTab(os.TempDir())
	tt.Frame()
	if len(a.tabs) < 2 {
		t.Fatalf("expected at least 2 tabs, got %d", len(a.tabs))
	}
	starSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14l-5-4.87 6.91-1.01L12 2z"/></svg>`
	a.tabs[0].Focus.Name = "custom-cli"
	a.tabs[0].Focus.info.Program = "custom-cli"
	a.tabs[0].Focus.info.Idle = false
	a.tabs[0].Focus.info.ProgramIcon = &rex.ProgramIcon{
		Format: "svg",
		Mode:   "mono",
		Data:   base64.StdEncoding.EncodeToString([]byte(starSVG)),
		Bg:     "#6d5bd0",
		Fg:     "#ffffff",
		Hash:   "shot0001",
	}
	a.tabs[1].Focus.Name = "my-png-app"
	a.tabs[1].Focus.info.Program = "my-png-app"
	a.tabs[1].Focus.info.Idle = false
	a.tabs[1].Focus.info.ProgramIcon = &rex.ProgramIcon{
		Format: "png",
		Mode:   "color",
		Data:   testPNGData(t),
		Bg:     "#1f6f43",
		Hash:   "shot0002",
	}
	a.selectTab(0)

	shotDir := os.Getenv("GOREX_SHOT_DIR")
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		img := tt.Image()
		if img == nil || img.Bounds().Empty() {
			t.Fatalf("empty image for dark=%v", dark)
		}
		if shotDir != "" {
			name := "light.png"
			if dark {
				name = "dark.png"
			}
			f, err := os.Create(filepath.Join(shotDir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, img)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestOmpGradientTile(t *testing.T) {
	omp := programOf("omp")
	if omp.TileBgGradient == nil {
		t.Fatal("omp has no TileBgGradient")
	}
	if omp.TileFg != ui.Hex("#ffffff") {
		t.Fatalf("omp TileFg = %v, want #ffffff", omp.TileFg)
	}
	raw := tileImage(omp)
	if len(raw) == 0 {
		t.Fatal("tileImage(omp) returned empty")
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode omp tile PNG: %v", err)
	}
	// Sample inside the tile (tile is 21x16 centered in 24x24, scaled 3x = 72x72).
	// Top-left corner of tile should be pink; bottom-right should be blue-violet.
	tlR, tlG, tlB, tlA := img.At(14, 18).RGBA()
	brR, brG, brB, brA := img.At(58, 52).RGBA()
	if tlA == 0 || brA == 0 {
		t.Fatalf("expected opaque tile corners, got alpha tl=%d br=%d", tlA>>8, brA>>8)
	}
	if tlR>>8 <= tlG>>8+40 || tlR>>8 < 180 {
		t.Errorf("top-left pixel = (%d,%d,%d), want pink", tlR>>8, tlG>>8, tlB>>8)
	}
	if brB>>8 <= brR>>8 || brB>>8 < 170 {
		t.Errorf("bottom-right pixel = (%d,%d,%d), want blue-violet", brR>>8, brG>>8, brB>>8)
	}
	if shotDir := os.Getenv("GOREX_SHOT_DIR"); shotDir != "" {
		if err := os.WriteFile(filepath.Join(shotDir, "omp-tile.png"), raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// A custom OSC 7502 background overrides the built-in gradient.
	overridden := programWithIcon(omp, &rex.ProgramIcon{Bg: "#112233"})
	if overridden.TileBgGradient != nil {
		t.Fatal("custom Bg should clear TileBgGradient")
	}
}

func TestAllBrandTilesParseAndRender(t *testing.T) {
	// Disable network fetch during the offline parse/render sweep.
	oldURL := brandBaseURL
	brandBaseURL = ""
	t.Cleanup(func() { brandBaseURL = oldURL })

	// Every registered program's glyph must return a valid SVG (embedded or fallback).
	for name, prog := range programs {
		g := prog.TileGlyph
		if g == "" {
			g = prog.Glyph
		}
		if icon(g) == nil {
			t.Fatalf("program %q glyph %q returned nil SVG", name, g)
		}
	}
	// Every extra on-demand brand SVG on disk must also parse cleanly.
	extraFiles, err := filepath.Glob(filepath.Join("assets", "brands-extra", "*.svg"))
	if err != nil || len(extraFiles) < 100 {
		t.Fatalf("expected >=100 extra brand SVGs, got %d (err=%v)", len(extraFiles), err)
	}
	for _, f := range extraFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := ui.ParseSVG(b); err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
	}
	// Spot-check key newly added brand tiles and command aliases.
	samples := []string{
		"cursor", "opencode", "pi", "gemini", "copilot", "ollama", "amp", "openclaw",
		"uv", "fish", "nu", "tailscale", "k9s", "jj", "yazi", "zig",
		"vite", "vercel", "cloudflare", "terraform", "xcode", "typst", "duckdb", "nix",
	}
	for _, s := range samples {
		p := programOf(s)
		if p.Glyph == "square-terminal" {
			t.Errorf("programOf(%q) fell back to generic terminal", s)
		}
		if s == "fish" || s == "nu" {
			if !p.Shell {
				t.Errorf("programOf(%q) should keep Shell=true", s)
			}
			continue
		}
		if raw := tileImage(p); len(raw) == 0 {
			t.Errorf("tileImage(%q) returned empty", s)
		}
	}
}

func TestBrandOnDemandFetchAndCache(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	var hits atomic.Int32
	cursorSVG, err := os.ReadFile(filepath.Join("assets", "brands-extra", "cursor.svg"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/cursor.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write(cursorSVG)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	resetBrandCache := func() {
		brandMu.Lock()
		brandSVGs = map[string]*ui.SVG{}
		brandDiskChecked = map[string]bool{}
		brandFetching = map[string]bool{}
		brandFailed = map[string]bool{}
		brandMu.Unlock()
	}
	oldURL, oldClient, oldCB := brandBaseURL, brandHTTPClient, onBrandLoaded
	t.Cleanup(func() {
		brandBaseURL, brandHTTPClient, onBrandLoaded = oldURL, oldClient, oldCB
		resetBrandCache()
	})
	resetBrandCache()
	brandBaseURL = srv.URL
	brandHTTPClient = srv.Client()

	loaded := make(chan string, 1)
	onBrandLoaded = func(id string) { loaded <- id }

	// 1. First call returns terminal fallback and kicks off background fetch.
	termSVG := icon("terminal")
	if got := icon("brand:cursor"); got != termSVG {
		t.Fatalf("expected immediate terminal fallback before fetch")
	}
	select {
	case id := <-loaded:
		if id != "cursor" {
			t.Fatalf("loaded id = %q, want cursor", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for on-demand brand fetch")
	}
	if got := icon("brand:cursor"); got == termSVG || got == nil {
		t.Fatal("expected fetched cursor SVG after load")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 HTTP fetch, got %d", hits.Load())
	}

	// 2. Clearing memory cache loads synchronously from disk with 0 HTTP hits.
	resetBrandCache()
	if got := icon("brand:cursor"); got == termSVG || got == nil {
		t.Fatal("expected disk-cached cursor SVG")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected disk cache hit without HTTP request, got %d hits", hits.Load())
	}
}
