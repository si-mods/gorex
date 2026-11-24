package theme

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"
)

// AssetsDir is the folder of a pack's assets, beside its theme.json.
const AssetsDir = "assets"

// The caps of an asset file (docs/DSK-010-theme-ambience.md, Caps).
const (
	maxAssetBytes   = 1 << 20  // one file
	maxPackBytes    = 4 << 20  // all files of a theme
	maxStillSide    = 1024     // a still image or a sprite sheet, in pixels
	maxAnimSide     = 512      // an animated image, in pixels
	maxAnimFrames   = 64       // frames of an animated image
	maxAnimDecoded  = 16 << 20 // bytes of an animated image's frames
	maxThemeDecoded = 48 << 20 // bytes of a theme's decoded images
)

// Format is the kind of an asset file, read from its first bytes.
type Format int

// The formats an asset may have.
const (
	FormatPNG Format = iota + 1
	FormatAPNG
	FormatGIF
	FormatSVG
)

func (f Format) String() string {
	return [...]string{"unknown", "PNG", "APNG", "GIF", "SVG"}[f]
}

// Header is what CheckHeader reads of an asset without decoding it.
type Header struct {
	Format        Format
	Width, Height int
	Frames        int // 1 for a still image
}

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// CheckHeader reads the format, the size and the frames of an image from
// its header, and checks them against the caps, so that a decode never
// meets a picture too large or too long: a decompression bomb fails here.
// It reads the chunks of a PNG up to its image data and the blocks of a
// GIF, but decodes no pixels.
func CheckHeader(b []byte) (Header, error) {
	var h Header
	switch {
	case bytes.HasPrefix(b, pngMagic):
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			return h, err
		}
		h = Header{Format: FormatPNG, Width: cfg.Width, Height: cfg.Height, Frames: 1}
		if n, ok := apngFrames(b); ok {
			h.Format, h.Frames = FormatAPNG, n
		}
	case bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a")):
		var err error
		if h, err = gifHeader(b); err != nil {
			return h, err
		}
	case isSVG(b):
		return Header{Format: FormatSVG, Frames: 1}, nil
	default:
		return h, errors.New("not a PNG, GIF or SVG file")
	}
	if h.Width <= 0 || h.Height <= 0 {
		return h, errors.New("empty image")
	}
	if h.Frames > 1 {
		switch {
		case h.Frames > maxAnimFrames:
			return h, fmt.Errorf("%d frames, more than %d", h.Frames, maxAnimFrames)
		case h.Width > maxAnimSide || h.Height > maxAnimSide:
			return h, fmt.Errorf("animated image of %d×%d px, larger than %d px", h.Width, h.Height, maxAnimSide)
		case h.Width*h.Height*4*h.Frames > maxAnimDecoded:
			return h, fmt.Errorf("animated image of more than %d MiB decoded", maxAnimDecoded>>20)
		}
	} else if h.Width > maxStillSide || h.Height > maxStillSide {
		return h, fmt.Errorf("image of %d×%d px, larger than %d px", h.Width, h.Height, maxStillSide)
	}
	return h, nil
}

// apngFrames returns the frame count of a PNG's acTL chunk, which an APNG
// has before its image data.
func apngFrames(b []byte) (int, bool) {
	for i := len(pngMagic); i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		if typ == "IDAT" || n < 0 || n > len(b) {
			return 0, false
		}
		if typ == "acTL" && n >= 8 && i+16 <= len(b) {
			return int(binary.BigEndian.Uint32(b[i+8:])), true
		}
		i += 12 + n
	}
	return 0, false
}

// gifHeader reads a GIF's logical screen and counts its images, walking
// its blocks without decoding them.
func gifHeader(b []byte) (Header, error) {
	h := Header{Format: FormatGIF}
	bad := errors.New("bad GIF")
	if len(b) < 13 {
		return h, bad
	}
	h.Width, h.Height = int(binary.LittleEndian.Uint16(b[6:])), int(binary.LittleEndian.Uint16(b[8:]))
	i := 13
	if flags := b[10]; flags&0x80 != 0 {
		i += 3 << (flags&7 + 1)
	}
	// skip passes the sub-blocks at i, which end with an empty one.
	skip := func() bool {
		for i < len(b) {
			n := int(b[i])
			i += 1 + n
			if n == 0 {
				return true
			}
		}
		return false
	}
	for i < len(b) {
		switch b[i] {
		case 0x3b: // trailer
			if h.Frames == 0 {
				return h, bad
			}
			return h, nil
		case 0x21: // extension
			i += 2
			if !skip() {
				return h, bad
			}
		case 0x2c: // image
			if i+10 > len(b) {
				return h, bad
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 {
				i += 3 << (flags&7 + 1)
			}
			i++ // the LZW code size
			if !skip() {
				return h, bad
			}
			if h.Frames++; h.Frames > maxAnimFrames {
				return h, nil // CheckHeader refuses it, unread to the end
			}
		default:
			return h, bad
		}
	}
	if h.Frames == 0 {
		return h, bad
	}
	return h, nil // a GIF cut short after whole images, as decoders take it
}

// isSVG reports whether b starts as an SVG document: an <svg element,
// after at most an XML declaration, comments and white space.
func isSVG(b []byte) bool {
	head := b[:min(len(b), 1024)]
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	i := bytes.Index(head, []byte("<svg"))
	return i >= 0 && bytes.IndexByte(bytes.TrimSpace(head), '<') == 0
}

// readAsset reads the file of an asset in dir: a regular file, not a link,
// of at most the byte cap, in a folder that is not a link either.
func readAsset(dir, file string) ([]byte, error) {
	if !validAssetFile(file) {
		return nil, fmt.Errorf("bad file name %q", file)
	}
	if st, err := os.Lstat(dir); err != nil {
		return nil, err
	} else if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", filepath.Base(dir))
	}
	p := filepath.Join(dir, file)
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", file)
	}
	if st.Size() > maxAssetBytes {
		return nil, fmt.Errorf("%s is larger than %d KiB", file, maxAssetBytes>>10)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxAssetBytes+1))
	if err == nil && len(b) > maxAssetBytes {
		err = fmt.Errorf("%s is larger than %d KiB", file, maxAssetBytes>>10)
	}
	return b, err
}

// assetUse is how a manifest uses an asset: as a still image of the
// background, or as the brand's icon, the one use an SVG may have.
type assetUse struct {
	still, icon bool
	shown       float32 // the largest size it shows at, in DIPs; 0 for a fraction of the window
}

// uses returns how m uses each of its assets.
func (m *Manifest) uses() map[string]assetUse {
	out := map[string]assetUse{}
	for _, s := range m.Ambience.Images {
		u := out[s.Asset]
		u.still = true
		if s.Height > 1 && u.shown >= 0 {
			u.shown = max(u.shown, s.Height)
		} else {
			u.shown = -1 // a fraction of the window: no bound
		}
		out[s.Asset] = u
	}
	if m.Brand.Icon != "" {
		u := out[m.Brand.Icon]
		u.icon = true
		out[m.Brand.Icon] = u
	}
	return out
}

// BrandIconSize is the brand row's icon, in DIPs.
const BrandIconSize = 20

// Art is a theme's decoded assets: bitmaps, and an SVG for the brand.
type Art struct {
	Bitmaps map[string]*ui.Bitmap
	Icon    *ui.SVG // the brand's icon when it is an SVG
	Bytes   int     // the bitmaps' decoded size
}

// LoadAssets decodes the assets that m uses from dir, the theme's assets
// folder, for a screen of the given scale. It reads each file with the
// byte cap, checks its header before it decodes it, and decodes a still
// image to at most twice its largest shown size. A decode that panics is
// recovered. It drops each asset that fails, with a warning; the rest of
// the theme draws. Call it off the UI thread.
func LoadAssets(dir string, m *Manifest, scale float32) (*Art, []string) {
	art := &Art{Bitmaps: map[string]*ui.Bitmap{}}
	var warn []string
	total := 0
	uses := m.uses()
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range uses {
			if !yield(k) {
				return
			}
		}
	}) {
		u, a := uses[name], m.Assets[name]
		drop := func(err error) { warn = append(warn, fmt.Sprintf("assets.%s: %v (dropped)", name, err)) }
		b, err := readAsset(dir, a.File)
		if err == nil && total+len(b) > maxPackBytes {
			err = fmt.Errorf("the theme's files pass %d MiB", maxPackBytes>>20)
		}
		if err != nil {
			drop(err)
			continue
		}
		total += len(b)
		h, err := CheckHeader(b)
		if err != nil {
			drop(err)
			continue
		}
		if h.Format == FormatSVG {
			if u.still {
				drop(errors.New("an SVG is only for brand.icon"))
				continue
			}
			if art.Icon, err = parseSVG(b); err != nil {
				drop(err)
			}
			continue
		}
		side := 0 // native, at most maxStillSide
		switch {
		case u.still && u.shown < 0:
		case u.still:
			side = int(2 * max(u.shown, BrandIconSize) * scale)
		default:
			side = int(2 * BrandIconSize * scale)
		}
		bm, err := decodeBitmap(b, side)
		if err == nil {
			w, h := bm.Size()
			if art.Bytes+w*h*4 > maxThemeDecoded {
				err = fmt.Errorf("the theme's images pass %d MiB decoded", maxThemeDecoded>>20)
			} else {
				art.Bytes += w * h * 4
			}
		}
		if err != nil {
			drop(err)
			continue
		}
		art.Bitmaps[name] = bm
	}
	return art, warn
}

// decodeBitmap decodes b to at most side pixels a side, 0 for its own
// size, and turns a panic of the decoder into an error.
func decodeBitmap(b []byte, side int) (bm *ui.Bitmap, err error) {
	defer func() {
		if r := recover(); r != nil {
			bm, err = nil, fmt.Errorf("decode: %v", r)
		}
	}()
	return ui.DecodeBitmapScaled(b, side)
}

// parseSVG parses an SVG icon, and turns a panic of the parser into an
// error.
func parseSVG(b []byte) (s *ui.SVG, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, fmt.Errorf("svg: %v", r)
		}
	}()
	return ui.ParseSVG(b)
}

// AssetFolder is the folder of the installed theme name's assets:
// <dir>/<name>.assets.
func AssetFolder(dir, name string) (string, error) {
	p, err := Path(dir, name)
	if err != nil {
		return "", err
	}
	return assetFolder(p), nil
}

// copyAssets copies each asset that m names from the pack's assets folder
// into a new folder in dir, and returns it: only regular files under the
// byte caps, in a format the header check takes. A file that fails is
// dropped with a warning.
func copyAssets(srcDir, dir string, m *Manifest) (string, []string, error) {
	tmp, err := os.MkdirTemp(dir, ".install-*")
	if err != nil {
		return "", nil, err
	}
	var warn []string
	total := 0
	copied := map[string]bool{}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range m.Assets {
			if !yield(k) {
				return
			}
		}
	}) {
		file := m.Assets[name].File
		if copied[file] {
			continue
		}
		b, err := readAsset(filepath.Join(srcDir, AssetsDir), file)
		if err == nil && total+len(b) > maxPackBytes {
			err = fmt.Errorf("the theme's files pass %d MiB", maxPackBytes>>20)
		}
		if err == nil {
			_, err = CheckHeader(b)
		}
		if err != nil {
			warn = append(warn, fmt.Sprintf("assets.%s: %v (dropped)", name, err))
			continue
		}
		total += len(b)
		if err := os.WriteFile(filepath.Join(tmp, file), b, 0o644); err != nil {
			os.RemoveAll(tmp)
			return "", nil, err
		}
		copied[file] = true
	}
	return tmp, warn, nil
}

// replaceDir puts the folder tmp in the place of dst, and removes the old
// one: a rename does not replace a folder on Windows.
func replaceDir(tmp, dst string) error {
	old := ""
	if _, err := os.Lstat(dst); err == nil {
		old = tmp + "-old"
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if old != "" {
			os.Rename(old, dst)
		}
		return err
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return nil
}
