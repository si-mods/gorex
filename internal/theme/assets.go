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
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// AssetsDir is the folder of a pack's assets, beside its theme.json.
const AssetsDir = "assets"

// The caps of an asset file (docs/DSK-010-theme-ambience.md, Caps, and
// docs/DSK-012-theme-shaders.md, Assets).
const (
	maxAssetBytes   = 1 << 20  // one file that no shader reads
	maxPackBytes    = 8 << 20  // all files of a theme
	maxStillSide    = 1024     // an image that no shader reads, in pixels
	maxKageBytes    = 16 << 10 // a shader's source
	maxShaderBytes  = 2 << 20  // an image that only shaders read
	maxShaderSide   = 2048     // the same, in pixels
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
	FormatKage // a shader's source, by its .kage name
)

func (f Format) String() string {
	return [...]string{"unknown", "PNG", "APNG", "GIF", "Kage"}[f]
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
func CheckHeader(b []byte) (Header, error) { return checkHeader(b, maxStillSide) }

// checkHeader is CheckHeader with a still image of at most maxSide pixels
// a side.
func checkHeader(b []byte, maxSide int) (Header, error) {
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
	default:
		return h, errors.New("not a PNG or GIF file")
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
	} else if h.Width > maxSide || h.Height > maxSide {
		return h, fmt.Errorf("image of %d×%d px, larger than %d px", h.Width, h.Height, maxSide)
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

// readAsset reads the file of an asset in dir: a regular file, not a link,
// of at most limit bytes, in a folder that is not a link either.
func readAsset(dir, file string, limit int) ([]byte, error) {
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
	if st.Size() > int64(limit) {
		return nil, fmt.Errorf("%s is larger than %d KiB", file, limit>>10)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err == nil && len(b) > limit {
		err = fmt.Errorf("%s is larger than %d KiB", file, limit>>10)
	}
	return b, err
}

// assetCaps returns the byte cap of an asset's file and the side cap of
// its pixels, by how the manifest uses it: a shader's source, an image
// that theme shaders read, or a file that nothing reads.
func assetCaps(u assetUse, file string) (bytes, side int) {
	switch {
	case IsKage(file):
		return maxKageBytes, 0
	case u.shader:
		return maxShaderBytes, maxShaderSide
	}
	return maxAssetBytes, maxStillSide
}

// checkKage checks a shader's source: UTF-8 text with no NUL. The
// compiler checks the rest at load.
func checkKage(b []byte) error {
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return errors.New("a .kage file is not UTF-8 text")
	}
	return nil
}

// assetUse is how a manifest uses an asset (docs/DSK-012-theme-shaders.md):
// shader tells that a theme shader reads the image, kage that the asset is
// a shader's source.
type assetUse struct {
	shader, kage bool
}

// uses returns how m uses each of its assets.
func (m *Manifest) uses() map[string]assetUse {
	out := map[string]assetUse{}
	for _, slot := range m.Shaders.slots() {
		u := out[slot.Source]
		u.kage = true
		out[slot.Source] = u
		for _, im := range slot.Images {
			u := out[im]
			u.shader = true
			out[im] = u
		}
	}
	return out
}

// slots returns the slots of s, none for a nil s.
func (s *Shaders) slots() []*ShaderSlot {
	if s == nil {
		return nil
	}
	out := []*ShaderSlot{}
	if s.Background != nil {
		out = append(out, s.Background)
	}
	for _, r := range FaceRoles {
		if f := s.Faces[r]; f != nil {
			out = append(out, f)
		}
	}
	return out
}

// Art is a theme's decoded assets: the bitmaps and the sources of its
// shaders.
type Art struct {
	Bitmaps map[string]*ui.Bitmap
	Sources map[string]string // the .kage assets, by name
	Bytes   int               // the bitmaps' decoded size
}

// LoadAssets decodes the assets that m's shaders use from dir, the
// theme's assets folder: the sources, and each image at its own size. It
// reads each file with the byte cap and checks its header before it
// decodes it. A decode that panics is recovered. It drops each asset that
// fails, with a warning; the rest of the theme draws. Call it off the UI
// thread.
func LoadAssets(dir string, m *Manifest) (*Art, []string) {
	art := &Art{Bitmaps: map[string]*ui.Bitmap{}, Sources: map[string]string{}}
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
		limit, maxSide := assetCaps(u, a.File)
		b, err := readAsset(dir, a.File, limit)
		if err == nil && total+len(b) > maxPackBytes {
			err = fmt.Errorf("the theme's files pass %d MiB", maxPackBytes>>20)
		}
		if err != nil {
			drop(err)
			continue
		}
		total += len(b)
		if IsKage(a.File) {
			if !u.kage {
				drop(errors.New("a .kage file is a shader's source"))
			} else if err := checkKage(b); err != nil {
				drop(err)
			} else {
				art.Sources[name] = string(b)
			}
			continue
		}
		if _, err := checkHeader(b, maxSide); err != nil {
			drop(err)
			continue
		}
		bm, err := decodeBitmap(b)
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

// decodeBitmap decodes b at its own size, and turns a panic of the
// decoder into an error.
func decodeBitmap(b []byte) (bm *ui.Bitmap, err error) {
	defer func() {
		if r := recover(); r != nil {
			bm, err = nil, fmt.Errorf("decode: %v", r)
		}
	}()
	return ui.DecodeBitmapScaled(b, 0)
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
	uses := m.uses()
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
		limit, maxSide := assetCaps(uses[name], file)
		b, err := readAsset(filepath.Join(srcDir, AssetsDir), file, limit)
		if err == nil && total+len(b) > maxPackBytes {
			err = fmt.Errorf("the theme's files pass %d MiB", maxPackBytes>>20)
		}
		if err == nil && IsKage(file) {
			err = checkKage(b)
		} else if err == nil {
			_, err = checkHeader(b, maxSide)
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
