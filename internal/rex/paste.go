package rex

import (
	"bytes"
	"strconv"
)

// encodePaste returns text as a program reads a paste: bracketed when it
// asked for bracketed pastes (mode 2004), so that it does not run as
// commands as it would typed, else with newlines as carriage returns and
// without the control characters that could run commands, as a terminal
// pastes.
func encodePaste(text string, bracketed bool) []byte {
	if text == "" {
		return nil
	}
	if bracketed {
		out := make([]byte, 0, len(text)+12)
		out = append(out, "\x1b[200~"...)
		out = append(out, text...)
		return append(out, "\x1b[201~"...)
	}
	out := make([]byte, 0, len(text))
	for i := range len(text) {
		switch c := text[i]; {
		case c == '\r':
			// CR LF, and a CR alone, end a line once.
			if i+1 < len(text) && text[i+1] == '\n' {
				continue
			}
			out = append(out, '\r')
		case c == '\n':
			out = append(out, '\r')
		case c == '\t':
			out = append(out, c)
		case c < 0x20 || c == 0x7f:
			// A control character a paste would run: dropped.
		default:
			out = append(out, c)
		}
	}
	return out
}

// maxModeSeq bounds the part of a sequence the mode tracker carries from
// one read to the next.
const maxModeSeq = 64

// decModes tracks a program's modes from what it prints, across reads:
// only the bracketed paste mode (2004) is kept, which a paste the server
// writes needs.
type decModes struct {
	// carry is the start of a sequence a read cut, joined to the next.
	carry     []byte
	bracketed bool
}

// feed reads output for the bracketed paste mode.
func (m *decModes) feed(p []byte) {
	if len(m.carry) > 0 {
		p = append(m.carry, p...)
		m.carry = nil
	}
	for {
		i := bytes.IndexByte(p, 0x1b)
		if i < 0 {
			return
		}
		p = p[i:]
		if len(p) < 2 {
			m.carry = append([]byte(nil), p...)
			return
		}
		switch p[1] {
		case 'c': // RIS: the modes go with the screen.
			m.bracketed = false
			p = p[2:]
			continue
		case '[':
		default:
			p = p[1:]
			continue
		}
		if len(p) < 3 {
			m.carry = append([]byte(nil), p...)
			return
		}
		j := 2
		private := p[j] == '?'
		if private {
			j++
		}
		start := j
		for j < len(p) && (p[j] >= '0' && p[j] <= '9' || p[j] == ';') {
			j++
		}
		if j == len(p) {
			// Unfinished: it may be the mode's, and only if it is short.
			if len(p) <= maxModeSeq {
				m.carry = append([]byte(nil), p...)
			}
			return
		}
		if !private || (p[j] != 'h' && p[j] != 'l') {
			p = p[2:]
			continue
		}
		if hasModeParam(p[start:j], 2004) {
			m.bracketed = p[j] == 'h'
		}
		p = p[j+1:]
	}
}

// hasModeParam tells whether params, "a;b;c", names mode n.
func hasModeParam(params []byte, n int) bool {
	for _, field := range bytes.Split(params, []byte{';'}) {
		if len(field) == 0 || len(field) > 6 {
			continue
		}
		if v, err := strconv.Atoi(string(field)); err == nil && v == n {
			return true
		}
	}
	return false
}
