package theme

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestParseDropsLooks: GoRex draws no look of its own from a pack
// (docs/DSK-008-theme.md, Non-goals). A manifest with the ambience and
// brand keys of DSK-010, and its mascot and ticker, loads its colors and
// its assets, and drops each of those keys with an unknown key warning, as
// an older build drops a key it does not know. A sprite sheet object is no
// file name, and a bad name or file drops.
func TestParseDropsLooks(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "name": "Sea",
  "seeds": { "light": { "accent": "#3b62f6" }, "dark": { "accent": "#6c8cff" } },
  "assets": {
    "side": "side.png",
    "wave": { "file": "wave.png", "frames": 8, "columns": 4, "fps": 10 },
    "bad/name": "x.png",
    "up": "../up.png"
  },
  "ambience": {
    "glows": [{ "x": 0.88, "y": -0.08, "rx": 0.55, "ry": 0.5, "color": "#3b62f64d" }],
    "images": [{ "asset": "side", "anchor": "bottomRight", "height": 0.74, "opacity": 0.1 }],
    "sprites": [{ "asset": "side", "y": 0.14, "height": 56, "seconds": 64 }]
  },
  "brand": { "icon": "side", "wordmark": "Sea Whale" },
  "mascot": { "idle": "side" },
  "ticker": { "lines": ["hi"] }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Sea" || m.Seeds[0][Accent] != ui.Hex("#3b62f6") || m.Seeds[1][Accent] != ui.Hex("#6c8cff") {
		t.Errorf("colors %+v", m.Seeds)
	}
	if want := map[string]Asset{"side": {File: "side.png"}}; !reflect.DeepEqual(m.Assets, want) {
		t.Errorf("assets %+v", m.Assets)
	}
	want := []string{
		`assets.up: bad file name "../up.png"`,
		`assets.wave: not a file name`,
		`assets: bad name "bad/name"`,
		`unknown key "ambience"`,
		`unknown key "brand"`,
		`unknown key "mascot"`,
		`unknown key "ticker"`,
	}
	if !slices.Equal(warn, want) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, want)
	}

	// A manifest of DSK-008 keys only has no assets.
	m, warn, err = Parse([]byte(`{"name":"slate","seeds":{"light":{"accent":"#0a6bff"}}}`))
	if err != nil || len(warn) != 0 || m.Assets != nil {
		t.Errorf("a DSK-008 manifest: %+v, %q, %v", m, warn, err)
	}
}

// TestParseText: a description loses control characters and
// bidirectional controls, and keeps its first characters, counted as
// characters.
func TestParseText(t *testing.T) {
	for in, want := range map[string]string{
		"蓝色大肥鱼":                               "蓝色大肥鱼",
		"a‮b⁦c‏d؜e":                           "abcde",
		"tab\there\nnew":                      "tabherenew",
		strings.Repeat("鱼", maxDescription+6): strings.Repeat("鱼", maxDescription),
	} {
		got, _ := cleanText(in, maxDescription)
		if got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// chunk is a PNG chunk with its CRC.
func chunk(typ string, data []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	b = append(b, typ...)
	b = append(b, data...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

// pngHeader is a PNG's signature and IHDR for a w×h picture, with an acTL
// of the frames when frames is above 0, and no pixels: what CheckHeader
// reads.
func pngHeader(w, h, frames int) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(w))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(h))
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	b := append(slices.Clone(pngMagic), chunk("IHDR", ihdr)...)
	if frames > 0 {
		b = append(b, chunk("acTL", binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(frames)), 0))...)
	}
	return append(b, chunk("IDAT", nil)...)
}

// gifFrames is a w×h GIF of n empty images: blocks, no pixels.
func gifFrames(w, h, n int) []byte {
	b := []byte("GIF89a")
	b = binary.LittleEndian.AppendUint16(b, uint16(w))
	b = binary.LittleEndian.AppendUint16(b, uint16(h))
	b = append(b, 0, 0, 0)
	for range n {
		b = append(b, 0x21, 0xf9, 4, 0, 10, 0, 0, 0) // graphic control
		b = append(b, 0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 0)
	}
	return append(b, 0x3b)
}

// TestAssetLimits: a PNG over the pixel cap, an APNG and a GIF over the
// frame cap, an animated image over its size cap and an unknown format
// each fail on their header, which is all a fixture holds: nothing is
// decoded. A name with a path, a link and a file over the byte cap do not
// read (docs/DSK-010-theme-ambience.md, Tests).
func TestAssetLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		ok   bool
		want Format
	}{
		{"png", pngHeader(1024, 300, 0), true, FormatPNG},
		{"png too large", pngHeader(1025, 10, 0), false, FormatPNG},
		{"png bomb", pngHeader(1<<20, 1<<20, 0), false, FormatPNG},
		{"apng", pngHeader(256, 256, 8), true, FormatAPNG},
		{"apng too many frames", pngHeader(64, 64, 65), false, FormatAPNG},
		{"apng too large", pngHeader(513, 64, 2), false, FormatAPNG},
		{"apng too much memory", pngHeader(512, 512, 17), false, FormatAPNG},
		{"gif", gifFrames(300, 200, 1), true, FormatGIF},
		{"gif animated", gifFrames(100, 100, 12), true, FormatGIF},
		{"gif too many frames", gifFrames(16, 16, 65), false, FormatGIF},
		{"gif animated too large", gifFrames(600, 100, 2), false, FormatGIF},
		{"gif cut", gifFrames(16, 16, 2)[:20], false, FormatGIF},
		{"svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), false, 0},
		{"jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF"), false, 0},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), false, 0},
		{"html", []byte("<html><svg/></html>"), false, 0},
	} {
		h, err := CheckHeader(c.b)
		if (err == nil) != c.ok || err == nil && h.Format != c.want {
			t.Errorf("%s: %+v, %v", c.name, h, err)
		}
	}
	if h, _ := CheckHeader(gifFrames(100, 100, 12)); h.Frames != 12 {
		t.Errorf("gif frames %d, want 12", h.Frames)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.png"), make([]byte, maxAssetBytes+1), 0o644)
	os.WriteFile(filepath.Join(dir, "ok.png"), pngHeader(4, 4, 0), 0o644)
	os.Mkdir(filepath.Join(dir, "sub.png"), 0o755)
	for _, f := range []string{"big.png", "sub.png", "../ok.png", `..\ok.png`, "C:ok.png", "missing.png"} {
		if _, err := readAsset(dir, f, maxAssetBytes); err == nil {
			t.Errorf("readAsset(%q) read it", f)
		}
	}
	if os.Symlink(filepath.Join(dir, "ok.png"), filepath.Join(dir, "link.png")) == nil {
		if _, err := readAsset(dir, "link.png", maxAssetBytes); err == nil {
			t.Error("readAsset followed a link")
		}
	}
	if _, err := readAsset(dir, "ok.png", maxAssetBytes); err != nil {
		t.Errorf("ok.png: %v", err)
	}
	// A pack's assets folder that is a link does not read either.
	link := filepath.Join(t.TempDir(), "assets")
	if os.Symlink(dir, link) == nil {
		if _, err := readAsset(link, "ok.png", maxAssetBytes); err == nil {
			t.Error("readAsset read through a linked assets folder")
		}
	}
}

// testPNG is a real w×h PNG of one color.
func testPNG(t *testing.T, w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{40, 120, 220, 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// writePack writes a theme pack: theme.json and the files of its assets
// folder.
func writePack(t *testing.T, manifest string, files map[string][]byte) string {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, PackFile), []byte(manifest), 0o644)
	os.Mkdir(filepath.Join(src, AssetsDir), 0o755)
	for name, b := range files {
		os.WriteFile(filepath.Join(src, AssetsDir, name), b, 0o644)
	}
	return src
}

const seaManifest = `{"name":"Sea","assets":{"side":"side.png","bomb":"bomb.png"}}`

// TestInstallThemeAssets: a pack installs its manifest and only the assets
// it names, each checked, to <dir>/<id>.assets; installing again replaces
// the folder; Remove deletes both; a single-file theme installs as before.
// Install and Remove remove an asset folder whose manifest is gone; List
// removes nothing, not a folder of the user's own either.
func TestInstallThemeAssets(t *testing.T) {
	src := writePack(t, seaManifest, map[string][]byte{
		"side.png":  testPNG(t, 64, 96),
		"bomb.png":  pngHeader(1<<16, 1<<16, 0),
		"extra.png": testPNG(t, 4, 4),
	})
	dir := filepath.Join(t.TempDir(), "themes")
	warn, err := Install(src, dir, "sea")
	if err != nil {
		t.Fatal(err)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "assets.bomb") {
		t.Errorf("warnings %q", warn)
	}
	names := func() []string {
		des, _ := os.ReadDir(filepath.Join(dir, "sea.assets"))
		var out []string
		for _, de := range des {
			out = append(out, de.Name())
		}
		return out
	}
	if got := names(); !slices.Equal(got, []string{"side.png"}) {
		t.Errorf("installed assets %q", got)
	}
	if des, _ := os.ReadDir(dir); len(des) != 2 {
		t.Errorf("themes folder holds %d entries, want sea.json and sea", len(des))
	}

	// Again, with one asset less: the folder is the new one.
	os.WriteFile(filepath.Join(src, PackFile), []byte(`{"name":"Sea","assets":{"side":"side.png"}}`), 0o644)
	if _, err := Install(src, dir, "sea"); err != nil {
		t.Fatal(err)
	}
	if got := names(); !slices.Equal(got, []string{"side.png"}) {
		t.Errorf("reinstalled assets %q", got)
	}
	// With none, the folder goes.
	os.WriteFile(filepath.Join(src, PackFile), []byte(`{"name":"Sea"}`), 0o644)
	if _, err := Install(src, dir, "sea"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sea.assets")); !os.IsNotExist(err) {
		t.Errorf("a pack without assets left its folder: %v", err)
	}

	os.WriteFile(filepath.Join(src, PackFile), []byte(seaManifest), 0o644)
	Install(src, dir, "sea")
	if err := Remove(dir, "sea"); err != nil {
		t.Fatal(err)
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("Remove left %d entries", len(des))
	}

	// An older build's Remove leaves the asset folder. List keeps it, and
	// a folder of the user's own; the next Install removes only the first.
	Install(src, dir, "sea")
	os.Remove(filepath.Join(dir, "sea.json"))
	os.Mkdir(filepath.Join(dir, "my-work"), 0o755)
	if got := List(dir); len(got) != 0 {
		t.Errorf("List %+v", got)
	}
	for _, f := range []string{"sea.assets", "my-work"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("List removed %s: %v", f, err)
		}
	}
	os.WriteFile(filepath.Join(src, PackFile), []byte(`{"name":"Other"}`), 0o644)
	if _, err := Install(src, dir, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sea.assets")); !os.IsNotExist(err) {
		t.Errorf("Install left an orphan asset folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-work")); err != nil {
		t.Errorf("Install removed a folder of the user's own: %v", err)
	}
}

// TestThemeNameJSON: a theme name that ends in ".json" is refused, so that
// it can never be, or delete, another theme's manifest: with slate
// installed, Install and Remove of "slate.json" leave slate.json in place.
func TestThemeNameJSON(t *testing.T) {
	src := writePack(t, seaManifest, map[string][]byte{"side.png": testPNG(t, 4, 4)})
	dir := filepath.Join(t.TempDir(), "themes")
	if _, err := Install(src, dir, "slate"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"slate.json", "slate.JSON"} {
		if ValidName(name) {
			t.Errorf("ValidName(%q)", name)
		}
		if _, err := Install(src, dir, name); err == nil {
			t.Errorf("Install %q", name)
		}
		if err := Remove(dir, name); err == nil {
			t.Errorf("Remove %q", name)
		}
	}
	if _, _, err := Load(filepath.Join(dir, "slate.json")); err != nil {
		t.Errorf("slate.json after slate.json's Install and Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "slate.assets", "side.png")); err != nil {
		t.Errorf("slate's assets: %v", err)
	}
}

// TestLoadAssets: the loader decodes the assets that the theme's shaders
// use, each image at its own size; it drops a file that fails its header,
// an SVG, which no shader reads, and a file that is gone, and the rest
// loads. An asset that no shader uses does not load.
func TestLoadAssets(t *testing.T) {
	src := writePack(t, `{"assets":{"bg":"bg.kage","side":"side.png","small":"small.png","bomb":"bomb.png","svgimg":"logo.svg","gone":"gone.png","unused":"side.png"},
"shaders":{"api":1,"background":{"source":"bg","images":["side","small","bomb"]},
"faces":{"sideBar.trackBackground":{"source":"bg","images":["svgimg","gone"]}}}}`, map[string][]byte{
		"bg.kage":   []byte("package main\n"),
		"side.png":  testPNG(t, 64, 96),
		"small.png": testPNG(t, 300, 300),
		"logo.svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"/>`),
		"bomb.png":  pngHeader(1<<16, 1<<16, 0),
	})
	m, _, err := Load(filepath.Join(src, PackFile))
	if err != nil {
		t.Fatal(err)
	}
	art, warn := LoadAssets(filepath.Join(src, AssetsDir), m)
	if w, h := art.Bitmaps["side"].Size(); w != 64 || h != 96 {
		t.Errorf("side %d×%d", w, h)
	}
	if w, _ := art.Bitmaps["small"].Size(); w != 300 {
		t.Errorf("small decoded %d px wide, want 300", w)
	}
	if len(art.Bitmaps) != 2 || art.Bytes != (64*96+300*300)*4 || art.Sources["bg"] != "package main\n" {
		t.Errorf("bitmaps %v, bytes %d, sources %q", art.Bitmaps, art.Bytes, art.Sources)
	}
	if len(warn) != 3 || !strings.Contains(warn[0], "assets.bomb") || !strings.Contains(warn[1], "assets.gone") ||
		!strings.Contains(warn[2], "assets.svgimg") || !strings.Contains(warn[2], "not a PNG or GIF") {
		t.Errorf("warnings %q", warn)
	}
}
