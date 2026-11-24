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

// TestParseAmbience: the DSK-010 keys of phase 1 parse; a value past a cap
// clamps, an entry past a count cap drops, a bad color drops and an
// unknown asset name drops, each with a warning
// (docs/DSK-010-theme-ambience.md, Tests).
func TestParseAmbience(t *testing.T) {
	m, warn, err := Parse([]byte(`{
  "name": "Sea",
  "assets": {
    "side": "side.png",
    "logo": "logo.svg",
    "wave": { "file": "wave.png", "frames": 8, "columns": 4, "fps": 10 },
    "bad/name": "x.png",
    "up": "../up.png"
  },
  "ambience": {
    "glows": [
      { "x": 0.88, "y": -0.08, "rx": 0.55, "ry": 0.5, "color": { "light": "#3b62f64d", "dark": "#6c8cff33" } },
      { "x": 0.5, "y": 0.5, "rx": 9, "ry": 0.3, "color": "#ffffffff" },
      { "x": 0.1, "color": "blue" },
      { "color": "#11223344" }, { "color": "#11223344" }, { "color": "#11223344" }
    ],
    "images": [
      { "asset": "side", "anchor": "bottomRight", "height": 0.74, "opacity": 0.5 },
      { "asset": "nope" },
      { "asset": "wave" },
      { "asset": "side", "anchor": "middle" },
      { "asset": "side", "anchor": "topLeft", "height": 40 },
      { "asset": "side" }
    ],
    "particles": []
  },
  "brand": { "icon": "logo", "wordmark": "Sea‮ Whale\u0007 with a long long long name" },
  "mascot": {}
}`))
	if err != nil {
		t.Fatal(err)
	}
	wantAssets := map[string]Asset{
		"side": {File: "side.png"},
		"logo": {File: "logo.svg"},
		"wave": {File: "wave.png", Frames: 8, Columns: 4, FPS: 10},
	}
	if !reflect.DeepEqual(m.Assets, wantAssets) {
		t.Errorf("assets %+v", m.Assets)
	}
	wantGlows := []Glow{
		{X: 0.88, Y: -0.08, RX: 0.55, RY: 0.5, Color: [2]ui.Color{ui.Hex("#3b62f64d"), ui.Hex("#6c8cff33")}},
		{X: 0.5, Y: 0.5, RX: 1.5, RY: 0.3, Color: [2]ui.Color{ui.Hex("#ffffff66"), ui.Hex("#ffffff66")}},
		{X: 0.5, Y: 0.5, RX: 0.5, RY: 0.5, Color: [2]ui.Color{ui.Hex("#11223344"), ui.Hex("#11223344")}},
		{X: 0.5, Y: 0.5, RX: 0.5, RY: 0.5, Color: [2]ui.Color{ui.Hex("#11223344"), ui.Hex("#11223344")}},
	}
	if !reflect.DeepEqual(m.Ambience.Glows, wantGlows) {
		t.Errorf("glows\n%+v\nwant\n%+v", m.Ambience.Glows, wantGlows)
	}
	if want := []Still{{Asset: "side", Anchor: BottomRight, Height: 0.74, Opacity: 0.25}, {Asset: "side", Anchor: TopLeft, Height: 40, Opacity: 0.1}}; !reflect.DeepEqual(m.Ambience.Images, want) {
		t.Errorf("images %+v", m.Ambience.Images)
	}
	if m.Brand != (Brand{Icon: "logo", Wordmark: "Sea Whale with a long lo"}) {
		t.Errorf("brand %+v", m.Brand)
	}
	want := []string{
		`ambience.glows[1].color: alpha clamped to 0.4`,
		`ambience.glows[1].rx: 9 clamped to 0.05–1.5`,
		`ambience.glows[2].color: bad color`,
		`ambience.glows[5]: more than 4 glows`,
		`ambience.images[0].opacity: 0.5 clamped to 0–0.25`,
		`ambience.images[1].asset: no image asset "nope"`,
		`ambience.images[2].asset: no image asset "wave"`,
		`ambience.images[3].anchor: unknown anchor`,
		`ambience.images[5]: more than 2 images`,
		`ambience.particles: not drawn by this build`,
		`assets.up: bad file name "../up.png"`,
		`assets: bad name "bad/name"`,
		`brand.wordmark: control characters removed or cut to 24 characters`,
		`unknown key "mascot"`,
	}
	if !slices.Equal(warn, want) {
		t.Errorf("warnings\n%q\nwant\n%q", warn, want)
	}

	// A manifest of DSK-008 keys only has none of DSK-010's.
	m, warn, err = Parse([]byte(`{"name":"slate","seeds":{"light":{"accent":"#0a6bff"}}}`))
	if err != nil || len(warn) != 0 || m.Assets != nil || !m.Ambience.Empty() || m.Brand != (Brand{}) {
		t.Errorf("a DSK-008 manifest: %+v, %q, %v", m, warn, err)
	}
}

// TestParseText: a wordmark loses control characters and bidirectional
// controls, and keeps its first 24 characters, counted as characters.
func TestParseText(t *testing.T) {
	for in, want := range map[string]string{
		"蓝色大肥鱼":                 "蓝色大肥鱼",
		"a‮b⁦c‏d؜e":             "abcde",
		"tab\there\nnew":        "tabherenew",
		strings.Repeat("鱼", 30): strings.Repeat("鱼", 24),
	} {
		got, _ := cleanText(in, maxWordmark)
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
		{"svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), true, FormatSVG},
		{"jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF"), false, 0},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), false, 0},
		{"html", []byte("<html><svg/></html>"), true, FormatSVG}, // ParseSVG refuses it at load
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
		if _, err := readAsset(dir, f); err == nil {
			t.Errorf("readAsset(%q) read it", f)
		}
	}
	if os.Symlink(filepath.Join(dir, "ok.png"), filepath.Join(dir, "link.png")) == nil {
		if _, err := readAsset(dir, "link.png"); err == nil {
			t.Error("readAsset followed a link")
		}
	}
	if _, err := readAsset(dir, "ok.png"); err != nil {
		t.Errorf("ok.png: %v", err)
	}
	// A pack's assets folder that is a link does not read either.
	link := filepath.Join(t.TempDir(), "assets")
	if os.Symlink(dir, link) == nil {
		if _, err := readAsset(link, "ok.png"); err == nil {
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

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" fill="currentColor"/></svg>`

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

const seaManifest = `{"name":"Sea","assets":{"side":"side.png","logo":"logo.svg","bomb":"bomb.png"},
"ambience":{"images":[{"asset":"side","height":0.7,"opacity":0.1},{"asset":"bomb","height":40}]},
"brand":{"icon":"logo","wordmark":"Sea"}}`

// TestInstallThemeAssets: a pack installs its manifest and only the assets
// it names, each checked, to <dir>/<id>.assets; installing again replaces
// the folder; Remove deletes both; a single-file theme installs as before.
// Install and Remove remove an asset folder whose manifest is gone; List
// removes nothing, not a folder of the user's own either.
func TestInstallThemeAssets(t *testing.T) {
	src := writePack(t, seaManifest, map[string][]byte{
		"side.png":  testPNG(t, 64, 96),
		"logo.svg":  []byte(testSVG),
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
	if got := names(); !slices.Equal(got, []string{"logo.svg", "side.png"}) {
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
	src := writePack(t, seaManifest, map[string][]byte{"side.png": testPNG(t, 4, 4), "logo.svg": []byte(testSVG)})
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

// TestLoadAssets: the loader decodes the assets in use, a still image of a
// DIP height to at most twice that size times the scale, an SVG as the
// brand icon; it drops a file that fails its header and an SVG that is not
// the icon, and the rest loads.
func TestLoadAssets(t *testing.T) {
	src := writePack(t, `{"assets":{"side":"side.png","logo":"logo.svg","small":"small.png","bomb":"bomb.png","svgimg":"logo.svg","gone":"gone.png"},
"ambience":{"images":[{"asset":"side","height":0.7},{"asset":"small","height":20}]},
"brand":{"icon":"logo"}}`, map[string][]byte{
		"side.png":  testPNG(t, 64, 96),
		"small.png": testPNG(t, 300, 300),
		"logo.svg":  []byte(testSVG),
		"bomb.png":  pngHeader(1<<16, 1<<16, 0),
	})
	m, _, err := Load(filepath.Join(src, PackFile))
	if err != nil {
		t.Fatal(err)
	}
	art, warn := LoadAssets(filepath.Join(src, AssetsDir), m, 2)
	if len(warn) != 0 {
		t.Errorf("warnings %q", warn)
	}
	if w, h := art.Bitmaps["side"].Size(); w != 64 || h != 96 {
		t.Errorf("side %d×%d", w, h)
	}
	if w, _ := art.Bitmaps["small"].Size(); w != 2*20*2 {
		t.Errorf("small decoded %d px wide, want %d", w, 2*20*2)
	}
	if art.Icon == nil || art.Bytes != (64*96+80*80)*4 {
		t.Errorf("icon %v, bytes %d", art.Icon, art.Bytes)
	}

	m.Ambience.Images = append(m.Ambience.Images, Still{Asset: "bomb", Height: 0.5}, Still{Asset: "svgimg", Height: 0.5}, Still{Asset: "gone", Height: 0.5})
	art, warn = LoadAssets(filepath.Join(src, AssetsDir), m, 2)
	if len(warn) != 3 || len(art.Bitmaps) != 2 || art.Icon == nil {
		t.Errorf("bitmaps %d, icon %v, warnings %q", len(art.Bitmaps), art.Icon, warn)
	}
}
