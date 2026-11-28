package rex

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type keyMods uint8

const (
	keyModShift keyMods = 1 << iota
	keyModAlt
	keyModCtrl
)

// IsSendKeysAction reports whether id names the built-in key-sending action.
func IsSendKeysAction(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "send-keys", "send-key", "send_keys", "send_key":
		return true
	}
	return false
}

// EncodeKeySequence encodes one or more key chord or sequence specifications
// (such as "alt+m", "esc>m", "ctrl+c", "shift+tab", or "enter") into terminal
// input bytes.
func EncodeKeySequence(specs ...string) ([]byte, error) {
	var out []byte
	for _, spec := range specs {
		if spec == "" {
			continue
		}
		if strings.Trim(spec, " ") == "" {
			out = append(out, spec...)
			continue
		}
		for _, tok := range strings.Fields(spec) {
			for _, step := range splitKeySequence(tok) {
				b, err := encodeKeyChord(step)
				if err != nil {
					return nil, err
				}
				out = append(out, b...)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("rex: no keys")
	}
	return out, nil
}

func splitKeySequence(tok string) []string {
	if tok == ">" || strings.HasSuffix(tok, "+>") {
		return []string{tok}
	}
	var steps []string
	start := 0
	for i := 0; i < len(tok); i++ {
		if tok[i] == '>' && i > start && tok[i-1] != '+' {
			steps = append(steps, tok[start:i])
			start = i + 1
		}
	}
	steps = append(steps, tok[start:])
	return steps
}

func encodeKeyChord(chord string) ([]byte, error) {
	chord = strings.TrimSpace(chord)
	if chord == "" {
		return nil, errors.New("rex: empty key")
	}
	if chord == "+" {
		return []byte{'+'}, nil
	}

	var modParts []string
	var keyPart string
	if strings.HasSuffix(chord, "++") {
		prefix := strings.TrimSuffix(chord, "++")
		modParts = strings.Split(prefix, "+")
		keyPart = "+"
	} else {
		parts := strings.Split(chord, "+")
		if parts[len(parts)-1] == "" {
			return nil, fmt.Errorf("rex: empty key in %q", chord)
		}
		modParts = parts[:len(parts)-1]
		keyPart = parts[len(parts)-1]
	}

	var mods keyMods
	for _, rawMod := range modParts {
		switch strings.ToLower(strings.TrimSpace(rawMod)) {
		case "shift":
			mods |= keyModShift
		case "alt", "opt", "option", "meta":
			mods |= keyModAlt
		case "ctrl", "control", "ctl":
			mods |= keyModCtrl
		default:
			return nil, fmt.Errorf("rex: unknown modifier %q", rawMod)
		}
	}

	return encodeModifiedKey(keyPart, mods)
}

func encodeModifiedKey(key string, mods keyMods) ([]byte, error) {
	lower := strings.ToLower(key)
	switch lower {
	case "esc", "escape":
		return withAltPrefix([]byte{0x1b}, mods&keyModAlt != 0), nil
	case "enter", "return", "cr":
		return withAltPrefix([]byte{'\r'}, mods&keyModAlt != 0), nil
	case "lf":
		return withAltPrefix([]byte{'\n'}, mods&keyModAlt != 0), nil
	case "tab":
		if mods == keyModShift {
			return []byte("\x1b[Z"), nil
		}
		if mods&keyModShift != 0 {
			return withAltPrefix([]byte("\x1b[Z"), mods&keyModAlt != 0), nil
		}
		return withAltPrefix([]byte{'\t'}, mods&keyModAlt != 0), nil
	case "btab", "backtab":
		return []byte("\x1b[Z"), nil
	case "space":
		if mods&keyModCtrl != 0 {
			return withAltPrefix([]byte{0x00}, mods&keyModAlt != 0), nil
		}
		return withAltPrefix([]byte{' '}, mods&keyModAlt != 0), nil
	case "bs", "backspace":
		b := byte(0x7f)
		if mods&keyModCtrl != 0 {
			b = 0x08
		}
		return withAltPrefix([]byte{b}, mods&keyModAlt != 0), nil
	case "del", "delete":
		return csiTilde(3, mods), nil
	case "ins", "insert":
		return csiTilde(2, mods), nil
	case "pgup", "pageup", "page_up":
		return csiTilde(5, mods), nil
	case "pgdn", "pagedown", "page_down":
		return csiTilde(6, mods), nil
	case "up", "arrow_up":
		return csiFinal('A', mods), nil
	case "down", "arrow_down":
		return csiFinal('B', mods), nil
	case "right", "arrow_right":
		return csiFinal('C', mods), nil
	case "left", "arrow_left":
		return csiFinal('D', mods), nil
	case "home":
		return csiFinal('H', mods), nil
	case "end":
		return csiFinal('F', mods), nil
	case "f1":
		return fKeySS3('P', mods), nil
	case "f2":
		return fKeySS3('Q', mods), nil
	case "f3":
		return fKeySS3('R', mods), nil
	case "f4":
		return fKeySS3('S', mods), nil
	case "f5":
		return csiTilde(15, mods), nil
	case "f6":
		return csiTilde(17, mods), nil
	case "f7":
		return csiTilde(18, mods), nil
	case "f8":
		return csiTilde(19, mods), nil
	case "f9":
		return csiTilde(20, mods), nil
	case "f10":
		return csiTilde(21, mods), nil
	case "f11":
		return csiTilde(23, mods), nil
	case "f12":
		return csiTilde(24, mods), nil
	}

	r, size := utf8.DecodeRuneInString(key)
	if r == utf8.RuneError || size != len(key) {
		return nil, fmt.Errorf("rex: unknown key %q", key)
	}
	if mods&keyModShift != 0 && r >= 'a' && r <= 'z' {
		r = r - 'a' + 'A'
	}
	if mods&keyModCtrl != 0 {
		b, ok := ctrlByte(r)
		if !ok {
			return nil, fmt.Errorf("rex: unsupported ctrl key %q", key)
		}
		return withAltPrefix([]byte{b}, mods&keyModAlt != 0), nil
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	return withAltPrefix(buf[:n], mods&keyModAlt != 0), nil
}

func ctrlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r - 'a' + 1), true
	case r >= 'A' && r <= 'Z':
		return byte(r - 'A' + 1), true
	case r == '@' || r == '2':
		return 0x00, true
	case r == '[' || r == '3':
		return 0x1b, true
	case r == '\\' || r == '4':
		return 0x1c, true
	case r == ']' || r == '5':
		return 0x1d, true
	case r == '^' || r == '6':
		return 0x1e, true
	case r == '_' || r == '/' || r == '7':
		return 0x1f, true
	case r == '?' || r == '8':
		return 0x7f, true
	default:
		return 0, false
	}
}

func withAltPrefix(b []byte, alt bool) []byte {
	if !alt {
		return b
	}
	out := make([]byte, 0, 1+len(b))
	out = append(out, 0x1b)
	return append(out, b...)
}

func xtermModParam(mods keyMods) int {
	p := 1
	if mods&keyModShift != 0 {
		p += 1
	}
	if mods&keyModAlt != 0 {
		p += 2
	}
	if mods&keyModCtrl != 0 {
		p += 4
	}
	return p
}

func csiFinal(final byte, mods keyMods) []byte {
	if p := xtermModParam(mods); p > 1 {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", p, final))
	}
	return []byte{0x1b, '[', final}
}

func fKeySS3(final byte, mods keyMods) []byte {
	if p := xtermModParam(mods); p > 1 {
		return []byte(fmt.Sprintf("\x1b[1;%d%c", p, final))
	}
	return []byte{0x1b, 'O', final}
}

func csiTilde(code int, mods keyMods) []byte {
	if p := xtermModParam(mods); p > 1 {
		return []byte(fmt.Sprintf("\x1b[%d;%d~", code, p))
	}
	return []byte(fmt.Sprintf("\x1b[%d~", code))
}
