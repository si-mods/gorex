package rex

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image/png"
	"regexp"
	"strings"
	"unicode/utf8"
)

// iconReply answers a program asking whether the terminal supports OSC 7502.
var iconReply = []byte("\x1b]7502;?\x1b\\")

// Limits of OSC 7502: a sequence or decoded icon past one is dropped whole.
const (
	maxIconSeq   = 48 * 1024
	maxIconBytes = 32 * 1024
	maxIconDim   = 512
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

var svgEventAttrRe = regexp.MustCompile(`(?i)\bon[a-z]+\s*=`)

// programIcon reads a session's output for OSC 7502 icon sequences and keeps
// the current icon for the foreground program.
type programIcon struct {
	carry   []byte
	icon    *ProgramIcon
	updates uint64
}

// feed reads output for icon reports, queries, prompts and resets, and returns
// how many times the program asked whether the terminal supports OSC 7502.
func (ic *programIcon) feed(p []byte) (queries int) {
	if len(ic.carry) > 0 {
		p = append(ic.carry, p...)
		ic.carry = nil
	}
	for {
		i := bytes.IndexByte(p, 0x1b)
		if i < 0 {
			return
		}
		p = p[i:]
		if len(p) < 2 {
			ic.carry = append([]byte(nil), p...)
			return
		}
		switch p[1] {
		case 'c': // RIS
			ic.drop()
			p = p[2:]
			continue
		case ']':
		default:
			p = p[1:]
			continue
		}
		body, n := oscBody(p[2:])
		if n < 0 {
			if len(p) <= maxIconSeq && (mayBe(p[2:], "7502;") || mayBe(p[2:], "133;")) {
				ic.carry = append([]byte(nil), p...)
			}
			return
		}
		p = p[2+n:]
		switch {
		case bytes.HasPrefix(body, []byte("133;A")) && (len(body) == 5 || body[5] == ';'):
			ic.drop()
		case !bytes.HasPrefix(body, []byte("7502;")) || 2+n > maxIconSeq:
		case strings.TrimSpace(string(body[5:])) == "?":
			queries++
		default:
			ic.apply(string(body[5:]))
		}
	}
}

// drop clears the active program icon, if one is set.
func (ic *programIcon) drop() {
	if ic.icon != nil {
		ic.icon = nil
		ic.updates++
	}
}

// get returns a copy of the active program icon, or nil when none is set.
func (ic *programIcon) get() *ProgramIcon {
	if ic.icon == nil {
		return nil
	}
	cp := *ic.icon
	return &cp
}

// restore sets the icon handed over during an in-place upgrade.
func (ic *programIcon) restore(pi *ProgramIcon) {
	if pi == nil {
		ic.icon = nil
		return
	}
	cp := *pi
	ic.icon = &cp
}

func (ic *programIcon) apply(body string) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "clear" {
		ic.drop()
		return
	}
	var format, mode, bg, fg, dataArg string
	for part := range strings.SplitSeq(body, ":") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return
		}
		switch k {
		case "state":
			if v == "clear" {
				ic.drop()
				return
			}
		case "format":
			format = strings.ToLower(v)
		case "mode":
			mode = strings.ToLower(v)
		case "bg":
			c, ok := parseHexColor(v)
			if !ok {
				return
			}
			bg = c
		case "fg":
			c, ok := parseHexColor(v)
			if !ok {
				return
			}
			fg = c
		case "data":
			dataArg = v
		}
	}
	if dataArg == "" {
		return
	}
	raw, ok := decodeBase64Bytes(dataArg)
	if !ok {
		return
	}
	next, ok := BuildProgramIcon(format, mode, bg, fg, raw)
	if !ok {
		return
	}
	if programIconEqual(ic.icon, next) {
		return
	}
	ic.icon = next
	ic.updates++
}

// BuildProgramIcon validates format ("svg" or "png"), mode ("mono" or "color"),
// optional hex colors, and raw icon bytes (up to maxIconBytes), returning a
// normalized ProgramIcon ready for caching and wire transport.
func BuildProgramIcon(format, mode, bg, fg string, raw []byte) (*ProgramIcon, bool) {
	format = strings.ToLower(strings.TrimSpace(format))
	mode = strings.ToLower(strings.TrimSpace(mode))
	if (format != "svg" && format != "png") || len(raw) == 0 || len(raw) > maxIconBytes {
		return nil, false
	}
	if bg != "" {
		c, ok := parseHexColor(bg)
		if !ok {
			return nil, false
		}
		bg = c
	}
	if fg != "" {
		c, ok := parseHexColor(fg)
		if !ok {
			return nil, false
		}
		fg = c
	}
	switch format {
	case "svg":
		if !validateSVG(raw) {
			return nil, false
		}
		if mode == "" {
			mode = "mono"
		}
		if mode != "mono" && mode != "color" {
			return nil, false
		}
	case "png":
		if !validatePNG(raw) {
			return nil, false
		}
		if mode == "" {
			mode = "color"
		}
		if mode != "color" {
			return nil, false
		}
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(format + "\x00" + mode + "\x00" + bg + "\x00" + fg + "\x00" + encoded))
	return &ProgramIcon{
		Format: format,
		Mode:   mode,
		Data:   encoded,
		Bg:     bg,
		Fg:     fg,
		Hash:   hex.EncodeToString(sum[:8]),
	}, true
}

func programIconEqual(a, b *ProgramIcon) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func decodeBase64Bytes(s string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

func validatePNG(raw []byte) bool {
	if !bytes.HasPrefix(raw, pngSignature) {
		return false
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	return cfg.Width >= 1 && cfg.Width <= maxIconDim && cfg.Height >= 1 && cfg.Height <= maxIconDim
}

func validateSVG(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	lower := strings.ToLower(string(raw))
	if !strings.Contains(lower, "<svg") {
		return false
	}
	if strings.Contains(lower, "<script") || strings.Contains(lower, "javascript:") || svgEventAttrRe.MatchString(lower) {
		return false
	}
	return true
}

func parseHexColor(s string) (string, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return "", false
	}
	for i := 0; i < 6; i++ {
		c := s[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return "", false
		}
	}
	return "#" + strings.ToLower(s), true
}
