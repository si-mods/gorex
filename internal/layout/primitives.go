package layout

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func hexNibble(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func hexByte(hi, lo byte) (uint8, bool) {
	a, ok1 := hexNibble(hi)
	b, ok2 := hexNibble(lo)
	if !ok1 || !ok2 {
		return 0, false
	}
	return a<<4 | b, true
}

// ParseHexRGB parses 3-digit or 6-digit hexadecimal RGB colors ("rgb", "#rgb",
// "rrggbb", "#rrggbb") without allocations or panics.
func ParseHexRGB(s string) (r, g, b uint8, ok bool) {
	h := strings.TrimPrefix(s, "#")
	switch len(h) {
	case 3:
		r, ok1 := hexNibble(h[0])
		g, ok2 := hexNibble(h[1])
		b, ok3 := hexNibble(h[2])
		if !ok1 || !ok2 || !ok3 {
			return 0, 0, 0, false
		}
		return r<<4 | r, g<<4 | g, b<<4 | b, true
	case 6:
		r, ok1 := hexByte(h[0], h[1])
		g, ok2 := hexByte(h[2], h[3])
		b, ok3 := hexByte(h[4], h[5])
		if !ok1 || !ok2 || !ok3 {
			return 0, 0, 0, false
		}
		return r, g, b, true
	default:
		return 0, 0, 0, false
	}
}

// NormalizeHex6 validates a 3-digit or 6-digit hex color (with or without a
// leading '#') and returns the lowercase 6-digit "rrggbb" string without '#'.
func NormalizeHex6(s string) (string, bool) {
	r, g, b, ok := ParseHexRGB(s)
	if !ok {
		return "", false
	}
	var raw [3]byte
	raw[0], raw[1], raw[2] = r, g, b
	var out [6]byte
	hex.Encode(out[:], raw[:])
	return string(out[:]), true
}

// NewUUIDv4 generates a random RFC 4122 version-4 UUID in canonical lowercase
// 8-4-4-4-12 format.
func NewUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return FormatUUIDv4(b)
}

// FormatUUIDv4 sets the RFC 4122 version-4 and variant bits on b and formats it
// as a canonical lowercase 8-4-4-4-12 UUID string.
func FormatUUIDv4(b [16]byte) string {
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}

// IsUUID reports whether s is a valid 8-4-4-4-12 hexadecimal UUID.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if _, ok := hexNibble(s[i]); !ok {
				return false
			}
		}
	}
	return true
}
