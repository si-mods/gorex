package main

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"image/png"
	"log"
	"sync"

	"github.com/egoist/mygo/ui"
	"gorex/internal/config"
	"gorex/internal/iconpack"
	"gorex/internal/rex"
)

const maxCustomIconCache = 256

var (
	customIconMu      sync.Mutex
	customSVGCache    = map[string]*ui.SVG{}
	customBitmapCache = map[string]ui.ImageSource{}

	iconPackMu      sync.RWMutex
	activeIconPacks *iconpack.Registry
)

func setActiveIconPacks(reg *iconpack.Registry) {
	iconPackMu.Lock()
	activeIconPacks = reg
	iconPackMu.Unlock()
}

func resolveCustomIcon(progName string, args []string, oscIcon *rex.ProgramIcon, dark bool) *rex.ProgramIcon {
	iconPackMu.RLock()
	reg := activeIconPacks
	iconPackMu.RUnlock()
	if reg == nil {
		return oscIcon
	}
	return reg.Resolve(progName, args, oscIcon, dark)
}

func reloadIconPacks(cfg *config.Config) {
	packIDs := prefs.IconPacks
	priority := prefs.IconPackPriority
	var overrides []config.ProgramIconRule
	if cfg != nil {
		if len(cfg.IconPacks) > 0 {
			packIDs = cfg.IconPacks
		}
		priority = cmp.Or(cfg.IconPackPriority, priority)
		overrides = cfg.ProgramIcons
	}
	if len(packIDs) == 0 && len(overrides) == 0 {
		setActiveIconPacks(nil)
		return
	}
	reg, warns := iconpack.LoadRegistry(iconpack.Dir(), config.Dir(), packIDs, overrides, priority)
	for _, w := range warns {
		log.Printf("gorex iconpack: %s", w)
	}
	setActiveIconPacks(reg)
}

// programWithIcon applies a program's custom OSC 7502 icon onto base.
func programWithIcon(base program, ic *rex.ProgramIcon) program {
	if ic == nil {
		return base
	}
	base.Icon = ic
	base.Shell = false
	if ic.Bg != "" {
		base.TileBg = ui.Hex(ic.Bg)
		base.TileBgGradient = nil
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
	customIconMu.Lock()
	defer customIconMu.Unlock()
	if s, ok := customSVGCache[key]; ok {
		return s
	}
	if len(customSVGCache) >= maxCustomIconCache {
		clear(customSVGCache)
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
	customSVGCache[key] = s
	return s
}

func customIconBitmap(ic *rex.ProgramIcon) ui.ImageSource {
	if ic == nil || ic.Format != "png" {
		return nil
	}
	key := programIconKey(ic)
	customIconMu.Lock()
	defer customIconMu.Unlock()
	if src, ok := customBitmapCache[key]; ok {
		return src
	}
	if len(customBitmapCache) >= maxCustomIconCache {
		clear(customBitmapCache)
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
