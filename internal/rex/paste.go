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

// trackedModes are the modes the server follows from the program's
// output, as "modes" reports them: the ones a client changes while it
// drives a terminal. Ghostty tracks more, which this server does not ask
// it for.
var trackedModes = []struct {
	name   string
	number int
	ansi   bool
	def    bool
}{
	{"insert", 4, true, false},
	{"newline_mode", 20, true, false},
	{"application_cursor_keys", 1, false, false},
	{"origin", 6, false, false},
	{"autowrap", 7, false, true},
	{"cursor_blink", 12, false, true},
	{"cursor_visible", 25, false, true},
	{"alt_screen", 47, false, false},
	{"application_keypad", 66, false, false},
	{"mouse_click", 1000, false, false},
	{"mouse_drag", 1002, false, false},
	{"mouse_motion", 1003, false, false},
	{"focus_event", 1004, false, false},
	{"mouse_sgr", 1006, false, false},
	{"mouse_urxvt", 1015, false, false},
	{"mouse_pixels", 1016, false, false},
	{"alt_screen_clear", 1047, false, false},
	{"alt_screen_save_cursor_clear_enter", 1049, false, false},
	{"bracketed_paste", 2004, false, false},
	{"sync_output", 2026, false, false},
	{"graphemes", 2027, false, false},
}

// decModes tracks a program's modes from what it prints, across reads:
// the bracketed paste mode (2004) a paste the server writes needs, and
// the modes of trackedModes, which "modes" reports.
type decModes struct {
	// carry is the start of a sequence a read cut, joined to the next.
	carry     []byte
	bracketed bool
	// set is the state of each mode of trackedModes, or nil before the
	// program set one, when it is its default.
	set []bool
}

// feed reads output for the modes it tracks.
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
			m.set = nil
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
		if p[j] != 'h' && p[j] != 'l' {
			p = p[2:]
			continue
		}
		on := p[j] == 'h'
		for _, field := range bytes.Split(p[start:j], []byte{';'}) {
			if len(field) == 0 || len(field) > 6 {
				continue
			}
			n, err := strconv.Atoi(string(field))
			if err != nil {
				continue
			}
			m.setMode(n, !private, on)
		}
		p = p[j+1:]
	}
}

// setMode sets, or resets, the mode numbered n, ANSI or DEC private,
// which the server tracks.
func (m *decModes) setMode(n int, ansi, on bool) {
	if n == 2004 && !ansi {
		m.bracketed = on
	}
	for i, md := range trackedModes {
		if md.number != n || md.ansi != ansi {
			continue
		}
		if m.set == nil {
			m.set = make([]bool, len(trackedModes))
		}
		m.set[i] = on
	}
}

// status reports the modes as "modes" shows them, and which screen shows.
func (m *decModes) status() ModeStatus {
	st := ModeStatus{Screen: "primary", Modes: make([]ModeState, 0, len(trackedModes))}
	alt := false
	for i, md := range trackedModes {
		on := md.def
		if m.set != nil {
			on = m.set[i]
		}
		if on && (md.number == 47 || md.number == 1047 || md.number == 1049) && !md.ansi {
			alt = true
		}
		st.Modes = append(st.Modes, ModeState{Name: md.name, Number: md.number, ANSI: md.ansi, Set: on, Default: md.def})
	}
	if alt {
		st.Screen = "alternate"
	}
	return st
}
