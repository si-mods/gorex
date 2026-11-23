package main

import (
	"bytes"
	"encoding/base64"
	"image/png"

	"github.com/egoist/mygo/ui"
	"gorex/internal/rex"
)

const maxCustomIconCache = 256

var (
	customSVGCache    = map[string]*ui.SVG{}
	customBitmapCache = map[string]ui.ImageSource{}
)

// programWithIcon applies a program's custom OSC 7502 icon onto base.
func programWithIcon(base program, ic *rex.ProgramIcon) program {
	if ic == nil {
		return base
	}
	base.Icon = ic
	base.Shell = false
	if ic.Bg != "" {
		base.TileBg = ui.Hex(ic.Bg)
		base.TileBorder = isLightColor(base.TileBg)
	}
	if ic.Fg != "" {
		base.TileFg = ui.Hex(ic.Fg)
	} else if base.TileFg == (ui.Color{}) {
		base.TileFg = ui.Hex("#ffffff")
	}
	return base
}

func isLightColor(c ui.Color) bool {
	luma := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
	return luma > 210
}

func programIconKey(ic *rex.ProgramIcon) string {
	if ic == nil {
		return ""
	}
	if ic.Hash != "" {
		return ic.Hash
	}
	return ic.Format + ":" + ic.Mode + ":" + ic.Data
}

func customIconSVG(ic *rex.ProgramIcon) *ui.SVG {
	if ic == nil || ic.Format != "svg" {
		return nil
	}
	key := programIconKey(ic)
	if s, ok := customSVGCache[key]; ok {
		return s
	}
	raw, err := base64.StdEncoding.DecodeString(ic.Data)
	if err != nil {
		customSVGCache[key] = nil
		return nil
	}
	s, err := ui.ParseSVG(raw)
	if err != nil {
		customSVGCache[key] = nil
		return nil
	}
	if len(customSVGCache) >= maxCustomIconCache {
		clear(customSVGCache)
	}
	customSVGCache[key] = s
	return s
}

func customIconBitmap(ic *rex.ProgramIcon) ui.ImageSource {
	if ic == nil || ic.Format != "png" {
		return nil
	}
	key := programIconKey(ic)
	if src, ok := customBitmapCache[key]; ok {
		return src
	}
	raw, err := base64.StdEncoding.DecodeString(ic.Data)
	if err != nil {
		customBitmapCache[key] = nil
		return nil
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		customBitmapCache[key] = nil
		return nil
	}
	src := ui.NewBitmap(img)
	if len(customBitmapCache) >= maxCustomIconCache {
		clear(customBitmapCache)
	}
	customBitmapCache[key] = src
	return src
}

// programIconEl draws ic when present and valid, else falls back to glyphIcon.
func programIconEl(c *ui.Context, ic *rex.ProgramIcon, fallback string, size float32, col ui.Color) ui.Element {
	if ic != nil {
		switch ic.Format {
		case "svg":
			if s := customIconSVG(ic); s != nil {
				return ui.Icon(c, s).Size(size, size).TextColor(col)
			}
		case "png":
			if src := customIconBitmap(ic); src != nil {
				return ui.Image(c, src).Size(size, size)
			}
		}
	}
	return glyphIcon(c, fallback, size, col)
}

// programIconIn draws ic in r when present and valid, else falls back to glyphIn.
func programIconIn(p *ui.Painter, ic *rex.ProgramIcon, fallback string, r ui.Rect, col ui.Color) {
	if ic != nil {
		switch ic.Format {
		case "svg":
			if s := customIconSVG(ic); s != nil {
				p.Icon(s, r, col)
				return
			}
		case "png":
			if src := customIconBitmap(ic); src != nil {
				p.Image(src, r, ui.Contain)
				return
			}
		}
	}
	glyphIn(p, fallback, r, col)
}
